package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestRehydrateReopenedFailureKeepsFilesAndIndex(t *testing.T) {
	root := setup(t)
	src := filepath.Join(root, "docs", "tasks", "archive", "2026", "XR-100.md")
	dst := taskFilePath(root, "XR-100")
	if err := os.MkdirAll(filepath.Dir(src), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, []byte("# Цель\n\n## Задачи цели\n\n- XR-004\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	gitSetup(t, root)
	before, err := collectDocs(root)
	if err != nil {
		t.Fatal(err)
	}
	index := gitOut(t, root, "ls-files", "--stage")
	b, err := LoadBoard(boardPath(root))
	if err != nil {
		t.Fatal(err)
	}
	// Сбой разбора возникает после восстановления файла на проекции.
	b.Lines[b.find("XR-004").LineIdx] = "| неверная строка |"
	if _, err := rehydrateReopened(root, b, src, dst); err == nil {
		t.Fatal("неверная строка должна сорвать пересчёт")
	}
	after, err := collectDocs(root)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("ошибка пересчёта изменила реальные файлы или режимы")
	}
	if got := gitOut(t, root, "ls-files", "--stage"); got != index {
		t.Fatal("ошибка пересчёта изменила индекс")
	}
	if _, err := os.Stat(dst); !os.IsNotExist(err) {
		t.Fatalf("после ошибки осталась восстановленная копия: %v", err)
	}
}
