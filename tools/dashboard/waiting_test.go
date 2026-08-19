package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dronrider/devkit/internal/chat"
)

// Состояние «ждёт человека» (DK-433, LLD DK-430, решение 4): три источника от
// точного к запасному, ранг между ними и подпись, по которой видно, чему
// верить. Стенды на живых форматах: признак ожидания пишется тем же
// internal/chat, каким его пишет taskctl ask, строки журнала уведомителя и
// реестра выписаны по формату писателей.

// waitNow это часы стендов: время в них стоит, и срок ожидания считается от
// него, а не от часов машины.
var waitNow = time.Date(2026, 8, 18, 12, 0, 0, 0, time.Local)

// writeAsk кладёт признак ожидания во вход задачи дерева, как его кладёт
// инструмент ожидания.
func writeAsk(t *testing.T, tree, id string, until time.Time, sid string, qs ...string) {
	t.Helper()
	a := chat.Ask{Until: until, Session: sid, Task: id}
	for _, q := range qs {
		a.Questions = append(a.Questions, chat.Question{Text: q})
	}
	if err := chat.WriteAsk(tree, chat.TaskName(id), a); err != nil {
		t.Fatal(err)
	}
}

// Живой признак ожидания это первый источник: состояние «ждёт ответа», вопросы
// пачкой в порядке инструмента, срок и ждущая сессия.
func TestAskWaitingReadsSign(t *testing.T) {
	tree := t.TempDir()
	until := waitNow.Add(7 * time.Minute)
	writeAsk(t, tree, "XR-4", until, "aaaa-1111", "чинить копией или общим модулем?", "ждать ли ревью?")

	w, ok := askWaiting(tree, "XR-4", waitNow)
	if !ok {
		t.Fatal("живой признак ожидания не прочитался")
	}
	if w.State != waitAskState || w.Source != waitAsk || w.Note != waitAskNote {
		t.Errorf("состояние: %+v", w)
	}
	if w.Until != until.Unix() {
		t.Errorf("срок: %d, жду %d", w.Until, until.Unix())
	}
	if w.Session != "aaaa-1111" {
		t.Errorf("сессия: %q", w.Session)
	}
	want := []string{"чинить копией или общим модулем?", "ждать ли ревью?"}
	if strings.Join(w.Questions, "|") != strings.Join(want, "|") {
		t.Errorf("вопросы: %q, жду %q", w.Questions, want)
	}
	if w.Since == 0 {
		t.Error("момента начала нет: врезке нечем сказать, сколько заход стоит")
	}
}

// Признак с вышедшим сроком за ожидание не выдаётся: ждущего за ним нет, снять
// признак ему было нечем, и живой чип врал бы, что ответа ещё ждут.
func TestAskWaitingIgnoresExpiredSign(t *testing.T) {
	tree := t.TempDir()
	writeAsk(t, tree, "XR-4", waitNow.Add(-time.Minute), "aaaa-1111", "вопрос")
	if w, ok := askWaiting(tree, "XR-4", waitNow); ok {
		t.Fatalf("брошенный признак прочитался ожиданием: %+v", w)
	}
	if _, ok := askWaiting(tree, "XR-9", waitNow); ok {
		t.Error("ожидание нашлось у задачи без признака")
	}
}

// Парковка это второй источник, и от первого он отличается словами: тут заход
// уже кончился рубежом, а не стоит с вопросом.
func TestParkedWaitingFromBlockReason(t *testing.T) {
	w, ok := parkedWaiting("blocked", "вопрос: чинить копией или общим модулем?")
	if !ok {
		t.Fatal("припаркованная вопросом строка не дала ожидания")
	}
	if w.State != waitParkState || w.Source != waitParked || w.Note != waitParkNote {
		t.Errorf("состояние: %+v", w)
	}
	if len(w.Questions) != 1 || w.Questions[0] != "чинить копией или общим модулем?" {
		t.Errorf("вопрос из причины: %q", w.Questions)
	}
	if w.Until != 0 {
		t.Error("у парковки появился срок, которого источник не знает")
	}
	if _, ok := parkedWaiting("blocked", "ждём железо"); ok {
		t.Error("блок без машинного разряда «вопрос:» выдан за ожидание")
	}
	if _, ok := parkedWaiting("in-progress", "вопрос: а что если"); ok {
		t.Error("строка не в Blocked выдана за парковку")
	}
}

