package draftmeta

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHeaderDefaultsAndSections(t *testing.T) {
	h, err := Parse([]byte("# XR-1: идея\n\nзаписан 2026-10-29\nприоритет: высокий\n\n## Текст\nзамок: locked\nтип: milestone\n"))
	if err != nil {
		t.Fatal(err)
	}
	if h.Type != "task" || h.Source != "thought" || h.Lock != "unlocked" || h.Priority != "high" || h.Written.Format("2006-01-02") != "2026-10-29" {
		t.Fatalf("header: %+v", h)
	}
}

func TestHeaderExplicitAndInvalidLock(t *testing.T) {
	h, err := Parse([]byte("# Запись\nтип: agreement\nисточник: meeting\nзамок: locked\n"))
	if err != nil || h.Type != "agreement" || h.Source != "meeting" || h.Lock != "locked" {
		t.Fatalf("header: %+v, %v", h, err)
	}
	for _, input := range []string{"замок locked", "замок=locked", "замок: неизвестно", "замок:", "замок: locked\nзамок: unlocked", "источник: unknown"} {
		if _, err := Parse([]byte("# Запись\n" + input)); err == nil {
			t.Errorf("accepted %q", input)
		}
	}
}

func TestReadReportsPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "draft.md")
	if err := os.WriteFile(path, []byte("# Запись\nзамок: typo\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(path); err == nil || !strings.Contains(err.Error(), path) {
		t.Fatalf("error: %v", err)
	}
}
