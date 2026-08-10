package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Образцы строк журнала уведомителя: первые две сняты с работающего
// hooks/notify.py до ленты (текста в них нет), дальше идут строки с хвостом
// текста, которые пишет уведомитель, позванный с поводом.
var notifyFixture = []string{
	"2026-08-02T14:03:11 сессия f07df579 повод permission_prompt уровень громкий бэкенд terminal-notifier цель vscode://file/p/devkit код возврата: 0",
	"2026-08-02T14:08:30 сессия f07df579 повод turn_done уровень громкий бэкенд - цель - пропуск: окно сессии в фокусе",
	"2026-08-09T21:16:04 сессия - повод task_check уровень громкий бэкенд terminal-notifier цель - код возврата: 0 текст «devkit: XR-213 в Check» «сценарий проверки за пользователем»",
	"2026-08-10T00:12:35 сессия - повод wait_human уровень громкий бэкенд terminal-notifier цель - код возврата: 0 текст «цель XR-100: wait-human» «плановый стоп серии»",
	"2026-08-10T00:41:02 сессия - повод run_stop уровень громкий бэкенд - цель - пропуск: песочница, корень /tmp/x лежит под /tmp текст «demo: XR-100 стоп из дашборда» «цикл цели снят из дашборда»",
}

func writeNotifyLog(t *testing.T, home string, lines []string) string {
	t.Helper()
	dir := filepath.Join(home, ".devkit")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "notify.log")
	body := ""
	if len(lines) > 0 {
		body = strings.Join(lines, "\n") + "\n"
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

type feedResp struct {
	Exists bool           `json:"exists"`
	Note   string         `json:"note"`
	Items  []Notification `json:"items"`
}

func getFeed(t *testing.T, e *testEnv, query string) feedResp {
	t.Helper()
	c := e.loggedClient(t)
	resp, err := c.Get(e.srv.URL + "/api/notifications" + query)
	if err != nil {
		t.Fatal(err)
	}
	var out feedResp
	if err := json.Unmarshal([]byte(body(t, resp)), &out); err != nil {
		t.Fatalf("ответ ленты не разобрался: %v", err)
	}
	return out
}

// Строка со стопом читается целиком: повод становится типом, текст хвоста
// заголовком и телом, а ID достаётся из текста, потому что своего поля с ним
// у журнала нет.
func TestParseNotifyLineWithText(t *testing.T) {
	n, ok := parseNotifyLine(notifyFixture[4])
	if !ok {
		t.Fatal("строка стопа не разобралась")
	}
	if n.Kind != "stop" || n.Reason != "run_stop" {
		t.Errorf("тип события: %q (повод %q), ожидал stop", n.Kind, n.Reason)
	}
	if n.Title != "demo: XR-100 стоп из дашборда" || n.Body != "цикл цели снят из дашборда" {
		t.Errorf("текст уведомления: %q / %q", n.Title, n.Body)
	}
	if n.ID != "XR-100" {
		t.Errorf("ID работы из текста: %q", n.ID)
	}
	// Баннер до человека не дошёл, и лента обязана это различать: пропуск это
	// не доставка.
	if n.Sent {
		t.Error("пропущенный баннер посчитан отправленным")
	}
	if !strings.HasPrefix(n.Result, "пропуск: песочница") {
		t.Errorf("причина пропуска потерялась: %q", n.Result)
	}
}

// Строки, писанные до ленты, текста не имеют: событие всё равно называется
// словами, а не остаётся пустой строкой экрана.
func TestParseNotifyLineWithoutText(t *testing.T) {
	n, ok := parseNotifyLine(notifyFixture[0])
	if !ok {
		t.Fatal("строка без текста не разобралась")
	}
	if n.Title != "сессия ждёт разрешения" || n.Kind != "other" {
		t.Errorf("строка без текста: %q, тип %q", n.Title, n.Kind)
	}
	if !n.Sent {
		t.Error("код возврата 0 не посчитан доставкой")
	}
}

// Битая строка пропускается без обрушения ленты, как битая строка
// транскрипта: журнал пишут и чужие руки.
func TestParseNotifyLineBroken(t *testing.T) {
	for _, line := range []string{
		"",
		"мусор без единого поля",
		"2026-08-02T14:03:11 сессия f07df579 повод turn_done",
		"2026-08-02T14:03:11 сессия f07df579 причина turn_done уровень громкий бэкенд - цель - код возврата: 0",
		"вчера сессия f07df579 повод turn_done уровень громкий бэкенд - цель - код возврата: 0",
	} {
		if n, ok := parseNotifyLine(line); ok {
			t.Errorf("битая строка %q разобралась: %+v", line, n)
		}
	}
	items, seen := parseNotifications([]string{"мусор", notifyFixture[3]}, nil)
	if len(items) != 1 || !seen {
		t.Fatalf("битая строка утащила за собой ленту: %d событий", len(items))
	}
}

// Лента отдаёт хвост журнала свежими событиями и разбирает их поводы; вход
// обязателен, как и всюду.
func TestNotificationsTail(t *testing.T) {
	e := newTestEnv(t)
	writeNotifyLog(t, e.home, notifyFixture)
	out := getFeed(t, e, "")
	if !out.Exists || len(out.Items) != len(notifyFixture) {
		t.Fatalf("лента отдала %d событий из %d (note %q)", len(out.Items), len(notifyFixture), out.Note)
	}
	if out.Items[0].Time != "2026-08-02T14:03:11" {
		t.Errorf("порядок ленты: первым %q", out.Items[0].Time)
	}
	kinds := map[string]int{}
	for _, n := range out.Items {
		kinds[n.Kind]++
	}
	if kinds["stop"] != 1 || kinds["wait"] != 1 || kinds["task"] != 1 || kinds["other"] != 2 {
		t.Errorf("типы событий: %v", kinds)
	}
}

// Фильтр по типам берёт три типа DoD порознь и вместе: экран ленты ходит
// теми же параметрами, что и smoke.
func TestNotificationsFilter(t *testing.T) {
	e := newTestEnv(t)
	writeNotifyLog(t, e.home, notifyFixture)
	for _, c := range []struct {
		query string
		want  int
	}{
		{"?kind=stop", 1},
		{"?kind=wait", 1},
		{"?kind=stop,wait,task", 3},
		{"?kind=", 5},
	} {
		out := getFeed(t, e, c.query)
		if len(out.Items) != c.want {
			t.Errorf("%s: %d событий, ожидал %d", c.query, len(out.Items), c.want)
		}
	}
}

// Три пустоты различимы словами: журнала нет, журнал пуст, под фильтр ничего
// не попало. Одинаковый пустой экран прятал бы поломку уведомителя.
func TestNotificationsEmptinessIsNamed(t *testing.T) {
	e := newTestEnv(t)
	out := getFeed(t, e, "")
	if out.Exists || !strings.Contains(out.Note, "уведомитель ещё ни разу не срабатывал") {
		t.Fatalf("без журнала: exists=%v note=%q", out.Exists, out.Note)
	}
	writeNotifyLog(t, e.home, nil)
	out = getFeed(t, e, "")
	if !out.Exists || !strings.Contains(out.Note, "журнал уведомителя пуст") {
		t.Fatalf("пустой журнал: exists=%v note=%q", out.Exists, out.Note)
	}
	writeNotifyLog(t, e.home, notifyFixture[:2])
	out = getFeed(t, e, "?kind=stop")
	if len(out.Items) != 0 || !strings.Contains(out.Note, "под фильтр") {
		t.Fatalf("фильтр без попаданий: %d событий, note %q", len(out.Items), out.Note)
	}
}

// Живая лента: хвост приходит сразу, а стоп, случившийся при открытом потоке,
// доезжает без перезагрузки страницы. Фильтр работает и в потоке.
func TestNotificationsStream(t *testing.T) {
	e := newTestEnv(t)
	old := tailPoll
	tailPoll = 10 * time.Millisecond
	t.Cleanup(func() { tailPoll = old })
	path := writeNotifyLog(t, e.home, notifyFixture[:4])
	c := e.loggedClient(t)

	r, done := sseClient(t, c, e.srv.URL+"/api/notifications?stream=1&kind=stop,wait")
	defer done()
	if _, data := sseNext(t, r); !strings.Contains(data, "wait-human") {
		t.Fatalf("хвост под фильтром: %q", data)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(notifyFixture[4] + "\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()
	_, data := sseNext(t, r)
	var n Notification
	if err := json.Unmarshal([]byte(data), &n); err != nil {
		t.Fatalf("событие потока не разобралось: %v (%q)", err, data)
	}
	if n.Kind != "stop" || n.ID != "XR-100" {
		t.Fatalf("живой стоп в ленте: %+v", n)
	}
}

// Экран ленты собран по макету «05 Лента»: три типа DoD чипами фильтров,
// группировка по дням, действие «Поднять виток» у стопа и живой поток вместо
// перезагрузки страницы.
func TestStaticFeedScreen(t *testing.T) {
	app := readFile(t, filepath.Join("static", "app.js"))
	for _, want := range []string{
		`{ kind: "stop", name: "Стопы" }`,
		`{ kind: "wait", name: "wait-human" }`,
		`{ kind: "task", name: "Задачи" }`,
		"Поднять виток",
		"Журнал цикла",
		"/api/notifications?stream=1",
		"dayLabel",
		"баннера не было",
	} {
		if !strings.Contains(app, want) {
			t.Errorf("в static/app.js нет части экрана ленты %q", want)
		}
	}
	css := readFile(t, filepath.Join("static", "style.css"))
	for _, want := range []string{".fchip", ".nday", ".i-stop", ".i-wait", ".i-done"} {
		if !strings.Contains(css, want) {
			t.Errorf("в static/style.css нет стиля ленты %q", want)
		}
	}
}

// Вход в ленту это колокольчик в шапке (DK-246), а не пункт разделов: шапка
// стоит над любым экраном и на ноутбуке, и на телефоне, поэтому один вход
// закрывает оба форм-фактора. Пункта «Лента» в боковой колонке и в нижних
// вкладках больше нет.
func TestStaticFeedBellEntry(t *testing.T) {
	page := readFile(t, filepath.Join("static", "index.html"))
	for _, want := range []string{`id="bell"`, `aria-label="Лента уведомлений"`, `id="bell-dot"`} {
		if !strings.Contains(page, want) {
			t.Errorf("в static/index.html нет колокольчика %q", want)
		}
	}
	for _, gone := range []string{`id="nav-feed"`, `id="tab-feed"`, ">Лента<"} {
		if strings.Contains(page, gone) {
			t.Errorf("в static/index.html остался прежний вход в ленту %q", gone)
		}
	}
	app := readFile(t, filepath.Join("static", "app.js"))
	if !strings.Contains(app, `["bell", "/feed"]`) {
		t.Error("колокольчик не ведёт на ленту")
	}
	if strings.Contains(app, "nav-feed") || strings.Contains(app, "tab-feed") {
		t.Error("в static/app.js осталась разводка пункта «Лента»")
	}
	css := readFile(t, filepath.Join("static", "style.css"))
	for _, want := range []string{".bell{", ".bell.on", ".bdot{"} {
		if !strings.Contains(css, want) {
			t.Errorf("в static/style.css нет стиля колокольчика %q", want)
		}
	}
}

// Счётчика непрочитанного нет и не будет: отметок прочитанного сервер не
// держит. Вместо числа точка новых с последнего захода, и заход помнит сам
// браузер (DK-246): открытая лента гасит точку, а событие, пришедшее на
// открытый экран, её не зажигает.
func TestStaticFeedDotFromLocalSeen(t *testing.T) {
	app := readFile(t, filepath.Join("static", "app.js"))
	for _, want := range []string{
		`const FEED_SEEN_KEY = "devkit.feed.seen"`,
		"localStorage.getItem(FEED_SEEN_KEY)",
		"localStorage.setItem(FEED_SEEN_KEY",
		"function refreshBellDot()",
		"showBellDot(Boolean(last) && last > seen)",
		"markFeedSeen(nowStamp())",
		"markFeedSeen(n.time)",
	} {
		if !strings.Contains(app, want) {
			t.Errorf("в static/app.js нет части индикатора новых %q", want)
		}
	}
	page := readFile(t, filepath.Join("static", "index.html"))
	if !strings.Contains(page, `class="bdot" hidden`) {
		t.Error("точка индикатора зажжена в разметке, а не логикой")
	}
}

// Потока без журнала не бывает молчащим: пустота называется событием note, а
// родившийся журнал подхватывает тот же поток.
func TestNotificationsStreamMissingThenBorn(t *testing.T) {
	e := newTestEnv(t)
	old := tailPoll
	tailPoll = 10 * time.Millisecond
	t.Cleanup(func() { tailPoll = old })
	c := e.loggedClient(t)

	r, done := sseClient(t, c, e.srv.URL+"/api/notifications?stream=1")
	defer done()
	event, data := sseNext(t, r)
	if event != "note" || !strings.Contains(data, "ни разу не срабатывал") {
		t.Fatalf("пустота без имени: event=%q data=%q", event, data)
	}
	writeNotifyLog(t, e.home, notifyFixture[3:4])
	if _, data := sseNext(t, r); !strings.Contains(data, "wait_human") {
		t.Fatalf("родившийся журнал не подхвачен: %q", data)
	}
}
