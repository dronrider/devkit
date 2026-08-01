package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestHarnessFixtures: общие с devkitctl фикстуры. Отчёт по каждому входу
// сверяется побайтно с .expected, тот же файл сверяет питоновская реализация
// (devkitctl/test.sh). Разъедься парсеры, и профиль читался бы двумя утилитами
// по-разному, а заметили бы это по расхождению поведения, а не по тесту.
func TestHarnessFixtures(t *testing.T) {
	dir := filepath.Join("..", "harness", "testdata")
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, e := range ents {
		if !strings.HasSuffix(e.Name(), ".toml") {
			continue
		}
		seen++
		t.Run(e.Name(), func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join(dir, e.Name()))
			if err != nil {
				t.Fatal(err)
			}
			want, err := os.ReadFile(filepath.Join(dir, strings.TrimSuffix(e.Name(), ".toml")+".expected"))
			if err != nil {
				t.Fatal(err)
			}
			got := profileReport(e.Name(), string(data), strings.HasPrefix(e.Name(), "profile-"))
			if got != string(want) {
				t.Fatalf("отчёт разошёлся с ожидаемым\nжду:\n%s\nвижу:\n%s", want, got)
			}
		})
	}
	if seen < 10 {
		t.Fatalf("фикстур найдено %d, набор потерялся", seen)
	}
}

// TestProfileClaudeCode: сегодняшний харнес описан профилем без предупреждений,
// и ключи в нём те, на которых стоит остальной devkit.
func TestProfileClaudeCode(t *testing.T) {
	p, err := loadProfile(filepath.Join("..", "harness"), "claude-code")
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Warns) != 0 {
		t.Fatalf("предупреждения на своём же профиле: %v", p.Warns)
	}
	cases := []struct{ section, key, want string }{
		{"detect", "env", "CLAUDECODE"},
		{"detect", "value", "1"},
		{"detect", "bin", "claude"},
		{"rules", "mode", "import"},
		{"rules", "file", "CLAUDE.md"},
		{"delegate", "mode", "native"},
		{"delegate", "agents_dir", "~/.claude/agents"},
		{"delegate", "map_pro", "opus"},
		{"hooks", "protocol", "claude-code"},
		{"hooks", "memory_index", "/memory/MEMORY.md"},
		{"quota", "snap", "usage-pane"},
		{"quota", "required", "week_all"},
	}
	for _, c := range cases {
		if got := p.section(c.section).str(c.key); got != c.want {
			t.Fatalf("[%s] %s = %q, жду %q", c.section, c.key, got, c.want)
		}
	}
	if got := strings.Join(p.section("hooks").arr("events"), ","); got != "write,session-start,notify,subagent-done" {
		t.Fatalf("events = %q", got)
	}
}

