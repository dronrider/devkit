package main

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

func fakeJudge(t *testing.T, args ...string) []string {
	t.Helper()
	path, err := filepath.Abs(filepath.Join("testdata", "fake-judge.sh"))
	if err != nil {
		t.Fatal(err)
	}
	return append([]string{"sh", path}, args...)
}

// judgeParams собирает прогон с судьёй: рабочая директория своя и живёт до
// конца теста, по calls.log судьи тесты смотрят, что и когда он читал.
func judgeParams(t *testing.T, scen []Scenario, first, second string, judge ...string) Params {
	t.Helper()
	p := params(t, scen, first, second)
	p.Judge = fakeJudge(t, judge...)
	p.JudgeModel = "fake-judge"
	p.Work = t.TempDir()
	return p
}

func judgeCalls(t *testing.T, p Params) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(p.Work, "judge", "cwd", "calls.log"))
	if err != nil {
		t.Fatalf("журнала вызовов судьи нет: %v", err)
	}
	return string(data)
}

func TestParseJudgeSection(t *testing.T) {
	s, err := parseScenario("j.md", `# судить

предмет: RULES.core.md

## Промпт

Скажи что-нибудь.

## Проверка

`+"```sh\ntrue\n```"+`

## Судья

критерий: «ответ написан от первого лица»

да: Я сделала.
да: «Я сверила.»
нет: «Сделано.»
`)
	if err != nil {
		t.Fatal(err)
	}
	j := s.Judge
	if j == nil {
		t.Fatal("секция «Судья» не разобрана")
	}
	if j.Input != judgeInputReply {
		t.Errorf("вход по умолчанию это «%s», вижу %q", judgeInputReply, j.Input)
	}
	if j.Criterion != "ответ написан от первого лица" {
		t.Errorf("критерий с ёлочками не снят: %q", j.Criterion)
	}
	if len(j.Yes) != 2 || j.Yes[0] != "Я сделала." || j.Yes[1] != "Я сверила." {
		t.Errorf("примеры «да»: %q", j.Yes)
	}
	if len(j.No) != 1 || j.No[0] != "Сделано." {
		t.Errorf("примеры «нет»: %q", j.No)
	}
}

func TestParseJudgeInputPath(t *testing.T) {
	s, err := parseScenario("j.md", "# судить\n\nпредмет: RULES.core.md\n\n## Промпт\n\nx\n\n## Проверка\n\ntrue\n\n## Судья\n\n"+
		"вход: docs/tasks/OB-021.md\nкритерий: x\nда: a\nнет: b\n")
	if err != nil {
		t.Fatal(err)
	}
	if s.Judge.Input != "docs/tasks/OB-021.md" {
		t.Errorf("вход путём: %q", s.Judge.Input)
	}
}

func TestParseJudgeErrors(t *testing.T) {
	head := "# судить\n\nпредмет: RULES.core.md\n\n## Промпт\n\nx\n\n## Проверка\n\ntrue\n\n## Судья\n\n"
	cases := []struct{ body, want string }{
		{"критерий: x\nда: a\n", "хотя бы один пример"},
		{"критерий: x\nнет: b\n", "хотя бы один пример"},
		{"да: a\nнет: b\n", "нет ключа «критерий»"},
		{"критерий: x\nда: a\nнет: b\nцвет: синий\n", "неизвестный ключ \"цвет\""},
		{"критерий: x\nда: a\nнет: b\nпросто строка\n", "жду «ключ: значение»"},
		{"критерий: x\nда: a\nнет: b\nвход: /etc/passwd\n", "не путь от корня проекта"},
		{"критерий: x\nда: a\nнет: b\nвход: ../secret\n", "не путь от корня проекта"},
		{"критерий:\nда: a\nнет: b\n", "пустое значение"},
	}
	for _, c := range cases {
		_, err := parseScenario("j.md", head+c.body)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("тело %q: жду ошибку с %q, вижу %v", c.body, c.want, err)
		}
	}
	// Номер строки в отказе считается от заголовка секции, а не от начала
	// файла с нуля: автор ищет строку по нему.
	_, err := parseScenario("j.md", head+"критерий: x\nда: a\nнет: b\nцвет: синий\n")
	if err == nil || !strings.Contains(err.Error(), "j.md:18:") {
		t.Errorf("номер строки отказа: %v", err)
	}
}

