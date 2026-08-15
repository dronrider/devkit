package main

import (
	"strings"
	"testing"
)

// TestCommitBoardTakesTaskFile: на смене статуса taskctl уносит пакет
// отмеченных этапов в раздел «Ход работы» файла задачи (DK-338), и коммит доски
// обязан забрать его с собой. Оставленная правка ловилась бы только предполётом
// следующего merge («в рабочем дереве незакоммиченное»), тем самым случаем, из-за
// которого этапы и уехали из рабочего дерева.
func TestCommitBoardTakesTaskFile(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	write(t, root, "docs/TASKS.md", "# доска после перевода\n")
	write(t, root, "docs/tasks/XR-001.md",
		"# XR-001\n\n## Ход работы\n\n- Разработка: субагент opus/high по вердикту pick, 2026-08-15 10:00-12:00.\n")
	if _, err := commitBoard(root, "docs(tasks): XR-001 в Check", "XR-001"); err != nil {
		t.Fatal(err)
	}
	if st := gitT(t, root, "status", "--porcelain", "--untracked-files=no"); st != "" {
		t.Fatalf("после коммита доски дерево не чистое:\n%s", st)
	}
	files := gitT(t, root, "show", "--name-only", "--format=", "HEAD")
	if !strings.Contains(files, "docs/tasks/XR-001.md") {
		t.Fatalf("файл задачи не попал в коммит доски:\n%s", files)
	}
}

// TestCommitBoardWithoutTaskFile: задача без файла коммит доски не роняет.
// Pathspec по несуществующему пути git отбивает, а строка без файла на доске
// законна, и падать из-за неё нельзя.
func TestCommitBoardWithoutTaskFile(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	write(t, root, "docs/TASKS.md", "# доска после перевода\n")
	if _, err := commitBoard(root, "docs(tasks): XR-404 в Check", "XR-404"); err != nil {
		t.Fatalf("коммит доски упал из-за задачи без файла: %v", err)
	}
	if st := gitT(t, root, "status", "--porcelain", "--untracked-files=no"); st != "" {
		t.Fatalf("после коммита доски дерево не чистое:\n%s", st)
	}
}
