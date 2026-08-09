package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// taskFilePath собирает путь к файлу задачи так же, как это делает shipctl
// (tools/shipctl/record.go): общего пакета у утилит нет, а функция крохотная.
func taskFilePath(root, id string) string {
	return filepath.Join(root, "docs", "tasks", id+".md")
}

// mergedSection это заголовок раздела «Выкат», который дописывает shipctl
// после слияния кода задачи. Для пометки строки в Check довольно самого
// факта, что раздел есть: точный состав выката и его состояние остаются за
// `shipctl status`, здесь только один бит «код слит».
const mergedSection = "## Выкат"

// agentScenarioSection это заголовок, которым сценарий проверки уже
// помечает себя агентским в девяти закрытых задачах; заводить отдельное
// поле ради разбора не стали, заголовок и так под ревью вместе с веткой.
const agentScenarioSection = "## Сценарий проверки (агентский)"

// scenarioSection это заголовок сценария проверки, без которого задачу не
// пускают в Check (RULES.board.md, «Трекинг задач» п. 6). Проверка по
// префиксу, поэтому агентский вариант заголовка тоже считается сценарием.
const scenarioSection = "## Сценарий проверки"

// taskDocSections читает файл задачи и говорит, какие из перечисленных
// заголовков в нём стоят. Файла может не быть (в Check пускают и со ссылкой вместо файла),
// тогда разбирать нечего, и это не ошибка, а честное ok=false.
func taskDocSections(root, id string, want ...string) (found []bool, ok bool) {
	found = make([]bool, len(want))
	data, err := os.ReadFile(taskFilePath(root, id))
	if err != nil {
		return found, false
	}
	// Заголовок внутри ограждённого блока это чужой вывод, а не раздел этого
	// файла: агентский сценарий по RULES.board.md вкладывает в файл реальный
	// вывод, а в нём сплошь и рядом попадается и «## Выкат», и заголовок
	// сценария соседней задачи. Построчный разбор принял бы цитату за свою
	// разметку и напечатал бы «код слит» задаче без выката. Тем же лечится
	// разбор в shipctl (tools/shipctl/taskdoc.go), общего пакета у утилит нет.
	fence := ""
	for _, ln := range strings.Split(string(data), "\n") {
		if m := fenceRe.FindStringSubmatch(ln); m != nil {
			switch {
			case fence == "":
				fence = m[1]
			case m[1][0] == fence[0] && len(m[1]) >= len(fence) && strings.TrimSpace(ln[len(m[0]):]) == "":
				fence = ""
			}
			continue
		}
		if fence != "" {
			continue
		}
		for i, w := range want {
			if strings.HasPrefix(ln, w) {
				found[i] = true
			}
		}
	}
	return found, true
}

// checkMarks определяет два признака строки в Check: держит ли она очередь
// выката и кто её принимает.
func checkMarks(root, id string) (queue, agent, ok bool) {
	f, ok := taskDocSections(root, id, mergedSection, agentScenarioSection)
	return f[0], f[1], ok
}

// hasScenario отвечает, готов ли у задачи сценарий проверки в её файле.
func hasScenario(root, id string) (has, hasFile bool) {
	f, ok := taskDocSections(root, id, scenarioSection)
	return f[0], ok
}

// checkMarkLabel собирает признаки в одну строку человеку: держит ли строка
// очередь выката и кто её проверяет. Оба значения печатаются всегда, даже
// когда оба «нет»: это и есть ответ на вопрос «чего ждут от владельца».
func checkMarkLabel(root, id string) string {
	queue, agent, ok := checkMarks(root, id)
	if !ok {
		return ""
	}
	q := "без выката"
	if queue {
		q = "код слит"
	}
	a := "сценарий пользовательский"
	if agent {
		a = "сценарий агентский"
	}
	return q + ", " + a
}

// timeNow подменяется тестами, чтобы возраст строки считался от известного
// момента, а не от настоящих часов.
var timeNow = time.Now

