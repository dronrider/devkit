package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Разговор с любой живой сессией (DK-345): реплика уходит во вход
// .devkit/chat дерева работы с адресатом в строке, доставляет её подхват
// hooks/chat-in.py. Стенды на фикстурной доске (runsBoardJSON) и транскриптах
// из writeSession, боковое дерево задачи рисуется каталогом рядом с проектом.

const plainTalk = `{"type":"user","message":{"role":"user","content":"работа идёт"},"timestamp":"2026-08-17T10:00:01.000Z","gitBranch":"main"}` + "\n"

// Доска без набитых нулями номеров: хвост бокового дерева xr-4 узнаёт задачу
// XR-4 без правки номера, цель XR-100 остаётся для отказа своей ручкой.
const chatBoardJSON = `{"prefix":"XR","sections":[` +
	`{"key":"in-progress","title":"In progress","rows":[` +
	`{"id":"XR-4","title":"Начатая задача","type":"task","p":"P2","r":31,"r_parts":[25,3,1,0,2],"cost":"-","link":"-"},` +
	`{"id":"XR-100","title":"Цель: пробный цикл","type":"task","p":"P2","r":41,"r_parts":[25,9,3,0,4],"cost":"XL","link":"-"}]}]}`

// chatEnv поднимает окружение с доской XR и клиентом.
func chatEnv(t *testing.T) (*testEnv, *http.Client) {
	t.Helper()
	e := newTestEnv(t)
	writeScript(t, e.bin, "taskctl", fmt.Sprintf("echo '%s'", chatBoardJSON))
	return e, e.loggedClient(t)
}

// sideTree заводит боковое дерево задачи рядом с проектом и возвращает путь.
func sideTree(t *testing.T, proj, suffix string) string {
	t.Helper()
	tree := filepath.Join(filepath.Dir(proj), filepath.Base(proj)+"-"+suffix)
	if err := os.MkdirAll(tree, 0o755); err != nil {
		t.Fatal(err)
	}
	return tree
}

func postSessionMessage(t *testing.T, c *http.Client, e *testEnv, sid, text string) *http.Response {
	t.Helper()
	return doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/sessions/"+sid+"/message",
		fmt.Sprintf(`{"text": %q}`, text))
}

// Реплика в сессию задачи ложится в чат task-<ID> её бокового дерева,
// строкой с адресатом сессии и подписью дашборда, тем же форматом, что
// разбирает подхват.
func TestSessionMessageLandsInTaskChat(t *testing.T) {
	e, c := chatEnv(t)
	tree := sideTree(t, e.proj, "xr-4")
	writeSession(t, e.home, e.proj, "-xr-4", "aaaa-1111", plainTalk, time.Now())

	resp := postSessionMessage(t, c, e, "aaaa-1111", "привет исполнитель")
	text := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("отправка: %d %s", resp.StatusCode, text)
	}
	if !strings.Contains(text, "чат task-XR-4") || !strings.Contains(text, "подхват доставит") {
		t.Errorf("ответ не называет чат и доставку: %s", text)
	}
	src := readFile(t, filepath.Join(tree, ".devkit", "chat", "task-XR-4.in"))
	if !strings.Contains(src, ", сессии aaaa-1111, из дашборда: привет исполнитель") {
		t.Fatalf("во входе нет строки с адресатом и подписью:\n%s", src)
	}
	if strings.Contains(readFile(t, filepath.Join(e.proj, ".devkit", "chat", "sess-aaaa-1111.in")), "привет") {
		t.Errorf("реплика легла в личный разговор основного дерева, хотя у сессии есть задача")
	}
}

// Сессия без распознанной задачи получает личный разговор в главном дереве:
// он называется сессией, и реплика в нём адресуется ей же.
func TestSessionMessageLandsInPersonalChat(t *testing.T) {
	e, c := chatEnv(t)
	writeSession(t, e.home, e.proj, "", "bbbb-2222", plainTalk, time.Now())

	resp := postSessionMessage(t, c, e, "bbbb-2222", "чем занят")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("отправка: %d %s", resp.StatusCode, body(t, resp))
	}
	src := readFile(t, filepath.Join(e.proj, ".devkit", "chat", "sess-bbbb-2222.in"))
	if !strings.Contains(src, ", сессии bbbb-2222, из дашборда: чем занят") {
		t.Fatalf("в личном разговоре нет строки:\n%s", src)
	}
}

