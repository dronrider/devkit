package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dronrider/devkit/internal/sessions"
	"github.com/dronrider/devkit/internal/stage"
	"github.com/dronrider/devkit/internal/taskform"
)

// Сессии синтетической машины. Восемь первых знаков у каждой свои: журнал
// агентов пишет сессию обрезанной, и сверка идёт по префиксу.
const (
	sessBatch = "b1a2c3d4-0000-4000-8000-000000000001"
	sessGroom = "c1a2c3d4-0000-4000-8000-000000000002"
	sessGoal  = "d1a2c3d4-0000-4000-8000-000000000003"
	sessTick  = "e1a2c3d4-0000-4000-8000-000000000004"
	sessTalk  = "f1a2c3d4-0000-4000-8000-000000000005"
	sessGone  = "a9a2c3d4-0000-4000-8000-000000000006"
)

// spendDay это день синтетического свода.
func spendDay(hour, min int) time.Time {
	return time.Date(2026, 9, 18, hour, min, 0, 0, time.Local)
}

// spendHead кладёт в подменный дом транскрипт головной сессии: ход на каждый
// названный момент.
func spendHead(t *testing.T, home, session string, turns map[time.Time]int) {
	t.Helper()
	dir := filepath.Join(home, ".claude", "projects", "slug")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	old := spendStamp
	defer func() { spendStamp = old }()
	var body strings.Builder
	i := 0
	for _, at := range spendSortedTimes(turns) {
		i++
		spendStamp = at
		body.WriteString(spendTurn(session+"-req-"+at.Format("1504"), turns[at], 10, 100))
	}
	if err := os.WriteFile(filepath.Join(dir, session+".jsonl"), []byte(body.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

// spendSortedTimes отдаёт моменты по возрастанию: порядок ходов в транскрипте
// тот же, что во времени.
func spendSortedTimes(turns map[time.Time]int) []time.Time {
	var out []time.Time
	for at := range turns {
		out = append(out, at)
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].Before(out[j-1]); j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// spendBind дописывает строку реестра чатов.
func spendBind(t *testing.T, home, session string, b sessions.Bind) {
	t.Helper()
	path := sessions.Path(home)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	line := sessions.Line(spendDay(5, 0), session, b, "startup")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(line); err != nil {
		t.Fatal(err)
	}
}

// spendEventLine дописывает строку журнала агентов: кто какую работу поднял.
func spendEventLine(t *testing.T, home, session string, at time.Time, text string) {
	t.Helper()
	path := filepath.Join(home, ".devkit", "agents.log")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	line := at.Format(sessions.Stamp) + " сессия " + session[:8] +
		" разряд subagent работа 1a2b3c событие запуск текст «" + text + "»\n"
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(line); err != nil {
		t.Fatal(err)
	}
}

// spendWatch кладёт запись реестра целей: какая сессия ведёт цикл цели.
func spendWatch(t *testing.T, home, goal, session string) {
	t.Helper()
	dir := filepath.Join(home, ".devkit", "goals")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "goal = " + goal + "\nroot = /tmp/synthetic\nsession = " + session + "\ncarrier = shell\n"
	if err := os.WriteFile(filepath.Join(dir, goal+"-slug.watch"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// spendMachine собирает синтетическую машину: пять головных сессий, реестр
// чатов, журнал агентов, реестр целей и запись этапа постановки.
func spendMachine(t *testing.T, root, home string) {
	t.Helper()
	// Диспетчер пачки: первый ход до всякой работы, дальше ходы под задачами.
	spendHead(t, home, sessBatch, map[time.Time]int{
		spendDay(10, 0): 7000, spendDay(10, 30): 5000, spendDay(11, 10): 3000,
	})
	spendEventLine(t, home, sessBatch, spendDay(10, 5), "Исполнитель XR-005")
	spendEventLine(t, home, sessBatch, spendDay(11, 0), "Ревью XR-001")
	spendBind(t, home, sessBatch, sessions.Bind{Project: "synthetic", Tree: root,
		Transcript: filepath.Join(home, ".claude", "projects", "slug", sessBatch+".jsonl")})

	// Груминг черновика: статья постановки по тексту работы в журнале агентов.
	spendHead(t, home, sessGroom, map[time.Time]int{spendDay(9, 10): 2000})
	spendEventLine(t, home, sessGroom, spendDay(9, 0), "Груминг черновика XR-009")
	spendBind(t, home, sessGroom, sessions.Bind{Project: "synthetic", Tree: root,
		Transcript: filepath.Join(home, ".claude", "projects", "slug", sessGroom+".jsonl")})

	// Виток цели: фон на ID цели по реестру целей.
	spendHead(t, home, sessGoal, map[time.Time]int{spendDay(8, 0): 1500})
	spendWatch(t, home, "XR-100", sessGoal)
	spendBind(t, home, sessGoal, sessions.Bind{Project: "synthetic", Tree: root, Carrier: "виток",
		Transcript: filepath.Join(home, ".claude", "projects", "slug", sessGoal+".jsonl")})

	// Безголовый тик без задачи и цели: фон машины.
	spendHead(t, home, sessTick, map[time.Time]int{spendDay(7, 0): 800})
	spendBind(t, home, sessTick, sessions.Bind{Project: "synthetic", Tree: root, Carrier: "дашборд",
		Transcript: filepath.Join(home, ".claude", "projects", "slug", sessTick+".jsonl")})

	// Разговор человека без привязки: вне статей.
	spendHead(t, home, sessTalk, map[time.Time]int{spendDay(6, 0): 600})
	spendBind(t, home, sessTalk, sessions.Bind{Project: "synthetic", Tree: root,
		Transcript: filepath.Join(home, ".claude", "projects", "slug", sessTalk+".jsonl")})

	// Сессия задачи без транскрипта: заход второго харнеса либо стёртый журнал.
	spendBind(t, home, sessGone, sessions.Bind{Task: "XR-005", Source: sessions.ByOrder, Project: "synthetic"})

	// Этап постановки живой записи: статья идёт на ID записи, а сессию несёт
	// сам этап.
	setupStage := stage.Stage{Kind: stage.Setup, Start: spendDay(12, 0), End: spendDay(12, 30),
		Note: "разбор записи", Session: sessTalk}
	if err := stage.Put(home, root, "XR-004", setupStage); err != nil {
		t.Fatal(err)
	}
	spendHead(t, home, sessTalk+"x", map[time.Time]int{})
}

// TestSpendItemsByTask: под этапами задачи стоят статьи «оркестрация» и
// «стенд» теми же колонками, а ход сессии пачки, за которым не стояло работы
// ни с одним ID, печатается строкой «оркестрация без привязки» (первая строка
// DoD DK-913, кейсы 1, 2 и 9).
func TestSpendItemsByTask(t *testing.T) {
	root := setup(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	old := timeNow
	defer func() { timeNow = old }()
	timeNow = func() time.Time { return spendDay(15, 0) }
	spendMachine(t, root, home)

	// След прогона стенда с числами, снятыми до сноса временного дома.
	path := filepath.Join(root, "docs", "tasks", "XR-005.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	mark := taskform.StandMark{Tree: "1a2b3c4d", Print: "ab12cd34", Base: "нет", Tier: "base",
		Repeats: 3, Scenarios: []string{"41"}, Turns: 12, Output: 4000, Input: 200, CacheRead: 9000}
	doc := taskform.InsertIntoSection(string(data), taskform.Verification,
		taskform.StandLine(mark, spendDay(13, 0), "зачтён"))
	if err := os.WriteFile(path, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}

	msg, err := cmdSpend(root, "XR-005")
	if err != nil {
		t.Fatalf("cmdSpend: %v", err)
	}
	want := []string{
		"- статья оркестрация: ходов 1, вывод 5.0k, свежий вход 10, чтение кэша 100, сессий 1",
		"- статья стенд: ходов 12, вывод 4.0k, свежий вход 200, чтение кэша 9.0k, прогонов 1",
		"- оркестрация без привязки: ходов 1, вывод 7.0k",
		"- нет данных, сессий без транскрипта 1: " + whyNoTranscript,
	}
	for _, w := range want {
		if !strings.Contains(msg, w) {
			t.Fatalf("в своде задачи нет строки %q:\n%s", w, msg)
		}
	}
	if strings.Contains(msg, "Исполнитель") || strings.Contains(msg, "Груминг") {
		t.Fatalf("наружу уехал текст работы:\n%s", msg)
	}
}

// TestSpendItemsPeriod: срез печатает статьи рядом с этапами, фон делится на
// цели и машину, а строка «вне статей» доводит сумму до расхода всех
// транскриптов среза (вторая строка DoD DK-913, кейсы 4, 5, 6, 7 и 10).
func TestSpendItemsPeriod(t *testing.T) {
	root := setup(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	old := timeNow
	defer func() { timeNow = old }()
	timeNow = func() time.Time { return spendDay(15, 0) }
	spendMachine(t, root, home)

	p, err := parseSpendPeriod("2026-09-01", "2026-09-20")
	if err != nil {
		t.Fatal(err)
	}
	msg, err := cmdSpendPeriod(root, p)
	if err != nil {
		t.Fatalf("cmdSpendPeriod: %v", err)
	}
	want := []string{
		// 7000+5000+3000+2000+1500+800+600 = 19900
		"токены за 2026-09-01..2026-09-20: ходов 7, вывод 19k",
		"- статья оркестрация: ходов 3, вывод 15k",
		"  - XR-005: ходов 1, вывод 5.0k",
		"  - XR-001: ходов 1, вывод 3.0k",
		"  - без привязки: ходов 1, вывод 7.0k",
		"- статья постановка: ходов 1, вывод 2.0k",
		"  - XR-009: ходов 1, вывод 2.0k",
		"- статья фон: ходов 2, вывод 2.3k",
		"  - XR-100: ходов 1, вывод 1.5k",
		"  - машина synthetic: ходов 1, вывод 800",
		"- вне статей: ходов 1, вывод 600",
		"- подгруппа LLD: ходов 1, вывод 3.0k, свежий вход 10, чтение кэша 100, задач 1, в медиану не входит",
	}
	for _, w := range want {
		if !strings.Contains(msg, w) {
			t.Fatalf("в срезе нет строки %q:\n%s", w, msg)
		}
	}
	if !strings.Contains(msg, "- нет данных: сессий без носителя в реестре 1") {
		t.Fatalf("сессия без носителя не названа:\n%s", msg)
	}
}

// TestSpendSetupStage: этап «постановка» живой записи относит ход головной
// сессии к статье «постановка» на ID записи, и строка «нет данных» про этап без
// номера работы рядом с ней не печатается (кейс 3).
func TestSpendSetupStage(t *testing.T) {
	root := setup(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	old := timeNow
	defer func() { timeNow = old }()
	timeNow = func() time.Time { return spendDay(15, 0) }
	spendMachine(t, root, home)
	// Ход сессии внутри этапа постановки записи XR-004.
	spendHead(t, home, sessTalk, map[time.Time]int{spendDay(6, 0): 600, spendDay(12, 10): 2500})

	msg, err := cmdSpend(root, "XR-004")
	if err != nil {
		t.Fatalf("cmdSpend: %v", err)
	}
	if !strings.Contains(msg, "- статья постановка: ходов 1, вывод 2.5k") {
		t.Fatalf("постановка записи не сведена статьёй:\n%s", msg)
	}
	if strings.Contains(msg, "- постановка 2026-09-18 12:00-12:30: нет данных") {
		t.Fatalf("этап постановки напечатан рядом со статьёй:\n%s", msg)
	}
}

// TestSpendStandWithoutTokens: прогон стенда без чисел (отметка, писанная до
// DK-913, и разведочный прогон без --task) в свод не входит вовсе.
func TestSpendStandWithoutTokens(t *testing.T) {
	root := setup(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(root, "docs", "tasks", "XR-005.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	mark := taskform.StandMark{Tree: "1a2b3c4d", Print: "ab12cd34", Base: "нет", Tier: "base",
		Repeats: 3, Scenarios: []string{"41"}}
	doc := taskform.InsertIntoSection(string(data), taskform.Verification,
		taskform.StandLine(mark, spendDay(13, 0), "зачтён"))
	if err := os.WriteFile(path, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	if u, runs := spendStands(root, "XR-005", spendPeriod{}); runs != 0 || !u.Empty() {
		t.Fatalf("отметка без чисел вошла в свод: %d прогонов, %+v", runs, u)
	}
}

// TestSpendSetupText: постановку в тексте работы узнают слова разбора записи,
// а «Виток цели XR-100» и «Исполнитель XR-005» это работа по коду и фон, и
// статья у них другая.
func TestSpendSetupText(t *testing.T) {
	setups := []string{"Груминг черновика XR-009", "Нарезка цели XR-100",
		"Интервью постановки XR-009", "Постановка задачи XR-005"}
	for _, text := range setups {
		if !spendSetupText(text) {
			t.Errorf("работа %q не узнана постановкой", text)
		}
	}
	others := []string{"Виток цели XR-100", "Исполнитель XR-005",
		"Ревью XR-001", "shipctl merge XR-005 целиком"}
	for _, text := range others {
		if spendSetupText(text) {
			t.Errorf("работа %q прочитана постановкой", text)
		}
	}
}

// TestSpendPeriodOpenHead: у среза без нижней границы своя голова строки:
// «токены за по 2026-09-18» читалось бы не по-русски.
func TestSpendPeriodOpenHead(t *testing.T) {
	root := setup(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	old := timeNow
	defer func() { timeNow = old }()
	timeNow = func() time.Time { return spendDay(15, 0) }
	spendMachine(t, root, home)

	p, err := parseSpendPeriod("", "2026-09-20")
	if err != nil {
		t.Fatal(err)
	}
	msg, err := cmdSpendPeriod(root, p)
	if err != nil {
		t.Fatalf("cmdSpendPeriod: %v", err)
	}
	if !strings.HasPrefix(msg, "токены по 2026-09-20: ") {
		t.Fatalf("голова среза без нижней границы:\n%s", strings.SplitN(msg, "\n", 2)[0])
	}
}

// TestSpendBlindSessionOnly: у задачи, от которой остались только слепая
// сессия без транскрипта и ход без привязки, свод не молчит. Молчание тут
// неотличимо от нулевого расхода, а это единственный сигнал про заход второго
// харнеса (DoD DK-913, возврат ревью).
func TestSpendBlindSessionOnly(t *testing.T) {
	root := setup(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	old := timeNow
	defer func() { timeNow = old }()
	timeNow = func() time.Time { return spendDay(15, 0) }
	spendBind(t, home, sessGone, sessions.Bind{Task: "XR-002", Source: sessions.ByOrder, Project: "synthetic"})

	msg, err := cmdSpend(root, "XR-002")
	if err != nil {
		t.Fatalf("cmdSpend: %v", err)
	}
	if strings.Contains(msg, "считать нечего") {
		t.Fatalf("слепая сессия съедена ранним выходом:\n%s", msg)
	}
	if !strings.Contains(msg, "- нет данных, сессий без транскрипта 1: "+whyNoTranscript) {
		t.Fatalf("причины у слепой сессии нет:\n%s", msg)
	}
	line, ok := spendTotalLine(root, "XR-002", spendDay(15, 0))
	if !ok {
		t.Fatal("итог закрытия не записан, строка «Токены» пропала вместе с сигналом")
	}
	if !strings.Contains(line, "нет данных, сессий без транскрипта 1") {
		t.Fatalf("итог закрытия молчит про слепую сессию: %s", line)
	}
}

// TestSpendPeriodKeepsBlindStages: срез без единого транскрипта, но с этапами
// без данных, печатает их числом, а не отговаривается пустотой.
func TestSpendPeriodKeepsBlindStages(t *testing.T) {
	root := setup(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	old := timeNow
	defer func() { timeNow = old }()
	timeNow = func() time.Time { return spendDay(15, 0) }
	st := stage.Stage{Kind: stage.Dev, Start: spendDay(10, 0), End: spendDay(11, 0),
		Note: "субагент второго харнеса, работа d4e5f60718a1b2c3e", Session: "",
		Work: "d4e5f60718a1b2c3e"}
	if err := stage.Put(home, root, "XR-005", st); err != nil {
		t.Fatal(err)
	}
	p, err := parseSpendPeriod("2026-09-01", "2026-09-20")
	if err != nil {
		t.Fatal(err)
	}
	msg, err := cmdSpendPeriod(root, p)
	if err != nil {
		t.Fatalf("cmdSpendPeriod: %v", err)
	}
	if strings.Contains(msg, "считать нечего") {
		t.Fatalf("этапы без данных съедены ранним выходом:\n%s", msg)
	}
	if !strings.Contains(msg, "этапов 1, без данных 1") {
		t.Fatalf("шапка молчит про этап без данных:\n%s", msg)
	}
}

// TestSpendSetupWithoutSession: у этапа «постановка» без сессии причина
// названа по существу. Про номер работы субагента тут говорить не о чем, его
// у постановки не бывает вовсе.
func TestSpendSetupWithoutSession(t *testing.T) {
	rows := spendRows(nil, []spendStage{
		{kind: stage.Setup, start: spendDay(10, 0)},
		{kind: stage.Dev, start: spendDay(11, 0)},
	})
	if rows[0].why != "сессии постановки в записи этапа нет, ход головной сессии к ней не привязать" {
		t.Fatalf("причина у постановки без сессии: %q", rows[0].why)
	}
	if rows[1].why != "номера работы в записи этапа нет" {
		t.Fatalf("причина у разработки без работы: %q", rows[1].why)
	}
}

// TestSpendPeriodCountsBlindSession: сессия, от которой осталась одна строка
// реестра чатов (заход второго харнеса: транскрипта нет, записи ~/.devkit/runs
// нет), видна в срезе за период и на пустом срезе, и рядом с чужой
// активностью. Потоковый разбор её не видит вовсе, потоков у неё нет
// (замечание ревью, круг 2).
func TestSpendPeriodCountsBlindSession(t *testing.T) {
	want := "- нет данных, сессий без транскрипта 1: " + whyNoTranscript

	t.Run("пустой срез", func(t *testing.T) {
		root := setup(t)
		home := t.TempDir()
		t.Setenv("HOME", home)
		old := timeNow
		defer func() { timeNow = old }()
		timeNow = func() time.Time { return spendDay(15, 0) }
		spendBind(t, home, sessGone, sessions.Bind{Task: "XR-600", Source: sessions.ByOrder,
			Project: "synthetic"})

		p, err := parseSpendPeriod("2026-09-01", "2026-09-20")
		if err != nil {
			t.Fatal(err)
		}
		msg, err := cmdSpendPeriod(root, p)
		if err != nil {
			t.Fatalf("cmdSpendPeriod: %v", err)
		}
		if strings.Contains(msg, "считать нечего") {
			t.Fatalf("слепая сессия съедена ранним выходом среза:\n%s", msg)
		}
		if !strings.Contains(msg, want) {
			t.Fatalf("в срезе нет строки %q:\n%s", want, msg)
		}
	})

	t.Run("рядом с чужой активностью", func(t *testing.T) {
		root := setup(t)
		home := t.TempDir()
		t.Setenv("HOME", home)
		old := timeNow
		defer func() { timeNow = old }()
		timeNow = func() time.Time { return spendDay(15, 0) }
		spendMachine(t, root, home)
		spendBind(t, home, sessGone, sessions.Bind{Task: "XR-600", Source: sessions.ByOrder,
			Project: "synthetic"})

		p, err := parseSpendPeriod("2026-09-01", "2026-09-20")
		if err != nil {
			t.Fatal(err)
		}
		msg, err := cmdSpendPeriod(root, p)
		if err != nil {
			t.Fatalf("cmdSpendPeriod: %v", err)
		}
		if !strings.Contains(msg, want) {
			t.Fatalf("слепая сессия потерялась среди чужой активности:\n%s", msg)
		}
	})
}
