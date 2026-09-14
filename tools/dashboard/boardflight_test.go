package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// Опрос доски стоит подпроцесса, и под нагрузкой цена эта умножалась на число
// запросов экрана: 45 живых taskctl list по 12 деревьям, средняя нагрузка 154 и
// дашборд, который не отвечает вовсе (DK-992). Здесь стоят стенды на то, чем это
// закрыто: одиночный полёт на дерево, общий потолок детей, устаревший ответ
// вместо ожидания и счёт отставания снаружи.

// blockingScript это тело фикстуры, которая отмечает свой запуск в журнале и
// стоит, пока тест не положит файл-отмашку. Так «сколько детей живёт разом»
// проверяется состоянием, а не секундомером.
func blockingScript(log, release, body string) string {
	return fmt.Sprintf("echo вызов >> %q\nwhile [ ! -f %q ]; do sleep 0.02; done\n%s", log, release, body)
}

// awaitLoad ждёт названной нагрузки опроса: столько-то живых детей taskctl и
// столько-то ждущих запросов.
func awaitLoad(t *testing.T, s *server, live, waiting int) {
	t.Helper()
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); {
		if l, w := s.boardLoad(); l == live && w == waiting {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	l, w := s.boardLoad()
	t.Fatalf("нагрузка опроса: живых %d, ждут %d, а жду живых %d и ждущих %d", l, w, live, waiting)
}

// release кладёт отмашку фикстуре.
func release(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
}

// Толпа запросов по одному дереву стоит одного taskctl: запрос, заставший чужой
// опрос, ждёт его ответа, а не поднимает свой. Без этого каждая ручка экрана
// гнала своего ребёнка, и чем дольше отвечал taskctl, тем больше их жило разом.
// Заодно проверяется строка журнала: первый пропущенный круг назван, и назван
// один раз, а не на каждый запрос.
func TestBoardSingleFlightPerTree(t *testing.T) {
	e := newTestEnv(t)
	var mu sync.Mutex
	var journal strings.Builder
	e.s.logf = func(format string, args ...any) {
		mu.Lock()
		fmt.Fprintf(&journal, format+"\n", args...)
		mu.Unlock()
	}
	log := filepath.Join(e.home, "taskctl.log")
	gofile := filepath.Join(e.home, "отмашка")
	writeScript(t, e.bin, "taskctl", blockingScript(log, gofile, fmt.Sprintf("echo '%s'", boardFixtureJSON)))

	const crowd = 8
	var wg sync.WaitGroup
	raws := make([]json.RawMessage, crowd)
	errs := make([]error, crowd)
	for i := 0; i < crowd; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			raws[i], errs[i] = e.s.projectBoard(e.proj)
		}(i)
	}
	// Толпа собралась: один опрос летит, остальные ждут его.
	awaitLoad(t, e.s, 1, crowd-1)
	release(t, gofile)
	wg.Wait()

	if n := calls(t, log); n != 1 {
		t.Fatalf("%d запросов по одному дереву стоили %d запусков taskctl, жду один: одиночного полёта нет", crowd, n)
	}
	for i := range raws {
		if errs[i] != nil || !strings.Contains(string(raws[i]), "XR-005") {
			t.Fatalf("запрос %d получил %s (%v): ждущий обязан получить ответ полёта", i, raws[i], errs[i])
		}
	}
	mu.Lock()
	said := journal.String()
	mu.Unlock()
	if n := strings.Count(said, "круг пропущен"); n != 1 {
		t.Fatalf("про пропущенный круг сказано %d раз, жду один раз на полосу отставания: %s", n, said)
	}
}

