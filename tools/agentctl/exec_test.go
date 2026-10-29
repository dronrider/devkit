package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/dronrider/devkit/internal/clientdir"
)

// Запуск чужой команды на выбранной подписке (DK-326, LLD
// DK-328-dashboard-screens, решение 3). Контур синтетический, как у остальных
// тестов делегирования: команда это sh, и окружение подпроцесса видно снаружи
// файлом.

// execEnvDump это команда, складывающая своё окружение в названный файл.
// Путь идёт аргументом, а не относительным именем: рабочий каталог команды
// выбирает сам exec (DK-1373), и относительному имени в нём доверять нельзя.
func execEnvDump(path string) []string {
	return []string{"/bin/sh", "-c", `env > "$1"`, "sh", path}
}

// execCwdDump это команда, складывающая свой рабочий каталог в названный файл.
func execCwdDump(path string) []string {
	return []string{"/bin/sh", "-c", `pwd > "$1"`, "sh", path}
}

func execOut(t *testing.T, start, name string, argv []string) (int, string) {
	t.Helper()
	var out, errw bytes.Buffer
	code, err := cmdExec(start, name, argv, &out, &errw)
	if err != nil {
		t.Fatalf("exec --harness %s: %v", name, err)
	}
	return code, out.String() + errw.String()
}

// execMachine это машинный слой с двумя харнесами: домашний по умолчанию и
// вторая подписка со своим каталогом конфигурации.
const execMachine = `enabled = ["homecli", "secondcli"]
default = "homecli"

[homecli]
mini = "cheap"
base = "cheap"
pro = "strong"
max = "strong"

[secondcli]
home = "~/.claude-second"
env = ["CLAUDE_CONFIG_DIR={home}", "SECOND_LOG=~/logs/second"]
`

func execKit(t *testing.T) string {
	t.Helper()
	kit := fakeKit(t)
	writeProfile(t, kit, "homecli", echoProfile)
	writeProfile(t, kit, "secondcli", echoProfile)
	writeMachine(t, kit, execMachine)
	return kit
}

// TestExecPutsHarnessEnv: команда получает пары окружения выбранного харнеса и
// его имя переменной DEVKIT_HARNESS. Без пар подмена подписки была бы
// переименованием: имя сменилось бы, а каталог конфигурации остался домашним.
func TestExecPutsHarnessEnv(t *testing.T) {
	kit := execKit(t)
	work := realPath(t, t.TempDir())
	dump := filepath.Join(work, "env.txt")
	code, out := execOut(t, kit, "secondcli", execEnvDump(dump))
	if code != 0 {
		t.Fatalf("код возврата %d, жду 0: %s", code, out)
	}
	env := dumpedEnv(t, kit, work)
	want := map[string]string{
		"CLAUDE_CONFIG_DIR": filepath.Join(kit, ".claude-second"),
		"SECOND_LOG":        filepath.Join(kit, "logs", "second"),
		harnessEnv:          "secondcli",
	}
	for name, value := range want {
		if env[name] != value {
			t.Fatalf("команде досталось %s=%q, жду %q", name, env[name], value)
		}
	}
	// Ограничитель вложенности exec не ставит: под ним живёт сессия-диспетчер, а
	// она обязана уметь делегировать. Взведи exec ограничитель, и конвейер,
	// поднятый с экрана, отказал бы на первом же субагенте.
	if v := env[runDepthEnv]; v != "" {
		t.Fatalf("exec взвёл ограничитель вложенности (%s=%q): поднятая сессия делегировать не сможет", runDepthEnv, v)
	}
	if !strings.Contains(out, "exec: харнес secondcli") {
		t.Fatalf("exec молчит о подписке, на которой поднял команду:\n%s", out)
	}
}

// TestExecNamesEnvWithoutValues: строка запуска называет имена переменных и не
// печатает ни одного значения. В env лежит токен второй подписки, а вывод
// уезжает в логи и в панель сессии, которую дашборд показывает на экране.
func TestExecNamesEnvWithoutValues(t *testing.T) {
	const marker = "значение-маркер-МАРКЕР42"
	kit := fakeKit(t)
	writeProfile(t, kit, "homecli", echoProfile)
	writeProfile(t, kit, "secondcli", echoProfile)
	writeMachine(t, kit, `enabled = ["homecli", "secondcli"]
default = "homecli"

[homecli]
mini = "cheap"
base = "cheap"
pro = "strong"
max = "strong"

[secondcli]
home = "~/.claude-second"
env = ["SECRET_TOKEN=`+marker+`"]
`)
	_, out := execOut(t, kit, "secondcli", []string{"/bin/sh", "-c", "exit 0"})
	if strings.Contains(out, marker) {
		t.Fatalf("значение env напечатано:\n%s", out)
	}
	if !strings.Contains(out, "SECRET_TOKEN") {
		t.Fatalf("имя переменной не названо, а окружение команды сказать надо:\n%s", out)
	}
}

