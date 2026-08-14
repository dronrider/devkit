package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

// Запуск чужой команды на выбранной подписке (DK-326, LLD
// DK-328-dashboard-screens, решение 3). Контур синтетический, как у остальных
// тестов делегирования: команда это sh, и окружение подпроцесса видно снаружи
// файлом.

// execEnvDump это команда, складывающая своё окружение в названный файл.
// Рабочую директорию exec не меняет, поэтому путь идёт аргументом, а не
// относительным именем.
func execEnvDump(path string) []string {
	return []string{"/bin/sh", "-c", `env > "$1"`, "sh", path}
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

// TestExecDefaultHarnessWithoutEnv: харнес по умолчанию пар окружения не
// требует, он и так поднимается домашним каталогом. Иначе выбор подписки не
// работал бы на машине с одной подпиской, где выбирать нечего.
func TestExecDefaultHarnessWithoutEnv(t *testing.T) {
	kit := execKit(t)
	work := realPath(t, t.TempDir())
	dump := filepath.Join(work, "env.txt")
	if code, out := execOut(t, kit, "homecli", execEnvDump(dump)); code != 0 {
		t.Fatalf("код возврата %d, жду 0: %s", code, out)
	}
	if env := dumpedEnv(t, kit, work); env[harnessEnv] != "homecli" {
		t.Fatalf("харнес команды %q, жду homecli", env[harnessEnv])
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
