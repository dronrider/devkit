package main

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
)

func pad(s string, w int) string {
	n := len([]rune(s))
	if n >= w {
		return s
	}
	return s + strings.Repeat(" ", w-n)
}

func padLeft(s string, w int) string {
	n := len([]rune(s))
	if n >= w {
		return s
	}
	return strings.Repeat(" ", w-n) + s
}

// render печатает таблицу «сценарий, зелёных из k на первой раскладке, зелёных
// из k на второй, вердикт», а под ней итог и по одной строке причины на каждый
// красный угол: без причины таблица говорит, что сломалось, но не говорит, где
// смотреть.
func render(rows []row, layouts []string, repeats int, base string) string {
	head := []string{"сценарий"}
	for _, l := range layouts {
		head = append(head, filepath.Base(l))
	}
	head = append(head, "вердикт")

	table := [][]string{head}
	for _, r := range rows {
		line := []string{r.Scenario.Title}
		for i := range layouts {
			switch {
			case r.Skipped:
				line = append(line, "-")
			default:
				line = append(line, fmt.Sprintf("%d/%d", r.Cells[i].green(), repeats))
			}
		}
		table = append(table, append(line, r.Verdict))
	}
	widths := make([]int, len(head))
	for _, line := range table {
		for i, c := range line {
			if n := len([]rune(c)); n > widths[i] {
				widths[i] = n
			}
		}
	}
	var b strings.Builder
	for _, line := range table {
		var parts []string
		for i, c := range line {
			if i == 0 || i == len(line)-1 {
				parts = append(parts, pad(c, widths[i]))
			} else {
				parts = append(parts, padLeft(c, widths[i]))
			}
		}
		b.WriteString(strings.TrimRight(strings.Join(parts, "  "), " ") + "\n")
	}

	var failed []row
	for _, r := range rows {
		if !r.Skipped && !counted(r.Verdict, base) {
			failed = append(failed, r)
		}
	}
	b.WriteString("\n")
	switch {
	case len(failed) > 0:
		fmt.Fprintf(&b, "итог: сценариев %d, не зачтено %d, база %s\n", len(rows), len(failed), base)
	default:
		fmt.Fprintf(&b, "итог: сценариев %d, все зачтены, база %s\n", len(rows), base)
	}
	for _, r := range failed {
		fmt.Fprintf(&b, "  %s: %s, %s\n", r.Scenario.ID, r.Verdict, why(r.Verdict, base))
	}
	suspect := 0
	for _, r := range rows {
		for _, c := range r.Cells {
			for _, a := range c.Attempts {
				if a.Suspect {
					suspect++
				}
			}
		}
	}
	if suspect > 0 {
		fmt.Fprintf(&b, "прогонов с зелёной проверкой при упавшей команде: %d, такую зелень стоит посмотреть глазами\n", suspect)
	}

	var notes []string
	for _, r := range rows {
		if r.Skipped {
			continue
		}
		for i, c := range r.Cells {
			for _, a := range c.Attempts {
				if a.Green {
					continue
				}
				notes = append(notes, fmt.Sprintf("  %s / %s / повтор %d: %s",
					r.Scenario.ID, filepath.Base(layouts[i]), a.Repeat, a.Note))
				break
			}
		}
	}
	if len(notes) > 0 {
		b.WriteString("\nпервый красный прогон каждого угла:\n")
		b.WriteString(strings.Join(notes, "\n") + "\n")
	}
	// Разбор судьи печатается по каждой клетке: вердикт без цитаты это слово,
	// а по цитате видно, что судья прочитал, и видно это без --keep.
	var judged []string
	for _, r := range rows {
		if r.Skipped || r.Scenario.Judge == nil {
			continue
		}
		for i, c := range r.Cells {
			for _, a := range c.Attempts {
				if a.Judge == "" {
					continue
				}
				judged = append(judged, fmt.Sprintf("  %s / %s / повтор %d: %s",
					r.Scenario.ID, filepath.Base(layouts[i]), a.Repeat, a.Judge))
			}
		}
	}
	if len(judged) > 0 {
		b.WriteString("\nразбор судьи:\n")
		b.WriteString(strings.Join(judged, "\n") + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// badSymbol это тот же рубеж, что в hooks/check-symbols.py: символы вне
// клавиатурных раскладок en/ru, «ёлочек» и №. Дублируется здесь нарочно, а не
// зовётся у хука, тесту нужен собственный python3-прогон, чтобы разбор не
// разошёлся молча.
var badSymbol = regexp.MustCompile(`[^\x00-\x7Fа-яА-ЯёЁ«»№]`)

// arrowGlyph это точечные замены для стрелок, у которых есть привычный
// ASCII-аналог; их называет сам хук в подсказке «-> <- =>». Стрелка без
// точечной замены получает "->" по умолчанию (keyboardRune). Код руны собран
// числом через rune(0x...), а не буквенным символом: живой символ в
// исходнике сам не прошёл бы рубеж, а числовая форма его не заводит.
var arrowGlyph = map[rune]string{
	rune(0x2190): "<-",  // влево
	rune(0x2192): "->",  // вправо
	rune(0x2194): "<->", // в обе стороны
	rune(0x21d0): "<=",  // влево, двойная линия
	rune(0x21d2): "=>",  // вправо, двойная линия
	rune(0x21d4): "<=>", // в обе стороны, двойная линия
}

// toKeyboard приводит текст к клавиатурным символам (RULES.md, «Код и
// тексты», п. 1) там, где его нельзя переписать живой прозой: разбор судьи и
// сырой вывод команд едут в файл задачи как записаны, а не в пересказе
// (DK-1056). Разбор идёт по тем же классам, что в ADVICE хука
// check-symbols.py, и тест TestToKeyboardMatchesHook гоняет по случаю каждый
// класс через сам хук, чтобы таблицы не разошлись на следующей правке одного
// без другого. Два символа разбираются не по себе самим, а по соседям
// (ревью DK-1056): U+2019 между двумя буквами это апостроф внутри слова
// («don't»), а не закрывающая кавычка; U+200D (ZWJ) убирается только рядом с
// эмодзи, иначе он вне эмодзи-последовательности молча склеил бы соседние
// слова («wordnext» вместо видимого следа) и обычной замене на «»» или на
// «?» они не подчиняются.
func toKeyboard(s string) string {
	if !badSymbol.MatchString(s) {
		return s
	}
	rs := []rune(s)
	var b strings.Builder
	for i, r := range rs {
		switch {
		case r == rune(0x2019) && isLetterAt(rs, i-1) && isLetterAt(rs, i+1):
			b.WriteString("'")
		case r == rune(0x200d) && (isEmojiAt(rs, i-1) || isEmojiAt(rs, i+1)):
			// соединитель составной эмодзи: убрать вместе с ней.
		case r == rune(0x200d):
			// ZWJ сам по себе, вне эмодзи-соседства: клавиатурного аналога
			// нет, видимый знак вопроса лучше молчаливой склейки слов.
			b.WriteString("?")
		default:
			b.WriteString(keyboardRune(r))
		}
	}
	return b.String()
}

// isLetterAt отвечает, стоит ли на месте i буква: индекс вне текста letter не
// считается, конец и начало строки апостроф внутри слова не заводят.
func isLetterAt(rs []rune, i int) bool {
	return i >= 0 && i < len(rs) && unicode.IsLetter(rs[i])
}

// isEmojiAt отвечает, стоит ли на месте i символ из эмодзи-классов
// keyboardRune (включая селектор вариации U+FE0F: он же сосед ZWJ в составных
// эмодзи вроде сердца с изменением цвета). Индекс вне текста эмодзи не
// считается: ZWJ на границе строки соседа не находит и уходит в «?».
func isEmojiAt(rs []rune, i int) bool {
	if i < 0 || i >= len(rs) {
		return false
	}
	r := rs[i]
	return (r >= rune(0x2600) && r <= rune(0x27bf)) || r == rune(0xfe0f) || (r >= rune(0x1f000) && r <= rune(0x1faff))
}

// keyboardRune отдаёт клавиатурную замену одного символа без оглядки на
// соседей; апостроф U+2019 между буквами и ZWJ рядом с эмодзи разбирает
// toKeyboard раньше, сюда U+2019 со своим обычным разбором (закрывающая
// ёлочка) доходит только вне слова, а ZWJ сюда не доходит вовсе. Границы
// классов собраны числом через rune(0x...) по той же причине, что у
// arrowGlyph.
func keyboardRune(r rune) string {
	switch {
	case !badSymbol.MatchString(string(r)):
		return string(r)
	case r >= rune(0x2012) && r <= rune(0x2015):
		// длинное тире: дефис с пробелами, а не двоеточие или запятая, в
		// чужой цитате смысл подменять нельзя, дефис его не меняет.
		return " - "
	case r == rune(0x2018) || r == rune(0x201c) || r == rune(0x201e):
		return "«"
	case r == rune(0x2019) || r == rune(0x201a) || r == rune(0x201d):
		return "»"
	case r == rune(0x2026):
		return "..."
	case r == rune(0x00a0) || r == rune(0x2007) || r == rune(0x2009) || r == rune(0x202f):
		return " "
	case (r >= rune(0x2190) && r <= rune(0x21ff)) || (r >= rune(0x2794) && r <= rune(0x27bf)) || (r >= rune(0x2b00) && r <= rune(0x2b11)):
		if g, ok := arrowGlyph[r]; ok {
			return g
		}
		return "->"
	case (r >= rune(0x2600) && r <= rune(0x27bf)) || r == rune(0xfe0f) || (r >= rune(0x1f000) && r <= rune(0x1faff)):
		// эмодзи: убрать, замены им нет (та же подсказка, что у хука).
		return ""
	default:
		// символ вне раскладок и вне известных классов: клавиатурного
		// аналога нет, а знак вопроса на его месте виден в тексте и не
		// выдаёт себя за часть цитаты.
		return "?"
	}
}