// Повтор того же текста второй строки не заводит: лежащая реплика уже ждёт
// сессию, и дубль приехал бы ей дважды.
func TestSessionMessageRepeatKeepsOneLine(t *testing.T) {
	e, c := chatEnv(t)
	writeSession(t, e.home, e.proj, "", "bbbb-2222", plainTalk, time.Now())
	for i := 0; i < 2; i++ {
		resp := postSessionMessage(t, c, e, "bbbb-2222", "один вопрос")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("отправка %d: %d %s", i, resp.StatusCode, body(t, resp))
		}
	}
	src := readFile(t, filepath.Join(e.proj, ".devkit", "chat", "sess-bbbb-2222.in"))
	if got := strings.Count(src, "один вопрос"); got != 1 {
		t.Fatalf("строк в разговоре %d, ожидалась одна:\n%s", got, src)
	}
}

// Сессия задачи-цели отправляется к ручке цели: у цели свой носитель,
// «Входящие» её файла, и второй вход рядом расколол бы разговор надвое.
func TestSessionMessageRefusesGoalSession(t *testing.T) {
	e, c := chatEnv(t)
	sideTree(t, e.proj, "xr-100")
	writeSession(t, e.home, e.proj, "-xr-100", "cccc-3333", plainTalk, time.Now())

	resp := postSessionMessage(t, c, e, "cccc-3333", "привет")
	text := body(t, resp)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("ожидался отказ цели: %d %s", resp.StatusCode, text)
	}
	if !strings.Contains(text, "ручкой") || !strings.Contains(text, "XR-100") {
		t.Errorf("отказ не называет ручку цели: %s", text)
	}
}

// Протухший транскрипт это честный «не идёт»: строка ляжет и дождётся хода,
// но обещать доставку сейчас ручка не вправе (та же честность, что у DK-319).
func TestSessionMessageAtStaleSessionSaysSo(t *testing.T) {
	e, c := chatEnv(t)
	old := time.Now().Add(-time.Hour)
	writeSession(t, e.home, e.proj, "", "dddd-4444", plainTalk, old)

	resp := postSessionMessage(t, c, e, "dddd-4444", "ты тут?")
	text := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("отправка: %d %s", resp.StatusCode, text)
	}
	if !strings.Contains(text, "не идёт") || !strings.Contains(text, "дождётся") {
		t.Errorf("ответ не называет стоящую сессию: %s", text)
	}
}

// Неизвестная сессия и удалённое боковое дерево отказываются словами: реплика
// без читателя лежала бы мёртвым грузом, и молчать об этом нельзя.
func TestSessionMessageRefusals(t *testing.T) {
	e, c := chatEnv(t)
	// Сессии с таким id среди транскриптов нет.
	resp := postSessionMessage(t, c, e, "no-such", "привет")
	if resp.StatusCode != http.StatusNotFound || !strings.Contains(body(t, resp), "не нашлась") {
		t.Errorf("неизвестная сессия: %d %s", resp.StatusCode, body(t, resp))
	}
	// Транскрипт есть, бокового дерева нет.
	writeSession(t, e.home, e.proj, "-xr-4", "eeee-5555", plainTalk, time.Now())
	resp = postSessionMessage(t, c, e, "eeee-5555", "привет")
	if resp.StatusCode != http.StatusBadGateway || !strings.Contains(body(t, resp), "дерево сессии") {
		t.Errorf("удалённое дерево: %d %s", resp.StatusCode, body(t, resp))
	}
}

