package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
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

// Имя tmux сворачивается по последней записи всего реестра, а сессии без
// транскрипта в списке не показываются (живой случай chat-DK-397-2): клиент за
// диалогами доверия пересоздаёт сессию, имя переиспользуется между заходами и
// проектами, и старая запись держала живое имя, отчего мёртвый разговор
// выглядел живым и ловился по ?tmux= вместо нового.
func TestChatListTmuxNameClaimedByLatest(t *testing.T) {
	e, c := chatEnv(t)
	writeScript(t, e.bin, "tmux", `case "$1" in
ls) echo "chat-X|1|123";;
esac
exit 0`)
	old := time.Now().Add(-2 * time.Hour)
	writeSession(t, e.home, e.proj, "", "aaaa-0001", plainTalk, old)
	writeSession(t, e.home, e.proj, "", "bbbb-0002", plainTalk, time.Now())
	reg := func(stamp, sid, tmux string) string {
		return stamp + " сессия " + sid + " задача - проект demo дерево " + e.proj +
			" транскрипт /tmp/" + sid + ".jsonl источник заказ повод startup tmux " + tmux + "\n"
	}
	writeBinds(t, e.home,
		reg("2026-08-20T10:00:00", "aaaa-0001", "chat-X"),
		reg("2026-08-22T10:00:00", "bbbb-0002", "chat-X"),
		// Эфемерная запись без транскрипта: клиент пересоздал сессию за
		// диалогом доверия, файла разговора у неё нет.
		reg("2026-08-22T10:00:01", "cccc-0003", "-"))
	list := chatsOf(t, e, c)
	byID := map[string]chatEntry{}
	for _, ch := range list {
		byID[ch.ID] = ch
	}
	if _, ghost := byID["cccc-0003"]; ghost {
		t.Fatalf("сессия без транскрипта встала в список: %+v", list)
	}
	if got := byID["bbbb-0002"]; got.Tmux != "chat-X" || got.State != chatLive {
		t.Errorf("свежая запись не владеет именем: %+v", got)
	}
	if got := byID["aaaa-0001"]; got.Tmux != "" || got.State == chatLive {
		t.Errorf("устаревшая запись держит имя и живость: %+v", got)
	}
	// Поиск по имени tmux находит одну сессию, владельца имени: по нему
	// дашборд пришивает свежеподнятый чат, и старый тут был бы чужим.
	resp := doReq(t, c, "GET", e.srv.URL+"/api/projects/demo/chats?tmux=chat-X", "")
	text := body(t, resp)
	var got struct {
		Chats []chatEntry `json:"chats"`
	}
	if err := json.Unmarshal([]byte(text), &got); err != nil {
		t.Fatalf("ответ не разобрался (%v): %s", err, text)
	}
	if len(got.Chats) != 1 || got.Chats[0].ID != "bbbb-0002" {
		t.Fatalf("по имени tmux нашёлся не владелец: %s", text)
	}
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

// Разговор о задаче чужой доски подписан словами: сессия соседнего проекта
// попадает в список по общему каталогу транскриптов, и без подписи её задача
// читалась бы как задача этой доски. Считалось это и раньше, но наружу шло
// только списком работ, а панель разговора подписи не видела вовсе.
func TestChatListSignsForeignTask(t *testing.T) {
	e, c := chatEnv(t)
	writeSession(t, e.home, e.proj, "-dk-9", "aaaa-1111", plainTalk, time.Now())
	sideTree(t, e.proj, "dk-9")

	resp := doReq(t, c, "GET", e.srv.URL+"/api/projects/demo/chats", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("список чатов: %d", resp.StatusCode)
	}
	var got struct {
		Chats []chatEntry `json:"chats"`
	}
	if err := json.Unmarshal([]byte(body(t, resp)), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Chats) != 1 {
		t.Fatalf("чатов в списке %d, ждал один", len(got.Chats))
	}
	if got.Chats[0].Note != foreignTaskNote {
		t.Fatalf("подпись чужой задачи %q, ждал %q", got.Chats[0].Note, foreignTaskNote)
	}
}

// Работа своей доски подписи не просит: заголовок разговора говорит про неё
// больше, чем «по дереву задачи», и вторая надпись в шапке была бы шумом.
func TestChatListKeepsOwnTaskUnsigned(t *testing.T) {
	e, c := chatEnv(t)
	writeSession(t, e.home, e.proj, "-xr-4", "aaaa-2222", plainTalk, time.Now())
	sideTree(t, e.proj, "xr-4")

	resp := doReq(t, c, "GET", e.srv.URL+"/api/projects/demo/chats", "")
	var got struct {
		Chats []chatEntry `json:"chats"`
	}
	if err := json.Unmarshal([]byte(body(t, resp)), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Chats) != 1 || got.Chats[0].Note != "" {
		t.Fatalf("своя задача подписана лишним: %+v", got.Chats)
	}
}

// Реплика в чат, за которым нет ни живого процесса, ни транскрипта, поднимает
// сессию заказом. Так живёт произвольный чат: сессия у него рождается первой же
// репликой человека, а до этой ветки ручка отвечала «разговаривать не с кем»,
// реплика ложилась в журнал отправленного без адресата, и молчание было
// неотличимо от работы (жалоба пользователя: три реплики за два дня без ответа).
func TestChatSayRaisesSessionWithoutTranscript(t *testing.T) {
	e, c := chatEnv(t)
	tmuxLog := filepath.Join(e.home, "tmux.log")
	writeScript(t, e.bin, "tmux", `echo "$@" >> "`+tmuxLog+`"
case "$1" in
ls) exit 1;;
esac
exit 0`)
	writeScript(t, e.bin, "claude", "exit 0")

	sid := "aaaa-1111-2222-3333"
	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/say",
		`{"text": "почему подписка тратится медленнее"}`)
	text := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("реплика в чат без сессии: %d %s", resp.StatusCode, text)
	}
	var got struct {
		Way  string `json:"way"`
		Tmux string `json:"tmux"`
	}
	if err := json.Unmarshal([]byte(text), &got); err != nil {
		t.Fatal(err)
	}
	if got.Way != "start" {
		t.Fatalf("дорога реплики %q, ждал подъём сессии: %s", got.Way, text)
	}
	if got.Tmux == "" {
		t.Fatal("подъём не назвал tmux-сессию: панели некуда переезжать")
	}
	log := readFile(t, tmuxLog)
	if !strings.Contains(log, "new-session") {
		t.Fatalf("сессию не поднимали: %s", log)
	}
	if !strings.Contains(log, "почему подписка тратится медленнее") {
		t.Fatalf("реплика человека не уехала заказом: %s", log)
	}
	if strings.Contains(log, "--resume") {
		t.Fatalf("подъём пошёл резюмом несуществующей сессии: %s", log)
	}
	// Правило плана приезжает тем же заказом, как у любого подъёма дашборда.
	if !strings.Contains(log, "план работ файлом") {
		t.Fatalf("в заказе нет правила плана: %s", log)
	}
}