func TestJudgeVerdict(t *testing.T) {
	cases := []struct {
		answer string
		want   string
		ok     bool
	}{
		{"цитата: «разобрала»\nда", judgeYes, true},
		{"цитата: «разобрала»\nДа.\n\n", judgeYes, true},
		{"цитата\n«нет»", judgeNo, true},
		{"цитата\nскорее да, чем нет", "скорее да, чем нет", false},
		{"да\nцитата в конце", "цитата в конце", false},
		{"", "", false},
	}
	for _, c := range cases {
		got, ok := judgeVerdict(c.answer)
		if got != c.want || ok != c.ok {
			t.Errorf("ответ %q: жду (%q, %v), вижу (%q, %v)", c.answer, c.want, c.ok, got, ok)
		}
	}
}

func TestAssistantReplies(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	stream := write("stream", `{"type":"system","subtype":"init"}
{"type":"assistant","message":{"content":[{"type":"text","text":"Сейчас посмотрю."},{"type":"tool_use","name":"Read","input":{"file_path":"x"}}]}}
{"type":"user","message":{"content":[{"type":"tool_result","content":"тело файла"}]}}
{"type":"assistant","message":{"content":"Строка ответа целиком."}}
{"type":"result","result":"Строка ответа целиком."}
`)
	got, err := assistantReplies(stream)
	if err != nil {
		t.Fatal(err)
	}
	if got != "Сейчас посмотрю.\n\nСтрока ответа целиком." {
		t.Errorf("реплики из потока: %q", got)
	}
	if strings.Contains(got, "тело файла") {
		t.Errorf("результат инструмента попал в реплики: %q", got)
	}
	plain := write("plain", "промпт: x\nответ голым текстом\n")
	if got, _ = assistantReplies(plain); got != "промпт: x\nответ голым текстом" {
		t.Errorf("транскрипт без JSON отдаётся целиком, вижу %q", got)
	}
	silent := write("silent", `{"type":"system"}`+"\n"+`{"type":"result","result":"x"}`+"\n")
	if got, _ = assistantReplies(silent); got != "" {
		t.Errorf("поток без реплик ассистента это пустой ответ, вижу %q", got)
	}
	if _, err := assistantReplies(filepath.Join(dir, "нет")); err == nil {
		t.Error("пропавший транскрипт должен быть ошибкой")
	}
}

// Ответ по стилю против ответа мимо стиля: судья красит клетку, и строка
// выходит просадкой. Разбор судьи печатается в таблице по каждой клетке.
func TestJudgeColoursCell(t *testing.T) {
	p := judgeParams(t, scenarios(t, "judge"), "judge-no", "judge-yes", "word", "разобрала")
	report, failed := runOK(t, p)
	if !failed || !strings.Contains(flat(report), "0/3 3/3") || !strings.Contains(report, verdictDrop) {
		t.Fatalf("таблица:\n%s", report)
	}
	if !strings.Contains(report, "разбор судьи:") || !strings.Contains(report, "judge / judge-yes / повтор 1: цитата: «Я разобрала постановку и сверила её с кодом.» да") {
		t.Fatalf("разбора судьи в таблице нет:\n%s", report)
	}
	if !strings.Contains(report, "judge / judge-no / повтор 1: судья: нет") {
		t.Fatalf("красная клетка не названа вердиктом судьи:\n%s", report)
	}
}

func TestJudgeReadsFileInput(t *testing.T) {
	p := judgeParams(t, scenarios(t, "judge-file"), "judge-yes", "judge-no", "word", "разобрала")
	report, failed := runOK(t, p)
	if failed || !strings.Contains(flat(report), "3/3 0/3") || !strings.Contains(report, verdictGain) {
		t.Fatalf("таблица:\n%s", report)
	}
	calls := judgeCalls(t, p)
	if !strings.Contains(calls, "Текст:\nЯ разобрала постановку") {
		t.Fatalf("судья читал не файл проекта:\n%s", calls)
	}
}

