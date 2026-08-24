package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// headAt возвращает sha, на котором стоит HEAD дерева.
func headAt(t *testing.T, root string) string {
	t.Helper()
	return gitOut(t, root, "rev-parse", "HEAD")
}

// TestCatchupMovesHead: команда догоняет отставшее боковое дерево до sha
// свежего указателя и печатает, сколько коммитов приехало.
func TestCatchupMovesHead(t *testing.T) {
	root, wt, _ := laggedWorktree(t, 2)
	want := gitOut(t, root, "rev-parse", "origin/main")

	msg, err := cmdCatchup(wt, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "приехало 2 коммита") {
		t.Fatalf("сообщение не называет число приехавших коммитов: %q", msg)
	}
	if got := headAt(t, wt); got != want {
		t.Fatalf("HEAD дерева %s, а ожидался %s", got, want)
	}
}

// TestCatchupFreshTree: на свежем дереве команда не двигает HEAD и говорит,
// что догонять нечего.
func TestCatchupFreshTree(t *testing.T) {
	_, wt, _ := laggedWorktree(t, 1)
	if _, err := cmdCatchup(wt, false); err != nil {
		t.Fatal(err)
	}
	before := headAt(t, wt)

	msg, err := cmdCatchup(wt, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "уже на актуальном") {
		t.Fatalf("свежее дерево не названо свежим: %q", msg)
	}
	if after := headAt(t, wt); after != before {
		t.Fatalf("HEAD свежего дерева тронут: %s -> %s", before, after)
	}
}

