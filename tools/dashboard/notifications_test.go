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

// notifyStopSent это настоящий доехавший стоп, соседний по тексту с
// пропуском по песочнице notifyFixture[4]: живой поток обязан отдать его
// событием, а призрак песочницы рядом - нет (DK-283).
const notifyStopSent = "2026-08-10T00:41:05 сессия - повод run_stop уровень громкий бэкенд terminal-notifier цель - код возврата: 0 текст «demo: XR-100 стоп из дашборда» «цикл цели снят из дашборда»"

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

// Строки уведомителя с полями задачи и проекта (DK-323): по ним лента ведёт
// от события к строке доски, и ID в тексте баннера ей больше не нужен.
var notifyTargetFixture = []string{
	// Конец хода в дереве задачи: ID в тексте не написан вовсе, и до полей
	// такое событие висело в ленте оторванным.
	"2026-08-14T10:01:02 сессия f07df579 повод turn_done уровень громкий бэкенд terminal-notifier цель - задача DK-323 проект devkit код возврата: 0 текст «devkit (dk-323): ход закончен» «правка уехала»",
	// Событие без задачи: самопроверка канала, вести от неё некуда, и поле
	// стоит прочерком честно.
	"2026-08-14T10:03:04 сессия - повод self-test уровень громкий бэкенд terminal-notifier цель - задача - проект devkit код возврата: 0 текст «devkit: самопроверка» «канал уведомлений devkit»",
	// Стоп чужого проекта: «Поднять виток» обязан бить в его проект, а не в
	// открытый на экране.
	"2026-08-14T10:05:06 сессия - повод run_stop уровень громкий бэкенд terminal-notifier цель - задача IRC-75 проект it-road-course код возврата: 0 текст «it-road-course: IRC-75 стоп из дашборда» «конвейер задачи снят из дашборда»",
}

// Задача и проект берутся из полей строки, а не из слов баннера: событие в
// дереве задачи ID в тексте не несёт, и раньше лента вела от него никуда.
func TestParseNotifyLineTakesTargetFromFields(t *testing.T) {
	n, ok := parseNotifyLine(notifyTargetFixture[0])
	if !ok {
		t.Fatal("строка с полями задачи и проекта не разобралась")
	}
	if n.ID != "DK-323" || n.Project != "devkit" {
		t.Errorf("задача и проект события: %q / %q", n.ID, n.Project)
	}
	if n.Title != "devkit (dk-323): ход закончен" || n.Body != "правка уехала" {
		t.Errorf("текст уведомления: %q / %q", n.Title, n.Body)
	}
	// Результат стоит за новыми полями, и разбор обязан начинать его с кода
	// возврата, а не со слова «задача».
	if n.Result != "код возврата: 0" || !n.Sent {
		t.Errorf("результат строки с полями: %q (доставка %v)", n.Result, n.Sent)
	}
	stop, ok := parseNotifyLine(notifyTargetFixture[2])
	if !ok {
		t.Fatal("строка стопа с полями не разобралась")
	}
	if stop.ID != "IRC-75" || stop.Project != "it-road-course" || stop.Kind != "stop" {
		t.Errorf("стоп чужого проекта: %q / %q, тип %q", stop.ID, stop.Project, stop.Kind)
	}
}

// Прочерк в поле задачи это честная пустота: ID из текста поверх него не
// вылавливается, иначе самопроверка вела бы на строку доски, которой у неё нет.
func TestParseNotifyLineKeepsEventWithoutTask(t *testing.T) {
	n, ok := parseNotifyLine(notifyTargetFixture[1])
	if !ok {
		t.Fatal("строка без задачи не разобралась")
	}
	if n.ID != "" {
		t.Errorf("задача выдумалась из текста: %q", n.ID)
	}
	if n.Project != "devkit" {
		t.Errorf("проект события: %q", n.Project)
	}
	withID := strings.Replace(notifyTargetFixture[1],
		"текст «devkit: самопроверка»", "текст «devkit: самопроверка XR-9»", 1)
	if n, _ := parseNotifyLine(withID); n.ID != "" {
		t.Errorf("ID из слов баннера победил поле с прочерком: %q", n.ID)
	}
}

// Строки, писанные до полей, читаются по-прежнему: задача берётся из текста,
// проекта у них нет, и ленту они не ломают.
func TestParseNotifyLineOldLinesStillRead(t *testing.T) {
	n, ok := parseNotifyLine(notifyFixture[2])
	if !ok {
		t.Fatal("строка без полей не разобралась")
	}
	if n.ID != "XR-213" || n.Project != "" {
		t.Errorf("старая строка: задача %q, проект %q", n.ID, n.Project)
	}
	if n.Result != "код возврата: 0" {
		t.Errorf("результат старой строки: %q", n.Result)
	}
}

