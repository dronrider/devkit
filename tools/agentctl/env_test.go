package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Каталог конфигурации и окружение подпроцесса: ключи home и env секции харнеса
// машинного слоя. Дизайн в docs/lld/DK-090-heterogeneous-ladder.md, раздел
// «Каталог конфигурации и окружение подпроцесса». Контур тут тот же
// синтетический, что у остальных тестов делегирования: профиль поднимает sh, и
// окружение подпроцесса видно снаружи.

// dumpProfile это харнес, чей подпроцесс складывает своё окружение в файл
// рабочей директории. В вывод оно не печатается сознательно: значения env не
// печатает никакая команда, и тест на значение-маркер ниже смотрит именно вывод.
const dumpProfile = `[delegate]
mode = "cli"
command = ["/bin/sh", "-c", "env > env.txt"]

[hooks]

[quota]
`

// dumpedEnv поднимает делегирование и отдаёт окружение подпроцесса разобранным
// по именам.
func dumpedEnv(t *testing.T, root, work string) map[string]string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(work, "env.txt"))
	if err != nil {
		t.Fatalf("окружение подпроцесса не снято: %v", err)
	}
	env := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		if i := strings.Index(line, "="); i > 0 {
			// Последнее значение побеждает: дубли в окружении подпроцесса
			// разрешаются так же, как их разрешает сам exec.
			env[line[:i]] = line[i+1:]
		}
	}
	return env
}

// TestRunEnvPairs: пары из env харнеса назначения приезжают подпроцессу, {home}
// подставляется из машинного ключа, ведущий ~/ разворачивается в обоих ключах, а
// унаследованное значение перебивается парой.
func TestRunEnvPairs(t *testing.T) {
	kit := fakeKit(t)
	writeProfile(t, kit, "dumpcli", dumpProfile)
	writeMachine(t, kit, `enabled = ["dumpcli"]
default = "dumpcli"

[dumpcli]
mini = "cheap"
base = "cheap"
pro = "strong"
max = "strong"
home = "~/.claude-glm"
env = ["CLAUDE_CONFIG_DIR={home}", "GLM_LOG=~/logs/glm", "OVER=новое", "PLAIN=просто"]
`)
	root := writeBoard(t)
	work := realPath(t, t.TempDir())
	t.Setenv("OVER", "старое")
	if code, out := runOut(t, root, "T-001", roleExec, work); code != 0 {
		t.Fatalf("код возврата %d, жду 0: %s", code, out)
	}
	env := dumpedEnv(t, root, work)
	want := map[string]string{
		"CLAUDE_CONFIG_DIR": filepath.Join(kit, ".claude-glm"),
		"GLM_LOG":           filepath.Join(kit, "logs", "glm"),
		"OVER":              "новое",
		"PLAIN":             "просто",
		harnessEnv:          "dumpcli",
		runDepthEnv:         "1",
	}
	for name, value := range want {
		if env[name] != value {
			t.Fatalf("подпроцессу досталось %s=%q, жду %q", name, env[name], value)
		}
	}
}

// TestRunEnvOfAssignedHarness: пары берутся у харнеса назначения, а не у
// активного. Уехавшая ступень поднимается своим каталогом конфигурации, и взять
// его у активного значит поднять окно на чужой подписке с домашним каталогом.
func TestRunEnvOfAssignedHarness(t *testing.T) {
	kit := fakeKit(t)
	writeProfile(t, kit, "homecli", echoProfile)
	writeProfile(t, kit, "awaycli", dumpProfile)
	writeMachine(t, kit, `enabled = ["homecli"]
default = "homecli"

[homecli]
mini = "awaycli:чужая"
base = "cheap"
pro = "strong"
max = "strong"
env = ["CLAUDE_CONFIG_DIR=/дом/активного"]

[awaycli]
home = "~/.claude-glm"
env = ["CLAUDE_CONFIG_DIR={home}"]
`)
	root := writeBoard(t)
	work := realPath(t, t.TempDir())
	if code, out := runOut(t, root, "T-001", roleExec, work); code != 0 {
		t.Fatalf("код возврата %d, жду 0: %s", code, out)
	}
	env := dumpedEnv(t, root, work)
	if got, want := env["CLAUDE_CONFIG_DIR"], filepath.Join(kit, ".claude-glm"); got != want {
		t.Fatalf("каталог подпроцесса %q, жду каталог назначения %q", got, want)
	}
	if env[harnessEnv] != "awaycli" {
		t.Fatalf("харнес подпроцесса %q, жду awaycli", env[harnessEnv])
	}
}

