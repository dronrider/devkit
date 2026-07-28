package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestStartWorktree: start заводит ветку задачи в отдельном дереве рядом с
// проектом, переводит её из Backlog в In progress и не трогает основной
// чекаут; повторный start отбивается, второй заход после удаления worktree
// подхватывает старую ветку.
func TestStartWorktree(t *testing.T) {
	root, callLog := setup(t, "", "")
	msg, err := cmdStart(root, StartParams{ID: "XR-002", Slug: "wt"})
	if err != nil {
		t.Fatal(err)
	}
	wt := filepath.Join(filepath.Dir(root), filepath.Base(root)+"-xr-002")
	if !strings.Contains(msg, wt) {
		t.Fatalf("в отчёте нет пути worktree: %q", msg)
	}
	if br := gitT(t, wt, "rev-parse", "--abbrev-ref", "HEAD"); br != "xr-002-wt" {
		t.Fatalf("в worktree стоит %q", br)
	}
	if br := gitT(t, root, "rev-parse", "--abbrev-ref", "HEAD"); br != "main" {
		t.Fatalf("основной чекаут ушёл с main на %q", br)
	}
	if fi, err := os.Stat(filepath.Join(wt, ".devkit")); err != nil || !fi.IsDir() {
		t.Fatal("start должен заводить .devkit в worktree под журнал запусков")
	}
	calls, _ := os.ReadFile(callLog)
	if !strings.Contains(string(calls), "move XR-002 in-progress") {
		t.Fatalf("задача не переведена в In progress: %q", calls)
	}

	// Задача уже в работе: второй start отбивается с путём существующего дерева.
	if _, err := cmdStart(root, StartParams{ID: "XR-002"}); err == nil ||
		!strings.Contains(err.Error(), "уже в работе") {
		t.Fatalf("повторный start должен отбиваться: %v", err)
	}

	// Второй заход: worktree убрали, ветка осталась, start находит её по ID.
	gitT(t, root, "worktree", "remove", "--force", wt)
	msg, err = cmdStart(root, StartParams{ID: "XR-002"})
	if err != nil {
		t.Fatal(err)
	}
	if br := gitT(t, wt, "rev-parse", "--abbrev-ref", "HEAD"); br != "xr-002-wt" {
		t.Fatalf("второй заход должен подхватить старую ветку, стоит %q", br)
	}
	if !strings.Contains(msg, "xr-002-wt") {
		t.Fatalf("отчёт второго захода: %q", msg)
	}
}

// TestStartPreconditions: чужой статус и незнакомый ID это чистые ошибки,
// без следов в git.
func TestStartPreconditions(t *testing.T) {
	root, _ := setup(t, "", rowCheck)
	if _, err := cmdStart(root, StartParams{ID: "XR-404"}); err == nil ||
		!strings.Contains(err.Error(), "нет на доске") {
		t.Fatalf("start незнакомой задачи должен отбиваться: %v", err)
	}
	if _, err := cmdStart(root, StartParams{ID: "XR-009"}); err == nil ||
		!strings.Contains(err.Error(), "из Backlog или In progress") {
		t.Fatalf("start задачи из Check должен отбиваться: %v", err)
	}
	if n := strings.Count(gitT(t, root, "worktree", "list", "--porcelain"), "worktree "); n != 1 {
		t.Fatalf("отказ start не должен оставлять worktree, деревьев %d", n)
	}
}

// startTask прогоняет боевую связку: start заводит worktree, правка кода
// коммитится там.
func startTask(t *testing.T, root, id, file string) string {
	t.Helper()
	if _, err := cmdStart(root, StartParams{ID: id, Slug: "fix"}); err != nil {
		t.Fatal(err)
	}
	wt := filepath.Join(filepath.Dir(root), filepath.Base(root)+"-"+strings.ToLower(id))
	write(t, wt, file, "new\n")
	gitT(t, wt, "add", ".")
	gitT(t, wt, "commit", "-qm", "fix: "+id+" правка")
	return wt
}

