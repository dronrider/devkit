package main

import (
	"os"
	"strings"
	"testing"
)

// TestSetDueCLI гоняет срок через утилиту целиком, как её зовут панель и
// человек: флаг --due у set, суффикс в файле доски, отказ кривой даты и снятие
// прочерком. Тест стоит на входе команды, а не на внутренних функциях, и
// потому краснеет на коде без --due (DK-1464).
func TestSetDueCLI(t *testing.T) {
	root := setup(t)
	out, err := runCLI(t, "-C", root, "set", "XR-004", "--due", "2026-11-05")
	if err != nil {
		t.Fatalf("set --due: %v\n%s", err, out)
	}
	if !strings.Contains(out, "срок - -> 2026-11-05") {
		t.Fatalf("в ответе нет установки срока: %s", out)
	}
	board, rerr := os.ReadFile(boardPath(root))
	if rerr != nil {
		t.Fatal(rerr)
	}
	if !strings.Contains(string(board), "Хвост [срок: 2026-11-05]") {
		t.Fatalf("срок не лёг в доску:\n%s", board)
	}

	// Кривая дата отбивается отказом, доска остаётся нетронутой.
	bad, err := runCLI(t, "-C", root, "set", "XR-004", "--due", "завтра")
	if err == nil {
		t.Fatalf("кривая дата прошла: %s", bad)
	}
	board, _ = os.ReadFile(boardPath(root))
	if !strings.Contains(string(board), "Хвост [срок: 2026-11-05]") {
		t.Fatalf("отказ кривой даты тронул доску:\n%s", board)
	}

	// Прочерк снимает срок, строка возвращается к чистому заголовку.
	out, err = runCLI(t, "-C", root, "set", "XR-004", "--due", "-")
	if err != nil {
		t.Fatalf("снятие срока: %v\n%s", err, out)
	}
	if !strings.Contains(out, "срок снят") {
		t.Fatalf("в ответе нет снятия срока: %s", out)
	}
	final, _ := os.ReadFile(boardPath(root))
	if string(final) != fixtureBoard {
		t.Fatalf("после снятия срока доска не вернулась к исходной:\n%s", final)
	}
}
