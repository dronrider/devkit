package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMergeTestEnv: окружение прогона собрано явно. Живой HOME подменён
// временным, каталоги из-под него ушли из PATH, переменные харнеса CLAUDE*
// унесены, а тулчейны вне дома и прочие переменные остались.
func TestMergeTestEnv(t *testing.T) {
	home := "/fake/home"
	t.Setenv("CLAUDE_CODE_TEST_MARKER", "1")
	t.Setenv("SHIPCTL_KEEP_ME", "yes")
	t.Setenv("GOPATH", home+"/go")
	t.Setenv("VIRTUAL_ENV", home+"/.venv")
	// /fake/homework проверяет границу: общий префикс строки это не вложенность.
	t.Setenv("PATH", home+"/bin:/usr/bin:/fake/homework:"+home)
	env := mergeTestEnv(home, "/tmp/newhome")
	joined := "\n" + strings.Join(env, "\n") + "\n"
	if !strings.Contains(joined, "\nHOME=/tmp/newhome\n") {
		t.Fatalf("нет временного HOME:\n%s", joined)
	}
	if strings.Count(joined, "\nHOME=") != 1 {
		t.Fatalf("HOME должен быть один:\n%s", joined)
	}
	if strings.Contains(joined, "CLAUDE_CODE_TEST_MARKER") {
		t.Fatalf("переменная харнеса протекла:\n%s", joined)
	}
	if strings.Contains(joined, "\nGOPATH=") || strings.Contains(joined, "\nVIRTUAL_ENV=") {
		t.Fatalf("указатель внутрь живого дома протёк:\n%s", joined)
	}
	if !strings.Contains(joined, "\nSHIPCTL_KEEP_ME=yes\n") {
		t.Fatalf("обычная переменная потеряна:\n%s", joined)
	}
	if !strings.Contains(joined, "\nPATH=/usr/bin:/fake/homework\n") {
		t.Fatalf("PATH урезан неверно:\n%s", joined)
	}
}

func TestTrimHomePath(t *testing.T) {
	if got := trimHomePath("/a:/b", ""); got != "/a:/b" {
		t.Fatalf("пустой home не должен резать PATH: %q", got)
	}
	if got := trimHomePath("/h/bin:/usr/bin:/h", "/h"); got != "/usr/bin" {
		t.Fatalf("каталоги под home остались: %q", got)
	}
}

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