// Замок разговора общий с подхватом (take_chat_lock в hooks/chat-in.py), и
// замок, занятый соседним прогоном, отдаёт 503 словами: реплика, написанная
// поверх чужой руки под замком, терялась бы. Замок ушёл, и реплика ложится
// следующим запросом, как у подхвата лежащую строку доезжает соседний прогон.
func TestSessionMessageRefusesBusyChat(t *testing.T) {
	e, c := chatEnv(t)
	writeSession(t, e.home, e.proj, "", "ffff-6666", plainTalk, time.Now())
	dir := filepath.Join(e.proj, ".devkit", "chat")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	hold, err := os.OpenFile(filepath.Join(dir, "sess-ffff-6666.lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(hold.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	resp := postSessionMessage(t, c, e, "ffff-6666", "под замком")
	text := body(t, resp)
	if resp.StatusCode != http.StatusServiceUnavailable {
		hold.Close()
		t.Fatalf("ожидался отказ 503 по занятому замку: %d %s", resp.StatusCode, text)
	}
	if !strings.Contains(text, "соседний прогон") {
		t.Errorf("отказ не называет соседний прогон: %s", text)
	}
	hold.Close()
	// Замок ушёл, та же реплика ложится без повторной попытки руками.
	resp = postSessionMessage(t, c, e, "ffff-6666", "под замком")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("после освобождения замка: %d %s", resp.StatusCode, body(t, resp))
	}
}

// Правило адресности (LLD DK-430, решение 2): ручка задачи кладёт безадресную
// строку во вход задачи основного чекаута. Дерево тут не выбирается, чекаут
// переживает боковое дерево, и сторожок читает оба места.

// chatParkedBoard это доска с задачей XR-7, припаркованной вопросом: ручка
// задачи и мера кончившегося разговора обе читают машинный разряд причины.
const chatParkedBoard = `{"prefix":"XR","sections":[` +
	`{"key":"in-progress","title":"In progress","rows":[` +
	`{"id":"XR-4","title":"Начатая задача","type":"task","p":"P2","r":31,"r_parts":[25,3,1,0,2],"cost":"-","link":"-"},` +
	`{"id":"XR-100","title":"Цель: пробный цикл","type":"task","p":"P2","r":41,"r_parts":[25,9,3,0,4],"cost":"XL","link":"-"}]},` +
	`{"key":"blocked","title":"Blocked","rows":[` +
	`{"id":"XR-7","title":"Спрашивает","block":"вопрос: какую схему брать","type":"task","p":"P2","r":31,"r_parts":[25,3,1,0,2],"cost":"-","link":"-"}]}]}`

func parkedEnv(t *testing.T) (*testEnv, *http.Client) {
	t.Helper()
	e := newTestEnv(t)
	writeScript(t, e.bin, "taskctl", fmt.Sprintf("echo '%s'", chatParkedBoard))
	return e, e.loggedClient(t)
}

func postTaskMessage(t *testing.T, c *http.Client, e *testEnv, id, text string) *http.Response {
	t.Helper()
	return doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/tasks/"+id+"/message",
		fmt.Sprintf(`{"text": %q}`, text))
}

// Реплика задаче ложится безадресной строкой в разговор task-<ID> основного
// чекаута: адресованную мёртвой сессии строку не взял бы никто, и припаркованная
// вопросом задача осталась бы спать.
func TestTaskMessageLandsUnaddressedInMainCheckout(t *testing.T) {
	e, c := parkedEnv(t)
	// Боковое дерево задачи есть, и реплика всё равно идёт в чекаут: дерево
	// сносится слиянием, а разговор задачи переживает его.
	side := sideTree(t, e.proj, "xr-7")

	resp := postTaskMessage(t, c, e, "XR-7", "бери схему из LLD")
	text := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("отправка: %d %s", resp.StatusCode, text)
	}
	if !strings.Contains(text, "припаркована вопросом") || !strings.Contains(text, "сторожка") {
		t.Errorf("ответ не называет парковку и пробуждение: %s", text)
	}
	src := readFile(t, filepath.Join(e.proj, ".devkit", "chat", "task-XR-7.in"))
	if !strings.Contains(src, ", из дашборда: бери схему из LLD") {
		t.Fatalf("во входе задачи нет строки с подписью дашборда:\n%s", src)
	}
	if strings.Contains(src, ", сессии ") {
		t.Fatalf("строка ушла с адресатом, а ответом задаче считается только безадресная:\n%s", src)
	}
	if _, err := os.Stat(filepath.Join(side, ".devkit", "chat", "task-XR-7.in")); err == nil {
		t.Errorf("реплика легла в боковое дерево, хотя ручка задачи пишет в чекаут")
	}
}

