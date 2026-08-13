package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// checkBoardSetup строит доску с задачами в Check (со всеми сочетаниями
// признаков и без файла задачи) в свежем git-репозитории, чтобы list и show
// могли посчитать и пометки, и возраст строк.
func checkBoardSetup(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	tasks := filepath.Join(root, "docs", "tasks")
	if err := os.MkdirAll(tasks, 0o755); err != nil {
		t.Fatal(err)
	}
	board := `# Тест: доска (префикс XR)

## In progress

| ID | Задача | Тип | P | R | Цена | Ссылка |
|--------|--------|-----|---|---|------|--------|
| XR-020 | В работе | task | P3 | 5 (0+3+1+0+1) | S | [tasks/XR-020.md](tasks/XR-020.md) |

## Check (готово, ждёт проверки пользователем)

| ID | Задача | Тип | P | R | Цена | Ссылка |
|--------|--------|-----|---|---|------|--------|
| XR-010 | Со сценарием агента и выкатом | task | P3 | 5 (0+3+1+0+1) | S | [tasks/XR-010.md](tasks/XR-010.md) |
| XR-011 | Пользовательская проверка без выката [приёмка: user] | task | P3 | 5 (0+3+1+0+1) | S | [tasks/XR-011.md](tasks/XR-011.md) |
| XR-012 | Без файла задачи | task | P3 | 5 (0+3+1+0+1) | S | - |

## Backlog

| ID | Задача | Тип | P | R | Цена | Ссылка |
|--------|--------|-----|---|---|------|--------|

## Blocked

Нет.
`
	if err := os.WriteFile(boardPath(root), []byte(board), 0o644); err != nil {
		t.Fatal(err)
	}
	writeTask := func(id, body string) {
		if err := os.WriteFile(filepath.Join(tasks, id+".md"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeTask("XR-020", "# XR-020\n")
	writeTask("XR-010", "# XR-010\n\n## Выкат\n\n- 2026-01-01 слито: 1234567\n\n## Сценарий проверки (агентский)\n\nшаги\n")
	writeTask("XR-011", "# XR-011\n\n## Сценарий проверки\n\nшаги\n")

	gitOut(t, root, "init", "-q", "-b", "main")
	gitOut(t, root, "config", "user.email", "test@test")
	gitOut(t, root, "config", "user.name", "test")
	gitOut(t, root, "add", ".")
	gitCommitDated(t, root, time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC), "init")
	return root
}

// lineAfter возвращает строку, идущую сразу за первым вхождением marker,
// либо пустую строку, если после него ничего нет (строка не найдена или это
// последняя строка вывода).
func lineAfter(t *testing.T, out, marker string) string {
	t.Helper()
	idx := strings.Index(out, marker)
	if idx < 0 {
		t.Fatalf("не нашёл %q в выводе:\n%s", marker, out)
	}
	rest := out[idx:]
	nl := strings.IndexByte(rest, '\n')
	if nl < 0 {
		return ""
	}
	afterRow := rest[nl+1:]
	if i := strings.IndexByte(afterRow, '\n'); i >= 0 {
		return afterRow[:i]
	}
	return afterRow
}

func TestListAnnotatesCheckRows(t *testing.T) {
	root := checkBoardSetup(t)
	old := timeNow
	defer func() { timeNow = old }()
	timeNow = func() time.Time { return time.Date(2026, 1, 11, 13, 0, 0, 0, time.UTC) } // +10 дней

	out, err := cmdList(root, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"| XR-010 | Со сценарием агента и выкатом",
		"  код слит, вид agent, строка не двигалась 10 дней",
		"| XR-011 | Пользовательская проверка без выката [приёмка: user]",
		"  без выката, вид user, строка не двигалась 10 дней",
		"| XR-012 | Без файла задачи",
		"| XR-020 | В работе",
		"  строка не двигалась 10 дней",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("в list нет %q:\n%s", want, out)
		}
	}
	// У строки без файла задачи метки очереди выката нет (файл непрочитан),
	// только вид и возраст: метка вида в Check печатается всегда.
	if got := lineAfter(t, out, "| XR-012 | Без файла задачи"); strings.Contains(got, "код слит") || strings.Contains(got, "без выката") {
		t.Fatalf("у задачи без файла не должно быть метки очереди выката: %q", got)
	}
	// У строки не из Check (In progress) меток очереди/сценария тоже нет,
	// только возраст, ровно тот же текст, что и у остальных строк.
	if got := lineAfter(t, out, "| XR-020 | В работе"); got != "  строка не двигалась 10 дней" {
		t.Fatalf("заметка In progress строки: %q, ожидал только возраст", got)
	}
}

func TestListNoAnnotationsWhenDirty(t *testing.T) {
	root := checkBoardSetup(t)
	// Незакоммиченная правка доски: возраст молчит на всей доске.
	board, err := os.ReadFile(boardPath(root))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(boardPath(root), append(board, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := cmdList(root, "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "строка не двигалась") {
		t.Fatalf("на грязном дереве возраст не должен печататься:\n%s", out)
	}
	// Пометки Check от чистоты дерева не зависят, они идут не из git.
	if !strings.Contains(out, "код слит, вид agent") {
		t.Fatalf("пометки Check обязаны остаться и на грязном дереве:\n%s", out)
	}
}

func TestShowAnnotatesCheckRow(t *testing.T) {
	root := checkBoardSetup(t)
	old := timeNow
	defer func() { timeNow = old }()
	timeNow = func() time.Time { return time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC) } // +1 день

	out, err := cmdShow(root, "XR-010")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"XR-010 в check",
		"код слит, вид agent, строка не двигалась 1 день",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("в show нет %q:\n%s", want, out)
		}
	}
}
