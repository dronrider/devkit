package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPickModel(t *testing.T) {
	cases := []struct {
		name  string
		r     row
		model string
		part  string
	}{
		{"мелочь S с ясным подходом", row{Type: "task", Rank: "6 (0+3+1+0+2)", Cost: "S"}, "haiku", "мелочь"},
		{"S, но подход не выбран", row{Type: "task", Rank: "8 (0+3+3+0+2)", Cost: "S"}, "sonnet", "обычная"},
		{"обычная M", row{Type: "task", Rank: "34 (25+4+1+0+4)", Cost: "M"}, "sonnet", "обычная"},
		{"баг L", row{Type: "bug", Rank: "35 (25+0+1+5+4)", Cost: "L"}, "sonnet", "обычная"},
		{"LLD сильнее дешевизны", row{Type: "LLD", Rank: "10 (0+5+1+0+4)", Cost: "S"}, "opus", "дизайн"},
		{"неопределённость 5 это грумминг", row{Type: "task", Rank: "64 (50+6+5+0+3)", Cost: "M"}, "opus", "грумминг"},
		{"XL сначала разбить", row{Type: "task", Rank: "20 (0+10+3+0+5)", Cost: "XL"}, "opus", "разбить"},
		{"цена не оценена", row{Type: "task", Rank: "8 (0+3+1+0+4)", Cost: "-"}, "sonnet", "не оценена"},
		{"нечитаемый ранг не даёт haiku", row{Type: "task", Rank: "-", Cost: "S"}, "sonnet", "обычная"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v := pickModel(c.r)
			if v.Model != c.model {
				t.Fatalf("модель %q, жду %q", v.Model, c.model)
			}
			if !strings.Contains(v.Reason, c.part) {
				t.Fatalf("причина %q без %q", v.Reason, c.part)
			}
		})
	}
}

func TestUncertainty(t *testing.T) {
	cases := []struct {
		rank string
		want int
	}{
		{"34 (25+4+1+0+4)", 1},
		{"64 (50+6+5+0+3)", 5},
		{"-", -1},
		{"34", -1},
		{"34 (25+4+1)", -1},
		{"34 (a+b+c+d+e)", -1},
	}
	for _, c := range cases {
		if got := uncertainty(c.rank); got != c.want {
			t.Fatalf("uncertainty(%q) = %d, жду %d", c.rank, got, c.want)
		}
	}
}

const sampleBoard = `# demo: задачи (префикс T)

Проза шапки, таблицей не является.

## In progress

| ID | Задача | Тип | P | R | Цена | Ссылка |
|---|---|---|---|---|---|---|
| T-002 | фича в работе | task | P2 | 34 (25+4+1+0+4) | M | - |

## Check

Нет.

## Backlog

| ID | Задача | Тип | P | R | Цена | Ссылка |
|---|---|---|---|---|---|---|
| T-001 | мелкая правка | task | P3 | 6 (0+3+1+0+2) | S | - |
| T-003 | спайк про синхронизацию | LLD | P1 | 64 (50+6+5+0+3) | - | - |

## Blocked

Нет.
`

func writeBoard(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "docs", "TASKS.md"), []byte(sampleBoard), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestCmdPick(t *testing.T) {
	root := writeBoard(t)
	cases := []struct {
		id    string
		first string
		part  string
	}{
		{"T-001", "model: haiku", "цена S"},
		{"T-002", "model: sonnet", "неопределённость 1"},
		{"T-003", "model: opus", "дизайн"},
	}
	for _, c := range cases {
		out, err := cmdPick(root, c.id)
		if err != nil {
			t.Fatalf("pick %s: %v", c.id, err)
		}
		lines := strings.SplitN(out, "\n", 2)
		if lines[0] != c.first {
			t.Fatalf("pick %s: первая строка %q, жду %q", c.id, lines[0], c.first)
		}
		if !strings.Contains(out, c.part) {
			t.Fatalf("pick %s: в выводе нет %q: %q", c.id, c.part, out)
		}
	}
}

func TestCmdPickMissing(t *testing.T) {
	root := writeBoard(t)
	if _, err := cmdPick(root, "T-999"); err == nil || !strings.Contains(err.Error(), "нет на доске") {
		t.Fatalf("жду ошибку про отсутствие на доске, получил %v", err)
	}
}

func TestPickOnRealBoardFormat(t *testing.T) {
	// Доска, сгенерированная taskctl init: секции с пояснением «Нет.» вместо
	// таблиц не должны ронять парсер.
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	empty := "# x\n\n## In progress\n\nНет.\n\n## Backlog\n\nНет.\n"
	if err := os.WriteFile(filepath.Join(root, "docs", "TASKS.md"), []byte(empty), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdPick(root, "T-001"); err == nil {
		t.Fatal("жду ошибку на пустой доске")
	}
}
