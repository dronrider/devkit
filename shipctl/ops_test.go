package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitT(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

const boardTable = "| ID | Задача | Тип | P | R | Ссылка |\n|--------|--------|-----|---|---|--------|\n"

func section(rows string) string {
	if rows == "" {
		return "Нет.\n"
	}
	return boardTable + rows
}

const rowInProg = "| XR-001 | Починка бага | bug | P1 | 55 (50+0+0+5+0) | [tasks/XR-001.md](tasks/XR-001.md) |\n"

// setup собирает репозиторий с доской: XR-001 в заданных секциях, стаб
// taskctl в PATH пишет вызовы в лог и имитирует правку доски.
func setup(t *testing.T, inProg, check string) (root, callLog string) {
	t.Helper()
	root = t.TempDir()
	gitT(t, root, "init", "-q", "-b", "main")
	gitT(t, root, "config", "user.email", "test@test")
	gitT(t, root, "config", "user.name", "test")
	board := "# Тест: задачи (префикс XR)\n\n" +
		"## In progress\n\n" + section(inProg) + "\n" +
		"## Check\n\n" + section(check) + "\n" +
		"## Backlog\n\n" + section("| XR-002 | Задел | task | P3 | 10 (0+5+0+0+5) |  |\n") + "\n" +
		"## Blocked\n\nНет.\n"
	write(t, root, "docs/TASKS.md", board)
	write(t, root, "docs/tasks/XR-001.md",
		"# XR-001: починка бага\n\n## Ревью\n\n- гонка в close: исправлено\n- нейминг: отклонено, стиль проекта\n")
	write(t, root, "code.txt", "old\n")
	gitT(t, root, "add", ".")
	gitT(t, root, "commit", "-qm", "seed")

	bin := t.TempDir()
	callLog = filepath.Join(bin, "calls.log")
	stub := "#!/bin/sh\necho \"$@\" >> \"" + callLog + "\"\nprintf '<!-- move -->\\n' >> \"$2/docs/TASKS.md\"\n"
	write(t, bin, "taskctl", stub)
	if err := os.Chmod(filepath.Join(bin, "taskctl"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return root, callLog
}

func write(t *testing.T, root, name, content string) {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func branchWithFix(t *testing.T, root string) {
	t.Helper()
	gitT(t, root, "checkout", "-qb", "xr-001-fix")
	write(t, root, "code.txt", "new\n")
	gitT(t, root, "add", ".")
	gitT(t, root, "commit", "-qm", "fix: XR-001 правка")
}

func TestMergeHappyPath(t *testing.T) {
	root, callLog := setup(t, rowInProg, "")
	branchWithFix(t, root)
	msg, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(msg, "предупреждение") {
		t.Errorf("лишнее предупреждение: %q", msg)
	}
	if br := gitT(t, root, "rev-parse", "--abbrev-ref", "HEAD"); br != "main" {
		t.Fatalf("после merge стоим на %q", br)
	}
	log := gitT(t, root, "log", "--format=%s")
	if !strings.HasPrefix(log, "docs(tasks): XR-001 в Check") || !strings.Contains(log, "fix: XR-001 правка") {
		t.Fatalf("история после merge:\n%s", log)
	}
	if gitT(t, root, "branch", "--list", "xr-001-fix") != "" {
		t.Error("фичеветка не удалена")
	}
	calls, _ := os.ReadFile(callLog)
	if !strings.Contains(string(calls), "move XR-001 check") {
		t.Errorf("taskctl move не вызван: %q", calls)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "code.txt")); string(got) != "new\n" {
		t.Fatalf("правка не доехала до main: %q", got)
	}
}

func TestMergeQueueBusy(t *testing.T) {
	root, _ := setup(t, rowInProg, "| XR-009 | Ждёт проверки | task | P2 | 30 (25+5+0+0+0) |  |\n")
	branchWithFix(t, root)
	_, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true"})
	if err == nil || !strings.Contains(err.Error(), "очередь занята") {
		t.Fatalf("ожидал отказ по очереди, получил: %v", err)
	}
}

func TestMergeOpenReview(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	f := filepath.Join(root, "docs", "tasks", "XR-001.md")
	data, _ := os.ReadFile(f)
	write(t, root, "docs/tasks/XR-001.md", string(data)+"- хвост без исхода, думаем\n")
	gitT(t, root, "add", ".")
	gitT(t, root, "commit", "-qm", "docs: замечание")
	branchWithFix(t, root)
	_, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true"})
	if err == nil || !strings.Contains(err.Error(), "без исхода") {
		t.Fatalf("ожидал отказ по ревью, получил: %v", err)
	}
}

func TestMergeRedTests(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	branchWithFix(t, root)
	_, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "false"})
	if err == nil || !strings.Contains(err.Error(), "красные") {
		t.Fatalf("ожидал отказ по тестам, получил: %v", err)
	}
	if br := gitT(t, root, "rev-parse", "--abbrev-ref", "HEAD"); br != "xr-001-fix" {
		t.Fatalf("после красных тестов стоим на %q", br)
	}
	if log := gitT(t, root, "log", "--format=%s", "main"); log != "seed" {
		t.Fatalf("main изменился: %s", log)
	}
}

func TestMergePreconditions(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	if _, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true"}); err == nil ||
		!strings.Contains(err.Error(), "фичеветки") {
		t.Fatalf("merge с main должен отбиваться: %v", err)
	}
	branchWithFix(t, root)
	if _, err := cmdMerge(root, MergeParams{ID: "XR-001"}); err == nil ||
		!strings.Contains(err.Error(), "--test") {
		t.Fatalf("merge без --test должен отбиваться: %v", err)
	}
	if _, err := cmdMerge(root, MergeParams{ID: "XR-404", Test: "true"}); err == nil ||
		!strings.Contains(err.Error(), "нет на доске") {
		t.Fatalf("merge неизвестной задачи должен отбиваться: %v", err)
	}
	write(t, root, "code.txt", "dirty\n")
	if _, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true"}); err == nil ||
		!strings.Contains(err.Error(), "незакоммиченное") {
		t.Fatalf("merge с грязным деревом должен отбиваться: %v", err)
	}
}

func TestStatus(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	msg, err := cmdStatus(root)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "XR-001 Починка бага") || !strings.Contains(msg, "очередь свободна") {
		t.Fatalf("status:\n%s", msg)
	}
	root2, _ := setup(t, "", rowInProg)
	msg, err = cmdStatus(root2)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "очередь занята: XR-001") {
		t.Fatalf("status с занятой очередью:\n%s", msg)
	}
}

