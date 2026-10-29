package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStagingImagesLeaveSourceLast(t *testing.T) {
	root := setup(t)
	if _, err := cmdDraft(root, "исходный материал", "mid", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(draftPath(root, "XR-008"))
	if err != nil {
		t.Fatal(err)
	}
	s, err := newStagingBoard(root)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err = os.Stat(filepath.Join(s.root, ".git")); !os.IsNotExist(err) {
		t.Fatalf("staging contains git: %v", err)
	}
	if err = os.WriteFile(draftPath(s.root, "XR-008"), []byte("receipt"), 0644); err != nil {
		t.Fatal(err)
	}
	newFile := filepath.Join(s.root, "docs", "tasks", "XR-009.md")
	if err = os.WriteFile(newFile, []byte("new"), 0644); err != nil {
		t.Fatal(err)
	}
	changes, err := s.changes(map[string]bool{draftRel("XR-008"): true})
	if err != nil || len(changes) != 2 || changes[1].Path != draftRel("XR-008") {
		t.Fatalf("changes %+v: %v", changes, err)
	}
	after, err := os.ReadFile(draftPath(root, "XR-008"))
	if err != nil || string(after) != string(original) {
		t.Fatalf("live source changed: %v", err)
	}
}
