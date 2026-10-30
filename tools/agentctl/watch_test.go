package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dronrider/devkit/internal/sessions"
)

// watchEntries читает реестр сторожка из подставного дома.
func watchEntries(t *testing.T, home string) []string {
	t.Helper()
	names, err := filepath.Glob(filepath.Join(home, ".devkit", "goals", "*.watch"))
	if err != nil {
		t.Fatal(err)
	}
	return names
}

func TestWatchRegister(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	root := t.TempDir()
	goalFile(t, root, "T-100", goalText("бюджет: week_all <= 25\n", ""))
	now := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)

	watchRegister(root, "docs/tasks/T-100.md", now)
	names := watchEntries(t, home)
	if len(names) != 1 {
		t.Fatalf("записей в реестре %d, ждали одну: %v", len(names), names)
	}
	body, err := os.ReadFile(names[0])
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, want := range []string{
		"goal = T-100",
		"root = " + root,
		"file = " + filepath.Join(root, "docs", "tasks", "T-100.md"),
		"seen = 2026-08-07T12:00:00",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("в записи нет строки %q:\n%s", want, text)
		}
	}
}

func TestWatchRegisterClearsStopMark(t *testing.T) {
	// Гейт стоит в начале витка, и сам его вызов это движение цикла: отметка
	// прошлого стопа после него не живёт, иначе сторожок молчал бы про
	// следующий стоп той же цели.
	home := t.TempDir()
	t.Setenv("HOME", home)
	root := t.TempDir()
	goalFile(t, root, "T-100", goalText("бюджет: week_all <= 25\n", ""))
	now := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)

	watchRegister(root, "docs/tasks/T-100.md", now)
	name := watchEntries(t, home)[0]
	if err := os.WriteFile(name, []byte("goal = T-100\nstopped = 2026-08-07T11:00:00\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	watchRegister(root, "docs/tasks/T-100.md", now.Add(time.Hour))
	body, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "stopped") {
		t.Errorf("отметка стопа пережила гейт витка:\n%s", body)
	}
	if !strings.Contains(string(body), "seen = 2026-08-07T13:00:00") {
		t.Errorf("время витка не обновилось:\n%s", body)
	}
}

func TestWatchRegisterSkipsMissingGoal(t *testing.T) {
	// Цели нет, значит и надзирать не за чем: пустая запись в реестре стоила бы
	// сторожку ложного зова.
	home := t.TempDir()
	t.Setenv("HOME", home)
	root := t.TempDir()
	watchRegister(root, "docs/tasks/T-404.md", time.Now())
	if names := watchEntries(t, home); len(names) != 0 {
		t.Errorf("в реестре появилась запись без файла цели: %v", names)
	}
}

// TestSpendRegistersGoal: гейт бюджета отмечает цель в реестре сторожка. Тест
// живой, через запуск команды: реестр заполняет главный разбор, а не cmdSpend,
// и в юните эта проводка не видна.
func TestSpendRegistersGoal(t *testing.T) {
	home := t.TempDir()
	root := writeBoard(t)
	goalFile(t, root, "T-100", goalText("бюджет: week_all <= 25\n", ""))
	cmd := exec.Command("go", "run", ".", "-C", root, "spend", "--goal", "docs/tasks/T-100.md")
	cmd.Env = append(os.Environ(), "HOME="+home)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("spend: %v\n%s", err, out)
	}
	names := watchEntries(t, home)
	if len(names) != 1 {
		t.Fatalf("гейт не отметил цель в реестре сторожка: %v", names)
	}
	body, err := os.ReadFile(names[0])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "goal = T-100") ||
		!strings.Contains(string(body), "root = "+root) {
		t.Fatalf("запись реестра не о той цели:\n%s", body)
	}
}

// TestSpendKeepsForeignKeys: чужие ключи записи переживают гейт. В записи живёт
// не только гейт: сторожок ставит туда отметку стопа, а перезапуск витка
// (DK-169) положит счётчик попыток, и гейт стоит в начале каждого витка, так
// что перезапись записи целиком стирала бы счётчик почти сразу.
func TestSpendKeepsForeignKeys(t *testing.T) {
	home := t.TempDir()
	root := writeBoard(t)
	goalFile(t, root, "T-100", goalText("бюджет: week_all <= 25\n", ""))
	dir := filepath.Join(home, ".devkit", "goals")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	entry := filepath.Join(dir, "T-100-"+watchSlug(root)+".watch")
	if err := os.WriteFile(entry, []byte("goal = T-100\nseen = 2026-08-07T11:00:00\n"+
		"stopped = 2026-08-07T11:40:00\nretries = 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("go", "run", ".", "-C", root, "spend", "--goal", "docs/tasks/T-100.md")
	cmd.Env = append(os.Environ(), "HOME="+home)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("spend: %v\n%s", err, out)
	}
	body, err := os.ReadFile(entry)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "retries = 2") {
		t.Errorf("гейт потерял чужой ключ записи:\n%s", body)
	}
	if strings.Contains(string(body), "stopped") {
		t.Errorf("отметка стопа пережила гейт витка:\n%s", body)
	}
	if strings.Contains(string(body), "seen = 2026-08-07T11:00:00") {
		t.Errorf("время витка не обновилось:\n%s", body)
	}
}

