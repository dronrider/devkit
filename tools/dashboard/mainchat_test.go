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

	"github.com/dronrider/devkit/internal/mainchat"
)

// writeMainChat кладёт журнал назначений в дом стенда: ручка читает то, что
// пишет писатель, и литеральные строки теста разъехались бы с форматом при
// первой же правке Line.
func writeMainChat(t *testing.T, home, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(mainchat.Path(home)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mainchat.Path(home), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func taskChatGet(t *testing.T, c *http.Client, url string) (int, struct {
	ID       string             `json:"id"`
	Project  string             `json:"project"`
	Chat     string             `json:"chat"`
	Source   string             `json:"source"`
	Since    string             `json:"since"`
	Segments []mainchat.Segment `json:"segments"`
}) {
	t.Helper()
	resp := doReq(t, c, "GET", url, "")
	text := body(t, resp)
	var out struct {
		ID       string             `json:"id"`
		Project  string             `json:"project"`
		Chat     string             `json:"chat"`
		Source   string             `json:"source"`
		Since    string             `json:"since"`
		Segments []mainchat.Segment `json:"segments"`
	}
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("ответ ручки адреса не разобрался: %v: %s", err, text)
	}
	return resp.StatusCode, out
}

// Ручка адреса разрешает ID записи в носителя журналом: свёртка отдаёт
// действующее назначение, отрезки стоят порядком строк с закрытыми прежними
// носителями и открытым действующим (LLD DK-1422, решения 1 и 2).
func TestTaskChatHandleResolvesCarrier(t *testing.T) {
	e := newTestEnv(t)
	c := e.loggedClient(t)
	stamp := time.Date(2026, 10, 23, 21, 14, 0, 0, time.UTC)
	var lines string
	for i, b := range []mainchat.Bind{
		{Record: "XR-005", Chat: "chat-a", Project: "demo", Source: mainchat.ByHand},
		{Record: "XR-005", Chat: "chat-b", Project: "demo", Source: mainchat.ByMove, Prior: "chat-a"},
		{Record: "XR-005", Source: mainchat.ByOff},
		{Record: "XR-005", Chat: "chat-c", Project: "demo", Source: mainchat.ByWork},
	} {
		lines += mainchat.Line(stamp.Add(time.Duration(i)*time.Second), b)
	}
	writeMainChat(t, e.home, lines)
	code, got := taskChatGet(t, c, e.srv.URL+"/api/projects/demo/tasks/XR-005/chat")
	if code != http.StatusOK {
		t.Fatalf("ручка адреса ответила %d", code)
	}
	if got.Chat != "chat-c" || got.Source != mainchat.ByWork || got.Project != "demo" {
		t.Fatalf("действующее назначение прочиталось неверно: %+v", got)
	}
	want := []mainchat.Segment{
		{Chat: "chat-a", Source: mainchat.ByHand,
			Since: stamp.Format(mainchat.Stamp), Until: stamp.Add(time.Second).Format(mainchat.Stamp)},
		{Chat: "chat-b", Source: mainchat.ByMove,
			Since: stamp.Add(time.Second).Format(mainchat.Stamp),
			Until: stamp.Add(2 * time.Second).Format(mainchat.Stamp)},
		{Chat: "chat-c", Source: mainchat.ByWork,
			Since: stamp.Add(3 * time.Second).Format(mainchat.Stamp)},
	}
	if len(got.Segments) != len(want) {
		t.Fatalf("отрезков %d, жду %d: %+v", len(got.Segments), len(want), got.Segments)
	}
	for i := range want {
		if got.Segments[i] != want[i] {
			t.Errorf("отрезок %d: %+v, жду %+v", i, got.Segments[i], want[i])
		}
	}
}

// Записи без назначения ручка отдаёт пустым носителем: панель по нему
// показывает состояние и действие запуска, а не первый чат из списка. Просмотр
// при этом ничего не создаёт: журнала нет и после захода (DoD DK-1427).
func TestTaskChatHandleWithoutAssignment(t *testing.T) {
	e := newTestEnv(t)
	c := e.loggedClient(t)
	code, got := taskChatGet(t, c, e.srv.URL+"/api/projects/demo/tasks/XR-002/chat")
	if code != http.StatusOK {
		t.Fatalf("ручка адреса ответила %d", code)
	}
	if got.Chat != "" || got.Segments != nil {
		t.Fatalf("без журнала ручка назвала разговор: %+v", got)
	}
	// Чтение адреса журнала не заводит.
	if _, err := os.Stat(mainchat.Path(e.home)); !os.IsNotExist(err) {
		t.Fatalf("просмотр адреса завёл журнал: %v", err)
	}
	// Снятая запись читается так же пустым разговором: строка снятия в отрезки
	// не открывается, а состояние записи для панели то же, разговора нет.
	writeMainChat(t, e.home,
		mainchat.Line(time.Date(2026, 10, 23, 21, 14, 0, 0, time.UTC),
			mainchat.Bind{Record: "XR-002", Source: mainchat.ByOff}))
	code, got = taskChatGet(t, c, e.srv.URL+"/api/projects/demo/tasks/XR-002/chat")
	if code != http.StatusOK || got.Chat != "" || got.Segments != nil {
		t.Fatalf("снятая запись прочиталась разговором: %d %+v", code, got)
	}
	// Строки, которой нет, ручка не выдумывает: адрес даётся записям.
	resp := doReq(t, c, "GET", e.srv.URL+"/api/projects/demo/tasks/XR-777/chat", "")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("чужой ID ответил %d, жду 404", resp.StatusCode)
	}
}

