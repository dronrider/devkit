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

// boardRowText это строка доски в тестах: зеркальная строка отличается от
// обычной только ссылкой, ведущей на тикет.
type boardRowText struct {
	sect, id, rank, cost, link string
}

// writeBoard кладёт доску с нужными строками. Секции идут в том же порядке, что
// в живой доске, пустые не пишутся.
func writeBoard(t *testing.T, root string, rows ...boardRowText) {
	t.Helper()
	head := []struct{ title, sect string }{
		{"## In progress", sectInProgress},
		{"## Check (готово, ждёт проверки пользователем)", sectCheck},
		{"## Backlog", sectBacklog},
		{"## Blocked", sectBlocked},
	}
	var b strings.Builder
	b.WriteString("# доска\n\n")
	for _, h := range head {
		var body []string
		for _, r := range rows {
			if r.sect != h.sect {
				continue
			}
			body = append(body, fmt.Sprintf("| %s | зеркало тикета | task | P2 | %s | %s | %s |", r.id, r.rank, r.cost, r.link))
		}
		if len(body) == 0 {
			continue
		}
		fmt.Fprintf(&b, "%s\n\n| ID | Задача | Тип | P | R | Цена | Ссылка |\n", h.title)
		b.WriteString("|--------|--------|-----|---|---|------|--------|\n")
		b.WriteString(strings.Join(body, "\n") + "\n\n")
	}
	corpWrite(t, filepath.Join(root, boardPath), b.String())
}

func ticketLink(key string) string {
	return fmt.Sprintf("[%s](https://tracker.example/browse/%s)", key, key)
}

