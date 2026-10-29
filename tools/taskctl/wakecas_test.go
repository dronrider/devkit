package main

import (
	"fmt"
	"strings"
	"testing"
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
