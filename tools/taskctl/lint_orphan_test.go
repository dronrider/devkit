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

// TestLintOrphanSatelliteFile: сиротой бывает и файл-спутник без расширения
// .md (сценарий проверки, лог): хозяина ID-префикса нет ни на доске, ни в
// архиве, либо хозяин закрыт, а спутник остался среди живых файлов (DK-167).
// Спутник живой задачи не находка, а имя без ID-префикса не смотрится вовсе.
func TestLintOrphanSatelliteFile(t *testing.T) {
	root := setup(t)
	tasks := filepath.Join(root, "docs", "tasks")
	for _, name := range []string{
		"XR-005-check.sh",   // живая XR-005, не находка
		"XR-0005-check.sh",  // правка ID задним числом
		"XR-0007-check.sh",  // хозяин в архиве, спутник среди живых
		"round2.log",        // без ID-префикса
		"x-0007-helper.log", // строчная буква, не ID доски
	} {
		if err := os.WriteFile(filepath.Join(tasks, name), []byte("#!/bin/sh\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	archiveByHand(t, root, "XR-0007")
	finds, err := cmdLint(root)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(finds, "\n")
	if !strings.Contains(joined, "файл-спутник docs/tasks/XR-0005-check.sh: хозяина XR-0005 нет ни на доске, ни в архиве") {
		t.Fatalf("осиротевший спутник не найден или назван не хозяином: %v", finds)
	}
	if !strings.Contains(joined, "файл-спутник docs/tasks/XR-0007-check.sh: спутник закрытой задачи XR-0007 лежит среди живых") {
		t.Fatalf("спутник закрытой задачи среди живых не найден: %v", finds)
	}
	if strings.Contains(joined, "XR-005-check.sh") || strings.Contains(joined, "round2.log") ||
		strings.Contains(joined, "x-0007-helper.log") {
		t.Fatalf("лишние находки по спутникам: %v", finds)
	}
}