// TestEnvValuesNotPrinted: значения env не печатает ни одна команда, печатаются
// только имена. Массив общий, завтра туда положат токен, а вывод команд уезжает
// в логи и в контекст агента. Проверяется на подставном значении-маркере.
func TestEnvValuesNotPrinted(t *testing.T) {
	const marker = "значение-маркер-МАРКЕР42"
	kit := fakeKit(t)
	writeProfile(t, kit, "quietcli", `[delegate]
mode = "cli"
command = ["/bin/sh", "-c", "exit 0"]

[hooks]

[quota]
`)
	writeMachine(t, kit, `enabled = ["quietcli"]
default = "quietcli"

[quietcli]
mini = "cheap"
base = "cheap"
pro = "strong"
max = "strong"
home = "~/.claude-glm"
env = ["SECRET_TOKEN=`+marker+`"]
`)
	root := writeBoard(t)
	var out, errw bytes.Buffer
	if _, err := cmdRun(root, "T-001", false, roleExec, "", root, &out, &errw); err != nil {
		t.Fatal(err)
	}
	texts := map[string]string{"run": out.String() + errw.String()}
	harness, err := cmdHarness(root, "")
	if err != nil {
		t.Fatal(err)
	}
	texts["harness"] = harness
	// Машинный вид той же команды уезжает по HTTP в дашборд, и значение,
	// просочившееся в JSON, попало бы в браузер вместе с ним.
	asJSON, err := cmdHarnessJSON(root)
	if err != nil {
		t.Fatal(err)
	}
	texts["harness --json"] = asJSON
	pick, err := cmdPick(root, "T-001", false, roleExec, "")
	if err != nil {
		t.Fatal(err)
	}
	texts["pick"] = pick
	for who, text := range texts {
		if strings.Contains(text, marker) {
			t.Fatalf("значение env напечатано командой %s:\n%s", who, text)
		}
		if !strings.Contains(text, "SECRET_TOKEN") && who != "pick" {
			t.Fatalf("имя переменной команда %s не назвала, а окружение подпроцесса сказать надо:\n%s", who, text)
		}
	}
	// Каталог, наоборот, печатается: это путь, по которому лежит машинное
	// хозяйство харнеса, и не назвать его значит оставить без ответа вопрос, куда
	// смотреть.
	if !strings.Contains(texts["harness"], filepath.Join(kit, ".claude-glm")) {
		t.Fatalf("каталог харнеса не назван:\n%s", texts["harness"])
	}
}

// TestMachineEnvErrors: проверки ключа env все жёсткие. Тихо пропущенная
// переменная это окно, которое молча ушло на дорогую подписку, а перебитая
// приставка DEVKIT_ это воронка сессий.
func TestMachineEnvErrors(t *testing.T) {
	dir := writeProfiles(t, map[string][2]string{"claude-code": {"CLAUDECODE", "native"}})
	head := "enabled = [\"claude-code\"]\n\n[claude-code]\nmini = \"h\"\nbase = \"s\"\npro = \"o\"\nmax = \"f\"\n"
	cases := []struct{ name, tail, want string }{
		{"без знака равенства", "env = [\"CLAUDE_CONFIG_DIR\"]\n", "нет знака ="},
		{"пустое имя", "env = [\"=значение\"]\n", "слева от = пусто"},
		{"приставка devkit", "env = [\"DEVKIT_RUN_DEPTH=0\"]\n", "приставка DEVKIT_ закрыта"},
		{"каталога нет", "env = [\"CLAUDE_CONFIG_DIR={home}\"]\n", "вписать home в секцию [claude-code]"},
		{"env строкой", "env = \"CLAUDE_CONFIG_DIR=/tmp\"\n", "env: жду массив строк, вижу строку"},
		{"home массивом", "home = [\"~/.claude\"]\n", "home: жду строку, вижу массив строк"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			machine := writeFile(t, t.TempDir(), "harness.local", head+c.tail)
			_, err := mergeLayers(dir, machine, "")
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("ошибка %v, жду про %q", err, c.want)
			}
		})
	}
}

