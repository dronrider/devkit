package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// Синтетический транскрипт: служебные записи, реплика человека строкой,
// размышления, текст с вызовом инструмента, tool_result, битая строка,
// ветка субагента. Ожидания ленты выводятся из него поле в поле.
const transcriptFixture = `{"type":"queue-operation","operation":"enqueue","timestamp":"2026-08-10T10:00:00.000Z"}
{"type":"user","message":{"role":"user","content":"возьми задачу XR-005 в работу"},"timestamp":"2026-08-10T10:00:01.000Z","gitBranch":"main"}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"thinking","thinking":"куда смотреть"}]},"timestamp":"2026-08-10T10:00:02.000Z"}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Беру XR-005, смотрю доску."},{"type":"tool_use","name":"Bash","input":{"command":"taskctl list | head -5","description":"Показать доску"}}]},"timestamp":"2026-08-10T10:00:03.000Z"}
{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"x","content":"ok"}]},"timestamp":"2026-08-10T10:00:04.000Z"}
битая строка не json
{"type":"assistant","isSidechain":true,"message":{"role":"assistant","content":[{"type":"text","text":"реплика субагента"}]},"timestamp":"2026-08-10T10:00:05.000Z"}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Доска прочитана."}]},"timestamp":"2026-08-10T10:00:06.000Z"}
`

var transcriptWant = []reply{
	{Seq: 0, Role: "user", Time: "2026-08-10T10:00:01.000Z", Text: "возьми задачу XR-005 в работу"},
	{Seq: 1, Role: "thinking", Time: "2026-08-10T10:00:02.000Z"},
	{Seq: 2, Role: "assistant", Time: "2026-08-10T10:00:03.000Z", Text: "Беру XR-005, смотрю доску."},
	{Seq: 3, Role: "tool", Time: "2026-08-10T10:00:03.000Z", Tool: "Bash", Note: "taskctl list | head -5"},
	{Seq: 4, Role: "assistant", Time: "2026-08-10T10:00:06.000Z", Text: "Доска прочитана."},
}

// writeSession кладёт транскрипт в каталог ~/.claude/projects по раскладке
// Claude Code; dirSuffix изображает каталог бокового дерева задачи.
func writeSession(t *testing.T, home, projPath, dirSuffix, sid, content string, mtime time.Time) string {
	t.Helper()
	dir := filepath.Join(home, ".claude", "projects", claudeDirName(projPath)+dirSuffix)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, sid+".jsonl")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatal(err)
	}
	return path
}

// Путь проекта кодируется в имя каталога как у Claude Code: всё, что не
// буква, не цифра и не дефис, становится дефисом.
func TestClaudeDirName(t *testing.T) {
	got := claudeDirName("/Users/x/my.proj_v2")
	if got != "-Users-x-my-proj-v2" {
		t.Fatalf("кодировка пути: %q", got)
	}
}

// Список сессий: свежие сверху по mtime, ветка и первая человеческая реплика
// из шапки, служебная вставка в угловых скобках репликой не считается,
// сессия из каталога бокового дерева попадает в список.
func TestSessionsList(t *testing.T) {
	e := newTestEnv(t)
	older := time.Date(2026, 8, 10, 9, 0, 0, 0, time.UTC)
	newer := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	writeSession(t, e.home, e.proj, "", "aaa-1", transcriptFixture, older)
	tagged := `{"type":"user","message":{"role":"user","content":[{"type":"text","text":"<ide_opened_file>шум</ide_opened_file>"},{"type":"text","text":"Выполни XR-007"}]},"timestamp":"2026-08-10T11:00:00.000Z","gitBranch":"dk-219"}` + "\n"
	writeSession(t, e.home, e.proj, "-dk-5", "bbb-2", tagged, newer)

	c := e.loggedClient(t)
	resp := doReq(t, c, "GET", e.srv.URL+"/api/projects/demo/sessions", "")
	var got struct {
		Sessions []sessionInfo `json:"sessions"`
	}
	if err := json.Unmarshal([]byte(body(t, resp)), &got); err != nil {
		t.Fatal(err)
	}
	want := []sessionInfo{
		{ID: "bbb-2", Mtime: "2026-08-10T12:00:00Z", Branch: "dk-219", First: "Выполни XR-007"},
		{ID: "aaa-1", Mtime: "2026-08-10T09:00:00Z", Branch: "main", First: "возьми задачу XR-005 в работу"},
	}
	if !reflect.DeepEqual(got.Sessions, want) {
		t.Errorf("список сессий:\n%+v\nожидал:\n%+v", got.Sessions, want)
	}
}

