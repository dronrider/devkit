package draftmeta

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGuardDraft(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "docs", "tasks", "drafts", "XR-1.md")
	if err := GuardDraft(root, "XR-1"); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		content string
		blocked bool
	}{{"# запись\n", false}, {"# запись\nзамок: locked\n", true}, {"# запись\nзамок: broken\n", true}, {"# запись\nзамок: unlocked\n", false}} {
		if err := os.WriteFile(path, []byte(tc.content), 0600); err != nil {
			t.Fatal(err)
		}
		err := GuardDraft(root, "XR-1")
		if (err != nil) != tc.blocked {
			t.Fatalf("%s: %v", tc.content, err)
		}
		if strings.Contains(tc.content, "locked\n") && !strings.Contains(tc.content, "unlocked") && (err == nil || !strings.Contains(err.Error(), "ждёт локальную модель")) {
			t.Fatalf("locked: %v", err)
		}
	}
}
