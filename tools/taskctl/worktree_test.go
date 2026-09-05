package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// addWorktree заводит линкованный worktree рядом с основным чекаутом root и
// возвращает его путь.
func addWorktree(t *testing.T, root, branch string) string {
	t.Helper()
	wt := filepath.Join(t.TempDir(), "wt")
	gitOut(t, root, "worktree", "add", "-b", branch, wt)
	return wt
}

// buildTaskctl собирает бинарь taskctl во временный файл: интеграционному
// тесту нужен процесс с чужим рабочим каталогом (worktree задачи), а «go run»
// там не находит go.mod пакета.
func buildTaskctl(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "taskctl")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("go build taskctl: %v\n%s", err, out)
	}
	return bin
}

// TestBoardGuardRefusesFromWorktree проверяет весь список изменяющих доску
// команд (RULES.board.md, «Доска в руках диспетчера»): из линкованного
// worktree каждая отказывает и не трогает docs/TASKS.md.
func TestBoardGuardRefusesFromWorktree(t *testing.T) {
	root := setup(t)
	gitSetup(t, root)
	wt := addWorktree(t, root, "dk-052-guard")

	before, err := os.ReadFile(boardPath(wt))
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		run  func() (string, error)
	}{
		{"add", func() (string, error) {
			return cmdAdd(wt, AddParams{Title: "Из worktree", Type: "task", Rank: "0+1+1+0+1", Link: "x", Accept: "agent"})
		}},
		{"move", func() (string, error) {
			return cmdMove(wt, "XR-004", SectInProgress, "", CommitOpts{})
		}},
		{"set", func() (string, error) {
			return cmdSet(wt, SetParams{ID: "XR-004", Title: "Хвост, новый"})
		}},
		{"close", func() (string, error) {
			return cmdClose(wt, CloseParams{ID: "XR-005"})
		}},
		{"sort", func() (string, error) { return cmdSort(wt, CommitOpts{}) }},
		{"dep add", func() (string, error) {
			return cmdDepAdd(wt, DepParams{ID: "XR-004", DepID: "XR-002"})
		}},
		{"dep rm", func() (string, error) {
			return cmdDepRm(wt, DepParams{ID: "XR-004", DepID: "XR-002"})
		}},
		{"file", func() (string, error) { return cmdFile(wt, "XR-001", CommitOpts{}) }},
		// draft строку доски не пишет, но номер берёт из той же сквозной
		// нумерации: заведённый на фичеветке черновик основному чекауту не
		// виден, и тот же ID уйдёт следующей задаче.
		{"draft", func() (string, error) { return cmdDraft(wt, "идея из worktree", "mid", CommitOpts{}) }},
		// Исходы разбора стоят под рубежом вместе с draft и по той же причине:
		// накопитель общий и живёт в основном чекауте, а пометка, поставленная
		// на фичеветке, основному чекауту не видна до слияния.
		{"draft defer", func() (string, error) {
			return cmdDraftDefer(wt, "XR-008", "причина из worktree", false, CommitOpts{})
		}},
		{"draft attach", func() (string, error) { return cmdDraftAttach(wt, "XR-008", "XR-002", CommitOpts{}) }},
		{"draft drop", func() (string, error) { return cmdDraftDrop(wt, "XR-008", "причина", CommitOpts{}) }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := c.run()
			if err == nil {
				t.Fatalf("%s из worktree должен отказывать", c.name)
			}
			if !strings.Contains(err.Error(), "линкован") {
				t.Fatalf("сообщение не про worktree: %v", err)
			}
		})
	}

	after, err := os.ReadFile(boardPath(wt))
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("файл доски в worktree тронут отказавшей командой")
	}
	if _, err := os.Stat(draftsDir(wt)); !os.IsNotExist(err) {
		t.Fatalf("отказавший draft завёл накопитель в worktree: %v", err)
	}
}

