package spend

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func runRow() RunRow {
	return RunRow{
		ID:      "ob-1",
		Task:    "XR-005",
		Repeats: 5,
		Status:  StatusOK,
		When:    time.Date(2026, 10, 7, 12, 0, 0, 0, time.Local),
		Usage:   Usage{Turns: 12, Output: 4000, Input: 200, CacheRead: 9000, CacheWrite: 400},
	}
}

// TestWriteRunKeepsEachLaunch: журнал держит строку на каждый запуск, повтор
// своего ключа не заменяет числа прошлого (кейс 2 DoD DK-1309).
func TestWriteRunKeepsEachLaunch(t *testing.T) {
	home := t.TempDir()
	a := runRow()
	if err := WriteRun(home, a); err != nil {
		t.Fatal(err)
	}
	b := runRow()
	b.ID = "ob-2"
	b.Usage.Output = 50
	if err := WriteRun(home, b); err != nil {
		t.Fatal(err)
	}
	rows := ReadRuns(home)
	if len(rows) != 2 {
		t.Fatalf("строк журнала %d, хочу 2: %+v", len(rows), rows)
	}
	u, n := RunUsage(home, "XR-005")
	if n != 2 || u.Output != 4050 || u.CacheWrite != 800 {
		t.Fatalf("сумма журнала: %d прогонов, %+v", n, u)
	}
}

// TestWriteRunReplacesSameID: идущий прогон обновляет свою строку, а не растит
// дубли. Обрыв сохраняет собранный расход в журнале (кейс 3).
func TestWriteRunReplacesSameID(t *testing.T) {
	home := t.TempDir()
	a := runRow()
	a.Status = StatusRun
	a.Usage = Usage{Turns: 3, Output: 100}
	if err := WriteRun(home, a); err != nil {
		t.Fatal(err)
	}
	a.Status = StatusAbort
	a.Usage = Usage{Turns: 7, Output: 250, CacheWrite: 40}
	if err := WriteRun(home, a); err != nil {
		t.Fatal(err)
	}
	rows := ReadRuns(home)
	if len(rows) != 1 {
		t.Fatalf("строк %d, хочу 1: %+v", len(rows), rows)
	}
	if rows[0].Status != StatusAbort || rows[0].Usage.Output != 250 {
		t.Fatalf("строка после обрыва %+v", rows[0])
	}
	if rows[0].Credited() {
		t.Fatal("обрыв не должен идти в свод")
	}
}

// TestRunRowCredited: в свод идут только замеры с --task и повторами от трёх,
// конченые. Разведка, обрывы и запуск без --task стоят в журнале и в свод не
// идут (контракт DK-913).
func TestRunRowCredited(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*RunRow)
		want bool
	}{
		{"замер", func(*RunRow) {}, true},
		{"без задачи", func(r *RunRow) { r.Task = "" }, false},
		{"разведка", func(r *RunRow) { r.Repeats = 2 }, false},
		{"обрыв", func(r *RunRow) { r.Status = StatusAbort }, false},
		{"идёт", func(r *RunRow) { r.Status = StatusRun }, false},
	}
	for _, c := range cases {
		r := runRow()
		c.mut(&r)
		if got := r.Credited(); got != c.want {
			t.Fatalf("%s: Credited=%v, хочу %v", c.name, got, c.want)
		}
	}
}

// TestWriteRunHomeless: без дома записи нет, и это ошибка, а не молчание.
func TestWriteRunHomeless(t *testing.T) {
	if err := WriteRun("", runRow()); err == nil {
		t.Fatal("запись без дома должна отказывать")
	}
	if rows := ReadRuns(""); rows != nil {
		t.Fatalf("чтение без дома вернуло %+v", rows)
	}
}

// TestParseRunIgnoresGarbage: битая строка журнала пропускается, соседние
// читаются.
func TestParseRunIgnoresGarbage(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".devkit", runsRel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	good := formatRun(runRow())
	if err := os.WriteFile(path, []byte("битая строка\n"+good+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rows := ReadRuns(home)
	if len(rows) != 1 || rows[0].ID != "ob-1" {
		t.Fatalf("прочитано %+v", rows)
	}
}
