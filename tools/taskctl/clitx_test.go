package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dronrider/devkit/internal/drafttx"
)

func TestCLIPublicationReleasesLockBeforeHook(t *testing.T) {
	root := setup(t)
	gitSetup(t, root)
	source, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(root, "hook-ran")
	script := "#!/bin/sh\ncd " + shQuote(source) + "\ngo run . -C " + shQuote(root) + " show XR-001 >/dev/null || exit 1\nprintf done > " + shQuote(marker) + "\n"
	writeHook(t, root, "pre-commit", script)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "run", ".", "-C", root, "draft", "--prio", "mid", "-m", "docs(tasks): XR черновик", "тестовая идея")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("draft commit: %v\n%s", err, out)
	}
	if ctx.Err() != nil {
		t.Fatal("hook deadlocked")
	}
	if _, err = os.Stat(marker); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "коммит") {
		t.Fatalf("no commit result: %s", out)
	}
}

func TestCLIPublicationFailureKeepsAppliedDraft(t *testing.T) {
	root := setup(t)
	gitSetup(t, root)
	writeHook(t, root, "pre-commit", "#!/bin/sh\nexit 1\n")
	out, err := runCLI(t, "-C", root, "draft", "--prio", "mid", "-m", "docs(tasks): XR черновик", "тестовая идея")
	if err == nil || !strings.Contains(out, "применено, публикация не завершена") {
		t.Fatalf("publication: %v %s", err, out)
	}
	if _, err = os.Stat(draftPath(root, "XR-008")); err != nil {
		t.Fatalf("applied file missing: %v", err)
	}
}

func TestCLIDraftLocksBeforeAllocatingID(t *testing.T) {
	root := setup(t)
	lock, err := drafttx.Acquire(root)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var output bytes.Buffer
	cmd := exec.CommandContext(ctx, "go", "run", ".", "-C", root, "draft", "--prio", "mid", "новая идея")
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err = cmd.Start(); err != nil {
		lock.Close()
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		lock.Close()
		t.Fatalf("finished before lock released: %v %s", err, output.String())
	case <-time.After(300 * time.Millisecond):
	}
	path := draftPath(root, "XR-020")
	if err = os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		lock.Close()
		t.Fatal(err)
	}
	if err = os.WriteFile(path, []byte("# XR-020: другая запись\n"), 0644); err != nil {
		lock.Close()
		t.Fatal(err)
	}
	if err = lock.Close(); err != nil {
		t.Fatal(err)
	}
	if err = <-done; err != nil || !strings.Contains(output.String(), "XR-021") {
		t.Fatalf("allocation: %v %s", err, output.String())
	}
}
