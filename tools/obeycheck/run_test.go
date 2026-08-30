package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// devkitRoot отдаёт корень того дерева, в котором лежит сам тест. Боевой
// findDevkit сюда не годится: он первым делом смотрит на DEVKIT_HOME, а в
// сессиях машины эта переменная показывает на соседний worktree. Тест тогда
// читает чужие сценарии и чужую раскладку и зеленеет ровно там, где дерево от
// соседнего отличается.
func devkitRoot(t *testing.T) string {
	t.Helper()
	_, self, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("не узнал путь до собственного файла теста")
	}
	root := filepath.Dir(filepath.Dir(filepath.Dir(self)))
	if _, err := os.Stat(filepath.Join(root, devkitMarker)); err != nil {
		t.Fatalf("корень дерева теста не похож на devkit: %v", err)
	}
	return root
}

func fakeAgent(t *testing.T) []string {
	t.Helper()
	path, err := filepath.Abs(filepath.Join("testdata", "fake-agent.sh"))
	if err != nil {
		t.Fatal(err)
	}
	return []string{"sh", path}
}

func scenarios(t *testing.T, only ...string) []Scenario {
	t.Helper()
	s, err := loadScenarios(filepath.Join("testdata", "scenarios"), only)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func layout(name string) string { return filepath.Join("testdata", "layouts", name) }

func params(t *testing.T, scen []Scenario, first, second string) Params {
	t.Helper()
	return Params{
		Scenarios: scen,
		Layouts:   []string{layout(first), layout(second)},
		Repeats:   3,
		Agent:     fakeAgent(t),
		End:       endSession,
		AgentDef:  "exec-medium",
		Devkit:    devkitRoot(t),
		UserHome:  fakeHome(t),
		Timeout:   time.Minute,
	}
}

// flat склеивает таблицу в одну строку: тесты механики смотрят на числа и
// вердикт, а ширины колонок это дело таблицы и её собственного теста.
func flat(s string) string { return strings.Join(strings.Fields(s), " ") }

func runOK(t *testing.T, p Params) (string, bool) {
	t.Helper()
	report, regression, err := Run(p)
	if err != nil {
		t.Fatal(err)
	}
	return report, regression
}

// Раскладка, где модель работает, против такой же: провалов нет ни там, ни там.
func TestHoldsOnBothLayouts(t *testing.T) {
	report, regression := runOK(t, params(t, scenarios(t, "press"), "full", "core-green"))
	if regression {
		t.Fatalf("регрессии быть не должно:\n%s", report)
	}
	if !strings.Contains(flat(report), "3/3 3/3") || !strings.Contains(report, verdictHolds) {
		t.Fatalf("таблица:\n%s", report)
	}
	if !strings.Contains(report, "full") || !strings.Contains(report, "core-green") {
		t.Fatalf("в шапке нет имён раскладок:\n%s", report)
	}
}

// На второй раскладке модель бездельничает: это регрессия и код возврата 1.
func TestRegressionOnSecondLayout(t *testing.T) {
	report, regression := runOK(t, params(t, scenarios(t, "press"), "full", "core"))
	if !regression {
		t.Fatalf("ждал регрессию:\n%s", report)
	}
	if !strings.Contains(flat(report), "3/3 0/3") || !strings.Contains(report, verdictRegress) {
		t.Fatalf("таблица:\n%s", report)
	}
	if !strings.Contains(report, "первый красный прогон") {
		t.Fatalf("нет причины красноты:\n%s", report)
	}
}

// Красный на обеих раскладках это находка про само правило, а не регрессия:
// код возврата такой прогон не поднимает.
func TestBothRedIsNotRegression(t *testing.T) {
	report, regression := runOK(t, params(t, scenarios(t, "press"), "core", "core"))
	if regression {
		t.Fatalf("красный на обеих не должен считаться регрессией:\n%s", report)
	}
	if !strings.Contains(report, verdictBothRed) {
		t.Fatalf("таблица:\n%s", report)
	}
}

// Считаются все k повторов, а не первый: модель, зелёная на двух повторах из
// трёх, даёт 2/3 и регрессию против полной раскладки.
func TestEveryRepeatCounted(t *testing.T) {
	report, regression := runOK(t, params(t, scenarios(t, "press"), "full", "flaky"))
	if !regression {
		t.Fatalf("ждал регрессию:\n%s", report)
	}
	if !strings.Contains(flat(report), "3/3 2/3") {
		t.Fatalf("таблица:\n%s", report)
	}
}

// Зелёность решает проверка, а не код возврата команды прогона: модель может
// выйти с ошибкой, сделав работу.
func TestAgentExitCodeIgnored(t *testing.T) {
	report, regression := runOK(t, params(t, scenarios(t, "press"), "angry", "core"))
	if !regression || !strings.Contains(flat(report), "3/3 0/3") {
		t.Fatalf("таблица:\n%s", report)
	}
}

// Окружение прогона: временный HOME, синтетический проект под гитом с доской,
// кодом и хуками devkit, фиктивный origin, раскладка на месте. Живой HOME при
// этом не подставляется даже случайно.
func TestRunEnvironment(t *testing.T) {
	p := params(t, scenarios(t, "env"), "full", "core")
	p.Repeats = 1
	p.Work = t.TempDir()
	p.Keep = true
	report, regression := runOK(t, p)
	if regression {
		t.Fatalf("проверка окружения не прошла:\n%s", report)
	}
	// Директория прогона живёт по разрешённому пути: стенд снимает симлинки,
	// чтобы pwd проекта сходился с тем, что печатает git.
	work, err := filepath.EvalSymlinks(p.Work)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(work, "env-full-1")
	data, err := os.ReadFile(filepath.Join(dir, "project", "env.txt"))
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	if !strings.Contains(got, "HOME="+filepath.Join(dir, "home")) {
		t.Fatalf("HOME прогона не временный:\n%s", got)
	}
	if home := os.Getenv("HOME"); home != "" && strings.Contains(got, "HOME="+home+"\n") {
		t.Fatalf("в прогон уехал живой HOME:\n%s", got)
	}
	for _, want := range []string{
		filepath.Join(dir, "project", "rules", "RULES.md"),                       // раскладка легла в проект
		filepath.Join(dir, "home", ".claude", "CLAUDE.md"),                       // и её часть для HOME
		filepath.Join(dir, "home", ".claude", "settings.json"),                   // хуки харнеса
		filepath.Join(dir, "home", ".claude", "agents", "exec-medium.md"),        // определения субагентов
		filepath.Join(dir, "home", ".claude", "skills", "proofread", "SKILL.md"), // скиллы
		filepath.Join(dir, "origin.git", "HEAD"),                                 // фиктивный origin
		filepath.Join(dir, "project", ".devkit"),                                 // журнал запусков утилит
		filepath.Join(dir, "transcript"),
	} {
		if _, err := os.Stat(want); err != nil {
			t.Errorf("нет %s: %v", want, err)
		}
	}
	tr, err := os.ReadFile(filepath.Join(dir, "transcript"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(tr), "промпт: Осмотрись в проекте") {
		t.Fatalf("транскрипт не собрался: %s", tr)
	}
}

// Субагентский конец: промпт уходит обёрнутым, а сценарий, объявленный
// сессионным, на этом конце пропускается.
func TestSubagentEnd(t *testing.T) {
	p := params(t, scenarios(t, "press", "session-only"), "full", "core")
	p.Repeats = 1
	p.End = endSub
	p.Work = t.TempDir()
	p.Keep = true
	report, _ := runOK(t, p)
	if !strings.Contains(report, "пропущен") {
		t.Fatalf("сессионный сценарий не пропущен:\n%s", report)
	}
	tr, err := os.ReadFile(filepath.Join(p.Work, "press-full-1", "transcript"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(tr), "Спавни субагента с определением exec-medium") {
		t.Fatalf("промпт не обёрнут для субагентского конца: %s", tr)
	}
	if _, err := os.Stat(filepath.Join(p.Work, "session-only-full-1")); err == nil {
		t.Fatal("пропущенный сценарий всё-таки гонялся")
	}
}

// Прогон, не уложившийся в таймаут, это красный прогон с внятной причиной, а не
// вечное ожидание: на полном проходе в двести сессий одна зависшая съедает ночь.
func TestTimeoutIsRed(t *testing.T) {
	p := params(t, scenarios(t, "press"), "slow", "core")
	p.Repeats = 1
	p.Timeout = 200 * time.Millisecond
	report, regression := runOK(t, p)
	if regression {
		t.Fatalf("оба угла красные, регрессии тут нет:\n%s", report)
	}
	if !strings.Contains(report, "не уложился") {
		t.Fatalf("причина таймаута не названа:\n%s", report)
	}
}

// Проба перед полным проходом: команда прогона, которая не поднимает сессии,
// ловится одной сессией, а не двумястами. Ради этого проба и заведена: молча
// красная (а на проверках-отрицаниях зелёная) таблица стоит сотни долларов.
func TestPreflightCatchesDeadAgent(t *testing.T) {
	p := params(t, scenarios(t, "press"), "broken", "core")
	p.Repeats = 1
	p.Preflight = true
	_, _, err := Run(p)
	if err == nil || !strings.Contains(err.Error(), "пробная сессия") {
		t.Fatalf("ждал отказ на пробе, получил: %v", err)
	}
}

func TestPreflightPassesOnLiveAgent(t *testing.T) {
	p := params(t, scenarios(t, "press"), "full", "core")
	p.Repeats = 1
	p.Preflight = true
	report, regression := runOK(t, p)
	if !regression || !strings.Contains(flat(report), "1/1 0/1") {
		t.Fatalf("после пробы прогон должен идти как обычно:\n%s", report)
	}
}

// Зелёная проверка при упавшей команде прогона не меняет счёт (решает
// проверка), но и не проходит молча: такой зеленью выглядит проверка, которая
// зеленеет на сессии, которой не было.
func TestSuspiciousGreenIsNamed(t *testing.T) {
	p := params(t, []Scenario{{
		ID: "always-green", Title: "проверка на отрицание", End: endAny,
		Prompt: "неважно", Check: "true",
	}}, "angry", "angry")
	p.Repeats = 1
	report, regression := runOK(t, p)
	if regression {
		t.Fatalf("регрессии тут нет:\n%s", report)
	}
	if !strings.Contains(report, "зелёной проверкой при упавшей команде") {
		t.Fatalf("подозрительная зелень не названа:\n%s", report)
	}
}

// Затравка HOME кладётся до раскладки: машинная обвязка (авторизация харнеса)
// приезжает во временный HOME, а раскладка правил вправе её перекрыть своим
// home/. Без затравки живой прогон упирается в неавторизованный HOME.
func TestHomeSeed(t *testing.T) {
	seed := t.TempDir()
	if err := os.MkdirAll(filepath.Join(seed, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		".claude/.credentials.json": "ключи харнеса",
		".claude/CLAUDE.md":         "затравочная точка правил",
	} {
		if err := os.WriteFile(filepath.Join(seed, filepath.FromSlash(name)), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	p := params(t, scenarios(t, "press"), "full", "core")
	p.Repeats = 1
	p.HomeSeed = seed
	p.Work = t.TempDir()
	p.Keep = true
	if _, _, err := Run(p); err != nil {
		t.Fatal(err)
	}
	work, err := filepath.EvalSymlinks(p.Work)
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(work, "press-full-1", "home", ".claude")
	creds, err := os.ReadFile(filepath.Join(home, ".credentials.json"))
	if err != nil || string(creds) != "ключи харнеса" {
		t.Fatalf("затравка не доехала во временный HOME: %q, %v", creds, err)
	}
	// у раскладки full своя глобальная точка правил, она затравочную перекрывает
	rules, err := os.ReadFile(filepath.Join(home, "CLAUDE.md"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(rules), "затравочная") {
		t.Fatalf("раскладка не перекрыла затравку: %q", rules)
	}
}

// Готовый дом со своими скиллами раскладку перекрывает целиком, тем же
// порядком, что и для определений субагентов (DK-546): каталог .claude/skills
// уже занят затравкой, и copyTree из kit/skills в него не идёт.
func TestHomeSeedOverridesSkills(t *testing.T) {
	seed := t.TempDir()
	skill := filepath.Join(seed, ".claude", "skills", "proofread")
	if err := os.MkdirAll(skill, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skill, "SKILL.md"), []byte("затравочный скилл"), 0o644); err != nil {
		t.Fatal(err)
	}
	p := params(t, scenarios(t, "press"), "full", "core")
	p.Repeats = 1
	p.HomeSeed = seed
	p.Work = t.TempDir()
	p.Keep = true
	if _, _, err := Run(p); err != nil {
		t.Fatal(err)
	}
	work, err := filepath.EvalSymlinks(p.Work)
	if err != nil {
		t.Fatal(err)
	}
	skillFile := filepath.Join(work, "press-full-1", "home", ".claude", "skills", "proofread", "SKILL.md")
	got, err := os.ReadFile(skillFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "затравочный скилл" {
		t.Fatalf("раскладка перезаписала затравочный скилл: %q", got)
	}
	// devkit сам по себе привозит другие скиллы (board-task и подобные), их
	// затравка не заводила: раз каталог .claude/skills занят, раскладка
	// уступает ему целиком, а не докладывает недостающее рядом.
	if _, err := os.Stat(filepath.Join(work, "press-full-1", "home", ".claude", "skills", "board-task")); err == nil {
		t.Fatal("раскладка докладывала скиллы devkit в занятый затравкой каталог")
	}
}

func TestHomeSeedMissing(t *testing.T) {
	p := params(t, scenarios(t, "press"), "full", "core")
	p.Repeats = 1
	p.HomeSeed = filepath.Join(t.TempDir(), "нет-такой")
	if _, _, err := Run(p); err == nil || !strings.Contains(err.Error(), "затравки HOME") {
		t.Fatalf("ждал ошибку про затравку, получил: %v", err)
	}
}

func TestSetupFailureStopsRun(t *testing.T) {
	p := params(t, []Scenario{{
		ID: "broken", Title: "сломанная подготовка", End: endAny,
		Prompt: "неважно", Setup: "exit 7", Check: "true",
	}}, "full", "core")
	p.Repeats = 1
	if _, _, err := Run(p); err == nil || !strings.Contains(err.Error(), "подготовка") {
		t.Fatalf("ждал ошибку про подготовку, получил: %v", err)
	}
}

func TestAgentCommandNotFound(t *testing.T) {
	p := params(t, scenarios(t, "press"), "full", "core")
	p.Repeats = 1
	p.Agent = []string{filepath.Join(t.TempDir(), "нет-такой-команды")}
	if _, _, err := Run(p); err == nil || !strings.Contains(err.Error(), "не запустилась") {
		t.Fatalf("ждал ошибку про команду прогона, получил: %v", err)
	}
}

func TestValidate(t *testing.T) {
	cases := []struct {
		name  string
		patch func(p *Params)
		want  string
	}{
		{"одна раскладка", func(p *Params) { p.Layouts = p.Layouts[:1] }, "ровно две раскладки"},
		{"нет раскладки", func(p *Params) { p.Layouts[1] = layout("нет-такой") }, "нет или это не директория"},
		{"ноль повторов", func(p *Params) { p.Repeats = 0 }, "хотя бы один"},
		{"нет команды", func(p *Params) { p.Agent = nil }, "команда прогона"},
		{"чужой конец", func(p *Params) { p.End = "оба" }, "конец прогона"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := params(t, scenarios(t, "press"), "full", "core")
			c.patch(&p)
			_, _, err := Run(p)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("ждал %q, получил: %v", c.want, err)
			}
		})
	}
}

// TestRunLog: журнал пишется в .devkit/log репозитория, без .devkit не заводится.
func TestRunLog(t *testing.T) {
	root := t.TempDir()
	if _, err := gitOut(root, "init", "-q", "-b", "main"); err != nil {
		t.Fatal(err)
	}
	logRun(root, 0)
	if _, err := os.Stat(filepath.Join(root, ".devkit", "log")); err == nil {
		t.Fatal("журнал не должен заводиться без .devkit")
	}
	if err := os.Mkdir(filepath.Join(root, ".devkit"), 0o755); err != nil {
		t.Fatal(err)
	}
	logRun(root, 1)
	data, err := os.ReadFile(filepath.Join(root, ".devkit", "log"))
	if err != nil {
		t.Fatalf("журнал не записан: %v", err)
	}
	if !strings.Contains(string(data), "\tobeycheck\trun\t1\n") {
		t.Fatalf("строки журнала: %q", data)
	}
}

// environ() фиксирует file-бэкенд secretctl: на macOS по умолчанию secretctl
// читает Keychain, куда подготовка сценария ничего не пишет, и секрет остаётся
// невидим. Стенд оперирует файлами-маркерами во временном HOME, file-бэкенд их
// читает. Заодно проверяются инварианты очистки: временный HOME и вычищенные
// CLAUDE*.
func TestEnvironPinsSecretctlBackend(t *testing.T) {
	e := &runEnv{
		Root:       t.TempDir(),
		Home:       filepath.Join(t.TempDir(), "home"),
		Project:    filepath.Join(t.TempDir(), "project"),
		Origin:     filepath.Join(t.TempDir(), "origin.git"),
		Transcript: filepath.Join(t.TempDir(), "transcript"),
		Seed:       "deadbeef",
	}
	t.Setenv("CLAUDE_FOO", "leak")
	have := e.environ("/devkit", "full", "11-secretctl", 1)
	joined := "\n" + strings.Join(have, "\n") + "\n"
	if !strings.Contains(joined, "\nSECRETCTL_BACKEND=file\n") {
		t.Fatalf("SECRETCTL_BACKEND не зафиксирован в file:\n%s", joined)
	}
	if !strings.Contains(joined, "\nHOME="+e.Home+"\n") {
		t.Fatalf("временный HOME не выставлен:\n%s", joined)
	}
	for _, kv := range have {
		name, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(name, "CLAUDE") {
			t.Fatalf("в прогон уехала CLAUDE*-переменная: %s", kv)
		}
	}
}
