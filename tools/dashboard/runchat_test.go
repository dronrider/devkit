package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/dronrider/devkit/internal/sessions"
)

// Свёртка состояния строки по её рабочим сессиям (DoD DK-716). Ход идёт, если
// идёт хоть у одной; ждёт человека, если ждёт хоть одна и никто не работает;
// иначе строка простаивает. Прежде состояние бралось у одной работы, и вторая
// сессия той же строки была невидима.
func TestStateMarksFoldsOverWorkSessions(t *testing.T) {
	list := []Work{
		{ID: "XR-1", Via: workViaSession, Session: "a", Live: workIdle, Rows: []string{"XR-1", "XR-2"}},
		{ID: "XR-2", Via: workViaSession, Session: "b", Live: workBusy, Rows: []string{"XR-2"}},
		{ID: "XR-3", Via: workViaSession, Session: "c", Live: workWait, Rows: []string{"XR-3"}},
		{ID: "XR-3", Via: workViaSession, Session: "d", Live: workIdle, Rows: []string{"XR-3"}},
		// Разговор о задаче в свёртку не идёт: строку он не ведёт.
		{ID: "XR-4", Via: workViaSession, Session: "e", Live: workBusy, Talk: true},
	}
	want := map[string]string{"XR-1": workIdle, "XR-2": workBusy, "XR-3": workWait}
	if got := stateMarks(list); !reflect.DeepEqual(got, want) {
		t.Errorf("свёртка состояний %+v, ожидал %+v", got, want)
	}
	busy := busyMarks(list)
	if !busy["XR-2"] || busy["XR-1"] || busy["XR-3"] || busy["XR-4"] {
		t.Errorf("ход идёт не по тем строкам: %+v", busy)
	}
}

// Одна сессия ведёт несколько строк, и признак получает каждая: работа по
// задаче кончается своим порядком, а до того сессия работает по всем своим
// задачам (развилка 3 DK-716).
func TestRunMarksSpreadOverEveryRow(t *testing.T) {
	list := []Work{{ID: "XR-1", Via: workViaSession, Session: "a", Own: true,
		Tmux: "chat-XR-1-1", Live: workBusy, Rows: []string{"XR-1", "XR-2"}}}
	marks := runMarks(list)
	if marks["XR-1"] != runChat || marks["XR-2"] != runChat {
		t.Errorf("признак работы разошёлся по строкам одной сессии: %+v", marks)
	}
}

// Иконка чата ведёт в разговор с идущим ходом, а при нескольких в свежайший по
// последней реплике: прежде она открывала адрес задачи, и до живого разговора
// человек делал ещё один клик, выбирая его глазами по времени.
func TestChatMarksPicksLiveTurn(t *testing.T) {
	list := []Work{
		{ID: "XR-1", Via: workViaSession, Session: "старый", Live: workBusy, Moved: 100, Rows: []string{"XR-1"}},
		{ID: "XR-1", Via: workViaSession, Session: "свежий", Live: workBusy, Moved: 200, Rows: []string{"XR-1"}},
		{ID: "XR-1", Via: workViaSession, Session: "стоящий", Live: workIdle, Moved: 300, Rows: []string{"XR-1"}},
	}
	if got := chatMarks(list)["XR-1"]; got != "свежий" {
		t.Errorf("иконка чата ведёт в %q, ожидал свежайший разговор с ходом", got)
	}
	// Работы за строкой нет вовсе: адрес задачи и есть правильный вход, он
	// откроет список её чатов или заведёт новый.
	if got := chatMarks([]Work{{ID: "XR-2", Via: workViaSession, Session: "z", Live: workDead}}); len(got) != 0 {
		t.Errorf("иконка чата ведёт в мёртвый разговор: %+v", got)
	}
}