// Устаревший ответ с тем же отпечатком файла отдаётся сразу, а свежий
// поднимается один раз в фоне: под нагрузкой экран обязан отвечать старой
// строкой, а не висеть до перезапуска демона.
func TestBoardStaleServedWhileRefreshing(t *testing.T) {
	e := newTestEnv(t)
	now := time.Now()
	e.s.now = func() time.Time { return now }
	first := filepath.Join(e.home, "быстрый.log")
	writeScript(t, e.bin, "taskctl", countingScript(first, fmt.Sprintf("echo '%s'", boardFixtureJSON)))
	if raw, err := e.s.projectBoard(e.proj); err != nil || !strings.Contains(string(raw), "XR-005") {
		t.Fatalf("первый ответ доски %s (%v)", raw, err)
	}

	slow := filepath.Join(e.home, "медленный.log")
	gofile := filepath.Join(e.home, "отмашка")
	writeScript(t, e.bin, "taskctl", blockingScript(slow, gofile, fmt.Sprintf("echo '%s'", boardFixtureJSON)))
	// Потолок памяти на доску 10 секунд (cache.go, boardTTL).
	e.advance(11 * time.Second)

	got := make(chan json.RawMessage, 1)
	go func() {
		raw, _ := e.s.projectBoard(e.proj)
		got <- raw
	}()
	select {
	case raw := <-got:
		if !strings.Contains(string(raw), "XR-005") {
			t.Fatalf("устаревший ответ пришёл пустым: %s", raw)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("запрос встал за устаревшим ответом: экран под нагрузкой ждёт taskctl вместо старой строки")
	}
	// Свежий ответ поднялся в фоне, и второй запрос по той же доске второго
	// ребёнка не заводит.
	awaitLoad(t, e.s, 1, 0)
	if raw, err := e.s.projectBoard(e.proj); err != nil || !strings.Contains(string(raw), "XR-005") {
		t.Fatalf("второй запрос под нагрузкой: %s (%v)", raw, err)
	}
	release(t, gofile)
	awaitLoad(t, e.s, 0, 0)
	if n := calls(t, slow); n != 1 {
		t.Fatalf("фоновый опрос стоил %d запусков taskctl, жду один", n)
	}
}

// Общий на процесс потолок держит сумму детей по всем деревьям: одиночный полёт
// считает по дереву, а машина считает по себе. Очередь сверх потолка ждёт, а не
// плодит процессы.
func TestTaskctlChildrenCapped(t *testing.T) {
	e := newTestEnv(t)
	const extra = 2
	dirs := []string{e.proj}
	for i := 0; i < taskctlLimit+extra-1; i++ {
		dir := filepath.Join(e.home, "projects", fmt.Sprintf("proj%d", i))
		mkProject(t, dir)
		dirs = append(dirs, dir)
	}
	log := filepath.Join(e.home, "taskctl.log")
	gofile := filepath.Join(e.home, "отмашка")
	writeScript(t, e.bin, "taskctl", blockingScript(log, gofile, fmt.Sprintf("echo '%s'", boardFixtureJSON)))

	var wg sync.WaitGroup
	for _, dir := range dirs {
		wg.Add(1)
		go func(dir string) {
			defer wg.Done()
			e.s.projectBoard(dir)
		}(dir)
	}
	awaitLoad(t, e.s, taskctlLimit, extra)
	release(t, gofile)
	wg.Wait()
	if n := calls(t, log); n != len(dirs) {
		t.Fatalf("%d деревьев стоили %d запусков taskctl, жду по одному на дерево", len(dirs), n)
	}
}

// Отставание опроса видно снаружи: /healthz печатает живых детей taskctl и
// ждущих запросов. Иначе про толпу за ключом узнают по нагрузке машины.
func TestHealthzShowsBoardLoad(t *testing.T) {
	e := newTestEnv(t)
	log := filepath.Join(e.home, "taskctl.log")
	gofile := filepath.Join(e.home, "отмашка")
	writeScript(t, e.bin, "taskctl", blockingScript(log, gofile, fmt.Sprintf("echo '%s'", boardFixtureJSON)))
	c := e.loggedClient(t)

	const crowd = 3
	var wg sync.WaitGroup
	for i := 0; i < crowd; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			e.s.projectBoard(e.proj)
		}()
	}
	awaitLoad(t, e.s, 1, crowd-1)

	code, text := getStatus(t, c, e.srv.URL+"/healthz")
	if code != http.StatusOK {
		t.Fatalf("/healthz: %d %s", code, text)
	}
	var health struct {
		Live    int `json:"taskctl_live"`
		Waiting int `json:"taskctl_waiting"`
	}
	if err := json.Unmarshal([]byte(text), &health); err != nil {
		t.Fatalf("/healthz не разобрался: %v (%s)", err, text)
	}
	if health.Live != 1 || health.Waiting != crowd-1 {
		t.Fatalf("/healthz сказал живых %d и ждущих %d, а живёт один опрос и ждут %d: отставание снаружи не видно",
			health.Live, health.Waiting, crowd-1)
	}
	release(t, gofile)
	wg.Wait()
}
