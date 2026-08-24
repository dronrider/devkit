package main

import (
	"path/filepath"
	"strings"
	"testing"
)

// laggedWorktree заводит репозиторий с origin и боковым деревом, отставшим от
// origin/main на lag пустых коммитов: пустые двигают указатель, не трогая
// доску, и считать отставание можно без правки файлов. Возврат: основной
// чекаут, отставшее дерево и голый origin.
func laggedWorktree(t *testing.T, lag int) (string, string, string) {
	t.Helper()
	root := setup(t)
	gitSetup(t, root)
	bare := filepath.Join(t.TempDir(), "origin.git")
	gitOut(t, root, "init", "-q", "--bare", bare)
	gitOut(t, root, "remote", "add", "origin", bare)
	gitOut(t, root, "push", "-q", "origin", "main")
	wt := filepath.Join(t.TempDir(), "wt")
	gitOut(t, root, "worktree", "add", "--detach", wt, "HEAD")
	for i := 0; i < lag; i++ {
		gitOut(t, root, "commit", "-q", "--allow-empty", "-m", "движение мимо бокового дерева")
	}
	gitOut(t, root, "push", "-q", "origin", "main")
	return root, wt, bare
}

// TestListWarnsWhenBoardBehind: list в отставшем боковом дереве обязан
// предупреждать об отставании до секций и называть команду подтяга.
// Регрессионный тест DK-269: раньше list отдавал устаревшую доску без оговорок.
func TestListWarnsWhenBoardBehind(t *testing.T) {
	_, wt, _ := laggedWorktree(t, 2)

	out, err := cmdList(wt, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, "доска позади origin/main на 2 коммита") {
		t.Fatalf("первой строкой нет предупреждения об отставании:\n%s", out)
	}
	if !strings.Contains(out, "taskctl catchup") {
		t.Fatalf("предупреждение не называет команду подтяга:\n%s", out)
	}
}

// TestShowCarriesStaleNote: предупреждение едет и в show, тоже первой строкой,
// чтобы карта задачи читалась после объяснения, почему она может быть старой.
func TestShowCarriesStaleNote(t *testing.T) {
	_, wt, _ := laggedWorktree(t, 1)

	out, err := cmdShow(wt, "XR-001")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, "доска позади origin/main на 1 коммит") {
		t.Fatalf("show без предупреждения об отставании:\n%s", out)
	}
}

// TestListOnMainBehindOriginSuggestsPull: основной чекаут на main отстаёт от
// origin/main после чужого пуша, и его способ догнать это git pull, а не
// catchup, которому нужно detached-дерево.
func TestListOnMainBehindOriginSuggestsPull(t *testing.T) {
	root, _, bare := laggedWorktree(t, 1)
	other := filepath.Join(t.TempDir(), "other")
	gitOut(t, root, "clone", "-q", "-b", "main", bare, other)
	gitOut(t, other, "config", "user.email", "test@test")
	gitOut(t, other, "config", "user.name", "test")
	gitOut(t, other, "commit", "-q", "--allow-empty", "-m", "со стороны")
	gitOut(t, other, "push", "-q", "origin", "main")
	gitOut(t, root, "fetch", "-q", "origin")

	out, err := cmdList(root, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "доска позади origin/main на 1 коммит") {
		t.Fatalf("отставание основного чекаута не названо:\n%s", out)
	}
	if !strings.Contains(out, "git pull") || strings.Contains(out, "taskctl catchup") {
		t.Fatalf("совет не про git pull:\n%s", out)
	}
}

// TestListSilentOnTaskBranch: дерево задачи стоит на своей ветке и отстаёт от
// origin/main по построению, предупреждение там шум.
func TestListSilentOnTaskBranch(t *testing.T) {
	root, _, _ := laggedWorktree(t, 2)
	wt := filepath.Join(t.TempDir(), "task")
	gitOut(t, root, "worktree", "add", "-q", "-b", "dk-100", wt, "HEAD~2")

	out, err := cmdList(wt, "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "позади") {
		t.Fatalf("дерево задачи на ветке получило предупреждение:\n%s", out)
	}
}

// TestListCleanTreeHasNoNote: свежее дерево и доска вне git молчат, чтобы
// предупреждение не красило каждый запуск на ровном месте.
func TestListCleanTreeHasNoNote(t *testing.T) {
	_, wt, _ := laggedWorktree(t, 0)

	out, err := cmdList(wt, "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "позади") {
		t.Fatalf("свежее дерево получило предупреждение:\n%s", out)
	}
	plain := setup(t)
	out, err = cmdList(plain, "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "позади") {
		t.Fatalf("доска вне git получила предупреждение:\n%s", out)
	}
	if !strings.HasPrefix(out, "In progress") {
		t.Fatalf("вне git list начинается не с секции:\n%s", out)
	}
}

// TestStaleNoteFallsBackToMain: без remote указателем свежести служит местный
// main, предупреждение считает от него.
func TestStaleNoteFallsBackToMain(t *testing.T) {
	root := setup(t)
	gitSetup(t, root)
	wt := filepath.Join(t.TempDir(), "wt")
	gitOut(t, root, "worktree", "add", "--detach", wt, "HEAD")
	gitOut(t, root, "commit", "-q", "--allow-empty", "-m", "движение без remote")
	gitOut(t, root, "commit", "-q", "--allow-empty", "-m", "ещё одно")

	out, err := cmdList(wt, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "доска позади main на 2 коммита") {
		t.Fatalf("запасной указатель main не назван:\n%s", out)
	}
}
