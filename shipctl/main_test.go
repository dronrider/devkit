package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestRunLog: запуск оставляет строку в журнале .devkit/log, без директории
// .devkit журнал не заводится.
func TestRunLog(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	if err := os.Mkdir(filepath.Join(root, ".devkit"), 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("go", "run", ".", "-C", root, "status").CombinedOutput(); err != nil {
		t.Fatalf("status: %v\n%s", err, out)
	}
	data, err := os.ReadFile(filepath.Join(root, ".devkit", "log"))
	if err != nil {
		t.Fatalf("журнал не записан: %v", err)
	}
	if !strings.Contains(string(data), "\tshipctl\tstatus\t0\n") {
		t.Fatalf("строки журнала: %q", data)
	}

	root2, _ := setup(t, rowInProg, "")
	if out, err := exec.Command("go", "run", ".", "-C", root2, "status").CombinedOutput(); err != nil {
		t.Fatalf("status без .devkit: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(root2, ".devkit", "log")); err == nil {
		t.Fatal("журнал не должен заводиться без .devkit")
	}
}

// TestHelpAfterSubcommand: -h/--help работает у любой подкоманды, до поиска
// корня и доски: «merge --help» раньше отбивался как merge без ID.
func TestHelpAfterSubcommand(t *testing.T) {
	for _, args := range [][]string{
		{"merge", "--help"},
		{"revert", "-h"},
		{"status", "--help"},
		{"ship", "--help"},
	} {
		out, err := exec.Command("go", append([]string{"run", "."}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("%v: справка по подкоманде упала: %v\n%s", args, err, out)
		}
		if !strings.Contains(string(out), "shipctl: слияние и откат задач") {
			t.Fatalf("%v: вместо справки: %s", args, out)
		}
	}
}
