package main

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// captureMoves подменяет вызов taskctl записью: доску sync двигает командой её
// утилиты, и проверять тут надо ровно вызов, а не переписанную таблицу.
func captureMoves(t *testing.T) *[]string {
	t.Helper()
	var moves []string
	saved := moveRow
	moveRow = func(root, id, sect string) error {
		moves = append(moves, fmt.Sprintf("%s %s", id, boardStatusName(sect)))
		return nil
	}
	t.Cleanup(func() { moveRow = saved })
	return &moves
}

func syncEnv(t *testing.T, rows ...boardRowText) string {
	t.Helper()
	root := setupEnv(t, contourFile, bindingFile)
	writeBoard(t, root, rows...)
	return root
}

// Трекер впереди доски (человек передвинул тикет руками), значит доска
// догоняет вызовом taskctl move. В трекер прогон не пишет вовсе: за sync только
// pull.
func TestSyncMovesBoardBehindTracker(t *testing.T) {
	root := syncEnv(t, boardRowText{sectBacklog, "XR-1", "20 (10+5+1+0+4)", "M", ticketLink("ABC-12")})
	moves := captureMoves(t)
	fakeState.ticket.Status = "Development"
	msg, err := cmdSync(root, false)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(*moves, []string{"XR-1 in-progress"}) {
		t.Fatalf("движения доски: %v", *moves)
	}
	if !reflect.DeepEqual(fakeState.calls, []string{"fetch ABC-12"}) {
		t.Fatalf("sync в трекер не пишет, вижу %v", fakeState.calls)
	}
	if !strings.Contains(msg, "доска догоняет тикет ABC-12") {
		t.Fatalf("вывод не рассказал про догон:\n%s", msg)
	}
}

