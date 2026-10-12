package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dronrider/devkit/internal/taskform"
)

// regcheck:test-begin
//
// Регрессия DK-1356: шаг долга не звался при сдаче поездом и тиком сторожка.
// Механика отметки проверяется тут утилитой, поведение агентов на заказах
// проверяет стенд obeycheck.

// TestMoveToCheckOpensDebtMark: перевод в Check открывает отметку долга в
// файле задачи. Точка одна на все маршруты сдачи, и какому маршруту строку
// перевели, отметке безразлично.
func TestMoveToCheckOpensDebtMark(t *testing.T) {
	root := setup(t)
	if _, err := cmdMove(root, "XR-005", SectCheck, "", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "docs", "tasks", "XR-005.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !taskform.DebtHeld(string(data)) {
		t.Fatalf("перевод в Check не открыл отметку долга:\n%s", data)
	}
	if !strings.Contains(string(data), "- "+taskform.DebtOpenNote) {
		t.Fatalf("строки отметки в «Ходе работы» нет:\n%s", data)
	}
}

// TestDebtDoneClosesMark: закрытие пишет итог разбора в отметку, повторное
// закрытие без открытой отметки не отказывает и ничего не пишет.
func TestDebtDoneClosesMark(t *testing.T) {
	root := setup(t)
	if _, err := cmdMove(root, "XR-005", SectCheck, "", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	out, err := cmdDebtDone(root, "XR-005", DebtDoneParams{
		Note: "пересечений нет", Now: time.Date(2026, 10, 12, 14, 2, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "хвост долга закрыт") {
		t.Fatalf("закрытие не назвало себя: %q", out)
	}
	data, err := os.ReadFile(filepath.Join(root, "docs", "tasks", "XR-005.md"))
	if err != nil {
		t.Fatal(err)
	}
	if taskform.DebtHeld(string(data)) {
		t.Fatalf("отметка осталась открытой:\n%s", data)
	}
	if !strings.Contains(string(data), "- "+taskform.DebtDoneNote+", пересечений нет, 2026-10-12 14:02.") {
		t.Fatalf("итог разбора не вписан в отметку:\n%s", data)
	}
	again, err := cmdDebtDone(root, "XR-005", DebtDoneParams{Note: "пересечений нет"})
	if err != nil {
		t.Fatalf("повторное закрытие отказало: %v", err)
	}
	if !strings.Contains(again, "закрывать нечего") {
		t.Fatalf("повтор не назвал себя: %q", again)
	}
	if _, err := cmdDebtDone(root, "XR-005", DebtDoneParams{}); err == nil {
		t.Fatal("пустой итог должен отказывать")
	}
}

// TestCloseHeldByDebtMark: открытая отметка держит закрытие и отказ называет
// команду, закрытая отметка отпускает строку в архив.
func TestCloseHeldByDebtMark(t *testing.T) {
	root := setup(t)
	if _, err := cmdMove(root, "XR-005", SectCheck, "", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	_, err := cmdClose(root, CloseParams{ID: "XR-005", Date: "2026-10-12"})
	if err == nil {
		t.Fatal("close с открытым хвостом долга должен падать")
	}
	if !strings.Contains(err.Error(), "taskctl debt-done XR-005") {
		t.Fatalf("отказ не называет команду закрытия хвоста: %v", err)
	}
	if !onBoard(t, root, "XR-005") {
		t.Fatal("строка уехала с доски при отказе ворот")
	}
	if _, err := cmdDebtDone(root, "XR-005", DebtDoneParams{Note: "пересечений нет"}); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdClose(root, CloseParams{ID: "XR-005", Date: "2026-10-12"}); err != nil {
		t.Fatalf("закрытая отметка держит закрытие: %v", err)
	}
}

// TestDebtMarkRound: круг доработки открывает отметку заново, а возврат из
// Check без разбора второй открытой строки не пишет: последняя сдача уже ждёт.
func TestDebtMarkRound(t *testing.T) {
	root := setup(t)
	if _, err := cmdMove(root, "XR-005", SectCheck, "", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdDebtDone(root, "XR-005", DebtDoneParams{Note: "пересечений нет"}); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdMove(root, "XR-005", SectInProgress, "", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdMove(root, "XR-005", SectCheck, "", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "docs", "tasks", "XR-005.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !taskform.DebtHeld(string(data)) {
		t.Fatalf("новая сдача не ждёт разбора нового диффа:\n%s", data)
	}
	if got := strings.Count(string(data), "- "+taskform.DebtOpenNote); got != 2 {
		t.Fatalf("открытых строк %d, ожидалось 2:\n%s", got, data)
	}
	// Возврат без разбора и новая сдача молча: состояние то же, что и до
	// возврата, и вторая строка его не точнее.
	if _, err := cmdMove(root, "XR-005", SectInProgress, "", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdMove(root, "XR-005", SectCheck, "", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(filepath.Join(root, "docs", "tasks", "XR-005.md"))
	if got := strings.Count(string(data), "- "+taskform.DebtOpenNote); got != 2 {
		t.Fatalf("повторная сдача без разбора удвоила отметку:\n%s", data)
	}
}

// regcheck:test-end
