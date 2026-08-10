package main

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// writeGoalLog кладёт фикстурный журнал цикла в .devkit проекта.
func writeGoalLog(t *testing.T, proj, id string, lines []string) string {
	t.Helper()
	dir := filepath.Join(proj, ".devkit")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "goal-"+id+".log")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

var goalLogFixture = []string{
	"2026-08-09T22:53:41 цикл цели XR-100 начат, pid 59226",
	"2026-08-09T22:53:41 виток 1 поднят, ход витка ниже",
	"2026-08-09T23:05:02 виток 1 маркер continue код 0 записей в журнале 2 -> 4",
}

// API журнала отдаёт хвост фикстурного журнала строка в строку (шаг 2
// сценария DK-219); ?n= режет хвост.
func TestGoalLogTail(t *testing.T) {
	e := newTestEnv(t)
	writeGoalLog(t, e.proj, "XR-100", goalLogFixture)
	c := e.loggedClient(t)

	resp := doReq(t, c, "GET", e.srv.URL+"/api/projects/demo/goals/XR-100/log", "")
	var got struct {
		Goal   string   `json:"goal"`
		Exists bool     `json:"exists"`
		Lines  []string `json:"lines"`
	}
	if err := json.Unmarshal([]byte(body(t, resp)), &got); err != nil {
		t.Fatal(err)
	}
	if !got.Exists || got.Goal != "XR-100" {
		t.Fatalf("журнал не нашёлся: %+v", got)
	}
	if !reflect.DeepEqual(got.Lines, goalLogFixture) {
		t.Errorf("хвост разошёлся с фикстурой:\n%v\n%v", got.Lines, goalLogFixture)
	}

	resp = doReq(t, c, "GET", e.srv.URL+"/api/projects/demo/goals/XR-100/log?n=1", "")
	if err := json.Unmarshal([]byte(body(t, resp)), &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Lines, goalLogFixture[2:]) {
		t.Errorf("хвост n=1: %v", got.Lines)
	}
}

// Отсутствие журнала называется словами: цель не гонялась оболочкой, и это
// другое состояние, чем молчащий цикл.
func TestGoalLogMissingNamed(t *testing.T) {
	e := newTestEnv(t)
	c := e.loggedClient(t)
	resp := doReq(t, c, "GET", e.srv.URL+"/api/projects/demo/goals/XR-100/log", "")
	text := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("журнал без файла: %d %s", resp.StatusCode, text)
	}
	for _, want := range []string{`"exists":false`, "журнала", "не гонялась оболочкой goal-run"} {
		if !strings.Contains(text, want) {
			t.Errorf("в ответе нет %q: %s", want, text)
		}
	}
}

// ID в пути обязан быть похож на ID задачи: путь до файла из него собирается
// склейкой, и произвольная строка туда не едет.
func TestGoalLogBadID(t *testing.T) {
	e := newTestEnv(t)
	c := e.loggedClient(t)
	resp := doReq(t, c, "GET", e.srv.URL+"/api/projects/demo/goals/..%2F..%2Fetc/log", "")
	text := body(t, resp)
	if resp.StatusCode != http.StatusBadRequest && resp.StatusCode != http.StatusNotFound {
		t.Fatalf("кривой ID: %d %s, ожидал 400 либо 404 от маршрутизатора", resp.StatusCode, text)
	}
}

// sseClient открывает поток событий с общим сроком: тест с SSE обязан
// закончиться сам, а не висеть на вечном чтении.
func sseClient(t *testing.T, c *http.Client, url string) (*bufio.Reader, func()) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	resp, err := c.Do(req)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		cancel()
		t.Fatalf("поток с типом %q", ct)
	}
	return bufio.NewReader(resp.Body), func() { cancel(); resp.Body.Close() }
}

// sseNext читает одно событие потока: имя (пусто у обычного) и данные.
func sseNext(t *testing.T, r *bufio.Reader) (event, data string) {
	t.Helper()
	var dataLines []string
	for {
		ln, err := r.ReadString('\n')
		if err != nil {
			t.Fatalf("поток оборвался: %v (событие не пришло в срок)", err)
		}
		ln = strings.TrimRight(ln, "\n")
		switch {
		case strings.HasPrefix(ln, "event: "):
			event = strings.TrimPrefix(ln, "event: ")
		case strings.HasPrefix(ln, "data: "):
			dataLines = append(dataLines, strings.TrimPrefix(ln, "data: "))
		case ln == "" && len(dataLines) > 0:
			return event, strings.Join(dataLines, "\n")
		}
	}
}