// Закрытый тикет строку доски не закрывает: закрытие требует даты и коммитов, и
// решает его диспетчер, а sync даёт подсказку.
func TestSyncDoneHintsAndKeepsRow(t *testing.T) {
	root := syncEnv(t, boardRowText{sectCheck, "XR-1", "20 (10+5+1+0+4)", "M", ticketLink("ABC-12")})
	moves := captureMoves(t)
	fakeState.ticket.Status = "Done"
	msg, err := cmdSync(root, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(*moves) != 0 {
		t.Fatalf("закрытый тикет закрыл строку доски сам: %v", *moves)
	}
	if !strings.Contains(msg, "taskctl close") {
		t.Fatalf("жду подсказку про закрытие:\n%s", msg)
	}
}

// Доска впереди тикета это находка: границу прошли мимо команд take и submit, и
// трекер отстал. Двигать тут нечего, sync в трекер не пишет.
func TestSyncBoardAheadIsFinding(t *testing.T) {
	root := syncEnv(t, boardRowText{sectCheck, "XR-1", "20 (10+5+1+0+4)", "M", ticketLink("ABC-12")})
	moves := captureMoves(t)
	fakeState.ticket.Status = "Open"
	msg, err := cmdSync(root, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(*moves) != 0 {
		t.Fatalf("доска впереди тикета, двигать её назад нечем: %v", *moves)
	}
	if !strings.Contains(msg, "находка:") || !strings.Contains(msg, "мимо") {
		t.Fatalf("жду находку про пройденную мимо границу:\n%s", msg)
	}
	if !reflect.DeepEqual(fakeState.calls, []string{"fetch ABC-12"}) {
		t.Fatalf("sync в трекер не пишет, вижу %v", fakeState.calls)
	}
}

// Статуса нет ни в одном списке контура: находка, ни строка, ни тикет не
// двигаются.
func TestSyncUnknownStatusIsFinding(t *testing.T) {
	root := syncEnv(t, boardRowText{sectInProgress, "XR-1", "20 (10+5+1+0+4)", "M", ticketLink("ABC-12")})
	moves := captureMoves(t)
	fakeState.ticket.Status = "Waiting for QA"
	msg, err := cmdSync(root, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(*moves) != 0 || !strings.Contains(msg, "находка:") {
		t.Fatalf("движения %v, вывод:\n%s", *moves, msg)
	}
}

// Строки без ссылки на тикет это обычные задачи доски, sync их не трогает и в
// трекер за ними не ходит.
func TestSyncSkipsRowsWithoutTicket(t *testing.T) {
	root := syncEnv(t,
		boardRowText{sectInProgress, "XR-1", "20 (10+5+1+0+4)", "M", "[tasks/XR-1.md](tasks/XR-1.md)"},
		boardRowText{sectInProgress, "XR-2", "20 (10+5+1+0+4)", "M", ticketLink("ABC-12")},
	)
	moves := captureMoves(t)
	fakeState.ticket.Status = "In Progress"
	msg, err := cmdSync(root, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(*moves) != 0 {
		t.Fatalf("совпавшая строка не двигается: %v", *moves)
	}
	if !reflect.DeepEqual(fakeState.calls, []string{"fetch ABC-12"}) {
		t.Fatalf("в трекер ходили за чужой строкой: %v", fakeState.calls)
	}
	if !strings.Contains(msg, "зеркальных строк: 1") {
		t.Fatalf("свод не сошёлся:\n%s", msg)
	}
}

// --if-stale гоняет только по протухшей отметке: старт сессии случается часто, и
// поход в сеть на каждом был бы платой ни за что.
func TestSyncIfStaleSkipsFreshMark(t *testing.T) {
	root := syncEnv(t, boardRowText{sectBacklog, "XR-1", "20 (10+5+1+0+4)", "M", ticketLink("ABC-12")})
	moves := captureMoves(t)
	if err := markSync(root); err != nil {
		t.Fatal(err)
	}
	msg, err := cmdSync(root, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(fakeState.calls) != 0 || len(*moves) != 0 {
		t.Fatalf("по свежей отметке прогона нет, вижу %v и %v", fakeState.calls, *moves)
	}
	if !strings.Contains(msg, "свежая") {
		t.Fatalf("вывод не объяснил отсутствие прогона:\n%s", msg)
	}
}

// Протухшая отметка прогон пропускает, и он же её освежает.
func TestSyncIfStaleRunsOnStaleMark(t *testing.T) {
	root := syncEnv(t, boardRowText{sectBacklog, "XR-1", "20 (10+5+1+0+4)", "M", ticketLink("ABC-12")})
	captureMoves(t)
	if err := markSync(root); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-syncMaxAge - time.Hour)
	if err := os.Chtimes(filepath.Join(root, syncMarkPath), old, old); err != nil {
		t.Fatal(err)
	}
	fakeState.ticket.Status = "Development"
	if _, err := cmdSync(root, true); err != nil {
		t.Fatal(err)
	}
	if !hasCall(fakeState, "fetch ABC-12") {
		t.Fatalf("по протухшей отметке прогон обязан состояться: %v", fakeState.calls)
	}
	fi, err := os.Stat(filepath.Join(root, syncMarkPath))
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(fi.ModTime()) > time.Minute {
		t.Fatalf("отметка не освежилась: %v", fi.ModTime())
	}
}

// Отметку прогона читает status, а протухшую ловит doctor: без неё видно, что
// доска с тикетами ни разу не сверялась.
func TestSyncMarksFreshness(t *testing.T) {
	root := syncEnv(t, boardRowText{sectInProgress, "XR-1", "20 (10+5+1+0+4)", "M", ticketLink("ABC-12")})
	captureMoves(t)
	fakeState.ticket.Status = "In Progress"
	if _, err := cmdSync(root, false); err != nil {
		t.Fatal(err)
	}
	msg, err := cmdStatus(root)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "sync:\t0 мин назад") {
		t.Fatalf("status не увидел отметку прогона:\n%s", msg)
	}
}

// Строку ревью sync не двигает и в трекер за ней не ходит: судьбу такой строки
// решает MR, а не статус тикета (LLD DK-756, решение 7). Тикет тут ушёл далеко
// вперёд доски, и обычную зеркальную строку это подвинуло бы.
func TestSyncKeepsReviewRow(t *testing.T) {
	root := setupEnv(t, contourFile, bindingFile)
	corpWrite(t, filepath.Join(root, boardPath), reviewBoardText(
		boardRowText{sectBacklog, "XR-777", "3 (0+0+1+0+2)", "L", ticketLink("ABC-12")},
		"Ревью ABC-12: чужая задача",
	))
	moves := captureMoves(t)
	fakeState.ticket.Status = "Development"

	msg, err := cmdSync(root, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(*moves) != 0 {
		t.Fatalf("строка ревью подвинута: %v", *moves)
	}
	if len(fakeState.calls) != 0 {
		t.Fatalf("sync сходил в трекер за строкой ревью: %v", fakeState.calls)
	}
	if !strings.Contains(msg, "зеркальных строк на доске нет") {
		t.Fatalf("строка ревью посчитана зеркальной:\n%s", msg)
	}
}
