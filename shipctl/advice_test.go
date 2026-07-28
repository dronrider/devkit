package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestMergeRegcheckWarning: слияние bug-задачи без зелёного regcheck в журнале
// получает подсказку; зелёный прогон и тип task её снимают. Подсказка живёт
// только в проектах с .devkit, где журнал вообще ведётся.
func TestMergeRegcheckWarning(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	if err := os.Mkdir(filepath.Join(root, ".devkit"), 0o755); err != nil {
		t.Fatal(err)
	}
	branchWithFix(t, root)
	msg, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "нет зелёного regcheck") {
		t.Fatalf("нет подсказки про regcheck: %q", msg)
	}

	// Зелёный regcheck за время жизни ветки снимает подсказку.
	root, _ = setup(t, rowInProg, "")
	if err := os.Mkdir(filepath.Join(root, ".devkit"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, root, ".devkit/log",
		fmt.Sprintf("%s\tregcheck\trun\t0\n", time.Now().Format("2006-01-02T15:04:05")))
	branchWithFix(t, root)
	msg, err = cmdMerge(root, MergeParams{ID: "XR-001", Test: "true"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(msg, "regcheck") {
		t.Fatalf("подсказка при зелёном regcheck: %q", msg)
	}

	// Красный запуск подсказку не снимает.
	if regcheckLogged(writeLog(t, "2026-01-01T10:00:00\tregcheck\trun\t1\n"), time.Unix(0, 0)) {
		t.Fatal("красный regcheck засчитан за прогон")
	}

	// Для задачи типа task подсказки нет и без журнала.
	root, _ = setup(t, rowInProg3, "")
	if err := os.Mkdir(filepath.Join(root, ".devkit"), 0o755); err != nil {
		t.Fatal(err)
	}
	branchFor(t, root, "XR-003", "xr-003-fix", "b.txt")
	msg, err = cmdMerge(root, MergeParams{ID: "XR-003", Test: "true"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(msg, "regcheck") {
		t.Fatalf("подсказка про regcheck у task-задачи: %q", msg)
	}
}

func writeLog(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "log")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

const boardTable7 = "| ID | Задача | Тип | P | R | Цена | Ссылка |\n|--------|--------|-----|---|---|------|--------|\n"

// setBoard7 подменяет доску семиколоночной (с «Ценой»): XR-001 крупная, XR-003
// мелкая, обе в In progress.
func setBoard7(t *testing.T, root string) {
	t.Helper()
	board := "# Тест: задачи (префикс XR)\n\n## In progress\n\n" + boardTable7 +
		"| XR-001 | Крупная правка | bug | P1 | 55 (50+0+0+5+0) | L | [tasks/XR-001.md](tasks/XR-001.md) |\n" +
		"| XR-003 | Вторая мелочь | task | P3 | 8 (0+3+0+5+0) | S | - |\n" +
		"\n## Check\n\nНет.\n\n## Backlog\n\nНет.\n\n## Blocked\n\nНет.\n"
	write(t, root, "docs/TASKS.md", board)
	gitT(t, root, "add", ".")
	gitT(t, root, "commit", "-qm", "board: цена")
}

// TestTrainCriteriaWarnings: мягкие критерии поезда проговариваются в отчёте
// merge --train: цена не S/M, пересечение файлов с задачами поезда, перебор
// размера. Слияние при этом проходит.
func TestTrainCriteriaWarnings(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	setBoard7(t, root)

	branchFor(t, root, "XR-001", "xr-001-fix", "a.txt")
	msg, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true", Train: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "цена XR-001 это L") {
		t.Fatalf("нет предупреждения про цену: %q", msg)
	}

	// Вторая задача трогает тот же файл, что уже слитая в поезд XR-001.
	branchFor(t, root, "XR-003", "xr-003-fix", "a.txt")
	msg, err = cmdMerge(root, MergeParams{ID: "XR-003", Test: "true", Train: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "файлы задач поезда") || !strings.Contains(msg, "XR-001: a.txt") {
		t.Fatalf("нет предупреждения про пересечение файлов: %q", msg)
	}
	if strings.Contains(msg, "цена") {
		t.Fatalf("цена S не должна давать предупреждения: %q", msg)
	}

	// Пять задач в поезде: предупреждение о размере (юнит, без пяти слияний).
	b, err := loadBoard(root)
	if err != nil {
		t.Fatal(err)
	}
	ws := trainWarnings(root, "main", "HEAD", b, "XR-003", []string{"A-1", "A-2", "A-3", "A-4", "A-5"})
	joined := strings.Join(ws, "\n")
	if !strings.Contains(joined, "больше 3-5 не копят") {
		t.Fatalf("нет предупреждения о размере поезда: %v", ws)
	}
}