func TestWatchSlugKeepsProjectsApart(t *testing.T) {
	// Имя директории у проектов совпадает сплошь и рядом (клон и worktree), а
	// запись реестра у каждого своя.
	a := watchSlug("/Users/x/projects/proj")
	b := watchSlug("/Users/x/work/proj")
	if a == b {
		t.Errorf("разные корни дали одно имя записи: %s", a)
	}
	if strings.ContainsAny(a, "/. ") {
		t.Errorf("в имени записи остались символы пути: %s", a)
	}
}

// liveChat пишет в подставной дом живую сессию чата: строку в реестре чатов
// со свежим транскриптом. Возврат это её sid и путь транскрипта.
func liveChat(t *testing.T, home string) (string, string) {
	t.Helper()
	sid := "chat-1"
	tr := filepath.Join(t.TempDir(), "chat.jsonl")
	if err := os.WriteFile(tr, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".devkit"), 0o755); err != nil {
		t.Fatal(err)
	}
	line := "2026-10-30T12:00:00 сессия " + sid + " задача T-100 транскрипт " + tr + "\n"
	if err := os.WriteFile(filepath.Join(home, ".devkit", "sessions.log"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	return sid, tr
}

// TestWatchRegisterKeepsLiveChatEntry: живой чат ведёт запись реестра сам, и
// гейт чужого витка её не забирает: сессия, носитель и маркер остаются у чата,
// а движение цели отмечается своим полем seen (DK-1226).
func TestWatchRegisterKeepsLiveChatEntry(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(sessions.SessionEnv, "turn-9")
	t.Setenv(shellEnv, "T-100")
	root := t.TempDir()
	goalFile(t, root, "T-100", goalText("бюджет: week_all <= 25\n", ""))
	sid, _ := liveChat(t, home)
	dir := filepath.Join(home, ".devkit", "goals")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	entry := filepath.Join(dir, "T-100-"+watchSlug(root)+".watch")
	foreign := "goal = T-100\nroot = " + root + "\nsession = " + sid +
		"\ncarrier = chat\nmarker = wait-human\nmarker_by = chat\nstopped = 2026-10-29T23:00:00\n"
	if err := os.WriteFile(entry, []byte(foreign), 0o644); err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	watchRegister(root, "docs/tasks/T-100.md", now)
	body, err := os.ReadFile(entry)
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, want := range []string{
		"session = " + sid,
		"carrier = chat",
		"marker = wait-human",
		"stopped = 2026-10-29T23:00:00",
		"seen = " + now.Format(watchStamp),
	} {
		if !strings.Contains(text, want) {
			t.Errorf("в записи нет строки %q:\n%s", want, body)
		}
	}
	if strings.Contains(text, "turn-9") || strings.Contains(text, carrierShell) {
		t.Errorf("гейт вписал в чужую живую запись свою сессию или носителя:\n%s", body)
	}
}

// TestWatchRegisterTakesOrphanChatEntry: осиротевшая запись, чья сессия умерла,
// берётся гейтом как раньше: подъём осиротевшего цикла это путь тика, и запись
// обязана перейти новому носителю.
func TestWatchRegisterTakesOrphanChatEntry(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(sessions.SessionEnv, "turn-9")
	t.Setenv(shellEnv, "T-100")
	root := t.TempDir()
	goalFile(t, root, "T-100", goalText("бюджет: week_all <= 25\n", ""))
	sid, tr := liveChat(t, home)
	// Транскрипт остыл за пределы окна живости, и страховки не видят чата.
	stale := time.Now().Add(-2 * sessions.SessionLive)
	if err := os.Chtimes(tr, stale, stale); err != nil {
		t.Fatal(err)
	}
	entry := filepath.Join(home, ".devkit", "goals", "T-100-"+watchSlug(root)+".watch")
	if err := os.MkdirAll(filepath.Dir(entry), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(entry, []byte("goal = T-100\nsession = "+sid+"\ncarrier = chat\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	watchRegister(root, "docs/tasks/T-100.md", time.Now())
	body, err := os.ReadFile(entry)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"session = turn-9", "carrier = shell"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("осиротевшая запись не перешла витку, нет строки %q:\n%s", want, body)
		}
	}
}

// TestWatchRegisterNamesSessionAndCarrier: запись называет сессию цикла и его
// носителя. По ним держатель хода (hooks/goal-hold.py) отличает свою сессию от
// соседних, а цикл в чате от витка оболочки, которому ход отдавать положено.
func TestWatchRegisterNamesSessionAndCarrier(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(sessions.SessionEnv, "sess-1")
	root := t.TempDir()
	goalFile(t, root, "T-100", goalText("бюджет: week_all <= 25\n", ""))

	watchRegister(root, "docs/tasks/T-100.md", time.Now())
	body, err := os.ReadFile(watchEntries(t, home)[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"session = sess-1", "carrier = chat"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("в записи нет строки %q:\n%s", want, body)
		}
	}
}

// TestWatchRegisterKnowsTheShell: виток под оболочкой goal-run помечен своим
// носителем. Ход такого витка кончается маркером, и держать его нельзя.
func TestWatchRegisterKnowsTheShell(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(shellEnv, "T-100")
	root := t.TempDir()
	goalFile(t, root, "T-100", goalText("бюджет: week_all <= 25\n", ""))

	watchRegister(root, "docs/tasks/T-100.md", time.Now())
	body, err := os.ReadFile(watchEntries(t, home)[0])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "carrier = shell") {
		t.Errorf("виток оболочки записан чатом:\n%s", body)
	}
}

// TestWatchMarkerWritesStop: стоп-маркер витка ложится в запись реестра. Это
// единственное, чем сессия чата вправе кончить работу над целью, и держателю
// хода нужен машинный след, переживающий конец хода.
func TestWatchMarkerWritesStop(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	root := t.TempDir()
	goalFile(t, root, "T-100", goalText("бюджет: week_all <= 25\n", ""))
	watchRegister(root, "docs/tasks/T-100.md", time.Now())
	name := watchEntries(t, home)[0]

	watchMarker(root, "docs/tasks/T-100.md", "wait-human")
	body, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "marker = wait-human") {
		t.Errorf("стоп-маркер не лёг в запись:\n%s", body)
	}
	// Продолжение работы маркер снимает: цикл едет дальше, и держатель хода
	// обязан снова держать ход.
	watchMarker(root, "docs/tasks/T-100.md", "continue")
	body, err = os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "marker") {
		t.Errorf("маркер стопа пережил продолжение цикла:\n%s", body)
	}
}

