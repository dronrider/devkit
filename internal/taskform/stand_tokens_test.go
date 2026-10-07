package taskform

import (
	"testing"
	"time"
)

// TestStandMarkTokens: числа расхода прогона едут в отметку и читаются из неё
// обратно вместе со списком сценариев, а отметка без чисел (прогон до DK-913)
// читается нулями и в свод не идёт.
func TestStandMarkTokens(t *testing.T) {
	when := time.Date(2026, 9, 18, 13, 0, 0, 0, time.Local)
	m := StandMark{Tree: "1a2b3c4d", Print: "ab12cd34", Base: "нет", Tier: "base",
		Repeats: 3, Scenarios: []string{"41", "42"},
		Turns: 120, Output: 45210, Input: 3100, CacheRead: 2100000, CacheWrite: 40000}
	line := StandLine(m, when, "зачтён")
	got := StandMarks(line)
	if len(got) != 1 {
		t.Fatalf("отметок %d в строке %q", len(got), line)
	}
	g := got[0]
	if g.Turns != 120 || g.Output != 45210 || g.Input != 3100 || g.CacheRead != 2100000 || g.CacheWrite != 40000 {
		t.Fatalf("числа прочитались как %+v (%s)", g, line)
	}
	if !g.Tokens() {
		t.Fatalf("отметка с числами названа пустой: %s", line)
	}
	if len(g.Scenarios) != 2 || g.Scenarios[0] != "41" || g.Scenarios[1] != "42" {
		t.Fatalf("сценарии прочитались как %v (%s)", g.Scenarios, line)
	}
	if !g.When.Equal(when) {
		t.Fatalf("момент прогона %s, ждал %s", g.When, when)
	}
	old := "- Стенд: 2026-09-01 10:00, дерево 1a2b3c4d, предмет ab12cd34, база нет, ярус base, k 3, сценарии 41, зачтён."
	prev := StandMarks(old)
	if len(prev) != 1 || prev[0].Tokens() {
		t.Fatalf("отметка без чисел разобрана как %+v", prev)
	}
	if len(prev[0].Scenarios) != 1 || prev[0].Scenarios[0] != "41" {
		t.Fatalf("старая отметка потеряла сценарии: %+v", prev[0])
	}
}
