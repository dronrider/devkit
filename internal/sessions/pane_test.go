package sessions

import (
	"strings"
	"testing"
	"time"
)

// Адрес панели (DK-931) читается своим ключом и не прилипает к соседям: имя
// tmux и родитель остаются собой.
func TestParseLineReadsPane(t *testing.T) {
	_, b, ok := ParseLine("2026-09-11T12:00:00 сессия aaa-1 задача DK-931 проект devkit " +
		"дерево /x транскрипт - источник дерево повод startup tmux - панель %12 родитель -")
	if !ok || b.Pane != "%12" || b.Tmux != "" || b.Parent != "" {
		t.Fatalf("разобрано %+v", b)
	}
}

func TestLineWritesPaneAndLastKeepsIt(t *testing.T) {
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.Local)
	line := Line(now, "aaa-1", Bind{Task: "DK-931", Tmux: "task-DK-931", Pane: "%9"}, "taskctl run")
	if !strings.Contains(line, " tmux task-DK-931 панель %9\n") {
		t.Fatalf("строка %q", line)
	}
	_, first, _ := ParseLine(line)
	_, work, _ := ParseLine(Line(now.Add(time.Minute), "aaa-1", Bind{Task: "DK-931", Source: BySrc}, "taskctl move"))
	if got := Last([]Bind{first, work}); got.Pane != "%9" || got.Tmux != "task-DK-931" {
		t.Fatalf("запись по факту работы стёрла адрес окна: %+v", got)
	}
}

func TestPaneOwnerTakesTheFreshestClaim(t *testing.T) {
	recs := map[string][]Bind{
		"old": {{Pane: "%3", Time: "2026-09-11T10:00:00"}},
		"new": {{Pane: "%3", Time: "2026-09-11T11:00:00"}},
		"far": {{Pane: "%4", Time: "2026-09-11T12:00:00"}},
	}
	if got := PaneOwner(recs, "%3"); got != "new" {
		t.Fatalf("хозяин панели %%3: %q", got)
	}
	if got := PaneOwner(recs, ""); got != "" {
		t.Fatalf("пустой адрес назвал хозяина %q", got)
	}
}
