package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/dronrider/devkit/internal/draftmeta"
)

func TestCloudStartReservationHandoff(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "docs", "tasks", "XR-1.md")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("# XR-1: синтетическая задача\n"), 0600); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	alive := filepath.Join(t.TempDir(), "alive")
	log := filepath.Join(t.TempDir(), "starts")
	writeScript(t, bin, "tmux", "case \"$1\" in new-session) touch '"+alive+"'; echo start >> '"+log+"';; has-session) test -f '"+alive+"';; esac")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	ack := ""
	if err := startReserved(root, root, "XR-1", "task-XR-1", func(path string) string { ack = path; return "agent" }); err != nil {
		t.Fatal(err)
	}
	if l, err := draftmeta.CloudExclusive(root, "XR-1"); err == nil {
		l.Close()
		t.Fatal("attach entered during start")
	}
	child, err := draftmeta.CloudShared(root, "XR-1")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(ack, []byte("ready"), 0600); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1100 * time.Millisecond)
	if l, err := draftmeta.CloudExclusive(root, "XR-1"); err == nil {
		l.Close()
		child.Close()
		t.Fatal("attach entered after handoff")
	}
	if err = child.Close(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		l, err := draftmeta.CloudExclusive(root, "XR-1")
		if err == nil {
			l.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("reservation did not release")
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err = os.Remove(alive); err != nil {
		t.Fatal(err)
	}
}

func TestCloudStartSharesReservationAcrossWorktreeAndHome(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "docs", "tasks", "XR-1.md")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("# XR-1: задача\n"), 0600); err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s %v", args, out, err)
		}
	}
	git("init")
	git("add", "docs/tasks/XR-1.md")
	git("-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-m", "fixture")
	tree := filepath.Join(t.TempDir(), "worktree")
	git("worktree", "add", "-b", "stand", tree)
	bin, alive := t.TempDir(), filepath.Join(t.TempDir(), "alive")
	writeScript(t, bin, "tmux", `case "$1" in new-session) touch '`+alive+`';; has-session) test -f '`+alive+`'; exit $?;; list-sessions) exit 0;; esac`)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("HOME", t.TempDir())
	if err := startReserved(root, tree, "XR-1", "task-XR-1", func(string) string { return "agent" }); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(alive) })
	t.Setenv("HOME", t.TempDir())
	if lock, err := draftmeta.CloudExclusive(tree, "XR-1"); err == nil {
		lock.Close()
		t.Fatal("different HOME/worktree bypassed reservation")
	}
	if err := os.Remove(alive); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if lock, err := draftmeta.CloudExclusive(tree, "XR-1"); err == nil {
			lock.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("reservation did not end")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestCloudStartAfterPrivateAttachRefusesLaunch(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "docs", "tasks", "XR-1.md")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("# XR-1: запись\n"), 0600); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	log := filepath.Join(t.TempDir(), "starts")
	writeScript(t, bin, "tmux", "echo start >> '"+log+"'")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	exclusive, err := draftmeta.CloudExclusive(root, "XR-1")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- startReserved(root, root, "XR-1", "task-XR-1", func(string) string { return "agent" }) }()
	time.Sleep(100 * time.Millisecond)
	data, err := draftmeta.ProtectRecord([]byte("# XR-1: закрытый текст\n"))
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	exclusive.Close()
	if err = <-done; err == nil {
		t.Fatal("private record launched")
	}
	if data, err = os.ReadFile(log); err == nil {
		t.Fatalf("client started: %s", data)
	}
}

func TestCloudStartProbeFailureKeepsReservation(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "docs", "tasks", "XR-1.md")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("# XR-1: задача\n"), 0600); err != nil {
		t.Fatal(err)
	}
	bin, marker := t.TempDir(), filepath.Join(t.TempDir(), "retry")
	if err := os.WriteFile(marker, []byte("fail"), 0600); err != nil {
		t.Fatal(err)
	}
	writeScript(t, bin, "tmux", `case "$1" in
new-session) exit 0;;
has-session) exit 1;;
list-sessions) if [ -f '`+marker+`' ]; then echo "temporary failure" >&2; exit 1; fi; exit 0;;
esac`)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	if err := startReserved(root, root, "XR-1", "task-XR-1", func(string) string { return "agent" }); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1200 * time.Millisecond)
	if lock, err := draftmeta.CloudExclusive(root, "XR-1"); err == nil {
		lock.Close()
		t.Fatal("uncertain probe released reservation")
	}
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if lock, err := draftmeta.CloudExclusive(root, "XR-1"); err == nil {
			lock.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("confirmed dead session left reservation")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestCloudStartSurvivesLauncherExit(t *testing.T) {
	if os.Getenv("DEVKIT_START_CHILD") == "1" {
		root := os.Getenv("DEVKIT_START_ROOT")
		if err := startReserved(root, root, "XR-1", "task-XR-1", func(string) string { return "agent" }); err != nil {
			t.Fatal(err)
		}
		return
	}
	root := t.TempDir()
	path := filepath.Join(root, "docs", "tasks", "XR-1.md")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("# XR-1: задача\n"), 0600); err != nil {
		t.Fatal(err)
	}
	bin, alive := t.TempDir(), filepath.Join(t.TempDir(), "alive")
	writeScript(t, bin, "tmux", `case "$1" in new-session) touch '`+alive+`';; has-session) test -f '`+alive+`'; exit $?;; list-sessions) exit 0;; esac`)
	cmd := exec.Command(os.Args[0], "-test.run=^TestCloudStartSurvivesLauncherExit$")
	cmd.Env = append(os.Environ(), "DEVKIT_START_CHILD=1", "DEVKIT_START_ROOT="+root, "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("launcher: %s %v", out, err)
	}
	t.Cleanup(func() { os.Remove(alive) })
	if lock, err := draftmeta.CloudExclusive(root, "XR-1"); err == nil {
		lock.Close()
		t.Fatal("launcher exit released reservation")
	}
	if err := os.Remove(alive); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if lock, err := draftmeta.CloudExclusive(root, "XR-1"); err == nil {
			lock.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("reservation not released after session death")
		}
		time.Sleep(50 * time.Millisecond)
	}
}
