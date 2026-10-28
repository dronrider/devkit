package main

import (
	"os"
	"strings"
	"testing"
)

// TestSetControl ставит и снимает контроль строки: число дней живёт суффиксом
// заголовка между сроком и приёмкой (DK-1470, развилка «контроль-потолки»
// закрыта исполнителем: контроль живёт внутри потолков DK-400, риски кладутся
// черновиком, строк контроль не заводит).
func TestSetControl(t *testing.T) {
	root := setup(t)
	if _, err := cmdSet(root, SetParams{ID: "XR-004", Due: "2026-11-05"}); err != nil {
		t.Fatal(err)
	}
	msg, err := cmdSet(root, SetParams{ID: "XR-004", Control: "3"})
	if err != nil {
		t.Fatal(err)
	}
	if msg != "XR-004: контроль взят: проверка за 3 дней до срока" {
		t.Fatalf("сообщение: %q", msg)
	}
	b, _ := LoadBoard(boardPath(root))
	if got := b.find("XR-004").Title; got != "Хвост [срок: 2026-11-05] [контроль: 3]" {
		t.Fatalf("заголовок после установки: %q", got)
	}
	// Повторная правка заменяет число дней, а не копит суффиксы.
	msg, err = cmdSet(root, SetParams{ID: "XR-004", Control: "0"})
	if err != nil {
		t.Fatal(err)
	}
	if msg != "XR-004: контроль 3 -> 0 дней до срока" {
		t.Fatalf("сообщение замены: %q", msg)
	}
	b, _ = LoadBoard(boardPath(root))
	if got := b.find("XR-004").Title; got != "Хвост [срок: 2026-11-05] [контроль: 0]" {
		t.Fatalf("заголовок после замены: %q", got)
	}
	// Прочерк снимает контроль, срок остаётся.
	msg, err = cmdSet(root, SetParams{ID: "XR-004", Control: "-"})
	if err != nil {
		t.Fatal(err)
	}
	if msg != "XR-004: контроль снят" {
		t.Fatalf("сообщение снятия: %q", msg)
	}
	b, _ = LoadBoard(boardPath(root))
	if got := b.find("XR-004").Title; got != "Хвост [срок: 2026-11-05]" {
		t.Fatalf("заголовок после снятия: %q", got)
	}
}

// TestSetControlRequiresDue: контроль считается от срока, и строка без срока
// контроль не берёт. Срок можно назначить тем же вызовом.
func TestSetControlRequiresDue(t *testing.T) {
	root := setup(t)
	if _, err := cmdSet(root, SetParams{ID: "XR-004", Control: "3"}); err == nil {
		t.Fatal("контроль без срока должен был отбиться")
	}
	board, _ := os.ReadFile(boardPath(root))
	if string(board) != fixtureBoard {
		t.Fatalf("доска изменилась после отказа:\n%s", board)
	}
	if _, err := cmdSet(root, SetParams{ID: "XR-004", Due: "2026-11-05", Control: "2"}); err != nil {
		t.Fatalf("контроль одним вызовом со сроком не встал: %v", err)
	}
	// Срок не снять, пока строка на контроле: окно повисло бы без даты.
	if _, err := cmdSet(root, SetParams{ID: "XR-004", Due: "-"}); err == nil {
		t.Fatal("снятие срока при живом контроле должно было отбиться")
	}
	// Снятие контроля тем же вызовом срок разблокирует.
	if _, err := cmdSet(root, SetParams{ID: "XR-004", Due: "-", Control: "-"}); err != nil {
		t.Fatalf("снятие срока с контролем одним вызовом не прошло: %v", err)
	}
	b, _ := LoadBoard(boardPath(root))
	if got := b.find("XR-004").Title; got != "Хвост" {
		t.Fatalf("заголовок после снятия обоих: %q", got)
	}
}

// TestSetControlValidated: число дней разбирается как целое шкалы 0..365,
// кривые значения доску не трогают.
func TestSetControlValidated(t *testing.T) {
	root := setup(t)
	if _, err := cmdSet(root, SetParams{ID: "XR-004", Due: "2026-11-05"}); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"три", "-5", "366", "1.5"} {
		if _, err := cmdSet(root, SetParams{ID: "XR-004", Control: bad}); err == nil {
			t.Fatalf("%q должен был отбиться", bad)
		}
	}
	b, _ := LoadBoard(boardPath(root))
	if got := b.find("XR-004").Title; got != "Хвост [срок: 2026-11-05]" {
		t.Fatalf("доска изменилась после отказов: %q", got)
	}
}

