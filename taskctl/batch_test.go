package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Бэклог под отбор пачки: сверху строки, которые отсеиваются каждая своим
// критерием, ниже годные, в самом хвосте лишняя годная под лимит. У XR-102,
// XR-103, XR-104 и XR-114 нарушено по два соседних критерия сразу, группа у
// них ожидается по первой непрошедшей проверке.
const fixtureBatchBoard = `# Тест: доска (префикс XR)

## In progress

| ID | Задача | Тип | P | R | Цена | Ссылка |
|--------|--------|-----|---|---|------|--------|
| XR-100 | Уже в работе | task | P3 | 16 (0+10+1+0+5) | S | - |

## Check

| ID | Задача | Тип | P | R | Цена | Ссылка |
|--------|--------|-----|---|---|------|--------|
| XR-113 | Ждёт проверки | task | P3 | 5 (0+0+0+0+5) | S | - |

## Backlog

| ID | Задача | Тип | P | R | Цена | Ссылка |
|--------|--------|-----|---|---|------|--------|
| XR-101 | Ждёт соседа [после XR-108] | task | P3 | 16 (0+10+1+0+5) | S | - |
| XR-102 | Дорогая, мутная и ждёт [после XR-108] | task | P3 | 15 (0+8+2+0+5) | L | - |
| XR-103 | Мутная и ждёт [после XR-108] | task | P3 | 14 (0+7+2+0+5) | S | - |
| XR-104 | Дизайн, к тому же дорогой | LLD | P3 | 13 (0+8+0+0+5) | L | (LLD рядом) |
| XR-105 | Не оценена | task | P3 | 12 (0+6+1+0+5) | - | - |
| XR-106 | Годная первая | task | P3 | 11 (0+5+1+0+5) | M | [tasks/XR-106.md](tasks/XR-106.md) |
| XR-107 | Та же ссылка, что у XR-106 | bug | P3 | 10 (0+5+0+0+5) | S | [tasks/XR-106.md](tasks/XR-106.md) |
| XR-114 | Та же ссылка и ждёт [после XR-108] | task | P3 | 10 (0+5+0+0+5) | S | [tasks/XR-106.md](tasks/XR-106.md) |
| XR-108 | Годная вторая | task | P3 | 9 (0+3+1+0+5) | S | - |
| XR-109 | Годная третья, ссылка тоже прочерк | bug | P3 | 8 (0+3+0+0+5) | S | - |
| XR-110 | Зависимость закрыта [после XR-007] | task | P3 | 7 (0+1+1+0+5) | M | (LLD рядом) |
| XR-111 | Годная пятая | task | P3 | 6 (0+1+0+0+5) | S | [tasks/XR-111.md](tasks/XR-111.md) |
| XR-112 | Годная, но лишняя | task | P3 | 5 (0+0+0+0+5) | S | - |

## Blocked

Нет.
`

func setupBatch(t *testing.T, board string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(boardPath(root), []byte(board), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(archivePath(root), []byte(fixtureArchive), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

// TestBatchSelect: полный отбор по фикстуре. Держит и критерии по отдельности,
// и порядок проверок, и обход всего бэклога (XR-110 и XR-111 берутся из
// хвоста, когда верхние строки отсеяны), и то, что зависимость на архивную
// задачу отбору не мешает, а прочерк ссылки не склеивает задачи в серию.
func TestBatchSelect(t *testing.T) {
	root := setupBatch(t, fixtureBatchBoard)
	out, err := cmdBatch(root, batchDefaultLimit)
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{
		"batch: XR-106, XR-108, XR-109, XR-110, XR-111",
		"взято: XR-106 (task, M, неопр. 1), XR-108 (task, S, неопр. 1), XR-109 (bug, S, неопр. 0), XR-110 (task, M, неопр. 1), XR-111 (task, S, неопр. 0)",
		"ждут незакрытых зависимостей: XR-101, XR-114",
		"цена не S и не M: XR-102 (L), XR-105 (-)",
		"неопределённость выше 1: XR-103 (2)",
		"тип LLD: XR-104",
		"ссылка та же, что у отобранной: XR-107 (как у XR-106)",
		"лимит пачки 5 исчерпан: XR-112",
		batchTail,
	}, "\n")
	if out != want {
		t.Fatalf("вывод отбора:\n%s\n\nожидал:\n%s", out, want)
	}
	// Секции кроме Backlog в отбор не входят и в отказах не печатаются.
	for _, id := range []string{"XR-100", "XR-113"} {
		if strings.Contains(out, id) {
			t.Errorf("%s не из Backlog, а попал в вывод", id)
		}
	}
}

// TestBatchLimit: лимит режет пачку ровно по переданному числу, остаток
// уходит в свою группу с этим же числом в подписи.
func TestBatchLimit(t *testing.T) {
	root := setupBatch(t, fixtureBatchBoard)
	out, err := cmdBatch(root, 2)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "batch: XR-106, XR-108\n") {
		t.Fatalf("пачка на лимите 2: %s", out)
	}
	if !strings.Contains(out, "лимит пачки 2 исчерпан: XR-109, XR-110, XR-111, XR-112") {
		t.Fatalf("остаток на лимите 2: %s", out)
	}
	if _, err := cmdBatch(root, 0); err == nil {
		t.Error("лимит 0 должен падать с ошибкой")
	}
}

// TestBatchLinkBeforeLimit: ссылка проверяется раньше лимита, поэтому задача
// из серии остаётся в своей группе даже на исчерпанной пачке.
func TestBatchLinkBeforeLimit(t *testing.T) {
	root := setupBatch(t, fixtureBatchBoard)
	out, err := cmdBatch(root, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "ссылка та же, что у отобранной: XR-107 (как у XR-106)") {
		t.Fatalf("XR-107 ожидался в группе ссылки: %s", out)
	}
	if strings.Contains(out, "исчерпан: XR-107") {
		t.Fatalf("XR-107 не должен уходить в группу лимита: %s", out)
	}
}

// TestBatchEmpty: пустая пачка не молчит, а печатает «batch: -» и все группы
// отказов; хвостовая строка про то, чего доска не видит, тут не нужна,
// сверять нечего.
func TestBatchEmpty(t *testing.T) {
	board := `# Тест: доска (префикс XR)

## In progress

| ID | Задача | Тип | P | R | Цена | Ссылка |
|--------|--------|-----|---|---|------|--------|

## Check

| ID | Задача | Тип | P | R | Цена | Ссылка |
|--------|--------|-----|---|---|------|--------|

## Backlog

| ID | Задача | Тип | P | R | Цена | Ссылка |
|--------|--------|-----|---|---|------|--------|
| XR-104 | Дизайн | LLD | P3 | 13 (0+8+0+0+5) | S | (LLD рядом) |
| XR-105 | Не оценена | task | P3 | 12 (0+6+1+0+5) | - | - |

## Blocked

Нет.
`
	root := setupBatch(t, board)
	out, err := cmdBatch(root, batchDefaultLimit)
	if err != nil {
		t.Fatal(err)
	}
	want := "batch: -\nтип LLD: XR-104\nцена не S и не M: XR-105 (-)"
	if out != want {
		t.Fatalf("вывод на пустой пачке:\n%s\n\nожидал:\n%s", out, want)
	}
}