// idleLine собирает строку журнала уведомителя по формату hooks/notify.py, с
// обрезанным до восьми знаков ID сессии.
func idleLine(stamp, short, reason string) string {
	return fmt.Sprintf("%s сессия %s повод %s уровень громкий бэкенд terminal-notifier цель - "+
		"задача - проект - код возврата: 0 текст «сессия ждёт ввода» «»", stamp, short, reason)
}

// Запасной источник: повод из журнала связывается с задачей реестром чатов по
// префиксу сессии, потому что в журнале ID обрезан.
func TestIdleWaitsBindsBySessionPrefix(t *testing.T) {
	binds := parseBinds([]byte(bindRecord("2026-08-18T11:40:00", "aaaa1111-full-id", "XR-4", bindOrder)))
	waits, unclaimed := idleWaits([]string{idleLine("2026-08-18T11:55:00", "aaaa1111", idlePromptReason)},
		binds, waitNow)
	w, ok := waits["XR-4"]
	if !ok {
		t.Fatalf("повод не дошёл до задачи: %+v, невязки %+v", waits, unclaimed)
	}
	if w.State != waitIdleState || w.Source != waitIdle || w.Note != waitIdleNote {
		t.Errorf("состояние: %+v", w)
	}
	if w.Session != "aaaa1111-full-id" {
		t.Errorf("сессия в поле обрезана: %q, реестр знает её целиком", w.Session)
	}
	if w.Until != 0 || len(w.Questions) != 0 {
		t.Errorf("у повода из журнала завёлся срок или вопрос: %+v", w)
	}
	if len(unclaimed) != 0 {
		t.Errorf("невязки на ровном месте: %+v", unclaimed)
	}
}

// Сессия, сходившая ход после ожидания, ждущей не считается: конец хода стоит
// в журнале последним, и ожидание с неё снято.
func TestIdleWaitsDropSupersededEvent(t *testing.T) {
	binds := parseBinds([]byte(bindRecord("2026-08-18T11:40:00", "aaaa1111-full-id", "XR-4", bindOrder)))
	lines := []string{
		idleLine("2026-08-18T11:50:00", "aaaa1111", idlePromptReason),
		idleLine("2026-08-18T11:55:00", "aaaa1111", "turn_done"),
	}
	if waits, _ := idleWaits(lines, binds, waitNow); len(waits) != 0 {
		t.Fatalf("ожидание осталось после хода сессии: %+v", waits)
	}
}

// Вчерашний повод ожиданием не считается: отбоя у idle_prompt нет, и без срока
// чип горел бы на строке неделю.
func TestIdleWaitsDropStaleEvent(t *testing.T) {
	binds := parseBinds([]byte(bindRecord("2026-08-17T09:00:00", "aaaa1111-full-id", "XR-4", bindOrder)))
	lines := []string{idleLine("2026-08-17T09:30:00", "aaaa1111", idlePromptReason)}
	if waits, _ := idleWaits(lines, binds, waitNow); len(waits) != 0 {
		t.Fatalf("повод старше полусуток выдан за ожидание: %+v", waits)
	}
}

// Две записи реестра под одним обрезанным ID: событие остаётся без задачи, а
// невязка называется словами, а не выбирается наугад.
func TestIdleWaitsNameAmbiguousPrefix(t *testing.T) {
	binds := parseBinds([]byte(
		bindRecord("2026-08-18T11:40:00", "aaaa1111-one", "XR-4", bindOrder) +
			bindRecord("2026-08-18T11:41:00", "aaaa1111-two", "XR-7", bindTree)))
	waits, unclaimed := idleWaits([]string{idleLine("2026-08-18T11:55:00", "aaaa1111", idlePromptReason)},
		binds, waitNow)
	if len(waits) != 0 {
		t.Fatalf("событие ушло к задаче наугад: %+v", waits)
	}
	why := unclaimed["aaaa1111"]
	if !strings.Contains(why, "aaaa1111") || !strings.Contains(why, "XR-4, XR-7") {
		t.Fatalf("невязка не называет ни сессии, ни задач: %q", why)
	}
}

