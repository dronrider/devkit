package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
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

// Отсутствие обоих источников называется словами: ни журнала оболочки, ни
// файла цели, и это другое состояние, чем молчащий цикл.
func TestGoalLogMissingNamed(t *testing.T) {
	e := newTestEnv(t)
	c := e.loggedClient(t)
	resp := doReq(t, c, "GET", e.srv.URL+"/api/projects/demo/goals/XR-100/log", "")
	text := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("журнал без файла: %d %s", resp.StatusCode, text)
	}
	for _, want := range []string{`"exists":false`, "журнала", "не гонялась ни оболочкой goal-run, ни живым чатом"} {
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
	if event, data := sseNext(t, r); event != "source" || !strings.Contains(data, "goal-XR-100.log") {
		t.Fatalf("источник не назван: event=%q data=%q", event, data)
	}
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
		{"журнал", func(w http.ResponseWriter, r *http.Request) {
			srv.streamGoalLog(w, r, logPath, journalSources{id: "XR-1", logName: "goal-XR-1.log"})
		}},
		{"транскрипт", func(w http.ResponseWriter, r *http.Request) { srv.streamSession(w, r, "aaa-1", trPath, nil) }},
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
	if event, data := sseNext(t, r); event != "source" || !strings.Contains(data, "файла цели") {
		t.Fatalf("источник не назван: event=%q data=%q", event, data)
	}
	event, data := sseNext(t, r)
	if event != "note" || !strings.Contains(data, "не гонялась ни оболочкой") {
		t.Fatalf("пустота без имени: event=%q data=%q", event, data)
	}
	writeGoalLog(t, e.proj, "XR-100", goalLogFixture[:1])
	// Журнал оболочки завёлся при открытом экране: подпись источника меняется
	// и дальше строки идут оттуда.
	if event, data := sseNext(t, r); event != "source" || !strings.Contains(data, "goal-XR-100.log") {
		t.Fatalf("родившийся журнал не назван источником: event=%q data=%q", event, data)
	}
	if _, data := sseNext(t, r); data != goalLogFixture[0] {
		t.Fatalf("родившийся журнал не подхвачен: %q", data)
	}
}

// writeGoalDoc кладёт фикстурный файл цели: у цели, которую ведёт живой чат,
// журнала оболочки нет, а ход витков лежит в её разделе «Журнал».
func writeGoalDoc(t *testing.T, proj, id, body string) string {
	t.Helper()
	dir := filepath.Join(proj, "docs", "tasks")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, id+".md")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

const goalDocFixture = `# XR-100: Цель: пробный цикл

## Журнал

Строки витков и снимков квоты допишет цикл.
- снимок 2026-08-09T22:53: week_all 83%, week_max 61%
- 2026-08-09, нарезка: 9 задач, порядок по dep; continue

## Итог

- сюда цикл не пишет
`

var goalDocLines = []string{
	"снимок 2026-08-09T22:53: week_all 83%, week_max 61%",
	"2026-08-09, нарезка: 9 задач, порядок по dep; continue",
}

// Журнал цели, которую ведёт живой чат: файла .devkit/goal-XR-100.log нет, и
// строки приходят из раздела «Журнал» файла цели с подписью источника. Проза
// раздела и строки соседних разделов в журнал не едут.
func TestGoalLogFromGoalDoc(t *testing.T) {
	e := newTestEnv(t)
	writeGoalDoc(t, e.proj, "XR-100", goalDocFixture)
	c := e.loggedClient(t)

	resp := doReq(t, c, "GET", e.srv.URL+"/api/projects/demo/goals/XR-100/log", "")
	var got struct {
		Exists bool     `json:"exists"`
		Source string   `json:"source"`
		Sign   string   `json:"source_note"`
		Lines  []string `json:"lines"`
	}
	if err := json.Unmarshal([]byte(body(t, resp)), &got); err != nil {
		t.Fatal(err)
	}
	if !got.Exists || got.Source != "goal-file" {
		t.Fatalf("журнал из файла цели не отдан: %+v", got)
	}
	if !strings.Contains(got.Sign, "docs/tasks/XR-100.md") {
		t.Errorf("подписи источника нет: %q", got.Sign)
	}
	if !reflect.DeepEqual(got.Lines, goalDocLines) {
		t.Errorf("строки раздела:\n%v\nожидал:\n%v", got.Lines, goalDocLines)
	}
}

