package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dronrider/devkit/internal/chat"
)

// Полка ждущих (DK-696). Состояние ожидания у строки доски считалось и раньше,
// а собранного места у него не было: человек шёл в Blocked и сверял строки
// глазами. Тут проверяется само место: список всех ждущих машины, порядок по
// давности молчания, адрес разговора у каждой записи и причина отказа рядом со
// списком.

// Доска стенда: припаркованная вопросом строка в Blocked, работающая строка без
// ожидания и строка Backlog. Ждать тут должна ровно одна.
const shelfBoardJSON = `{"prefix":"XR","sections":[` +
	`{"key":"in-progress","title":"In progress","rows":[{"id":"XR-005","title":"Задача в работе","type":"task","p":"P2","r":30,"r_parts":[25,2,1,0,2],"cost":"-","link":"-"}]},` +
	`{"key":"check","title":"Check","rows":[]},` +
	`{"key":"backlog","title":"Backlog","rows":[{"id":"XR-002","title":"Верхняя","type":"bug","p":"P1","r":55,"r_parts":[50,0,0,5,0],"cost":"-","link":"-"}]},` +
	`{"key":"blocked","title":"Blocked","rows":[{"id":"XR-007","title":"Слияние встало","type":"task","p":"P1","r":40,"r_parts":[25,5,5,5,0],"cost":"-","link":"-","sect":"blocked","block":"вопрос: куда катить дальше"}]}]}`

// shelfEnv поднимает стенд с этой доской и часами стенда.
func shelfEnv(t *testing.T, now time.Time) (*testEnv, *http.Client) {
	t.Helper()
	e := newTestEnv(t)
	writeScript(t, e.bin, "taskctl", fmt.Sprintf("echo '%s'", shelfBoardJSON))
	writeScript(t, e.bin, "tmux", "exit 1")
	e.s.now = func() time.Time { return now }
	return e, e.loggedClient(t)
}

func getShelf(t *testing.T, e *testEnv, c *http.Client) (items []WaitItem, errs []string) {
	t.Helper()
	resp := doReq(t, c, "GET", e.srv.URL+"/api/waiting", "")
	text := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("полка ждущих: %d %s", resp.StatusCode, text)
	}
	var got struct {
		Items  []WaitItem `json:"items"`
		Errors []string   `json:"errors"`
	}
	if err := json.Unmarshal([]byte(text), &got); err != nil {
		t.Fatalf("ответ полки не разобрался: %v\n%s", err, text)
	}
	return got.Items, got.Errors
}

// Полка собирает и строку доски, и вопрос, за которым строки нет вовсе:
// припаркованная задача стоит в Blocked, а вопрос разговора о чужой задаче не
// стоит на доске ничем, и в прежнем месте ожидания его было не видно.
func TestWaitShelfCollectsRowsAndAsks(t *testing.T) {
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.Local)
	e, c := shelfEnv(t, now)
	writeAskFor(t, e.proj, "XR-9", "aaa-1", now.Add(20*time.Minute))

	items, errs := getShelf(t, e, c)
	if len(errs) != 0 {
		t.Fatalf("полка пожаловалась на живую доску: %v", errs)
	}
	if len(items) != 2 {
		t.Fatalf("на полке %d записей, ожидал две: %+v", len(items), items)
	}
	seen := map[string]WaitItem{}
	for _, it := range items {
		seen[it.ID] = it
		if it.Project != "demo" {
			t.Errorf("запись %s без проекта: %q", it.ID, it.Project)
		}
	}
	parked, ok := seen["XR-007"]
	if !ok {
		t.Fatalf("припаркованной строки на полке нет: %+v", items)
	}
	if parked.Waiting.Source != waitParked {
		t.Errorf("источник припаркованной строки %q, ожидал %q", parked.Waiting.Source, waitParked)
	}
	if len(parked.Waiting.Questions) == 0 || parked.Waiting.Questions[0] != "куда катить дальше" {
		t.Errorf("вопрос парковки на полку не приехал: %+v", parked.Waiting.Questions)
	}
	if parked.Title != "Слияние встало" {
		t.Errorf("заголовок строки на полке %q", parked.Title)
	}
	handed, ok := seen["XR-9"]
	if !ok {
		t.Fatalf("вопрос без строки доски на полку не попал: %+v", items)
	}
	if handed.Waiting.Source != waitAsk {
		t.Errorf("источник вопроса %q, ожидал %q", handed.Waiting.Source, waitAsk)
	}
	// Дорога до разговора у вопроса это ждущая сессия, а у парковки задача: сессии
	// за ней нет вовсе, и открывать человеку надо последний разговор строки.
	if handed.Addr != "aaa-1" {
		t.Errorf("адрес разговора у вопроса %q, ожидал сессию aaa-1", handed.Addr)
	}
	if parked.Addr != "XR-007" {
		t.Errorf("адрес разговора у парковки %q, ожидал задачу XR-007", parked.Addr)
	}
}

