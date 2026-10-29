package main

import (
	"os"
	"strings"
	"testing"
)

func TestFileTailPreservesSections(t *testing.T) {
	root := setup(t)
	path := taskFileAbs(root, "XR-001")
	before := "# XR-001\n\n## Ход работы\n\n- начало\n\n## Проверка\n\nсценарий\n"
	if err := os.WriteFile(path, []byte(before), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdFileTail(root, "XR-001", "фоновая команда"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "- начало\n- Хвост: фоновая команда") || !strings.Contains(string(data), "## Проверка\n\nсценарий") {
		t.Fatalf("tail: %s", data)
	}
	if _, err = cmdFileTail(root, "../outside", "x"); err == nil {
		t.Fatal("invalid ID accepted")
	}
}

func TestCLIGuardRejectsLockedDraftAndTask(t *testing.T) {
	root := setup(t)
	if _, err := cmdDraft(root, "запись", "mid", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	source := draftPath(root, "XR-008")
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	data = []byte(strings.Replace(string(data), "## ", "замок: locked\n\n## ", 1))
	if err = os.WriteFile(source, data, 0644); err != nil {
		t.Fatal(err)
	}
	if out, err := runCLI(t, "-C", root, "guard", "XR-008"); err == nil || !strings.Contains(out, "ждёт локальную модель") {
		t.Fatalf("draft %v %s", err, out)
	}
	target := taskFileAbs(root, "XR-001")
	if err = os.WriteFile(target, []byte("# XR-001\nзащита: locked\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if out, err := runCLI(t, "-C", root, "guard", "XR-001"); err == nil || !strings.Contains(out, "ждёт локальную модель") {
		t.Fatalf("target %v %s", err, out)
	}
}
