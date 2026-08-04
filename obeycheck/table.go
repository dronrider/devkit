package main

import (
	"fmt"
	"path/filepath"
	"strings"
)

const (
	verdictHolds   = "держится"
	verdictRegress = "регрессия"
	verdictBothRed = "красный на обеих"
)

// verdictOf применяет правило решения дизайна: вынос принимается, когда на
// второй раскладке нет ни одного провала там, где первая зелёная. Сценарий,
// красный на обеих, это не повод для выноса, а находка про само правило: текст
// в резиденте его всё равно не держит.
func verdictOf(first, second cell) string {
	a, b := first.green(), second.green()
	switch {
	case a == 0 && b == 0:
		return verdictBothRed
	case b < a:
		return verdictRegress
	default:
		return verdictHolds
	}
}

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
func render(rows []row, layouts []string, repeats int) string {
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

	var regress, bothRed int
	for _, r := range rows {
		switch r.Verdict {
		case verdictRegress:
			regress++
		case verdictBothRed:
			bothRed++
		}
	}
	b.WriteString("\n")
	switch {
	case regress > 0:
		fmt.Fprintf(&b, "итог: сценариев %d, с регрессией %d, вынос не принимается\n", len(rows), regress)
	default:
		fmt.Fprintf(&b, "итог: сценариев %d, регрессий нет\n", len(rows))
	}
	if bothRed > 0 {
		fmt.Fprintf(&b, "красных на обеих раскладках: %d, это находка про сами правила, а не повод для выноса\n", bothRed)
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
	return strings.TrimRight(b.String(), "\n")
}