// Реплику взял сокет, а хода ей не дали: клиент стоит на вопросе разрешения в
// своём окне. Ручка называет это в ответе, и панель рисует пузырь «не
// доставлено» с причиной, а не благополучно отправленным.
func TestChatSayNamesStuckPermission(t *testing.T) {
	e, c := chatEnv(t)
	sid := "bbbb-2222-3333-4444"
	writeSession(t, e.home, e.proj, "", sid, plainTalk, time.Now())
	writeScript(t, e.bin, "tmux", `case "$1" in
ls) printf 'chat-1\t1\t1754770421\n';;
esac
exit 0`)
	// Реестр держит имя живой tmux-сессии чата: реплика пойдёт в неё.
	writeBinds(t, e.home, "2026-08-20T12:00:00 сессия "+sid+
		" задача - проект demo дерево "+e.proj+" транскрипт /tmp/t.jsonl "+
		"источник заказ повод startup tmux chat-1\n")
	writeNotifyLog(t, e.home, []string{permissionNotify(sid)})

	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/say",
		`{"text": "почему не ответил"}`)
	text := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("реплика в живую сессию: %d %s", resp.StatusCode, text)
	}
	if !strings.Contains(text, `"stuck"`) {
		t.Fatalf("ручка не сказала, что реплика легла в очередь вставшего клиента: %s", text)
	}
	if !strings.Contains(text, "ждёт разрешения") {
		t.Fatalf("причина не названа словами: %s", text)
	}

	// Ход кончился, значит вопрос закрыт: свежее событие снимает пометку.
	writeNotifyLog(t, e.home, []string{permissionNotify(sid),
		"2026-08-20T12:06:00 сессия " + sid[:8] +
			" повод turn_done уровень фоновый бэкенд terminal-notifier цель - задача - проект demo " +
			"код возврата: 0 текст «devkit: ход кончился» «готово»"})
	resp = doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/say",
		`{"text": "продолжай"}`)
	text = body(t, resp)
	if strings.Contains(text, `"stuck"`) {
		t.Fatalf("пометка очереди осталась после закрытого хода: %s", text)
	}
}

