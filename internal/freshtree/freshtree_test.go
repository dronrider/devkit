package freshtree

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestEnv: окружение прогона собрано явно. Живой HOME подменён временным,
// каталоги из-под него ушли из PATH, переменные харнеса CLAUDE* унесены, а
// тулчейны вне дома и прочие переменные остались.
func TestEnv(t *testing.T) {
	home := "/fake/home"
	t.Setenv("CLAUDE_CODE_TEST_MARKER", "1")
	t.Setenv("FRESHTREE_KEEP_ME", "yes")
	t.Setenv("GOPATH", home+"/go")
	t.Setenv("VIRTUAL_ENV", home+"/.venv")
	// /fake/homework проверяет границу: общий префикс строки это не вложенность.
	t.Setenv("PATH", home+"/bin:/usr/bin:/fake/homework:"+home)
	env := Env(home, "/tmp/newhome")
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
	if !strings.Contains(joined, "\nFRESHTREE_KEEP_ME=yes\n") {
		t.Fatalf("обычная переменная потеряна:\n%s", joined)
	}
	if !strings.Contains(joined, "\nPATH=/usr/bin:/fake/homework\n") {
		t.Fatalf("PATH урезан неверно:\n%s", joined)
	}
}

func TestTrimHomePath(t *testing.T) {
	if got := TrimHomePath("/a:/b", ""); got != "/a:/b" {
		t.Fatalf("пустой home не должен резать PATH: %q", got)
	}
	if got := TrimHomePath("/h/bin:/usr/bin:/h", "/h"); got != "/usr/bin" {
		t.Fatalf("каталоги под home остались: %q", got)
	}
}

// TestMakeCheckoutsCommitWithoutWorkTree: дерево выложено на названный коммит,
// незакоммиченный артефакт исходного чекаута в него не поехал, рядом стоит
// пустой временный HOME, а cleanup уносит и дерево, и запись о нём в гите.
func TestMakeCheckoutsCommitWithoutWorkTree(t *testing.T) {
	root := gitRepo(t)
	sha := gitT(t, root, "rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(root, "artifact.pyc"), []byte("след работы\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tree, home, cleanup, err := Make(root, sha, "freshtree-test-")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(tree, "file.txt")); err != nil {
		t.Fatalf("коммит не выложен: %v", err)
	}
	if _, err := os.Stat(filepath.Join(tree, "artifact.pyc")); err == nil {
		t.Fatal("артефакт прогретого дерева поехал в свежее")
	}
	entries, err := os.ReadDir(home)
	if err != nil || len(entries) != 0 {
		t.Fatalf("временный HOME должен быть пустым каталогом: %v, %d записей", err, len(entries))
	}
	cleanup()
	if _, err := os.Stat(tree); err == nil {
		t.Fatal("дерево после уборки на месте")
	}
	if wl := gitT(t, root, "worktree", "list"); strings.Count(wl, "\n") != 0 {
		t.Fatalf("запись о дереве в гите осталась:\n%s", wl)
	}
}

// TestMakeNamesGitFailure: неизвестный коммит это отказ с выводом гита, а не
// голое «exit status 128»: без причины разбирать нечего.
func TestMakeNamesGitFailure(t *testing.T) {
	root := gitRepo(t)
	_, _, _, err := Make(root, "0000000000000000000000000000000000000000", "freshtree-test-")
	if err == nil {
		t.Fatal("выкладка несуществующего коммита должна падать")
	}
	if !strings.Contains(err.Error(), "invalid reference") && !strings.Contains(err.Error(), "fatal") {
		t.Fatalf("отказ не несёт вывода гита: %v", err)
	}
}

func gitRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte("исходник\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitT(t, root, "init", "-q", "-b", "main")
	gitT(t, root, "config", "user.email", "test@test")
	gitT(t, root, "config", "user.name", "test")
	gitT(t, root, "add", ".")
	gitT(t, root, "commit", "-q", "-m", "init")
	return root
}

func gitT(t *testing.T, root string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}
