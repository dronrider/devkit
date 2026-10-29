package draftmeta

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dronrider/devkit/internal/drafttx"
)

func TestGuardDraft(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "docs", "tasks", "drafts", "XR-1.md")
	if err := GuardRecord(root, "XR-1"); err == nil {
		t.Fatal("missing source accepted")
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
		err := GuardRecord(root, "XR-1")
		if (err != nil) != tc.blocked {
			t.Fatalf("%s: %v", tc.content, err)
		}
		if strings.Contains(tc.content, "locked\n") && !strings.Contains(tc.content, "unlocked") && (err == nil || !strings.Contains(err.Error(), "ждёт локальную модель")) {
			t.Fatalf("locked: %v", err)
		}
	}
}

func TestRecordProtectionSurvivesSourceChange(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "docs", "tasks", "XR-2.md")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	protected, err := ProtectRecord([]byte("# XR-2\n\n## Текст\nматериал\n"))
	if err != nil {
		t.Fatal(err)
	}
	again, err := ProtectRecord(protected)
	if err != nil || string(again) != string(protected) {
		t.Fatalf("not idempotent %q %v", again, err)
	}
	if err = os.WriteFile(path, protected, 0644); err != nil {
		t.Fatal(err)
	}
	if err = GuardRecord(root, "XR-2"); err == nil || !strings.Contains(err.Error(), "ждёт локальную модель") {
		t.Fatalf("guard %v", err)
	}
}

func TestRecordProtectionRejectsMalformedAndDuplicates(t *testing.T) {
	for _, text := range []string{"# задача\nзащита: broken\n", "# задача\nзащита=locked\n", "# задача\nзащита: locked\nзащита: locked\n"} {
		if _, err := RecordProtected([]byte(text)); err == nil {
			t.Fatalf("invalid protection accepted: %s", text)
		}
	}
}

func TestGuardReadsAfterPendingTransaction(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "docs", "tasks", "drafts", "XR-1.md")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("# запись\nзамок: unlocked\n"), 0600); err != nil {
		t.Fatal(err)
	}
	lock, err := drafttx.Acquire(root)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- GuardRecord(root, "XR-1") }()
	select {
	case err := <-done:
		lock.Close()
		t.Fatalf("guard read before transaction: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	if err = os.WriteFile(path, []byte("# запись\nзамок: locked\n"), 0600); err != nil {
		lock.Close()
		t.Fatal(err)
	}
	if err = lock.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "ждёт локальную модель") {
			t.Fatalf("guard %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("guard stayed blocked")
	}
}

func TestGuardArchivedProtection(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "docs", "tasks", "archive", "2026", "XR-2.md")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	for _, content := range []string{"# XR-2\nзащита: locked\n", "# XR-2\nзащита: broken\n"} {
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		if err := GuardRecord(root, "XR-2"); err == nil {
			t.Fatalf("archive accepted: %q", content)
		}
	}
}

func TestGuardMissingSourceFailsClosed(t *testing.T) {
	root := t.TempDir()
	if err := GuardRecord(root, "XR-404"); err == nil || !strings.Contains(err.Error(), "отсутствует") {
		t.Fatalf("missing accepted: %v", err)
	}
}

func TestGuardErrorDoesNotExposeMalformedPrivateValue(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "docs", "tasks", "drafts", "XR-1.md")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("# секрет заголовка\nзамок: секрет значения\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := GuardRecord(root, "XR-1"); err == nil || strings.Contains(err.Error(), "секрет") {
		t.Fatalf("unsafe error: %v", err)
	}
}