// permissionNotify это строка журнала уведомителя про запрос разрешения, как её
// пишет hooks/notify.py: ID сессии там обрезан до восьми знаков.
func permissionNotify(sid string) string {
	return "2026-08-20T12:05:00 сессия " + sid[:8] +
		" повод permission_prompt уровень громкий бэкенд terminal-notifier цель - задача - проект demo " +
		"код возврата: 0 текст «devkit: нужно разрешение» «Claude needs your permission»"
}

// Панель знает обе новости ручки: поднятую репликой сессию (переезжает в неё)
// и реплику, легшую в очередь вставшего клиента (пузырь с причиной).
func TestStaticPanelKnowsStartAndStuck(t *testing.T) {
	app := readFile(t, filepath.Join("static", "app.js"))
	for _, want := range []string{`if (r.body.stuck) echo.held(m, r.body.stuck);`,
		`if (r.body.way === "start")`, `chatWait(project, r.body.tmux)`,
		`m.state === "held" ? "не доставлено: "`} {
		if !strings.Contains(app, want) {
			t.Errorf("в static/app.js нет %q", want)
		}
	}
}

// Произвольный чат (кнопка «+» без задачи) поднимается тем же порядком, что
// задачный: дерево это корень проекта, задачи в окружении нет вовсе, правило
// плана и реплика человека едут заказом.
func TestChatStartWithoutTask(t *testing.T) {
	e, c := chatEnv(t)
	tmuxLog := filepath.Join(e.home, "tmux.log")
	writeScript(t, e.bin, "tmux", `echo "$@" >> "`+tmuxLog+`"
case "$1" in
ls) exit 1;;
esac
exit 0`)
	writeScript(t, e.bin, "claude", "exit 0")

	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats",
		`{"text": "подписка тратится медленнее, разберись"}`)
	text := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("подъём произвольного чата: %d %s", resp.StatusCode, text)
	}
	if !strings.Contains(text, `"tmux":"chat-1"`) {
		t.Fatalf("имя сессии не по образцу chat-<n>: %s", text)
	}
	log := readFile(t, tmuxLog)
	if !strings.Contains(log, "new-session") || !strings.Contains(log, "подписка тратится медленнее") {
		t.Fatalf("сессия поднята не с репликой человека: %s", log)
	}
	if strings.Contains(log, "DEVKIT_TASK") {
		t.Fatalf("у разговора без задачи в окружении встала задача: %s", log)
	}
	if !strings.Contains(log, "план работ файлом") {
		t.Fatalf("в заказе нет правила плана: %s", log)
	}
}

// Правило отзывчивости едет каждым заказом чата: подъёмом, резюмом и вводной
// продолжения. Повод живой: агент чата полчаса молча гонял поиск по всему
// дому, и с той стороны разговора это выглядело зависшей сессией, а не работой.
func TestChatPaceRuleInEveryOrder(t *testing.T) {
	pace := "отдавай субагенту"
	fresh := chatCmd("", "opus", "", "посмотри доску", execRotateDefault, nil, "agentctl")
	if !strings.Contains(fresh, pace) || !strings.Contains(fresh, "план работ файлом") {
		t.Errorf("в заказе подъёма нет правил хода: %s", fresh)
	}
	// У резюма текст это реплика человека, и правило плана к ней не цепляется:
	// план сессия уже ведёт. Отзывчивость нужна и тут, длинный разговор идёт
	// как раз резюмами.
	again := chatCmd("", "opus", "aaaa-1111", "и что вышло", execRotateDefault, nil, "agentctl")
	if !strings.Contains(again, pace) {
		t.Errorf("в резюмном заказе нет правила отзывчивости: %s", again)
	}
	if strings.Contains(again, "план работ файлом") {
		t.Errorf("правило плана уехало в реплику человека: %s", again)
	}
	if !strings.Contains(continuePrompt("XR-1", execRotateDefault), pace) {
		t.Errorf("во вводной продолжения нет правила отзывчивости: %s",
			continuePrompt("XR-1", execRotateDefault))
	}
}

