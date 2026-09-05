package main

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Класс причины возврата это машинная часть ответа на вопрос, почему работа
// вернулась: делали не то, делали не по правилам или сделали то и так, но
// сломанным. Причина прозой отвечает на него же, но по доске её не сложить, а
// цель DK-565 меряет себя долей возвратов по постановке. Отсюда закрытый
// список из трёх слов и две машинные записи под него: строка «Ход работы» у
// провала проверки и маркер в голове замечания ревью.
const (
	retSpec = "постановка"
	retRule = "правила"
	retImpl = "реализация"
	// retUnnamed стоит в записи провала, вызванного без класса: событие
	// остаётся видимым и считается отдельно, а в долю по постановке не идёт.
	// Своим ключом его не поставить, это метка пропуска, а не четвёртый класс.
	retUnnamed = "неназван"
)

// returnClasses это закрытый список классов в порядке печати сводки.
var returnClasses = []string{retSpec, retRule, retImpl}

// returnClassWhat объясняет каждый класс одной строкой: перечень допустимых
// значений в отказе без объяснения ставится наугад, а разница между «не то» и
// «не так» решает, что править, постановку или правила.
var returnClassWhat = map[string]string{
	retSpec: "делали не то: расхождение с постановкой, предмет понят иначе",
	retRule: "делали не по правилам: тесты, дока, стиль, доводка до пользователя",
	retImpl: "делали то и по правилам, но сломанным: баг, недосмотр, регрессия",
}

// checkReturnClass пускает только слово из списка. Пустое значение это не
// ошибка: ключ необязателен, и его отсутствие каждая команда трактует сама.
func checkReturnClass(class string) error {
	class = strings.TrimSpace(class)
	if class == "" {
		return nil
	}
	for _, c := range returnClasses {
		if c == class {
			return nil
		}
	}
	var lines []string
	for _, c := range returnClasses {
		lines = append(lines, fmt.Sprintf("%s (%s)", c, returnClassWhat[c]))
	}
	return fmt.Errorf("класс возврата %q не из списка, жду одно из: %s", class, strings.Join(lines, "; "))
}

// returnClassFlagHelp это подсказка ключа --class у обеих команд: список
// короткий, и держать его в двух строках справки значило бы разойтись.
func returnClassFlagHelp() string {
	return "класс причины возврата: " + strings.Join(returnClasses, " | ")
}

// retSource это откуда пришёл возврат: проверка после выката или ревью.
const (
	retFromCheck  = "проверка"
	retFromReview = "ревью"
)

// retDateLayout это дата записи возврата. Окно сводки режется по ней, поэтому
// дата стоит в самой записи, а не берётся из времени файла.
const retDateLayout = "2006-01-02"

// returnStageLine это запись возврата в разделе «Ход работы» файла задачи:
// «- Возврат: постановка, проверка, 2026-09-05, прод падает на старте.»
// Место выбрано тем же, где живут прочие машинные отметки этапов (TASKFORM.md,
// «Ход работы»): признак провала в строке доски снимается починкой, а мерять
// надо случившееся, а не висящее сейчас.
func returnStageLine(class, source, reason string, now time.Time) string {
	if class == "" {
		class = retUnnamed
	}
	line := fmt.Sprintf("- Возврат: %s, %s, %s", class, source, now.Format(retDateLayout))
	if reason = strings.TrimSpace(reason); reason != "" {
		line += ", " + reason
	}
	return line + "."
}

// returnNoteMark это маркер класса в голове замечания ревью: «- [возврат:
// постановка, 2026-09-05] суть». Класс замечания живёт при самом замечании, а
// не отдельной строкой этапов: замечаний у ревью бывает с десяток, и десять
// строк журнала на один заход ревьювера читать нечем.
func returnNoteMark(class string, now time.Time) string {
	if class == "" {
		return ""
	}
	return fmt.Sprintf("[возврат: %s, %s] ", class, now.Format(retDateLayout))
}

// returnStageRe и returnNoteRe разбирают обе записи. Класс ловится любым
// словом, а не перечислением: запись, сделанная версией с другим списком,
// должна попасть в сводку строкой «класс не из списка», а не пропасть молча.
var (
	returnStageRe = regexp.MustCompile(`^\s*-\s*Возврат:\s*([^,]+),\s*([^,]+),\s*(\d{4}-\d{2}-\d{2})`)
	returnNoteRe  = regexp.MustCompile(`^\[возврат:\s*([^,\]]+)(?:,\s*(\d{4}-\d{2}-\d{2}))?\]\s*`)
)

// returnEvent это одно событие возврата: чья задача, какой класс, откуда и
// когда. Сводка считает события, а не задачи: одна задача возвращается и
// дважды, и причины у возвратов бывают разные.
type returnEvent struct {
	ID     string
	Class  string
	Source string
	Date   string
}

