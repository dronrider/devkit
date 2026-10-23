package taskform

import (
	"regexp"
	"strings"
)

// GateCut это имя ворот нарезки в пометке-исключении: «- Исключение: нарезка
// отложена (причина)». Ворота стоят на закрытии LLD-строки (DK-489), и
// откладывается у них нарезка, а не закрытие: одно слово «нарезка» читалось
// бы отказом от нарезки вовсе, а отложенная нарезка это решение дизайна
// довести кандидатов до доски позже, как у DK-1318 до пробы DK-1413.
const GateCut = "нарезка отложена"

// cutHeading это заголовок контрактного раздела LLD-документа. Кандидаты
// нарезки живут в нём, а не в решениях и не в «Что не входит» (DK-489), и имя
// уже носят живые документы: DK-972, DK-933, DK-606.
const cutHeading = "## Нарезка"

// cutGoalWord открывает строку-пометку цели: «- цель DK-490, кандидаты уехали
// в раздел Задачи цели файла цели». Слово в голове строки списка держит за
// собой пометку, и кандидат, чьё имя начинается с него, читается разбором как
// обычный незаведённый кандидат.
const cutGoalWord = "цель"

// taskIDRe узнаёт ID строки доски в голове строки кандидата: тот же вид, что
// разбирает доска, буквы латиницы или кириллицы с номером.
var taskIDRe = regexp.MustCompile(`^[A-ZА-Я]+-[0-9]+$`)

// CutRow это строка-кандидат раздела «Нарезка». Незаведённый кандидат
// начинается словом «кандидат» с номером, как в списке «Задачи цели», и его
// TaskID пуст; нарезанный несёт ID заведённой строки в голове.
type CutRow struct {
	TaskID string
	Text   string
}

// CutSection разбирает раздел «Нарезка» LLD-документа: строки-кандидаты и
// пометку цели, если кандидаты уехали в цель одним составом. Кандидатом
// считается только строка списка, той же формы, что список «Задачи цели»
// (goal-cut), а проза, таблицы и подразделы остаются вне разбора. Заголовок
// с хвостом («## Нарезка реализации») читается тем же разделом, а
// «Предварительная нарезка» и другие имена разделом не считаются.
func CutSection(doc string) (rows []CutRow, goal string, found bool) {
	lines := strings.Split(doc, "\n")
	mask, _ := FenceMask(lines)
	in := false
	for i, ln := range lines {
		if mask[i] {
			continue
		}
		if strings.HasPrefix(ln, "## ") {
			in = strings.HasPrefix(ln, cutHeading)
			if in {
				found = true
			}
			continue
		}
		if !in {
			continue
		}
		t := strings.TrimSpace(ln)
		rest, ok := strings.CutPrefix(t, "- ")
		if !ok {
			continue
		}
		if g, ok := strings.CutPrefix(rest, cutGoalWord+" "); ok {
			if id := cutHead(g); taskIDRe.MatchString(id) {
				goal = id
				continue
			}
		}
		row := CutRow{Text: rest}
		if id := cutHead(rest); taskIDRe.MatchString(id) {
			row.TaskID = id
		}
		rows = append(rows, row)
	}
	return rows, goal, found
}

// cutHead называет голову строки списка: слово до первого пробела без
// замыкающего двоеточия и запятой. В голове кандидата стоит либо ID
// заведённой строки, либо слово «кандидат» с номером.
func cutHead(s string) string {
	if i := strings.IndexAny(s, " \t"); i >= 0 {
		s = s[:i]
	}
	return strings.TrimRight(s, ":,")
}