// Ранг источников: живой признак старше парковки, парковка старше повода из
// журнала, и подпись у каждого своя.
func TestWaitLookupRanksSources(t *testing.T) {
	e := newTestEnv(t)
	e.s.now = func() time.Time { return waitNow }
	writeBinds(t, e.home, bindRecord("2026-08-18T11:40:00", "aaaa1111-full-id", "XR-4", bindOrder))
	writeNotifyLog(t, e.home, []string{idleLine("2026-08-18T11:55:00", "aaaa1111", idlePromptReason)})

	look := e.s.waitLookup(e.proj)
	w, ok := look("XR-4", "in-progress", "")
	if !ok || w.Source != waitIdle {
		t.Fatalf("запасной источник не сработал в одиночку: %+v %v", w, ok)
	}
	if w, ok := look("XR-4", "blocked", "вопрос: чинить копией?"); !ok || w.Source != waitParked {
		t.Fatalf("парковка не перебила повод из журнала: %+v %v", w, ok)
	}
	writeAsk(t, e.proj, "XR-4", waitNow.Add(5*time.Minute), "aaaa1111-full-id", "чинить копией?")
	look = e.s.waitLookup(e.proj)
	if w, ok := look("XR-4", "blocked", "вопрос: чинить копией?"); !ok || w.Source != waitAsk {
		t.Fatalf("признак ожидания не перебил парковку: %+v %v", w, ok)
	}
	if _, ok := look("XR-9", "backlog", ""); ok {
		t.Error("у стоящей строки завелось ожидание, хотя никто её не ждёт")
	}
}

// waitingBoardJSON это доска с припаркованной вопросом строкой: по ней видно,
// что поле waiting доезжает и до строки доски, и до экрана задачи.
const waitingBoardJSON = `{"prefix":"XR","sections":[` +
	`{"key":"in-progress","title":"In progress","rows":[` +
	`{"id":"XR-4","title":"Начатая задача","type":"task","p":"P2","r":31,"r_parts":[25,3,1,0,2],"cost":"-","link":"-"}]},` +
	`{"key":"blocked","title":"Blocked","rows":[` +
	`{"id":"XR-7","title":"Стоит вопросом","type":"task","p":"P2","r":30,"r_parts":[25,2,1,0,2],"cost":"-","link":"-",` +
	`"block":"вопрос: чинить копией или общим модулем?"}]}]}`

// waitEnv поднимает стенд на доске с припаркованной строкой и стоящими часами.
func waitEnv(t *testing.T) (*testEnv, *http.Client) {
	t.Helper()
	e := newTestEnv(t)
	writeScript(t, e.bin, "taskctl", fmt.Sprintf("echo '%s'", waitingBoardJSON))
	e.s.now = func() time.Time { return waitNow }
	return e, e.loggedClient(t)
}

// Строка доски несёт состояние ожидания своим полем: собирать его на клиенте из
// строки, списка сессий и ленты значило бы повторить ошибку признака Run.
func TestBoardRowCarriesWaiting(t *testing.T) {
	e, c := waitEnv(t)
	writeAsk(t, e.proj, "XR-4", waitNow.Add(9*time.Minute), "aaaa-1111", "чинить копией?")

	resp, err := c.Get(e.srv.URL + "/api/projects/demo/board")
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Board struct {
			Sections []struct {
				Rows []boardRow `json:"rows"`
			} `json:"sections"`
		} `json:"board"`
	}
	if err := json.Unmarshal([]byte(body(t, resp)), &out); err != nil {
		t.Fatal(err)
	}
	got := map[string]*Waiting{}
	for _, sec := range out.Board.Sections {
		for _, row := range sec.Rows {
			got[row.ID] = row.Waiting
		}
	}
	if got["XR-4"] == nil || got["XR-4"].Source != waitAsk {
		t.Fatalf("у строки с признаком ожидания нет поля waiting: %+v", got["XR-4"])
	}
	if got["XR-7"] == nil || got["XR-7"].Source != waitParked {
		t.Fatalf("припаркованная строка не назвалась ждущей: %+v", got["XR-7"])
	}
	if got["XR-7"].State == got["XR-4"].State {
		t.Error("парковка и живой признак дали одно состояние: разницу человеку не увидеть")
	}
}