// TestSetControlKeepsOtherTails: контроль встаёт на своё место в фиксированном
// порядке хвостов, переживает чужие правки строки, и линтер порядка молчит.
func TestSetControlKeepsOtherTails(t *testing.T) {
	root := setup(t)
	b, err := LoadBoard(boardPath(root))
	if err != nil {
		t.Fatal(err)
	}
	row := b.find("XR-004")
	row.Title = "Хвост [после XR-002] [долг] [план: целиком] [срок: 2026-11-05] [приёмка: mixed]"
	b.Lines[row.LineIdx] = formatRow(row)
	if err := b.Save(); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdSet(root, SetParams{ID: "XR-004", Control: "2"}); err != nil {
		t.Fatal(err)
	}
	b, _ = LoadBoard(boardPath(root))
	want := "Хвост [после XR-002] [долг] [план: целиком] [срок: 2026-11-05] [контроль: 2] [приёмка: mixed]"
	if got := b.find("XR-004").Title; got != want {
		t.Fatalf("контроль не на своём месте: %q", got)
	}
	// Замена заголовка переносит контроль вместе с остальными хвостами.
	if _, err := cmdSet(root, SetParams{ID: "XR-004", Title: "Другой хвост"}); err != nil {
		t.Fatal(err)
	}
	b, _ = LoadBoard(boardPath(root))
	if got := b.find("XR-004").Title; got != strings.Replace(want, "Хвост", "Другой хвост", 1) {
		t.Fatalf("контроль не пережил замену заголовка: %q", got)
	}
	if finds := lintTails(b, boardPath(root)); len(finds) != 0 {
		t.Fatalf("линтер ругается на порядок хвостов: %v", finds)
	}
}

// TestLintControlWithoutDue: контроль без срока это находка линтера, воронка
// команд такую строку не пускает, сюда попадает ручная правка доски.
func TestLintControlWithoutDue(t *testing.T) {
	root := setup(t)
	b, err := LoadBoard(boardPath(root))
	if err != nil {
		t.Fatal(err)
	}
	row := b.find("XR-004")
	row.Title = "Хвост [контроль: 2]"
	b.Lines[row.LineIdx] = formatRow(row)
	if err := b.Save(); err != nil {
		t.Fatal(err)
	}
	finds := lintControl(b, boardPath(root))
	if len(finds) != 1 || !strings.Contains(finds[0], "контроль без срока") {
		t.Fatalf("находка контроля без срока не поднялась: %v", finds)
	}
}

// TestJSONControlField: list --json несёт контроль отдельным полем control,
// nil у строки без контроля. Ноль дней это «в сам день срока» и обязан
// выживать в json, поэтому поле указателем.
func TestJSONControlField(t *testing.T) {
	root := setup(t)
	b, err := LoadBoard(boardPath(root))
	if err != nil {
		t.Fatal(err)
	}
	row := b.find("XR-004")
	row.Title = "Хвост [срок: 2026-11-05] [контроль: 0]"
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
	if got.Title != "Хвост" || got.Control == nil || *got.Control != 0 {
		t.Fatalf("control не разобран: title=%q control=%v", got.Title, got.Control)
	}
	if plain.Control != nil {
		t.Fatalf("у строки без контроля поле control должно быть nil: %v", *plain.Control)
	}
}

// TestRowStateNoteControlOverdue: напоминание строки на просроченном контроле
// говорит про дожим, а не только называет дату.
func TestRowStateNoteControlOverdue(t *testing.T) {
	note, err := rowStateNote("Хвост [срок: 2020-01-01] [контроль: 2]")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(note, "контроль просрочен") || !strings.Contains(note, "2020-01-01") {
		t.Fatalf("повод контроля не собран: %q", note)
	}
	note, err = rowStateNote("Хвост [срок: 2999-01-01]")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(note, "контроль") {
		t.Fatalf("строка без контроля несёт лишний повод: %q", note)
	}
}
