package works

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// TestParseSessions гоняет разбор вывода tmux ls: пустой вывод, мусорные
// строки и честные строки с окнами и временем создания.
func TestParseSessions(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []Session
	}{
		{"пусто", "", []Session{}},
		{"одна", "task-XR-5\t2\t1700000000\n", []Session{{"task-XR-5", 2, 1700000000}}},
		{"без полей", "голое-имя\n", []Session{{"голое-имя", 0, 0}}},
		{"пустая строка пропущена", "task-XR-6\t1\t8\n\ntask-XR-7\t1\t9\n",
			[]Session{{"task-XR-6", 1, 8}, {"task-XR-7", 1, 9}}},
	}
	for _, c := range cases {
		if got := ParseSessions([]byte(c.in)); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: ParseSessions = %+v, ожидал %+v", c.name, got, c.want)
		}
	}
}

// TestSessionTask опознаёт работы конвейера по имени сессии: свой префикс
// проходит, чужой, производный хвост с моментом запуска и пустой префикс
// опознаны не бывают.
func TestSessionTask(t *testing.T) {
	cases := []struct {
		name, session, prefix, id, kind string
	}{
		{"своя задача", "task-XR-5", "XR", "XR-5", "task"},
		{"своя цель", "goal-XR-112", "XR", "XR-112", "goal"},
		{"чужой префикс", "task-ZZ-5", "XR", "", ""},
		{"производная сессия", "task-XR-208_1_1786532648", "XR", "", ""},
		{"не работа конвейера", "window-1", "XR", "", ""},
		{"доска без префикса", "task-XR-5", "", "", ""},
	}
	for _, c := range cases {
		id, kind := SessionTask(c.session, c.prefix)
		if id != c.id || kind != c.kind {
			t.Errorf("%s: SessionTask(%q, %q) = %q/%q, ожидал %q/%q",
				c.name, c.name, c.prefix, id, kind, c.id, c.kind)
		}
	}
}

// TestRegistryGoals: реестр отдаёт цели своего корня, чужие корни и записи без
// полей не считаются. Порядок сортированный.
func TestRegistryGoals(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".devkit", "goals")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	files := map[string]string{
		"a.watch": "# цель\nroot = " + root + "\ngoal = DK-112\n",
		"b.watch": "root = /чужой/корень\ngoal = DK-113\n",
		"c.watch": "root = " + root + "\n",
		"d.watch": "root = " + root + "\ngoal = DK-095\n",
	}
	for name, text := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got := RegistryGoals(home, root)
	if !reflect.DeepEqual(got, []string{"DK-112", "DK-095"}) {
		t.Fatalf("RegistryGoals = %v, ожидал [DK-112 DK-095]", got)
	}
}

// TestBusyTmux: занятость собирается из tmux без реестра. tmux подменяется
// скриптом в PATH, как его подменяют тесты дашборда.
func TestBusyTmux(t *testing.T) {
	bin := t.TempDir()
	script := "#!/bin/sh\nprintf 'task-XR-5\\t1\\t100\\ngoal-XR-9\\t1\\t200\\ntask-ZZ-1\\t1\\t300\\n'\n"
	if err := os.WriteFile(filepath.Join(bin, "tmux"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	busy := Busy("XR", t.TempDir(), t.TempDir())
	if !busy["XR-5"] || !busy["XR-9"] {
		t.Fatalf("свои сессии не заняли задачи: %v", busy)
	}
	if busy["ZZ-1"] {
		t.Fatal("чужой префикс занял задачу")
	}
}
