package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Доска берётся подпроцессом taskctl list --json, а не своим разбором
// markdown: правда о формате одна и живёт в утилите (LLD DK-112, «Откуда
// сервер берёт данные»). Честная ошибка «taskctl не нашёлся» полезнее
// выживания с собственным разбором, который молча устареет.

// taskctlBin подменяется тестами.
var taskctlBin = "taskctl"

// exeDir отдаёт каталог собственного бинаря; подменяется тестами, потому что
// os.Executable у go test показывает на тестовый бинарь во временной сборке.
var exeDir = func() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	return filepath.Dir(exe)
}

// taskctlPath ищет taskctl сначала рядом с собственным бинарём, потом по
// PATH. Под launchd PATH системный (EnvironmentVariables в plist нет, бинарь
// dashboard назван полным путём), а утилиты devkit лежат одним каталогом:
// сосед по каталогу это и есть бинарь той же раскладки. PATH остаётся
// откатом для запуска из исходников и тестов. Пусто, если не нашёлся нигде.
func taskctlPath() string {
	if dir := exeDir(); dir != "" {
		p := filepath.Join(dir, taskctlBin)
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() && fi.Mode()&0o111 != 0 {
			return p
		}
	}
	if p, err := exec.LookPath(taskctlBin); err == nil {
		return p
	}
	return ""
}

// procTimeout ограничивает подпроцессы сроком: подвисший taskctl или tmux не
// должен держать горутину запроса вечно, тем более после ухода клиента.
// Переменная, чтобы тест изображал зависание без настоящего ожидания.
var procTimeout = 30 * time.Second

// runProc гоняет подпроцесс со сроком; по срыву срока процесс снимается, а
// ошибка называет срок, а не пересказывает сигнал убийства.
func runProc(name string, args ...string) ([]byte, error) {
	return runProcIn("", name, args...)
}

// runProcIn это тот же запуск с текстом на входе. Текст черновика едет так, а
// не аргументом: аргументом он проходит через разбор флагов и через стража
// подкоманд taskctl, и мысль вида «-p» или «fix» там теряется целиком.
func runProcIn(stdin, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), procTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	// Без WaitDelay Output ждёт трубу, а её после убийства процесса может
	// держать открытой его выживший потомок: срок обязан вернуть управление,
	// а не переложить вечное ожидание с процесса на трубу.
	cmd.WaitDelay = time.Second
	out, err := cmd.Output()
	if ctx.Err() != nil {
		return nil, fmt.Errorf("%s не ответил за %s и снят по сроку", name, procTimeout)
	}
	return out, err
}

// commitDocs коммитит и пушит правку доски или файла задачи: доска это общий
// источник правды, и отставший remote оборачивается конфликтами (ядро правил
// доски). Провал возвращается словами, а не кодом ошибки: правка уже на
// месте, и утилита с витком видят её и без коммита.
func commitDocs(dir, msg string, paths ...string) string {
	for _, args := range [][]string{
		append([]string{"add", "--"}, paths...),
		append([]string{"commit", "-m", msg, "--"}, paths...),
		{"push"},
	} {
		if _, err := runProc("git", append([]string{"-C", dir}, args...)...); err != nil {
			return fmt.Sprintf("запись на месте, но git %s не прошёл: %s", args[0], procErr(err))
		}
	}
	return ""
}

func taskctlMissing() string {
	if taskctlPath() == "" {
		return "taskctl не нашёлся ни рядом с бинарём дашборда, ни в PATH: доски не читаются, " +
			"поставить бинари: devkitctl update"
	}
	return ""
}

// boardJSON отдаёт доску проекта как есть, байтами ответа taskctl. Память на
// этот ответ живёт уровнем выше, в методе сервера (cache.go).
func boardJSON(dir string) (json.RawMessage, error) {
	bin := taskctlPath()
	if bin == "" {
		return nil, errors.New(taskctlMissing())
	}
	out, err := runProc(bin, "list", "--json", "-C", dir)
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && len(ee.Stderr) > 0 {
			return nil, fmt.Errorf("taskctl list --json в %s: %s", dir, strings.TrimSpace(string(ee.Stderr)))
		}
		if m := taskctlMissing(); m != "" {
			return nil, errors.New(m)
		}
		return nil, fmt.Errorf("taskctl list --json в %s: %v", dir, err)
	}
	return json.RawMessage(bytes.TrimSpace(out)), nil
}

// boardView это минимум, который сервер сам вычитывает из ответа taskctl:
// префикс для привязки tmux-сессий и счётчики секций для списка проектов.
type boardView struct {
	Prefix   string `json:"prefix"`
	Sections []struct {
		Key  string            `json:"key"`
		Rows []json.RawMessage `json:"rows"`
	} `json:"sections"`
}

func parseBoardView(raw json.RawMessage) (boardView, error) {
	var v boardView
	err := json.Unmarshal(raw, &v)
	return v, err
}

