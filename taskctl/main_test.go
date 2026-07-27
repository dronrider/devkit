package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGlobalDirParse(t *testing.T) {
	cases := []struct {
		in   []string
		dir  string
		rest []string
	}{
		{[]string{"-C", "/x", "lint"}, "/x", []string{"lint"}},
		{[]string{"--C", "/x", "close", "XR-1"}, "/x", []string{"close", "XR-1"}},
		{[]string{"-C=/x", "sort"}, "/x", []string{"sort"}},
		{[]string{"--C=/x", "id"}, "/x", []string{"id"}},
		{[]string{"lint"}, "", []string{"lint"}},
		{[]string{"close", "-C", "/x", "XR-1"}, "", []string{"close", "-C", "/x", "XR-1"}},
	}
	for _, c := range cases {
		dir, rest, err := globalDir(c.in)
		if err != nil {
			t.Fatalf("%v: %v", c.in, err)
		}
		if dir != c.dir || strings.Join(rest, " ") != strings.Join(c.rest, " ") {
			t.Errorf("%v: получил dir=%q rest=%v", c.in, dir, rest)
		}
	}
	if _, _, err := globalDir([]string{"-C"}); err == nil {
		t.Error("-C без значения должен падать с ошибкой")
	}
}

// TestRunLog: запуск оставляет строку в журнале .devkit/log (успех и ошибка с
// кодом выхода), а без директории .devkit журнал не заводится.
func TestRunLog(t *testing.T) {
	root := setup(t)
	if err := os.Mkdir(filepath.Join(root, ".devkit"), 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("go", "run", ".", "-C", root, "lint").CombinedOutput(); err != nil {
		t.Fatalf("lint: %v\n%s", err, out)
	}
	exec.Command("go", "run", ".", "-C", root, "show", "XR-404").Run()
	data, err := os.ReadFile(filepath.Join(root, ".devkit", "log"))
	if err != nil {
		t.Fatalf("журнал не записан: %v", err)
	}
	if !strings.Contains(string(data), "\ttaskctl\tlint\t0\n") ||
		!strings.Contains(string(data), "\ttaskctl\tshow\t1\n") {
		t.Fatalf("строки журнала: %q", data)
	}

	root2 := setup(t)
	if out, err := exec.Command("go", "run", ".", "-C", root2, "lint").CombinedOutput(); err != nil {
		t.Fatalf("lint без .devkit: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(root2, ".devkit", "log")); err == nil {
		t.Fatal("журнал не должен заводиться без .devkit")
	}
}

// TestHelpAfterSubcommand: -h/--help работает у любой подкоманды, включая
// команды с позиционными аргументами, где флаг раньше принимался за ID.
func TestHelpAfterSubcommand(t *testing.T) {
	root := setup(t)
	for _, args := range [][]string{
		{"show", "--help"},
		{"move", "XR-005", "-h"},
		{"review", "add", "--help"},
		{"add", "--help"},
	} {
		out, err := exec.Command("go", append([]string{"run", ".", "-C", root}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("%v: справка по подкоманде упала: %v\n%s", args, err, out)
		}
		if !strings.Contains(string(out), "taskctl: механика канбан-доски") {
			t.Fatalf("%v: вместо справки: %s", args, out)
		}
	}
}

func TestGlobalDirBeforeCommand(t *testing.T) {
	root := setup(t)
	out, err := exec.Command("go", "run", ".", "-C", root, "lint").CombinedOutput()
	if err != nil {
		t.Fatalf("-C перед командой отбит: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "доска и архив в порядке") {
		t.Fatalf("неожиданный вывод: %s", out)
	}
}
