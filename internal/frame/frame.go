// Package frame строит выжимку вывода команды по формату LLD DK-137.
//
// Capture запускает команду, полный вывод пишет файлом в
// .devkit/cmdout/<timestamp>-<slug>/out репозитория, а возвращает сводку с
// полями в фиксированном порядке: exit, lines_total, lines_hidden,
// significant, tail, path. Ниже порога (4K символов или 100 строк) выжимка не
// строится: вызов получает полный вывод как есть. Примитив заведён как ядро
// общего модуля каркаса (LLD DK-237) и как первая очередь серии cmdout из
// DK-264; переезжающие позже regcheck и shipctl зовут Capture подпроцессом или
// напрямую.
package frame

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

// Порог включения выжимки. Ниже вывод отдаётся как есть, на пороге или выше
// строится сводка. Порог фиксирован в коде и не вынесен во флаги: развилка по
// порогу решена в LLD DK-137, а не при каждом вызове. Длина вывода измеряется в
// символах UTF-8 (рунах), как и медиана замера DK-137, чтобы мультибайтный
// вывод не перешагивал порог по байтам.
const (
	thresholdRunes = 4096
	thresholdLines = 100
	tailLines      = 30
	significantLimit = 30
)

// Significant markers, стартовый набор из LLD DK-137. Пополняется по находкам
// регчеков и CI. Поиск по подстроке без сведения регистра: набор и так покрывает
// оба написания (error и Error:), а case-insensitive раздувало бы хвост ложными
// попаданиями в путях и именах.
var significantMarkers = []string{
	"FAIL", "--- FAIL", "error", "panic:", "Error:", "fatal:",
	"undefined", "cannot find", "not found", "Permission denied", "CONFLICT",
}

// Summary это вывод команды в одной из двух форм. Ниже порога Summarized ложь,
// заполнено только Raw и Exit; на пороге и выше Summarized истина, заполнены
// счётчики, значимые строки, хвост и путь к файлу. Path существует в обоих
// режимах, когда нашёлся корень репозитория с .devkit/cmdout: файл пишется
// всегда, путь отдаётся агенту только в выжимке.
type Summary struct {
	Exit        int
	Path        string
	Raw         string
	LinesTotal  int
	LinesHidden int
	Significant []string
	Tail        []string
	Summarized  bool
}

// Capture запускает argv в каталоге dir и строит сводку вывода. Аргументов нет
// это ошибка, а не пустой вывод: звать Capture без команды никто не должен.
func Capture(dir string, argv []string) (*Summary, error) {
	if len(argv) == 0 {
		return nil, fmt.Errorf("нужна команда для прогона")
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	exit := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			exit = ee.ExitCode()
		} else {
			return nil, fmt.Errorf("запуск %s: %v", argv[0], err)
		}
	}
	return Summarize(dir, argv, out, exit), nil
}

// Summarize строит сводку из уже готового вывода команды, не запуская её
// заново. Переиспользует writeFull и buildSummary, для потребителей, которые
// держат out и exit сами: shipctl пускает команды через exec.Command и в ошибке
// хочет выжимку вместо бывшего tail(out), regcheck тем же путём выкладывает
// второй прогон. argv уходит в slug имени каталога и в путь файла полного
// вывода; пустой argv не ошибочен (в отличие от Capture), slug падает на «cmd»:
// готовый вывод мог прийти не из команды (тело HTTP-ответа), и сводка по нему
// всё равно осмыслена. Capture сводится к запуску команды и вызову Summarize,
// поэтому состав полей и порог суммы не разъезжаются.
func Summarize(dir string, argv []string, out []byte, exit int) *Summary {
	s := &Summary{Exit: exit}
	s.Path = writeFull(dir, argv, out)
	lines := splitLines(string(out))
	if utf8.RuneCount(out) >= thresholdRunes || len(lines) >= thresholdLines {
		buildSummary(s, lines)
	} else {
		s.Raw = string(out)
	}
	return s
}