// Work это живая работа проекта: tmux-сессия goal-*/task-*, цель из реестра
// ~/.devkit/goals (цикл, который ведёт другая сессия, без tmux-сессии) либо
// интерактивная сессия человека в окне, узнанная по свежему транскрипту
// (DK-263). Session и Note заполняет только третий источник: у интерактивной
// работы есть транскрипт, а задача у неё бывает и не узнана.
type Work struct {
	ID string `json:"id"`
	// Kind говорит, о чём работа, а Via чем она видна. Вид session это работа
	// с неузнанной задачей: чем она занята, сказать нечем.
	Kind string `json:"kind"` // goal | task | session
	Via  string `json:"via"`  // tmux | registry | session
	// Title это заголовок со строки доски: им работа и подписана на экране,
	// имя сессии goal-DK-112 о занятии агента не говорит ничего (DK-255).
	// Пусто у работы, чьей строки на доске нет.
	Title   string `json:"title,omitempty"`
	Session string `json:"session,omitempty"`
	Note    string `json:"note,omitempty"`
	// Sect это ключ секции доски (in-progress, check, backlog, blocked): им
	// подписан статус работы на экране «Агенты». Пусто у работы, чьей строки на
	// доске нет.
	Sect string `json:"sect,omitempty"`
	// Started это момент начала работы в unix-секундах: экран «Агенты» считает
	// по нему, сколько она идёт. Знает его только tmux, у которого сессия
	// заведена; запись реестра и транскрипт помечены последним касанием, а не
	// первым, и время работы из них не выводится. Ноль значит «начала не
	// видно», и строка тогда остаётся без времени, а не с нулём минут.
	Started int64 `json:"started,omitempty"`
}

// liveWorks собирает работы проекта. tmux-сессии на машине общие, к проекту
// они привязываются префиксом ID с его доски; доска без префикса работ из
// tmux не получает. Реестр целей привязан корнем и добирает цели, которые
// ведёт живой чат без tmux-сессии. Третьим идут интерактивные сессии: окно
// агента у человека не заводит ни tmux-сессии, ни записи в реестре, и без
// них половина работы на машине была бы невидима.
func (s *server) liveWorks(projectPath, prefix string, board json.RawMessage) []Work {
	// Пустой список, а не null: клиент и smoke-сценарий различают «работ нет»
	// и «поля нет».
	works := []Work{}
	seen := map[string]bool{}
	busy := map[string]bool{}
	// Заголовки берутся с доски разом: работа подписывается им, а не именем
	// сессии goal-DK-112, которое о занятии агента не говорит ничего (DK-255).
	// Строки на доске может и не быть, и тогда работа остаётся при своём ID.
	rows, _ := parseBoardRows(board)
	if prefix != "" {
		// Сессии спрашиваются со временем создания (tmuxList): по нему экран
		// «Агенты» говорит, сколько работа идёт.
		for _, sess := range tmuxList() {
			for _, kind := range []string{"goal", "task"} {
				id, ok := strings.CutPrefix(sess.Name, kind+"-")
				// Производная сессия конвейера (task-DK-208_1_1786532648) с
				// prefix-проверкой не отсеивается: у неё тот же префикс доски,
				// а хвост это номер и unix-момент запуска, а не ID (DK-279).
				// goalIDRe та же проверка, что у ручек журнала и черновика.
				if !ok || !strings.HasPrefix(id, prefix+"-") || !goalIDRe.MatchString(id) {
					continue
				}
				works = append(works, Work{ID: id, Kind: kind, Title: rows[id].Title,
					Sect: rows[id].Sect, Via: "tmux", Started: sess.Created})
				seen[kind+"-"+id] = true
				busy[id] = true
			}
		}
	}
	for _, path := range globSorted(filepath.Join(s.cfg.Home, ".devkit", "goals", "*.watch")) {
		entry := readEntry(path)
		goal, root := entry["goal"], entry["root"]
		if goal == "" || root == "" || filepath.Clean(root) != filepath.Clean(projectPath) {
			continue
		}
		if seen["goal-"+goal] {
			continue
		}
		works = append(works, Work{ID: goal, Kind: "goal", Title: rows[goal].Title,
			Sect: rows[goal].Sect, Via: "registry"})
		busy[goal] = true
	}
	return append(works, s.sessionWorks(projectPath, prefix, rows, busy)...)
}

// tmuxMissingCheck называет ненайденный tmux: без него живые работы это
// молча пустой список, неотличимый от «агенты не работают», а по LLD
// («Молчание различимо») это обязаны быть разные ответы. tmux не утилита
// devkit и соседом по каталогу не лежит, PATH launchd-агенту дописывает
// devkitctl doctor --fix.
func tmuxMissingCheck() string {
	if _, err := exec.LookPath("tmux"); err != nil {
		return "tmux не нашёлся в PATH: живые работы tmux-сессий не видны, поставить tmux " +
			"или дописать PATH launchd-агенту (devkitctl doctor --fix)"
	}
	return ""
}

// tmuxSessions отдаёт одни имена: занятость задачи и поиск сессии по имени
// временем не интересуются. Ненулевой код tmux ls это штатное «сессий нет»:
// без единой сессии tmux не держит сервера и ls отвечает ошибкой, поломкой это
// не считается; ненайденный бинарь называет tmuxMissingCheck на уровне ответа,
// здесь оба случая дают пустой список.
func tmuxSessions() []string {
	out, err := runProc("tmux", "ls", "-F", "#{session_name}")
	if err != nil {
		return nil
	}
	return strings.Fields(string(out))
}

func globSorted(pattern string) []string {
	paths, _ := filepath.Glob(pattern)
	return paths
}

// readEntry читает файл «ключ = значение», как записи реестра целей.
func readEntry(path string) map[string]string {
	data := map[string]string{}
	text, err := os.ReadFile(path)
	if err != nil {
		return data
	}
	for _, ln := range strings.Split(string(text), "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" || strings.HasPrefix(ln, "#") {
			continue
		}
		if k, v, ok := strings.Cut(ln, "="); ok {
			data[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	return data
}