// Назначение читается и из дома машины: журнал писали до подмены дома и пишут
// мимо дашборда, и адрес без второго дома смотрел бы в пустоту.
func TestTaskChatReadsSecondHome(t *testing.T) {
	e := newTestEnv(t)
	c := e.loggedClient(t)
	other := t.TempDir()
	was := realHomeFn
	realHomeFn = func() string { return other }
	t.Cleanup(func() { realHomeFn = was })
	writeMainChat(t, other, mainchat.Line(time.Date(2026, 10, 23, 21, 14, 0, 0, time.UTC),
		mainchat.Bind{Record: "XR-005", Chat: "chat-far", Project: "demo", Source: mainchat.ByHand}))
	code, got := taskChatGet(t, c, e.srv.URL+"/api/projects/demo/tasks/XR-005/chat")
	if code != http.StatusOK || got.Chat != "chat-far" {
		t.Fatalf("журнал дома машины не прочитался: %d %+v", code, got)
	}
}

// Закрытая запись сохраняет адрес: ручка читает её строку из архива наравне
// с доской, без журнала отдаёт пустого носителя, и панель открывает переписку
// прежней дорогой списка, а с журналом назначенного диспетчера (DK-1004,
// миграция прежних разговоров это DK-1432).
func TestTaskChatHandleClosedRow(t *testing.T) {
	e := newTestEnv(t)
	c := e.loggedClient(t)
	arch := "# Архив (префикс XR)\n\n| ID | Задача | Тип | P | Закрыто | Ссылка |\n" +
		"|--------|--------|-----|---|---------|--------|\n" +
		"| XR-009 | Закрытая с хвостом | bug | P1 | 2026-08-13 | [tasks/archive/2026/XR-009.md](tasks/archive/2026/XR-009.md) |\n"
	if err := os.WriteFile(filepath.Join(e.proj, "docs", "TASKS-archive.md"), []byte(arch), 0o644); err != nil {
		t.Fatal(err)
	}
	code, got := taskChatGet(t, c, e.srv.URL+"/api/projects/demo/tasks/XR-009/chat")
	if code != http.StatusOK || got.Chat != "" || got.Segments != nil {
		t.Fatalf("закрытая запись без журнала не прочиталась прежним путём: %d %+v", code, got)
	}
	writeMainChat(t, e.home, mainchat.Line(time.Date(2026, 10, 23, 21, 14, 0, 0, time.UTC),
		mainchat.Bind{Record: "XR-009", Chat: "tmux-task-XR-009", Project: "demo", Source: mainchat.ByWork}))
	code, got = taskChatGet(t, c, e.srv.URL+"/api/projects/demo/tasks/XR-009/chat")
	if code != http.StatusOK || got.Chat != "tmux-task-XR-009" {
		t.Fatalf("закрытая запись не получила носителя из журнала: %d %+v", code, got)
	}
}

