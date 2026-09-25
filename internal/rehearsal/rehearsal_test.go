package rehearsal

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dronrider/devkit/internal/taskform"
)

// doc собирает файл задачи со сценарием и заданным хвостом.
func doc(tail string) string {
	return "# XR-001\n\n## Сценарий проверки\n\n1. Запустить.\n" + tail
}

func stamp(scenario, sha string) string {
	return "\n## Проверка\n\n" + taskform.RehearsalNote + " 2026-09-24 10:00, свежее дерево " +
		sha + ", сценарий " + taskform.ScenarioPrint(scenario) + ", шагов 1, все зелёные.\n"
}

// TestMarkNeedsStamp: без отметки и без пометки-исключения ворот отказывает,
// называя команду обкатки и тот выход, с которого его позвали.
func TestMarkNeedsStamp(t *testing.T) {
	err := Mark("XR-001", doc(""), "закрытие")
	if err == nil {
		t.Fatal("файл без отметки ворот проходить не должен")
	}
	for _, want := range []string{"taskctl rehearse XR-001", "повторить закрытие"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("в отказе нет %q: %v", want, err)
		}
	}
}

// TestMarkPasses: отметка и пометка-исключение открывают ворот одинаково.
func TestMarkPasses(t *testing.T) {
	d := doc("")
	if err := Mark("XR-001", d+stamp(d, "1a2b3c4d5e6f"), "закрытие"); err != nil {
		t.Fatalf("отметка ворот не открыла: %v", err)
	}
	if err := Mark("XR-001", doc("\n- Исключение: обкатка (шаги гоняет прод)\n"), "закрытие"); err != nil {
		t.Fatalf("пометка-исключение ворот не погасила: %v", err)
	}
}

// TestFreshCatchesSwappedScenario: сценарий, переписанный после обкатки, ворот
// не открывает даже без git: отпечаток в отметке разошёлся с текстом раздела.
func TestFreshCatchesSwappedScenario(t *testing.T) {
	d := doc("")
	swapped := "# XR-001\n\n## Сценарий проверки\n\n1. Запустить другое.\n" + stamp(d, "1a2b3c4d5e6f")
	err := Fresh(t.TempDir(), "XR-001", swapped, "слияние")
	if err == nil {
		t.Fatal("подменённый сценарий ворот не отбил")
	}
	if !strings.Contains(err.Error(), "сценарий менялся после обкатки") {
		t.Fatalf("отказ не называет причину: %v", err)
	}
}

// TestFreshOutsideGit: вне git сверять коммит не с чем, и вороту довольно
// самой отметки: доска живёт и там, где код лежит отдельно от неё.
func TestFreshOutsideGit(t *testing.T) {
	d := doc("")
	if err := Fresh(t.TempDir(), "XR-001", d+stamp(d, "1a2b3c4d5e6f"), "слияние"); err != nil {
		t.Fatalf("вне git отметки должно хватать: %v", err)
	}
}

// repo заводит репозиторий с одним коммитом и отдаёт его путь.
func repo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"config", "user.name", "t"},
		{"config", "user.email", "t@t"},
	} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	return dir
}

// commit кладёт файл и коммитит его, отдавая новый HEAD.
func commit(t *testing.T, dir, rel, body string) string {
	t.Helper()
	full := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "."}, {"commit", "-qm", "правка " + rel}} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

// TestFreshSurvivesNeighbourTaskDoc: запись обкатки соседней задачи отметку не
// гасит. Поезд собирается из нескольких строк, запись каждой ложится в файл
// своей задачи, и раньше первая же такая запись числила соседа протухшим
// (DK-1161).
func TestFreshSurvivesNeighbourTaskDoc(t *testing.T) {
	dir := repo(t)
	head := commit(t, dir, "tools/x/main.go", "package main\n")
	d := doc("")
	commit(t, dir, "docs/tasks/XR-002.md", "запись обкатки соседа\n")
	if err := Fresh(dir, "XR-001", d+stamp(d, head), "слияние"); err != nil {
		t.Fatalf("коммит файла соседней задачи погасил отметку: %v", err)
	}
}

// TestFreshCatchesCodeAfterMark: код, приехавший после обкатки, ворот отбивает
// по-прежнему. Послабление про записи доски кода не касается.
func TestFreshCatchesCodeAfterMark(t *testing.T) {
	dir := repo(t)
	head := commit(t, dir, "tools/x/main.go", "package main\n")
	d := doc("")
	commit(t, dir, "tools/x/other.go", "package main\n\nfunc f() {}\n")
	err := Fresh(dir, "XR-001", d+stamp(d, head), "слияние")
	if err == nil {
		t.Fatal("код после обкатки ворот не отбил")
	}
	if !strings.Contains(err.Error(), "отметка обкатки стоит на коммите") {
		t.Fatalf("отказ не называет причину: %v", err)
	}
}

// TestFreshSurvivesBoardMove: перевод строки соседа по поезду отметку не гасит.
// Такой коммит правит доску и запись задачи вместе, и без доски послабление
// накрывало бы половину случая (замечание ревью DK-1161).
func TestFreshSurvivesBoardMove(t *testing.T) {
	dir := repo(t)
	head := commit(t, dir, "tools/x/main.go", "package main\n")
	d := doc("")
	commit(t, dir, "docs/TASKS.md", "| XR-002 |\n")
	commit(t, dir, "docs/tasks/XR-002.md", "запись соседа\n")
	if err := Fresh(dir, "XR-001", d+stamp(d, head), "слияние"); err != nil {
		t.Fatalf("перевод строки соседа погасил отметку: %v", err)
	}
}

// TestFreshCatchesScriptNextToTaskDoc: сценарный скрипт рядом с записью задачи
// это код, и отметку он гасит: по нему гоняются шаги проверки.
func TestFreshCatchesScriptNextToTaskDoc(t *testing.T) {
	dir := repo(t)
	head := commit(t, dir, "tools/x/main.go", "package main\n")
	d := doc("")
	commit(t, dir, "docs/tasks/XR-001-check.sh", "echo шаг\n")
	if err := Fresh(dir, "XR-001", d+stamp(d, head), "слияние"); err == nil {
		t.Fatal("правка сценарного скрипта ворот не отбила")
	}
}
