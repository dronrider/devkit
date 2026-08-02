package main

import (
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
// делает и разметка при отрисовке.
func fenceMask(lines []string) []bool {
	mask := make([]bool, len(lines))
	open := ""
	for i, ln := range lines {
		t := strings.TrimLeft(ln, " \t")
		m := fenceRe.FindString(t)
		if open == "" {
			if m != "" {
				open, mask[i] = m, true
			}
			continue
		}
		mask[i] = true
		if m != "" && m[0] == open[0] && len(m) >= len(open) && strings.TrimSpace(t[len(m):]) == "" {
			open = ""
		}
	}
	return mask
}

// hasHeading говорит, есть ли в файле заголовок любого уровня с заданным
// текстом. Заголовок внутри ограждённого блока не считается: это чужой вывод,
// а не раздел нашего файла.
func hasHeading(doc, name string) bool {
	lines := strings.Split(doc, "\n")
	mask := fenceMask(lines)
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
	mask := fenceMask(lines)
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
