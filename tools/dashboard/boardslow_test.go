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

// tunableClock подменяет часы сервера шагающими. Каждый спрос отдаёт текущее
// время и сдвигает часы на шаг вперёд, а длину шага тест меняет на ходу. Круг
// опроса выходит длиной в два шага, очередь семафора в один. Стенд не спит и
// стенного времени не мерит, проверяется факт строки в журнале (скилл
// test-standard, «Факт вместо стенного времени»).
func tunableClock(s *server, step time.Duration) (setStep func(time.Duration)) {
	var mu sync.Mutex
	cur := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		t := cur
		cur = cur.Add(step)
		return t
	}
	return func(d time.Duration) {
		mu.Lock()
		step = d
		mu.Unlock()
	}
}

// round гоняет один круг опроса доски мимо памяти ответов. Отпечатка файла нет,
// ответ в память не ложится, и следующий круг снова идёт до конца.
func (e *testEnv) round(t *testing.T) {
	t.Helper()
	fl := &boardFlight{done: make(chan struct{})}
	e.s.mu.Lock()
	e.s.flights[e.proj] = fl
	e.s.mu.Unlock()
	e.s.fly(e.proj, "", false, fl)
	if fl.err != nil {
		t.Fatalf("круг опроса доски: %v", fl.err)
	}
}

// narrowGate сужает общий потолок живых taskctl до одного и занимает его. Полёт
// стенда встаёт в очередь, как под потолком на машине с десятком деревьев.
// Отдаёт отмашку, после которой полёт идёт дальше.
func narrowGate(t *testing.T) (free func()) {
	t.Helper()
	old := taskctlGate
	g := newGate(1)
	taskctlGate = g
	g.enter()
	var once sync.Once
	free = func() { once.Do(g.leave) }
	t.Cleanup(func() {
		free()
		taskctlGate = old
	})
	return free
}

// awaitQueue ждёт названного числа полётов в очереди семафора. Очередь
// проверяется состоянием, а не секундомером.
func awaitQueue(t *testing.T, want int) {
	t.Helper()
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); {
		if _, waiting := taskctlGate.counts(); waiting == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	_, waiting := taskctlGate.counts()
	t.Fatalf("в очереди семафора %d полётов, жду %d", waiting, want)
}

// TestBoardSlowNamedInJournal: между порогом отзывчивости и потолком
// подпроцессов дашборд тормозил молча. Потолок снимает опрос на тридцатой
// секунде, а просадка до пяти секунд человеку уже заметна, и узнать о ней было
// негде. Теперь её называет строка журнала с числом секунд.
func TestBoardSlowNamedInJournal(t *testing.T) {
	e := newTestEnv(t)
	said := slowJournal(e.s)
	tunableClock(e.s, 7*time.Second)

	if _, err := e.s.projectBoard(e.proj); err != nil {
		t.Fatalf("обход доски: %v", err)
	}
	got := said()
	if !strings.Contains(got, "обход доски") || !strings.Contains(got, "ответил за 14.0с") {
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
	tunableClock(e.s, time.Second)

	if _, err := e.s.projectBoard(e.proj); err != nil {
		t.Fatalf("обход доски: %v", err)
	}
	if got := said(); strings.Contains(got, "обход доски") {
		t.Fatalf("в журнале %q: обход уложился в порог, а строка про просадку всё равно встала", got)
	}
}

// TestBoardSlowCountsQueueWait: число секунд обязано включать ожидание в
// очереди семафора. Живых taskctl разом шесть, деревьев задач на машине больше
// десятка, и под полным прогоном рядом экран ждёт как раз очереди. Счёт от
// входа в семафор дал бы «уложился в порог» там, где человек смотрел на
// пустой экран вдвое дольше порога (замечание ревью DK-1168).
func TestBoardSlowCountsQueueWait(t *testing.T) {
	e := newTestEnv(t)
	said := slowJournal(e.s)
	// Шаг четыре секунды: сам опрос короче порога, а круг с очередью вдвое
	// длиннее его и порог перебирает.
	tunableClock(e.s, 4*time.Second)
	free := narrowGate(t)

	done := make(chan error, 1)
	go func() {
		_, err := e.s.projectBoard(e.proj)
		done <- err
	}()
	awaitQueue(t, 1)
	free()
	if err := <-done; err != nil {
		t.Fatalf("обход доски: %v", err)
	}

	got := said()
	if !strings.Contains(got, "ответил за 8.0с") {
		t.Fatalf("в журнале %q: жду круг целиком, четыре секунды очереди и четыре опроса, а не одну его половину", got)
	}
	if !strings.Contains(got, "в очереди 4.0с") {
		t.Fatalf("в журнале %q: жду ожидание в очереди отдельным числом. Без него причину просадки по строке не назвать", got)
	}
}

// TestBoardSlowSaidOncePerBand: у жалобы на просадку та же полоса, что у
// соседнего noteLag. Без полосы одна и та же строка забивает журнал. На живой
// машине жалоба на пропущенный круг заняла 26735 строк из 37799. Полосу
// снимает первый уложившийся круг, и следующая просадка снова стоит строки.
func TestBoardSlowSaidOncePerBand(t *testing.T) {
	e := newTestEnv(t)
	said := slowJournal(e.s)
	setStep := tunableClock(e.s, 7*time.Second)

	e.round(t)
	e.round(t)
	// Круг уложился в порог: полоса снята.
	setStep(time.Second)
	e.round(t)
	setStep(7 * time.Second)
	e.round(t)

	got := said()
	if n := strings.Count(got, "ответил за"); n != 2 {
		t.Fatalf("про просадку сказано %d раз, жду два. Одна строка на полосу, снимает полосу уложившийся круг:\n%s", n, got)
	}
}
