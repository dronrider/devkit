package main

import (
	"os"
	"strings"
	"testing"
)

func TestSplitJoinTitle(t *testing.T) {
	cases := []struct {
		title    string
		base     string
		deps     []string
		blockSuf string
	}{
		{"Простой заголовок", "Простой заголовок", nil, ""},
		{"С зависимостью [после XR-001]", "С зависимостью", []string{"XR-001"}, ""},
		{"С двумя [после XR-001, XR-002]", "С двумя", []string{"XR-001", "XR-002"}, ""},
		{"С блоком [блок: ждём]", "С блоком", nil, " [блок: ждём]"},
		{"Оба [после XR-001] [блок: ждём]", "Оба", []string{"XR-001"}, " [блок: ждём]"},
	}
	for _, c := range cases {
		base, deps, blockSuf := splitTitle(c.title)
		if base != c.base || strings.Join(deps, ",") != strings.Join(c.deps, ",") || blockSuf != c.blockSuf {
			t.Fatalf("splitTitle(%q) = %q, %v, %q; ожидал %q, %v, %q",
				c.title, base, deps, blockSuf, c.base, c.deps, c.blockSuf)
		}
		if got := joinTitle(base, deps, blockSuf); got != c.title {
			t.Fatalf("joinTitle не восстановил заголовок: %q, ожидал %q", got, c.title)
		}
	}
}

func TestDepAddRmList(t *testing.T) {
	root := setup(t)
	msg, err := cmdDepAdd(root, DepParams{ID: "XR-002", DepID: "XR-001"})
	if err != nil {
		t.Fatal(err)
	}
	if msg != "XR-002: после XR-001" {
		t.Fatalf("сообщение: %q", msg)
	}
	if got := backlogTitle(t, root, "XR-002"); got != "Верхняя [после XR-001]" {
		t.Fatalf("заголовок XR-002: %q", got)
	}

	// Обратное направление: XR-001 держит XR-002.
	out, err := cmdDepList(root, "XR-001")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "XR-001 после: -") || !strings.Contains(out, "XR-001 держит: XR-002") {
		t.Fatalf("dep list XR-001: %q", out)
	}

	// dep list без ID видит обе стороны по всей доске.
	out, err = cmdDepList(root, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "XR-002 после: XR-001") || !strings.Contains(out, "XR-001 держит: XR-002") {
		t.Fatalf("dep list всей доски: %q", out)
	}

	if _, err := cmdDepAdd(root, DepParams{ID: "XR-001", DepID: "XR-002"}); err == nil {
		t.Fatal("цикл должен падать")
	}
	if _, err := cmdDepAdd(root, DepParams{ID: "XR-003", DepID: "XR-099"}); err == nil {
		t.Fatal("несуществующая зависимость должна падать")
	}
	if _, err := cmdDepAdd(root, DepParams{ID: "XR-003", DepID: "XR-003"}); err == nil {
		t.Fatal("зависимость от себя должна падать")
	}
	if _, err := cmdDepAdd(root, DepParams{ID: "XR-002", DepID: "XR-001"}); err == nil {
		t.Fatal("повторное dep add должно падать")
	}

	msg, err = cmdDepRm(root, DepParams{ID: "XR-002", DepID: "XR-001"})
	if err != nil {
		t.Fatal(err)
	}
	if msg != "XR-002: зависимость от XR-001 снята" {
		t.Fatalf("сообщение rm: %q", msg)
	}
	if got := backlogTitle(t, root, "XR-002"); got != "Верхняя" {
		t.Fatalf("заголовок XR-002 после rm: %q", got)
	}
	if _, err := cmdDepRm(root, DepParams{ID: "XR-002", DepID: "XR-001"}); err == nil {
		t.Fatal("повторный rm должен падать")
	}
	if _, err := cmdDepList(root, "XR-404"); err == nil {
		t.Fatal("dep list несуществующей задачи должен падать")
	}
}

func backlogTitle(t *testing.T, root, id string) string {
	t.Helper()
	b, err := LoadBoard(boardPath(root))
	if err != nil {
		t.Fatal(err)
	}
	r := b.find(id)
	if r == nil {
		t.Fatalf("%s нет на доске", id)
	}
	return r.Title
}

// TestMoveBlockedByDep: незакрытая зависимость держит задачу вне in-progress,
// закрытие зависимости страховку снимает.
func TestMoveBlockedByDep(t *testing.T) {
	root := setup(t)
	if _, err := cmdDepAdd(root, DepParams{ID: "XR-004", DepID: "XR-001"}); err != nil {
		t.Fatal(err)
	}
	_, err := cmdMove(root, "XR-004", SectInProgress, "", CommitOpts{})
	if err == nil {
		t.Fatal("move в in-progress с незакрытой зависимостью должен падать")
	}
	if !strings.Contains(err.Error(), "XR-001") {
		t.Fatalf("в ошибке нет ID зависимости: %v", err)
	}

	if _, err := cmdClose(root, CloseParams{ID: "XR-001", Date: "2026-07-08"}); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdMove(root, "XR-004", SectInProgress, "", CommitOpts{}); err != nil {
		t.Fatalf("после закрытия зависимости move должен пройти: %v", err)
	}
}