// writeProfiles кладёт во временную директорию профили с заданными режимами
// делегирования и переменными детекта.
func writeProfiles(t *testing.T, specs map[string][2]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, spec := range specs {
		detect := ""
		if spec[0] != "" {
			detect = "env = " + quoteTOML(spec[0]) + "\nvalue = \"1\"\nbin = " + quoteTOML(name) + "\n"
		}
		command := ""
		if spec[1] == "cli" {
			command = "command = [" + quoteTOML(name) + ", \"run\", \"{prompt}\"]\n"
		}
		text := "[detect]\n" + detect + "\n[rules]\nmode = \"embed\"\n\n[delegate]\nmode = " +
			quoteTOML(spec[1]) + "\n" + command +
			"map_mini = \"m1\"\nmap_base = \"m2\"\nmap_pro = \"m3\"\nmap_max = \"m4\"\n\n[hooks]\n\n[quota]\n"
		if err := os.WriteFile(filepath.Join(dir, name+".toml"), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func writeFile(t *testing.T, dir, name, text string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestMergeWithoutMachineConfig: пункт 20 скелета со стороны слоёв. Машинного
// конфига нет, значит включён один claude-code с маппингом-предложением из его
// профиля, и это ровно сегодняшнее поведение.
func TestMergeWithoutMachineConfig(t *testing.T) {
	l, err := mergeLayers(filepath.Join("..", "harness"), filepath.Join(t.TempDir(), "harness.local"), "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(l.Enabled, ",") != "claude-code" {
		t.Fatalf("включены %v", l.Enabled)
	}
	if l.Default != "claude-code" {
		t.Fatalf("default %q", l.Default)
	}
	s := l.Setup["claude-code"]
	if !s.mapped() || !s.Suggested {
		t.Fatalf("маппинг %+v", s)
	}
	for tier, want := range map[string]string{"mini": "haiku", "base": "sonnet", "pro": "opus", "max": "fable"} {
		if s.Map[tier] != want {
			t.Fatalf("ярус %s развёрнут в %q, жду %q", tier, s.Map[tier], want)
		}
	}
	if len(l.Warns) != 0 {
		t.Fatalf("предупреждения на пустом контуре: %v", l.Warns)
	}
}

// TestMergeMachineConfig: машинный слой включает и маппит, имя без профиля
// выбрасывается предупреждением, включённый без своей секции остаётся
// ненастроенным (ярусная часть вердикта работает, разворачивать нечем).
func TestMergeMachineConfig(t *testing.T) {
	dir := writeProfiles(t, map[string][2]string{
		"claude-code": {"CLAUDECODE", "native"},
		"codex":       {"CODEX_HOME", "cli"},
	})
	home := t.TempDir()
	machine := writeFile(t, home, "harness.local", `default = "codex"
enabled = ["claude-code", "codex", "opencode"]

[codex]
mini = "gpt-mini"
base = "gpt-mini"
pro = "gpt-pro"
max = "gpt-pro"
budget = 200
bin = "/opt/codex"
`)
	l, err := mergeLayers(dir, machine, "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(l.Enabled, ",") != "claude-code,codex" {
		t.Fatalf("включены %v", l.Enabled)
	}
	if l.Setup["claude-code"].mapped() {
		t.Fatal("харнес без своей секции не должен считаться настроенным")
	}
	s := l.Setup["codex"]
	if !s.mapped() || s.Suggested || s.Map["pro"] != "gpt-pro" || s.Budget != 200 || s.Bin != "/opt/codex" {
		t.Fatalf("маппинг codex %+v", s)
	}
	if len(l.Warns) != 1 || !strings.Contains(l.Warns[0], "opencode включён, а профиля") {
		t.Fatalf("предупреждения %v", l.Warns)
	}
}

// TestMachineConfigPartialTiers: пропуск яруса неотличим от забытого, поэтому
// это жёсткая ошибка с именем файла и ключа, а не молчаливый недомаппинг.
func TestMachineConfigPartialTiers(t *testing.T) {
	dir := writeProfiles(t, map[string][2]string{"claude-code": {"CLAUDECODE", "native"}})
	machine := writeFile(t, t.TempDir(), "harness.local", "enabled = [\"claude-code\"]\n\n[claude-code]\nmini = \"haiku\"\nbase = \"sonnet\"\npro = \"opus\"\n")
	_, err := mergeLayers(dir, machine, "")
	if err == nil || !strings.Contains(err.Error(), "нет ключа max") {
		t.Fatalf("ошибка %v", err)
	}
}

// TestMachineConfigTypes: тип значения в слоях машины и проекта проверяется
// так же жёстко, как в профиле. Фикстурами это не покрыть, машинный слой читает
// только agentctl, а перепутанный тип молча выключил бы харнес: строка вместо
// массива в enabled дала бы пустой список, целое в ярусе пустую модель.
func TestMachineConfigTypes(t *testing.T) {
	dir := writeProfiles(t, map[string][2]string{"claude-code": {"CLAUDECODE", "native"}})
	cases := []struct{ name, machine, project, want string }{
		{"enabled строкой", "enabled = \"claude-code\"\n", "", "enabled: жду массив строк, вижу строку"},
		{"default массивом", "default = [\"claude-code\"]\n", "", "default: жду строку, вижу массив строк"},
		{"ярус целым", "enabled = [\"claude-code\"]\n\n[claude-code]\nmini = 1\nbase = \"s\"\npro = \"o\"\nmax = \"f\"\n", "",
			"mini: жду строку, вижу целое"},
		{"бюджет строкой", "enabled = [\"claude-code\"]\n\n[claude-code]\nmini = \"h\"\nbase = \"s\"\npro = \"o\"\nmax = \"f\"\nbudget = \"200\"\n", "",
			"budget: жду целое, вижу строку"},
		{"enabled проекта строкой", "enabled = [\"claude-code\"]\n", "enabled = \"claude-code\"\n",
			"enabled: жду массив строк, вижу строку"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			machine := writeFile(t, t.TempDir(), "harness.local", c.machine)
			project := ""
			if c.project != "" {
				project = writeFile(t, t.TempDir(), "harness.local", c.project)
			}
			_, err := mergeLayers(dir, machine, project)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("ошибка %v, жду про %q", err, c.want)
			}
		})
	}
}

// TestProjectNarrow: проектный слой только сужает. Имени вне машинного списка
// он не добавляет, лишний ключ это находка, а пустое пересечение равносильно
// «не определилось».
func TestProjectNarrow(t *testing.T) {
	dir := writeProfiles(t, map[string][2]string{
		"claude-code": {"CLAUDECODE", "native"},
		"codex":       {"CODEX_HOME", "cli"},
	})
	machine := writeFile(t, t.TempDir(), "harness.local", `default = "claude-code"
enabled = ["claude-code", "codex"]

[claude-code]
mini = "haiku"
base = "sonnet"
pro = "opus"
max = "fable"
`)
	proj := writeFile(t, t.TempDir(), "harness.local", "enabled = [\"codex\", \"cursor\"]\ndefault = \"cursor\"\n")
	l, err := mergeLayers(dir, machine, proj)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(l.Enabled, ",") != "codex" {
		t.Fatalf("после сужения включены %v", l.Enabled)
	}
	joined := strings.Join(l.Warns, "\n")
	for _, part := range []string{"cursor сужением не включить", "claude-code сужен проектным слоем", "ключ default проектному слою не положен"} {
		if !strings.Contains(joined, part) {
			t.Fatalf("нет предупреждения %q в %v", part, l.Warns)
		}
	}

	empty := writeFile(t, t.TempDir(), "harness.local", "enabled = []\n")
	l, err = mergeLayers(dir, machine, empty)
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Enabled) != 0 {
		t.Fatalf("пустое пересечение оставило %v", l.Enabled)
	}
	r, err := resolveHarness(l, "", func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	if r.Name != "" {
		t.Fatalf("на пустом списке резолв дал %q", r.Name)
	}
}

// TestResolveHarness: пять шагов детекта, первое сработавшее решает.
func TestResolveHarness(t *testing.T) {
	dir := writeProfiles(t, map[string][2]string{
		"claude-code": {"CLAUDECODE", "native"},
		"codex":       {"CODEX_HOME", "cli"},
		"gemini":      {"GEMINI_CLI", "cli"},
		"quiet":       {"", "none"},
	})
	machine := writeFile(t, t.TempDir(), "harness.local", `default = "claude-code"
enabled = ["claude-code", "codex", "quiet"]

[claude-code]
mini = "haiku"
base = "sonnet"
pro = "opus"
max = "fable"
`)
	l, err := mergeLayers(dir, machine, "")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		want string
		env  map[string]string
		how  string
		flag string
		note string
	}{
		{"флаг сильнее всего", "codex", map[string]string{"CLAUDECODE": "1", "DEVKIT_HARNESS": "claude-code"}, "флаг --harness", "codex", ""},
		{"переменная сильнее детекта", "codex", map[string]string{"CLAUDECODE": "1", "DEVKIT_HARNESS": "codex"}, "переменная DEVKIT_HARNESS", "", ""},
		{"детект по окружению", "claude-code", map[string]string{"CLAUDECODE": "1"}, "детект по переменной CLAUDECODE", "", ""},
		{"чужое значение переменной не детект", "claude-code", map[string]string{"CLAUDECODE": "0"}, "default машинного конфига", "", ""},
		{"неоднозначный детект уходит в default", "claude-code", map[string]string{"CLAUDECODE": "1", "CODEX_HOME": "1"}, "default машинного конфига", "", "неоднозначен"},
		{"сессия внутри невключённого", "claude-code", map[string]string{"GEMINI_CLI": "1"}, "default машинного конфига", "", "сессия внутри gemini"},
		{"пусто вокруг, остаётся default", "claude-code", nil, "default машинного конфига", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r, err := resolveHarness(l, c.flag, func(k string) string { return c.env[k] })
			if err != nil {
				t.Fatal(err)
			}
			if r.Name != c.want || r.How != c.how {
				t.Fatalf("харнес %q (%s), жду %q (%s)", r.Name, r.How, c.want, c.how)
			}
			if c.note != "" && !strings.Contains(strings.Join(r.Notes, "\n"), c.note) {
				t.Fatalf("нет заметки %q в %v", c.note, r.Notes)
			}
		})
	}

	// default вне списка включённых пропускается, и молчать про это нельзя:
	// иначе харнес выглядел бы активным, а вердикт шёл бы без модели.
	proj := writeFile(t, t.TempDir(), "harness.local", "enabled = [\"codex\"]\n")
	narrowed, err := mergeLayers(dir, machine, proj)
	if err != nil {
		t.Fatal(err)
	}
	r, err := resolveHarness(narrowed, "", func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	if r.Name != "" || !strings.Contains(strings.Join(r.Notes, "\n"), "default claude-code не в списке включённых") {
		t.Fatalf("резолв %q, заметки %v", r.Name, r.Notes)
	}

	// Явное имя без профиля это отказ, а не тихий уход в default: команду
	// запустили с опечаткой, и делать вид, что она сработала, нельзя.
	if _, err := resolveHarness(l, "cursor", func(string) string { return "" }); err == nil {
		t.Fatal("жду ошибку на харнес без профиля")
	}
}

// TestCmdHarness: наблюдаемость. Резолв объясняется командой, а не чтением
// трёх конфигов руками, поэтому в выводе есть и чем определён харнес, и
// откуда взялся список, и маппинг с режимом делегирования.
func TestCmdHarness(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("DEVKIT_HOME", "")
	t.Setenv("CLAUDECODE", "1")
	t.Setenv("DEVKIT_HARNESS", "")
	fixNow(t, time.Date(2026, 8, 1, 12, 0, 0, 0, time.Local))
	out, err := cmdHarness("..", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, part := range []string{
		"харнес: claude-code (детект по переменной CLAUDECODE)",
		"включены: claude-code (машинного конфига ",
		"маппинг ярусов: mini = haiku, base = sonnet, pro = opus, max = fable (предложение профиля",
		"делегирование: native",
		"снимка нет",
		filepath.Join(".devkit", "quota", "claude-code.local"),
	} {
		if !strings.Contains(out, part) {
			t.Fatalf("в выводе нет %q:\n%s", part, out)
		}
	}

	// Ненастроенный контур говорит о себе сам: пустой список включённых и
	// команда, которой это чинится.
	writeFile(t, home, ".devkit/harness.local", "enabled = []\n")
	out, err = cmdHarness("..", "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "харнес: не определён") || !strings.Contains(out, "включены: никого") {
		t.Fatalf("ненастроенный контур молчит:\n%s", out)
	}

	// Битый профиль это отказ с именем файла и ключа, а не молча выключенная ось.
	broken := t.TempDir()
	writeFile(t, broken, "harness/claude-code.toml", "[detect]\n\n[rules]\nmode = \"import\"\n\n[delegate]\nmode = \"none\"\n\n[hooks]\n\n[quota]\n")
	t.Setenv("DEVKIT_HOME", broken)
	if _, err := cmdHarness("..", ""); err == nil || !strings.Contains(err.Error(), "claude-code.toml: [rules] нет ключа file") {
		t.Fatalf("ошибка %v", err)
	}
}

// TestHarnessDir: директорию профилей бинарь ищет сам, а не знает про свой
// devkit; не нашёл, значит говорит, где искал и чем это чинится.
func TestHarnessDir(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("DEVKIT_HOME", "")
	dir, err := harnessDir("..")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(dir) != "harness" {
		t.Fatalf("директория профилей %q", dir)
	}
	own := t.TempDir()
	writeFile(t, own, "harness/x.toml", "")
	t.Setenv("DEVKIT_HOME", own)
	if dir, err = harnessDir(".."); err != nil || dir != filepath.Join(own, "harness") {
		t.Fatalf("DEVKIT_HOME не победил: %q %v", dir, err)
	}
	t.Setenv("DEVKIT_HOME", "")
	if _, err := harnessDir(t.TempDir()); err == nil || !strings.Contains(err.Error(), "DEVKIT_HOME") {
		t.Fatalf("ошибка %v", err)
	}
}
