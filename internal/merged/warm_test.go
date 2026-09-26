package merged

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gitCounter кладёт на PATH подставной `git`, который дописывает свои аргументы
// в журнал и отдаёт работу настоящему. Считается не время, а число запусков
// процесса: число держится одним и тем же под любой нагрузкой машины, а время
// под соседним прогоном плывёт втрое-впятеро (замер DK-1168).
func gitCounter(t *testing.T) func() []string {
	t.Helper()
	real, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git на машине не найден")
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

// warmRepo кладёт стенд обхода доски: n предпосылок, у каждой коммит работы и
// два коммита доски новее него. Признак идёт по логу от новых к старым и
// спрашивает состав каждого встреченного коммита задачи, так что без пакетного
// чтения на каждую предпосылку выходит по три вызова git и ещё один на её файл.
func warmRepo(t *testing.T, n int) (root string, ids []string) {
	t.Helper()
	root = repo(t)
	for i := 1; i <= n; i++ {
		id := "DK-" + string(rune('0'+i))
		ids = append(ids, id)
		commit(t, root, "feat(x): "+id+" код", map[string]string{
			"x/" + id + ".go": "package x\n",
		})
		commit(t, root, "docs(tasks): "+id+" сценарий", map[string]string{
			"docs/tasks/" + id + ".md": "# " + id + "\n",
		})
		commit(t, root, "docs(tasks): "+id+" в Check", map[string]string{
			"docs/TASKS.md": "# доска " + id + "\n",
		})
	}
	return root, ids
}

// TestExpectHoldsGitSpawns: обход доски спрашивает признак «слита» про десятки
// строк, и без предупреждения книга поднимает git на каждый файл задачи и на
// каждый коммит. Потолок тут факт состава хода, а не стенное время: под
// соседним полным прогоном цена одного запуска процесса втрое-впятеро выше, и
// `nice` её не снимает, поэтому лечится именно число запусков (DK-1168).
func TestExpectHoldsGitSpawns(t *testing.T) {
	root, ids := warmRepo(t, 6)
	calls := gitCounter(t)

	b := Open(root)
	b.Expect(ids)
	for _, id := range ids {
		if _, err := b.Task(id); err != nil {
			t.Fatalf("признак %s: %v", id, err)
		}
	}
	got := calls()
	// Потолок: лог main, пакет файлов задач, пакет состава коммитов и запас на
	// разбор репозитория. Шесть предпосылок без пакета стоили бы двух десятков
	// вызовов, и потолок рос бы с каждой строкой доски.
	if len(got) > 6 {
		t.Fatalf("книга подняла %d подпроцессов git на %d предпосылок, жду не больше 6:\n%s",
			len(got), len(ids), strings.Join(got, "\n"))
	}
	for _, c := range got {
		if strings.Contains(c, "show --name-only --pretty= ") {
			t.Fatalf("состав коммита прочитан вызовом на коммит, а не пакетом: %s", c)
		}
	}
}

// TestExpectKeepsVerdicts: пакетное чтение это ускорение, и вердикт признака от
// него не меняется. Книга с предупреждением и книга без него отвечают одно и то
// же про каждую строку стенда.
func TestExpectKeepsVerdicts(t *testing.T) {
	root, ids := warmRepo(t, 4)
	// Откат одной строки и запись «Выкат» у другой: оба случая ведут признак
	// мимо простого «первый коммит с ID в subject».
	commit(t, root, "revert: DK-2 откат", map[string]string{"x/DK-2.go": "package x\n// revert\n"})
	rec := commit(t, root, "chore(x): без ID в subject", map[string]string{"x/loose.go": "package x\n"})
	commit(t, root, "docs(tasks): DK-3 выкат", map[string]string{
		"docs/tasks/DK-3.md": "# DK-3\n\n## Выкат\n\n- 2026-09-26 слито: " + rec[:9] + "\n",
	})

	cold := Open(root)
	warm := Open(root)
	warm.Expect(append(ids, "DK-9"))
	for _, id := range append(ids, "DK-9") {
		want, err1 := cold.Task(id)
		got, err2 := warm.Task(id)
		if (err1 == nil) != (err2 == nil) {
			t.Fatalf("%s: ошибки разошлись: %v против %v", id, err1, err2)
		}
		if want != got {
			t.Fatalf("%s: пакетное чтение дало другой вердикт: %+v против %+v", id, got, want)
		}
		ever1, _ := cold.Ever(id)
		ever2, _ := warm.Ever(id)
		if ever1 != ever2 {
			t.Fatalf("%s: Ever разошёлся: %v против %v", id, ever2, ever1)
		}
	}
}

// TestExpectSkipsMissingDoc: у строки может не быть файла задачи в main, и
// пакетное чтение обязано отличить «файла нет» от пустого содержимого. Иначе
// запись «Выкат» соседа уехала бы не той задаче.
func TestExpectSkipsMissingDoc(t *testing.T) {
	root := repo(t)
	work := commit(t, root, "chore(x): без ID", map[string]string{"x/a.go": "package x\n"})
	commit(t, root, "docs(tasks): DK-5 выкат", map[string]string{
		"docs/tasks/DK-5.md": "# DK-5\n\n## Выкат\n\n- 2026-09-26 слито: " + work[:9] + "\n",
	})
	b := Open(root)
	b.Expect([]string{"DK-4", "DK-5", "DK-6"})
	if v, err := b.Task("DK-5"); err != nil || !v.Merged {
		t.Fatalf("DK-5 названа записью «Выкат», а признак ответил %+v (%v)", v, err)
	}
	for _, id := range []string{"DK-4", "DK-6"} {
		if v, err := b.Task(id); err != nil || v.Merged {
			t.Fatalf("%s файла задачи в main не имеет, а признак ответил %+v (%v)", id, v, err)
		}
	}
}
