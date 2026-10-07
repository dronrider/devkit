package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
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

// TestRunJournalWriteErrorVisible: отказ записи журнала печатается в stderr, а
// финальный статус называет его повторно. Молчаливая потеря строки журнала
// это тот же класс, что задача чинит: прогон выходит нулём, расхода в своде
// нет, наружу ничего не видно.
func TestRunJournalWriteErrorVisible(t *testing.T) {
	home := t.TempDir()
	// Каталог на месте файла журнала роняет запись строки.
	if err := os.MkdirAll(filepath.Dir(spend.RunsPath(home)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(spend.RunsPath(home), 0o755); err != nil {
		t.Fatal(err)
	}
	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	j := startRunJournal(home, "XR-005", 3)
	j.ok(spend.Usage{Turns: 2, Output: 10})
	w.Close()
	os.Stderr = old
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "журнал запусков") {
		t.Fatalf("отказ записи не дошёл до stderr: %q", out)
	}
	if strings.Count(out, "журнал запусков") < 2 {
		t.Fatalf("финальный статус не назвал ошибку повторно: %q", out)
	}
}