// Останавливаются отсюда только разговоры с идущим ходом и своим окном:
// прерывать в стоящей работе нечего, а к чужому окну у дашборда нет клавиатуры.
func TestStoppableChatsPicksOurLiveWindows(t *testing.T) {
	rowed := []Work{
		{ID: "XR-1", Via: workViaSession, Session: "чужое-окно", Live: workBusy},
		{ID: "XR-1", Via: workViaSession, Session: "стоящий", Own: true, Tmux: "chat-XR-1-2", Live: workIdle},
		{ID: "XR-1", Via: workViaSession, Session: "старый", Own: true, Tmux: "chat-XR-1-3", Live: workWait, Moved: 100},
		{ID: "XR-1", Via: workViaSession, Session: "свежий", Own: true, Tmux: "chat-XR-1-4", Live: workBusy, Moved: 200},
	}
	got := stoppableChats(rowed)
	if len(got) != 2 || got[0].Session != "свежий" || got[1].Session != "старый" {
		t.Fatalf("список для выбора: %+v", got)
	}
	// Конвейер сюда не идёт: его снимают убийством сессии, а не Escape.
	pipe := []Work{{ID: "XR-1", Via: workViaTmux, Own: true, Tmux: "task-XR-1", Live: workBusy}}
	if got := stoppableChats(pipe); len(got) != 0 {
		t.Errorf("конвейер попал в список прерывания хода: %+v", got)
	}
}

// chatWorkEnv поднимает стенд, где строку XR-004 ведёт разговор в нашем окне:
// свежий транскрипт, запись реестра о работе и живой ход.
func chatWorkEnv(t *testing.T, sid, tmux string) (*testEnv, *http.Client, string) {
	t.Helper()
	e, c, tmuxLog := runsEnv(t, "")
	now := time.Date(2026, 8, 10, 10, 0, 10, 0, time.UTC)
	e.s.now = func() time.Time { return now }
	writeSession(t, e.home, e.proj, "", sid, transcriptFixture, now)
	writeBinds(t, e.home, fmt.Sprintf(
		"2026-08-10T09:59:00 сессия %s задача XR-004 проект demo дерево %s "+
			"транскрипт /tmp/t.jsonl источник заказ повод startup tmux %s\n"+
			"2026-08-10T09:59:30 сессия %s задача XR-004 проект demo дерево %s "+
			"транскрипт - источник работа повод «agentctl stage XR-004 разработка» tmux -\n",
		sid, e.proj, tmux, sid, e.proj))
	return e, c, tmuxLog
}

// «Стоп» у работы, идущей в окне разговора: ход прерывается двумя Escape,
// привязка сессии к задаче снимается записью «снята», а в ленту разговора
// ложится строка о причине. Сессию при этом не убивают: у разговора память
// человека, и следующая реплика придёт в него же.
func TestRunStopInterruptsChatWork(t *testing.T) {
	sid := "dff98764-1111-4111-8111-111111111111"
	e, c, tmuxLog := chatWorkEnv(t, sid, "chat-XR-004-1")

	// Предусловие стенда: строка помечена работой в окне разговора, и ход в
	// ней идёт. Без него стенд проверял бы не ту ветку.
	if got := boardRows(t, e)["XR-004"]; got.Run != runChat || !got.RunBusy {
		t.Fatalf("строку не ведёт разговор с ходом: run=%q busy=%v", got.Run, got.RunBusy)
	}

	resp := doReq(t, c, "DELETE", e.srv.URL+"/api/projects/demo/runs/XR-004", "")
	text := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("стоп разговора: %d %s", resp.StatusCode, text)
	}
	if !strings.Contains(text, "chat-XR-004-1") || !strings.Contains(text, "прерван") {
		t.Errorf("ответ стопа не назвал прерванный разговор: %s", text)
	}
	log := readFile(t, tmuxLog)
	if strings.Count(log, "send-keys -t =chat-XR-004-1: Escape") != 2 {
		t.Errorf("ход прерван не двумя Escape: %s", log)
	}
	if strings.Contains(log, "kill-session") {
		t.Errorf("разговор снят целиком, а прерывать надо было ход: %s", log)
	}
	// Привязка снята именно у этой задачи: соседние задачи того же разговора
	// запись не трогает.
	recs := sessions.LoadAll(e.home)[sid]
	last := recs[len(recs)-1]
	if last.Source != sessions.ByOff || last.Task != "XR-004" {
		t.Fatalf("запись об окончании работы: источник %q, задача %q", last.Source, last.Task)
	}
	if sessions.WorksOn(recs, "XR-004") {
		t.Error("после стопа сессия по-прежнему считается работой задачи")
	}
	// Строка отдала «Стоп» обратно кнопке запуска.
	if got := boardRows(t, e)["XR-004"]; got.Run != "" {
		t.Errorf("после стопа строка осталась рабочей: run=%q", got.Run)
	}
	// Причина стоит в ленте самого разговора: агент вернётся в неё следующим
	// ходом и прочитает её там же, где читает всё остальное (дорога DK-728).
	said := readFile(t, saidFile(e.s.cfg.Home, saidSessionKey(sid)))
	if !strings.Contains(said, "остановлена человеком") {
		t.Errorf("лента разговора о стопе не узнала: %s", said)
	}
}