// TestCloseStripsDeps: close снимает «[после <ID>]» со всех строк доски и не
// переносит собственный маркер задачи в архив.
func TestCloseStripsDeps(t *testing.T) {
	root := setup(t)
	if _, err := cmdDepAdd(root, DepParams{ID: "XR-002", DepID: "XR-005"}); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdDepAdd(root, DepParams{ID: "XR-003", DepID: "XR-005"}); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdDepAdd(root, DepParams{ID: "XR-005", DepID: "XR-001"}); err != nil {
		t.Fatal(err)
	}

	msg, err := cmdClose(root, CloseParams{ID: "XR-005", Commits: "deadbee", Date: "2026-07-08"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "маркер «после» снят у: XR-002, XR-003") {
		t.Fatalf("сообщение close: %q", msg)
	}
	if got := backlogTitle(t, root, "XR-002"); got != "Верхняя" {
		t.Fatalf("XR-002 после close XR-005: %q", got)
	}
	if got := backlogTitle(t, root, "XR-003"); got != "Та же R, больший ID" {
		t.Fatalf("XR-003 после close XR-005: %q", got)
	}

	arch, err := LoadArchive(archivePath(root))
	if err != nil {
		t.Fatal(err)
	}
	var archTitle string
	for _, r := range arch.Rows {
		if r.ID == "XR-005" {
			archTitle = r.Cells[1]
		}
	}
	if strings.Contains(archTitle, "[после") {
		t.Fatalf("маркер попал в архив: %q", archTitle)
	}
}

// TestShowPrintsDeps: show печатает зависимости в обе стороны.
func TestShowPrintsDeps(t *testing.T) {
	root := setup(t)
	if _, err := cmdDepAdd(root, DepParams{ID: "XR-002", DepID: "XR-001"}); err != nil {
		t.Fatal(err)
	}
	out, err := cmdShow(root, "XR-002")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "после: XR-001") || !strings.Contains(out, "держит: -") {
		t.Fatalf("show XR-002: %q", out)
	}
	out, err = cmdShow(root, "XR-001")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "после: -") || !strings.Contains(out, "держит: XR-002") {
		t.Fatalf("show XR-001: %q", out)
	}
}

// TestSetTitleKeepsDepSuffix: set --title сохраняет маркер «после» и порядок
// суффиксов (после -> блок), как уже устроено для «[блок: ...]».
func TestSetTitleKeepsDepSuffix(t *testing.T) {
	root := setup(t)
	if _, err := cmdDepAdd(root, DepParams{ID: "XR-004", DepID: "XR-001"}); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdMove(root, "XR-004", SectBlocked, "ждём железо", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdSet(root, SetParams{ID: "XR-004", Title: "Хвост, уточнённый"}); err != nil {
		t.Fatal(err)
	}
	if got := backlogTitle(t, root, "XR-004"); got != "Хвост, уточнённый [после XR-001] [блок: ждём железо]" {
		t.Fatalf("заголовок после set: %q", got)
	}
}

func TestLintDeps(t *testing.T) {
	root := setup(t)
	board := `# Тест (префикс XR)

## In progress

| ID | Задача | Тип | P | R | Цена | Ссылка |
|--------|--------|-----|---|---|------|--------|
| XR-020 | В работе [после XR-021] | task | P3 | 9 (0+4+1+0+4) | - | x |
| XR-028 | В работе на закрытую [после XR-007] | task | P3 | 9 (0+4+1+0+4) | - | x |

## Check

| ID | Задача | Тип | P | R | Цена | Ссылка |
|--------|--------|-----|---|---|------|--------|

## Backlog

| ID | Задача | Тип | P | R | Цена | Ссылка |
|--------|--------|-----|---|---|------|--------|
| XR-021 | Хвост | task | P3 | 9 (0+4+1+0+4) | - | x |
| XR-022 | Сам на себя [после XR-022] | task | P3 | 9 (0+4+1+0+4) | - | x |
| XR-023 | Дубль [после XR-021, XR-021] | task | P3 | 9 (0+4+1+0+4) | - | x |
| XR-024 | Несуществующая [после XR-999] | task | P3 | 9 (0+4+1+0+4) | - | x |
| XR-025 | Цикл A [после XR-026] | task | P3 | 9 (0+4+1+0+4) | - | x |
| XR-026 | Цикл B [после XR-025] | task | P3 | 9 (0+4+1+0+4) | - | x |
| XR-027 | На архивную [после XR-007] | task | P3 | 9 (0+4+1+0+4) | - | x |

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
	for _, want := range []string{
		"маркер «после» ссылается сам на себя (XR-022)",
		"маркер «после» дублирует XR-021",
		"маркер «после» ссылается на несуществующую задачу XR-999",
		"цикл зависимостей",
		"XR-020 в In progress с незакрытой зависимостью XR-021",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("нет находки %q среди:\n%s", want, joined)
		}
	}
	if strings.Contains(joined, "незакрытой зависимостью XR-007") {
		t.Errorf("зависимость на закрытую (архивную) задачу не должна считаться незакрытой:\n%s", joined)
	}
}
