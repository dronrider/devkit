package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// countGit кладёт на PATH подставной `git`, который дописывает свои аргументы в
// журнал и отдаёт работу настоящему. Считается факт состава хода, число
// запусков процесса, а не стенное время: число одно и то же под любой нагрузкой
// машины, а время под соседним полным прогоном плывёт втрое-впятеро (замер
// DK-1168, скилл test-standard, «Факт вместо стенного времени»).
func countGit(t *testing.T) func() []string {
	t.Helper()
	real, err := exec.LookPath("git")
	if err != nil {
		t.Skip("нет git")
	}
	dir := t.TempDir()
	log := filepath.Join(dir, "calls.log")
	shim := filepath.Join(dir, "git")
	body := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> " + log + "\nexec " + real + " \"$@\"\n"
	if err := os.WriteFile(shim, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return func() []string {
		data, err := os.ReadFile(log)
		if err != nil {
			return nil
		}
		var out []string
		for _, ln := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
			if ln != "" {
				out = append(out, ln)
			}
		}
		return out
	}
}

// spawnRepo это доска из n строк, каждая на своей предпосылке, и история под
// них: у предпосылки коммит работы и два коммита доски новее него. Признак
// «слита» идёт от новых коммитов к старым и спрашивает состав каждого коммита
// задачи, так что без пакетного чтения одна предпосылка стоит трёх вызовов git
// на состав и ещё одного на свой файл задачи.
func spawnRepo(t *testing.T, n int) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("нет git")
	}
	root := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"config", "user.email", "t@example"},
		{"config", "user.name", "t"},
		{"config", "commit.gpgsign", "false"},
		{"config", "core.hooksPath", "/dev/null"},
	} {
		if out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", args[0], err, out)
		}
	}
	for _, dir := range []string{filepath.Join("docs", "tasks"), "x"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	write := func(rel, body string) {
		if err := os.WriteFile(filepath.Join(root, rel), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	commit := func(msg string) {
		if out, err := exec.Command("git", "-C", root, "add", "-A").CombinedOutput(); err != nil {
			t.Fatalf("git add: %v\n%s", err, out)
		}
		if out, err := exec.Command("git", "-C", root, "commit", "-q", "-m", msg).CombinedOutput(); err != nil {
			t.Fatalf("git commit: %v\n%s", err, out)
		}
	}

	head := "| ID | Задача | Тип | P | R | Цена | Ссылка |\n|--------|--------|-----|---|---|------|--------|\n"
	var deps, rows strings.Builder
	for i := 1; i <= n; i++ {
		dep := fmt.Sprintf("XR-%03d", 100+i)
		row := fmt.Sprintf("XR-%03d", 200+i)
		write("docs/tasks/"+dep+".md", "# "+dep+"\n")
		write("x/"+dep+".go", "package x\n")
		commit("feat(x): " + dep + " код")
		write("docs/tasks/"+dep+".md", "# "+dep+"\n\n## Сценарий проверки\n\n1. Шаг.\n")
		commit("docs(tasks): " + dep + " сценарий")
		write("docs/TASKS.md", "# Тест: доска (префикс XR)\n\nстрок: "+dep+"\n")
		commit("docs(tasks): " + dep + " в Check")
		deps.WriteString(fmt.Sprintf("| %s | Предпосылка %d | task | P3 | 5 (0+0+0+0+5) | S | - |\n", dep, i))
		rows.WriteString(fmt.Sprintf("| %s | Строка %d [после %s] | task | P3 | 5 (0+0+0+0+5) | S | - |\n",
			row, i, dep))
	}
	// Предпосылки стоят строками Check, зависимые в Backlog: ребро на строку,
	// которой на доске нет, снятость считает без git, и стенд не увидел бы того
	// обхода лога, что идёт на живой доске.
	write("docs/TASKS.md", "# Тест: доска (префикс XR)\n\n## In progress\n\n"+head+
		"\n## Check\n\n"+head+deps.String()+
		"\n## Backlog\n\n"+head+rows.String()+
		"\n## Blocked\n\n"+head)
	write("docs/TASKS-archive.md",
		"# сделано\n\n| ID | Задача | Тип | P | Закрыто | Ссылка |\n|---|---|---|---|---|---|\n")
	commit("docs(tasks): доска целиком")
	return root
}

// TestListJSONHoldsGitSpawns: обход доски это самая частая команда, и её зовёт
// дашборд каждым кругом опроса. Вся её цена это подпроцессы git, и под соседним
// полным прогоном слияния цена одного запуска втрое-впятеро выше обычной.
// Понижение приоритета дерева прогона (DK-1124) её не снимает: nice берёт счёт,
// а не создание процесса. Поэтому потолок стоит на числе запусков, а строка
// растёт от каждой новой строки доски, если признак опять пойдёт по одному
// коммиту за вызов (DK-1168, первая строка DoD цели DK-1084).
func TestListJSONHoldsGitSpawns(t *testing.T) {
	root := spawnRepo(t, 8)
	calls := countGit(t)

	if _, err := cmdListJSON(root, ""); err != nil {
		t.Fatalf("обход доски: %v", err)
	}
	got := calls()
	// Потолок с запасом на разбор репозитория: лог main, пакет файлов задач,
	// пакет состава коммитов, возраст строк, чистота доски и локальный конфиг.
	// Восемь строк по одному вызову на коммит стоили бы трёх десятков.
	if len(got) > 14 {
		t.Fatalf("обход доски из 8 строк поднял %d подпроцессов git, жду не больше 14:\n%s",
			len(got), strings.Join(got, "\n"))
	}
}

// TestListJSONSpawnsDoNotGrowWithRows: потолок числа запусков легко взять,
// подогнав его под текущую доску. Держит правку другое: число запусков не
// зависит от числа строк, и обход доски вдвое длиннее стоит того же.
func TestListJSONSpawnsDoNotGrowWithRows(t *testing.T) {
	small := spawnRepo(t, 4)
	big := spawnRepo(t, 12)
	calls := countGit(t)

	if _, err := cmdListJSON(small, ""); err != nil {
		t.Fatalf("обход малой доски: %v", err)
	}
	after := len(calls())
	if _, err := cmdListJSON(big, ""); err != nil {
		t.Fatalf("обход большой доски: %v", err)
	}
	grew := len(calls()) - after
	if grew > after {
		t.Fatalf("доска из 12 строк подняла %d подпроцессов git против %d у доски из 4: цена растёт со строками",
			grew, after)
	}
}
