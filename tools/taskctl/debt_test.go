package main

import (
	"strings"
	"testing"

	"github.com/dronrider/devkit/internal/accept"
)

// TestDebtMarkAndClear: метка ставится и снимается одной командой, повтор
// ничего не портит и говорит словами.
func TestDebtMarkAndClear(t *testing.T) {
	root := setup(t)
	out, err := cmdDebt(root, "XR-004", DebtParams{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "[долг]") {
		t.Fatalf("вывод без метки: %q", out)
	}
	b, err := LoadBoard(boardPath(root))
	if err != nil {
		t.Fatal(err)
	}
	if !isDebt(b.find("XR-004").Title) {
		t.Fatalf("метка не села: %q", b.find("XR-004").Title)
	}
	again, err := cmdDebt(root, "XR-004", DebtParams{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(again, "уже помечена") {
		t.Fatalf("повтор не назвал себя повтором: %q", again)
	}
	if _, err := cmdDebt(root, "XR-004", DebtParams{Off: true}); err != nil {
		t.Fatal(err)
	}
	b, err = LoadBoard(boardPath(root))
	if err != nil {
		t.Fatal(err)
	}
	if isDebt(b.find("XR-004").Title) {
		t.Fatalf("метка не снялась: %q", b.find("XR-004").Title)
	}
	if _, err := cmdDebt(root, "XR-999", DebtParams{}); err == nil {
		t.Fatal("ждал отказ на строке, которой нет")
	}
}

// TestDebtAddFlag: строка заводится с меткой сразу, и метка стоит до вида
// приёмки.
func TestDebtAddFlag(t *testing.T) {
	root := setup(t)
	if _, err := cmdAdd(root, AddParams{
		Title: "Стенд без драйвера", Type: "task", Rank: "0+3+0+0+4",
		Accept: acceptUser, Barrier: "глаза", Debt: true,
	}); err != nil {
		t.Fatal(err)
	}
	b, err := LoadBoard(boardPath(root))
	if err != nil {
		t.Fatal(err)
	}
	var row *Row
	for _, r := range b.Rows {
		if strings.HasPrefix(r.Title, "Стенд без драйвера") {
			row = r
		}
	}
	if row == nil {
		t.Fatal("новой строки на доске нет")
	}
	if !strings.HasSuffix(row.Title, "[долг] [приёмка: user]") {
		t.Fatalf("порядок хвостов сломан: %q", row.Title)
	}
}

// TestDebtKeepsAcceptKind: регрессия по разбору 2026-09-11. Регулярки пакета
// accept привязаны к концу строки, и суффикс, встань он после «[приёмка:
// ...]», тихо вернул бы agent у пользовательской задачи. Метка стоит до
// приёмки, и вид читается прежним при любом наборе хвостов.
func TestDebtKeepsAcceptKind(t *testing.T) {
	cases := []string{
		"Долг [долг] [приёмка: user]",
		"Долг [после XR-001] [долг] [взвод] [приёмка: mixed]",
		"Долг [долг] [приёмка: user] [провал: 500]",
		"Долг [долг] [приёмка: mixed] [блок: ждём железо]",
	}
	want := []string{accept.User, accept.Mixed, accept.User, accept.Mixed}
	for i, title := range cases {
		if got := acceptOf(title); got != want[i] {
			t.Fatalf("acceptOf(%q) = %q, ожидал %q", title, got, want[i])
		}
		if !isDebt(title) {
			t.Fatalf("isDebt(%q) не увидел метку", title)
		}
	}
}

// TestDebtListFilter: фильтр печатает только помеченные строки и не режет
// Backlog десяткой.
func TestDebtListFilter(t *testing.T) {
	root := setup(t)
	if _, err := cmdDebt(root, "XR-004", DebtParams{}); err != nil {
		t.Fatal(err)
	}
	out, err := cmdList(root, "", true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "XR-004") {
		t.Fatalf("помеченной строки в выводе нет: %q", out)
	}
	for _, id := range []string{"XR-001", "XR-002", "XR-003", "XR-005"} {
		if strings.Contains(out, id) {
			t.Fatalf("непомеченная строка %s попала в вывод: %q", id, out)
		}
	}
	js, err := cmdListJSON(root, "backlog", true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(js, `"debt":true`) || strings.Contains(js, "XR-002") {
		t.Fatalf("машинный вывод фильтра неверен: %q", js)
	}
}

// TestDebtSurvivesSetAndMove: метка это постоянное свойство строки, и замена
// заголовка с переводом по статусам её не теряет.
func TestDebtSurvivesSetAndMove(t *testing.T) {
	root := setup(t)
	if _, err := cmdDebt(root, "XR-005", DebtParams{}); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdSet(root, SetParams{ID: "XR-005", Title: "Другой заголовок"}); err != nil {
		t.Fatal(err)
	}
	b, err := LoadBoard(boardPath(root))
	if err != nil {
		t.Fatal(err)
	}
	if !isDebt(b.find("XR-005").Title) {
		t.Fatalf("замена заголовка потеряла метку: %q", b.find("XR-005").Title)
	}
}

// TestLintTails ловит сломанный порядок хвостов: в такой строке разбор не
// видит ни метки долга, ни вида приёмки.
func TestLintTails(t *testing.T) {
	root := setup(t)
	b, err := LoadBoard(boardPath(root))
	if err != nil {
		t.Fatal(err)
	}
	row := b.find("XR-004")
	row.Title = "Хвост [приёмка: user] [долг]"
	b.updateLine(row.LineIdx, formatRow(row))
	if err := b.Save(); err != nil {
		t.Fatal(err)
	}
	b, err = LoadBoard(boardPath(root))
	if err != nil {
		t.Fatal(err)
	}
	finds := lintTails(b, "docs/TASKS.md")
	if len(finds) != 1 || !strings.Contains(finds[0], "приёмка") {
		t.Fatalf("находки порядка хвостов нет: %v", finds)
	}
}