// Журнал оболочки старше файла цели: пока goal-<ID>.log есть, читается он, а
// раздел файла цели остаётся вторым источником.
func TestGoalLogPrefersShellLog(t *testing.T) {
	e := newTestEnv(t)
	writeGoalDoc(t, e.proj, "XR-100", goalDocFixture)
	writeGoalLog(t, e.proj, "XR-100", goalLogFixture)
	c := e.loggedClient(t)

	text := body(t, doReq(t, c, "GET", e.srv.URL+"/api/projects/demo/goals/XR-100/log", ""))
	if !strings.Contains(text, `"source":"goal-log"`) || !strings.Contains(text, goalLogFixture[0]) {
		t.Fatalf("журнал оболочки не в приоритете: %s", text)
	}
}

// Пустота различима тремя причинами: файла цели нет вовсе, раздела «Журнал» в
// нём нет, раздел заведён и пуст. Каждая называется своими словами.
func TestGoalLogEmptyKinds(t *testing.T) {
	cases := []struct {
		name string
		doc  string
		want string
	}{
		{"без файла цели", "", "не гонялась ни оболочкой goal-run, ни живым чатом"},
		{"без раздела", "# XR-100: Цель: пробный цикл\n\n## Итог\n\n- пусто\n", "нет раздела «Журнал»"},
		{"раздел пуст", "# XR-100: Цель: пробный цикл\n\n## Журнал\n\nСтроки допишет цикл.\n", "пуст"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newTestEnv(t)
			if tc.doc != "" {
				writeGoalDoc(t, e.proj, "XR-100", tc.doc)
			}
			text := body(t, doReq(t, e.loggedClient(t), "GET", e.srv.URL+"/api/projects/demo/goals/XR-100/log", ""))
			if !strings.Contains(text, `"exists":false`) || !strings.Contains(text, tc.want) {
				t.Fatalf("пустота без имени: %s", text)
			}
		})
	}
}

