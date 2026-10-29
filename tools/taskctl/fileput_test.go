package main

import (
	"os"
	"strings"
	"testing"
)

func TestFilePutPreservesProtection(t *testing.T) {
	root := setup(t)
	path := taskFileAbs(root, "XR-001")
	if err := os.WriteFile(path, []byte("# XR-001\nзащита: locked\n\n## Что происходит\nтекст\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdFilePut(root, "XR-001", "# XR-001\nновый текст", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(data), "защита: locked") || !strings.Contains(string(data), "новый текст") {
		t.Fatalf("file %s %v", data, err)
	}
	if _, err = cmdFilePut(root, "XR-999", "# нет", CommitOpts{}); err == nil {
		t.Fatal("missing row accepted")
	}
	if _, err = cmdFilePut(root, "XR-001", "", CommitOpts{}); err == nil {
		t.Fatal("empty text accepted")
	}
}
