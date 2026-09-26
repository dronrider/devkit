package main

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// slowJournal подменяет журнал сервера копилкой строк.
func slowJournal(s *server) (said func() string) {
	var mu sync.Mutex
	var journal strings.Builder
	s.logf = func(format string, args ...any) {
		mu.Lock()
		fmt.Fprintf(&journal, format+"\n", args...)
		mu.Unlock()
	}
	return func() string {
		mu.Lock()
		defer mu.Unlock()
		return journal.String()
	}
}

// steppingClock подменяет часы сервера шагающими: каждый спрос сдвигает их на
// step вперёд, так что обход доски выходит длиной ровно в один шаг. Стенд не
// спит и стенного времени не мерит, а проверяется факт появления строки в
// журнале (скилл test-standard, «Факт вместо стенного времени»).
func steppingClock(s *server, step time.Duration) {
	base := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	var mu sync.Mutex
	calls := 0
	s.now = func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		t := base.Add(time.Duration(calls) * step)
		calls++
		return t
	}
}

// TestBoardSlowNamedInJournal: между порогом отзывчивости и потолком
// подпроцессов дашборд тормозил молча. Потолок снимает опрос на тридцатой
// секунде, а просадка до пяти секунд человеку уже заметна, и узнать о ней было
// негде. Теперь её называет строка журнала с числом секунд.
func TestBoardSlowNamedInJournal(t *testing.T) {
	e := newTestEnv(t)
	said := slowJournal(e.s)
	steppingClock(e.s, 7*time.Second)

	if _, err := e.s.projectBoard(e.proj); err != nil {
		t.Fatalf("обход доски: %v", err)
	}
	got := said()
	if !strings.Contains(got, "обход доски") || !strings.Contains(got, "7.0с") {
		t.Fatalf("в журнале %q: жду строку про просадку обхода доски с числом секунд", got)
	}
	if !strings.Contains(got, "порог отзывчивости 5с") {
		t.Fatalf("в журнале %q: жду сам порог, иначе число секунд не с чем сравнить", got)
	}
}

// TestBoardFastStaysOutOfJournal: строка пишется по просадке, а не каждым
// кругом опроса. Ручек у экрана десяток, и жалоба на каждый круг забила бы
// журнал так же, как её отсутствие скрывало просадку.
func TestBoardFastStaysOutOfJournal(t *testing.T) {
	e := newTestEnv(t)
	said := slowJournal(e.s)
	steppingClock(e.s, time.Second)

	if _, err := e.s.projectBoard(e.proj); err != nil {
		t.Fatalf("обход доски: %v", err)
	}
	if got := said(); strings.Contains(got, "обход доски") {
		t.Fatalf("в журнале %q: обход уложился в порог, а строка про просадку всё равно встала", got)
	}
}
