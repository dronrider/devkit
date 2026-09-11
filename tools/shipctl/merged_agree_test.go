package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dronrider/devkit/internal/merged"
)

func agreeGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir,
		"-c", "user.name=t", "-c", "user.email=t@t", "-c", "commit.gpgsign=false",
		"-c", "core.hooksPath=/dev/null"}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

func agreeCommit(t *testing.T, dir, subj, rel, body string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	agreeGit(t, dir, "add", rel)
	agreeGit(t, dir, "commit", "-q", "-m", subj)
}

// TestTaskCommitsAgreeWithMerged: откат shipctl и признак «слита» из
// internal/merged находят работу задачи одним кодом. Разойдись они, голова,
// дождавшаяся слияния, и откат той же задачи спорили бы о том, есть ли в main
// что откатывать.
func TestTaskCommitsAgreeWithMerged(t *testing.T) {
	root := t.TempDir()
	agreeGit(t, root, "init", "-q", "-b", "main")
	steps := []struct {
		subj, rel, body string
		want            bool
	}{
		{"docs(tasks): DK-1 в работу", "docs/tasks/DK-1.md", "# DK-1\n", false},
		{"feat: DK-1 код", "x/code.go", "package x\n", true},
		{"revert: DK-1 откат", "x/code.go", "", false},
		{"docs(lld): DK-1 документ", "docs/lld/DK-1.md", "# LLD\n", true},
	}
	for _, s := range steps {
		agreeCommit(t, root, s.subj, s.rel, s.body)
		shas, err := taskCommits(root, "main", "DK-1")
		if err != nil {
			t.Fatal(err)
		}
		v, err := merged.Task(root, "main", "DK-1")
		if err != nil {
			t.Fatal(err)
		}
		if (len(shas) > 0) != s.want || v.Merged != s.want {
			t.Fatalf("после %q откат видит %d коммитов, признак %+v, ждали работу: %v", s.subj, len(shas), v, s.want)
		}
	}
}
