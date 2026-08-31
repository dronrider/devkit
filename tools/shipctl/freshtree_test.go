package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Разбор окружения и выкладки дерева переехал в internal/freshtree вместе с
// их собственными тестами (DK-643): здесь остаётся то, что проверяется только
// через слияние, а именно что merge гонит тесты в свежем дереве и убирает его.

// TestMergeRunsTestsInFreshTree: прогон идёт в свежем дереве на ребейзнутом
// коммите и с временным HOME. Незакоммиченный артефакт работы исполнителя
// (природа 98b43e7: слепок стенда считал __pycache__) в свежем дереве
// отсутствует, и команда, красная в прогретом чекауте, слияние проходит.
func TestMergeRunsTestsInFreshTree(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	branchWithFix(t, root)
	write(t, root, "cache.pyc", "артефакт прогона исполнителя\n")
	rec := t.TempDir()
	testCmd := "pwd > " + filepath.Join(rec, "pwd") +
		" && echo \"$HOME\" > " + filepath.Join(rec, "home") +
		" && test ! -e cache.pyc"
	msg, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: testCmd})
	if err != nil {
		t.Fatalf("артефакт прогретого дерева уронил тесты, значит прогон шёл не в свежем: %v", err)
	}
	pwd := readTrim(t, filepath.Join(rec, "pwd"))
	if pwd == root || strings.HasPrefix(pwd+"/", root+"/") {
		t.Fatalf("тесты гнались в чекауте %s, а не в свежем дереве", pwd)
	}
	home := readTrim(t, filepath.Join(rec, "home"))
	if home == os.Getenv("HOME") {
		t.Fatalf("дом прогона совпал с домом сессии: %s", home)
	}
	if home == "" || pwd == "" {
		t.Fatal("прогон не записал pwd или HOME")
	}
	if !strings.Contains(msg, "в свежем дереве") {
		t.Fatalf("отчёт молчит про свежее дерево: %q", msg)
	}
	if wl := gitT(t, root, "worktree", "list"); strings.Count(wl, "\n") != 0 {
		t.Fatalf("временное дерево не убрано:\n%s", wl)
	}
	if _, err := os.Stat(filepath.Join(root, "cache.pyc")); err != nil {
		t.Fatal("артефакт в прогретом дереве пропал, тест мерил пустоту")
	}
}

// TestMergeRedTestNamesFreshTree: отказ красных тестов называет свежее дерево,
// а само дерево убирается и при провале.
func TestMergeRedTestNamesFreshTree(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	branchWithFix(t, root)
	_, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "false"})
	if err == nil || !strings.Contains(err.Error(), "в свежем дереве") {
		t.Fatalf("отказ должен называть свежее дерево: %v", err)
	}
	if wl := gitT(t, root, "worktree", "list"); strings.Count(wl, "\n") != 0 {
		t.Fatalf("временное дерево после провала не убрано:\n%s", wl)
	}
}

func readTrim(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(b))
}
