package spend

// Журнал запусков стенда. Сессии прогона живут во временном доме, и дом
// сносится по концу вместе с журналами харнеса: своду читать после этого
// нечего. Числа снимаются с домов до сноса и дописываются сюда строкой на
// каждый запуск, с ID запуска, задачей и расходом (DK-1309). Транскрипты в
// журнал не едут: тела токенов остались бы в нём навсегда, а снос дома от
// этой утечки DK-231 спасал как раз тем, что журнала не оставлял.

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// MinRepeats это нижняя граница повторов, при которой прогон считается
// замером, а не разведкой. Значение общее для стенда и свода: разойдись оно,
// статья «стенд» считала бы другой набор запусков, чем тот, что пишет след.
const MinRepeats = 3

// Статусы запуска в журнале. «run» это идущий прогон: строка обновляется по
// мере набора чисел и переживает обрыв. «ok» это конченый прогон, «abort»
// остановленный: ошибка, потолок времени, сигнал. В свод идут только «ok»
// с задачей и повторами от MinRepeats.
const (
	StatusRun   = "run"
	StatusOK    = "ok"
	StatusAbort = "abort"
)

// runsRel это путь журнала внутри ~/.devkit.
const runsRel = "obeycheck-runs.tsv"

// RunRow это строка журнала запусков стенда.
type RunRow struct {
	ID      string    // ID запуска, уникален на машине
	Task    string    // ID задачи из --task, пусто без него
	Repeats int       // повторов на раскладку, из -k
	Status  string    // run, ok или abort
	When    time.Time // момент записи
	Usage   Usage     // расход сессий, снятый до сноса временного дома
}

// Credited отвечает, идёт ли запуск в свод статьёй «стенд». Замер это прогон
// с --task и повторами от MinRepeats; разведка, обрывы и запуск без --task
// стоят в журнале и в свод не идут (DK-913, DK-1309). Признак производный:
// колонка в строке журнала это свёртка для читателя, а не второе хранилище.
func (r RunRow) Credited() bool {
	return r.Task != "" && r.Repeats >= MinRepeats && r.Status == StatusOK
}

// RunsPath это путь журнала запусков в доме.
func RunsPath(home string) string {
	if home == "" {
		return ""
	}
	return filepath.Join(home, ".devkit", runsRel)
}

// WriteRun кладёт строку журнала, заменяя прошлую с тем же ID запуска.
// Повторный прогон своего ключа в файле задачи отметку заменяет, а строки
// журнала живут рядом: у каждого запуска своя, и повтор не стирает числа
// прошлого. Запись идёт под flock: два одновременных прогона читают и
// переписывают весь файл, и без замка строка одного затирает строку другого.
// Провал записи ронять прогон не должен: расход уже собран, и потеря строки
// журнала хуже потери запуска, но не смертельна для свода.
func WriteRun(home string, r RunRow) error {
	path := RunsPath(home)
	if path == "" {
		return fmt.Errorf("журнал запусков: дома нет")
	}
	if r.ID == "" {
		return fmt.Errorf("журнал запусков: пустой ID запуска")
	}
	if r.When.IsZero() {
		r.When = time.Now()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	data, err := io.ReadAll(f)
	if err != nil {
		return err
	}
	line := formatRun(r)
	var out []string
	found := false
	for _, ln := range strings.Split(string(data), "\n") {
		if ln == "" {
			continue
		}
		if runIDOf(ln) == r.ID {
			out = append(out, line)
			found = true
			continue
		}
		out = append(out, ln)
	}
	if !found {
		out = append(out, line)
	}
	body := strings.Join(out, "\n") + "\n"
	if err := f.Truncate(0); err != nil {
		return err
	}
	if _, err := f.Seek(0, 0); err != nil {
		return err
	}
	_, err = f.WriteString(body)
	return err
}

// ReadRuns читает журнал запусков. Битая строка пропускается молча: журнал
// ведёт стенд, и падать из-за чужой правки файла нечему.
func ReadRuns(home string) []RunRow {
	path := RunsPath(home)
	if path == "" {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []RunRow
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if r, ok := parseRun(sc.Text()); ok {
			out = append(out, r)
		}
	}
	return out
}

// CreditedRuns достаёт зачтённые строки журнала с непустым расходом. Строка
// без чисел (прогон, из которого снять было нечего) в сумму и в счёт не
// входит. Фильтр один на журнал: его зовут и сход статьи «стенд», и суммы
// прогонов, иначе две стороны разъедутся при правке одной из них.
func CreditedRuns(home, task string) []RunRow {
	var out []RunRow
	for _, r := range ReadRuns(home) {
		if task != "" && r.Task != task {
			continue
		}
		if !r.Credited() || r.Usage.Empty() {
			continue
		}
		out = append(out, r)
	}
	return out
}

// RunUsage складывает расход зачтённых запусков задачи. Числа идут ровно те,
// что лежат в журнале: сход статьи «стенд» с суммой журнала держится на
// одном источнике.
func RunUsage(home, task string) (Usage, int) {
	var out Usage
	rows := CreditedRuns(home, task)
	for _, r := range rows {
		out = out.Add(r.Usage)
	}
	return out, len(rows)
}

// formatRun собирает строку журнала. Разделитель это таб: числа и ID не
// содержат его, а пробел живёт в датах.
func formatRun(r RunRow) string {
	credited := "0"
	if r.Credited() {
		credited = "1"
	}
	return strings.Join([]string{
		r.When.Format(time.RFC3339),
		r.ID,
		r.Task,
		strconv.Itoa(r.Repeats),
		r.Status,
		credited,
		strconv.Itoa(r.Usage.Turns),
		strconv.Itoa(r.Usage.Output),
		strconv.Itoa(r.Usage.Input),
		strconv.Itoa(r.Usage.CacheRead),
		strconv.Itoa(r.Usage.CacheWrite),
	}, "\t")
}

// parseRun разбирает строку журнала.
func parseRun(line string) (RunRow, bool) {
	parts := strings.Split(line, "\t")
	if len(parts) != 11 {
		return RunRow{}, false
	}
	when, err := time.Parse(time.RFC3339, parts[0])
	if err != nil {
		return RunRow{}, false
	}
	repeats, err := strconv.Atoi(parts[3])
	if err != nil || repeats < 0 {
		return RunRow{}, false
	}
	var nums [5]int
	for i := range nums {
		n, err := strconv.Atoi(parts[6+i])
		if err != nil || n < 0 {
			return RunRow{}, false
		}
		nums[i] = n
	}
	return RunRow{
		ID:      parts[1],
		Task:    parts[2],
		Repeats: repeats,
		Status:  parts[4],
		When:    when,
		Usage: Usage{
			Turns:      nums[0],
			Output:     nums[1],
			Input:      nums[2],
			CacheRead:  nums[3],
			CacheWrite: nums[4],
		},
	}, true
}

// runIDOf достаёт ID запуска из строки, не разбирая её целиком.
func runIDOf(line string) string {
	parts := strings.Split(line, "\t")
	if len(parts) < 2 {
		return ""
	}
	return parts[1]
}