// Судья слепой: свой дом без правил, рабочая директория вне проекта, без
// указателей стенда, а в промпте критерий и один текст. Калибровка идёт до
// первой сессии, и в её вызовах нет разметки примеров.
func TestJudgeIsBlind(t *testing.T) {
	// Стенд сам этих переменных не несёт, они подставляются, как если бы стенд
	// звали из живой сессии харнеса или из проверки сценария.
	t.Setenv("OBEY_TRANSCRIPT", "/чужой/транскрипт")
	t.Setenv("CLAUDE_CODE_SESSION_ID", "чужая-сессия")
	p := judgeParams(t, scenarios(t, "judge"), "judge-yes", "judge-yes", "word", "разобрала")
	p.Repeats = 1
	if _, failed := runOK(t, p); failed {
		t.Fatal("ждал зачтённую строку")
	}
	calls := judgeCalls(t, p)
	// Путь прогона стенд разрешает от симлинков (на macOS /var это /private/var),
	// и дом судьи сравнивается с разрешённым.
	work, err := filepath.EvalSymlinks(p.Work)
	if err != nil {
		t.Fatal(err)
	}
	blocks := strings.Split(calls, "---\n")
	blocks = blocks[:len(blocks)-1] // за последним разделителем пусто
	if n := len(blocks); n != 2+2 {
		t.Fatalf("жду 2 вызова калибровки и 2 клетки, вижу %d:\n%s", n, calls)
	}
	for i, b := range blocks {
		if !strings.Contains(b, "HOME="+filepath.Join(work, "judge", "home")) {
			t.Errorf("вызов %d не из дома судьи:\n%s", i, b)
		}
		if strings.Contains(b, "/project") {
			t.Errorf("вызов %d знает про проект прогона:\n%s", i, b)
		}
		if !strings.Contains(b, "harness=\n") && !strings.Contains(b, "harness= \n") {
			t.Errorf("вызову %d достались указатели стенда или переменные харнеса:\n%s", i, b)
		}
		if strings.Contains(b, "чужой") || strings.Contains(b, "чужая") {
			t.Errorf("вызов %d видит подставленные переменные:\n%s", i, b)
		}
		if strings.Contains(b, "да:") || strings.Contains(b, "нет:") {
			t.Errorf("вызов %d видит разметку примеров:\n%s", i, b)
		}
	}
	if !strings.Contains(blocks[0], "Я разобрала постановку") || !strings.Contains(blocks[1], "Я разобрал постановку") {
		t.Errorf("первые два вызова это примеры по порядку, вижу:\n%s", calls)
	}
	if strings.Contains(blocks[0], "Я разобрал постановку и") {
		t.Errorf("в вызове с примером «да» виден пример «нет»:\n%s", blocks[0])
	}
	for _, name := range []string{".claude", "CLAUDE.md"} {
		if pathExists(filepath.Join(p.Work, "judge", "home", name)) {
			t.Errorf("в доме судьи лежит %s", name)
		}
	}
	if !pathExists(filepath.Join(p.Work, "judge", "home", keychainRel)) {
		t.Error("дом судьи без ссылки на связку ключей, харнес в нём не авторизуется")
	}
}

// Расхождение с примером останавливает прогон кодом 2 до первой сессии: в
// тексте сценарий и пример, а команда прогона не звалась ни разу.
func TestCalibrationMismatchStops(t *testing.T) {
	p := judgeParams(t, scenarios(t, "judge"), "judge-yes", "judge-no", "word", "сверил ")
	_, err := Run(p)
	if err == nil {
		t.Fatal("ждал остановку на калибровке")
	}
	for _, want := range []string{"судья сценария judge разошёлся с примером «Я разобрала постановку и сверила её с кодом.»", "калибровка не сошлась", "сессии не поднимались"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("в тексте остановки нет %q: %v", want, err)
		}
	}
	if pathExists(filepath.Join(p.Work, "judge-judge-yes-1")) {
		t.Error("сессия поднималась после несошедшейся калибровки")
	}
	// Ответ не по форме на калибровке это тоже расхождение.
	p = judgeParams(t, scenarios(t, "judge"), "judge-yes", "judge-no", "vague")
	if _, err := Run(p); err == nil || !strings.Contains(err.Error(), "разошёлся с примером") {
		t.Errorf("ответ не по форме на калибровке: %v", err)
	}
}