// SSE журнала из файла цели: хвост раздела приходит сразу с подписью
// источника, дописанная витком строка доезжает живым дострением по правке
// файла, а строки прочих разделов в поток не попадают.
func TestGoalLogStreamGoalDocAppends(t *testing.T) {
	e := newTestEnv(t)
	old := tailPoll
	tailPoll = 10 * time.Millisecond
	t.Cleanup(func() { tailPoll = old })
	path := writeGoalDoc(t, e.proj, "XR-100", goalDocFixture)
	c := e.loggedClient(t)

	r, done := sseClient(t, c, e.srv.URL+"/api/projects/demo/goals/XR-100/log?stream=1")
	defer done()
	if event, data := sseNext(t, r); event != "source" || !strings.Contains(data, "docs/tasks/XR-100.md") {
		t.Fatalf("источник не назван: event=%q data=%q", event, data)
	}
	for i, want := range goalDocLines {
		if _, data := sseNext(t, r); data != want {
			t.Fatalf("строка %d хвоста: %q, ожидал %q", i, data, want)
		}
	}
	next := "2026-08-10, виток 2: пачка DK-215 и DK-216 слита; continue"
	doc := strings.Replace(goalDocFixture, "\n\n## Итог", "\n- "+next+"\n\n## Итог", 1)
	if err := os.WriteFile(path, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, data := sseNext(t, r); data != next {
		t.Fatalf("живое дострение по правке файла цели: %q", data)
	}
}

// journalBoardJSON это доска с одной целью XR-100 и заданной ссылкой на её
// файл: сито ссылки проверяется только настоящей ссылкой, прочерк ведёт на
// путь по умолчанию.
func journalBoardJSON(link string) string {
	return `{"prefix":"XR","sections":[{"key":"in-progress","title":"In progress","rows":[` +
		`{"id":"XR-100","title":"Цель: пробный цикл","type":"task","p":"P2","r":41,` +
		`"r_parts":[25,9,3,0,4],"cost":"XL","link":"` + link + `"}]}]}`
}

// writeDocAt кладёт файл цели по произвольному пути, в том числе мимо
// docs/tasks и мимо самого проекта.
func writeDocAt(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Ссылка со строки доски ведёт к файлу цели, но только относительная и без
// выхода вверх: доска своя, а собирать по ней путь куда угодно незачем.
// Абсолютная ссылка и ссылка через ../ отбиваются на docs/tasks/<ID>.md, и
// журнал читается оттуда, а не из указанного ссылкой файла.
func TestGoalDocLinkSieve(t *testing.T) {
	t.Run("относительная ссылка подхватывается", func(t *testing.T) {
		e := newTestEnv(t)
		writeScript(t, e.bin, "taskctl", fmt.Sprintf("echo '%s'", journalBoardJSON("goals/XR-100.md")))
		writeDocAt(t, filepath.Join(e.proj, "docs", "goals", "XR-100.md"), goalDocFixture)

		text := body(t, doReq(t, e.loggedClient(t), "GET", e.srv.URL+"/api/projects/demo/goals/XR-100/log", ""))
		if !strings.Contains(text, "docs/goals/XR-100.md") || !strings.Contains(text, goalDocLines[0]) {
			t.Fatalf("ссылка строки доски не подхвачена: %s", text)
		}
	})

	// Файл цели ложится там, куда привела бы неотбитая ссылка: со снятым ситом
	// журнал прочитался бы оттуда, и подмена видна строками.
	for _, tc := range []struct {
		name, link string
		at         func(proj string) string
	}{
		{"абсолютная отбивается", "/goals/XR-100.md",
			func(proj string) string { return filepath.Join(proj, "docs", "goals", "XR-100.md") }},
		{"выход вверх отбивается", "../XR-100.md",
			func(proj string) string { return filepath.Join(proj, "XR-100.md") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newTestEnv(t)
			writeScript(t, e.bin, "taskctl", fmt.Sprintf("echo '%s'", journalBoardJSON(tc.link)))
			writeDocAt(t, tc.at(e.proj), goalDocFixture)

			text := body(t, doReq(t, e.loggedClient(t), "GET", e.srv.URL+"/api/projects/demo/goals/XR-100/log", ""))
			if strings.Contains(text, goalDocLines[0]) {
				t.Fatalf("журнал прочитан по ссылке мимо docs/tasks: %s", text)
			}
			if !strings.Contains(text, `"exists":false`) || !strings.Contains(text, "docs/tasks/XR-100.md") {
				t.Fatalf("путь не отбит на docs/tasks/XR-100.md: %s", text)
			}
		})
	}
}

// Живой формат ссылки строки доски это markdown целиком
// («[tasks/DK-112.md](tasks/DK-112.md)», так её ставит taskctl), и путём
// работает адрес в скобках, а не вся разметка. Пути в доске относительны
// docs/, как их считает и линтер taskctl.
func TestGoalDocMarkdownLink(t *testing.T) {
	t.Run("обычное место через разметку", func(t *testing.T) {
		e := newTestEnv(t)
		writeScript(t, e.bin, "taskctl",
			fmt.Sprintf("echo '%s'", journalBoardJSON("[tasks/XR-100.md](tasks/XR-100.md)")))
		writeGoalDoc(t, e.proj, "XR-100", goalDocFixture)

		text := body(t, doReq(t, e.loggedClient(t), "GET", e.srv.URL+"/api/projects/demo/goals/XR-100/log", ""))
		if !strings.Contains(text, goalDocLines[0]) || !strings.Contains(text, `"source":"goal-file"`) {
			t.Fatalf("разметка ссылки принята за путь: %s", text)
		}
	})

	t.Run("ссылка мимо обычного места", func(t *testing.T) {
		e := newTestEnv(t)
		writeScript(t, e.bin, "taskctl",
			fmt.Sprintf("echo '%s'", journalBoardJSON("[goals/XR-100.md](goals/XR-100.md)")))
		writeDocAt(t, filepath.Join(e.proj, "docs", "goals", "XR-100.md"), goalDocFixture)

		text := body(t, doReq(t, e.loggedClient(t), "GET", e.srv.URL+"/api/projects/demo/goals/XR-100/log", ""))
		if !strings.Contains(text, "docs/goals/XR-100.md") || !strings.Contains(text, goalDocLines[0]) {
			t.Fatalf("адрес из разметки не подхвачен: %s", text)
		}
	})
}

// Ссылка, ведущая на несуществующий файл, откатывается на docs/tasks/<ID>.md:
// колонка доски бывает и устаревшей, а файл цели лежит на обычном месте, и
// читать его честнее, чем отвечать «журнала нет».
func TestGoalDocLinkFallback(t *testing.T) {
	e := newTestEnv(t)
	writeScript(t, e.bin, "taskctl",
		fmt.Sprintf("echo '%s'", journalBoardJSON("[goals/XR-100.md](goals/XR-100.md)")))
	writeGoalDoc(t, e.proj, "XR-100", goalDocFixture)

	text := body(t, doReq(t, e.loggedClient(t), "GET", e.srv.URL+"/api/projects/demo/goals/XR-100/log", ""))
	if !strings.Contains(text, "docs/tasks/XR-100.md") || !strings.Contains(text, goalDocLines[0]) {
		t.Fatalf("отката на обычное место нет: %s", text)
	}
}
