package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// rankBoardText собирает доску из строк Backlog: тестам поправок нужна только
// эта секция, остальные пустые.
func rankBoardText(backlog ...string) string {
	head := "| ID | Задача | Тип | P | R | Цена | Ссылка |\n|--------|--------|-----|---|---|------|--------|\n"
	return "# Тест: доска (префикс XR)\n\n## In progress\n\n" + head +
		"\n## Check (готово, ждёт проверки пользователем)\n\n" + head +
		"\n## Backlog\n\n" + head + strings.Join(backlog, "\n") + "\n\n## Blocked\n\nНет.\n"
}

// parseRankBoard разбирает доску в памяти: computeRanks зовёт сам разбор, и
// поправки видны сразу в полях строк.
func parseRankBoard(t *testing.T, text string) *Board {
	t.Helper()
	b, err := parseLines("board", strings.Split(text, "\n"))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// setupRank раскладывает доску и файлы задач во временном корне: цели читают
// состав из docs/tasks, а изменяющие команды отказывают строке без файла.
func setupRank(t *testing.T, text string, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	tasks := filepath.Join(root, "docs", "tasks")
	if err := os.MkdirAll(tasks, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(boardPath(root), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(archivePath(root), []byte(fixtureArchive), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(tasks, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// TestCostBonusPerCost: бонус за дешевизну из RANKING.md по каждой цене.
// Дешёвая работа поднимается над равной по пользе дорогой, и видно это шестым
// местом в скобке, а не правкой слагаемых.
func TestCostBonusPerCost(t *testing.T) {
	cases := []struct {
		cost string
		cell string
		p    string
	}{
		{"S", "50 (25+10+3+5+5, S+2)", "P1"},
		{"M", "49 (25+10+3+5+5, M+1)", "P2"},
		{"L", "48 (25+10+3+5+5)", "P2"},
		{"XL", "48 (25+10+3+5+5)", "P2"},
		{"-", "48 (25+10+3+5+5)", "P2"},
	}
	for _, c := range cases {
		line := fmt.Sprintf("| XR-001 | Ценой %s | task | P2 | 48 (25+10+3+5+5) | %s | (LLD позже) |", c.cost, c.cost)
		b := parseRankBoard(t, rankBoardText(line))
		r := b.Rows[0]
		if r.ROwn != 48 {
			t.Fatalf("цена %s: собственная сумма %d, ожидал 48", c.cost, r.ROwn)
		}
		if got := rankCell(r); got != c.cell {
			t.Fatalf("цена %s: ячейка %q, ожидал %q", c.cost, got, c.cell)
		}
		if got := bucket(r.RTotal); got != c.p {
			t.Fatalf("цена %s: бакет %s, ожидал %s", c.cost, got, c.p)
		}
	}
}

// TestPullChainOfThree: цепочка A держит B, B держит C с собственными рангами
// 35, 60 и 85. Ранг верхней задачи доезжает до самого низа цепочки, и в
// приписке стоит она сама, а не соседнее звено.
func TestPullChainOfThree(t *testing.T) {
	b := parseRankBoard(t, rankBoardText(
		"| XR-003 | Верх цепочки [после XR-002] | task | P0 | 85 (75+6+1+0+3) | - | (LLD позже) |",
		"| XR-002 | Середина [после XR-001] | task | P1 | 60 (50+6+1+0+3) | - | (LLD позже) |",
		"| XR-001 | Предпосылка | task | P2 | 35 (25+6+1+0+3) | - | (LLD позже) |",
	))
	want := map[string]string{
		"XR-001": "85 (25+6+1+0+3, от XR-003)",
		"XR-002": "85 (50+6+1+0+3, от XR-003)",
		"XR-003": "85 (75+6+1+0+3)",
	}
	for _, r := range b.Rows {
		if got := rankCell(r); got != want[r.ID] {
			t.Fatalf("%s: ячейка %q, ожидал %q", r.ID, got, want[r.ID])
		}
		if r.RTotal != 85 {
			t.Fatalf("%s: R_eff %d, ожидал 85", r.ID, r.RTotal)
		}
	}
}

// TestGoalPullsItsTasks: задача из раздела «Задачи цели» стоит не ниже самой
// цели, а закрытая цель (её строки на доске уже нет) не тянет никого.
func TestGoalPullsItsTasks(t *testing.T) {
	goal := "| XR-100 | Цель: пример | task | P1 | 60 (50+6+1+0+3) | - | [tasks/XR-100.md](tasks/XR-100.md) |"
	taskRow := "| XR-101 | Задача цели | task | P3 | 20 (0+10+5+0+5) | - | (LLD позже) |"
	goalFile := "# XR-100: Цель: пример\n\n## Задачи цели\n\n- XR-101 первая\n"

	root := setupRank(t, rankBoardText(goal, taskRow), map[string]string{"XR-100.md": goalFile})
	b, err := LoadBoard(boardPath(root))
	if err != nil {
		t.Fatal(err)
	}
	if got := rankCell(b.find("XR-101")); got != "60 (0+10+5+0+5, от XR-100)" {
		t.Fatalf("задача цели: ячейка %q", got)
	}

	// Цель закрыта: строки на доске нет, ребро наследования пропало вместе с
	// ней, и задача возвращается к своему рангу.
	closed := setupRank(t, rankBoardText(taskRow), map[string]string{"XR-100.md": goalFile})
	b, err = LoadBoard(boardPath(closed))
	if err != nil {
		t.Fatal(err)
	}
	if got := rankCell(b.find("XR-101")); got != "20 (0+10+5+0+5)" {
		t.Fatalf("после закрытия цели: ячейка %q", got)
	}
}

// TestCloseDropsInheritance: пока зависимая задача жива, предпосылка стоит на
// её ранге; закрытие зависимой отпускает предпосылку вниз, и переезд виден
// строкой в выводе close.
func TestCloseDropsInheritance(t *testing.T) {
	root := setup(t)
	if _, err := cmdDepAdd(root, DepParams{ID: "XR-002", DepID: "XR-001"}); err != nil {
		t.Fatal(err)
	}
	if got := backlogCell(t, root, "XR-001"); got != "55 (25+2+1+0+2, от XR-002)" {
		t.Fatalf("до закрытия: ячейка XR-001 %q", got)
	}
	msg, err := cmdClose(root, CloseParams{ID: "XR-002", Date: "2026-07-08"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "XR-001: 55 -> 30") {
		t.Fatalf("close без переезда предпосылки: %s", msg)
	}
	if got := backlogCell(t, root, "XR-001"); got != "30 (25+2+1+0+2)" {
		t.Fatalf("после закрытия: ячейка XR-001 %q", got)
	}
}

// backlogCell достаёт ячейку R строки прямо из файла доски.
func backlogCell(t *testing.T, root, id string) string {
	t.Helper()
	b, err := LoadBoard(boardPath(root))
	if err != nil {
		t.Fatal(err)
	}
	r := b.find(id)
	if r == nil {
		t.Fatalf("строки %s на доске нет", id)
	}
	return r.RCell
}

// TestEqualRankPutsHolderFirst: у предпосылки и зависимой ранг сравнялся
// наследованием, и порядок решает не номер, а ребро. Предпосылку делают
// раньше, поэтому она стоит выше, хотя номер у неё больше.
func TestEqualRankPutsHolderFirst(t *testing.T) {
	root := setupRank(t, rankBoardText(
		"| XR-001 | Зависимая [после XR-009] | task | P1 | 55 (50+2+1+0+2) | - | (LLD позже) |",
		"| XR-009 | Предпосылка | task | P2 | 30 (25+2+1+0+2) | - | (LLD позже) |",
	), nil)
	b, err := LoadBoard(boardPath(root))
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, r := range backlogOrder(root, b) {
		ids = append(ids, r.ID)
	}
	if got := strings.Join(ids, " "); got != "XR-009 XR-001" {
		t.Fatalf("порядок Backlog: %q, ожидал «XR-009 XR-001»", got)
	}
}

// TestLintRejectsHandwrittenTail: хвост скобки производный, и дописанный
// руками расходится с пересчётом так же молча, как разошёлся бы бакет P.
// Незнакомое имя поправки lint называет отдельно: такого имени нет в таблице,
// и пересчёт его не напишет никогда.
func TestLintRejectsHandwrittenTail(t *testing.T) {
	root := setup(t)
	spoil(t, root, "| XR-004 | Хвост | task | P3 | 9 (0+4+1+0+4) | - | (LLD позже) |",
		"| XR-004 | Хвост | task | P3 | 9 (0+4+1+0+4, Ы+3) | - | (LLD позже) |")
	finds, err := cmdLint(root)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(finds, "\n")
	if !strings.Contains(joined, "поправка «Ы» не из таблицы") {
		t.Fatalf("незнакомая поправка не найдена: %v", finds)
	}
	if !strings.Contains(joined, "не сходится с пересчётом") {
		t.Fatalf("рукописный хвост не найден: %v", finds)
	}
}

// spoil подменяет строку в файле доски: так выглядит правка руками мимо
// утилиты.
func spoil(t *testing.T, root, old, repl string) {
	t.Helper()
	p := boardPath(root)
	body, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	text := strings.Replace(string(body), old, repl, 1)
	if text == string(body) {
		t.Fatalf("строка для подмены не найдена: %s", old)
	}
	if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestParseAdjTailGrammar: грамматику хвоста держит парсер, а список имён
// таблица. Незнакомое имя разбирается и доезжает до lint, сломанная запись
// это ошибка разбора строки.
func TestParseAdjTailGrammar(t *testing.T) {
	adjs, err := parseAdjTail(", S+2, штраф-3, от XR-473")
	if err != nil {
		t.Fatal(err)
	}
	if len(adjs) != 3 {
		t.Fatalf("разобрано поправок: %d", len(adjs))
	}
	if adjs[0].Name != "S" || adjs[0].Delta != 2 || adjs[0].kind() != adjAdditive {
		t.Fatalf("первая поправка: %+v", adjs[0])
	}
	if adjs[1].Delta != -3 {
		t.Fatalf("вторая поправка: %+v", adjs[1])
	}
	if adjs[2].From != "XR-473" || adjs[2].kind() != adjPull {
		t.Fatalf("третья поправка: %+v", adjs[2])
	}
	if got := formatAdjTail(adjs); got != ", S+2, штраф-3, от XR-473" {
		t.Fatalf("сборка хвоста: %q", got)
	}
	if knownAdj("штраф") {
		t.Fatal("таблица не знает «штраф», а knownAdj его признал")
	}
	if _, err := parseAdjTail(", кривой хвост"); err == nil {
		t.Fatal("сломанная грамматика разобралась")
	}
}

// TestJSONCarriesAdjustments: дашборд хвост не разбирает, он читает готовые
// r, r_own и список поправок.
func TestJSONCarriesAdjustments(t *testing.T) {
	root := setup(t)
	if _, err := cmdSet(root, SetParams{ID: "XR-004", Cost: "S"}); err != nil {
		t.Fatal(err)
	}
	out, err := cmdListJSON(root, "backlog")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"r":11`) || !strings.Contains(out, `"r_own":9`) {
		t.Fatalf("json без итога и собственной суммы:\n%s", out)
	}
	if !strings.Contains(out, `"adjustments":[{"name":"S","delta":2}]`) {
		t.Fatalf("json без поправок:\n%s", out)
	}
}
