package main

import (
	"os"
	"strings"
	"testing"
)

// TestSetDue ставит и снимает срок строки: дата живёт суффиксом заголовка,
// порядок строк при этом не меняется, его держит ранг (DK-1464, развилка
// «срок-ранг» закрыта исполнителем).
func TestSetDue(t *testing.T) {
	root := setup(t)
	msg, err := cmdSet(root, SetParams{ID: "XR-004", Due: "2026-11-05"})
	if err != nil {
		t.Fatal(err)
	}
	if msg != "XR-004: срок - -> 2026-11-05" {
		t.Fatalf("сообщение: %q", msg)
	}
	b, _ := LoadBoard(boardPath(root))
	if got := b.find("XR-004").Title; got != "Хвост [срок: 2026-11-05]" {
		t.Fatalf("заголовок после установки: %q", got)
	}
	// Повторная правка заменяет дату, а не копит суффиксы.
	msg, err = cmdSet(root, SetParams{ID: "XR-004", Due: "2026-12-01"})
	if err != nil {
		t.Fatal(err)
	}
	if msg != "XR-004: срок 2026-11-05 -> 2026-12-01" {
		t.Fatalf("сообщение замены: %q", msg)
	}
	// Прочерк снимает срок, строка возвращается к чистому заголовку.
	msg, err = cmdSet(root, SetParams{ID: "XR-004", Due: "-"})
	if err != nil {
		t.Fatal(err)
	}
	if msg != "XR-004: срок снят" {
		t.Fatalf("сообщение снятия: %q", msg)
	}
	b, _ = LoadBoard(boardPath(root))
	if got := b.find("XR-004").Title; got != "Хвост" {
		t.Fatalf("заголовок после снятия: %q", got)
	}
}

// TestSetDueDateIsValidated: срок это абсолютная дата календаря, «завтра» и
// 30 февраля не проходят, доска остаётся нетронутой.
func TestSetDueDateIsValidated(t *testing.T) {
	root := setup(t)
	for _, bad := range []string{"завтра", "2026-02-30", "2026-11-5", "2026-11-05x"} {
		if _, err := cmdSet(root, SetParams{ID: "XR-004", Due: bad}); err == nil {
			t.Fatalf("%q должен был отбиться", bad)
		}
	}
	board, _ := os.ReadFile(boardPath(root))
	if string(board) != fixtureBoard {
		t.Fatalf("доска изменилась после отказов:\n%s", board)
	}
}

// TestSetDueKeepsOtherTails: срок встаёт на своё место в фиксированном порядке
// хвостов и переживает чужие правки строки, а линтер порядка молчит.
func TestSetDueKeepsOtherTails(t *testing.T) {
	root := setup(t)
	b, err := LoadBoard(boardPath(root))
	if err != nil {
		t.Fatal(err)
	}
	row := b.find("XR-004")
	row.Title = "Хвост [после XR-002] [долг] [взвод] [план: целиком] [приёмка: mixed]"
	b.Lines[row.LineIdx] = formatRow(row)
	if err := b.Save(); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdSet(root, SetParams{ID: "XR-004", Due: "2026-11-05"}); err != nil {
		t.Fatal(err)
	}
	b, _ = LoadBoard(boardPath(root))
	want := "Хвост [после XR-002] [долг] [взвод] [план: целиком] [срок: 2026-11-05] [приёмка: mixed]"
	if got := b.find("XR-004").Title; got != want {
		t.Fatalf("срок не на своём месте: %q", got)
	}
	// Замена заголовка переносит срок вместе с остальными хвостами.
	if _, err := cmdSet(root, SetParams{ID: "XR-004", Title: "Другой хвост"}); err != nil {
		t.Fatal(err)
	}
	b, _ = LoadBoard(boardPath(root))
	if got := b.find("XR-004").Title; got != strings.Replace(want, "Хвост", "Другой хвост", 1) {
		t.Fatalf("срок не пережил замену заголовка: %q", got)
	}
	if finds := lintTails(b, boardPath(root)); len(finds) != 0 {
		t.Fatalf("линтер ругается на порядок хвостов: %v", finds)
	}
}

// TestJSONDueField: list --json несёт срок отдельным полем due, пустое поле у
// строки без срока. Виджеты главной и напоминания читают это поле, а не
// разбирают заголовок заново.
func TestJSONDueField(t *testing.T) {
	root := setup(t)
	b, err := LoadBoard(boardPath(root))
	if err != nil {
		t.Fatal(err)
	}
	row := b.find("XR-004")
	row.Title = "Хвост [срок: 2026-11-05]"
	b.Lines[row.LineIdx] = formatRow(row)
	if err := b.Save(); err != nil {
		t.Fatal(err)
	}
	out, err := cmdListJSON(root, "backlog", false)
	if err != nil {
		t.Fatal(err)
	}
	rows := decodeBoard(t, out).Sections[0].Rows
	var got, plain *jsonRow
	for i := range rows {
		switch rows[i].ID {
		case "XR-004":
			got = &rows[i]
		case "XR-003":
			plain = &rows[i]
		}
	}
	if got == nil || plain == nil {
		t.Fatal("строки фикстуры не нашлись")
	}
	if got.Title != "Хвост" || got.Due != "2026-11-05" {
		t.Fatalf("due не разобран: title=%q due=%q", got.Title, got.Due)
	}
	if plain.Due != "" {
		t.Fatalf("у строки без срока поле due должно быть пустым: %q", plain.Due)
	}
}

// TestSetDueRankUntouched: срок не меняет ранг и порядок Backlog, он
// группирует и подсвечивает, а порядок держит ранг.
func TestSetDueRankUntouched(t *testing.T) {
	root := setup(t)
	if _, err := cmdSet(root, SetParams{ID: "XR-004", Due: "2026-11-05"}); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(backlogIDs(t, root), " "); got != "XR-002 XR-001 XR-003 XR-004" {
		t.Fatalf("порядок Backlog сдвинулся: %s", got)
	}
	b, _ := LoadBoard(boardPath(root))
	if r := b.find("XR-004").RTotal; r != 9 {
		t.Fatalf("ранг изменился: %d", r)
	}
}

// TestDueDoesNotMaskAccept: срок стоит до приёмки, и общий разбор вида
// (internal/accept, его зовёт и shipctl) продолжает видеть неагентский вид у
// строки со сроком.
func TestDueDoesNotMaskAccept(t *testing.T) {
	if got := acceptOf("Задача [срок: 2026-11-05] [приёмка: mixed]"); got != "mixed" {
		t.Fatalf("срок скрыл вид приёмки: %q", got)
	}
}