// Старт конвейера пишет строку назначения: подъём назвал разговор записи
// основным, и адрес после пуска открывает именно его (LLD DK-1422). Повторный
// пуск дописывает строку, а не отказывает: у записи не больше одного основного,
// и повтор того же носителя инвариантом разрешён.
func TestRunStartAssignsMainChat(t *testing.T) {
	e, c, _ := runsEnv(t, "")
	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/runs", `{"id": "XR-002"}`)
	text := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("запуск задачи: %d %s", resp.StatusCode, text)
	}
	data := readFile(t, mainchat.Path(e.home))
	want := " назначение XR-002 разговор tmux-task-XR-002 проект demo источник работа прежде -"
	if !strings.Contains(data, want) {
		t.Fatalf("строка назначения не легла в журнал:\n%s\nждал вхождение %q", data, want)
	}
	// Запуск цели журнала не трогает: носителем у цели становится виток, и
	// строку его положит свой писатель (DK-1431).
	resp = doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/runs", `{"id": "XR-100"}`)
	text = body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("запуск цели: %d %s", resp.StatusCode, text)
	}
	if strings.Contains(readFile(t, mainchat.Path(e.home)), "XR-100") {
		t.Fatalf("запуск цели записал назначение витка:\n%s", readFile(t, mainchat.Path(e.home)))
	}
	if n := strings.Count(readFile(t, mainchat.Path(e.home)), "\n"); n != 1 {
		t.Fatalf("в журнале строк %d, жду одну", n)
	}
}

// Панель без назначения остаётся без реплики: клиент не заводит разговор
// первой строкой, пока журнала нет. Строки клиента сторожат сам экран.
func TestStaticUnassignedWay(t *testing.T) {
	app, err := os.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`kind: "unassigned"`,
		`"Начать работу"`,
		`"/tasks/" + encodeURIComponent(st.task) + "/chat"`,
	} {
		if !strings.Contains(string(app), want) {
			t.Errorf("в app.js нет %s", want)
		}
	}
	// Прежний выбор адреса кнопкой остался только у цели: строка задачи
	// открывается своим ID, и журнал назначений решает, какой разговор за ним.
	if strings.Count(string(app), "run_chat || row.talk_chat || row.id") != 1 {
		t.Error("выбор run_chat уехал из кнопки цели либо завёлся второй раз")
	}
}

// segmentLabel это отрезок ответа ручки вместе с подписью: роль и этап
// выводятся из записей реестра сессий носителя (DK-871), и читаются отдельно
// от полей журнала назначений.
type segmentLabel struct {
	Chat  string `json:"chat"`
	Role  string `json:"role"`
	Stage string `json:"stage"`
}

func segmentsLabeled(t *testing.T, c *http.Client, url string) []segmentLabel {
	t.Helper()
	resp := doReq(t, c, "GET", url, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("ручка адреса ответила %d", resp.StatusCode)
	}
	var got struct {
		Segments []segmentLabel `json:"segments"`
	}
	if err := json.Unmarshal([]byte(body(t, resp)), &got); err != nil {
		t.Fatalf("ответ ручки адреса не разобрался: %v", err)
	}
	return got.Segments
}