// Рабочих сессий у строки несколько: остановить чужую работу вслепую дороже,
// чем спросить, и стоп отвечает списком, а выбранную называет параметр.
func TestRunStopListsSeveralChatWorks(t *testing.T) {
	first := "dff98764-1111-4111-8111-111111111111"
	second := "eeee8888-8888-4888-8888-888888888888"
	e, c, tmuxLog := chatWorkEnv(t, first, "chat-XR-004-1")
	now := e.s.now()
	writeSession(t, e.home, e.proj, "", second, transcriptFixture, now)
	appendBinds(t, e.home, fmt.Sprintf(
		"2026-08-10T09:59:40 сессия %s задача XR-004 проект demo дерево %s "+
			"транскрипт /tmp/t2.jsonl источник заказ повод startup tmux chat-XR-004-2\n"+
			"2026-08-10T09:59:50 сессия %s задача XR-004 проект demo дерево %s "+
			"транскрипт - источник работа повод «taskctl move XR-004» tmux -\n",
		second, e.proj, second, e.proj))

	resp := doReq(t, c, "DELETE", e.srv.URL+"/api/projects/demo/runs/XR-004", "")
	text := body(t, resp)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("стоп при двух рабочих сессиях: %d %s", resp.StatusCode, text)
	}
	var got struct {
		Sessions []stopChat `json:"sessions"`
	}
	if err := json.Unmarshal([]byte(text), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Sessions) != 2 {
		t.Fatalf("список для выбора: %s", text)
	}
	if strings.Contains(readFile(t, tmuxLog), "Escape") {
		t.Errorf("ход прервали, не спросив, в какой сессии: %s", readFile(t, tmuxLog))
	}

	resp = doReq(t, c, "DELETE",
		e.srv.URL+"/api/projects/demo/runs/XR-004?session="+second, "")
	if text := body(t, resp); resp.StatusCode != http.StatusOK {
		t.Fatalf("стоп названной сессии: %d %s", resp.StatusCode, text)
	}
	log := readFile(t, tmuxLog)
	if !strings.Contains(log, "send-keys -t =chat-XR-004-2: Escape") {
		t.Errorf("прервали не названную сессию: %s", log)
	}
	if strings.Contains(log, "chat-XR-004-1: Escape") {
		t.Errorf("прервали заодно и соседнюю сессию: %s", log)
	}
	// Сессия, которой не называли, работой строки и осталась: стоп у одной не
	// снимает работу у другой.
	if !sessions.WorksOn(sessions.LoadAll(e.home)[first], "XR-004") {
		t.Error("стоп в одной сессии снял работу в соседней")
	}
}

// Сессия, названная параметром, по строке не работает: молчаливо остановить
// первую попавшуюся хуже, чем сказать об этом словами.
func TestRunStopRejectsForeignSession(t *testing.T) {
	e, c, _ := chatWorkEnv(t, "dff98764-1111-4111-8111-111111111111", "chat-XR-004-1")
	resp := doReq(t, c, "DELETE", e.srv.URL+"/api/projects/demo/runs/XR-004?session=чужая", "")
	if text := body(t, resp); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("стоп чужой сессии: %d %s", resp.StatusCode, text)
	}
}

// Работы за строкой нет вовсе: останавливать нечего, и ручка говорит это
// словами. Дорога проверяется отдельно от прочих, потому что стоп теперь
// разбирает работы строки по её признаку, а не по одному полю ID.
func TestRunStopWithoutAnyWork(t *testing.T) {
	e, c, _ := runsEnv(t, "")
	resp := doReq(t, c, "DELETE", e.srv.URL+"/api/projects/demo/runs/XR-004", "")
	text := body(t, resp)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("стоп без работы: %d %s", resp.StatusCode, text)
	}
	if !strings.Contains(text, "не идёт") {
		t.Errorf("отказ не назвал причину словами: %s", text)
	}
}