// TestMachineEnvErrorKeepsValueQuiet: сообщение об отказе называет номер и имя, а
// значение не печатает. Ошибка уезжает в те же логи, что и всё остальное, и
// пришедший в env токен утёк бы через неё.
func TestMachineEnvErrorKeepsValueQuiet(t *testing.T) {
	const marker = "МАРКЕР42"
	dir := writeProfiles(t, map[string][2]string{"claude-code": {"CLAUDECODE", "native"}})
	machine := writeFile(t, t.TempDir(), "harness.local",
		"enabled = [\"claude-code\"]\n\n[claude-code]\nmini = \"h\"\nbase = \"s\"\npro = \"o\"\nmax = \"f\"\nenv = [\"OK=1\", \"DEVKIT_TOKEN="+marker+"\"]\n")
	_, err := mergeLayers(dir, machine, "")
	if err == nil {
		t.Fatal("жду отказ на имени с приставкой DEVKIT_")
	}
	if strings.Contains(err.Error(), marker) {
		t.Fatalf("значение попало в текст ошибки: %v", err)
	}
	for _, want := range []string{"элемент 2", "DEVKIT_TOKEN"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("в отказе нет %q: %v", want, err)
		}
	}
}

// TestMachineHomeOnlySection: секция без ярусов с одними home и env законна. Это
// харнес, который в конфиге присутствует только назначением соседей и своей
// машинной обвязкой, а активным бывает ненастроенным.
func TestMachineHomeOnlySection(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := writeProfiles(t, map[string][2]string{
		"claude-code": {"CLAUDECODE", "native"},
		"glm-code":    {"", "cli"},
	})
	machine := writeFile(t, home, "harness.local", `default = "claude-code"
enabled = ["claude-code"]

[claude-code]
mini = "haiku"
base = "glm-code:glm-5.3-flash"
pro = "opus"
max = "fable"

[glm-code]
home = "~/.claude-glm"
env = ["CLAUDE_CONFIG_DIR={home}"]
`)
	l, err := mergeLayers(dir, machine, "")
	if err != nil {
		t.Fatal(err)
	}
	s := l.Setup["glm-code"]
	if s.mapped() {
		t.Fatal("секция без ярусов не должна считаться настроенной")
	}
	if s.Home != filepath.Join(home, ".claude-glm") {
		t.Fatalf("каталог %q, жду развёрнутую тильду", s.Home)
	}
	if len(s.Env) != 1 || s.Env[0].Name != "CLAUDE_CONFIG_DIR" || s.Env[0].Value != s.Home {
		t.Fatalf("окружение %+v, жду CLAUDE_CONFIG_DIR с подставленным каталогом", s.Env)
	}
}

// TestParseHarnessEnvHomeSubstitution: {home} подставляется в любом месте
// значения, а не только целым словом: путям бывает нужен подкаталог.
func TestParseHarnessEnvHomeSubstitution(t *testing.T) {
	pairs, err := parseHarnessEnv("m.local", "glm-code",
		[]string{"CFG={home}", "LOG={home}/logs/last.jsonl"}, "/дом/glm")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"CFG=/дом/glm", "LOG=/дом/glm/logs/last.jsonl"}
	var got []string
	for _, p := range pairs {
		got = append(got, p.Name+"="+p.Value)
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("вышло %v, жду %v", got, want)
	}
}