// Экран задачи спрашивает про ожидание то же, что доска: врезка панели чата
// читает поле оттуда.
func TestTaskRowCarriesWaiting(t *testing.T) {
	e, c := waitEnv(t)
	writeAsk(t, e.proj, "XR-4", waitNow.Add(9*time.Minute), "aaaa-1111", "чинить копией?")

	resp, err := c.Get(e.srv.URL + "/api/projects/demo/tasks/XR-4")
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Row boardRow `json:"row"`
	}
	if err := json.Unmarshal([]byte(body(t, resp)), &out); err != nil {
		t.Fatal(err)
	}
	if out.Row.Waiting == nil || out.Row.Waiting.Source != waitAsk {
		t.Fatalf("на экране задачи нет ожидания: %+v", out.Row.Waiting)
	}
	if len(out.Row.Waiting.Questions) != 1 {
		t.Fatalf("вопрос до экрана задачи не доехал: %+v", out.Row.Waiting)
	}
}

// Повод «сессия ждёт ввода» стоит в ленте типом wait, а не в прочих: иначе
// фильтр «Ожидание пользователя» прятал бы ровно тот случай, ради которого
// строка доски и загорается ожиданием.
func TestFeedIdlePromptIsWaitKind(t *testing.T) {
	e := newTestEnv(t)
	writeNotifyLog(t, e.home, []string{idleLine("2026-08-18T11:55:00", "aaaa1111", idlePromptReason)})
	feed := getFeed(t, e, "?kind=wait")
	if len(feed.Items) != 1 {
		t.Fatalf("под фильтр ожидания повод не попал: %+v, %s", feed.Items, feed.Note)
	}
	if feed.Items[0].Kind != waitKind {
		t.Errorf("тип события: %q", feed.Items[0].Kind)
	}
}

// Событие ожидания доводится до задачи реестром чатов: в журнале уведомителя
// поле задачи у сессии главного чекаута пустое, и без реестра лента вела бы в
// никуда.
func TestFeedNamesWaitTaskByRegistry(t *testing.T) {
	e := newTestEnv(t)
	writeBinds(t, e.home, bindRecord("2026-08-18T11:40:00", "aaaa1111-full-id", "XR-4", bindOrder))
	writeNotifyLog(t, e.home, []string{idleLine("2026-08-18T11:55:00", "aaaa1111", idlePromptReason)})
	feed := getFeed(t, e, "?kind=wait")
	if len(feed.Items) != 1 {
		t.Fatalf("событий в ленте %d", len(feed.Items))
	}
	if feed.Items[0].ID != "XR-4" {
		t.Errorf("задача события: %q, реестр знает её по префиксу сессии", feed.Items[0].ID)
	}
	if feed.Items[0].Note != "" {
		t.Errorf("у связанного события завелась невязка: %q", feed.Items[0].Note)
	}
}

// Две живые сессии под одним обрезанным ID: событие остаётся без задачи, и
// лента говорит почему, а не выбирает наугад.
func TestFeedNamesAmbiguousPrefix(t *testing.T) {
	e := newTestEnv(t)
	writeBinds(t, e.home,
		bindRecord("2026-08-18T11:40:00", "aaaa1111-one", "XR-4", bindOrder),
		bindRecord("2026-08-18T11:41:00", "aaaa1111-two", "XR-7", bindTree))
	writeNotifyLog(t, e.home, []string{idleLine("2026-08-18T11:55:00", "aaaa1111", idlePromptReason)})
	feed := getFeed(t, e, "?kind=wait")
	if len(feed.Items) != 1 {
		t.Fatalf("событий в ленте %d", len(feed.Items))
	}
	if feed.Items[0].ID != "" {
		t.Errorf("событие ушло к задаче %q наугад", feed.Items[0].ID)
	}
	if !strings.Contains(feed.Items[0].Note, "aaaa1111") {
		t.Errorf("невязка не названа словами: %q", feed.Items[0].Note)
	}
}

