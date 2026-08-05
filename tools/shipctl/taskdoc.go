package main

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

// В файл задачи по RULES.board.md вкладывается реальный вывод команд, а вывод
// сплошь и рядом содержит куски чужих файлов задач: и заголовок «## Выкат», и
// строку записи, и замечание ревью. Читатели идут по строкам, поэтому такую
// цитату они приняли бы за собственную разметку файла: чужой sha попал бы в
// запись выката (и очередь посчитала бы выкаченной не ту задачу), а
// процитированное замечание без исхода отбило бы слияние.
var fenceRe = regexp.MustCompile("^(`{3,}|~{3,})")

// fenceMask помечает строки, лежащие внутри ограждённого блока, вместе с
// самими строками ограждения. Блок закрывается ограждением того же знака не
// короче открывающего и без хвоста, поэтому ``` внутри блока на ~~~ его не
// закроет. Незакрытое ограждение уводит в блок весь остаток файла, как это
// делает и разметка при отрисовке, и вторым значением возвращается номер его
// строки (нумерация с единицы, ноль значит «файл цел»): дальше по этому
// признаку читатели говорят вслух, что разделы после обрыва не прочитаны.
func fenceMask(lines []string) ([]bool, int) {
	mask := make([]bool, len(lines))
	open, at := "", 0
	for i, ln := range lines {
		t := strings.TrimLeft(ln, " \t")
		m := fenceRe.FindString(t)
		if open == "" {
			if m != "" {
				open, at, mask[i] = m, i+1, true
			}
			continue
		}
		mask[i] = true
		if m != "" && m[0] == open[0] && len(m) >= len(open) && strings.TrimSpace(t[len(m):]) == "" {
			open, at = "", 0
		}
	}
	return mask, at
}

// openFenceLine возвращает строку незакрытого ограждения, ноль у целого файла.
func openFenceLine(doc string) int {
	_, at := fenceMask(strings.Split(doc, "\n"))
	return at
}

// cutTaskFile проверяет файл задачи на оборванное ограждение. Разбор угадывать
// намерение автора не берётся (сочти незакрытый блок закрытым, и цитата с
// одним ограждением опять станет записью), но и молчать тут нельзя: за
// обрывом пропадают разом «Выкат», «Ревью» и «Сценарий проверки», а RULES.md
// («Фича доезжает до пользователя») требует, чтобы бездействие из-за кривых
// данных было видно снаружи. Файла нет значит и обрыва нет.
func cutTaskFile(root, id string) int {
	data, err := os.ReadFile(taskFilePath(root, id))
	if err != nil {
		return 0
	}
	return openFenceLine(string(data))
}

// cutTaskFileNote это та же находка словами, одинаково в status и в отказе
// merge: где оборвано и что из-за этого не прочитано.
func cutTaskFileNote(id string, at int) string {
	return fmt.Sprintf("в docs/tasks/%s.md не закрыт ограждённый блок (строка %d), всё после него читается как цитата: разделы «Выкат», «Ревью» и «Сценарий проверки» оттуда не видны",
		id, at)
}

// hasHeading говорит, есть ли в файле заголовок любого уровня с заданным
// текстом. Заголовок внутри ограждённого блока не считается: это чужой вывод,
// а не раздел нашего файла.
func hasHeading(doc, name string) bool {
	lines := strings.Split(doc, "\n")
	mask, _ := fenceMask(lines)
	for i, ln := range lines {
		if !mask[i] && strings.HasPrefix(ln, "#") && strings.Contains(ln, name) {
			return true
		}
	}
	return false
}

// sectionLines возвращает строки раздела с заданным заголовком, пропуская
// ограждённые блоки. Заголовок сравнивается по префиксу: у разделов в файлах
// задач встречается хвост вроде «## Ревью (второй круг)».
func sectionLines(doc, heading string) []string {
	lines := strings.Split(doc, "\n")
	mask, _ := fenceMask(lines)
	var out []string
	in := false
	for i, ln := range lines {
		if mask[i] {
			continue
		}
		if strings.HasPrefix(ln, "## ") {
			in = strings.HasPrefix(ln, heading)
			continue
		}
		if in {
			out = append(out, ln)
		}
	}
	return out
}