// Ошибка вызова судьи это код 2 без отметки, а не красная клетка: и на
// калибровке, и посреди прогона.
func TestJudgeErrorStopsRun(t *testing.T) {
	for _, mode := range []string{"dead", "empty"} {
		p := judgeParams(t, scenarios(t, "judge"), "judge-yes", "judge-no", mode)
		_, err := Run(p)
		if err == nil {
			t.Fatalf("режим %s: ждал остановку", mode)
		}
		for _, want := range []string{"судья fake-judge недоступен на сценарии judge, калибровка", "отметка не писалась"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("режим %s: в тексте нет %q: %v", mode, want, err)
			}
		}
	}
	p := judgeParams(t, scenarios(t, "judge"), "judge-yes", "judge-no", "dead")
	_, err := Run(p)
	if !strings.Contains(err.Error(), "Not logged in") {
		t.Errorf("ошибка харнеса не дословно: %v", err)
	}
	p = judgeParams(t, scenarios(t, "judge"), "judge-yes", "judge-no", "slow")
	p.Timeout = 2 * time.Second
	if _, err := Run(p); err == nil || !strings.Contains(err.Error(), "не уложился") {
		t.Errorf("потолок времени судьи: %v", err)
	}
	// Отвал посреди прогона: калибровка прошла, судья упал на первой клетке.
	// Это остановка с номером повтора, а не красная клетка в таблице.
	p = judgeParams(t, scenarios(t, "judge"), "judge-yes", "judge-no", "dead-late", "разобрала")
	res, err := Run(p)
	if err == nil {
		t.Fatalf("отвал судьи на клетке прошёл как вердикт:\n%s", res.Report)
	}
	for _, want := range []string{"судья fake-judge недоступен на сценарии judge, повтор 1", "Not logged in", "отметка не писалась"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("в тексте остановки на клетке нет %q: %v", want, err)
		}
	}
	if res.Report != "" || len(res.Rows) != 0 {
		t.Errorf("после остановки таблицы быть не должно: %+v", res)
	}
}

// Ответ не по форме посреди прогона это красная клетка с примечанием: модель
// ответила, и это находка про критерий.
func TestJudgeOffFormIsRedCell(t *testing.T) {
	p := judgeParams(t, scenarios(t, "judge"), "judge-yes", "judge-yes", "vague-late", "разобрала")
	p.Repeats = 1
	report, failed := runOK(t, p)
	if !failed || !strings.Contains(report, "судья ответил не по форме: «скорее да, чем нет, хотя место спорное") {
		t.Fatalf("таблица:\n%s", report)
	}
	// Последняя строка ответа длиннее потолка oneLine, и примечание её режет:
	// абзац в таблицу и в отметку файла задачи не едет.
	for _, line := range strings.Split(report, "\n") {
		if !strings.Contains(line, "не по форме") {
			continue
		}
		if !strings.HasSuffix(line, "...»") || len([]rune(line)) > 400 {
			t.Fatalf("примечание не по форме не обрезано: %s", line)
		}
	}
}

// Судья зовётся только после зелёной проверки: на раскладке, где модель
// бездельничает и проверка красная, вызовов судьи сверх калибровки нет.
func TestJudgeSkippedOnRedCheck(t *testing.T) {
	// Вход «ответ»: транскрипт есть у любой сессии, и судью от красной клетки
	// удерживает только порядок «сначала проверка».
	p := judgeParams(t, scenarios(t, "judge-red"), "core", "core-green", "word", "разобрала")
	p.Repeats = 1
	report, _ := runOK(t, p)
	if !strings.Contains(flat(report), "0/1 0/1") {
		t.Fatalf("таблица:\n%s", report)
	}
	calls := judgeCalls(t, p)
	if n := strings.Count(calls, "---\n"); n != 2+1 {
		t.Errorf("жду 2 вызова калибровки и 1 клетку на зелёной проверке, вижу %d:\n%s", n, calls)
	}
	if !strings.Contains(report, "judge-red / core / повтор 1: проверка:") {
		t.Errorf("красная проверка на core названа не проверкой:\n%s", report)
	}
}

