package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// bareOrigin заводит origin.git и пушет туда main из root, тем же способом,
// что laggedWorktree в stale_test.go.
func bareOrigin(t *testing.T, root string) string {
	t.Helper()
	bare := filepath.Join(t.TempDir(), "origin.git")
	gitOut(t, root, "init", "-q", "--bare", bare)
	gitOut(t, root, "remote", "add", "origin", bare)
	gitOut(t, root, "push", "-q", "origin", "main")
	return bare
}

// TestLintMainAheadWithCode: находка DK-602 называет число код-коммитов
// впереди origin и первый из них по имени, до отказа пуша это видно заранее.
func TestLintMainAheadWithCode(t *testing.T) {
	root := setup(t)
	gitSetup(t, root)
	bareOrigin(t, root)
	if err := os.WriteFile(filepath.Join(root, "app.txt"), []byte("code\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitOut(t, root, "add", "app.txt")
	gitOut(t, root, "commit", "-q", "-m", "fix: мелкий баг")

	finds, err := cmdLint(root)
	if err != nil {
		t.Fatal(err)
	}
	var found string
	for _, f := range finds {
		if strings.Contains(f, "main впереди") {
			found = f
			break
		}
	}
	if found == "" {
		t.Fatalf("нет находки про main впереди origin: %v", finds)
	}
	if !strings.Contains(found, "1 коммит") || !strings.Contains(found, "fix: мелкий баг") {
		t.Fatalf("находка не называет число и коммит: %q", found)
	}
}

// TestLintMainAheadSilentOnBoardOnly: чистая доска впереди origin находки не
// даёт, регрессия того же рода, что и раньше у пуша: сообщение только про
// код.
func TestLintMainAheadSilentOnBoardOnly(t *testing.T) {
	root := setup(t)
	gitSetup(t, root)
	bareOrigin(t, root)
	p := filepath.Join(root, "docs", "tasks", "XR-001.md")
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, append(data, []byte("ход\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	gitOut(t, root, "add", ".")
	gitOut(t, root, "commit", "-q", "-m", "docs(tasks): XR-001 ход")

	finds, err := cmdLint(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range finds {
		if strings.Contains(f, "main впереди") {
			t.Fatalf("чистая доска впереди origin не должна давать находку: %q", f)
		}
	}
}

// TestLintMainAheadSilentWithoutOrigin: без origin (или без опережения)
// находка молчит, а не падает.
func TestLintMainAheadSilentWithoutOrigin(t *testing.T) {
	root := setup(t)
	gitSetup(t, root)

	finds, err := cmdLint(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range finds {
		if strings.Contains(f, "main впереди") {
			t.Fatalf("без origin находки быть не должно: %q", f)
		}
	}
}
