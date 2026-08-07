package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