// Лента отдаёт поля наружу: клиент строит переход по ним, и в JSON ручки они
// обязаны быть.
func TestNotificationsGiveTargetFields(t *testing.T) {
	e := newTestEnv(t)
	writeNotifyLog(t, e.home, notifyTargetFixture)
	out := getFeed(t, e, "")
	if len(out.Items) != len(notifyTargetFixture) {
		t.Fatalf("лента отдала %d событий из %d (note %q)", len(out.Items),
			len(notifyTargetFixture), out.Note)
	}
	want := []struct{ id, project string }{
		{"DK-323", "devkit"}, {"", "devkit"}, {"IRC-75", "it-road-course"},
	}
	for i, w := range want {
		if out.Items[i].ID != w.id || out.Items[i].Project != w.project {
			t.Errorf("событие %d: задача %q проект %q, ждал %q / %q", i,
				out.Items[i].ID, out.Items[i].Project, w.id, w.project)
		}
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
// обязателен, как и всюду. Пятая строка образца это пропуск по песочнице
// (DK-283), и в счёт она не идёт: 22-52 строки этого файла её так же не ждут.
func TestNotificationsTail(t *testing.T) {
	e := newTestEnv(t)
	writeNotifyLog(t, e.home, notifyFixture)
	out := getFeed(t, e, "")
	want := len(notifyFixture) - 1
	if !out.Exists || len(out.Items) != want {
		t.Fatalf("лента отдала %d событий из %d (note %q)", len(out.Items), want, out.Note)
	}
	if out.Items[0].Time != "2026-08-02T14:03:11" {
		t.Errorf("порядок ленты: первым %q", out.Items[0].Time)
	}
	kinds := map[string]int{}
	for _, n := range out.Items {
		kinds[n.Kind]++
	}
	if kinds["stop"] != 0 || kinds["wait"] != 1 || kinds["task"] != 1 || kinds["other"] != 2 {
		t.Errorf("типы событий: %v", kinds)
	}
}

// Фильтр по типам берёт три типа DoD порознь и вместе: экран ленты ходит
// теми же параметрами, что и smoke. Пропуск по песочнице (пятая строка
// образца) под фильтр stop не попадает ни разу: лента его не показывает.
func TestNotificationsFilter(t *testing.T) {
	e := newTestEnv(t)
	writeNotifyLog(t, e.home, notifyFixture)
	for _, c := range []struct {
		query string
		want  int
	}{
		{"?kind=stop", 0},
		{"?kind=wait", 1},
		{"?kind=stop,wait,task", 2},
		{"?kind=", 4},
	} {
		out := getFeed(t, e, c.query)
		if len(out.Items) != c.want {
			t.Errorf("%s: %d событий, ожидал %d", c.query, len(out.Items), c.want)
		}
	}
}

// Строка с пометкой «пропуск: песочница» не идёт в ленту вовсе: она видна в
// parseNotifyLine (тест разбора строки), но до аггрегата ленты не доезжает,
// как будто её не было в журнале (DK-283). Журнал из одной такой строки
// читается как пустой, а не как «событие, не прошедшее фильтр».
func TestNotificationsHidesSandboxSkip(t *testing.T) {
	e := newTestEnv(t)
	writeNotifyLog(t, e.home, notifyFixture)
	out := getFeed(t, e, "")
	for _, n := range out.Items {
		if n.sandboxSkipped() {
			t.Fatalf("строка песочницы доехала до ленты: %+v", n)
		}
	}

	writeNotifyLog(t, e.home, notifyFixture[4:5])
	out = getFeed(t, e, "")
	if len(out.Items) != 0 || !strings.Contains(out.Note, "журнал уведомителя пуст") {
		t.Fatalf("журнал из одной строки песочницы: %d событий, note %q", len(out.Items), out.Note)
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
	// Пропуск по песочнице дописывается первым и своего события отдать не
	// должен: следом идёт настоящий стоп, и лента обязана донести именно его,
	// а не призрак песочницы (DK-283).
	if _, err := f.WriteString(notifyFixture[4] + "\n" + notifyStopSent + "\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()
	_, data := sseNext(t, r)
	var n Notification
	if err := json.Unmarshal([]byte(data), &n); err != nil {
		t.Fatalf("событие потока не разобралось: %v (%q)", err, data)
	}
	if n.Kind != "stop" || n.ID != "XR-100" || !n.Sent {
		t.Fatalf("живой стоп в ленте: %+v", n)
	}
}

// Экран ленты собран по макету «05 Лента»: три типа DoD чипами фильтров,
// группировка по дням, действие «Поднять виток» у стопа и живой поток вместо
// перезагрузки страницы.
func TestStaticFeedScreen(t *testing.T) {
	app := readFile(t, filepath.Join("static", "app.js"))
	for _, want := range []string{
		`{ kind: "stop", name: "Остановки" }`,
		`{ kind: "wait", name: "Ожидание пользователя" }`,
		`{ kind: "task", name: "Задачи" }`,
		"Поднять виток",
		"Журнал агента",
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

// Переход от события клиент строит по полям события (DK-323): проект берётся
// из события, а не с открытого экрана, к задаче и к журналу агента ведёт любое
// событие с задачей, а событие без задачи помечено словами.
func TestStaticFeedGoesByEventFields(t *testing.T) {
	app := readFile(t, filepath.Join("static", "app.js"))
	for _, want := range []string{
		"const to = n.project || project;",
		"startRun(to, n.id)",
		`location.hash = to + "/" + n.id;`,
		`jrn.href = "#" + to + "/agent/" + n.id;`,
		"задачи у события нет",
	} {
		if !strings.Contains(app, want) {
			t.Errorf("в static/app.js нет перехода по полям события %q", want)
		}
	}
	if strings.Contains(app, "startRun(project, n.id)") {
		t.Error("«Поднять виток» бьёт в открытый на экране проект, а не в проект события")
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
	} {
		if !strings.Contains(app, want) {
			t.Errorf("в static/app.js нет части индикатора новых %q", want)
		}
	}
	// Гашение спрашивается у той функции, которая за него отвечает: та же
	// строка стоит и в refreshBellDot, и поиск по всему файлу пропустил бы её
	// пропажу с экрана ленты.
	feed := funcBody(t, app, "function renderFeed(")
	if !strings.Contains(feed, "markFeedSeen(nowStamp())") {
		t.Error("заход на ленту не гасит точку: renderFeed не отмечает заход")
	}
	if !strings.Contains(feed, "markFeedSeen(n.time)") {
		t.Error("событие на открытой ленте зажжёт точку: живой поток не отмечает прочитанное")
	}
	if !strings.Contains(funcBody(t, app, "async function refreshBellDot("), "markFeedSeen(nowStamp())") {
		t.Error("первый заход в браузере зажжёт точку на всей истории журнала")
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

// Флеш всплывает на то, что случилось при открытом окне: хвост журнала,
// который сервер отдаёт при подключении, не всплывает, и на открытой ленте
// событие тоже не дублируется, там оно и так дописывается строкой.
func TestStaticFlashWorthy(t *testing.T) {
	heads := []string{"function flashWorthy("}
	cases := []struct {
		expr string
		want string
	}{
		{`flashWorthy({time: "2026-08-11T10:20:00"}, "2026-08-11T10:00:00", false)`, "true"},
		{`flashWorthy({time: "2026-08-11T09:20:00"}, "2026-08-11T10:00:00", false)`, "false"},
		{`flashWorthy({time: "2026-08-11T10:20:00"}, "2026-08-11T10:00:00", true)`, "false"},
		{`flashWorthy({}, "2026-08-11T10:00:00", false)`, "false"},
		{`flashWorthy(null, "", false)`, "false"},
	}
	for _, c := range cases {
		if got := jsEval(t, heads, c.expr); got != c.want {
			t.Errorf("%s: %q, жду %q", c.expr, got, c.want)
		}
	}
}

// Флеш-уведомление собрано по макету «05 Лента»: полоска остатка времени,
// не больше трёх штук с последним сверху, гаснет само, нажатие ведёт в ленту.
// Поток живёт отдельно от экранов, иначе уведомление приходило бы только там,
// где оно и так видно.
func TestStaticFlashNotice(t *testing.T) {
	app := readFile(t, filepath.Join("static", "app.js"))
	show := funcBody(t, app, "function showFlash(")
	// Каркас карточки (крестик, таймер жизни, потолок числа) общий с ответом на
	// нажатие и лежит в toast: событию остаётся своё содержимое, смахивание и
	// переход в ленту.
	card := funcBody(t, app, "function toast(")
	for _, want := range []string{`el("div", "flife")`, "animationDuration", "box.prepend(card)",
		"FLASH_MAX", "card.remove()", "setTimeout"} {
		if !strings.Contains(card, want) {
			t.Errorf("в каркасе карточки нет %q", want)
		}
	}
	for _, want := range []string{"FLASH_LIFE", `"/feed"`} {
		if !strings.Contains(show, want) {
			t.Errorf("во флеш-уведомлении нет %q", want)
		}
	}
	if !strings.Contains(card, `icon("close")`) || !strings.Contains(card, `aria-label", "Закрыть"`) {
		t.Error("у флеша нет крестика закрытия")
	}
	if !strings.Contains(card, "ev.stopPropagation()") {
		t.Error("крестик флеша не гасит клик по карточке: нажатие на него заодно уведёт в ленту")
	}
	for _, want := range []string{"pointerdown", "pointermove", "pointerup", "pointercancel", "flashSwiped(dx)"} {
		if !strings.Contains(show, want) {
			t.Errorf("во флеше нет смахивания %q", want)
		}
	}
	wire := funcBody(t, app, "function wireFlash(")
	for _, want := range []string{"/api/notifications?stream=1", "flashWorthy(n, flashSince, route().feed)",
		"showBellDot"} {
		if !strings.Contains(wire, want) {
			t.Errorf("в потоке флеша нет %q", want)
		}
	}
	if strings.Contains(wire, "agentLive.push") {
		t.Error("поток флеша закрывается вместе с экраном: уведомление придёт только на ленте")
	}
	if !strings.Contains(app, "\nwireFlash();") {
		t.Error("поток флеша не поднимается при старте страницы")
	}
	page := readFile(t, filepath.Join("static", "index.html"))
	if !strings.Contains(page, `id="flashes"`) {
		t.Error("в static/index.html нет угла для флеш-уведомлений")
	}
	css := readFile(t, filepath.Join("static", "style.css"))
	for _, want := range []string{".flashes{", ".flash{", ".flife{", ".flash .nx{"} {
		if !strings.Contains(css, want) {
			t.Errorf("в static/style.css нет стиля флеша %q", want)
		}
	}
}

// Смахивание отличают от дрожания пальца при тапе: короткий сдвиг остаётся
// тапом и ведёт в ленту (проверено в TestStaticFlashNotice), дальний
// закрывает карточку.
func TestStaticFlashSwiped(t *testing.T) {
	heads := []string{"function flashSwiped("}
	cases := []struct {
		expr string
		want string
	}{
		{"flashSwiped(0)", "false"},
		{"flashSwiped(47)", "false"},
		{"flashSwiped(-47)", "false"},
		{"flashSwiped(48)", "true"},
		{"flashSwiped(-60)", "true"},
	}
	for _, c := range cases {
		if got := jsEval(t, heads, c.expr); got != c.want {
			t.Errorf("%s: %q, жду %q", c.expr, got, c.want)
		}
	}
}

// На узком экране флеш занимает всю ширину, и три карточки подряд закрывают
// собой рабочий экран целиком, а единственный способ их убрать (по DK-282,
// до этой задачи) уводил бы с того места, где человек работал. Держит это
// одна видимая карточка: очередь в DOM остаётся прежней (не больше трёх), но
// лишние скрыты, пока верхнюю не закроют или она не погаснет сама.
func TestStaticFlashNarrowQueue(t *testing.T) {
	css := readFile(t, filepath.Join("static", "style.css"))
	narrow := funcBody(t, css, "@media (max-width:900px){")
	if !strings.Contains(narrow, ".flash:not(:first-child){display:none}") {
		t.Error("на узком экране флеш не сведён к одной карточке")
	}
	if !strings.Contains(narrow, ".flash .nx{") {
		t.Error("крестик флеша на узком экране остался мимо пальца в 44 пикселя")
	}
}

// Заголовок ленты не пересказывает её состав: что в неё попадает, говорит
// значок информации, а колокольчик и значки событий рисуются копией из
// разметки, а не рамками стилей.
func TestStaticFeedHeadAndIcons(t *testing.T) {
	app := readFile(t, filepath.Join("static", "app.js"))
	feed := funcBody(t, app, "function renderFeed(")
	for _, want := range []string{"На этом экране отображаются все уведомления от агентов", "tipq", "tipbox"} {
		if !strings.Contains(feed, want) {
			t.Errorf("в шапке ленты нет %q", want)
		}
	}
	if strings.Contains(feed, "уведомления машины: стопы работ") {
		t.Error("заголовок ленты снова пересказывает её состав подписью")
	}
	page := readFile(t, filepath.Join("static", "index.html"))
	for _, want := range []string{`id="icons"`, `data-ico="i-stop"`, `data-ico="i-wait"`,
		`data-ico="i-done"`, `data-ico="close"`, "<svg viewBox=\"0 0 24 24\""} {
		if !strings.Contains(page, want) {
			t.Errorf("в static/index.html нет значка %q", want)
		}
	}
	if !strings.Contains(funcBody(t, app, "function icon("), `[data-ico="`) {
		t.Error("значок не берётся копией из разметки")
	}
}