// Пустота различима: без единого транскрипта список пуст и причина названа
// словами, а не пустым экраном.
func TestSessionsNoneNamed(t *testing.T) {
	e := newTestEnv(t)
	c := e.loggedClient(t)
	resp := doReq(t, c, "GET", e.srv.URL+"/api/projects/demo/sessions", "")
	text := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("сессии без каталога: %d %s", resp.StatusCode, text)
	}
	for _, want := range []string{`"sessions":[]`, "транскриптов нет"} {
		if !strings.Contains(text, want) {
			t.Errorf("в ответе нет %q: %s", want, text)
		}
	}
}

// Лента реплик сверяется с фикстурой поле в поле (шаг 3 сценария DK-219):
// ход агента и его ответы на месте, служебное, битое и ветки субагентов
// выпали, вызов инструмента свёрнут в одну строку.
func TestSessionRepliesFieldByField(t *testing.T) {
	e := newTestEnv(t)
	writeSession(t, e.home, e.proj, "", "aaa-1", transcriptFixture, time.Now())
	c := e.loggedClient(t)
	resp := doReq(t, c, "GET", e.srv.URL+"/api/projects/demo/sessions/aaa-1", "")
	var got struct {
		Session string  `json:"session"`
		Total   int     `json:"total"`
		Items   []reply `json:"items"`
	}
	if err := json.Unmarshal([]byte(body(t, resp)), &got); err != nil {
		t.Fatal(err)
	}
	if got.Session != "aaa-1" || got.Total != len(transcriptWant) {
		t.Fatalf("шапка ленты: %+v", got)
	}
	if !reflect.DeepEqual(got.Items, transcriptWant) {
		t.Errorf("лента разошлась с фикстурой:\n%+v\nожидал:\n%+v", got.Items, transcriptWant)
	}
}

// Пагинация назад: ?n= режет хвост, ?before= отдаёт реплики до курсора, до
// начала ленты доходит без дыр.
func TestSessionPagination(t *testing.T) {
	e := newTestEnv(t)
	writeSession(t, e.home, e.proj, "", "aaa-1", transcriptFixture, time.Now())
	c := e.loggedClient(t)

	// page ходит за страницей свежей структурой: повторный Unmarshal в тот же
	// срез оставил бы поля прежних элементов.
	page := func(query string) (int, []reply) {
		resp := doReq(t, c, "GET", e.srv.URL+"/api/projects/demo/sessions/aaa-1?"+query, "")
		var got struct {
			Total int     `json:"total"`
			Items []reply `json:"items"`
		}
		if err := json.Unmarshal([]byte(body(t, resp)), &got); err != nil {
			t.Fatal(err)
		}
		return got.Total, got.Items
	}
	total, items := page("n=2")
	if total != 5 || !reflect.DeepEqual(items, transcriptWant[3:]) {
		t.Fatalf("хвост n=2: total=%d %+v", total, items)
	}
	if _, items = page("n=2&before=3"); !reflect.DeepEqual(items, transcriptWant[1:3]) {
		t.Fatalf("страница before=3: %+v", items)
	}
	if _, items = page("n=2&before=1"); !reflect.DeepEqual(items, transcriptWant[:1]) {
		t.Fatalf("страница before=1: %+v", items)
	}
}

// Транскрипта нет, и это 404 со словами, а не пустая лента.
func TestSessionMissingNamed(t *testing.T) {
	e := newTestEnv(t)
	c := e.loggedClient(t)
	resp := doReq(t, c, "GET", e.srv.URL+"/api/projects/demo/sessions/no-such", "")
	text := body(t, resp)
	if resp.StatusCode != http.StatusNotFound || !strings.Contains(text, "транскрипта no-such нет") {
		t.Fatalf("несуществующая сессия: %d %s", resp.StatusCode, text)
	}
}

