package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestFitCLI: команда зарегистрирована в main, путь цели берётся относительно
// корня доски, без --goal она отказывает, и в общей справке она есть.
func TestFitCLI(t *testing.T) {
	home := t.TempDir()
	root := writeBoard(t)
	goalFile(t, root, "T-100", goalText("бюджет: week_all <= 25\n", ""))
	cmd := exec.Command("go", "run", ".", "-C", root, "fit", "--goal", "docs/tasks/T-100.md")
	cmd.Env = append(os.Environ(), "HOME="+home, "DEVKIT_HOME="+repoRoot(t), "DEVKIT_HARNESS=")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("fit: %v\n%s", err, out)
	}
	if !strings.HasPrefix(string(out), "fit: unknown\n") {
		t.Fatalf("вывод:\n%s", out)
	}

	cmd = exec.Command("go", "run", ".", "-C", root, "fit")
	cmd.Env = append(os.Environ(), "HOME="+home, "DEVKIT_HOME="+repoRoot(t), "DEVKIT_HARNESS=")
	if out, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("fit без --goal должен отказывать:\n%s", out)
	}

	out, err = exec.Command("go", "run", ".", "--help").CombinedOutput()
	if err != nil {
		t.Fatalf("справка: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "fit --goal") {
		t.Fatalf("в общей справке нет fit:\n%s", out)
	}
}
