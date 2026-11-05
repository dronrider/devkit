package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestArchiveBoardRowMetadata(t *testing.T) {
	r := &ArchRow{ID: "XR-010", Cells: []string{"XR-010", "Задача [приёмка: user]", "bug", "P1", "2026-11-01", "-", "55 (50+0+0+5+0)", "L", "user"}}
	row, legacy, err := archiveBoardRow(r)
	if err != nil || legacy || row.RParts != [5]int{50, 0, 0, 5, 0} || row.Cost != "L" || acceptOf(row.Title) != "user" {
		t.Fatalf("%+v %v %v", row, legacy, err)
	}
	r.Cells = r.Cells[:6]
	if row, legacy, err = archiveBoardRow(r); err != nil || !legacy || row != nil {
		t.Fatalf("legacy: %+v %v %v", row, legacy, err)
	}
}

func TestReopenCountIgnoresExamples(t *testing.T) {
	path := filepath.Join(t.TempDir(), "XR-001.md")
	mark := "- Возврат из архива 2026-11-05: причина (закрыта 2026-10-01). <!-- taskctl:reopen -->"
	doc := "# Задача\n\n## Ход работы\n\n" + mark + "\n\n```md\n" + mark + "\n```\n\n## Проверка\n\n" + mark + "\n"
	if err := os.WriteFile(path, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := reopenCountFile(path); got != 1 {
		t.Fatalf("возвратов %d вместо 1", got)
	}
}