// named отвечает, назван ли класс события. Событие без класса в долю не идёт
// ни числителем, ни знаменателем, и печатается сводкой отдельной строкой.
func (e returnEvent) named() bool {
	for _, c := range returnClasses {
		if c == e.Class {
			return true
		}
	}
	return false
}

// returnAfter отвечает, попадает ли событие в окно с даты since. Запись без
// даты (её оставила рука или прежняя версия команды) в окно не попадает: место
// события во времени неизвестно, и приписывать его окну значило бы врать.
func returnAfter(e returnEvent, since time.Time) bool {
	if e.Date == "" {
		return false
	}
	d, err := time.Parse(retDateLayout, e.Date)
	if err != nil {
		return false
	}
	return !d.Before(since)
}

// cutText называет срез сводки первой строкой: без него доля читается как доля
// по всей доске, а она посчитана по окну и цели.
func cutText(cut StatsCut) string {
	var parts []string
	if cut.Goal != "" {
		parts = append(parts, "цель "+cut.Goal)
	}
	if cut.Since != "" {
		parts = append(parts, "окно с "+cut.Since)
	}
	if len(parts) == 0 {
		return ""
	}
	return "срез: " + strings.Join(parts, ", ")
}

// returnsText это блок сводки про возвраты: раскладка по классам, доля по
// постановке и счёт возвратов, чей класс не назвали. Доля считается от
// названных классов: возврат без класса не говорит ни за, ни против
// постановки, и растворять его в знаменателе значит занижать цифру.
func returnsText(events []returnEvent) []string {
	if len(events) == 0 {
		return []string{"возвратов с классом причины пока нет"}
	}
	byClass := map[string]int{}
	named, unnamed, other := 0, 0, 0
	for _, e := range events {
		switch {
		case e.named():
			byClass[e.Class]++
			named++
		case e.Class == retUnnamed || e.Class == "":
			unnamed++
		default:
			other++
		}
	}
	var parts []string
	for _, c := range returnClasses {
		parts = append(parts, fmt.Sprintf("%s %d", c, byClass[c]))
	}
	out := []string{fmt.Sprintf("возвраты с классом: %d (%s)", named, strings.Join(parts, ", "))}
	if named > 0 {
		out = append(out, fmt.Sprintf("возвраты по постановке: %d из %d, %d%%",
			byClass[retSpec], named, byClass[retSpec]*100/named))
	}
	if unnamed > 0 {
		out = append(out, fmt.Sprintf("возвратов без класса: %d, в долю они не идут", unnamed))
	}
	if other > 0 {
		out = append(out, fmt.Sprintf("записей с классом не из списка: %d", other))
	}
	return out
}

// goalMembership возвращает проверку принадлежности файла задачи цели: строка
// «Цель: ...» в самом файле либо упоминание в разделе «Задачи цели» файла цели.
// Двух источников тут не для запаса: строка связи стоит у задачи, заведённой
// под цель, а нарезка кладёт состав в файл цели, и задача, приписанная к цели
// позже, есть только во втором месте. Пустая цель значит «без среза», и тогда
// проверки нет вовсе.
func goalMembership(root, goal string) func(id string, lines []string) bool {
	if goal == "" {
		return nil
	}
	listed := map[string]bool{}
	for _, id := range goalTaskIDs(root, goal) {
		listed[id] = true
	}
	return func(id string, lines []string) bool {
		if id == goal || listed[id] {
			return true
		}
		for _, ln := range lines {
			s := strings.TrimSpace(ln)
			if strings.HasPrefix(s, "Цель:") {
				return goalKey(s) == goal
			}
			if strings.HasPrefix(s, "## ") {
				return false
			}
		}
		return false
	}
}

// returnsIn собирает события возврата из строк файла задачи: записи этапов и
// маркеры замечаний. Разбор идёт по строкам файла, а не по разобранному
// разделу «Ревью», чтобы одна и та же сводка читала и живой файл, и архивный.
func returnsIn(id string, lines []string) []returnEvent {
	var out []returnEvent
	for _, ln := range lines {
		if m := returnStageRe.FindStringSubmatch(ln); m != nil {
			out = append(out, returnEvent{ID: id, Class: strings.TrimSpace(m[1]),
				Source: strings.TrimSpace(m[2]), Date: m[3]})
			continue
		}
		t := strings.TrimSpace(ln)
		if !strings.HasPrefix(t, "- ") && !strings.HasPrefix(t, "* ") {
			continue
		}
		if m := returnNoteRe.FindStringSubmatch(strings.TrimSpace(t[2:])); m != nil {
			out = append(out, returnEvent{ID: id, Class: strings.TrimSpace(m[1]),
				Source: retFromReview, Date: m[2]})
		}
	}
	return out
}