// boardClean говорит, есть ли у docs/TASKS.md незакоммиченные правки. `git
// log -L` смотрит на историю коммитов и берёт номера строк из HEAD, а не из
// рабочей копии: на грязном дереве они разъедутся, и возраст достанется не
// той строке. Отсутствие git-репозитория тоже считается «грязным»: тогда
// возраст просто не печатается, а не врёт.
func boardClean(root string) bool {
	out, err := exec.Command("git", "-C", root, "status", "--porcelain", "--", "docs/TASKS.md").Output()
	return err == nil && strings.TrimSpace(string(out)) == ""
}

// boardTimes возвращает время последней правки каждой строки docs/TASKS.md
// (ключ это номер строки с нуля, значение unix-секунды). Один `git blame` на
// всю доску, а не `git log -L` на строку: обход доски это самая частая
// команда, а запуск git на каждую строку стоил на живой доске секунды против
// сотых. Режим --incremental вчетверо дешевле построчного порцелана (0.13с
// против 0.53с на доске devkit), потому что не тащит с собой содержимое строк.
// Дата тут не про перевод в статус, а про последнюю правку строки:
// отдельного поля под дату перевода в доске нет.
func boardTimes(root string) map[int]int64 {
	out, err := exec.Command("git", "-C", root, "blame", "--incremental",
		"--", "docs/TASKS.md").Output()
	if err != nil {
		return nil
	}
	type span struct {
		sha   string
		start int // номер первой строки порции, с единицы
		count int
	}
	var spans []span
	times := map[string]int64{}
	cur := ""
	for _, ln := range strings.Split(string(out), "\n") {
		// Заголовок порции: <sha> <строка в исходном файле> <строка в
		// текущем> <сколько строк>. Метаданные коммита идут следом и только
		// у первой его порции, поэтому время держим по sha.
		if f := strings.Fields(ln); len(f) == 4 && len(f[0]) >= 7 && isHex(f[0]) {
			start, err1 := strconv.Atoi(f[2])
			count, err2 := strconv.Atoi(f[3])
			if err1 == nil && err2 == nil {
				cur = f[0]
				spans = append(spans, span{cur, start, count})
			}
			continue
		}
		if !strings.HasPrefix(ln, "author-time ") || cur == "" {
			continue
		}
		if sec, err := strconv.ParseInt(strings.TrimSpace(strings.TrimPrefix(ln, "author-time ")), 10, 64); err == nil {
			times[cur] = sec
		}
	}
	byLine := map[int]int64{}
	for _, sp := range spans {
		sec, ok := times[sp.sha]
		if !ok {
			continue
		}
		for i := 0; i < sp.count; i++ {
			byLine[sp.start-1+i] = sec
		}
	}
	return byLine
}

// isHex отличает заголовок порции blame от полей: у заголовка первым идёт sha.
func isHex(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return len(s) > 0
}

// lineAge переводит время правки строки в дни.
func lineAge(times map[int]int64, lineIdx int) (days int, ok bool) {
	sec, ok := times[lineIdx]
	if !ok {
		return 0, false
	}
	d := int(timeNow().Sub(time.Unix(sec, 0)) / (24 * time.Hour))
	if d < 0 {
		d = 0
	}
	return d, true
}

// ageLabel собирает возраст строки в человеческую подпись, либо пустую
// строку, когда возраст не посчитать (грязное дерево, нет git, строка ещё
// не попадала в коммит).
func ageLabel(times map[int]int64, lineIdx int, clean bool) string {
	if !clean {
		return ""
	}
	days, ok := lineAge(times, lineIdx)
	if !ok {
		return ""
	}
	return fmt.Sprintf("строка не двигалась %d %s", days, pluralDays(days))
}

// pluralDays склоняет «день» по числу дней.
func pluralDays(n int) string {
	n = n % 100
	if n >= 11 && n <= 14 {
		return "дней"
	}
	switch n % 10 {
	case 1:
		return "день"
	case 2, 3, 4:
		return "дня"
	default:
		return "дней"
	}
}