// Правило ротации исполнителя едет заказом подъёма с числом порога: диспетчер
// держит одного субагента подолгу, контекст того распухает, и после порога
// следующее задание уходит свежему субагенту (DK-397). Число видно прямо в
// тексте заказа, отдельного экрана у порога нет.
func TestChatRotateRuleCarriesThreshold(t *testing.T) {
	fresh := chatCmd("", "opus", "", "посмотри доску", 640000, nil, "agentctl")
	if !strings.Contains(fresh, "640000") || !strings.Contains(fresh, "subagent_tokens") {
		t.Errorf("в заказе подъёма нет порога ротации: %s", fresh)
	}
	// У резюма текст это реплика человека, правила к ней не цепляются.
	again := chatCmd("", "opus", "aaaa-1111", "и что вышло", 640000, nil, "agentctl")
	if strings.Contains(again, "640000") {
		t.Errorf("правило ротации уехало в реплику человека: %s", again)
	}
	if got := continuePrompt("XR-1", 640000); !strings.Contains(got, "640000") {
		t.Errorf("во вводной продолжения нет порога ротации: %s", got)
	}
}

// Число порога едет от машинного конфига до текста заказа: раскладка с ключом
// exec_rotate_tokens приезжает ответом agentctl, и заказ нового чата называет
// её число, а не умолчание.
func TestChatOrderCarriesConfiguredThreshold(t *testing.T) {
	e, c := chatEnv(t)
	tmuxLog := filepath.Join(e.home, "tmux.log")
	writeScript(t, e.bin, "tmux", `echo "$@" >> "`+tmuxLog+`"
case "$1" in
ls) exit 1;;
esac
exit 0`)
	writeScript(t, e.bin, "claude", "exit 0")
	writeAgentctlFake(t, e.bin, `{"source": "фикстура", "exec_rotate_tokens": 777000, "harnesses": []}`)

	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats",
		`{"text": "разбери пачку"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("подъём чата: %d %s", resp.StatusCode, body(t, resp))
	}
	log := readFile(t, tmuxLog)
	if !strings.Contains(log, "777000") {
		t.Fatalf("в заказе не число порога из конфига: %s", log)
	}
}

// chatsOf читает список чатов проекта ответом ручки.
func chatsOf(t *testing.T, e *testEnv, c *http.Client) []chatEntry {
	t.Helper()
	resp := doReq(t, c, "GET", e.srv.URL+"/api/projects/demo/chats", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("список чатов: %d", resp.StatusCode)
	}
	var got struct {
		Chats []chatEntry `json:"chats"`
	}
	if err := json.Unmarshal([]byte(body(t, resp)), &got); err != nil {
		t.Fatal(err)
	}
	return got.Chats
}

// writePeer кладёт запись реестра живых сессий клиента: она и говорит, что у
// разговора есть процесс со своим сокетом.
func writePeer(t *testing.T, home, sid string, pid int) {
	t.Helper()
	dir := filepath.Join(home, ".claude", "sessions")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := fmt.Sprintf(`{"pid":%d,"sessionId":%q,"name":"devkit-1","kind":"interactive"}`, pid, sid)
	if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("%d.json", pid)), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Клин узнаётся по трём признакам разом: процесс клиента жив, tmux-сессии, в
// которой он поднят, больше нет, и транскрипт молчит дольше пары минут. Такой
// разговор выглядит живым (сокет берёт реплики и отвечает удачей), а хода в нём
// нет и не будет: клиент стоит на записи в исчезнувший терминал, очередь
// копится, человек видит вечное «работает» (инцидент с чатом DK-460).
func TestChatEntryWedgeWhenTerminalLost(t *testing.T) {
	e, c := chatEnv(t)
	now := time.Date(2026, 8, 22, 15, 0, 0, 0, time.UTC)
	e.s.now = func() time.Time { return now }
	sid := "dddd4444-4444-4444-8444-444444444444"
	// Транскрипт замер десять минут назад, а процесс жив: свой же и берём, он
	// точно есть.
	writeSession(t, e.home, e.proj, "", sid, plainTalk, now.Add(-10*time.Minute))
	writePeer(t, e.home, sid, os.Getpid())
	writeBinds(t, e.home, fmt.Sprintf("2026-08-22T14:40:00 сессия %s задача XR-1 проект demo "+
		"дерево %s транскрипт /tmp/t.jsonl источник заказ повод startup tmux chat-XR-1-1\n", sid, e.proj))

	// tmux-сессии нет: список пуст.
	writeTmuxFake(t, e.bin, filepath.Join(e.home, "tmux.log"), "")
	got := chatsOf(t, e, c)
	if len(got) != 1 {
		t.Fatalf("чатов в списке %d, ждал один: %+v", len(got), got)
	}
	if got[0].Stuck != stuckLostTermWord {
		t.Errorf("клин не узнан: stuck=%q, ждал %q", got[0].Stuck, stuckLostTermWord)
	}
	// Состояние остаётся живым нарочно: процесс жив, и врать про смерть нельзя,
	// а про клин сказано своим полем.
	if got[0].State != chatLive {
		t.Errorf("состояние разговора в клине %q, ждал %q", got[0].State, chatLive)
	}

	// Та же сессия при живой tmux это не клин, а обычная работа.
	writeTmuxFake(t, e.bin, filepath.Join(e.home, "tmux.log"), "chat-XR-1-1\t1\t1786000000\n")
	if got := chatsOf(t, e, c); got[0].Stuck != "" {
		t.Errorf("живой разговор назван клином: %q", got[0].Stuck)
	}

	// Свежий транскрипт это идущий ход, а не клин: tmux-сессии нет, но агент
	// только что писал, и снимать его нельзя.
	writeTmuxFake(t, e.bin, filepath.Join(e.home, "tmux.log"), "")
	writeSession(t, e.home, e.proj, "", sid, plainTalk, now.Add(-10*time.Second))
	if got := chatsOf(t, e, c); got[0].Stuck != "" {
		t.Errorf("свежий ход назван клином: %q", got[0].Stuck)
	}
}

// Недоставленные реплики едут вводной резюма: клин берёт их сокетом и кладёт в
// очередь, которая умирает вместе с процессом. Без этого человек пишет три
// реплики, снимает клин и получает ответ только на последнюю.
func TestLostSaidGoesIntoResume(t *testing.T) {
	if got := withLost(nil, "последняя"); got != "последняя" {
		t.Errorf("без потерянных реплик вводная поменялась: %q", got)
	}
	got := withLost([]string{"первая", "вторая"}, "третья")
	for _, want := range []string{"первая", "вторая", "третья", "не дошли"} {
		if !strings.Contains(got, want) {
			t.Errorf("во вводной резюма нет %q: %q", want, got)
		}
	}
	if strings.Index(got, "первая") > strings.Index(got, "третья") {
		t.Error("потерянные реплики встали после последней: порядок разговора сбит")
	}
}

// Снятие клина это отдельный род стопа, и род называет зовущий. Обычный стоп
// подаёт Escape в терминал, а у клина терминала нет: зависший процесс снимается
// сигналом, иначе разговор остаётся вечным (инцидент с чатом DK-460).
func TestChatStopKillsWedged(t *testing.T) {
	e, c := chatEnv(t)
	now := time.Date(2026, 8, 22, 15, 0, 0, 0, time.UTC)
	e.s.now = func() time.Time { return now }
	sid := "eeee5555-5555-4555-8555-555555555555"
	writeSession(t, e.home, e.proj, "", sid, plainTalk, now.Add(-10*time.Minute))
	writeBinds(t, e.home, fmt.Sprintf("2026-08-22T14:40:00 сессия %s задача XR-1 проект demo "+
		"дерево %s транскрипт /tmp/t.jsonl источник заказ повод startup tmux chat-XR-1-1\n", sid, e.proj))

	// Процесс-жертва настоящий: снятие проверяется тем, что он умер, а не тем,
	// что ручка так сказала.
	victim := exec.Command("sleep", "120")
	if err := victim.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { victim.Process.Kill(); victim.Wait() })
	writePeer(t, e.home, sid, victim.Process.Pid)

	// tmux-сессии нет: это и есть клин.
	writeTmuxFake(t, e.bin, filepath.Join(e.home, "tmux.log"), "")
	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/stop", `{"kill": true}`)
	text := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("снятие клина: %d %s", resp.StatusCode, text)
	}
	if !strings.Contains(text, `"way":"kill"`) {
		t.Errorf("ручка не назвала род стопа: %s", text)
	}
	done := make(chan error, 1)
	go func() { done <- victim.Wait() }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("зависший процесс не снят: он жив через пять секунд после стопа")
	}

	// Живая tmux-сессия это не клин, и снимать процесс отказываются словами:
	// ход там прерывается обычным стопом. Процесс для этой половины свой:
	// прежний снят, а мёртвого в реестре живых сессий уже нет.
	second := exec.Command("sleep", "120")
	if err := second.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { second.Process.Kill(); second.Wait() })
	writePeer(t, e.home, sid, second.Process.Pid)
	writeTmuxFake(t, e.bin, filepath.Join(e.home, "tmux.log"), "chat-XR-1-1\t1\t1786000000\n")
	resp = doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/stop", `{"kill": true}`)
	if text := body(t, resp); resp.StatusCode != http.StatusConflict || !strings.Contains(text, "это не клин") {
		t.Errorf("снятие живой сессии: %d %s, ждал отказ про живую tmux", resp.StatusCode, text)
	}
}

// Время разговора в списке это время последней содержательной реплики, а не
// время правки транскрипта. Файл трогает всякая служебщина (постановка реплики
// в очередь, отметки харнеса, служебные вставки, которые лента и так прячет), и
// по ней наверх списка всплывали разговоры, где месяц никто не писал (замечание
// пользователя). Стенд держит и порядок: сортировки в списке когда-то не было
// вовсе, он стоял так, как отдал обход каталога, то есть по тем же mtime.
func TestChatListTimeIsLastSaid(t *testing.T) {
	e, c := chatEnv(t)
	now := time.Date(2026, 8, 22, 19, 30, 0, 0, time.UTC)
	e.s.now = func() time.Time { return now }

	// Разговор, где последняя реплика вчера, а файл тронут только что: ровно
	// так выглядит чат после служебного касания.
	quiet := "aaaa1111-1111-4111-8111-111111111111"
	writeSession(t, e.home, e.proj, "", quiet, said("2026-08-21T10:00:00.123Z", "давний разговор")+
		queued("2026-08-22T19:25:52.000Z"), now.Add(-time.Minute))

	// Разговор, где вчера же говорили позже: он и должен стоять выше.
	later := "bbbb2222-2222-4222-8222-222222222222"
	writeSession(t, e.home, e.proj, "", later, said("2026-08-21T12:00:00.456Z", "разговор позже")+
		queued("2026-08-22T19:20:00.000Z"), now.Add(-2*time.Minute))

	// Живой разговор: реплика сегодня, ему и стоять первым.
	fresh := "cccc3333-3333-4333-8333-333333333333"
	writeSession(t, e.home, e.proj, "", fresh, said("2026-08-22T19:00:00.000Z", "сегодняшний разговор"),
		now.Add(-3*time.Minute))

	got := chatsOf(t, e, c)
	if len(got) != 3 {
		t.Fatalf("чатов в списке %d, ждал три: %+v", len(got), got)
	}
	want := map[string]string{
		quiet: "2026-08-21T10:00:00Z",
		later: "2026-08-21T12:00:00Z",
		fresh: "2026-08-22T19:00:00Z",
	}
	for _, e := range got {
		if want[e.ID] != e.Mtime {
			t.Errorf("время разговора %s %q, ждал %q: взято касание файла, а не реплика",
				e.ID[:8], e.Mtime, want[e.ID])
		}
	}
	order := []string{got[0].ID, got[1].ID, got[2].ID}
	if order[0] != fresh || order[1] != later || order[2] != quiet {
		t.Errorf("порядок списка %v: список стоит не по последней реплике", order)
	}
}

// said это обычная пара реплик транскрипта: слова человека и ответ агента.
func said(at, text string) string {
	return fmt.Sprintf(`{"type":"user","message":{"role":"user","content":%q},"timestamp":%q}`+"\n"+
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"ответ"}]},"timestamp":%q}`+"\n",
		text, at, at)
}