// writeRuns кладёт в журнал запусков отметки прогонов: команда, время и код,
// как их пишут все утилиты devkit.
func writeRuns(t *testing.T, root string, stamps ...time.Time) {
	t.Helper()
	var b strings.Builder
	for _, s := range stamps {
		fmt.Fprintf(&b, "%s\ttaskctl\tshow\t0\n", s.Format(stampLayout))
	}
	if err := os.WriteFile(filepath.Join(root, ".devkit", "log"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

// factsEnv раскладывает окружение сдачи: привязка с корп-клоном рядом, коммиты
// тикета в двух днях и журнал запусков вокруг них.
func factsEnv(t *testing.T) string {
	t.Helper()
	root := setupEnv(t, contourFile, strings.Replace(bindingFile, "repo = ../corp-repo", "repo = repo", 1))
	commitRepo(t, filepath.Join(root, "repo"), []struct {
		subject string
		when    time.Time
	}{
		{"feat: ABC-12 первый шаг", at(3, 11, 30)},
		{"fix: ABC-12 второй шаг", at(4, 10, 0)},
	})
	writeRuns(t, root, at(3, 10, 0), at(3, 10, 20), at(3, 11, 0), at(3, 11, 20), at(4, 9, 0), at(4, 9, 40))
	writeBoard(t, root, boardRowText{sectInProgress, "XR-1", "33 (25+5+1+0+2)", "M", ticketLink("ABC-12")})
	return root
}

// submit пишет ворклоги по фактам и только потом зовёт переход: тикет,
// переехавший на границу без единого ворклога, это ровно то, ради чего команда
// и заведена, поэтому проверяется весь след целиком, вместе с порядком.
func TestSubmitWritesWorklogsThenTransitions(t *testing.T) {
	root := factsEnv(t)
	fakeState.ticket.Status = "In Progress"
	msg, err := cmdSubmit(root, "ABC-12", false)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"fetch ABC-12",
		"worklog ABC-12 2026-08-03 2h",
		"worklog ABC-12 2026-08-04 1h",
		"transition ABC-12 Review [reviewer=ivanov]",
	}
	if !reflect.DeepEqual(fakeState.calls, want) {
		t.Fatalf("след вызовов:\n%v\nжду:\n%v", fakeState.calls, want)
	}
	if !strings.Contains(msg, "2026-08-03 2h") || !strings.Contains(msg, "«In Progress» -> «Review»") {
		t.Fatalf("вывод не рассказал, что уехало:\n%s", msg)
	}
}

// --log-only доливает время долгой задачи: ворклоги уходят, статус не трогается.
func TestSubmitLogOnlyKeepsStatus(t *testing.T) {
	root := factsEnv(t)
	fakeState.ticket.Status = "In Progress"
	msg, err := cmdSubmit(root, "ABC-12", true)
	if err != nil {
		t.Fatal(err)
	}
	if !hasCall(fakeState, "worklog ABC-12 2026-08-03") {
		t.Fatalf("ворклоги не уехали: %v", fakeState.calls)
	}
	if hasCall(fakeState, "transition") {
		t.Fatalf("--log-only статус не трогает: %v", fakeState.calls)
	}
	if !strings.Contains(msg, "--log-only") {
		t.Fatalf("вывод не объяснил отсутствие перехода:\n%s", msg)
	}
}

// Оба варианта идемпотентны: что уехало, лежит строкой в журнале, и повторный
// прогон те же дни второй раз не пишет.
func TestSubmitDoesNotDoubleWorklogs(t *testing.T) {
	root := factsEnv(t)
	fakeState.ticket.Status = "In Progress"
	if _, err := cmdSubmit(root, "ABC-12", true); err != nil {
		t.Fatal(err)
	}
	fakeState.calls = nil
	msg, err := cmdSubmit(root, "ABC-12", true)
	if err != nil {
		t.Fatal(err)
	}
	if hasCall(fakeState, "worklog") {
		t.Fatalf("второй прогон задвоил ворклоги: %v", fakeState.calls)
	}
	if !strings.Contains(msg, "уже записано раньше") {
		t.Fatalf("вывод не сказал про пропущенные дни:\n%s", msg)
	}
}

// Долитое время не мешает границе: после --log-only переход уходит, а дни
// второй раз не пишутся.
func TestSubmitAfterLogOnlyStillTransitions(t *testing.T) {
	root := factsEnv(t)
	fakeState.ticket.Status = "In Progress"
	if _, err := cmdSubmit(root, "ABC-12", true); err != nil {
		t.Fatal(err)
	}
	fakeState.calls = nil
	if _, err := cmdSubmit(root, "ABC-12", false); err != nil {
		t.Fatal(err)
	}
	want := []string{"fetch ABC-12", "transition ABC-12 Review [reviewer=ivanov]"}
	if !reflect.DeepEqual(fakeState.calls, want) {
		t.Fatalf("след вызовов:\n%v\nжду:\n%v", fakeState.calls, want)
	}
}

// Тикет уже на границе: ворклоги доливаются, а перехода нет.
func TestSubmitAlreadyInCheck(t *testing.T) {
	root := factsEnv(t)
	fakeState.ticket.Status = "Testing"
	msg, err := cmdSubmit(root, "ABC-12", false)
	if err != nil {
		t.Fatal(err)
	}
	if hasCall(fakeState, "transition") || !hasCall(fakeState, "worklog") {
		t.Fatalf("жду ворклоги без перехода: %v", fakeState.calls)
	}
	if !strings.Contains(msg, "перехода не делаю") {
		t.Fatalf("вывод не объяснил отсутствие перехода:\n%s", msg)
	}
}

// Коммитов с ключом тикета нет: писать нечего, и команда говорит об этом, а не
// сочиняет часы.
func TestSubmitWithoutFactsSaysSo(t *testing.T) {
	root := setupEnv(t, contourFile, strings.Replace(bindingFile, "repo = ../corp-repo", "repo = repo", 1))
	commitRepo(t, filepath.Join(root, "repo"), []struct {
		subject string
		when    time.Time
	}{{"init без ключа", at(3, 11, 30)}})
	writeRuns(t, root, at(3, 10, 0), at(3, 11, 0))
	fakeState.ticket.Status = "In Progress"
	msg, err := cmdSubmit(root, "ABC-12", true)
	if err != nil {
		t.Fatal(err)
	}
	if hasCall(fakeState, "worklog") {
		t.Fatalf("без коммитов тикета ворклогов нет: %v", fakeState.calls)
	}
	if !strings.Contains(msg, "фактов работы") {
		t.Fatalf("вывод не сказал про пустой факт:\n%s", msg)
	}
}

// Тикет в конечном статусе: ни ворклогов, ни перехода, только подсказка про
// закрытие строки доски.
func TestSubmitDoneHints(t *testing.T) {
	root := factsEnv(t)
	fakeState.ticket.Status = "Closed"
	msg, err := cmdSubmit(root, "ABC-12", false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "taskctl close") {
		t.Fatalf("жду подсказку про закрытие: %q", msg)
	}
	if !reflect.DeepEqual(fakeState.calls, []string{"fetch ABC-12"}) {
		t.Fatalf("в done автоматика не пишет, вижу %v", fakeState.calls)
	}
}

// Комментариев автоматика не пишет вовсе: ворклог уходит голым, тексты в трекер
// сочиняет человек. Адаптер здесь со всеми необязательными операциями, наличие
// comment поводом писать текст не служит.
func TestSubmitWritesNoComment(t *testing.T) {
	root := factsEnv(t)
	fullContour(t, root)
	fakeState.ticket.Status = "In Progress"
	if _, err := cmdSubmit(root, "ABC-12", false); err != nil {
		t.Fatal(err)
	}
	if hasCall(fakeState, "comment") {
		t.Fatalf("автоматика написала комментарий: %v", fakeState.calls)
	}
}

// fullContour переписывает контур окружения на адаптер со всеми
// необязательными операциями.
func fullContour(t *testing.T, root string) {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	text := strings.Replace(contourFile, `adapter = "fake"`, `adapter = "fake-full"`, 1)
	corpWrite(t, filepath.Join(home, ".devkit", "tracker", "corp.local"), text)
}
