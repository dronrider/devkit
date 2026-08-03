package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// gitCommitDated коммитит застейдженное с фиксированной датой автора и
// коммитера: возраст строки считается по времени коммита, и тест обязан его
// контролировать, а не полагаться на момент прогона.
func gitCommitDated(t *testing.T, root string, when time.Time, msg string) {
	t.Helper()
	cmd := exec.Command("git", "-C", root, "commit", "-q", "-m", msg)
	ts := when.UTC().Format("2006-01-02T15:04:05Z")
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_DATE="+ts, "GIT_COMMITTER_DATE="+ts)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v\n%s", err, out)
	}
}

func TestPluralDays(t *testing.T) {
	cases := map[int]string{
		0: "дней", 1: "день", 2: "дня", 3: "дня", 4: "дня", 5: "дней",
		10: "дней", 11: "дней", 12: "дней", 14: "дней", 21: "день", 22: "дня", 25: "дней",
		101: "день", 102: "дня", 111: "дней",
	}
	for n, want := range cases {
		if got := pluralDays(n); got != want {
			t.Errorf("pluralDays(%d) = %q, ожидал %q", n, got, want)
		}
	}
}

func TestCheckMarks(t *testing.T) {
	root := t.TempDir()
	tasks := filepath.Join(root, "docs", "tasks")
	if err := os.MkdirAll(tasks, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(id, body string) {
		if err := os.WriteFile(filepath.Join(tasks, id+".md"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("XR-010", "# XR-010\n\n## Выкат\n\n- 2026-01-01 слито: 1234567\n\n## Сценарий проверки (агентский)\n\nшаги\n")
	write("XR-011", "# XR-011\n\n## Сценарий проверки\n\nшаги\n")
	write("XR-012", "# XR-012\n\n## Сценарий проверки (агентский)\n\nшаги\n")
	write("XR-013", "# XR-013\n\n## Выкат\n\n- 2026-01-01 слито: 1234567\n\n## Сценарий проверки\n\nшаги\n")

	cases := []struct {
		id        string
		wantQueue bool
		wantAgent bool
		wantOK    bool
		wantLabel string
	}{
		{"XR-010", true, true, true, "код слит, сценарий агентский"},
		{"XR-011", false, false, true, "без выката, сценарий пользовательский"},
		{"XR-012", false, true, true, "без выката, сценарий агентский"},
		{"XR-013", true, false, true, "код слит, сценарий пользовательский"},
		{"XR-404", false, false, false, ""},
	}
	for _, c := range cases {
		queue, agent, ok := checkMarks(root, c.id)
		if queue != c.wantQueue || agent != c.wantAgent || ok != c.wantOK {
			t.Errorf("checkMarks(%s) = %v,%v,%v; ожидал %v,%v,%v", c.id, queue, agent, ok, c.wantQueue, c.wantAgent, c.wantOK)
		}
		if got := checkMarkLabel(root, c.id); got != c.wantLabel {
			t.Errorf("checkMarkLabel(%s) = %q, ожидал %q", c.id, got, c.wantLabel)
		}
	}
}

// simpleGitBoard заводит минимальный docs/TASKS.md (без разбора Board, для
// lineAge важны только номера строк файла) и коммитит его.
func simpleGitBoard(t *testing.T, root string, lines []string, at time.Time) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(boardPath(root), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitOut(t, root, "init", "-q", "-b", "main")
	gitOut(t, root, "config", "user.email", "test@test")
	gitOut(t, root, "config", "user.name", "test")
	gitOut(t, root, "add", "docs/TASKS.md")
	gitCommitDated(t, root, at, "init")
}

func TestLineAge(t *testing.T) {
	root := t.TempDir()
	t0 := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	simpleGitBoard(t, root, []string{"a", "b", "c"}, t0)

	// Правим только строку 2, коммит с другой датой: строка 1 и 3 не тронуты.
	t1 := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	if err := os.WriteFile(boardPath(root), []byte("a\nB\nc\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitOut(t, root, "add", "docs/TASKS.md")
	gitCommitDated(t, root, t1, "правка строки 2")

	old := timeNow
	defer func() { timeNow = old }()

	// 10 дней после последней правки строки 2, с запасом в час против
	// плавающей точки на границе суток.
	timeNow = func() time.Time { return t1.Add(10*24*time.Hour + time.Hour) }
	days, ok := lineAge(root, 1) // 0-based индекс строки "B"
	if !ok || days != 10 {
		t.Fatalf("lineAge(строка 2) = %d,%v; ожидал 10,true", days, ok)
	}

	// Строку 1 в последний раз трогал самый первый коммит (t0), а не правка t1.
	timeNow = func() time.Time { return t0.Add(30*24*time.Hour + time.Hour) }
	days, ok = lineAge(root, 0)
	if !ok || days != 30 {
		t.Fatalf("lineAge(строка 1) = %d,%v; ожидал 30,true", days, ok)
	}
}

func TestLineAgeSkipsDirtyBoard(t *testing.T) {
	root := t.TempDir()
	t0 := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	simpleGitBoard(t, root, []string{"a", "b", "c"}, t0)

	if boardClean(root) != true {
		t.Fatal("свежий коммит должен считаться чистым деревом")
	}
	if ageLabel(root, 0, true) == "" {
		t.Fatal("на чистом дереве возраст обязан считаться")
	}

	// Незакоммиченная правка board.md: номера строк рабочей копии и HEAD
	// могут разъехаться, возраст в этот момент лучше не печатать вовсе.
	if err := os.WriteFile(boardPath(root), []byte("a\nb\nc\nd\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if boardClean(root) {
		t.Fatal("грязное дерево не должно считаться чистым")
	}
	if got := ageLabel(root, 0, boardClean(root)); got != "" {
		t.Fatalf("на грязном дереве ageLabel должен молчать, получил %q", got)
	}
}

func TestLineAgeNoGitRepo(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(boardPath(root), []byte("a\nb\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if boardClean(root) {
		t.Fatal("вне git-репозитория дерево не может считаться чистым")
	}
	if got := ageLabel(root, 0, boardClean(root)); got != "" {
		t.Fatalf("вне git-репозитория ageLabel должен молчать, получил %q", got)
	}
}
