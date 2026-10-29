package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAgentRunRefusesUnconfirmedGroomResult(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "docs", "tasks", "drafts", "XR-1.md")
	if err := os.MkdirAll(filepath.Dir(source), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("# XR-1: источник\n"), 0600); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "docs", "tasks", "XR-2.md")
	if err := os.WriteFile(target, []byte("# XR-2: задача\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("DEVKIT_HOME", t.TempDir())
	t.Setenv("PATH", t.TempDir())
	t.Setenv("DEVKIT_GROOM_SOURCE", "XR-1")
	t.Setenv("DEVKIT_GROOM_MODE", "run")
	var out, errout bytes.Buffer
	_, err := cmdRun(root, "XR-2", "exec", "", root, &out, &errout)
	if err == nil || !strings.Contains(err.Error(), "только принятым результатам add") {
		t.Fatalf("unconfirmed agent run: %v %s %s", err, out.String(), errout.String())
	}
}
