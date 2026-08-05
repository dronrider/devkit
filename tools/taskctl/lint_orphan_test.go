package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLintOrphanTaskFile: правка ID строки задним числом оставляет файл под
// прежним именем, и вместе с ним уезжают замечания ревью и запись слитых
// коммитов, по которой shipctl держит очередь выката.
func TestLintOrphanTaskFile(t *testing.T) {
	root := setup(t)
	if _, err := cmdSet(root, SetParams{ID: "XR-005", Title: "та же задача, ID правили руками"}); err != nil {
		t.Fatal(err)
	}
	finds, err := cmdLint(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(finds) != 0 {
		t.Fatalf("на чистой доске находок быть не должно: %v", finds)
	}

	if err := os.Rename(filepath.Join(root, "docs", "tasks", "XR-005.md"),
		filepath.Join(root, "docs", "tasks", "XR-0005.md")); err != nil {
		t.Fatal(err)
	}
	finds, err = cmdLint(root)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(finds, "\n"), "docs/tasks/XR-0005.md ни на доске, ни в архиве") {
		t.Fatalf("осиротевший файл задачи не найден: %v", finds)
	}
}
