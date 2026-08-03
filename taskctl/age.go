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
// (shipctl/record.go): общего пакета у утилит нет, а функция крохотная.
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

// checkMarks читает файл задачи и определяет два признака строки в Check:
// держит ли она очередь выката и кто её принимает. Файла может не быть (в
// Check пускают и со ссылкой вместо файла), тогда признаки не определить, и
// это не ошибка, а честное ok=false.
func checkMarks(root, id string) (queue, agent, ok bool) {
	data, err := os.ReadFile(taskFilePath(root, id))
	if err != nil {
		return false, false, false
	}
	for _, ln := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(ln, mergedSection) {
			queue = true
		}
		if strings.HasPrefix(ln, agentScenarioSection) {
			agent = true
		}
	}
	return queue, agent, true
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

// lineAge возвращает, сколько дней назад в последний раз менялась строка
// lineIdx (0-based) файла docs/TASKS.md: дата берётся из последнего коммита,
// тронувшего именно эту строку (`git log -L`), а не даты перевода в текущий
// статус, для которой в доске нет отдельного поля.
func lineAge(root string, lineIdx int) (days int, ok bool) {
	ln := lineIdx + 1
	out, err := exec.Command("git", "-C", root, "log", "-1", "--format=%ct",
		"-L", fmt.Sprintf("%d,%d:docs/TASKS.md", ln, ln)).Output()
	if err != nil {
		return 0, false
	}
	first, _, _ := strings.Cut(string(out), "\n")
	sec, err := strconv.ParseInt(strings.TrimSpace(first), 10, 64)
	if err != nil {
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
func ageLabel(root string, lineIdx int, clean bool) string {
	if !clean {
		return ""
	}
	days, ok := lineAge(root, lineIdx)
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