// TestBoardGuardRefusesInitFromWorktree: init считает root тем же способом
// (git rev-parse --show-toplevel), и рубеж обязан сработать раньше, чем
// команда создаст доску.
func TestBoardGuardRefusesInitFromWorktree(t *testing.T) {
	root := t.TempDir()
	gitOut(t, root, "init", "-q", "-b", "main")
	gitOut(t, root, "config", "user.email", "test@test")
	gitOut(t, root, "config", "user.name", "test")
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitOut(t, root, "add", ".")
	gitOut(t, root, "commit", "-q", "-m", "init")
	wt := addWorktree(t, root, "dk-052-init")

	if _, err := cmdInit(wt, InitParams{Prefix: "XR"}); err == nil {
		t.Fatal("init из worktree должен отказывать")
	} else if !strings.Contains(err.Error(), "линкован") {
		t.Fatalf("сообщение не про worktree: %v", err)
	}
	if _, err := os.Stat(boardPath(wt)); !os.IsNotExist(err) {
		t.Fatalf("init из worktree создал доску: %v", err)
	}
}

// TestBoardGuardFollowsRootNotCwd это путь shipctl merge: taskctl запускается
// с cwd в дереве задачи (worktree), а доску двигает явным -C на основной
// чекаут. Рубеж считается по -C (root), а не по текущей директории, поэтому
// один и тот же процесс с cwd=worktree проходит с -C на основной чекаут и
// отказывает с -C на сам worktree.
func TestBoardGuardFollowsRootNotCwd(t *testing.T) {
	root := setup(t)
	gitSetup(t, root)
	wt := addWorktree(t, root, "dk-052-cwd")
	bin := buildTaskctl(t)

	run := func(dashC string) (string, error) {
		cmd := exec.Command(bin, "-C", dashC, "move", "XR-004", "in-progress")
		cmd.Dir = wt
		out, err := cmd.CombinedOutput()
		return string(out), err
	}

	if out, err := run(root); err != nil {
		t.Fatalf("-C на основной чекаут из worktree должен проходить: %v\n%s", err, out)
	}
	out, err := run(wt)
	if err == nil {
		t.Fatalf("-C на worktree должен отказывать\n%s", out)
	}
	if !strings.Contains(out, "линкован") {
		t.Fatalf("сообщение не про worktree: %s", out)
	}
}

// TestBoardGuardSilentOutsideGit: вне git-репозитория (и при недоступном git)
// проверка молчит, иначе временные директории без git (а таких большинство
// тестов) ловили бы отказ на ровном месте.
func TestBoardGuardSilentOutsideGit(t *testing.T) {
	root := setup(t)
	if _, err := cmdMove(root, "XR-004", SectInProgress, "", CommitOpts{}); err != nil {
		t.Fatalf("вне git-репозитория move не должен отбиваться рубежом: %v", err)
	}
	if err := boardGuard(root, "move"); err != nil {
		t.Fatalf("boardGuard вне git-репозитория должен молчать: %v", err)
	}
}

// TestReviewAddFromWorktreeWritesOnlyTaskFile: review add остаётся разрешён
// из worktree (в отличие от изменяющих доску команд), но не чинит ссылку в
// строке доски, а печатает, что её нужно поправить в основном чекауте.
func TestReviewAddFromWorktreeWritesOnlyTaskFile(t *testing.T) {
	root := setup(t)
	gitSetup(t, root)
	wt := addWorktree(t, root, "dk-052-review")

	boardBefore, err := os.ReadFile(boardPath(wt))
	if err != nil {
		t.Fatal(err)
	}
	// Файл задачи снимаем в самом worktree: review add заведёт его сам.
	dropTaskFile(t, wt, "XR-001")
	msg, err := cmdReviewAdd(wt, "XR-001", "замечание из worktree", "", CommitOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "файл задачи создан") {
		t.Fatalf("сообщение: %q", msg)
	}
	if !strings.Contains(msg, "поправь в основном чекауте") {
		t.Fatalf("нет подсказки про основной чекаут: %q", msg)
	}
	data, err := os.ReadFile(taskFileAbs(wt, "XR-001"))
	if err != nil || !strings.Contains(string(data), "- замечание из worktree") {
		t.Fatalf("файл задачи: %q, %v", data, err)
	}
	boardAfter, err := os.ReadFile(boardPath(wt))
	if err != nil {
		t.Fatal(err)
	}
	if string(boardAfter) != string(boardBefore) {
		t.Fatal("review add из worktree тронул доску")
	}
}