// TestExecNamesWorkingDir: рабочий каталог команды называется явно, а не
// наследуется молча. Наследованный каталог уходил команде как есть, и у
// launchd-обёртки exec (агент дашборда, сменные съёмщики) им оказывался корень
// файловой системы: поднятое дерево обходило весь диск и поднимало диалоги
// доступа macOS на имя дашборда (DK-1373). Годный каталог поднявшего
// сохраняется, дом меняется на служебный пустой каталог подъёма клиента.
func TestExecNamesWorkingDir(t *testing.T) {
	kit := execKit(t)

	t.Run("каталог поднявшего сохраняется", func(t *testing.T) {
		work := realPath(t, t.TempDir())
		dump := filepath.Join(work, "cwd.txt")
		t.Chdir(work)
		code, out := execOut(t, kit, "secondcli", execCwdDump(dump))
		if code != 0 {
			t.Fatalf("код возврата %d, жду 0: %s", code, out)
		}
		raw, err := os.ReadFile(dump)
		if err != nil {
			t.Fatalf("команда не оставила след каталога: %v", err)
		}
		if got := strings.TrimSpace(string(raw)); got != work {
			t.Fatalf("команда поднята в %q, ждали каталог поднявшего %q", got, work)
		}
	})

	t.Run("дом меняется на служебный пустой", func(t *testing.T) {
		// Стенд сам ставит HOME временным каталогом кита. Тот же каталог с
		// развёрнутыми симлинками становится и рабочим: поднятие из дома это
		// дорога, уходящая в служебный пустой каталог, а развёрнутая запись
		// нужна, чтобы pwd и HOME совпали до последнего знака, иначе на macOS
		// их спорили бы /var и /private/var.
		home := realPath(t, kit)
		dump := filepath.Join(home, "cwd.txt")
		t.Setenv("HOME", home)
		t.Chdir(home)
		code, out := execOut(t, kit, "secondcli", execCwdDump(dump))
		if code != 0 {
			t.Fatalf("код возврата %d, жду 0: %s", code, out)
		}
		raw, err := os.ReadFile(dump)
		if err != nil {
			t.Fatalf("команда не оставила след каталога: %v", err)
		}
		got, err := filepath.EvalSymlinks(strings.TrimSpace(string(raw)))
		if err != nil {
			t.Fatal(err)
		}
		want, err := filepath.EvalSymlinks(filepath.Join(home, clientdir.Base, clientdir.Name))
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("команда поднята в %q, ждали служебный каталог %q", got, want)
		}
	})
}

// TestExecExitCode: код выхода команды проезжает наружу как есть. Сессию
// конвейера поднимает именно exec, и проглоченный код превратил бы умершую
// сессию в удачный запуск.
func TestExecExitCode(t *testing.T) {
	kit := execKit(t)
	code, out := execOut(t, kit, "secondcli", []string{"/bin/sh", "-c", "exit 7"})
	if code != 7 {
		t.Fatalf("код возврата %d, жду 7: %s", code, out)
	}
}

// TestExecRefusesHarnessWithoutEnv: харнес не по умолчанию и без пар окружения
// это отказ. Иначе команда пошла бы на подписке по умолчанию, называя себя
// чужим именем, а замечается такое по счёту, то есть поздно.
func TestExecRefusesHarnessWithoutEnv(t *testing.T) {
	kit := fakeKit(t)
	writeProfile(t, kit, "homecli", echoProfile)
	writeProfile(t, kit, "barecli", echoProfile)
	writeMachine(t, kit, `enabled = ["homecli", "barecli"]
default = "homecli"

[homecli]
mini = "cheap"
base = "cheap"
pro = "strong"
max = "strong"
`)
	_, err := cmdExec(kit, "barecli", []string{"/bin/sh", "-c", "exit 0"}, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("жду отказ: харнес без пар окружения поднялся бы на подписке по умолчанию")
	}
	for _, want := range []string{"barecli", "под чужим именем", machineConfigPath()} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("в отказе нет %q: %v", want, err)
		}
	}
}

