package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// devkitDir заводит обвязку .devkit в корне: без неё замок не берётся, как не
// пишется и журнал запусков.
func devkitDir(t *testing.T, root string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, ".devkit"), 0o755); err != nil {
		t.Fatal(err)
	}
}

// TestLockRefusesSecondRun: пока замок конвейера занят, все четыре команды
// отказывают чисто и ничего не делают. Отказ проверяется на каждой: merge
// двигает main и доску, ship катит выкат, revert чинит прод, а start пишет и
// коммитит доску в основном дереве.
func TestLockRefusesSecondRun(t *testing.T) {
	root, callLog := setup(t, rowInProg, "")
	devkitDir(t, root)
	branchWithFix(t, root) // остаёмся на фичеветке: merge сливает её же
	head := gitT(t, root, "rev-parse", "main")

	unlock, err := acquireLock(root)
	if err != nil {
		t.Fatal(err)
	}

	for _, c := range []struct {
		name string
		run  func() (string, error)
	}{
		{"merge", func() (string, error) { return cmdMerge(root, MergeParams{ID: "XR-001", Test: "true"}) }},
		{"ship", func() (string, error) { return cmdShip(root, ShipParams{Deploy: "true"}) }},
		{"revert", func() (string, error) { return cmdRevert(root, RevertParams{ID: "XR-001"}) }},
		{"start", func() (string, error) { return cmdStart(root, StartParams{ID: "XR-002"}) }},
	} {
		_, err := c.run()
		if err == nil || !strings.Contains(err.Error(), "конвейер занят") {
			t.Fatalf("%s при взятом замке должен отбиваться: %v", c.name, err)
		}
	}
	if got := gitT(t, root, "rev-parse", "main"); got != head {
		t.Fatalf("отказ по замку сдвинул main: %s -> %s", head, got)
	}
	if calls, _ := os.ReadFile(callLog); len(calls) > 0 {
		t.Fatalf("отказ по замку не должен звать taskctl: %q", calls)
	}
	if n := strings.Count(gitT(t, root, "worktree", "list", "--porcelain"), "worktree "); n != 1 {
		t.Fatalf("отказ start не должен заводить worktree, деревьев %d", n)
	}

	// Замок снят: та же команда проходит. Держится он дескриптором, поэтому
	// снимается закрытием файла (и ядром при завершении процесса), а сам файл
	// остаётся лежать пустым.
	unlock()
	if _, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true"}); err != nil {
		t.Fatalf("после снятия замка merge должен проходить: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, lockPath)); err != nil {
		t.Fatalf("файл замка должен остаться на месте: %v", err)
	}
	// Замок отпущен и после самой команды, иначе следующий запуск встал бы.
	release, err := acquireLock(root)
	if err != nil {
		t.Fatalf("merge не отпустил замок: %v", err)
	}
	release()
}

// TestLockSkippedWithoutDevkit: без директории .devkit замок не берётся, и
// команды работают как раньше (в проекте без обвязки запирать нечего).
func TestLockSkippedWithoutDevkit(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	first, err := acquireLock(root)
	if err != nil {
		t.Fatal(err)
	}
	defer first()
	if _, err := acquireLock(root); err != nil {
		t.Fatalf("без .devkit второй замок не должен отказывать: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, lockPath)); !os.IsNotExist(err) {
		t.Fatalf("без .devkit файл замка заводиться не должен: %v", err)
	}
	branchWithFix(t, root)
	if _, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true"}); err != nil {
		t.Fatalf("merge без .devkit должен проходить: %v", err)
	}
}
