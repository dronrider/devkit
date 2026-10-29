package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/dronrider/devkit/internal/chat"
)

func TestWakeRejectsRowChangedAfterSelection(t *testing.T) {
	root := setup(t)
	old := wakePreflight
	t.Cleanup(func() { wakePreflight = old })
	wakePreflight = func(string, string, runOpts) error { return fmt.Errorf("unexpected preflight") }
	b, err := LoadBoard(boardPath(root))
	if err != nil {
		t.Fatal(err)
	}
	row := b.find("XR-003")
	w := waiter{ID: row.ID, Class: classArm, rowLine: b.Lines[row.LineIdx], rowSection: row.Sect}
	b.Lines[row.LineIdx] = strings.Replace(b.Lines[row.LineIdx], row.Title, row.Title+" новая правка", 1)
	if err := b.Save(); err != nil {
		t.Fatal(err)
	}
	msg, ok := wakeRow(root, w, wakeOpts{})
	if ok || !strings.Contains(msg, "изменилась") {
		t.Fatalf("stale selection accepted: %v %s", ok, msg)
	}
}

func TestWakeKeepsQuestionReplacedDuringPreflight(t *testing.T) {
	root := setup(t)
	parkRow(t, root, "XR-005", "вопрос: нужна схема")
	name := chat.TaskName("XR-005")
	if err := chat.WriteAsk(root, name, chat.Ask{Task: "XR-005", Questions: []chat.Question{{Text: "старый вопрос"}}}); err != nil {
		t.Fatal(err)
	}
	old := wakePreflight
	t.Cleanup(func() { wakePreflight = old })
	wakePreflight = func(string, string, runOpts) error {
		return chat.WriteAsk(root, name, chat.Ask{Task: "XR-005", Questions: []chat.Question{{Text: "новый вопрос"}}})
	}
	msg, ok := wakeRow(root, waiter{ID: "XR-005", Class: classAsk}, wakeOpts{})
	if ok || !strings.Contains(msg, "вопрос изменился") {
		t.Fatalf("stale question accepted: %v %s", ok, msg)
	}
	ask, ok := chat.ReadAsk(chat.AskPath(root, name))
	if !ok || len(ask.Questions) != 1 || ask.Questions[0].Text != "новый вопрос" || sect(t, root, "XR-005") != SectBlocked {
		t.Fatalf("replacement lost: %+v", ask)
	}
}

func TestWakeRechecksSelectedDependency(t *testing.T) {
	root := setup(t)
	parkRow(t, root, "XR-005", "закрытие: XR-001")
	oldSources, oldPreflight := waiterSources, wakePreflight
	t.Cleanup(func() { waiterSources, wakePreflight = oldSources, oldPreflight })
	waiterSources = []waiterSource{func(string, *Board, *Archive) ([]waiter, []string) {
		return []waiter{{ID: "XR-005", Class: classClose, Dep: "XR-001", Met: true}}, nil
	}}
	preflight := 0
	wakePreflight = func(string, string, runOpts) error { preflight++; return nil }
	msg, failed, err := cmdWake(root, nil, wakeOpts{})
	if err != nil || !failed || !strings.Contains(msg, "событие предпосылки больше не выполнено") {
		t.Fatalf("stale dependency accepted: %v %v %s", failed, err, msg)
	}
	if preflight != 0 || sect(t, root, "XR-005") != SectBlocked {
		t.Fatalf("stale row started: preflight=%d", preflight)
	}
}