// SSE журнала: хвост приходит сразу, дописанная строка доезжает живым
// дострением, недописанная (без перевода строки) не отдаётся раньше времени.
func TestGoalLogStreamAppends(t *testing.T) {
	e := newTestEnv(t)
	old := tailPoll
	tailPoll = 10 * time.Millisecond
	t.Cleanup(func() { tailPoll = old })
	path := writeGoalLog(t, e.proj, "XR-100", goalLogFixture)
	c := e.loggedClient(t)

	r, done := sseClient(t, c, e.srv.URL+"/api/projects/demo/goals/XR-100/log?stream=1")
	defer done()
	for i, want := range goalLogFixture {
		if _, data := sseNext(t, r); data != want {
			t.Fatalf("строка %d хвоста: %q, ожидал %q", i, data, want)
		}
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("2026-08-09T23:05:03 виток 2 поднят, ход витка ниже\nнедописанная"); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if _, data := sseNext(t, r); data != "2026-08-09T23:05:03 виток 2 поднят, ход витка ниже" {
		t.Fatalf("живое дострение: %q", data)
	}
}

// Поток живёт ровно до ухода клиента: отменённый контекст запроса выводит
// цикл дострения за пару секунд, а не оставляет горутину тикать вечно.
// Мутация, на которой тест обязан краснеть адресно: снять case
// <-r.Context().Done() в цикле потока (замечание ревью DK-219; раньше её
// ловил только тайм-аут всего прогона на зависшем Close сервера).
func TestStreamsExitOnClientGone(t *testing.T) {
	old := tailPoll
	tailPoll = 10 * time.Millisecond
	t.Cleanup(func() { tailPoll = old })
	dir := t.TempDir()
	logPath := filepath.Join(dir, "goal-XR-1.log")
	if err := os.WriteFile(logPath, []byte("2026-08-09T22:53:41 строка\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	trPath := filepath.Join(dir, "s.jsonl")
	if err := os.WriteFile(trPath, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	srv := &server{}
	streams := []struct {
		name string
		run  func(w http.ResponseWriter, r *http.Request)
	}{
		{"журнал", func(w http.ResponseWriter, r *http.Request) { srv.streamGoalLog(w, r, logPath, "нет") }},
		{"транскрипт", func(w http.ResponseWriter, r *http.Request) { srv.streamSession(w, r, trPath) }},
	}
	for _, s := range streams {
		ctx, cancel := context.WithCancel(context.Background())
		req := httptest.NewRequest("GET", "/?stream=1", nil).WithContext(ctx)
		rec := httptest.NewRecorder()
		done := make(chan struct{})
		go func() {
			s.run(rec, req)
			close(done)
		}()
		// Потоку хватает пары тиков отправить хвост, потом клиент уходит.
		time.Sleep(30 * time.Millisecond)
		cancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatalf("%s: поток не вышел после ухода клиента", s.name)
		}
	}
}

// SSE без журнала называет пустоту событием note, а появление файла
// подхватывает тот же поток.
func TestGoalLogStreamMissingThenBorn(t *testing.T) {
	e := newTestEnv(t)
	old := tailPoll
	tailPoll = 10 * time.Millisecond
	t.Cleanup(func() { tailPoll = old })
	c := e.loggedClient(t)

	r, done := sseClient(t, c, e.srv.URL+"/api/projects/demo/goals/XR-100/log?stream=1")
	defer done()
	event, data := sseNext(t, r)
	if event != "note" || !strings.Contains(data, "не гонялась оболочкой") {
		t.Fatalf("пустота без имени: event=%q data=%q", event, data)
	}
	writeGoalLog(t, e.proj, "XR-100", goalLogFixture[:1])
	if _, data := sseNext(t, r); data != goalLogFixture[0] {
		t.Fatalf("родившийся журнал не подхвачен: %q", data)
	}
}
