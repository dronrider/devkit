package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func setup(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	tasks := filepath.Join(root, "docs", "tasks")
	if err := os.MkdirAll(tasks, 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		boardPath(root):                   fixtureBoard,
		archivePath(root):                 fixtureArchive,
		filepath.Join(tasks, "XR-005.md"): "# XR-005\n",
		filepath.Join(tasks, "XR-002.md"): "# XR-002\n",
	}
	for p, content := range files {
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func backlogIDs(t *testing.T, root string) []string {
	t.Helper()
	b, err := LoadBoard(boardPath(root))
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, r := range b.Sects[SectBacklog].Rows {
		ids = append(ids, r.ID)
	}
	return ids
}

func TestNextID(t *testing.T) {
	root := setup(t)
	id, err := cmdID(root)
	if err != nil {
		t.Fatal(err)
	}
	if id != "XR-008" {
		t.Fatalf("ожидал XR-008, получил %s", id)
	}
}

func TestAddSorted(t *testing.T) {
	root := setup(t)
	// Равный R с XR-002: новая строка с большим номером встаёт ниже.
	if _, err := cmdAdd(root, AddParams{Title: "Равный ранг", Type: "bug", Rank: "50+0+0+5+0", Link: "x"}); err != nil {
		t.Fatal(err)
	}
	// Максимальный ранг встаёт первым, минимальный последним.
	if _, err := cmdAdd(root, AddParams{ID: "XR-020", Title: "Наверх", Type: "task", Rank: "75+0+1+0+0", Link: "x"}); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdAdd(root, AddParams{ID: "XR-021", Title: "В хвост", Type: "task", Rank: "0+0+1+0+0", Link: "x"}); err != nil {
		t.Fatal(err)
	}
	got := strings.Join(backlogIDs(t, root), " ")
	want := "XR-020 XR-002 XR-008 XR-001 XR-003 XR-004 XR-021"
	if got != want {
		t.Fatalf("порядок Backlog: %s, ожидал %s", got, want)
	}
}

func TestAddValidation(t *testing.T) {
	root := setup(t)
	cases := []AddParams{
		{Title: "Дубль", Type: "task", Rank: "0+1+1+0+1", Link: "x", ID: "XR-007"},
		{Title: "С|пайпом", Type: "task", Rank: "0+1+1+0+1", Link: "x"},
		{Title: "Плохой тип", Type: "feature", Rank: "0+1+1+0+1", Link: "x"},
		{Title: "Плохой статус", Type: "task", Rank: "0+1+1+0+1", Link: "x", Status: "done"},
		{Title: "Цена вне шкалы", Type: "task", Rank: "0+1+1+0+1", Link: "x", Cost: "XXL"},
	}
	for _, p := range cases {
		if _, err := cmdAdd(root, p); err == nil {
			t.Errorf("ожидал ошибку на %+v", p)
		}
	}
}

func TestAddWithoutFileAndBareLink(t *testing.T) {
	root := setup(t)
	// Файла задачи нет: в ячейке ссылки плейсхолдер, add не падает.
	if _, err := cmdAdd(root, AddParams{Title: "Однострочник", Type: "task", Rank: "0+1+1+0+1"}); err != nil {
		t.Fatal(err)
	}
	board, _ := os.ReadFile(boardPath(root))
	if !strings.Contains(string(board), "| XR-008 | Однострочник | task | P3 | 3 (0+1+1+0+1) | - | - |") {
		t.Fatalf("нет строки с плейсхолдером:\n%s", board)
	}
	// Голый путь в --link оборачивается в markdown-ссылку.
	if _, err := cmdAdd(root, AddParams{Title: "Голый путь", Type: "task", Rank: "0+1+1+0+1", Link: "tasks/XR-002.md"}); err != nil {
		t.Fatal(err)
	}
	board, _ = os.ReadFile(boardPath(root))
	if !strings.Contains(string(board), "| XR-009 | Голый путь | task | P3 | 3 (0+1+1+0+1) | - | [tasks/XR-002.md](tasks/XR-002.md) |") {
		t.Fatalf("голый путь не обёрнут:\n%s", board)
	}
}

func TestAddWithCost(t *testing.T) {
	root := setup(t)
	if _, err := cmdAdd(root, AddParams{Title: "Оценённая", Type: "task", Rank: "0+1+1+0+1", Cost: "M", Link: "x"}); err != nil {
		t.Fatal(err)
	}
	board, _ := os.ReadFile(boardPath(root))
	if !strings.Contains(string(board), "| XR-008 | Оценённая | task | P3 | 3 (0+1+1+0+1) | M | x |") {
		t.Fatalf("нет строки с ценой:\n%s", board)
	}
}

func TestStatusAliases(t *testing.T) {
	root := setup(t)
	if _, err := cmdAdd(root, AddParams{Title: "Алиас", Type: "task", Rank: "0+1+1+0+1", Status: "In progress"}); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdMove(root, "XR-008", "Check", "", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	b, err := LoadBoard(boardPath(root))
	if err != nil {
		t.Fatal(err)
	}
	if r := b.find("XR-008"); r == nil || r.Sect != SectCheck {
		t.Fatalf("XR-008 не дошла до check: %+v", r)
	}
}

// Регрессия: причина блокировки не должна оставаться в заголовке после
// выхода из Blocked.
func TestMoveUnblockStripsReason(t *testing.T) {
	root := setup(t)
	if _, err := cmdMove(root, "XR-004", SectBlocked, "ждём железо", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdMove(root, "XR-004", SectInProgress, "", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	b, err := LoadBoard(boardPath(root))
	if err != nil {
		t.Fatal(err)
	}
	if got := b.find("XR-004").Title; got != "Хвост" {
		t.Fatalf("заголовок после разблокировки: %q", got)
	}
}

func TestMoveToBlockedAndBack(t *testing.T) {
	root := setup(t)
	if _, err := cmdMove(root, "XR-004", SectBlocked, "", CommitOpts{}); err == nil {
		t.Fatal("blocked без --reason должен падать")
	}
	if _, err := cmdMove(root, "XR-004", SectBlocked, "ждём железо", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	b, err := LoadBoard(boardPath(root))
	if err != nil {
		t.Fatal(err)
	}
	rows := b.Sects[SectBlocked].Rows
	if len(rows) != 1 || !strings.Contains(rows[0].Title, "[блок: ждём железо]") {
		t.Fatalf("Blocked после move: %+v", rows)
	}
	if _, err := cmdMove(root, "XR-004", SectBlocked, "повтор", CommitOpts{}); err == nil {
		t.Fatal("повторный move в ту же секцию должен падать")
	}
	if _, err := cmdMove(root, "XR-004", SectBacklog, "", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	ids := backlogIDs(t, root)
	if ids[len(ids)-1] != "XR-004" {
		t.Fatalf("XR-004 должен вернуться в хвост Backlog: %v", ids)
	}
}

func TestSetTypeInPlace(t *testing.T) {
	root := setup(t)
	msg, err := cmdSet(root, SetParams{ID: "XR-005", Type: "bug"})
	if err != nil {
		t.Fatal(err)
	}
	if msg != "XR-005: тип task -> bug" {
		t.Fatalf("сообщение: %q", msg)
	}
	board, _ := os.ReadFile(boardPath(root))
	old := "| XR-005 | Задача в работе | task | P2 | 30 (25+2+1+0+2) | - | [tasks/XR-005.md](tasks/XR-005.md) |"
	want := strings.Replace(fixtureBoard, old, strings.Replace(old, "task", "bug", 1), 1)
	if string(board) != want {
		t.Fatalf("доска после set отличается не только типом XR-005:\n%s", board)
	}
}

func TestSetRankResortsBacklog(t *testing.T) {
	root := setup(t)
	// Хвост Backlog получает максимальный ранг и должен встать первым.
	msg, err := cmdSet(root, SetParams{ID: "XR-004", Rank: "75+0+1+0+0"})
	if err != nil {
		t.Fatal(err)
	}
	if msg != "XR-004: R 9 -> 76, P P3 -> P0" {
		t.Fatalf("сообщение: %q", msg)
	}
	got := strings.Join(backlogIDs(t, root), " ")
	if want := "XR-004 XR-002 XR-001 XR-003"; got != want {
		t.Fatalf("порядок Backlog: %s, ожидал %s", got, want)
	}
	board, _ := os.ReadFile(boardPath(root))
	if !strings.Contains(string(board), "| XR-004 | Хвост | task | P0 | 76 (75+0+1+0+0) | - | (LLD позже) |") {
		t.Fatalf("строка XR-004 не пересобралась:\n%s", board)
	}
	if finds, err := cmdLint(root); err != nil || len(finds) != 0 {
		t.Fatalf("lint после set: %v, %v", finds, err)
	}
}

func TestSetRankOutsideBacklogStaysPut(t *testing.T) {
	root := setup(t)
	// XR-005 в In progress: ранг меняется, строка остаётся в своей секции.
	if _, err := cmdSet(root, SetParams{ID: "XR-005", Rank: "50+2+1+0+2"}); err != nil {
		t.Fatal(err)
	}
	b, err := LoadBoard(boardPath(root))
	if err != nil {
		t.Fatal(err)
	}
	rows := b.Sects[SectInProgress].Rows
	if len(rows) != 1 || rows[0].ID != "XR-005" || rows[0].P != "P1" || rows[0].RTotal != 55 {
		t.Fatalf("In progress после set: %+v", rows)
	}
}

func TestSetTitleAndLink(t *testing.T) {
	root := setup(t)
	msg, err := cmdSet(root, SetParams{ID: "XR-001", Title: "Новый заголовок", Link: "tasks/XR-002.md"})
	if err != nil {
		t.Fatal(err)
	}
	if msg != "XR-001: заголовок, ссылка" {
		t.Fatalf("сообщение: %q", msg)
	}
	board, _ := os.ReadFile(boardPath(root))
	if !strings.Contains(string(board), "| XR-001 | Новый заголовок | task/LLD | P2 | 30 (25+2+1+0+2) | - | [tasks/XR-002.md](tasks/XR-002.md) |") {
		t.Fatalf("строка не пересобралась:\n%s", board)
	}
}

func TestSetCost(t *testing.T) {
	root := setup(t)
	msg, err := cmdSet(root, SetParams{ID: "XR-005", Cost: "L"})
	if err != nil {
		t.Fatal(err)
	}
	if msg != "XR-005: цена - -> L" {
		t.Fatalf("сообщение: %q", msg)
	}
	board, _ := os.ReadFile(boardPath(root))
	if !strings.Contains(string(board), "| XR-005 | Задача в работе | task | P2 | 30 (25+2+1+0+2) | L | [tasks/XR-005.md](tasks/XR-005.md) |") {
		t.Fatalf("цена не встала в строку:\n%s", board)
	}
	// Обратно в «не оценено» через --cost -.
	if _, err := cmdSet(root, SetParams{ID: "XR-005", Cost: "-"}); err != nil {
		t.Fatal(err)
	}
	board, _ = os.ReadFile(boardPath(root))
	if string(board) != fixtureBoard {
		t.Fatalf("доска не вернулась к исходной после сброса цены:\n%s", board)
	}
}

func TestSetTitleKeepsBlockSuffix(t *testing.T) {
	root := setup(t)
	if _, err := cmdMove(root, "XR-004", SectBlocked, "ждём железо", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdSet(root, SetParams{ID: "XR-004", Title: "Хвост, уточнённый"}); err != nil {
		t.Fatal(err)
	}
	b, err := LoadBoard(boardPath(root))
	if err != nil {
		t.Fatal(err)
	}
	if got := b.find("XR-004").Title; got != "Хвост, уточнённый [блок: ждём железо]" {
		t.Fatalf("заголовок blocked-строки: %q", got)
	}
}

func TestFileCreatesAndRelinks(t *testing.T) {
	root := setup(t)
	msg, err := cmdFile(root, "XR-001", CommitOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "docs/tasks/XR-001.md создан") || !strings.Contains(msg, "ссылка в строке обновлена") {
		t.Fatalf("сообщение: %q", msg)
	}
	data, err := os.ReadFile(filepath.Join(root, "docs", "tasks", "XR-001.md"))
	if err != nil || string(data) != "# XR-001: Средняя\n" {
		t.Fatalf("скелет файла: %q, %v", data, err)
	}
	board, _ := os.ReadFile(boardPath(root))
	if !strings.Contains(string(board), "| XR-001 | Средняя | task/LLD | P2 | 30 (25+2+1+0+2) | - | [tasks/XR-001.md](tasks/XR-001.md) |") {
		t.Fatalf("ссылка не обновилась:\n%s", board)
	}
	if _, err := cmdFile(root, "XR-001", CommitOpts{}); err == nil {
		t.Fatal("повторный file должен падать: и файл, и ссылка уже есть")
	}
}

func TestList(t *testing.T) {
	root := setup(t)
	out, err := cmdList(root, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"In progress (1)", "Check (0)", "Нет.", "Backlog (4)", "Blocked (0)", "| XR-005 |"} {
		if !strings.Contains(out, want) {
			t.Errorf("в list нет %q:\n%s", want, out)
		}
	}
	// Разросшийся Backlog обрезается с подсказкой, list backlog отдаёт целиком.
	for i := 0; i < listBacklogTop; i++ {
		p := AddParams{ID: fmt.Sprintf("XR-%03d", 20+i), Title: "Наполнение", Type: "task", Rank: "0+1+1+0+1", Link: "x"}
		if _, err := cmdAdd(root, p); err != nil {
			t.Fatal(err)
		}
	}
	out, err = cmdList(root, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Backlog (14, первые 10; целиком: taskctl list backlog)") {
		t.Fatalf("нет обрезки Backlog:\n%s", out)
	}
	// 1 строка In progress + 10 обрезанного Backlog.
	if got := strings.Count(out, "\n| XR-"); got != 11 {
		t.Fatalf("строк задач в кратком виде: %d, ожидал 11:\n%s", got, out)
	}
	out, err = cmdList(root, "Backlog")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(out, "\n| XR-"); got != 14 {
		t.Fatalf("строк в list backlog: %d, ожидал 14:\n%s", got, out)
	}
}

func TestShow(t *testing.T) {
	root := setup(t)
	out, err := cmdShow(root, "XR-005")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"XR-005 в in-progress", "| XR-005 | Задача в работе |", "файл задачи: docs/tasks/XR-005.md"} {
		if !strings.Contains(out, want) {
			t.Errorf("в show нет %q:\n%s", want, out)
		}
	}
	out, err = cmdShow(root, "XR-001")
	if err != nil || !strings.Contains(out, "файла задачи нет") {
		t.Fatalf("show без файла: %q, %v", out, err)
	}
	out, err = cmdShow(root, "XR-007")
	if err != nil || !strings.Contains(out, "XR-007 в архиве (закрыта 2026-06-12)") {
		t.Fatalf("show по архиву: %q, %v", out, err)
	}
	if _, err := cmdShow(root, "XR-404"); err == nil {
		t.Fatal("show несуществующей задачи должен падать")
	}
}

func TestSetValidation(t *testing.T) {
	root := setup(t)
	cases := []SetParams{
		{ID: "XR-404", Type: "bug"},
		{ID: "XR-005"},
		{ID: "XR-005", Type: "feature"},
		{ID: "XR-005", Rank: "1+2+3"},
		{ID: "XR-005", Type: "task"},
		{ID: "XR-005", Rank: "25+2+1+0+2"},
		{ID: "XR-005", Cost: "XXL"},
		{ID: "XR-005", Cost: "-"},
	}
	for _, p := range cases {
		if _, err := cmdSet(root, p); err == nil {
			t.Errorf("ожидал ошибку на %+v", p)
		}
	}
	board, _ := os.ReadFile(boardPath(root))
	if string(board) != fixtureBoard {
		t.Fatalf("доска изменилась после отбитых set:\n%s", board)
	}
}

func TestClose(t *testing.T) {
	root := setup(t)
	msg, err := cmdClose(root, CloseParams{ID: "XR-005", Commits: "deadbee", Date: "2026-07-08"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "tasks/archive/2026/XR-005.md") {
		t.Fatalf("сообщение без пути архива: %s", msg)
	}
	if _, err := os.Stat(filepath.Join(root, "docs", "tasks", "archive", "2026", "XR-005.md")); err != nil {
		t.Fatal("файл задачи не переехал в архив:", err)
	}
	if _, err := os.Stat(filepath.Join(root, "docs", "tasks", "XR-005.md")); !os.IsNotExist(err) {
		t.Fatal("файл задачи остался на старом месте")
	}
	board, _ := os.ReadFile(boardPath(root))
	rowLine := "| XR-005 | Задача в работе | task | P2 | 30 (25+2+1+0+2) | - | [tasks/XR-005.md](tasks/XR-005.md) |\n"
	if want := strings.Replace(fixtureBoard, rowLine, "", 1); string(board) != want {
		t.Fatalf("доска после close отличается не только строкой XR-005:\n%s", board)
	}
	arch, _ := os.ReadFile(archivePath(root))
	wantRow := "| XR-005 | Задача в работе | task | P2 | 2026-07-08 | [tasks/archive/2026/XR-005.md](tasks/archive/2026/XR-005.md), `deadbee` |\n"
	if !strings.HasSuffix(string(arch), wantRow) {
		t.Fatalf("хвост архива: %s", arch)
	}
}

func TestCloseWithoutFileKeepsLink(t *testing.T) {
	root := setup(t)
	if _, err := cmdClose(root, CloseParams{ID: "XR-001", Date: "2026-07-08"}); err != nil {
		t.Fatal(err)
	}
	arch, _ := os.ReadFile(archivePath(root))
	if !strings.HasSuffix(string(arch), "| XR-001 | Средняя | task/LLD | P2 | 2026-07-08 | (LLD позже) |\n") {
		t.Fatalf("хвост архива: %s", arch)
	}
}

func TestSort(t *testing.T) {
	root := setup(t)
	// Перемешиваем Backlog: хвост наверх, пару с равным R меняем местами.
	b, err := LoadBoard(boardPath(root))
	if err != nil {
		t.Fatal(err)
	}
	rows := b.Sects[SectBacklog].Rows
	lines := []string{b.Lines[rows[3].LineIdx], b.Lines[rows[2].LineIdx], b.Lines[rows[1].LineIdx], b.Lines[rows[0].LineIdx]}
	for i, r := range rows {
		b.Lines[r.LineIdx] = lines[i]
	}
	if err := b.Save(); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdSort(root, CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	got := strings.Join(backlogIDs(t, root), " ")
	if want := "XR-002 XR-001 XR-003 XR-004"; got != want {
		t.Fatalf("после sort: %s, ожидал %s", got, want)
	}
	msg, err := cmdSort(root, CommitOpts{})
	if err != nil || msg != "Backlog уже отсортирован" {
		t.Fatalf("повторный sort: %q, %v", msg, err)
	}
}

// Штатная миграция доски старого формата: sort сохраняет файл с колонкой
// «Цена», даже когда переставлять нечего.
func TestSortMigratesLegacyBoard(t *testing.T) {
	root := setup(t)
	if err := os.WriteFile(boardPath(root), []byte(fixtureBoardLegacy), 0o644); err != nil {
		t.Fatal(err)
	}
	msg, err := cmdSort(root, CommitOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "переведена в формат") {
		t.Fatalf("сообщение: %q", msg)
	}
	board, _ := os.ReadFile(boardPath(root))
	if string(board) != fixtureBoard {
		t.Fatalf("доска после миграции:\n%s", board)
	}
	msg, err = cmdSort(root, CommitOpts{})
	if err != nil || msg != "Backlog уже отсортирован" {
		t.Fatalf("sort после миграции: %q, %v", msg, err)
	}
}

func TestLintClean(t *testing.T) {
	root := setup(t)
	finds, err := cmdLint(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(finds) != 0 {
		t.Fatalf("на чистой доске находки: %v", finds)
	}
}

func TestLintFindings(t *testing.T) {
	root := setup(t)
	board := `# Тест (префикс XR)

## In progress

| ID | Задача | Тип | P | R | Цена | Ссылка |
|--------|--------|-----|---|---|------|--------|

## Check

| ID | Задача | Тип | P | R | Цена | Ссылка |
|--------|--------|-----|---|---|------|--------|

## Backlog

| ID | Задача | Тип | P | R | Цена | Ссылка |
|--------|--------|-----|---|---|------|--------|
| XR-010 | Не тот бакет | task | P1 | 9 (0+4+1+0+4) | - | x |
| XR-011 | Битая ссылка | task | P3 | 9 (0+4+1+0+4) | - | [tasks/XR-404.md](tasks/XR-404.md) |
| XR-012 | Стоит ниже старшего | task | P3 | 20 (0+10+5+0+5) | - | x |
| XR-007 | Дубль с архивом | bug | P2 | 30 (25+0+0+5+0) | - | x |

## Blocked

Нет.
`
	if err := os.WriteFile(boardPath(root), []byte(board), 0o644); err != nil {
		t.Fatal(err)
	}
	finds, err := cmdLint(root)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(finds, "\n")
	for _, want := range []string{"дубль ID XR-007", "P=P1, а по R=9", "битая ссылка", "не отсортирован"} {
		if !strings.Contains(joined, want) {
			t.Errorf("нет находки %q среди:\n%s", want, joined)
		}
	}
}

func TestLintFlagsLegacyBoard(t *testing.T) {
	root := setup(t)
	if err := os.WriteFile(boardPath(root), []byte(fixtureBoardLegacy), 0o644); err != nil {
		t.Fatal(err)
	}
	finds, err := cmdLint(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(finds) != 1 || !strings.Contains(finds[0], "без колонки «Цена»") {
		t.Fatalf("находки на доске старого формата: %v", finds)
	}
}

// Полный цикл: завёл, взял в работу, закрыл. Доска возвращается к исходным
// байтам, задача остаётся только в архиве.
func TestCycle(t *testing.T) {
	root := setup(t)
	if _, err := cmdAdd(root, AddParams{Title: "Временная", Type: "task", Rank: "0+1+1+0+1", Link: "x"}); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdMove(root, "XR-008", SectInProgress, "", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdClose(root, CloseParams{ID: "XR-008", Date: "2026-07-08"}); err != nil {
		t.Fatal(err)
	}
	board, _ := os.ReadFile(boardPath(root))
	if string(board) != fixtureBoard {
		t.Fatalf("доска после цикла не вернулась к исходной:\n%s", board)
	}
	arch, _ := os.ReadFile(archivePath(root))
	if !strings.HasSuffix(string(arch), "| XR-008 | Временная | task | P3 | 2026-07-08 | x |\n") {
		t.Fatalf("хвост архива: %s", arch)
	}
}