// Отрезки основной ленты подписаны ролями и этапами: роль выводится из
// поводов записей реестра сессий носителя за время отрезка, у оформления
// черновика отрезок это бывший свободный разговор, а отрезку без известной
// роли подпись говорит об этом словами. Смена этапов и ролей адрес записи не
// меняет: один основной чат проходит их без заведения новых (DK-871).
func TestTaskChatSegmentsLabeledByRoles(t *testing.T) {
	e := newTestEnv(t)
	c := e.loggedClient(t)
	stamp := time.Date(2026, 10, 23, 21, 14, 0, 0, time.UTC)
	at := func(add time.Duration) string { return stamp.Add(add).Format(mainchat.Stamp) }
	writeMainChat(t, e.home,
		mainchat.Line(stamp, mainchat.Bind{Record: "XR-005", Chat: "chat-free", Project: "demo", Source: mainchat.ByDecor})+
			mainchat.Line(stamp.Add(time.Minute), mainchat.Bind{Record: "XR-005", Chat: "tmux-task-XR-005", Project: "demo", Source: mainchat.ByWork, Prior: "chat-free"}))
	// Свободный разговор не работал: единственная его запись это рождение.
	// Конвейер прошёл постановку и разработку, и подпись отрезка берёт итог.
	writeBinds(t, e.home,
		at(0)+" сессия free-1 задача - проект demo дерево "+e.proj+" транскрипт - источник заказ повод startup tmux chat-free панель -\n"+
			at(time.Minute)+" сессия conv-1 задача XR-005 проект demo дерево - транскрипт - источник заказ повод taskctl run tmux task-XR-005 панель -\n"+
			at(2*time.Minute)+" сессия conv-1 задача XR-005 проект demo дерево - транскрипт - источник работа повод agentctl stage постановка tmux - панель -\n"+
			at(3*time.Minute)+" сессия conv-1 задача XR-005 проект demo дерево - транскрипт - источник работа повод agentctl stage разработка tmux - панель -\n")
	want := []segmentLabel{
		{Chat: "chat-free", Role: "свободный чат"},
		{Chat: "tmux-task-XR-005", Role: "исполнение", Stage: "разработка"},
	}
	got := segmentsLabeled(t, c, e.srv.URL+"/api/projects/demo/tasks/XR-005/chat")
	if len(got) != len(want) {
		t.Fatalf("отрезков %d, жду %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("отрезок %d: %+v, жду %+v", i, got[i], want[i])
		}
	}
	// Дальше задача ушла в ревью и проверку выката: подпись отрезка едет за
	// свежей ролью, а адрес записи остаётся тем же назначением, смена роли
	// и стадии второй основной чат не заводит.
	appendBinds(t, e.home,
		at(4*time.Minute)+" сессия conv-2 задача XR-005 проект demo дерево - транскрипт - источник заказ повод taskctl run tmux task-XR-005 панель -\n"+
			at(5*time.Minute)+" сессия conv-2 задача XR-005 проект demo дерево - транскрипт - источник работа повод agentctl stage ревью tmux - панель -\n"+
			at(6*time.Minute)+" сессия conv-2 задача XR-005 проект demo дерево - транскрипт - источник работа повод shipctl smoke XR-005 tmux - панель -\n")
	got = segmentsLabeled(t, c, e.srv.URL+"/api/projects/demo/tasks/XR-005/chat")
	if len(got) != 2 || got[1].Role != "проверка выката" {
		t.Fatalf("после проверки подпись отрезка не поехала за свежей ролью: %+v", got)
	}
	code, after := taskChatGet(t, c, e.srv.URL+"/api/projects/demo/tasks/XR-005/chat")
	if code != http.StatusOK || after.Chat != "tmux-task-XR-005" {
		t.Fatalf("смена этапов сменила адрес основного чата: %d %+v", code, after)
	}
}

// Отрезок без роли в реестре подписан неизвестной ролью словами: молчание
// было бы неотличимо от отсутствия подписи, и человек не отличил бы разбор
// от поломки.
func TestTaskChatSegmentUnknownRole(t *testing.T) {
	e := newTestEnv(t)
	c := e.loggedClient(t)
	stamp := time.Date(2026, 10, 23, 21, 14, 0, 0, time.UTC)
	writeMainChat(t, e.home, mainchat.Line(stamp,
		mainchat.Bind{Record: "XR-005", Chat: "chat-quiet", Project: "demo", Source: mainchat.ByHand}))
	writeBinds(t, e.home,
		stamp.Format(mainchat.Stamp)+" сессия quiet-1 задача XR-005 проект demo дерево "+e.proj+
			" транскрипт - источник заказ повод startup tmux chat-quiet панель -\n")
	got := segmentsLabeled(t, c, e.srv.URL+"/api/projects/demo/tasks/XR-005/chat")
	if len(got) != 1 || got[0].Role != "роль неясна" || got[0].Stage != "" {
		t.Fatalf("отрезок без роли не подписан словами: %+v", got)
	}
}