// Затравка --home-seed везёт судье только то, чем харнес авторизуется: файл
// учётных данных, связку и ключи авторизации из настроек. Всё остальное, что
// бы ни лежало в затравке, до дома судьи не доезжает: список закрыт с обеих
// сторон, и правила из незнакомого каталога не проскочат.
func TestJudgeHomeSeedWithoutRules(t *testing.T) {
	seed := t.TempDir()
	files := map[string]string{
		".claude/.credentials.json":     "ключи харнеса",
		".claude/settings.json":         `{"apiKeyHelper": "secretctl exec key", "env": {"X": "1"}, "hooks": {"PostToolUse": []}, "permissions": {"allow": ["Bash"]}}`,
		".claude/settings.local.json":   `{"permissions": {"allow": ["Bash"]}}`,
		".claude/CLAUDE.md":             "затравка",
		".claude/CLAUDE.local.md":       "затравка",
		".claude/agents/exec-medium.md": "затравка",
		".claude/skills/prose/SKILL.md": "затравка",
		".claude/commands/go.md":        "затравка",
		".claude/plugins/x/SKILL.md":    "затравка",
		".claude/unknown/rules.md":      "затравка",
		"CLAUDE.md":                     "затравка",
		"Library/Keychains/login":       "связка затравки",
	}
	for name, body := range files {
		path := filepath.Join(seed, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	p := judgeParams(t, scenarios(t, "judge"), "judge-yes", "judge-yes", "word", "разобрала")
	p.Repeats = 1
	p.HomeSeed = seed
	if _, failed := runOK(t, p); failed {
		t.Fatal("ждал зачтённую строку")
	}
	home := filepath.Join(p.Work, "judge", "home")
	creds, err := os.ReadFile(filepath.Join(home, ".claude", ".credentials.json"))
	if err != nil || string(creds) != "ключи харнеса" {
		t.Errorf("учётные данные затравки до дома судьи не доехали: %q, %v", creds, err)
	}
	kc, err := os.ReadFile(filepath.Join(home, keychainRel, "login"))
	if err != nil || string(kc) != "связка затравки" {
		t.Errorf("связка затравки до дома судьи не доехала: %q, %v", kc, err)
	}
	settings, err := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	if err != nil {
		t.Fatalf("ключи авторизации из настроек затравки не доехали: %v", err)
	}
	for _, want := range []string{"apiKeyHelper", "secretctl exec key", `"X": "1"`} {
		if !strings.Contains(string(settings), want) {
			t.Errorf("в настройках судьи нет %q: %s", want, settings)
		}
	}
	for _, drop := range []string{"hooks", "permissions"} {
		if strings.Contains(string(settings), drop) {
			t.Errorf("в настройки судьи уехало %q: %s", drop, settings)
		}
	}
	// Проверяется всё дерево дома, а не перечень: мутация «взять из затравки
	// всё» обязана краснеть на любом незнакомом каталоге.
	var got []string
	filepath.WalkDir(home, func(path string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			rel, _ := filepath.Rel(home, path)
			got = append(got, filepath.ToSlash(rel))
		}
		return nil
	})
	sort.Strings(got)
	want := []string{".claude/.credentials.json", ".claude/settings.json", "Library/Keychains/login"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("в доме судьи лежит лишнее из затравки: %v, жду %v", got, want)
	}
}

// Без судейских сценариев судья не собирается вовсе, а с ними без команды
// судьи прогон не начинается.
func TestJudgeCommandRequired(t *testing.T) {
	p := params(t, scenarios(t, "press"), "full", "core-green")
	p.Work = t.TempDir()
	if _, failed := runOK(t, p); failed {
		t.Fatal("ждал зачтённую строку")
	}
	if pathExists(filepath.Join(p.Work, "judge")) {
		t.Error("судья собран прогону без судейских сценариев")
	}
	p = params(t, scenarios(t, "judge"), "judge-yes", "judge-yes")
	if _, err := Run(p); err == nil || !strings.Contains(err.Error(), "команды судьи нет") {
		t.Errorf("прогон без команды судьи: %v", err)
	}
}
