package taskform

import (
	"fmt"
	"regexp"
	"strings"
)

var taskListRe = regexp.MustCompile(`^(?:[-*+](?: +|$)|[0-9]+[.)](?: +|$))(?:\[[ xX]\] *)?`)
var rankFormulaRe = regexp.MustCompile("(?m)^[ \t]*`?[0-9]+\\s*\\+\\s*[0-9]+\\s*\\+\\s*[0-9]+\\s*\\+\\s*[0-9]+\\s*\\+\\s*[0-9]+\\s*=\\s*[0-9]+`?,?\\s+P[0-3]\\.?[ \t]*$")
var rankReasonRe = regexp.MustCompile(`(?i)^(серьёзность|серьезность|ценность|неопределённость|неопределенность|поправка на баг|баг|рычаг|влияние на команду|влияние)\s*(?:[+]?[0-9]+)?\s*[:.]\s*(.*)$`)

// ValidateTask проверяет записанную постановку task/bug. Тип и исключения
// для целей определяет вызывающий, содержание причин оценивает ревью.
func ValidateTask(doc string) error {
	doc = VisibleText(doc)
	lines := strings.Split(doc, "\n")
	mask, unclosed := FenceMask(lines)
	bodies := map[string][]string{}
	heading := ""
	for i, line := range lines {
		if !mask[i] && strings.HasPrefix(line, "## ") {
			heading = strings.TrimSpace(line)
			continue
		}
		if heading != "" {
			bodies[heading] = append(bodies[heading], line)
		}
	}
	var missing []string
	for _, h := range []string{Situation, Want, DoD, Rank} {
		if !taskText(strings.Join(bodies[h], "\n")) {
			missing = append(missing, "«"+strings.TrimPrefix(h, "## ")+"»")
		}
	}
	rank := strings.Join(bodies[Rank], "\n")
	rankMask, _ := FenceMask(bodies[Rank])
	var rankLines []string
	for i, line := range bodies[Rank] {
		if !rankMask[i] {
			rankLines = append(rankLines, line)
		}
	}
	if taskText(rank) {
		if !rankFormulaRe.MatchString(strings.Join(rankLines, "\n")) {
			missing = append(missing, "формула и P в «Ранг»")
		}
		seen := map[string]bool{}
		for _, line := range rankLines {
			line = strings.TrimSpace(taskListRe.ReplaceAllString(strings.TrimSpace(line), ""))
			line = strings.ReplaceAll(line, "**", "")
			m := rankReasonRe.FindStringSubmatch(line)
			if m == nil || !taskText(m[2]) {
				continue
			}
			name := strings.ReplaceAll(strings.ToLower(m[1]), "ё", "е")
			switch name {
			case "баг":
				name = "поправка на баг"
			case "влияние", "влияние на команду":
				name = "рычаг"
			}
			seen[name] = true
		}
		for _, name := range []string{"Серьёзность", "Ценность", "Неопределённость", "Поправка на баг", "Рычаг"} {
			if !seen[strings.ReplaceAll(strings.ToLower(name), "ё", "е")] {
				missing = append(missing, "причина «"+name+"» в «Ранг»")
			}
		}
	}
	if unclosed != 0 {
		missing = append(missing, fmt.Sprintf("закрывающее ограждение блока кода со строки %d", unclosed))
	}
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf("не заполнены: %s; заполните постановку по %s или сохраните через taskctl draft", strings.Join(missing, ", "), Doc)
}

func taskText(text string) bool {
	lines := strings.Split(text, "\n")
	mask, _ := FenceMask(lines)
	for i, line := range lines {
		line = strings.TrimSpace(line)
		if fenceRe.MatchString(line) || (!mask[i] && strings.HasPrefix(line, "#")) {
			continue
		}
		line = taskListRe.ReplaceAllString(line, "")
		line = strings.Trim(strings.TrimSpace(line), "*_` .-[]()<>:")
		switch strings.ToLower(line) {
		case "", "x", "todo", "tbd", "заполнить", "описание", "причина", "разбор корня, у бага три ответа спольски", "предмет работы, каким он виден снаружи", "конец работы сверх общего dod":
			continue
		}
		return true
	}
	return false
}

// WithRankFormula ставит вычисленную формулу рядом с причинами, сохраняя
// сами причины и вложенные примеры.
func WithRankFormula(doc, formula string) string {
	lines := strings.Split(doc, "\n")
	visible := strings.Split(VisibleText(doc), "\n")
	mask, _ := FenceMask(visible)
	for i, line := range visible {
		if mask[i] || strings.TrimSpace(line) != Rank {
			continue
		}
		end := i + 1
		for end < len(lines) && (mask[end] || !strings.HasPrefix(visible[end], "## ")) {
			end++
		}
		var body []string
		for j := i + 1; j < end; j++ {
			if !mask[j] && rankFormulaRe.MatchString(strings.TrimSpace(visible[j])) {
				continue
			}
			body = append(body, lines[j])
		}
		text := strings.Trim(strings.Join(body, "\n"), "\n")
		block := []string{Rank, "", formula}
		if text != "" {
			block = append(block, "", text)
		}
		block = append(block, "")
		out := append([]string{}, lines[:i]...)
		out = append(out, block...)
		out = append(out, lines[end:]...)
		return strings.Join(out, "\n")
	}
	return InsertSection(doc, Rank, formula)
}

// VisibleText убирает HTML-комментарии вне блоков кода, сохраняя строки:
// читатель разделов видит разметку, а писатель использует прежние смещения.
func VisibleText(doc string) string {
	lines := strings.Split(doc, "\n")
	comment, fence := false, ""
	for i, line := range lines {
		if fence != "" {
			t := strings.TrimLeft(line, " \t")
			m := fenceRe.FindString(t)
			if m != "" && m[0] == fence[0] && len(m) >= len(fence) && strings.TrimSpace(t[len(m):]) == "" {
				fence = ""
			}
			continue
		}
		var visible strings.Builder
		for {
			if comment {
				end := strings.Index(line, "-->")
				if end < 0 {
					break
				}
				line, comment = line[end+3:], false
			}

			tick := strings.IndexByte(line, '`')
			at := strings.Index(line, "<!--")
			if tick >= 0 && (at < 0 || tick < at) {
				end := tick
				for end < len(line) && line[end] == '`' {
					end++
				}
				width := end - tick
				closeAt := end
				for closeAt < len(line) {
					next := strings.IndexByte(line[closeAt:], '`')
					if next < 0 {
						closeAt = len(line)
						break
					}
					closeAt += next
					closeEnd := closeAt
					for closeEnd < len(line) && line[closeEnd] == '`' {
						closeEnd++
					}
					if closeEnd-closeAt == width {
						end = closeEnd
						break
					}
					closeAt = closeEnd
				}
				visible.WriteString(line[:end])
				line = line[end:]
				continue
			}
			if at < 0 {
				visible.WriteString(line)
				break
			}
			visible.WriteString(line[:at])
			line, comment = line[at+4:], true
		}
		lines[i] = visible.String()
		fence = fenceRe.FindString(strings.TrimLeft(lines[i], " \t"))
	}
	return strings.Join(lines, "\n")
}