// writeFull кладёт полный вывод файлом в .devkit/cmdout/<timestamp>-<slug>/out
// корня репозитория и возвращает абсолютный путь. Не git или нет прав на запись
// не смертельны: возвращается пустой путь, выжимка строится без файла.
func writeFull(dir string, argv []string, out []byte) string {
	root, err := gitRoot(dir)
	if err != nil {
		return ""
	}
	base := filepath.Join(root, ".devkit", "cmdout")
	if err := os.MkdirAll(base, 0o755); err != nil {
		return ""
	}
	slug := slug(argv)
	now := time.Now()
	stamp := now.Format("20060102T150405")
	name := fmt.Sprintf("%s-%s", stamp, slug)
	dir2 := filepath.Join(base, name)
	// Два прогона в одну секунду с одним slug не должны бить друг друга: для
	// занятого имени добавляем порядковый суффикс. Цикл ограничен, чтобы при
	// чужой поломке (каталог существует и не наш) не крутиться бесконечно.
	for i := 0; i < 1000; i++ {
		if err := os.Mkdir(dir2, 0o755); err == nil {
			break
		} else if !os.IsExist(err) {
			return ""
		}
		name = fmt.Sprintf("%s-%d-%s", stamp, i+1, slug)
		dir2 = filepath.Join(base, name)
	}
	path := filepath.Join(dir2, "out")
	if err := os.WriteFile(path, out, 0o644); err != nil {
		return ""
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	return abs
}

// buildSummary наполняет Summary по разметке вывода: значимые строки, хвост и
// число скрытых. Significant и tail показывают пересекающиеся строки по одному
// разу, поэтому lines_hidden это lines_total минус мощность объединения по
// индексам, а не простая разность длин. Обрезанные свыше significantLimit
// значимые в объединение не входят: они не показаны и уходят в hidden вместе с
// остальным кадром, а маркер «... и ещё N» в significant не строка вывода.
func buildSummary(s *Summary, lines []string) {
	s.Summarized = true
	s.LinesTotal = len(lines)
	var sigIdx []int
	for i, l := range lines {
		if isSignificant(l) {
			sigIdx = append(sigIdx, i)
		}
	}
	shown := sigIdx
	if len(shown) > significantLimit {
		s.Significant = make([]string, 0, significantLimit+1)
		for _, idx := range sigIdx[:significantLimit] {
			s.Significant = append(s.Significant, lines[idx])
		}
		s.Significant = append(s.Significant,
			fmt.Sprintf("... и ещё %d значимых строк", len(sigIdx)-significantLimit))
		shown = sigIdx[:significantLimit]
	} else {
		s.Significant = make([]string, 0, len(shown))
		for _, idx := range sigIdx {
			s.Significant = append(s.Significant, lines[idx])
		}
	}
	tailStart := len(lines) - tailLines
	if tailStart < 0 {
		tailStart = 0
	}
	s.Tail = append([]string{}, lines[tailStart:]...)
	seen := make(map[int]bool, len(shown)+len(s.Tail))
	for _, idx := range shown {
		seen[idx] = true
	}
	for i := tailStart; i < len(lines); i++ {
		seen[i] = true
	}
	s.LinesHidden = len(lines) - len(seen)
}

// Render отдаёт сводку одной строкой на поле в порядке LLD: exit, lines_total,
// lines_hidden, significant, tail, path. Порядок фиксирован, чтобы агенту было
// просто парсить без поиска по именам. Ниже порога выжимка не строилась, и Render
// отдаёт полный вывод (Raw) с приписанным путём файла: поле path для короткого
// вывода всё равно осмысленно, полный текст уже у агента под рукой, но путь
// закрывает молчаливую потерю файла на повторный разбор.
func (s *Summary) Render() string {
	if !s.Summarized {
		var b strings.Builder
		b.WriteString(s.Raw)
		if s.Raw != "" && !strings.HasSuffix(s.Raw, "\n") {
			b.WriteByte('\n')
		}
		if s.Path != "" {
			fmt.Fprintf(&b, "path: %s\n", s.Path)
		}
		return b.String()
	}
	var b strings.Builder
	fmt.Fprintf(&b, "exit: %d\n", s.Exit)
	fmt.Fprintf(&b, "lines_total: %d\n", s.LinesTotal)
	fmt.Fprintf(&b, "lines_hidden: %d\n", s.LinesHidden)
	b.WriteString("significant:\n")
	for _, l := range s.Significant {
		fmt.Fprintln(&b, l)
	}
	b.WriteString("tail:\n")
	for _, l := range s.Tail {
		fmt.Fprintln(&b, l)
	}
	fmt.Fprintf(&b, "path: %s\n", s.Path)
	return b.String()
}

// isSignificant ловит строку с маркером ошибки из стартового набора.
func isSignificant(line string) bool {
	for _, m := range significantMarkers {
		if strings.Contains(line, m) {
			return true
		}
	}
	return false
}

// slug это базовое имя первой лексемы команды: «go test» даёт «go», полный путь
// к бинарю даёт его имя. Пустой argv сюда приходит из Summarize, когда вывод
// взят не из команды (тело HTTP-ответа), и slug падает на «cmd».
func slug(argv []string) string {
	if len(argv) == 0 {
		return "cmd"
	}
	first := argv[0]
	if i := strings.LastIndexAny(first, "/\\"); i >= 0 {
		first = first[i+1:]
	}
	if first == "" {
		first = "cmd"
	}
	return first
}

func splitLines(s string) []string {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

func gitRoot(dir string) (string, error) {
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git rev-parse: %v (%s)", err, bytes.TrimSpace(out))
	}
	return strings.TrimSpace(string(out)), nil
}
