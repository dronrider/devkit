package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dronrider/devkit/internal/taskhead"
)

// runDevkit раскладывает дерево devkit стенда: оболочка и профиль. Сама
// лестница проверена в internal/taskhead, тут проверяется обвязка команды.
func runDevkit(t *testing.T) (root, home, dk string) {
	t.Helper()
	base := t.TempDir()
	root, home, dk = filepath.Join(base, "proj"), filepath.Join(base, "home"), filepath.Join(base, "devkit")
	for _, p := range []string{root, home, filepath.Join(dk, "kit", "harness"), filepath.Join(dk, "kit", "skills", "board-task")} {
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	os.WriteFile(filepath.Join(dk, filepath.FromSlash(taskhead.TaskRunRel)), []byte(""), 0o644)
	os.WriteFile(taskhead.ProfilePath(dk, "claude-code"), []byte("[head]\nclient = [\"claude\"]\n"), 0o644)
	t.Setenv("DEVKIT_HOME", dk)
	t.Setenv("HOME", home)
	return root, home, dk
}

func TestRunRequestPicksHarness(t *testing.T) {
	root, home, dk := runDevkit(t)
	t.Setenv(runHarnessEnv, "")
	q, err := runRequest(root, home, "dk-7", runOpts{model: "opus", hidden: true})
	if err != nil {
		t.Fatal(err)
	}
	if q.ID != "DK-7" || q.Harness != runDefaultHarness || q.Devkit != dk || q.Project != "proj" ||
		q.Model != "opus" || !q.Hidden {
		t.Fatalf("заказ %+v", q)
	}
	t.Setenv(runHarnessEnv, "glm-code")
	if q, _ := runRequest(root, home, "DK-7", runOpts{}); q.Harness != "glm-code" {
		t.Fatalf("переменная харнеса не прочитана: %s", q.Harness)
	}
	if q, _ := runRequest(root, home, "DK-7", runOpts{harness: "x"}); q.Harness != "x" {
		t.Fatalf("флаг харнеса не старше переменной: %s", q.Harness)
	}
}

func TestRunRequestRefusals(t *testing.T) {
	root, home, _ := runDevkit(t)
	if _, err := runRequest(root, home, "не-ид", runOpts{}); err == nil {
		t.Fatal("кривой ID принят")
	}
	t.Setenv(taskhead.AdoptEnv, "полминуты")
	if _, err := runRequest(root, home, "DK-7", runOpts{}); err == nil || !strings.Contains(err.Error(), taskhead.AdoptEnv) {
		t.Fatalf("кривой срок передачи замка: %v", err)
	}
	t.Setenv(taskhead.AdoptEnv, "")
	t.Setenv("DEVKIT_HOME", filepath.Join(home, "нет"))
	if _, err := runRequest(filepath.Join(home, "далеко", "proj"), home, "DK-7", runOpts{}); err == nil ||
		!strings.Contains(err.Error(), "DEVKIT_HOME") {
		t.Fatalf("без дерева devkit: %v", err)
	}
}

// Занятый замок доходит до команды кодом 3 и словами про владельца.
func TestCmdRunBusyLock(t *testing.T) {
	root, home, _ := runDevkit(t)
	sleep := exec.Command("/bin/sleep", "30")
	if err := sleep.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sleep.Process.Kill(); sleep.Wait() })
	if err := taskhead.Take(taskhead.LockPath(home, "DK-7"), sleep.Process.Pid); err != nil {
		t.Fatal(err)
	}
	out, code, err := cmdRun(root, "DK-7", runOpts{})
	if err != nil || code != taskhead.CodeBusy || !strings.Contains(out, "уже поднята") {
		t.Fatalf("код %d, ошибка %v, вывод:\n%s", code, err, out)
	}
}