// Оформление черновика из свободного разговора назначает ему основной чат
// источником «оформление»: строка ложится в журнал, разговор открывается
// кнопкой записи, пока та лежит в накопителе без строки доски, и разработку
// ничего не поднимает (LLD DK-1422, решение 1).
func TestDraftDecorAssignsFreeChat(t *testing.T) {
	e, c, _ := tasksEnv(t)
	writeBinds(t, e.home, bindRecord(e.home, "2026-10-24T10:00:00", "free-1", "-", "рука"))

	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/drafts",
		`{"title": "Мысль из обсуждения", "prio": "mid", "chat": "free-1"}`)
	text := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("черновик из разговора: %d %s", resp.StatusCode, text)
	}
	if !strings.Contains(text, "разговор стал основным чатом записи") {
		t.Errorf("оформление молчит о назначении: %s", text)
	}
	data := readFile(t, mainchat.Path(e.home))
	want := " назначение XR-005 разговор free-1 проект demo источник оформление прежде -"
	if !strings.Contains(data, want) {
		t.Fatalf("строка источника «оформление» не легла в журнал:\n%s\nждал вхождение %q", data, want)
	}
	// Создание черновика разработку не запускает: журнал держит одну строку
	// и конвейер не называет.
	if n := strings.Count(data, "\n"); n != 1 {
		t.Fatalf("в журнале строк %d, жду одну", n)
	}
	code, got := taskChatGet(t, c, e.srv.URL+"/api/projects/demo/tasks/XR-005/chat")
	if code != http.StatusOK || got.Chat != "free-1" || got.Source != mainchat.ByDecor {
		t.Fatalf("адрес записи накопителя не решил в разговор: %d %+v", code, got)
	}
}

// Вторая запись из того же обсуждения получает свой основной чат: разговор
// служит основной лентой только одной записи, первое назначение сохраняется,
// а второй записи заводится незачатый разговор с итогом постановки и ссылкой
// на источник.
func TestDraftDecorSecondRecordGetsOwnChat(t *testing.T) {
	e, c, _ := tasksEnv(t)
	writeBinds(t, e.home, bindRecord(e.home, "2026-10-24T10:00:00", "free-1", "-", "рука"))

	for _, title := range []string{"Первая мысль", "Вторая мысль"} {
		resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/drafts",
			fmt.Sprintf(`{"title": %q, "prio": "mid", "chat": "free-1"}`, title))
		if text := body(t, resp); resp.StatusCode != http.StatusOK {
			t.Fatalf("черновик %s: %d %s", title, resp.StatusCode, text)
		}
	}
	binds := mainchat.Load(e.home)
	if first := binds["XR-005"]; first.Chat != "free-1" || first.Source != mainchat.ByDecor {
		t.Fatalf("первое назначение не сохранилось: %+v", first)
	}
	second := binds["XR-006"]
	if !strings.HasPrefix(second.Chat, "blank-") || second.Source != mainchat.ByDecor {
		t.Fatalf("у второй записи нет своего чата: %+v", second)
	}
	// Свой чат несёт итог постановки и ссылку на общее обсуждение, а сессии
	// за ним нет: разработку запись не запускает.
	store := readFile(t, filepath.Join(chatStoreDir(e.home), second.Chat+".json"))
	for _, want := range []string{"Вторая мысль",
		"Постановка выделена из общего обсуждения: #demo/chat/free-1",
		`"task":"XR-006"`, `"blank":true`} {
		if !strings.Contains(store, want) {
			t.Errorf("в своём чате второй записи нет %q: %s", want, store)
		}
	}
	// Кнопка каждой записи открывает её носителя.
	code, got := taskChatGet(t, c, e.srv.URL+"/api/projects/demo/tasks/XR-005/chat")
	if code != http.StatusOK || got.Chat != "free-1" {
		t.Fatalf("первая запись потеряла разговор: %d %+v", code, got)
	}
	code, got = taskChatGet(t, c, e.srv.URL+"/api/projects/demo/tasks/XR-006/chat")
	if code != http.StatusOK || got.Chat != second.Chat {
		t.Fatalf("вторая запись не получила свой чат адресом: %d %+v", code, got)
	}
}

