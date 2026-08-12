package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// racer пишет скрипт, кладущий коммит поверх текущей вершины main. Так в тесте
// воспроизводится соседняя сессия, успевшая закоммитить, пока идёт прогон:
// ветка задачи стоит в чекауте, поэтому main двигается через отдельный
// detached-worktree и update-ref. Каждый запуск даёт новый коммит, счётчик
// лежит рядом со скриптом.
func racer(t *testing.T, root, file, subject string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "race.sh")
	body := "#!/bin/sh\nset -e\n" +
		"unset GIT_DIR GIT_WORK_TREE GIT_INDEX_FILE\n" +
		"n=$(cat " + dir + "/n 2>/dev/null || echo 0)\n" +
		"n=$((n+1))\n" +
		"echo $n > " + dir + "/n\n" +
		"w=" + dir + "/wt$n\n" +
		"git -C " + root + " worktree add -q --detach $w main\n" +
		"mkdir -p $(dirname $w/" + file + ")\n" +
		"printf 'соседняя правка %s\\n' $n >> $w/" + file + "\n" +
		"git -C $w add -A\n" +
		"git -C $w commit -qm \"" + subject + " $n\"\n" +
		"git -C " + root + " update-ref refs/heads/main $(git -C $w rev-parse HEAD)\n" +
		"git -C " + root + " worktree remove --force $w\n"
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestMergeCatchUpBoardCommit: коммит доски, легший в main за время прогона, не
// валит слияние. Оно проходит одним вызовом, тесты гонятся один раз, добор
// назван в отчёте, а в файл задачи записан только коммит ветки.
func TestMergeCatchUpBoardCommit(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	branchWithFix(t, root)
	race := racer(t, root, "docs/TASKS.md", "docs(tasks): XR-002 в работу")
	runs := filepath.Join(t.TempDir(), "runs")
	msg, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "sh " + race + " && echo x >> " + runs})
	if err != nil {
		t.Fatalf("коммит доски за время прогона уронил слияние: %v", err)
	}
	if got := lines(t, runs); got != 1 {
		t.Fatalf("тесты гнались %d раза, добор перегонять их не должен", got)
	}
	if !strings.Contains(msg, "добран") {
		t.Fatalf("отчёт молчит про добор: %q", msg)
	}
	if br := gitT(t, root, "rev-parse", "--abbrev-ref", "HEAD"); br != "main" {
		t.Fatalf("после merge стоим на %q", br)
	}
	log := gitT(t, root, "log", "--format=%s", "main")
	if !strings.Contains(log, "fix: XR-001 правка") || !strings.Contains(log, "docs(tasks): XR-002 в работу 1") {
		t.Fatalf("история после добора:\n%s", log)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "code.txt")); string(got) != "new\n" {
		t.Fatalf("правка не доехала до main: %q", got)
	}
	// В файле задачи только коммит ветки: чужой коммит доски записался бы туда
	// со старым preSha, и revert с окном выката потащили бы его за собой.
	fix := gitT(t, root, "log", "--format=%h", "-1", "--grep", "fix: XR-001 правка", "main")
	board := gitT(t, root, "log", "--format=%h", "-1", "--grep", "XR-002 в работу", "main")
	doc, err := os.ReadFile(filepath.Join(root, "docs/tasks/XR-001.md"))
	if err != nil {
		t.Fatal(err)
	}
	rec := recordedLine(t, string(doc))
	if !strings.Contains(rec, fix) {
		t.Fatalf("коммит ветки %s не записан в файл задачи: %q", fix, rec)
	}
	if strings.Contains(rec, board) {
		t.Fatalf("чужой коммит доски %s записан в файл задачи: %q", board, rec)
	}
}

// TestMergeCatchUpCodeRefused: коммит с кодом за то же окно доборным не
// считается, слияние отказывает прежним отказом. Такой код обязан пройти тесты
// вместе с веткой.
func TestMergeCatchUpCodeRefused(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	branchWithFix(t, root)
	race := racer(t, root, "other.txt", "fix: XR-002 соседняя правка")
	_, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "sh " + race})
	if err == nil || !strings.Contains(err.Error(), "fast-forward не прошёл") {
		t.Fatalf("код в дельте main обязан держать слияние: %v", err)
	}
	if br := gitT(t, root, "rev-parse", "--abbrev-ref", "HEAD"); br != "main" {
		t.Fatalf("после отказа стоим на %q", br)
	}
	if strings.Contains(gitT(t, root, "log", "--format=%s", "main"), "fix: XR-001 правка") {
		t.Fatal("ветка уехала в main мимо отказа")
	}
}

// TestMergeCatchUpLimit: main, уезжающий после каждого ребейза, упирается в
// потолок доборов, и отказ говорит, что main меняется быстрее слияния.
func TestMergeCatchUpLimit(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	branchWithFix(t, root)
	// Первый коммит доски кладёт прогон, дальше их кладёт хук post-rewrite после
	// каждого ребейза добора, и ff краснеет заново, пока не упрётся в потолок.
	hooks := t.TempDir()
	race := racer(t, root, "docs/TASKS.md", "docs(tasks): XR-002 в работу")
	if err := os.WriteFile(filepath.Join(hooks, "post-rewrite"),
		[]byte("#!/bin/sh\nsh "+race+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	gitT(t, root, "config", "core.hooksPath", hooks)
	_, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "sh " + race})
	if err == nil || !strings.Contains(err.Error(), "доборов ребейза сделано") {
		t.Fatalf("серия сверх потолка обязана держать слияние: %v", err)
	}
	if br := gitT(t, root, "rev-parse", "--abbrev-ref", "HEAD"); br != "main" {
		t.Fatalf("после отказа стоим на %q", br)
	}
	if strings.Contains(gitT(t, root, "log", "--format=%s", "main"), "fix: XR-001 правка") {
		t.Fatal("ветка уехала в main мимо отказа")
	}
}

// lines считает строки в файле, заведённом тестовой командой.
func lines(t *testing.T, path string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("тестовая команда не запускалась: %v", err)
	}
	return len(strings.Fields(string(data)))
}

// recordedLine достаёт из файла задачи строку записи слияния.
func recordedLine(t *testing.T, doc string) string {
	t.Helper()
	for _, ln := range strings.Split(doc, "\n") {
		if strings.Contains(ln, "слито: ") {
			return ln
		}
	}
	t.Fatalf("в файле задачи нет записи слияния:\n%s", doc)
	return ""
}
