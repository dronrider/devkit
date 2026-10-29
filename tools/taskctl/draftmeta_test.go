package main

import (
	"path/filepath"
	"testing"
)

func TestDraftMetadataWrite(t *testing.T) {
	root := setup(t)
	kit, err := filepath.Abs("../../kit")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEVKIT_HOME", filepath.Dir(kit))
	if _, err = cmdDraftWithMeta(root, "идея", "", false, "milestone", "meeting", "locked", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	d, err := findDraftFor(root, "XR-008")
	if err != nil || d.Type != "milestone" || d.Source != "meeting" || d.Lock != "locked" {
		t.Fatalf("draft %+v: %v", d, err)
	}
	if _, err = cmdDraftMeta(root, d.ID, "agreement", "mail", "unlocked", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	d, err = findDraftFor(root, d.ID)
	if err != nil || d.Type != "agreement" || d.Source != "mail" || d.Lock != "unlocked" {
		t.Fatalf("draft %+v: %v", d, err)
	}
	for _, bad := range []struct{ k, s, l string }{{"invalid", "", ""}, {"", "bad", ""}, {"", "", "wat"}, {"", "thought\nзамок: locked", ""}} {
		if _, err = cmdDraftMeta(root, d.ID, bad.k, bad.s, bad.l, CommitOpts{}); err == nil {
			t.Fatalf("accepted %+v", bad)
		}
	}
}