// TestCatchupRefusesBranch: дерево на ветке (задача или основной чекаут)
// командой не двигается, у них свой способ обновляться.
func TestCatchupRefusesBranch(t *testing.T) {
	root, _, _ := laggedWorktree(t, 1)
	taskWt := addWorktree(t, root, "dk-101")
	if err := os.WriteFile(filepath.Join(taskWt, "x.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	for name, dir := range map[string]string{"дерево задачи": taskWt, "основной чекаут": root} {
		_, err := cmdCatchup(dir, false)
		if err == nil {
			t.Fatalf("%s на ветке принялось догонять", name)
		}
		if !strings.Contains(err.Error(), "на ветке") {
			t.Fatalf("%s: отказ не про ветку: %v", name, err)
		}
	}
}

// TestCatchupRefusesDirtyTree: правки под догоном терять нельзя, грязное
// дерево отказывает и HEAD не двигает.
func TestCatchupRefusesDirtyTree(t *testing.T) {
	_, wt, _ := laggedWorktree(t, 2)
	before := headAt(t, wt)
	if err := os.WriteFile(filepath.Join(wt, "docs", "TASKS.md"),
		[]byte("правка поверх доски\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := cmdCatchup(wt, false)
	if err == nil {
		t.Fatal("грязное дерево принято догонять")
	}
	if !strings.Contains(err.Error(), "не чистое") {
		t.Fatalf("отказ не про грязное дерево: %v", err)
	}
	if after := headAt(t, wt); after != before {
		t.Fatalf("HEAD грязного дерева тронут: %s -> %s", before, after)
	}
}

// TestCatchupRefusesDivergedHead: собственные коммиты дерева пропадают из HEAD
// при слепом чекауте, расходящееся дерево отказывает.
func TestCatchupRefusesDivergedHead(t *testing.T) {
	_, wt, _ := laggedWorktree(t, 1)
	gitOut(t, wt, "commit", "-q", "--allow-empty", "-m", "своё в боковом дереве")

	_, err := cmdCatchup(wt, false)
	if err == nil {
		t.Fatal("расходящееся дерево принято догонять")
	}
	if !strings.Contains(err.Error(), "не позади") {
		t.Fatalf("отказ не про расхождение: %v", err)
	}
}

// TestCatchupRefusesDuringRebase: чистое detached-дерево посреди rebase выглядит
// «позади main» так же, как отставшее, и чекаут сломал бы операцию на середине.
func TestCatchupRefusesDuringRebase(t *testing.T) {
	_, wt, _ := laggedWorktree(t, 2)
	p := gitOut(t, wt, "rev-parse", "--git-path", "rebase-merge")
	if !filepath.IsAbs(p) {
		p = filepath.Join(wt, p)
	}
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}

	_, err := cmdCatchup(wt, false)
	if err == nil {
		t.Fatal("дерево посреди rebase принято догонять")
	}
	if !strings.Contains(err.Error(), "rebase") {
		t.Fatalf("отказ не про идущую операцию git: %v", err)
	}
}

// TestCatchupRefusesDuringBisect: bisect держит HEAD на проверяемом коммите,
// и чекаут уводил бы бисекцию с него так же, как rebase с середины.
func TestCatchupRefusesDuringBisect(t *testing.T) {
	_, wt, _ := laggedWorktree(t, 2)
	p := gitOut(t, wt, "rev-parse", "--git-path", "BISECT_LOG")
	if !filepath.IsAbs(p) {
		p = filepath.Join(wt, p)
	}
	if err := os.WriteFile(p, []byte("git bisect start\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := cmdCatchup(wt, false)
	if err == nil {
		t.Fatal("дерево посреди bisect принято догонять")
	}
	if !strings.Contains(err.Error(), "bisect") {
		t.Fatalf("отказ не про идущую операцию git: %v", err)
	}
}

// TestCatchupHookActs: в режиме хука отставшее боковое дерево догоняется и
// сообщение уходит в stdout, а не в ошибку: старт сессии хук не ломает.
func TestCatchupHookActs(t *testing.T) {
	root, wt, _ := laggedWorktree(t, 2)
	want := gitOut(t, root, "rev-parse", "origin/main")

	msg, err := cmdCatchup(wt, true)
	if err != nil {
		t.Fatalf("режим хука вернул ошибку: %v", err)
	}
	if !strings.Contains(msg, "приехало 2 коммита") {
		t.Fatalf("сообщение не называет число приехавших коммитов: %q", msg)
	}
	if got := headAt(t, wt); got != want {
		t.Fatalf("HEAD дерева %s, а ожидался %s", got, want)
	}
}

// TestCatchupHookLoudRefusal: отказ в гардах доезжает до сессии строкой, иначе
// отставание дерева выглядело бы починенным.
func TestCatchupHookLoudRefusal(t *testing.T) {
	_, wt, _ := laggedWorktree(t, 2)
	if err := os.WriteFile(filepath.Join(wt, "docs", "TASKS.md"),
		[]byte("правка поверх доски\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	msg, err := cmdCatchup(wt, true)
	if err != nil {
		t.Fatalf("режим хука вернул ошибку: %v", err)
	}
	if !strings.Contains(msg, "не догнано") || !strings.Contains(msg, "не чистое") {
		t.Fatalf("отказ не дошёл до сессии: %q", msg)
	}
}

// TestCatchupHookSilentScopes: хук стоит у всех сессий машины, и обо всём вне
// боковых отставших деревьев он молчит: основной чекаут, дерево задачи на
// ветке, свежее дерево и доска вне git.
func TestCatchupHookSilentScopes(t *testing.T) {
	root, wt, _ := laggedWorktree(t, 1)
	taskWt := addWorktree(t, root, "dk-102")
	if err := os.WriteFile(filepath.Join(taskWt, "x.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	plain := setup(t)

	cases := map[string]string{
		"основной чекаут": root,
		"дерево задачи":   taskWt,
		"доска вне git":   plain,
	}
	for name, dir := range cases {
		msg, err := cmdCatchup(dir, true)
		if err != nil {
			t.Fatalf("%s: режим хука вернул ошибку: %v", name, err)
		}
		if msg != "" {
			t.Fatalf("%s: хук не промолчал: %q", name, msg)
		}
	}
	if _, err := cmdCatchup(wt, true); err != nil {
		t.Fatal(err)
	}
	if msg, err := cmdCatchup(wt, true); err != nil || msg != "" {
		t.Fatalf("свежее дерево не промолчало: %q, %v", msg, err)
	}
	// Грязное дерево вне линкованных (дерево задачи) хуку не объяснение:
	// молчит оно уже потому, что на ветке, и порядок гардов этого не меняет.
	if err := os.WriteFile(filepath.Join(taskWt, "x.txt"), []byte("y\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if msg, err := cmdCatchup(taskWt, true); err != nil || msg != "" {
		t.Fatalf("грязное дерево задачи не промолчало: %q, %v", msg, err)
	}
}

// TestCatchupOutsideGit: без git и указателя свежести явная команда отказывает
// с причиной, а не падает.
func TestCatchupOutsideGit(t *testing.T) {
	plain := setup(t)
	_, err := cmdCatchup(plain, false)
	if err == nil {
		t.Fatal("доска вне git принята догонять")
	}
	if !strings.Contains(err.Error(), "указателя свежести") {
		t.Fatalf("отказ не про отсутствующий указатель: %v", err)
	}
}

// TestPluralCommits: склонение числа коммитов в сообщениях команды.
func TestPluralCommits(t *testing.T) {
	cases := map[int]string{
		1: "коммит", 2: "коммита", 5: "коммитов",
		11: "коммитов", 21: "коммит", 22: "коммита", 25: "коммитов",
	}
	for n, want := range cases {
		if got := pluralCommits(n); got != want {
			t.Errorf("pluralCommits(%d) = %q, хочу %q", n, got, want)
		}
	}
}