// Дольше всех ждущий стоит первым: полка отвечает на вопрос «кому отвечать
// сейчас». Запись без момента начала уходит в конец, нулём она встала бы во
// главе списка и оттеснила бы настоящий простой.
func TestWaitShelfOldestFirst(t *testing.T) {
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.Local)
	e, c := shelfEnv(t, now)
	writeAskFor(t, e.proj, "XR-9", "aaa-1", now.Add(20*time.Minute))
	writeAskFor(t, e.proj, "XR-8", "bbb-2", now.Add(20*time.Minute))
	// Признак кладётся файлом, и момент начала это его mtime: у XR-8 он
	// отодвигается в прошлое, значит и ждут по нему дольше.
	touchAsk(t, e.proj, "XR-8", now.Add(-30*time.Minute))

	items, _ := getShelf(t, e, c)
	if len(items) != 3 {
		t.Fatalf("на полке %d записей, ожидал три: %+v", len(items), items)
	}
	if items[0].ID != "XR-8" {
		t.Errorf("первым на полке %q, ожидал дольше всех ждущего XR-8", items[0].ID)
	}
	if items[1].ID != "XR-9" {
		t.Errorf("вторым на полке %q, ожидал XR-9", items[1].ID)
	}
	// Парковка своего момента начала не знает вовсе, и место ей в конце.
	if items[2].ID != "XR-007" {
		t.Errorf("последней на полке %q, ожидал парковку XR-007", items[2].ID)
	}
}

// Нечитаемая доска не выдаётся за тишину: пустая полка при упавшем taskctl
// говорила бы «никто не ждёт», и человек ушёл бы спокойным. Вопросы, которые
// доски не касаются, при этом приезжают своим ходом.
func TestWaitShelfNamesBoardFailure(t *testing.T) {
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.Local)
	e, c := shelfEnv(t, now)
	writeAskFor(t, e.proj, "XR-9", "aaa-1", now.Add(20*time.Minute))
	writeScript(t, e.bin, "taskctl", "echo 'доска не читается' >&2; exit 3")

	items, errs := getShelf(t, e, c)
	if len(errs) == 0 {
		t.Fatal("отказ доски на полке молчит")
	}
	if len(items) != 1 || items[0].ID != "XR-9" {
		t.Fatalf("вопрос мимо доски на полку не попал: %+v", items)
	}
}

// Просроченный признак ожидания за ждущего не выдаётся: снять его было некому,
// и строка на полке звала бы отвечать тому, кого уже нет.
func TestWaitShelfSkipsStaleAsk(t *testing.T) {
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.Local)
	e, c := shelfEnv(t, now)
	writeAskFor(t, e.proj, "XR-9", "aaa-1", now.Add(-time.Minute))

	items, _ := getShelf(t, e, c)
	for _, it := range items {
		if it.ID == "XR-9" {
			t.Fatalf("просроченный признак попал на полку: %+v", it)
		}
	}
}

// Вопрос своей задачи не двоится: строка доски и признак ожидания это один и
// тот же простой, и второй записью он звал бы отвечать дважды.
func TestWaitShelfKeepsOneRecordPerTask(t *testing.T) {
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.Local)
	e, c := shelfEnv(t, now)
	writeAskFor(t, e.proj, "XR-005", "aaa-1", now.Add(20*time.Minute))

	items, _ := getShelf(t, e, c)
	count := 0
	for _, it := range items {
		if it.ID == "XR-005" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("задача XR-005 стоит на полке %d раз: %+v", count, items)
	}
}

// touchAsk двигает время правки признака: момент начала ожидания берётся у
// самого файла, своего поля старта у признака нет.
func touchAsk(t *testing.T, tree, id string, at time.Time) {
	t.Helper()
	path := chat.AskPath(tree, chat.TaskName(id))
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatal(err)
	}
}