// TestWatchMarkerNamesItsCarrier: стоп-маркер ложится с признаком носителя,
// а текст маркера не меняется: печать маркера это протокол headless-витка
// (DK-1226). По признаку держатель хода отличает свой стоп от стопа витка
// оболочки.
func TestWatchMarkerNamesItsCarrier(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	root := t.TempDir()
	goalFile(t, root, "T-100", goalText("бюджет: week_all <= 25\n", ""))
	watchRegister(root, "docs/tasks/T-100.md", time.Now())
	name := watchEntries(t, home)[0]

	t.Setenv(shellEnv, "T-100")
	watchMarker(root, "docs/tasks/T-100.md", "wait-human")
	body, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"marker = wait-human", "marker_by = shell"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("в записи витка оболочки нет строки %q:\n%s", want, body)
		}
	}

	t.Setenv(shellEnv, "")
	watchMarker(root, "docs/tasks/T-100.md", "done")
	body, err = os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"marker = done", "marker_by = chat"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("в записи витка чата нет строки %q:\n%s", want, body)
		}
	}
}

// TestLapWritesMarkerToRegistry: живая проводка от команды до записи. Маркер
// кладёт главный разбор после cmdLap, и в юните этой проводки не видно.
func TestLapWritesMarkerToRegistry(t *testing.T) {
	home := t.TempDir()
	root := writeBoard(t)
	goalFile(t, root, "T-100", goalText("бюджет: week_all <= 25\n", ""))
	cmd := exec.Command("go", "run", ".", "-C", root, "spend", "--goal", "docs/tasks/T-100.md")
	cmd.Env = append(os.Environ(), "HOME="+home)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("spend: %v\n%s", err, out)
	}
	cmd = exec.Command("go", "run", ".", "-C", root, "lap", "--goal", "docs/tasks/T-100.md",
		"--note", "цель дошла до вопроса", "--marker", "wait-human")
	cmd.Env = append(os.Environ(), "HOME="+home)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("lap: %v\n%s", err, out)
	}
	body, err := os.ReadFile(watchEntries(t, home)[0])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "marker = wait-human") {
		t.Errorf("маркер витка не доехал до записи реестра:\n%s", body)
	}
}
