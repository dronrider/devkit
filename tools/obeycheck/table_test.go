package main

import (
	"strings"
	"testing"
)

func cellOf(green, red int) cell {
	var c cell
	for i := 0; i < green; i++ {
		c.Attempts = append(c.Attempts, attempt{Green: true, Repeat: len(c.Attempts) + 1})
	}
	for i := 0; i < red; i++ {
		c.Attempts = append(c.Attempts, attempt{Repeat: len(c.Attempts) + 1, Note: "проверка: exit status 1"})
	}
	return c
}

func TestVerdictOf(t *testing.T) {
	cases := []struct {
		name             string
		firstG, firstR   int
		secondG, secondR int
		want             string
	}{
		{"обе зелёные", 5, 0, 5, 0, verdictHolds},
		{"один провал на второй", 5, 0, 4, 1, verdictRegress},
		{"полный провал на второй", 5, 0, 0, 5, verdictRegress},
		{"красный на обеих", 0, 5, 0, 5, verdictBothRed},
		{"первая шумит, вторая держит", 3, 2, 3, 2, verdictHolds},
		{"вторая лучше первой", 2, 3, 4, 1, verdictHolds},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := verdictOf(cellOf(c.firstG, c.firstR), cellOf(c.secondG, c.secondR))
			if got != c.want {
				t.Fatalf("вердикт %q, ждал %q", got, c.want)
			}
		})
	}
}

func TestRenderTable(t *testing.T) {
	rows := []row{
		{
			Scenario: Scenario{ID: "01-doc", Title: "написать абзац документации"},
			Cells:    []cell{cellOf(5, 0), cellOf(5, 0)},
			Verdict:  verdictHolds,
		},
		{
			Scenario: Scenario{ID: "02-bug", Title: "починить баг"},
			Cells:    []cell{cellOf(5, 0), cellOf(1, 4)},
			Verdict:  verdictRegress,
		},
		{
			Scenario: Scenario{ID: "10-delegate", Title: "отдать задачу исполнителю", End: endSession},
			Skipped:  true,
			Verdict:  "пропущен, конец сессия",
		},
	}
	got := render(rows, []string{"layouts/полная", "layouts/ядро"}, 5)
	for _, want := range []string{
		"сценарий",
		"полная",
		"ядро",
		"написать абзац документации     5/5   5/5  держится",
		"починить баг                    5/5   1/5  регрессия",
		"отдать задачу исполнителю         -     -  пропущен, конец сессия",
		"итог: сценариев 3, с регрессией 1, вынос не принимается",
		"первый красный прогон каждого угла:",
		"02-bug / ядро / повтор 2",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("в таблице нет %q:\n%s", want, got)
		}
	}
}

func TestRenderCleanRun(t *testing.T) {
	rows := []row{{
		Scenario: Scenario{ID: "01-doc", Title: "написать абзац"},
		Cells:    []cell{cellOf(5, 0), cellOf(5, 0)},
		Verdict:  verdictHolds,
	}}
	got := render(rows, []string{"полная", "ядро"}, 5)
	if !strings.Contains(got, "итог: сценариев 1, регрессий нет") {
		t.Fatalf("итог чистого прогона:\n%s", got)
	}
	if strings.Contains(got, "первый красный") {
		t.Fatalf("на чистом прогоне причин красноты быть не должно:\n%s", got)
	}
}