// Не всякий разговор панели становится основным чатом: беседа чужой записи
// и посторонний ключ журналом не завладевают, а черновик записывается всё равно.
func TestDraftDecorSkipsNotFreeChat(t *testing.T) {
	e, c, _ := tasksEnv(t)
	writeBinds(t, e.home,
		bindRecord(e.home, "2026-10-24T10:00:00", "groom-1", "XR-001", "заказ"),
		bindRecord(e.home, "2026-10-24T10:01:00", "free-1", "-", "рука"))

	for _, chat := range []string{"groom-1", "no-such"} {
		resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/drafts",
			fmt.Sprintf(`{"title": "Мысль", "prio": "mid", "chat": %q}`, chat))
		text := body(t, resp)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("черновик с разговором %s: %d %s", chat, resp.StatusCode, text)
		}
		if !strings.Contains(text, "основным чатом черновика он не стал") {
			t.Errorf("отказ разговора %s молчит: %s", chat, text)
		}
	}
	if _, err := os.Stat(mainchat.Path(e.home)); !os.IsNotExist(err) {
		t.Fatalf("несвободный разговор завёл журнал:\n%s", readFile(t, mainchat.Path(e.home)))
	}
	for _, id := range []string{"XR-005", "XR-006"} {
		if !isFile(filepath.Join(e.proj, "docs", "tasks", "drafts", id+".md")) {
			t.Errorf("черновик %s не записался", id)
		}
	}
}

// Разговор чужого проекта основной лентой не становится: кнопка записи не
// открывает чужое обсуждение, журнал молчит, а черновик записывается всё равно.
func TestDraftDecorSkipsForeignProjectChat(t *testing.T) {
	e, c, _ := tasksEnv(t)
	foreign := fmt.Sprintf("2026-10-24T10:00:00 сессия free-2 задача - проект other "+
		"дерево /tmp/other транскрипт %s источник рука повод startup tmux -\n",
		standTranscript(e.home, "free-2"))
	writeBinds(t, e.home, foreign)

	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/drafts",
		`{"title": "Мысль", "prio": "mid", "chat": "free-2"}`)
	text := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("черновик с чужим разговором: %d %s", resp.StatusCode, text)
	}
	if !strings.Contains(text, "разговор free-2 из проекта other, основным чатом черновика он не стал") {
		t.Errorf("отказ чужому разговору молчит: %s", text)
	}
	if _, err := os.Stat(mainchat.Path(e.home)); !os.IsNotExist(err) {
		t.Fatalf("чужой разговор завёл журнал:\n%s", readFile(t, mainchat.Path(e.home)))
	}
	if !isFile(filepath.Join(e.proj, "docs", "tasks", "drafts", "XR-005.md")) {
		t.Errorf("черновик XR-005 не записался")
	}
}

