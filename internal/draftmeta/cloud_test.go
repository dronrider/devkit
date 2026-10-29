package draftmeta

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestCloudLocksSerializeLaunchAndPrivateAttach(t *testing.T) {
	home := t.TempDir()
	shared, err := CloudShared(home, "xr-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = CloudExclusive(home, "XR-1"); err == nil {
		t.Fatal("attachment entered while client active")
	}
	if err = shared.Close(); err != nil {
		t.Fatal(err)
	}
	exclusive, err := CloudExclusive(home, "XR-1")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		l, err := CloudShared(home, "XR-1")
		if err == nil {
			err = l.Close()
		}
		done <- err
	}()
	select {
	case err := <-done:
		exclusive.Close()
		t.Fatalf("launch entered early: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	if err = exclusive.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("launch stuck")
	}
}

func TestCloudLockSharedAcrossWorktrees(t *testing.T) {
	root := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root, "-c", "user.name=stand", "-c", "user.email=stand@example.invalid"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git: %v %s", err, out)
		}
	}
	git("init", "-q")
	if err := os.WriteFile(filepath.Join(root, "fixture"), []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	git("add", "fixture")
	git("commit", "-qm", "fixture")
	tree := filepath.Join(t.TempDir(), "tree")
	git("worktree", "add", "-qb", "fixture-tree", tree)
	lock, err := CloudShared(tree, "xr-1")
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if l, err := CloudExclusive(root, "XR-1"); err == nil {
		l.Close()
		t.Fatal("different worktree entered")
	}
}