// TestExecRefusesDefaultHarnessWithoutEnv: харнес по умолчанию без пар
// окружения это отказ, а не тихий пропуск обвязки (DK-1271, кейс 3). Без пар
// команда пошла бы мимо обвязки подписки, и чат поднимался на профиле первой
// подписки с истёкшим входом.
func TestExecRefusesDefaultHarnessWithoutEnv(t *testing.T) {
	kit := execKit(t)
	_, err := cmdExec(kit, "homecli", []string{"/bin/sh", "-c", "exit 0"}, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("жду отказ: default без пар окружения поднялся бы мимо обвязки")
	}
	for _, want := range []string{"homecli", "devkitctl doctor --fix"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("в отказе нет %q: %v", want, err)
		}
	}
}

// TestExecDefaultHarnessWithEnv: default с парами окружения поднимается
// обвязкой как обычная подписка: пары кладутся, имя харнеса едет в
// DEVKIT_HARNESS.
func TestExecDefaultHarnessWithEnv(t *testing.T) {
	kit := fakeKit(t)
	writeProfile(t, kit, "homecli", echoProfile)
	writeMachine(t, kit, `enabled = ["homecli"]
default = "homecli"

[homecli]
env = ["CLAUDE_CONFIG_DIR=~/.claude-home"]
`)
	work := realPath(t, t.TempDir())
	dump := filepath.Join(work, "env.txt")
	if code, out := execOut(t, kit, "homecli", execEnvDump(dump)); code != 0 {
		t.Fatalf("код возврата %d, жду 0: %s", code, out)
	}
	env := dumpedEnv(t, kit, work)
	if env[harnessEnv] != "homecli" {
		t.Fatalf("харнес команды %q, жду homecli", env[harnessEnv])
	}
	if env["CLAUDE_CONFIG_DIR"] == "" {
		t.Fatalf("пара окружения не легла: %v", env)
	}
}

// TestExecUnknownHarness: имя без профиля это отказ, а не запуск на подписке по
// умолчанию. Опечатка тут стоит целой сессии, ушедшей не на ту квоту.
func TestExecUnknownHarness(t *testing.T) {
	kit := execKit(t)
	_, err := cmdExec(kit, "опечатка", []string{"/bin/sh", "-c", "exit 0"}, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "нет профиля") {
		t.Fatalf("ошибка %v, жду отказ про отсутствующий профиль", err)
	}
}

// TestExecNeedsNameAndCommand: без имени харнеса и без команды exec отказывает
// словами, а не поднимает что-нибудь на чём-нибудь.
func TestExecNeedsNameAndCommand(t *testing.T) {
	kit := execKit(t)
	if _, err := cmdExec(kit, "", []string{"/bin/sh"}, &bytes.Buffer{}, &bytes.Buffer{}); err == nil {
		t.Fatal("жду отказ без имени харнеса")
	}
	if _, err := cmdExec(kit, "secondcli", nil, &bytes.Buffer{}, &bytes.Buffer{}); err == nil {
		t.Fatal("жду отказ без команды")
	}
}

// TestExecNotEnabledNote: разовый заход в невключённый харнес законен, но
// молчать про неразложенные правила нельзя.
func TestExecNotEnabledNote(t *testing.T) {
	kit := fakeKit(t)
	writeProfile(t, kit, "homecli", echoProfile)
	writeProfile(t, kit, "offcli", echoProfile)
	writeMachine(t, kit, `enabled = ["homecli"]
default = "homecli"

[homecli]
mini = "cheap"
base = "cheap"
pro = "strong"
max = "strong"

[offcli]
home = "~/.claude-off"
env = ["CLAUDE_CONFIG_DIR={home}"]
`)
	_, out := execOut(t, kit, "offcli", []string{"/bin/sh", "-c", "exit 0"})
	if !strings.Contains(out, "не в списке включённых") {
		t.Fatalf("exec молчит про невключённый харнес:\n%s", out)
	}
}

// TestExecStdoutPassesThrough: вывод команды идёт наружу, а не в пустоту. Под
// exec живут сессии агентов, и проглоченный вывод оставил бы панель пустой.
func TestExecStdoutPassesThrough(t *testing.T) {
	kit := execKit(t)
	var out, errw bytes.Buffer
	if _, err := cmdExec(kit, "secondcli", []string{"/bin/sh", "-c", "echo слово команды"}, &out, &errw); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "слово команды") {
		t.Fatalf("вывод команды до вызывающего не доехал: %q", out.String())
	}
}