// queued это служебное касание транскрипта: постановка реплики в очередь и ход
// инструмента. Лента такие записи пузырём не показывает, и время разговора они
// двигать не должны.
func queued(at string) string {
	return fmt.Sprintf(`{"type":"queue-operation","operation":"enqueue","timestamp":%q}`+"\n"+
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash",`+
		`"input":{"command":"ls"}}]},"timestamp":%q}`+"\n", at, at)
}

// tailLine собирает запись транскрипта нужной роли: ими стенд складывает хвост,
// по которому дашборд решает, работает ли сессия по задаче.
func tailLine(role, text, at string) string {
	if role == "bash" {
		return fmt.Sprintf(`{"type":"assistant","message":{"role":"assistant","content":`+
			`[{"type":"tool_use","name":"Bash","input":{"command":%q}}]},"timestamp":%q}`+"\n", text, at)
	}
	if role == "assistant" {
		return fmt.Sprintf(`{"type":"assistant","message":{"role":"assistant","content":`+
			`[{"type":"text","text":%q}]},"timestamp":%q}`+"\n", text, at)
	}
	return fmt.Sprintf(`{"type":"user","message":{"role":"user","content":%q},"timestamp":%q}`+"\n", text, at)
}

// Сессия без привязки, которая по строке работает, считается её исполнителем, и
// строка перестаёт называться взятой в другом месте. Прежде отсутствие
// привязанной сессии объявлялось чужой машиной, и это было прямое враньё:
// человек открыл окно, сказал «test» и работал по задаче, а дашборд писал, что
// её ведут где-то ещё (жалоба пользователя на DK-481).
func TestBoardRunFindsWorkerWithoutBind(t *testing.T) {
	e, _, _ := runsEnv(t, "")
	now := time.Date(2026, 8, 22, 20, 0, 0, 0, time.UTC)
	e.s.now = func() time.Time { return now }
	sid := "ffff6666-6666-4666-8666-666666666666"
	writePeer(t, e.home, sid, os.Getpid())
	// Привязки у сессии нет вовсе: реестр пуст, задача в записи не названа.
	writeBinds(t, e.home, "")

	// Хвост, где ID звучит один раз: это разговор о задаче, а не работа по ней.
	writeSession(t, e.home, e.proj, "", sid,
		tailLine("user", "test", "2026-08-22T19:00:00Z")+
			tailLine("assistant", "а что там с XR-004, посмотреть?", "2026-08-22T19:01:00Z"),
		now.Add(-time.Minute))
	if got := boardRows(t, e)["XR-004"]; got.Run != runOther {
		t.Errorf("одного упоминания хватило на присвоение строки: run=%q", got.Run)
	}

	// Коммит с ID в subject: это работа, и строка получает исполнителя.
	writeSession(t, e.home, e.proj, "", sid,
		tailLine("user", "test", "2026-08-22T19:00:00Z")+
			tailLine("bash", `git commit -m "feat(dashboard): XR-004 первый кусок"`, "2026-08-22T19:05:00Z"),
		now.Add(-time.Minute))
	if got := boardRows(t, e)["XR-004"]; got.Run == runOther {
		t.Errorf("коммит по задаче не сделал сессию исполнителем: run=%q", got.Run)
	}

	// Два упоминания в репликах это тоже работа: человек ведёт задачу словами.
	writeSession(t, e.home, e.proj, "", sid,
		tailLine("user", "делаем XR-004", "2026-08-22T19:00:00Z")+
			tailLine("assistant", "по XR-004 осталось дописать стенд", "2026-08-22T19:02:00Z"),
		now.Add(-time.Minute))
	if got := boardRows(t, e)["XR-004"]; got.Run == runOther {
		t.Errorf("работа словами не сделала сессию исполнителем: run=%q", got.Run)
	}

	// Мёртвая сессия исполнителем не считается: работать ею уже некому.
	if err := os.RemoveAll(filepath.Join(e.home, ".claude", "sessions")); err != nil {
		t.Fatal(err)
	}
	if got := boardRows(t, e)["XR-004"]; got.Run != runOther {
		t.Errorf("исполнителем стала мёртвая сессия: run=%q", got.Run)
	}
}
