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
		if !strings.Contains(string(out), "agentctl: выбор исполнителя") {
			t.Fatalf("%v: вместо справки: %s", args, out)
		}
	}
}

// TestOldSixColumnBoard: доска без колонки «Цена» не превращается в «нет на
// доске», цена трактуется как неоценённая.
func TestOldSixColumnBoard(t *testing.T) {
	isolateQuota(t)
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
	out, err := cmdPick(root, "T-001", false, roleExec, "")
	if err != nil {
		t.Fatalf("pick на 6-колоночной доске: %v", err)
	}
	if !strings.HasPrefix(out, "model: opus") || !strings.Contains(out, "не оценена") {
		t.Fatalf("жду opus с пометкой про цену, получил %q", out)
	}
}

// TestQuotaWithoutSpec: команды quota читают и пишут снимок, и без объявления
// квоты им работать не с чем, поэтому тут честный отказ, а не прочерк с хвостом,
// как у pick. Гоняется процессом: развилка живёт в main, и из библиотечного
// вызова её не видно, а без неё в q.read() уходил бы nil, то есть паника вместо
// причины.
func TestQuotaWithoutSpec(t *testing.T) {
	home := t.TempDir()
	// Профиль сегодняшнего харнеса с пустой секцией [quota]: резолв прежний,
	// снимать остаток нечем.
	dk := t.TempDir()
	writeFile(t, dk, "harness/claude-code.toml", "[detect]\nenv = \"CLAUDECODE\"\nvalue = \"1\"\nbin = \"claude\"\n\n"+
		"[rules]\nmode = \"embed\"\n\n[delegate]\nmode = \"native\"\n\n[hooks]\n\n[quota]\n")

	cases := []struct {
		name    string
		machine string
		want    string
	}{
		{"пустая секция quota", "", "секция [quota] пуста"},
		{"харнес не определён", "enabled = []\n", "харнес не определён"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			machine := filepath.Join(home, ".devkit", "harness.local")
			if c.machine == "" {
				os.Remove(machine)
			} else {
				writeFile(t, home, ".devkit/harness.local", c.machine)
			}
			for _, args := range [][]string{{"quota"}, {"quota", "refresh"}} {
				cmd := exec.Command("go", append([]string{"run", "."}, args...)...)
				cmd.Env = append(os.Environ(), "HOME="+home, "DEVKIT_HOME="+dk, "DEVKIT_HARNESS=")
				out, err := cmd.CombinedOutput()
				if err == nil {
					t.Fatalf("%v: команда без объявления квоты завершилась успехом:\n%s", args, out)
				}
				if code := cmd.ProcessState.ExitCode(); code != 1 {
					t.Fatalf("%v: код возврата %d при выводе:\n%s", args, code, out)
				}
				if strings.Contains(string(out), "panic") {
					t.Fatalf("%v: вместо причины паника:\n%s", args, out)
				}
				if !strings.HasPrefix(string(out), "ошибка: ") || !strings.Contains(string(out), c.want) {
					t.Fatalf("%v: причина отказа не человеческая:\n%s", args, out)
				}
			}
		})
	}
}
