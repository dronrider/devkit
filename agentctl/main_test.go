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
		args []string
		dir  string
		rest string
	}{
		{[]string{"pick", "T-001"}, "", "pick T-001"},
		{[]string{"-C", "/tmp/x", "pick", "T-001"}, "/tmp/x", "pick T-001"},
		{[]string{"-C=/tmp/x", "pick", "T-001"}, "/tmp/x", "pick T-001"},
	}
	for _, c := range cases {
		dir, rest, err := globalDir(c.args)
		if err != nil {
			t.Fatalf("%v: %v", c.args, err)
		}
		if dir != c.dir || strings.Join(rest, " ") != c.rest {
			t.Fatalf("%v: dir=%q rest=%q", c.args, dir, rest)
		}
	}
	if _, _, err := globalDir([]string{"-C"}); err == nil {
		t.Fatal("жду ошибку на -C без значения")
	}
}

// TestRunLog: запуск оставляет строку в журнале .devkit/log, без директории
// .devkit журнал не заводится.
func TestRunLog(t *testing.T) {
	root := writeBoard(t)
	if err := os.Mkdir(filepath.Join(root, ".devkit"), 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("go", "run", ".", "-C", root, "pick", "T-001").CombinedOutput(); err != nil {
		t.Fatalf("pick: %v\n%s", err, out)
	}
	data, err := os.ReadFile(filepath.Join(root, ".devkit", "log"))
	if err != nil {
		t.Fatalf("журнал не записан: %v", err)
	}
	if !strings.Contains(string(data), "\tagentctl\tpick\t0\n") {
		t.Fatalf("строки журнала: %q", data)
	}

	root2 := writeBoard(t)
	if out, err := exec.Command("go", "run", ".", "-C", root2, "pick", "T-001").CombinedOutput(); err != nil {
		t.Fatalf("pick без .devkit: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(root2, ".devkit", "log")); err == nil {
		t.Fatal("журнал не должен заводиться без .devkit")
	}
}

// TestHelpAfterSubcommand: -h/--help работает у подкоманды до поиска корня
// и доски, как у соседних инструментов.
func TestHelpAfterSubcommand(t *testing.T) {
	for _, args := range [][]string{
		{"pick", "--help"},
		{"pick", "-h"},
	} {
		out, err := exec.Command("go", append([]string{"run", "."}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("%v: справка по подкоманде упала: %v\n%s", args, err, out)
		}
		if !strings.Contains(string(out), "agentctl: выбор модели") {
			t.Fatalf("%v: вместо справки: %s", args, out)
		}
	}
}

// TestOldSixColumnBoard: доска без колонки «Цена» не превращается в «нет на
// доске», цена трактуется как неоценённая.
func TestOldSixColumnBoard(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	old := `# x

## Backlog

| ID | Задача | Тип | P | R | Ссылка |
|---|---|---|---|---|---|
| T-001 | старый формат | task | P3 | 6 (0+3+1+0+2) | - |
`
	if err := os.WriteFile(filepath.Join(root, "docs", "TASKS.md"), []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := cmdPick(root, "T-001")
	if err != nil {
		t.Fatalf("pick на 6-колоночной доске: %v", err)
	}
	if !strings.HasPrefix(out, "model: sonnet") || !strings.Contains(out, "не оценена") {
		t.Fatalf("жду sonnet с пометкой про цену, получил %q", out)
	}
}