// Стенд фронта: место ждущих на экране. Проверка по тексту app.js тут не
// годится, разметку держал бы и поиск по строке, а предмет это собранная
// полка: число на кнопке шапки, состав строки, порядок сервера и дорога до
// разговора. Без node шаг пропускается: узел стенда, а не рабочей части.
func TestStaticWaitShelf(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node не найден: стенд полки ждущих пропущен")
	}
	out, err := exec.Command(node, filepath.Join("testdata", "poc_waitshelf.mjs"),
		filepath.Join("static", "app.js")).CombinedOutput()
	if err != nil {
		t.Fatalf("полка ждущих в шапке: %v\n%s", err, out)
	}
	t.Log(strings.TrimSpace(string(out)))
}

// idleShelf ставит на стенд повод «сессия ждёт ввода» по задаче XR-005 за час
// до часов стенда: реестр связывает сессию с задачей, журнал уведомителя несёт
// сам повод. Возвращает момент повода, от него считаются реплики.
func idleShelf(t *testing.T, e *testEnv, now time.Time, sid string) time.Time {
	t.Helper()
	at := now.Add(-time.Hour)
	writeBinds(t, e.home, bindRecord(e.home, at.Add(-10*time.Minute).Format(bindStamp), sid, "XR-005", bindOrder))
	writeNotifyLog(t, e.home, []string{idleLine(at.Format(bindStamp), sid[:8], idlePromptReason)})
	return at
}

// humanLine это реплика человека в транскрипте с меткой времени.
func humanLine(text string, at time.Time) string {
	return fmt.Sprintf(`{"type":"user","message":{"role":"user","content":%q},"timestamp":%q}`+"\n",
		text, at.UTC().Format(time.RFC3339))
}

func shelfHas(items []WaitItem, id string) bool {
	for _, it := range items {
		if it.ID == id {
			return true
		}
	}
	return false
}

// Разговор, убранный в архив, на полке не стоит: отвечать в нём некому, а
// снятый архив возвращает запись тем же чтением (живой случай DK-837 и DK-839,
// 2026-09-09).
func TestWaitShelfDropsArchivedChat(t *testing.T) {
	now := time.Date(2026, 9, 9, 18, 0, 0, 0, time.Local)
	e, c := shelfEnv(t, now)
	const sid = "aaaa1111-arch-id"
	idleShelf(t, e, now, sid)
	if items, _ := getShelf(t, e, c); !shelfHas(items, "XR-005") {
		t.Fatalf("повод из журнала до полки не доехал: %+v", items)
	}

	if err := e.s.chatStoreWrite(sid, chatStore{Archived: true}); err != nil {
		t.Fatal(err)
	}
	if items, _ := getShelf(t, e, c); shelfHas(items, "XR-005") {
		t.Fatalf("архивный разговор остался на полке: %+v", items)
	}

	if err := e.s.chatStoreWrite(sid, chatStore{Archived: false}); err != nil {
		t.Fatal(err)
	}
	if items, _ := getShelf(t, e, c); !shelfHas(items, "XR-005") {
		t.Fatalf("снятый архив не вернул запись на полку: %+v", items)
	}
}

// Повод «сессия ждёт ввода» гасит реплика человека позже момента, с которого
// сессия ждёт. Просмотр разговора не гасит: просмотр это не ответ, а сессия
// после просмотра стоит ровно так же.
func TestWaitShelfDropsAnsweredIdle(t *testing.T) {
	now := time.Date(2026, 9, 9, 18, 0, 0, 0, time.Local)
	e, c := shelfEnv(t, now)
	const sid = "aaaa1111-idle-id"
	at := idleShelf(t, e, now, sid)

	path := writeSession(t, e.home, e.proj, "", sid, humanLine("начинай", at.Add(-time.Hour)), at)
	if err := e.s.chatStoreWrite(sid, chatStore{Seen: at.Add(10 * time.Minute).Unix()}); err != nil {
		t.Fatal(err)
	}
	if items, _ := getShelf(t, e, c); !shelfHas(items, "XR-005") {
		t.Fatalf("просмотр без реплики снял запись с полки: %+v", items)
	}

	talk := humanLine("начинай", at.Add(-time.Hour)) + humanLine("катим", at.Add(5*time.Minute))
	if err := os.WriteFile(path, []byte(talk), 0o644); err != nil {
		t.Fatal(err)
	}
	if items, _ := getShelf(t, e, c); shelfHas(items, "XR-005") {
		t.Fatalf("отвеченный повод остался на полке: %+v", items)
	}
}