// Ручка сессии по-прежнему пишет адресно, и рядом с безадресной строкой задачи
// это видно в одном входе: правило выбирает ручка, а не догадка о живости.
func TestTaskAndSessionLinesDifferByAddressee(t *testing.T) {
	e, c := parkedEnv(t)
	sideTree(t, e.proj, "xr-4")
	writeSession(t, e.home, e.proj, "-xr-4", "aaaa-1111", plainTalk, time.Now())

	if resp := postSessionMessage(t, c, e, "aaaa-1111", "адресно"); resp.StatusCode != http.StatusOK {
		t.Fatalf("реплика сессии: %d %s", resp.StatusCode, body(t, resp))
	}
	if resp := postTaskMessage(t, c, e, "XR-4", "безадресно"); resp.StatusCode != http.StatusOK {
		t.Fatalf("реплика задаче: %d %s", resp.StatusCode, body(t, resp))
	}
	side := readFile(t, filepath.Join(e.proj+"-xr-4", ".devkit", "chat", "task-XR-4.in"))
	if !strings.Contains(side, ", сессии aaaa-1111, из дашборда: адресно") {
		t.Errorf("реплика сессии потеряла адресата:\n%s", side)
	}
	main := readFile(t, filepath.Join(e.proj, ".devkit", "chat", "task-XR-4.in"))
	if strings.Contains(main, ", сессии ") || !strings.Contains(main, "безадресно") {
		t.Errorf("реплика задаче ушла не безадресной:\n%s", main)
	}
}

// Повтор той же реплики второй строки не заводит: у ручки задачи то же правило,
// что у ручки сессии, и сторожок разбудил бы строку дважды.
func TestTaskMessageRepeatKeepsOneLine(t *testing.T) {
	e, c := parkedEnv(t)
	for i := 0; i < 2; i++ {
		resp := postTaskMessage(t, c, e, "XR-7", "один ответ")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("отправка %d: %d %s", i, resp.StatusCode, body(t, resp))
		}
	}
	src := readFile(t, filepath.Join(e.proj, ".devkit", "chat", "task-XR-7.in"))
	if got := strings.Count(src, "один ответ"); got != 1 {
		t.Fatalf("строк в разговоре %d, ожидалась одна:\n%s", got, src)
	}
}

// Строка цели и строка, которой на доске нет, отказываются словами: у цели свой
// носитель, а ответ несуществующей задаче лёг бы во вход, который никто не
// читает.
func TestTaskMessageRefusals(t *testing.T) {
	e, c := parkedEnv(t)
	resp := postTaskMessage(t, c, e, "XR-100", "привет")
	text := body(t, resp)
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(text, "ручкой") {
		t.Errorf("цель: %d %s", resp.StatusCode, text)
	}
	resp = postTaskMessage(t, c, e, "XR-999", "привет")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("строки нет на доске: %d %s", resp.StatusCode, body(t, resp))
	}
	resp = postTaskMessage(t, c, e, "XR-7", "   ")
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(body(t, resp), "пустое сообщение") {
		t.Errorf("пустой текст: %d", resp.StatusCode)
	}
}

// Мера кончившегося разговора машинная, и порог живости транскрипта в ней не
// участвует: живой разговор отвечает ручкой сессии, даже когда транскрипт молчит
// час.

func sessionReply(t *testing.T, c *http.Client, e *testEnv, sid string) (string, string) {
	t.Helper()
	resp := doReq(t, c, "GET", e.srv.URL+"/api/projects/demo/sessions/"+sid, "")
	text := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("шапка сессии: %d %s", resp.StatusCode, text)
	}
	var v struct {
		Head struct {
			Reply string `json:"reply"`
			Note  string `json:"replyNote"`
		} `json:"head"`
	}
	if err := json.Unmarshal([]byte(text), &v); err != nil {
		t.Fatalf("шапка не разобралась: %v", err)
	}
	return v.Head.Reply, v.Head.Note
}

