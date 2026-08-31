package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
	writeFile(t, dk, "kit/harness/claude-code.toml", "[detect]\nenv = \"CLAUDECODE\"\nvalue = \"1\"\nbin = \"claude\"\n\n"+
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

// TestQuotaHarnessFlag: снимок второй подписки читается флагом --harness из
// чужой сессии. Активной glm-code бывает только внутри собственного подпроцесса,
// и без флага его остаток из сессии первой подписки недосягаем. Гоняется
// процессом, как TestQuotaWithoutSpec: развилка живёт в main.
func TestQuotaHarnessFlag(t *testing.T) {
	home := t.TempDir()
	writeFile(t, home, ".devkit/harness.local", `default = "claude-code"
enabled = ["claude-code", "glm-code"]

[claude-code]
mini = "haiku"
base = "sonnet"
pro = "opus"
max = "fable"

[glm-code]
home = "`+home+`"
`)
	// Профили и съёмщик берутся из репозитория: временный DEVKIT_HOME без snap/
	// проверял бы отсутствие съёмщика, а не его работу.
	cases := []struct {
		args    []string
		refused bool
		want    string
	}{
		{[]string{"quota", "--harness", "glm-code"}, false, "glm-code.local"},
		// refresh и правда зовёт съёмщика: без настроек клиента в пустом HOME он
		// честно отказывает, и в причине назван съёмщик второй подписки.
		{[]string{"quota", "refresh", "--harness", "glm-code"}, true, "glm-code.sh"},
	}
	for _, c := range cases {
		cmd := exec.Command("go", append([]string{"run", "."}, c.args...)...)
		cmd.Env = append(os.Environ(), "HOME="+home, "DEVKIT_HOME="+repoRoot(t), "DEVKIT_HARNESS=")
		out, err := cmd.CombinedOutput()
		if (err != nil) != c.refused {
			t.Fatalf("%v: отказ %v при выводе:\n%s", c.args, err, out)
		}
		if strings.Contains(string(out), "claude-code") || !strings.Contains(string(out), c.want) {
			t.Fatalf("%v: флаг не привёл команду к снимку второй подписки:\n%s", c.args, out)
		}
	}
}

// TestQuotaRefreshAll: периодическому съёму (тик сторожка, демон дашборда)
// активный харнес не указ, и режим --all обходит обе подписки за один вызов
// (DK-633). Гоняется собранным бинарём с урезанным PATH: без tmux и claude
// панель первой подписки честно отказывает, а не поднимает живого клиента из
// теста. Проверяются три исхода: отказ обеих подписок не глушит друг друга и
// называет каждую, свежие снимки при --if-stale не переснимаются и не
// трогаются на диске, а занятый замок кончается тихим нулём.
func TestQuotaRefreshAll(t *testing.T) {
	home := t.TempDir()
	writeFile(t, home, ".devkit/harness.local", `default = "claude-code"
enabled = ["claude-code", "glm-code"]

[claude-code]
mini = "haiku"
base = "sonnet"
pro = "opus"
max = "fable"

[glm-code]
home = "`+home+`"
`)
	bin := filepath.Join(t.TempDir(), "agentctl")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("сборка бинаря: %v\n%s", err, out)
	}
	run := func(args ...string) (string, int) {
		cmd := exec.Command(bin, args...)
		cmd.Env = append(os.Environ(), "HOME="+home, "DEVKIT_HOME="+repoRoot(t),
			"DEVKIT_HARNESS=", "PATH=/usr/bin:/bin")
		out, _ := cmd.CombinedOutput()
		return string(out), cmd.ProcessState.ExitCode()
	}

	out, code := run("quota", "refresh", "--all")
	if code != 1 {
		t.Fatalf("отказ обеих подписок должен кончаться кодом 1, а не %d:\n%s", code, out)
	}
	for _, part := range []string{"харнес claude-code", "харнес glm-code", "отказов 2"} {
		if !strings.Contains(out, part) {
			t.Fatalf("в разборе отказа нет %q:\n%s", part, out)
		}
	}

	taken := time.Now().Format("2006-01-02T15:04")
	claude := writeFile(t, home, ".devkit/quota/claude-code.local",
		"taken = "+taken+"\nweek_all = 10% сброс 2099-01-01T00:00\nweek_max = 5% сброс 2099-01-01T00:00\n")
	glm := writeFile(t, home, ".devkit/quota/glm-code.local",
		"taken = "+taken+"\nwindow5h_all = 0% сброс 2099-01-01T00:00\nweek_all = 7% сброс 2099-01-01T00:00\n")
	before := map[string]string{}
	for _, p := range []string{claude, glm} {
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		before[p] = string(data)
	}
	out, code = run("quota", "refresh", "--all", "--if-stale")
	if code != 0 {
		t.Fatalf("свежие снимки должны кончаться нулём, а не %d:\n%s", code, out)
	}
	for _, part := range []string{"свежих 2", "снято 0", "отказов 0"} {
		if !strings.Contains(out, part) {
			t.Fatalf("в счёте исходов нет %q:\n%s", part, out)
		}
	}
	for p, was := range before {
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != was {
			t.Fatalf("свежий снимок %s переписан:\n%s", p, data)
		}
	}

	lock := filepath.Join(home, ".devkit", "quota-refresh.lock")
	if err := os.Mkdir(lock, 0o755); err != nil {
		t.Fatal(err)
	}
	out, code = run("quota", "refresh", "--all")
	if code != 0 || !strings.Contains(out, "съём уже идёт") {
		t.Fatalf("занятый замок должен кончаться тихим нулём, код %d:\n%s", code, out)
	}
	if _, err := os.Stat(lock); err != nil {
		t.Fatalf("чужой замок снят: %v", err)
	}
}

// TestQuotaAllFlagGuards: --all не сочетается с --harness и без refresh не
// работает, причина в обоих случаях человеческая.
func TestQuotaAllFlagGuards(t *testing.T) {
	home := t.TempDir()
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"quota", "refresh", "--all", "--harness", "glm-code"}, "--all и --harness вместе не работают"},
		{[]string{"quota", "--all"}, "--all идёт вместе с refresh"},
	}
	for _, c := range cases {
		cmd := exec.Command("go", append([]string{"run", "."}, c.args...)...)
		cmd.Env = append(os.Environ(), "HOME="+home, "DEVKIT_HOME="+repoRoot(t), "DEVKIT_HARNESS=")
		out, err := cmd.CombinedOutput()
		if err == nil || !strings.Contains(string(out), c.want) {
			t.Fatalf("%v: жду отказ с %q, получил (%v):\n%s", c.args, c.want, err, out)
		}
	}
}