// Стенд фронта: у строки, за которой работает окно разговора, стоит красный
// «Стоп» со своим исходом, иконка чата ведёт в разговор с идущим ходом, а при
// нескольких рабочих сессиях стоп спрашивает, в какой прервать ход. Проверка
// по тексту app.js тут не годится: разметку держал бы и прежний код, а человек
// на экране видел жёлтое «Продолжить». Стенд рисует колонку действий в
// поддельном DOM (testdata/poc_rowstop.mjs). Без node шаг пропускается: узел
// стенда, а не рабочей части.
func TestStaticRowStopInChatWindow(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node не найден: стенд кнопок строки пропущен")
	}
	out, err := exec.Command(node, filepath.Join("testdata", "poc_rowstop.mjs"),
		filepath.Join("static", "app.js")).CombinedOutput()
	if err != nil {
		t.Fatalf("кнопки строки с работой в окне разговора: %v\n%s", err, out)
	}
	t.Log(strings.TrimSpace(string(out)))
}

// Плашка «думает...» в панели чата гасилась только опросом /status, а тот
// считает занятость и по хвосту транскрипта, где незакрытый вызов инструмента
// висит до получаса (busyNow, sessions.go): после явного стопа хода плашка
// зависала на весь этот срок, хотя ответ самой ручки /stop уже сказал, что ход
// кончен (вторая приёмка DK-716, 2026-09-05). Стенд рисует панель чата в
// поддельном DOM (testdata/poc_chatstopbusy.mjs). Без node шаг пропускается:
// узел стенда, а не рабочей части.
func TestStaticChatStopClearsBusyPlate(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node не найден: стенд плашки стопа пропущен")
	}
	out, err := exec.Command(node, filepath.Join("testdata", "poc_chatstopbusy.mjs"),
		filepath.Join("static", "app.js")).CombinedOutput()
	if err != nil {
		t.Fatalf("плашка работы после стопа хода в панели чата: %v\n%s", err, out)
	}
	t.Log(strings.TrimSpace(string(out)))
}

// Живой разговор о строке едет отдельным признаком, а работой не становится
// (третья приёмка DK-716). Выбирается свежайшая говорящая сессия, работы в
// признак не попадают, а замолчавший разговор не попадает тоже: строке о нём
// сказать нечего.
func TestTalkMarksNamesLiveConversation(t *testing.T) {
	list := []Work{
		{ID: "XR-1", Via: workViaSession, Session: "старый", Live: workBusy, Moved: 100, Talk: true},
		{ID: "XR-1", Via: workViaSession, Session: "свежий", Live: workBusy, Moved: 200, Talk: true},
		{ID: "XR-1", Via: workViaSession, Session: "молчащий", Live: workIdle, Moved: 300, Talk: true},
		{ID: "XR-2", Via: workViaSession, Session: "работа", Live: workBusy, Rows: []string{"XR-2"}},
	}
	got := talkMarks(list)
	if got["XR-1"].Session != "свежий" {
		t.Errorf("признак разговора взят у сессии %q, ожидал свежайшую говорящую", got["XR-1"].Session)
	}
	if _, hit := got["XR-2"]; hit {
		t.Errorf("работа попала в признак разговора: %+v", got["XR-2"])
	}
	// Иконка чата от этого не поехала в разговор: у неё своя сторона того же
	// правила, и работой она считает работу.
	if _, hit := chatMarks(list)["XR-1"]; hit {
		t.Errorf("иконка чата взяла разговор за работу: %+v", chatMarks(list))
	}
}

// chatTalkEnv поднимает стенд, где по XR-004 говорит наш чат, а работы за
// строкой не заявлено: в реестре одна запись «заказ» от подъёма чата, команды
// доски не было. Это и есть живой случай третьей приёмки, человек попросил в
// чате продолжить работу.
func chatTalkEnv(t *testing.T, sid, tmux string) (*testEnv, *http.Client) {
	t.Helper()
	e, c, _ := runsEnv(t, "")
	now := time.Date(2026, 8, 10, 10, 0, 10, 0, time.UTC)
	e.s.now = func() time.Time { return now }
	writeSession(t, e.home, e.proj, "", sid, transcriptFixture, now)
	writeBinds(t, e.home, fmt.Sprintf(
		"2026-08-10T09:59:00 сессия %s задача XR-004 проект demo дерево %s "+
			"транскрипт /tmp/t.jsonl источник заказ повод startup tmux %s\n",
		sid, e.proj, tmux))
	return e, c
}