// Назначение переживает смену вида записи: черновик, оформленный в задачу тем
// же ID, держит свой разговор, а начало работы называет носителем конвейер
// и оставляет отрезок оформления в истории ленты.
func TestDraftDecorSurvivesPromoteAndWork(t *testing.T) {
	e, c, _ := tasksEnv(t)
	writeBinds(t, e.home, bindRecord(e.home, "2026-10-24T10:00:00", "free-1", "-", "рука"))
	doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/drafts",
		`{"title": "Мысль из обсуждения", "prio": "mid", "chat": "free-1"}`).Body.Close()

	// Ворота add спрашивают у черновика раздел DoD: грумминг дописывает его
	// раньше оформления, и тест делает то же.
	draft := filepath.Join(e.proj, "docs", "tasks", "drafts", "XR-005.md")
	if err := os.WriteFile(draft, []byte("# Мысль из обсуждения\n\n## DoD\n\n- сделано\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Оформление в задачу: грумминг доводит taskctl add --id, ID не меняется.
	runTaskctl(t, e.proj, "add", "--id", "XR-005", "--title", "Мысль из обсуждения",
		"--rank", "25+1+1+0+0", "--accept", "agent")
	code, got := taskChatGet(t, c, e.srv.URL+"/api/projects/demo/tasks/XR-005/chat")
	if code != http.StatusOK || got.Chat != "free-1" || got.Source != mainchat.ByDecor {
		t.Fatalf("оформление в задачу потеряло назначение: %d %+v", code, got)
	}

	// Начало работы пишет свою строку тем же писателем, каким пишет старт
	// конвейера: адрес остаётся у записи, отрезок оформления закрывается
	// отрезком работы.
	if err := mainchat.Put(e.home, mainchat.Bind{
		Record: "XR-005", Chat: "tmux-task-XR-005", Project: "demo", Source: mainchat.ByWork}); err != nil {
		t.Fatal(err)
	}
	code, got = taskChatGet(t, c, e.srv.URL+"/api/projects/demo/tasks/XR-005/chat")
	if code != http.StatusOK || got.Chat != "tmux-task-XR-005" || got.Source != mainchat.ByWork {
		t.Fatalf("начало работы не сменило носителя: %d %+v", code, got)
	}
	if len(got.Segments) != 2 || got.Segments[0].Chat != "free-1" ||
		got.Segments[0].Source != mainchat.ByDecor || got.Segments[0].Until == "" {
		t.Fatalf("отрезок оформления не сохранился: %+v", got.Segments)
	}
}

// Незачатый носитель вырастает в сессию первой репликой, и адрес следует
// за ростом: строка журнала называет прежний ключ, а ручка отдаёт сессию.
func TestTaskChatFollowsGrownSeed(t *testing.T) {
	e, c, _ := tasksEnv(t)
	doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/drafts",
		`{"title": "Мысль", "prio": "mid"}`).Body.Close()
	seed := chatBlankID()
	st := chatStore{Blank: true, Born: time.Now().Unix(), Project: "demo", Task: "XR-005"}
	if err := e.s.chatStoreWrite(seed, st); err != nil {
		t.Fatal(err)
	}
	writeMainChat(t, e.home, mainchat.Line(time.Date(2026, 10, 24, 10, 0, 0, 0, time.UTC),
		mainchat.Bind{Record: "XR-005", Chat: seed, Project: "demo", Source: mainchat.ByDecor}))
	code, got := taskChatGet(t, c, e.srv.URL+"/api/projects/demo/tasks/XR-005/chat")
	if code != http.StatusOK || got.Chat != seed {
		t.Fatalf("незачатый носитель не прошёл адресом: %d %+v", code, got)
	}

	// Сессия выросла: адрес отдаёт её, а строка журнала остаётся как была.
	st.Grown = "sess-9"
	if err := e.s.chatStoreWrite(seed, st); err != nil {
		t.Fatal(err)
	}
	code, got = taskChatGet(t, c, e.srv.URL+"/api/projects/demo/tasks/XR-005/chat")
	if code != http.StatusOK || got.Chat != "sess-9" {
		t.Fatalf("выросший носитель не отдался сессией: %d %+v", code, got)
	}
	if data := readFile(t, mainchat.Path(e.home)); !strings.Contains(data, seed) {
		t.Fatalf("ручка адреса переписала журнал:\n%s", data)
	}
}

// Клиентская сторона оформления: форма черновика передаёт свободный разговор
// панели, и носитель-заготовка открывается экраном нового чата с итогом
// постановки, а не пустой лентой. Строки клиента сторожат сам экран.
func TestStaticDecorWay(t *testing.T) {
	app, err := os.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"chatDecorSource()",
		`chat: chatDecorSource()`,
		`c.id === st.main.chat && c.blank`,
	} {
		if !strings.Contains(string(app), want) {
			t.Errorf("в app.js нет %s", want)
		}
	}
}
