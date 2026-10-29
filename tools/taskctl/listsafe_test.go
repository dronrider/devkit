package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSafeBoardListHidesProtectedTitles(t *testing.T) {
	root := setup(t)
	board, err := os.ReadFile(boardPath(root))
	if err != nil {
		t.Fatal(err)
	}
	board = []byte(strings.Replace(string(board), "Средняя", "секретный заголовок", 1))
	if err = os.WriteFile(boardPath(root), board, 0644); err != nil {
		t.Fatal(err)
	}
	path := taskFileAbs(root, "XR-001")
	if err = os.WriteFile(path, []byte("# секретный заголовок\nзащита: locked\n\n## Тело\nчастное тело\n"), 0644); err != nil {
		t.Fatal(err)
	}
	archive := strings.Replace(fixtureArchive, "Закрытая", "архивный секрет", 1)
	if err = os.WriteFile(archivePath(root), []byte(archive), 0644); err != nil {
		t.Fatal(err)
	}
	archived := filepath.Join(root, "docs", "tasks", "archive", "2026", "XR-007.md")
	if err = os.MkdirAll(filepath.Dir(archived), 0755); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(archived, []byte("# архивный секрет\nзащита: locked\n"), 0644); err != nil {
		t.Fatal(err)
	}
	for _, flags := range [][]string{{"--safe"}, {"--safe", "--json"}} {
		args := append([]string{"-C", root, "list"}, flags...)
		out, err := runCLI(t, args...)
		if err != nil || strings.Contains(out, "секрет") || strings.Contains(out, "частное тело") || !strings.Contains(out, "ждёт локальную модель") {
			t.Fatalf("safe: %v %s", err, out)
		}
	}
	out, err := runCLI(t, "-C", root, "list")
	if err != nil || !strings.Contains(out, "секретный заголовок") {
		t.Fatalf("local: %v %s", err, out)
	}
}
