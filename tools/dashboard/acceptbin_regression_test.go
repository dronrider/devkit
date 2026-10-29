package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestTaskctlAcceptBinUsesNamedExecutable(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "installed-taskctl")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nprintf 'installed-test-bin'\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TASKCTL_ACCEPT_BIN", bin)
	old := realTaskctlPath
	t.Cleanup(func() { realTaskctlPath = old })
	got := buildTaskctl(t)
	if got != bin {
		t.Fatalf("получен %s вместо %s", got, bin)
	}
	out, err := exec.Command(got).Output()
	if err != nil || string(out) != "installed-test-bin" {
		t.Fatalf("%s %v", out, err)
	}
}