// Обход путей через sid отбивается до склейки пути: {sid} приходит из URL с
// раскодированными %2F, мультиплексор их пропускает, и без сита sessionIDRe
// склейка sessionPath отдала бы чужой файл с диска (замечание ревью DK-219).
func TestSessionTraversalBlocked(t *testing.T) {
	e := newTestEnv(t)
	secret := `{"type":"user","message":{"role":"user","content":"секретное содержимое"},"timestamp":"2026-08-10T10:00:01.000Z"}` + "\n"
	if err := os.WriteFile(filepath.Join(e.home, "secret.jsonl"), []byte(secret), 0o644); err != nil {
		t.Fatal(err)
	}
	c := e.loggedClient(t)
	resp := doReq(t, c, "GET", e.srv.URL+"/api/projects/demo/sessions/..%2F..%2F..%2Fsecret", "")
	text := body(t, resp)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("обход путей: %d %s, ожидал 400", resp.StatusCode, text)
	}
	if strings.Contains(text, "секретное содержимое") {
		t.Errorf("чужой файл утёк наружу: %s", text)
	}
}

// Пустая лента в потоке называется первым событием note, а не молчит: тихий
// стрим неотличим от оборвавшегося; дострение после note идёт как обычно.
func TestSessionStreamEmptyNamed(t *testing.T) {
	e := newTestEnv(t)
	old := tailPoll
	tailPoll = 10 * time.Millisecond
	t.Cleanup(func() { tailPoll = old })
	path := writeSession(t, e.home, e.proj, "", "aaa-1", "", time.Now())
	c := e.loggedClient(t)

	r, done := sseClient(t, c, e.srv.URL+"/api/projects/demo/sessions/aaa-1?stream=1")
	defer done()
	event, data := sseNext(t, r)
	if event != "note" || data != "в транскрипте пока нет реплик" {
		t.Fatalf("пустая лента без имени: event=%q data=%q", event, data)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	line := `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Готово."}]},"timestamp":"2026-08-10T10:00:07.000Z"}` + "\n"
	if _, err := f.WriteString(line); err != nil {
		t.Fatal(err)
	}
	f.Close()
	var item reply
	_, data = sseNext(t, r)
	if err := json.Unmarshal([]byte(data), &item); err != nil {
		t.Fatal(err)
	}
	want := reply{Seq: 0, Role: "assistant", Time: "2026-08-10T10:00:07.000Z", Text: "Готово."}
	if !reflect.DeepEqual(item, want) {
		t.Fatalf("дострение после note: %+v, ожидал %+v", item, want)
	}
}

// Экран транскрипта обязан слушать событие note и гасить кнопку «раньше»,
// когда раньше нечего показывать: держится грепом по статике, как слово
// «пауза» в тесте стопа. note слушают обе панели, журнал и транскрипт.
func TestStaticTranscriptEmptyHandled(t *testing.T) {
	text := readFile(t, filepath.Join("static", "app.js"))
	if strings.Count(text, `addEventListener("note"`) < 2 {
		t.Error("в static/app.js меньше двух слушателей note: пустой транскрипт останется пустой коробкой")
	}
	if !strings.Contains(text, "more.hidden") {
		t.Error("в static/app.js кнопка «раньше» не гаснет: мёртвая кнопка на пустой ленте")
	}
}

// SSE транскрипта: последние реплики приходят сразу, дописанная запись
// доезжает живым дострением с продолжением нумерации.
func TestSessionStreamAppends(t *testing.T) {
	e := newTestEnv(t)
	old := tailPoll
	tailPoll = 10 * time.Millisecond
	t.Cleanup(func() { tailPoll = old })
	path := writeSession(t, e.home, e.proj, "", "aaa-1", transcriptFixture, time.Now())
	c := e.loggedClient(t)

	r, done := sseClient(t, c, e.srv.URL+"/api/projects/demo/sessions/aaa-1?stream=1")
	defer done()
	for i, want := range transcriptWant {
		var item reply
		_, data := sseNext(t, r)
		if err := json.Unmarshal([]byte(data), &item); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(item, want) {
			t.Fatalf("событие %d: %+v, ожидал %+v", i, item, want)
		}
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	line := `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Готово."}]},"timestamp":"2026-08-10T10:00:07.000Z"}` + "\n"
	if _, err := f.WriteString(line); err != nil {
		t.Fatal(err)
	}
	f.Close()
	var item reply
	_, data := sseNext(t, r)
	if err := json.Unmarshal([]byte(data), &item); err != nil {
		t.Fatal(err)
	}
	want := reply{Seq: 5, Role: "assistant", Time: "2026-08-10T10:00:07.000Z", Text: "Готово."}
	if !reflect.DeepEqual(item, want) {
		t.Fatalf("живое дострение: %+v, ожидал %+v", item, want)
	}
}
