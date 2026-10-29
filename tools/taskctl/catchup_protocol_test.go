package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCatchupBlocksNestedWriterWithoutDeadlock(t *testing.T) {
	_, wt, _ := laggedWorktree(t, 1)
	bin := filepath.Join(t.TempDir(), "taskctl")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, out)
	}
	git, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	bindir := t.TempDir()
	marker := filepath.Join(bindir, "nested")
	script := "#!/bin/sh\ncase \" $* \" in *\" checkout -q \"*) " + shQuote(bin) + " -C " + shQuote(wt) + " show XR-001 >" + shQuote(marker) + " 2>&1;; esac\nexec " + shQuote(git) + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bindir, "git"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bindir+string(os.PathListSeparator)+os.Getenv("PATH"))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "-C", wt, "catchup").CombinedOutput()
	if ctx.Err() != nil {
		t.Fatal("nested taskctl deadlocked during checkout")
	}
	if err != nil {
		t.Fatalf("catchup: %v %s", err, out)
	}
	data, err := os.ReadFile(marker)
	if err != nil || !strings.Contains(string(data), "внешнее обновление") {
		t.Fatalf("nested writer not blocked: %v %s", err, data)
	}
	if out, err := exec.Command(bin, "-C", wt, "show", "XR-001").CombinedOutput(); err != nil {
		t.Fatalf("barrier not released: %v %s", err, out)
	}
}

func TestCatchupReportsAppliedCheckoutWhenHookFails(t *testing.T) {
	root, wt, _ := laggedWorktree(t, 1)
	target := gitOut(t, root, "rev-parse", "origin/main")
	git, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	bindir := t.TempDir()
	script := "#!/bin/sh\ncase \" $* \" in *\" checkout -q \"*) " + shQuote(git) + " \"$@\" || exit $?; echo hook-failed >&2; exit 7;; esac\nexec " + shQuote(git) + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bindir, "git"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bindir+string(os.PathListSeparator)+os.Getenv("PATH"))
	_, err = cmdCatchup(wt, false)
	if err == nil || !strings.Contains(err.Error(), "checkout применён") {
		t.Fatalf("missing applied outcome: %v", err)
	}
	if got := headAt(t, wt); got != target {
		t.Fatalf("HEAD %s != %s", got, target)
	}
	if out, err := runCLI(t, "-C", wt, "show", "XR-001"); err != nil {
		t.Fatalf("barrier left after applied checkout: %v %s", err, out)
	}
}

func TestCatchupDoesNotRaceActivePublication(t *testing.T) {
	_, wt, _ := laggedWorktree(t, 1)
	before := headAt(t, wt)
	bin := filepath.Join(t.TempDir(), "taskctl")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, out)
	}
	git, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	ready, release := filepath.Join(dir, "ready"), filepath.Join(dir, "release")
	script := "#!/bin/sh\ncase \" $* \" in *\" commit \"*) " + shQuote(git) + " -C " + shQuote(wt) + " reset --hard HEAD >/dev/null || exit $?; touch " + shQuote(ready) + "; while [ ! -f " + shQuote(release) + " ]; do sleep 0.05; done; echo simulated-commit-failure >&2; exit 7;; esac\nexec " + shQuote(git) + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "-C", wt, "review", "level", "XR-001", "2", "publication race", "-m", "test publication")
	output := make(chan []byte, 1)
	go func() { out, _ := cmd.CombinedOutput(); output <- out }()
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		select {
		case out := <-output:
			t.Fatalf("publication exited before commit: %s", out)
		case <-ctx.Done():
			t.Fatal("publication did not reach commit")
		case <-time.After(20 * time.Millisecond):
		}
	}
	defer os.WriteFile(release, nil, 0600)
	if _, err := cmdCatchup(wt, false); err == nil || !strings.Contains(err.Error(), "публикация Git не завершена") {
		t.Fatalf("catchup ignored active publication: %v", err)
	}
	if headAt(t, wt) != before {
		t.Fatal("checkout raced publication")
	}
	if err := os.WriteFile(release, nil, 0600); err != nil {
		t.Fatal(err)
	}
	select {
	case out := <-output:
		if !strings.Contains(string(out), "simulated-commit-failure") {
			t.Fatalf("publication failure not reported: %s", out)
		}
	case <-ctx.Done():
		t.Fatal("publication did not finish")
	}
	if _, err := cmdCatchup(wt, false); err != nil {
		t.Fatalf("catchup after publication: %v", err)
	}
}