func TestRevert(t *testing.T) {
	root, callLog := setup(t, "", rowInProg) // задача уже в Check
	write(t, root, "code.txt", "broken\n")
	gitT(t, root, "add", ".")
	gitT(t, root, "commit", "-qm", "feat: XR-001 фича")
	// Коммит только по доске откатываться не должен.
	f, _ := os.ReadFile(filepath.Join(root, "docs", "TASKS.md"))
	write(t, root, "docs/TASKS.md", string(f)+"<!-- маркер -->\n")
	gitT(t, root, "add", ".")
	gitT(t, root, "commit", "-qm", "docs(tasks): XR-001 в Check")

	msg, err := cmdRevert(root, RevertParams{ID: "XR-001", Test: "true"})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "code.txt")); string(got) != "old\n" {
		t.Fatalf("код не откатился: %q", got)
	}
	board, _ := os.ReadFile(filepath.Join(root, "docs", "TASKS.md"))
	if !strings.Contains(string(board), "<!-- маркер -->") {
		t.Fatal("откатился коммит доски")
	}
	log := gitT(t, root, "log", "--format=%s")
	if !strings.HasPrefix(log, "docs(tasks): XR-001 обратно в In progress") ||
		!strings.Contains(log, "revert: XR-001 откат") {
		t.Fatalf("история после revert:\n%s", log)
	}
	calls, _ := os.ReadFile(callLog)
	if !strings.Contains(string(calls), "move XR-001 in-progress") {
		t.Errorf("taskctl move не вызван: %q", calls)
	}
	if !strings.Contains(msg, "откачено коммитов: 1") {
		t.Fatalf("сообщение: %q", msg)
	}
	// Повторный запуск не находит что откатывать: свой revert не трогается.
	if _, err := cmdRevert(root, RevertParams{ID: "XR-001"}); err == nil ||
		!strings.Contains(err.Error(), "откатывать нечего") {
		t.Fatalf("повторный revert должен отбиваться: %v", err)
	}
}

func TestLoadBoardAndReview(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	b, err := loadBoard(root)
	if err != nil {
		t.Fatal(err)
	}
	if b.sectOf("XR-001") != "in-progress" || b.sectOf("XR-002") != "backlog" || b.sectOf("XR-404") != "" {
		t.Fatalf("секции разобраны неверно: %+v", b.sects)
	}
	open, err := openReviewNotes(root, "XR-001")
	if err != nil || len(open) != 0 {
		t.Fatalf("закрытое ревью считается открытым: %v %v", open, err)
	}
	if open, _ := openReviewNotes(root, "XR-404"); open != nil {
		t.Fatal("отсутствующий файл задачи не должен давать замечаний")
	}
}