// TestMergeFromMainWithWorktree: основной чекаут стоит на main, ветка задачи
// живёт в worktree. merge находит её по ID, сливает fast-forward и прибирает
// дерево с веткой.
func TestMergeFromMainWithWorktree(t *testing.T) {
	root, callLog := setup(t, rowInProg, "")
	wt := startTask(t, root, "XR-001", "code.txt")
	msg, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true"})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "code.txt")); string(got) != "new\n" {
		t.Fatalf("правка не доехала до main: %q", got)
	}
	if _, err := os.Stat(wt); err == nil {
		t.Fatal("worktree слитой задачи должен убираться")
	}
	if gitT(t, root, "branch", "--list", "xr-001-fix") != "" {
		t.Error("фичеветка не удалена")
	}
	if !strings.Contains(msg, "worktree") || !strings.Contains(msg, "убран") {
		t.Fatalf("в отчёте нет уборки worktree: %q", msg)
	}
	calls, _ := os.ReadFile(callLog)
	if !strings.Contains(string(calls), "move XR-001 check") {
		t.Errorf("taskctl move не вызван: %q", calls)
	}
}

// TestMergeFromInsideWorktree: merge запускается из самого worktree (корень
// найден по его docs/TASKS.md), а слияние и доска идут в основном дереве.
func TestMergeFromInsideWorktree(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	wt := startTask(t, root, "XR-001", "code.txt")
	if _, err := cmdMerge(wt, MergeParams{ID: "XR-001", Test: "true"}); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "code.txt")); string(got) != "new\n" {
		t.Fatalf("правка не доехала до main: %q", got)
	}
	if br := gitT(t, root, "rev-parse", "--abbrev-ref", "HEAD"); br != "main" {
		t.Fatalf("основной чекаут ушёл с main на %q", br)
	}
	if _, err := os.Stat(wt); err == nil {
		t.Fatal("worktree слитой задачи должен убираться")
	}
}

// TestMergeWorktreeReviewOnBranch: замечания ревью живут в файле задачи на
// фичеветке, основной чекаут на main их не видит. merge обязан читать файл
// из worktree: открытое замечание там держит слияние.
func TestMergeWorktreeReviewOnBranch(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	wt := startTask(t, root, "XR-001", "code.txt")
	data, _ := os.ReadFile(filepath.Join(wt, "docs", "tasks", "XR-001.md"))
	write(t, wt, "docs/tasks/XR-001.md", string(data)+"- хвост без исхода, думаем\n")
	gitT(t, wt, "add", ".")
	gitT(t, wt, "commit", "-qm", "docs: замечание")
	if _, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true"}); err == nil ||
		!strings.Contains(err.Error(), "без исхода") {
		t.Fatalf("открытое замечание на ветке должно держать merge: %v", err)
	}
}

// TestMergeWorktreeDirty: незакоммиченное в worktree держит слияние, а
// untracked-хлам не валит merge, но оставляет дерево на уборку руками.
func TestMergeWorktreeDirty(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	wt := startTask(t, root, "XR-001", "code.txt")
	write(t, wt, "code.txt", "dirty\n")
	if _, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true"}); err == nil ||
		!strings.Contains(err.Error(), "незакоммиченное") {
		t.Fatalf("грязный worktree должен держать merge: %v", err)
	}

	gitT(t, wt, "checkout", "--", "code.txt")
	write(t, wt, "scratch.txt", "черновик\n")
	msg, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true"})
	if err != nil {
		t.Fatalf("untracked-файл не должен валить merge: %v", err)
	}
	if _, err := os.Stat(filepath.Join(wt, "scratch.txt")); err != nil {
		t.Fatal("untracked-черновик потерян при уборке worktree")
	}
	if !strings.Contains(msg, "прибрать руками") {
		t.Fatalf("в отчёте нет подсказки про уборку: %q", msg)
	}
}

// TestStatusShowsWorktrees: status перечисляет worktree задач и одинаково
// отвечает из основного дерева и из worktree.
func TestStatusShowsWorktrees(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	wt := startTask(t, root, "XR-001", "code.txt")
	for _, from := range []string{root, wt} {
		msg, err := cmdStatus(from)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(msg, "worktree: xr-001-fix") {
			t.Fatalf("status из %s не показывает worktree:\n%s", from, msg)
		}
		if !strings.Contains(msg, "ветка: main") {
			t.Fatalf("status из %s должен считаться по основному дереву:\n%s", from, msg)
		}
	}
}