func TestExecBlocksProtectedManagedTask(t *testing.T) {
	kit := execKit(t)
	root := t.TempDir()
	t.Chdir(root)
	t.Setenv("DEVKIT_TASK", "XR-1")
	path := filepath.Join(root, "docs", "tasks", "XR-1.md")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("# XR-1\nзащита: locked\n"), 0600); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(root, "started")
	var out, errout bytes.Buffer
	_, err := cmdExec(kit, "secondcli", []string{"/bin/sh", "-c", `touch "$1"`, "sh", marker}, &out, &errout)
	if err == nil {
		t.Fatal("protected task launched")
	}
	if _, err = os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("client started: %v", err)
	}
}

func TestExecKeepsManagedCloudLockThroughClient(t *testing.T) {
	kit := execKit(t)
	root := t.TempDir()
	t.Chdir(root)
	t.Setenv("DEVKIT_TASK", "XR-1")
	path := filepath.Join(root, "docs", "tasks", "XR-1.md")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("# XR-1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	marker, stop := filepath.Join(root, "started"), filepath.Join(root, "stop")
	script := `touch "$1"; while [ ! -e "$2" ]; do sleep 0.05; done`
	done := make(chan error, 1)
	go func() {
		var out, errout bytes.Buffer
		_, err := cmdExec(kit, "secondcli", []string{"/bin/sh", "-c", script, "sh", marker, stop}, &out, &errout)
		done <- err
	}()
	t.Cleanup(func() { os.WriteFile(stop, []byte("stop"), 0600) })
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("client did not start")
		}
		time.Sleep(20 * time.Millisecond)
	}
	f, err := os.OpenFile(filepath.Join(root, ".devkit", "draft-tx", "cloud-locks", "XR-1"), os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err == nil {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		t.Fatal("private attach can enter")
	}
	if err = os.WriteFile(stop, []byte("stop"), 0600); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("client stuck")
	}
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatalf("lock remained: %v", err)
	}
	syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}

func TestExecBlocksProtectedMainFromStaleWorktree(t *testing.T) {
	kit := execKit(t)
	root := t.TempDir()
	t.Setenv("DEVKIT_TASK", "XR-1")
	path := filepath.Join(root, "docs", "tasks", "XR-1.md")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("# XR-1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root, "-c", "user.name=stand", "-c", "user.email=stand@example.invalid"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git: %v %s", err, out)
		}
	}
	git("init", "-q")
	git("add", "docs")
	git("commit", "-qm", "fixture")
	tree := filepath.Join(t.TempDir(), "tree")
	git("worktree", "add", "-qb", "fixture-tree", tree)
	if err := os.WriteFile(path, []byte("# XR-1\nзащита: locked\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(tree)
	marker := filepath.Join(tree, "started")
	var out, errout bytes.Buffer
	_, err := cmdExec(kit, "secondcli", []string{"/bin/sh", "-c", `touch "$1"`, "sh", marker}, &out, &errout)
	if err == nil {
		t.Fatal("protected primary ignored")
	}
	if _, err = os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("client started: %v", err)
	}
}

func TestExecWaitsForPrivateAttachThenRefuses(t *testing.T) {
	kit := execKit(t)
	root := t.TempDir()
	t.Chdir(root)
	t.Setenv("DEVKIT_TASK", "XR-1")
	path := filepath.Join(root, "docs", "tasks", "XR-1.md")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("# XR-1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	lockPath := filepath.Join(root, ".devkit", "draft-tx", "cloud-locks", "XR-1")
	if err := os.MkdirAll(filepath.Dir(lockPath), 0700); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(root, "started")
	done := make(chan error, 1)
	go func() {
		var out, errout bytes.Buffer
		_, err := cmdExec(kit, "secondcli", []string{"/bin/sh", "-c", `touch "$1"`, "sh", marker}, &out, &errout)
		done <- err
	}()
	select {
	case err := <-done:
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		t.Fatalf("launch passed reservation: %v", err)
	case <-time.After(200 * time.Millisecond):
	}
	if err = os.WriteFile(path, []byte("# XR-1\nзащита: locked\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_UN); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("protected material launched")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("launch stuck")
	}
	if _, err = os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("client started: %v", err)
	}
}