// Ответ из врезки уходит ручкой задачи и будит припаркованную строку: строка
// ложится во вход задачи основного чекаута безадресной, и сторожок считает
// ответом только такую.
func TestWaitAnswerGoesByTaskHandle(t *testing.T) {
	e, c := waitEnv(t)
	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/tasks/XR-7/message",
		`{"text": "общим модулем"}`)
	text := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("ответ на вопрос: %d %s", resp.StatusCode, text)
	}
	if !strings.Contains(text, "припаркована вопросом") {
		t.Errorf("ответ не называет пробуждение припаркованной строки: %s", text)
	}
	src := readFile(t, filepath.Join(e.proj, ".devkit", "chat", "task-XR-7.in"))
	if strings.Contains(src, ", сессии ") {
		t.Fatalf("ответ лёг адресованной строкой, её сторожок ответом не считает:\n%s", src)
	}
}

// Слова чипа: состояние словом плюс обратный отсчёт до срока, и отсчёт есть
// только у первого источника, потому что срок знает только он.
func TestStaticWaitWords(t *testing.T) {
	heads := []string{"function waitLeft(", "function waitWords(", "function workAge("}
	now := waitNow.UnixMilli()
	cases := []struct{ input, want string }{
		{fmt.Sprintf(`{state: "ждёт ответа", until: %d}`, waitNow.Add(7*time.Minute).Unix()),
			"ждёт ответа, 7 мин"},
		{fmt.Sprintf(`{state: "ждёт ответа", until: %d}`, waitNow.Add(-time.Minute).Unix()),
			"ждёт ответа, срок вышел"},
		{`{state: "припаркована вопросом"}`, "припаркована вопросом"},
		{`{state: "сессия ждёт ввода"}`, "сессия ждёт ввода"},
	}
	for _, c := range cases {
		if got := jsEval(t, heads, fmt.Sprintf("waitWords(%s, %d)", c.input, now)); got != c.want {
			t.Errorf("подпись для %s: %q, жду %q", c.input, got, c.want)
		}
	}
}

// Чип ожидания стоит и в строке доски, и на карточке задачи, а сам вопрос
// приходит подсказкой при нём: без этого поле waiting некуда показать, и
// простой задачи с доски не виден.
func TestStaticWaitChipAndCard(t *testing.T) {
	text := readFile(t, filepath.Join("static", "app.js"))
	if !strings.Contains(funcBody(t, text, "function rowChips("), "waitChip(row)") {
		t.Error("в строке доски нет чипа ожидания: простой задачи с доски не виден")
	}
	if !strings.Contains(funcBody(t, text, "async function renderTask("), "waitChip(row)") {
		t.Error("на карточке задачи нет чипа ожидания: с её экрана простой не виден")
	}
	chip := funcBody(t, text, "function waitChip(")
	if !strings.Contains(chip, "row.waiting") {
		t.Error("чип ожидания собирается не из поля waiting строки")
	}
	for _, want := range []string{"w.questions", `" Вопрос: "`, "w.note"} {
		if !strings.Contains(chip, want) {
			t.Errorf("подсказка чипа не несёт %q: чего от человека ждут, видно только словом «ждёт»", want)
		}
	}
	css := readFile(t, filepath.Join("static", "style.css"))
	if !strings.Contains(css, ".c-wait{") {
		t.Error("в static/style.css нет стиля ожидания .c-wait")
	}
}

// Признак ожидания и реестр читаются один раз на ответ, а не на каждую строку
// доски: доска на сотню строк иначе стоила бы сотни чтений журнала.
func TestWaitScanReadsLogOnce(t *testing.T) {
	e := newTestEnv(t)
	e.s.now = func() time.Time { return waitNow }
	writeNotifyLog(t, e.home, []string{idleLine("2026-08-18T11:55:00", "aaaa1111", idlePromptReason)})
	path := filepath.Join(e.home, ".devkit", "notify.log")
	look := e.s.waitLookup(e.proj)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	// Журнала уже нет, а разбор был снят до него: строка отвечает по снятому
	// хвосту, а не бежит за новым чтением.
	writeBinds(t, e.home, bindRecord("2026-08-18T11:40:00", "aaaa1111-full-id", "XR-4", bindOrder))
	if _, ok := look("XR-4", "in-progress", ""); ok {
		t.Error("разбор пошёл за журналом второй раз: реестр приехал после снятия хвоста")
	}
}