func TestChatReplyStaysSessionWhileTranscriptIsSilent(t *testing.T) {
	e, c := parkedEnv(t)
	sideTree(t, e.proj, "xr-4")
	writeSession(t, e.home, e.proj, "-xr-4", "aaaa-1111", plainTalk, time.Now().Add(-time.Hour))
	if reply, note := sessionReply(t, c, e, "aaaa-1111"); reply != replyToSession || note != "" {
		t.Fatalf("молчащий транскрипт посчитан концом разговора: %q %q", reply, note)
	}
}

// Снесённое слиянием дерево это первый признак: разговора больше нет, и реплика
// уходит ручкой задачи.
func TestChatReplyGoesToTaskWhenTreeIsGone(t *testing.T) {
	e, c := parkedEnv(t)
	writeSession(t, e.home, e.proj, "-xr-4", "aaaa-1111", plainTalk, time.Now())
	reply, note := sessionReply(t, c, e, "aaaa-1111")
	if reply != replyToTask || !strings.Contains(note, "дерева сессии больше нет") {
		t.Fatalf("снесённое дерево не назвало ручку задачи: %q %q", reply, note)
	}
}

// Припаркованная вопросом задача это второй признак: заход после парковки точно
// кончился, и ответ идёт задаче.
func TestChatReplyGoesToTaskWhenRowIsParked(t *testing.T) {
	e, c := parkedEnv(t)
	sideTree(t, e.proj, "xr-7")
	writeSession(t, e.home, e.proj, "-xr-7", "bbbb-2222", plainTalk, time.Now())
	reply, note := sessionReply(t, c, e, "bbbb-2222")
	if reply != replyToTask || !strings.Contains(note, "припаркована вопросом") {
		t.Fatalf("парковка не назвала ручку задачи: %q %q", reply, note)
	}
}

// Третий признак: сессию поднимал дашборд, а её tmux-сессии в списке уже нет.
// Живое имя в списке разговор не хоронит.
func TestChatReplyReadsTmuxOfOrderedSession(t *testing.T) {
	e, c := parkedEnv(t)
	sideTree(t, e.proj, "xr-4")
	writeSession(t, e.home, e.proj, "-xr-4", "cccc-3333", plainTalk, time.Now())
	writeBinds(t, e.home, "2026-08-18T12:03:11 сессия cccc-3333 задача XR-4 проект demo "+
		"дерево /tmp транскрипт /tmp/t.jsonl источник заказ повод startup tmux chat-XR-4-1\n")
	reply, note := sessionReply(t, c, e, "cccc-3333")
	if reply != replyToTask || !strings.Contains(note, "chat-XR-4-1") {
		t.Fatalf("мёртвая tmux-сессия заказа не назвала ручку задачи: %q %q", reply, note)
	}
	// Та же запись с живым именем (фикстурный tmux стенда его печатает).
	writeBinds(t, e.home, "2026-08-18T12:03:11 сессия cccc-3333 задача XR-4 проект demo "+
		"дерево /tmp транскрипт /tmp/t.jsonl источник заказ повод startup tmux task-XR-5\n")
	if reply, note := sessionReply(t, c, e, "cccc-3333"); reply != replyToSession {
		t.Fatalf("живая tmux-сессия посчитана мёртвой: %q %q", reply, note)
	}
}

// Кончившийся разговор без задачи отвечать некуда: ручки задачи у него нет, и
// пустая ручка это погасший ввод панели (решение 6). Тем же выходит разговор
// задачи, уехавшей с доски: ручка задачи отбила бы такую реплику 404.
func TestChatReplyEmptyWithoutTask(t *testing.T) {
	e, c := parkedEnv(t)
	writeSession(t, e.home, e.proj, "-scratch", "dddd-4444", plainTalk, time.Now())
	reply, note := sessionReply(t, c, e, "dddd-4444")
	if reply != "" || !strings.Contains(note, "продолжить его некому") {
		t.Fatalf("разговор без задачи назвал ручку: %q %q", reply, note)
	}
}

func TestChatReplyEmptyWhenRowLeftTheBoard(t *testing.T) {
	e, c := parkedEnv(t)
	writeSession(t, e.home, e.proj, "-xr-777", "eeee-5555", plainTalk, time.Now())
	if reply, note := sessionReply(t, c, e, "eeee-5555"); reply != "" || note == "" {
		t.Fatalf("закрытая задача назвала ручку: %q %q", reply, note)
	}
}
