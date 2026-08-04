package main

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
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
	if _, err := cmdMove(root, "XR-004", SectInProgress, "", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
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
	if _, err := cmdMove(root, "XR-004", SectInProgress, "", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
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

// TestMoveToBlockedRejectsBracketInReason: регрессия. checkCell пропускал в
// --reason квадратную скобку («ждём [DK-5]»), а суффикс «[блок: ...]» тогда
// собирался с лишней «[» внутри и переставал распознаваться как единый
// суффикс: на выходе из Blocked он не снимался, set --title приклеивал его
// к заголовку как обычный текст, lint молчал.
func TestMoveToBlockedRejectsBracketInReason(t *testing.T) {
	root := setup(t)
	if _, err := cmdMove(root, "XR-004", SectBlocked, "ждём [DK-5]", CommitOpts{}); err == nil {
		t.Fatal("причина со скобкой должна падать")
	}
	board, _ := os.ReadFile(boardPath(root))
	if string(board) != fixtureBoard {
		t.Fatalf("доска изменилась после отбитого move:\n%s", board)
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
	if _, err := cmdMove(root, "XR-004", SectInProgress, "", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
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
	// Второй вызов подряд идемпотентен: и файл, и ссылка уже на месте, команда
	// это подтверждает и выходит с нулём, а не падает.
	msg2, err := cmdFile(root, "XR-001", CommitOpts{})
	if err != nil {
		t.Fatalf("повторный file не должен падать: %v", err)
	}
	if !strings.Contains(msg2, "уже есть и файл, и ссылка") {
		t.Fatalf("сообщение повторного вызова: %q", msg2)
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

// TestLintTaskFiles: задача в работе без файла и задача в Check без файла и
// без ссылки на сценарий это находки; файл и ссылка их снимают.
func TestLintTaskFiles(t *testing.T) {
	root := setup(t)
	if err := os.Remove(filepath.Join(root, "docs", "tasks", "XR-005.md")); err != nil {
		t.Fatal(err)
	}
	finds, err := cmdLint(root)
	if err != nil {
		t.Fatal(err)
	}
	if joined := strings.Join(finds, "\n"); !strings.Contains(joined, "XR-005 в работе без файла задачи") {
		t.Fatalf("нет находки про файл задачи в работе: %v", finds)
	}

	if err := os.WriteFile(filepath.Join(root, "docs", "tasks", "XR-005.md"), []byte("# XR-005\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdAdd(root, AddParams{Title: "Готова, некуда смотреть", Type: "task", Rank: "0+3+0+0+0", Status: "check"}); err != nil {
		t.Fatal(err)
	}
	finds, err = cmdLint(root)
	if err != nil {
		t.Fatal(err)
	}
	if joined := strings.Join(finds, "\n"); !strings.Contains(joined, "XR-008 в Check без файла задачи") {
		t.Fatalf("нет находки про сценарий проверки в Check: %v", finds)
	}

	// Ссылка на сценарий (существующий файл) снимает находку.
	if _, err := cmdSet(root, SetParams{ID: "XR-008", Link: "tasks/XR-002.md"}); err != nil {
		t.Fatal(err)
	}
	finds, err = cmdLint(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(finds) != 0 {
		t.Fatalf("после ссылки на сценарий находки остались: %v", finds)
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

// TestRewriteLinksInFile проверяет переписывание относительных ссылок в файле
// при переносе в архив. Ссылка, разрешавшаяся от docs/tasks/, должна
// разрешаться из docs/tasks/archive/2026/ в тот же целевой файл.
func TestRewriteLinksInFile(t *testing.T) {
	root := setup(t)
	oldBaseDir := filepath.Join(root, "docs", "tasks")
	newBaseDir := filepath.Join(root, "docs", "tasks", "archive", "2026")
	os.MkdirAll(newBaseDir, 0o755)

	// Создать целевой файл для ссылок (например, LLD)
	os.MkdirAll(filepath.Join(root, "docs", "lld"), 0o755)
	lldPath := filepath.Join(root, "docs", "lld", "XR-001.md")
	os.WriteFile(lldPath, []byte("# XR-001 LLD\n"), 0o644)

	// Создать файл задачи со ссылками
	taskContent := `# XR-005

Описание со ссылками:
- [LLD](../lld/XR-001.md)
- [Якорь](../lld/XR-001.md#section)
- [Внешняя](https://example.com)
- [Relative task](XR-002.md)
`
	taskPath := filepath.Join(newBaseDir, "XR-005.md")
	os.WriteFile(taskPath, []byte(taskContent), 0o644)

	// Переписать ссылки
	if err := rewriteLinksInFile(taskPath, oldBaseDir, newBaseDir); err != nil {
		t.Fatal(err)
	}

	// Проверить содержимое
	result, _ := os.ReadFile(taskPath)
	resultStr := string(result)

	// Ссылка на LLD должна стать ../../../lld/XR-001.md
	if !strings.Contains(resultStr, "[LLD](../../../lld/XR-001.md)") {
		t.Errorf("LLD ссылка не переписана правильно:\n%s", resultStr)
	}

	// Якорь должен сохраниться
	if !strings.Contains(resultStr, "[Якорь](../../../lld/XR-001.md#section)") {
		t.Errorf("Якорь не сохранился:\n%s", resultStr)
	}

	// Внешняя ссылка не должна измениться
	if !strings.Contains(resultStr, "[Внешняя](https://example.com)") {
		t.Errorf("Внешняя ссылка изменилась:\n%s", resultStr)
	}

	// Ссылка на другую задачу должна стать ../../XR-002.md
	if !strings.Contains(resultStr, "[Relative task](../../XR-002.md)") {
		t.Errorf("Ссылка на задачу не переписана правильно:\n%s", resultStr)
	}
}

// TestFindAndRewriteReferences проверяет переписывание ссылок на переносимый
// файл в других файлах репозитория.
func TestFindAndRewriteReferences(t *testing.T) {
	root := setup(t)
	os.MkdirAll(filepath.Join(root, "docs", "tasks", "archive", "2026"), 0o755)

	// Создать файл задачи в архиве
	oldPath := filepath.Join(root, "docs", "tasks", "XR-008.md")
	newPath := filepath.Join(root, "docs", "tasks", "archive", "2026", "XR-008.md")
	os.MkdirAll(filepath.Dir(newPath), 0o755)
	os.WriteFile(newPath, []byte("# XR-008 archived\n"), 0o644)

	// Создать файл со ссылкой на старый путь
	refPath := filepath.Join(root, "docs", "REFERENCE.md")
	refContent := `# Reference

- [Task](tasks/XR-008.md)
- [Another](tasks/XR-002.md)
`
	os.WriteFile(refPath, []byte(refContent), 0o644)

	// Переписать ссылки: ищем ссылки, разрешающиеся в oldPath, и заменяем на newPath
	changed, err := findAndRewriteReferencesToFile(root, oldPath, newPath)
	if err != nil {
		t.Fatal(err)
	}

	// Проверить, что файл был изменён
	if len(changed) != 1 || !strings.Contains(changed[0], "REFERENCE.md") {
		t.Errorf("ожидалась находка REFERENCE.md, получено %v", changed)
	}

	// Проверить содержимое файла
	result, _ := os.ReadFile(refPath)
	resultStr := string(result)

	// Ссылка должна измениться на tasks/archive/2026/XR-008.md
	if !strings.Contains(resultStr, "[Task](tasks/archive/2026/XR-008.md)") {
		t.Errorf("Ссылка не переписана правильно:\n%s", resultStr)
	}

	// Ссылка на XR-002 не должна измениться
	if !strings.Contains(resultStr, "[Another](tasks/XR-002.md)") {
		t.Errorf("Ссылка на XR-002 изменилась:\n%s", resultStr)
	}
}

// TestCloseWithLinks проверяет, что при закрытии задачи переписываются ссылки
// как в самом файле задачи, так и в других файлах, ссылающихся на неё.
func TestCloseWithLinks(t *testing.T) {
	root := setup(t)

	// Добавить новую задачу в работу
	if _, err := cmdAdd(root, AddParams{ID: "XR-099", Title: "С ссылками", Type: "task", Rank: "0+1+1+0+1", Link: "x"}); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdMove(root, "XR-099", SectInProgress, "", CommitOpts{}); err != nil {
		t.Fatal(err)
	}

	// Создать LLD-файл
	os.MkdirAll(filepath.Join(root, "docs", "lld"), 0o755)
	lldPath := filepath.Join(root, "docs", "lld", "XR-099.md")
	os.WriteFile(lldPath, []byte("# XR-099 LLD\n"), 0o644)

	// Создать файл задачи со ссылкой на LLD
	taskContent := `# XR-099

Описание:
- [LLD](../lld/XR-099.md)
`
	taskPath := filepath.Join(root, "docs", "tasks", "XR-099.md")
	os.WriteFile(taskPath, []byte(taskContent), 0o644)

	// Создать файл со ссылкой на задачу
	refPath := filepath.Join(root, "docs", "REFERENCE.md")
	os.WriteFile(refPath, []byte("[Task](tasks/XR-099.md)\n"), 0o644)

	// Закрыть
	if _, err := cmdClose(root, CloseParams{ID: "XR-099", Date: "2026-07-08"}); err != nil {
		t.Fatal(err)
	}

	// Проверить, что файл задачи в архиве
	archivedPath := filepath.Join(root, "docs", "tasks", "archive", "2026", "XR-099.md")
	archivedContent, _ := os.ReadFile(archivedPath)
	archivedStr := string(archivedContent)

	// Ссылка в архивированном файле должна быть переписана
	if !strings.Contains(archivedStr, "[LLD](../../../lld/XR-099.md)") {
		t.Errorf("Ссылка в архивированном файле не переписана:\n%s", archivedStr)
	}

	// Проверить, что ссылка в REFERENCE.md переписана
	refContent, _ := os.ReadFile(refPath)
	refStr := string(refContent)
	if !strings.Contains(refStr, "[Task](tasks/archive/2026/XR-099.md)") {
		t.Errorf("Ссылка в REFERENCE.md не переписана:\n%s", refStr)
	}
}

// TestCloseRewritesIncomingLinks проверяет, что переписанные входящие ссылки
// уезжают в коммит close, а не остаются правкой в рабочем дереве.
func TestCloseRewritesIncomingLinks(t *testing.T) {
	root := setup(t)

	if _, err := cmdAdd(root, AddParams{ID: "XR-077", Title: "С ссылками", Type: "task", Rank: "0+1+1+0+1", Link: "x"}); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdMove(root, "XR-077", SectInProgress, "", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(
		filepath.Join(root, "docs", "tasks", "XR-077.md"),
		[]byte("# XR-077\nTask\n"),
		0o644,
	)
	os.WriteFile(
		filepath.Join(root, "docs", "INCOMING.md"),
		[]byte("[Task](tasks/XR-077.md)\n"),
		0o644,
	)
	gitSetup(t, root)

	p := CloseParams{ID: "XR-077", Date: "2026-07-31", Commit: CommitOpts{Msg: "docs(tasks): XR-077 закрыта"}}
	if _, err := cmdClose(root, p); err != nil {
		t.Fatal(err)
	}

	if committed := gitOut(t, root, "show", "HEAD:docs/INCOMING.md"); committed != "[Task](tasks/archive/2026/XR-077.md)" {
		t.Errorf("в коммите не переписанная ссылка: %q", committed)
	}
	if st := gitOut(t, root, "status", "--porcelain"); st != "" {
		t.Errorf("после close осталось незакоммиченное:\n%s", st)
	}
}

// rewritten прогоняет содержимое файла задачи через перенос в архив и
// возвращает то, что легло на диск.
func rewritten(t *testing.T, content string) string {
	t.Helper()
	root := setup(t)
	oldBaseDir := filepath.Join(root, "docs", "tasks")
	newBaseDir := filepath.Join(oldBaseDir, "archive", "2026")
	if err := os.MkdirAll(newBaseDir, 0o755); err != nil {
		t.Fatal(err)
	}
	taskPath := filepath.Join(newBaseDir, "XR-001.md")
	if err := os.WriteFile(taskPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := rewriteLinksInFile(taskPath, oldBaseDir, newBaseDir); err != nil {
		t.Fatal(err)
	}
	out, err := os.ReadFile(taskPath)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

// TestRewriteLinksKeepsFileTail следит за концом файла: перевод строки не
// удваивается, а отсутствие перевода не превращается в перевод.
func TestRewriteLinksKeepsFileTail(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"с переводом строки", "[LLD](../lld/X.md)\n", "[LLD](../../../lld/X.md)\n"},
		{"без перевода строки", "[LLD](../lld/X.md)", "[LLD](../../../lld/X.md)"},
		{"без ссылок", "# Заголовок\n\nПроза.\n", "# Заголовок\n\nПроза.\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := rewritten(t, c.in); got != c.want {
				t.Errorf("получено %q, ждал %q", got, c.want)
			}
		})
	}
}

// TestRewriteLinksFences проверяет разметку заборов: вложенный короткий забор
// не закрывает длинный внешний, тильды работают наравне с кавычками, отступ
// забора не больше трёх пробелов, незакрытый забор тянется до конца файла.
func TestRewriteLinksFences(t *testing.T) {
	link := "[LLD](../lld/X.md)"
	moved := "[LLD](../../../lld/X.md)"
	cases := []struct{ name, in, want string }{
		{
			"вложенный забор не закрывает внешний",
			"````\n```\n" + link + "\n```\n````\n" + link + "\n",
			"````\n```\n" + link + "\n```\n````\n" + moved + "\n",
		},
		{
			"забор из тильд",
			"~~~\n" + link + "\n~~~\n" + link + "\n",
			"~~~\n" + link + "\n~~~\n" + moved + "\n",
		},
		{
			"забор с отступом в три пробела",
			"   ```\n" + link + "\n   ```\n" + link + "\n",
			"   ```\n" + link + "\n   ```\n" + moved + "\n",
		},
		{
			"четыре пробела это уже не забор",
			"    ```\n" + link + "\n",
			"    ```\n" + moved + "\n",
		},
		{
			"инлайн-код это не ссылка",
			"пример: `" + link + "` и " + link + "\n",
			"пример: `" + link + "` и " + moved + "\n",
		},
		{
			"незакрытый забор до конца файла",
			"```\n" + link + "\n",
			"```\n" + link + "\n",
		},
		{
			"забор из тильд не закрывается кавычками",
			"~~~\n```\n" + link + "\n",
			"~~~\n```\n" + link + "\n",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := rewritten(t, c.in); got != c.want {
				t.Errorf("получено %q, ждал %q", got, c.want)
			}
		})
	}
}

// TestRewriteLinksSkipsCodeBlocks проверяет, что ссылки внутри блоков кода
// не переписываются. Одна и та же ссылка стоит и в прозе, и внутри кода;
// только первая должна измениться.
func TestRewriteLinksSkipsCodeBlocks(t *testing.T) {
	root := setup(t)
	oldBaseDir := filepath.Join(root, "docs", "tasks")
	newBaseDir := filepath.Join(root, "docs", "tasks", "archive", "2026")
	os.MkdirAll(newBaseDir, 0o755)

	// Создать целевой файл
	os.MkdirAll(filepath.Join(root, "docs", "lld"), 0o755)
	os.WriteFile(filepath.Join(root, "docs", "lld", "XR-001.md"), []byte("# LLD\n"), 0o644)

	// Файл со ссылкой в прозе и в блоке кода (```)
	content := `# Task

Описание: [LLD](../lld/XR-001.md)

` + "```" + `regcheck -- sh -c 'cd taskctl && go test ./...'
# Ссылка на LLD: [LLD](../lld/XR-001.md)
` + "```" + `

[Ещё ссылка](../lld/XR-001.md)
`
	taskPath := filepath.Join(newBaseDir, "XR-001.md")
	os.WriteFile(taskPath, []byte(content), 0o644)

	// Переписать ссылки
	if err := rewriteLinksInFile(taskPath, oldBaseDir, newBaseDir); err != nil {
		t.Fatal(err)
	}

	result, _ := os.ReadFile(taskPath)
	resultStr := string(result)

	// Ссылка в прозе должна быть переписана
	if !strings.Contains(resultStr, "Описание: [LLD](../../../lld/XR-001.md)") {
		t.Errorf("Ссылка в прозе не переписана:\n%s", resultStr)
	}

	// Ссылка в блоке кода НЕ должна быть переписана
	if !strings.Contains(resultStr, "# Ссылка на LLD: [LLD](../lld/XR-001.md)") {
		t.Errorf("Ссылка в блоке кода была переписана (не должна быть):\n%s", resultStr)
	}

	// Ссылка после блока кода должна быть переписана
	if !strings.Contains(resultStr, "[Ещё ссылка](../../../lld/XR-001.md)") {
		t.Errorf("Ссылка после блока кода не переписана:\n%s", resultStr)
	}
}

// TestMoveBlockedOnlyFromWork: заблокированной бывает только начатая задача
// (RULES.board.md, «Трекинг задач» п. 4). Строку из Backlog разблокировать
// некому, а Blocked у неё значил бы просто «не начали», как весь Backlog.
func TestMoveBlockedOnlyFromWork(t *testing.T) {
	root := setup(t)
	_, err := cmdMove(root, "XR-004", SectBlocked, "ждём железо", CommitOpts{})
	if err == nil {
		t.Fatal("блокировка задачи из Backlog должна отбиваться")
	}
	if !strings.Contains(err.Error(), "ещё не в работе") || !strings.Contains(err.Error(), "dep add") {
		t.Fatalf("отказ должен называть причину и путь для своей же зависимости: %v", err)
	}
	b, err := LoadBoard(boardPath(root))
	if err != nil {
		t.Fatal(err)
	}
	if r := b.find("XR-004"); r.Sect != SectBacklog {
		t.Fatalf("строка уехала из Backlog: %s", r.Sect)
	}
	if _, err := cmdMove(root, "XR-004", SectInProgress, "", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdMove(root, "XR-004", SectBlocked, "ждём железо", CommitOpts{}); err != nil {
		t.Fatalf("начатую задачу блокировать можно: %v", err)
	}
}

// TestAddBlockedRejected: новую строку в Blocked не заводят, иначе add обходит
// инвариант «заблокированной бывает только начатая задача» тем же путём,
// каким его обходил move из Backlog.
func TestAddBlockedRejected(t *testing.T) {
	root := setup(t)
	_, err := cmdAdd(root, AddParams{
		Title: "Сразу на блокере", Type: "task", Rank: "0+1+1+0+1", Link: "x",
		Status: "blocked",
	})
	if err == nil {
		t.Fatal("add --status blocked должен отбиваться")
	}
	if !strings.Contains(err.Error(), "не заводят") || !strings.Contains(err.Error(), "dep add") {
		t.Fatalf("отказ должен называть причину и путь для своей же зависимости: %v", err)
	}
	board, _ := os.ReadFile(boardPath(root))
	if string(board) != fixtureBoard {
		t.Fatalf("доска изменилась после отбитого add:\n%s", board)
	}
}

// Разрешение на пуш едет хуку pre-push только с самим пушем: обычные команды
// git остаются на наследованном окружении, иначе рубеж пропускал бы всё, что
// taskctl запускает попутно.
func TestPushEnv(t *testing.T) {
	if env := pushEnv([]string{"commit", "-m", "x"}); env != nil {
		t.Fatalf("окружение подменено не на пуше: %v", env)
	}
	if env := pushEnv(nil); env != nil {
		t.Fatalf("окружение подменено на пустых аргументах: %v", env)
	}
	env := pushEnv([]string{"push"})
	if !slices.Contains(env, "DEVKIT_PUSH_OK=1") {
		t.Fatalf("пуш без разрешения для pre-push: %v", env)
	}
	if !slices.Contains(env, "PATH="+os.Getenv("PATH")) {
		t.Fatalf("родительское окружение потерялось: %v", env)
	}
}
