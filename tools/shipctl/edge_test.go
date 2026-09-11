package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// XR-001 в работе после XR-002, предпосылка лежит в Backlog стенда.
const rowInProgAfter = "| XR-001 | Починка бага [после XR-002] | bug | P1 | 55 (50+0+0+5+0) | [tasks/XR-001.md](tasks/XR-001.md) |\n"

// boardEdit правит строку доски стенда и коммитит правку отдельным коммитом
// доски: откат задачи коммиты доски не трогает, и перемешивать их с кодом
// нельзя.
func boardEdit(t *testing.T, root, from, to string) {
	t.Helper()
	p := filepath.Join(root, "docs", "TASKS.md")
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), from) {
		t.Fatalf("на доске нет %q", from)
	}
	write(t, root, "docs/TASKS.md", strings.Replace(string(data), from, to, 1))
	gitT(t, root, "add", "docs/TASKS.md")
	gitT(t, root, "commit", "-qm", "docs(tasks): правка доски стенда")
}

// DoD: shipctl merge Y при откаченной X отказывает, называет X и печатает
// готовую парковку «слияние: X». Ветка при этом в main не льётся.
func TestMergeRefusesUnliftedEdge(t *testing.T) {
	root, _ := setup(t, rowInProgAfter, "")
	codeCommit(t, root, "XR-002", "two.txt")
	gitT(t, root, "rm", "-q", "two.txt")
	gitT(t, root, "commit", "-qm", "revert: XR-002 откат")
	branchWithFix(t, root)
	_, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true"})
	if err == nil || !strings.Contains(err.Error(), "XR-002 откачена") ||
		!strings.Contains(err.Error(), `taskctl move XR-001 blocked --reason "слияние: XR-002"`) {
		t.Fatalf("ждал отказ по неснятому ребру с парковкой, а пришло: %v", err)
	}
	if log := gitT(t, root, "log", "main", "--format=%s"); strings.Contains(log, "fix: XR-001 правка") {
		t.Fatalf("отбитый merge всё же слил ветку:\n%s", log)
	}
	// Поездом тот же код идёт в main, и ворота те же.
	if _, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true", Train: true}); err == nil ||
		!strings.Contains(err.Error(), "XR-002 откачена") {
		t.Fatalf("поездное слияние прошло мимо неснятого ребра: %v", err)
	}
}

// Предпосылка вида user держит ребро до закрытия, и парковка тогда ждёт
// закрытия: разряд «слияние:» на уже слитую предпосылку отбила бы сама
// парковка, событие уже случилось.
func TestMergeUserPrereqParksOnClose(t *testing.T) {
	root, _ := setup(t, rowInProgAfter, "")
	boardEdit(t, root, "| XR-002 | Задел |", "| XR-002 | Задел [приёмка: user] |")
	codeCommit(t, root, "XR-002", "two.txt")
	branchWithFix(t, root)
	_, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true"})
	if err == nil || !strings.Contains(err.Error(), `--reason "закрытие: XR-002"`) {
		t.Fatalf("ждал парковку до закрытия предпосылки вида user, а пришло: %v", err)
	}
}

// Закрытая предпосылка ребро снимает, и слияние идёт как обычно.
func TestMergePassesClosedPrereq(t *testing.T) {
	root, _ := setup(t, rowInProgAfter, "")
	write(t, root, "docs/TASKS-archive.md", "# архив\n\n| ID | Задача | Тип | P | Закрыто | Ссылка |\n|---|---|---|---|---|---|\n| XR-002 | Задел | task | P3 | 2026-09-11 | - |\n")
	gitT(t, root, "add", "docs/TASKS-archive.md")
	gitT(t, root, "commit", "-qm", "docs(tasks): XR-002 закрыта")
	branchWithFix(t, root)
	if _, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true"}); err != nil {
		t.Fatalf("закрытая предпосылка держит слияние: %v", err)
	}
}

// DoD: shipctl revert X при слитой Y называет Y, а работу Y не трогает.
func TestRevertNamesMergedDependents(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	boardEdit(t, root, "| XR-002 | Задел |", "| XR-002 | Задел [после XR-001] |")
	codeCommit(t, root, "XR-001", "one.txt")
	codeCommit(t, root, "XR-002", "two.txt")
	msg, err := cmdRevert(root, RevertParams{ID: "XR-001", Test: "true"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "работа строк с ребром на XR-001") || !strings.Contains(msg, "XR-002 слита") {
		t.Fatalf("откат не назвал слитую зависимую:\n%s", msg)
	}
	if _, err := os.Stat(filepath.Join(root, "two.txt")); err != nil {
		t.Fatalf("откат X снял работу зависимой: %v", err)
	}
}

// Откат без зависимых молчит про рёбра.
func TestRevertWithoutDependentsIsQuiet(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	codeCommit(t, root, "XR-001", "one.txt")
	msg, err := cmdRevert(root, RevertParams{ID: "XR-001", Test: "true"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(msg, "ребром на") {
		t.Fatalf("откат без зависимых заговорил про рёбра:\n%s", msg)
	}
}