// Строка, по которой идёт живой разговор, говорит об этом отдельным признаком
// и работой не становится. Первое лечит мутность: человек попросил агента в
// чате продолжить работу, агент работал, а строка стояла такой же, какой была
// до просьбы. Второе держит защиту DK-460: чат, открытый по строке, не отбирает
// у неё кнопку запуска и не выглядит вторым исполнителем.
func TestBoardRowTalkStateWithoutWork(t *testing.T) {
	sid := "dff98764-1111-4111-8111-111111111111"
	e, c := chatTalkEnv(t, sid, "chat-XR-004-1")

	got := boardRows(t, e)["XR-004"]
	if got.TalkState != workBusy || got.TalkChat != sid {
		t.Fatalf("строка молчит о живом разговоре: talk_state=%q talk_chat=%q", got.TalkState, got.TalkChat)
	}
	if got.Run != "" || got.RunBusy || got.RunState != "" {
		t.Errorf("разговор присвоил строку: run=%q busy=%v state=%q", got.Run, got.RunBusy, got.RunState)
	}
	// Экран задачи судит о строке тем же способом, что список: пустая полоса
	// действий на форме и молчащая строка это одна и та же слепота.
	text := body(t, doReq(t, c, "GET", e.srv.URL+"/api/projects/demo/tasks/XR-004", ""))
	if !strings.Contains(text, `"talk_state":"busy"`) || !strings.Contains(text, `"talk_chat":"`+sid+`"`) {
		t.Errorf("форма задачи о живом разговоре не узнала: %s", text)
	}
}

// Строка с настоящей работой о разговоре не говорит: о ходе там говорит признак
// работы, и второе слово о том же ходе читалось бы как вторая сессия.
func TestBoardRowTalkSilentUnderWork(t *testing.T) {
	sid := "dff98764-1111-4111-8111-111111111111"
	e, _, _ := chatWorkEnv(t, sid, "chat-XR-004-1")
	got := boardRows(t, e)["XR-004"]
	if got.Run != runChat || !got.RunBusy {
		t.Fatalf("предусловие: строку ведёт разговор с ходом, run=%q busy=%v", got.Run, got.RunBusy)
	}
	if got.TalkState != "" || got.TalkChat != "" {
		t.Errorf("у работающей строки встал второй признак хода: talk_state=%q talk_chat=%q",
			got.TalkState, got.TalkChat)
	}
}

// Живой случай третьей приёмки целиком. Чат по строке брал её этапом, стоп
// привязку снял, а потом человек попросил в том же чате продолжить работу.
// Записи о работе с тех пор нет, и строка до этой правки молчала о происходящем
// вовсе. Теперь она говорит про разговор, а работой по-прежнему не считается:
// снятую привязку возвращает команда доски, а не реплика.
func TestBoardRowTalkAfterStopRelease(t *testing.T) {
	sid := "dff98764-1111-4111-8111-111111111111"
	e, _, _ := chatWorkEnv(t, sid, "chat-XR-004-1")
	appendBinds(t, e.home, fmt.Sprintf(
		"2026-08-10T09:59:40 сессия %s задача XR-004 проект demo дерево %s "+
			"транскрипт - источник снята повод «стоп со строки» tmux -\n",
		sid, e.proj))

	got := boardRows(t, e)["XR-004"]
	if got.Run != "" || got.RunState != "" {
		t.Fatalf("снятая привязка оставила строку рабочей: run=%q state=%q", got.Run, got.RunState)
	}
	if got.TalkState != workBusy || got.TalkChat != sid {
		t.Errorf("строка молчит о разговоре после стопа: talk_state=%q talk_chat=%q",
			got.TalkState, got.TalkChat)
	}
}

// Стенд фронта: полоса действий формы задачи не пустеет на секции In progress и
// обещает то же, что строка списка, а живой разговор виден точкой, чипом и
// подсказкой запуска, кнопки при этом не отбирая. Проверка по тексту app.js тут
// не годится: условие ветки читалось бы и в прежнем коде, а человек на экране
// видел пустое место. Стенд рисует обе полосы в поддельном DOM
// (testdata/poc_rowtalk.mjs). Без node шаг пропускается: узел стенда, а не
// рабочей части.
func TestStaticTaskActionsMatchRow(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node не найден: стенд полосы действий пропущен")
	}
	out, err := exec.Command(node, filepath.Join("testdata", "poc_rowtalk.mjs"),
		filepath.Join("static", "app.js")).CombinedOutput()
	if err != nil {
		t.Fatalf("полоса действий формы и признак живого разговора: %v\n%s", err, out)
	}
	t.Log(strings.TrimSpace(string(out)))
}
