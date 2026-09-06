package main

import (
	"fmt"
	"path/filepath"
	"strings"
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
