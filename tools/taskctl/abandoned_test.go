package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// abandonedBoardSetup ставит доску фикстуры в свежий git-репозиторий одним
// коммитом с заданной датой. Возраст строки для пометки простоя считается
// blame (boardTimes), и без истории строки нет ни возраста, ни возврата.
// Дом подменяется, как у setup: реестр сессий читается из HOME, и настоящий
// дом машины вносил бы в тест чужие привязки.
func abandonedBoardSetup(t *testing.T, at time.Time) string {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	root := t.TempDir()
	tasks := filepath.Join(root, "docs", "tasks")
	if err := os.MkdirAll(tasks, 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		boardPath(root):                 fixtureBoard,
		archivePath(root):               fixtureArchive,
		filepath.Join(tasks, "XR-005.md"): "# XR-005\n",
	}
	for p, content := range files {
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	gitOut(t, root, "init", "-q", "-b", "main")
	gitOut(t, root, "config", "user.email", "test@test")
	gitOut(t, root, "config", "user.name", "test")
	gitOut(t, root, "add", ".")
	gitCommitDated(t, root, at, "доска")
	return root
}

// TestListMarksRowWithoutWork: регрессия DK-1417. Долг взят у диффа, носитель
// умер до первого следа работы, и строка простояла дольше срока: простой
// обязан быть виден в list рядом с возрастом, с обещанием возврата тиком. На
// старом коде пометки нет, и тест красный.
func TestListMarksRowWithoutWork(t *testing.T) {
	root := abandonedBoardSetup(t, time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC))
	old := timeNow
	defer func() { timeNow = old }()
	timeNow = func() time.Time { return time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC) } // +5 дней

	out, err := cmdList(root, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "строка не двигалась 5 дней, работы за строкой нет, тик вернёт её в очередь") {
		t.Fatalf("простой брошенной строки не виден в list:\n%s", out)
	}
}

// TestListNotesYoungRowWithoutPromise: до срока пометка называет простой, а
// обещания возврата не даёт. Строка моложе трёх суток, и тик её ещё не забрал
// бы: у взявшего есть время оставить след.
func TestListNotesYoungRowWithoutPromise(t *testing.T) {
	root := abandonedBoardSetup(t, time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC))
	old := timeNow
	defer func() { timeNow = old }()
	timeNow = func() time.Time { return time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC) } // +1 день

	out, err := cmdList(root, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "строка не двигалась 1 день, работы за строкой нет") {
		t.Fatalf("простой молодой брошенной строки не виден в list:\n%s", out)
	}
	if strings.Contains(out, "тик вернёт её в очередь") {
		t.Fatalf("молодая строка обещает возврат раньше срока:\n%s", out)
	}
}

// TestListKeepsRowWithTree: живая строка не помечается. Дерево задачи это
// след работы, и пометки простоя у строки с веткой нет в любом возрасте, а
// возраст печатается как прежде.
func TestListKeepsRowWithTree(t *testing.T) {
	root := abandonedBoardSetup(t, time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC))
	gitOut(t, root, "branch", "xr-005")
	old := timeNow
	defer func() { timeNow = old }()
	timeNow = func() time.Time { return time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC) } // +5 дней

	out, err := cmdList(root, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "строка не двигалась 5 дней") {
		t.Fatalf("возраст строки с деревом не печатается:\n%s", out)
	}
	if strings.Contains(out, "работы за строкой нет") {
		t.Fatalf("строка с деревом помечена брошенной:\n%s", out)
	}
}
