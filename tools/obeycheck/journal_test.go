package main

import (
	"testing"

	"github.com/dronrider/devkit/internal/spend"
)

// TestOpenRunJournalScoutLeavesRow: разведка (--task с повторами ниже
// MinRepeats) до сессий не доходит, а строку в журнале оставляет и в свод
// не идёт (DK-1309, DK-913).
func TestOpenRunJournalScoutLeavesRow(t *testing.T) {
	home := t.TempDir()
	j, err := openRunJournal(home, "XR-005", 1)
	if err == nil {
		t.Fatal("разведка с --task должна отказывать")
	}
	if j == nil {
		t.Fatal("отказ разведки обязан оставить журнал")
	}
	rows := spend.ReadRuns(home)
	if len(rows) != 1 {
		t.Fatalf("строк журнала %d, хочу 1: %+v", len(rows), rows)
	}
	if rows[0].Task != "XR-005" || rows[0].Repeats != 1 {
		t.Fatalf("строка разведки %+v", rows[0])
	}
	if rows[0].Status != spend.StatusAbort {
		t.Fatalf("статус разведки %q, хочу %q", rows[0].Status, spend.StatusAbort)
	}
	if rows[0].Credited() {
		t.Fatal("разведка не должна идти в свод")
	}
}

// TestOpenRunJournalMeasure: замер получает журнал без отказа.
func TestOpenRunJournalMeasure(t *testing.T) {
	home := t.TempDir()
	j, err := openRunJournal(home, "XR-005", 3)
	if err != nil {
		t.Fatal(err)
	}
	if j == nil {
		t.Fatal("замер должен получить журнал")
	}
	j.ok(spend.Usage{Turns: 2, Output: 10})
	rows := spend.ReadRuns(home)
	if len(rows) != 1 || !rows[0].Credited() {
		t.Fatalf("строка замера %+v", rows)
	}
}
