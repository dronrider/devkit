package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
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

// Общий список машины (?all=1): диалоги всех проектов в одном ответе, свежие
// сверху, у каждой строки назван её проект. Панель выбирает разговор из него,
// не меняя проекта доски; проектный список без параметра остаётся прежним.
func TestChatListAllProjects(t *testing.T) {
	e, c := chatEnv(t)
	other := filepath.Join(filepath.Dir(e.proj), "other")
	mkProject(t, other)
	// Метка последней реплики в транскрипте соседа свежее: порядок общего
	// списка считается по ней, а не по касанию файла.
	fresher := `{"type":"user","message":{"role":"user","content":"работа идёт"},"timestamp":"2026-08-18T10:00:01.000Z","gitBranch":"main"}` + "\n"
	writeSession(t, e.home, e.proj, "", "aaaa-0001", plainTalk, time.Now().Add(-time.Hour))
	writeSession(t, e.home, other, "", "bbbb-0002", fresher, time.Now())
	// Окно свежести тут снято ключом days=0: предмет этого стенда общий список
	// машины, а метки реплик в фикстурах старше окна по умолчанию.
	resp := doReq(t, c, "GET", e.srv.URL+"/api/projects/demo/chats?all=1&days=0", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("общий список: %d", resp.StatusCode)
	}
	var got struct {
		Chats []chatEntry `json:"chats"`
	}
	if err := json.Unmarshal([]byte(body(t, resp)), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Chats) != 2 {
		t.Fatalf("в общем списке не оба проекта: %+v", got.Chats)
	}
	if got.Chats[0].ID != "bbbb-0002" || got.Chats[0].Project != "other" {
		t.Errorf("свежий разговор соседнего проекта не первый или без имени проекта: %+v", got.Chats[0])
	}
	if got.Chats[1].ID != "aaaa-0001" || got.Chats[1].Project != "demo" {
		t.Errorf("разговор своего проекта без имени проекта: %+v", got.Chats[1])
	}
	// Первая реплика едет своим полем: заголовок бывает от харнеса, а панель
	// пришивает застрявший адрес new именно по первой реплике.
	if got.Chats[0].First != "работа идёт" {
		t.Errorf("первой реплики нет своим полем: %+v", got.Chats[0])
	}
	// Без ?all=1 ответ прежний: только свой проект и без поля project.
	own := chatsOf(t, e, c)
	if len(own) != 1 || own[0].ID != "aaaa-0001" || own[0].Project != "" {
		t.Errorf("проектный список изменился: %+v", own)
	}
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

// Команда чужой подписки несёт режим разрешений флагом: свежий профиль
// поднимает клиента в ручном режиме, и чат вставал с вопросом «Do you want to
// proceed?» на каждом инструменте (живой случай chat-13 на второй подписке).
// Явный флаг выравнивает поведение с чатами подписки по умолчанию и не зависит
// от содержимого чужого конфига; сам конфиг подписки дашборд не правит.
func TestChatCmdSecondHarnessCarriesAutoPermissionMode(t *testing.T) {
	h := &Harness{Name: "втораяtest", Bin: "клиент-2"}
	got := chatCmd("", "glm-5.3", "", "посмотри доску", execRotateDefault, h, "agentctl")
	if !strings.Contains(got, " --permission-mode auto") {
		t.Errorf("заказ второй подписки без режима разрешений: %s", got)
	}
	// Резюм идёт тем же клиентом и с тем же режимом.
	again := chatCmd("", "glm-5.3", "aaaa-1111", "и что вышло", execRotateDefault, h, "agentctl")
	if !strings.Contains(again, " --permission-mode auto") {
		t.Errorf("резюм второй подписки без режима разрешений: %s", again)
	}
	// Подписке по умолчанию флаг не ставится: её режим настраивает сам человек,
	// и дашборд в него не лезет.
	def := chatCmd("", "opus", "", "посмотри доску", execRotateDefault, nil, "agentctl")
	if strings.Contains(def, "--permission-mode") {
		t.Errorf("режим разрешений уехал в заказ подписки по умолчанию: %s", def)
	}
}

// Живой клиент, вставший на вопросе в своём терминале (разрешение, доверие
// каталогу первого запуска чужого профиля), виден в списке чатов своим словом:
// без него «чат молчит» неотличим от работы, а человеку нужен attach в tmux, а
// не снятие процесса (живой случай chat-13 на второй подписке).
func TestChatEntryAskWhenPermissionPrompt(t *testing.T) {
	e, c := chatEnv(t)
	now := time.Date(2026, 8, 23, 15, 0, 0, 0, time.UTC)
	e.s.now = func() time.Time { return now }
	sid := "eeee5555-5555-4555-8555-555555555555"
	writeSession(t, e.home, e.proj, "", sid, plainTalk, now.Add(-30*time.Second))
	writeBinds(t, e.home, fmt.Sprintf("2026-08-23T14:59:00 сессия %s задача XR-1 проект demo "+
		"дерево %s транскрипт /tmp/t.jsonl источник заказ повод startup tmux chat-13\n", sid, e.proj))
	writeTmuxFake(t, e.bin, filepath.Join(e.home, "tmux.log"), "chat-13\t1\t1786000000\n")
	writeNotifyLog(t, e.home, []string{permissionNotify(sid)})

	got := chatsOf(t, e, c)
	if len(got) != 1 {
		t.Fatalf("чатов в списке %d, ждал один: %+v", len(got), got)
	}
	if got[0].Stuck != stuckAskWord {
		t.Errorf("вопрос в терминале не узнан: stuck=%q, ждал %q", got[0].Stuck, stuckAskWord)
	}
	if got[0].Tmux != "chat-13" {
		t.Errorf("имя tmux потеряно, плашке некуда звать attach: %+v", got[0])
	}

	// Ход кончился, значит вопрос закрыт: свежее событие снимает слово.
	writeNotifyLog(t, e.home, []string{permissionNotify(sid),
		"2026-08-23T14:59:30 сессия " + sid[:8] +
			" повод turn_done уровень фоновый бэкенд terminal-notifier цель - задача - проект demo " +
			"код возврата: 0 текст «devkit: ход кончился» «готово»"})
	if got := chatsOf(t, e, c); got[0].Stuck != "" {
		t.Errorf("слово вопроса осталось после конца хода: %q", got[0].Stuck)
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
	if !strings.Contains(continuePrompt("XR-1", execRotateDefault, ""), pace) {
		t.Errorf("во вводной продолжения нет правила отзывчивости: %s",
			continuePrompt("XR-1", execRotateDefault, ""))
	}
}

// Команда чужой подписки несёт модель выбранным ярусом: клиент второй
// подписки принимает имя из своей лестницы, и сессия поднимается выбранной
// моделью, а не дефолтом профиля (DK-750, проба: заказ под agentctl exec
// отвечает и выходит нулём). Прежний пропуск флага держался на предупреждении
// unrecognized_model из панели DK-269, а оно оказалось жалобой клиента на
// незнание окна контекста и оставлено осознанно.
func TestChatCmdSecondHarnessCarriesModel(t *testing.T) {
	h := &Harness{Name: "втораяtest", Bin: "клиент-2"}
	got := chatCmd("", "glm-5.3-flash", "", "посмотри доску", execRotateDefault, h, "agentctl")
	if !strings.Contains(got, " --model 'glm-5.3-flash'") {
		t.Errorf("заказ второй подписки потерял модель яруса: %s", got)
	}
	if !strings.Contains(got, "exec --harness 'втораяtest'") {
		t.Errorf("заказ второй подписки потерял обёртку exec: %s", got)
	}
	// Резюм на второй подписке идёт той же дорогой: с моделью в команде.
	again := chatCmd("", "glm-5.3-flash", "aaaa-1111", "и что вышло", execRotateDefault, h, "agentctl")
	if !strings.Contains(again, " --model 'glm-5.3-flash'") {
		t.Errorf("резюм второй подписки потерял модель яруса: %s", again)
	}
	// Подписка по умолчанию остаётся с моделью: выбор селектора панели работает
	// как работал.
	def := chatCmd("", "opus", "", "посмотри доску", execRotateDefault, nil, "agentctl")
	if !strings.Contains(def, " --model 'opus'") {
		t.Errorf("заказ подписки по умолчанию потерял модель: %s", def)
	}
	byDef := chatCmd("", "opus", "", "посмотри доску", execRotateDefault,
		&Harness{Name: "перваяtest", Bin: "клиент-1", Default: true}, "agentctl")
	if !strings.Contains(byDef, " --model 'opus'") {
		t.Errorf("заказ default-харнеса потерял модель: %s", byDef)
	}
}

// Правило плана несёт запасной адрес по имени tmux-сессии: в контуре второй
// подписки CLAUDE_CODE_SESSION_ID пуст, и агент DK-269 сжёг первый десяток
// ходов, разыскивая свой ID по printenv и каталогу планов. Имя берётся из пар
// окружения заказа, отдельного параметра у команды нет.
func TestChatCmdPlanRuleFallbackName(t *testing.T) {
	srv := newServer(&Config{Home: t.TempDir()}, nil, nil)
	got := chatCmd(srv.launchEnv("XR-4", "chat-XR-4-1", ""), "opus", "", "привет", execRotateDefault, nil, "agentctl")
	if !strings.Contains(got, "Если CLAUDE_CODE_SESSION_ID пуст, веди план файлом ~/.devkit/plans/chat-XR-4-1.json.") {
		t.Errorf("в заказе нет запасного адреса плана с именем tmux: %s", got)
	}
	// Без имени tmux правило остаётся прежним: запасному адресу неоткуда
	// взяться, и выдуманный он был бы хуже отсутствия.
	bare := chatCmd("", "opus", "", "привет", execRotateDefault, nil, "agentctl")
	if strings.Contains(bare, "Если CLAUDE_CODE_SESSION_ID пуст") {
		t.Errorf("запасной адрес появился без имени tmux: %s", bare)
	}
	// Вводная продолжения несёт тот же запасной адрес.
	cont := continuePrompt("XR-4", execRotateDefault, "chat-XR-4-2")
	if !strings.Contains(cont, "~/.devkit/plans/chat-XR-4-2.json") {
		t.Errorf("вводная продолжения без запасного адреса плана: %s", cont)
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
	if got := continuePrompt("XR-1", 640000, ""); !strings.Contains(got, "640000") {
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

// writePeerSock то же, но с явным сокетом канала: тесты доставки поднимают
// свои сокеты, а умолчание /tmp/cc-socks/<pid>.sock тут указало бы в чужие.
func writePeerSock(t *testing.T, home, sid string, pid int, sock string) {
	t.Helper()
	dir := filepath.Join(home, ".claude", "sessions")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := fmt.Sprintf(`{"pid":%d,"sessionId":%q,"name":"devkit-1","kind":"interactive","messagingSocketPath":%q}`,
		pid, sid, sock)
	if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("%d.json", pid)), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
}

// sockTempDir выбирает короткий каталог под тестовый сокет: у unix-сокета
// предел длины пути около сотни знаков, и t.TempDir() в него не влезает.
func sockTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "dashsock")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

// liveSock изображает живого клиента: соединение принимается, дочитывается до
// полузакрытия и закрывается, как это делает настоящий событийный цикл.
func liveSock(t *testing.T) string {
	t.Helper()
	path := filepath.Join(sockTempDir(t), "live.sock")
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				io.Copy(io.Discard, c)
			}(c)
		}
	}()
	return path
}

// deafSock изображает клин второго рода: сокет слушает, но соединений не
// принимает. Ровно так выглядит клиент с мёртвым событийным циклом: connect и
// write проходят силами ядра, а подтверждения нет (живой случай клиента 69975).
func deafSock(t *testing.T) string {
	t.Helper()
	path := filepath.Join(sockTempDir(t), "deaf.sock")
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	return path
}

// Доставка в сокет не успех по факту записи байтов: успехом считается только
// подтверждение клиента (закрытие соединения после нашего полузакрытия). Байты
// в сокет клина уходят «удачно» силами ядра, и до этой сверки дашборд трижды
// писал «реплика ушла в сокет» замершему клиенту (живой случай 69975).
func TestPeerSayNeedsAck(t *testing.T) {
	if err := peerSay(liveSock(t), "привет"); err != nil {
		t.Fatalf("доставка живому клиенту не прошла: %v", err)
	}
	err := peerSay(deafSock(t), "привет")
	if err == nil {
		t.Fatal("запись в молчащий сокет сошла за доставку")
	}
	if !errors.Is(err, errPeerNoAck) {
		t.Fatalf("отказ не назван отказом подтверждения: %v", err)
	}
	// Пустой зонд различает их так же, но без кадра: им ходит детектор.
	if err := peerProbe(liveSock(t), time.Second); err != nil {
		t.Errorf("зонд живого клиента не прошёл: %v", err)
	}
	if err := peerProbe(deafSock(t), 300*time.Millisecond); !errors.Is(err, errPeerNoAck) {
		t.Errorf("зонд клина не назвал молчание: %v", err)
	}
}

// Реплика без подтверждения остаётся недоставленной: ручка отвечает отказом с
// именем клина, запасной дорогой в замороженный pty не идёт (send-keys там
// исчезает без эха), а список чатов называет клин своим полем сразу, без
// второго зонда.
func TestChatSayNoAckStaysUndelivered(t *testing.T) {
	e, c := chatEnv(t)
	sid := "eeee5555-5555-4555-8555-555555555555"
	writeSession(t, e.home, e.proj, "", sid, plainTalk, time.Now().Add(-10*time.Minute))
	writePeerSock(t, e.home, sid, os.Getpid(), deafSock(t))
	calls := filepath.Join(e.bin, "tmux-calls")
	writeScript(t, e.bin, "tmux", `echo "$@" >> `+calls+`
case "$1" in ls) echo "chat-XR-1-1|1|123";; esac
exit 0`)
	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/say", `{"text": "ау"}`)
	text := body(t, resp)
	if resp.StatusCode == http.StatusOK {
		t.Fatalf("реплика без подтверждения сошла за доставленную: %s", text)
	}
	if !strings.Contains(text, stuckDeafWord) {
		t.Errorf("отказ не назвал клин словами: %s", text)
	}
	if data, _ := os.ReadFile(calls); strings.Contains(string(data), "send-keys") {
		t.Errorf("реплика уехала запасной дорогой в замороженный pty: %s", data)
	}
	list := chatsOf(t, e, c)
	if len(list) != 1 || list[0].Stuck != stuckDeafWord {
		t.Errorf("клин не виден списком после отказа доставки: %+v", list)
	}
}

// Детектор второго рода клина: pty жив (tmux на месте), подтверждения нет,
// транскрипт стоит. Такой разговор помечается той же механикой, что и
// пропавший терминал: плашка и кнопка выхода берутся из поля stuck. Живой
// сосед с тем же стоящим транскриптом клином не считается: его канал отвечает.
func TestChatListDeafPeerStuck(t *testing.T) {
	e, c := chatEnv(t)
	deadSid := "dddd4444-4444-4444-8444-444444444444"
	okSid := "aaaa1111-1111-4111-8111-111111111111"
	old := time.Now().Add(-10 * time.Minute)
	writeSession(t, e.home, e.proj, "", deadSid, plainTalk, old)
	fresher := `{"type":"user","message":{"role":"user","content":"работа идёт"},"timestamp":"2026-08-16T10:00:01.000Z","gitBranch":"main"}` + "\n"
	writeSession(t, e.home, e.proj, "", okSid, fresher, old)
	writePeerSock(t, e.home, deadSid, os.Getpid(), "/tmp/dash-deaf-test.sock")
	writePeerSock(t, e.home, okSid, os.Getppid(), "/tmp/dash-live-test.sock")
	writeScript(t, e.bin, "tmux", `case "$1" in
ls) echo "chat-XR-1-1|1|123";;
esac
exit 0`)
	// Зонд отвечает по имени сокета: молчание у клина, закрытие у живого.
	e.s.probe = func(sock string, wait time.Duration) error {
		if strings.Contains(sock, "deaf") {
			return fmt.Errorf("%w: тишина", errPeerNoAck)
		}
		return nil
	}
	byID := map[string]chatEntry{}
	for _, ch := range chatsOf(t, e, c) {
		byID[ch.ID] = ch
	}
	if got := byID[deadSid]; got.Stuck != stuckDeafWord {
		t.Errorf("клин с живым pty не узнан: stuck=%q, ждал %q", got.Stuck, stuckDeafWord)
	}
	if got := byID[okSid]; got.Stuck != "" {
		t.Errorf("живой сосед со старым транскриптом назван клином: %+v", got)
	}
	// Свежий транскрипт снимает вопрос без зонда: разговор ходит.
	e.s.probe = func(string, time.Duration) error {
		t.Error("зонд пошёл к разговору со свежим транскриптом")
		return nil
	}
	e.s.mu.Lock()
	e.s.deaf = map[string]deafEntry{}
	e.s.mu.Unlock()
	writeSession(t, e.home, e.proj, "", deadSid, plainTalk, time.Now())
	writeSession(t, e.home, e.proj, "", okSid, fresher, time.Now())
	for _, ch := range chatsOf(t, e, c) {
		if ch.Stuck != "" {
			t.Errorf("свежий транскрипт не снял клин: %+v", ch)
		}
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
// Снятие под перезапуск (род drop): живая tmux-сессия заканчивается целиком,
// чтобы следующая реплика подняла разговор резюмом с новой моделью. Это не
// клин (у клина tmux уже нет) и не Escape (ход не прерывают, сессию снимают).
func TestChatStopDropEndsLiveSession(t *testing.T) {
	e, c := chatEnv(t)
	sid := "ffff6666-6666-4666-8666-666666666666"
	writeSession(t, e.home, e.proj, "", sid, plainTalk, time.Now())
	writeBinds(t, e.home, fmt.Sprintf("2026-08-23T14:40:00 сессия %s задача XR-1 проект demo "+
		"дерево %s транскрипт /tmp/t.jsonl источник заказ повод startup tmux chat-XR-1-1\n", sid, e.proj))
	tmuxLog := filepath.Join(e.home, "tmux.log")
	writeTmuxFake(t, e.bin, tmuxLog, "chat-XR-1-1\t1\t1786000000\n")

	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/stop", `{"drop": true}`)
	text := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("снятие под перезапуск: %d %s", resp.StatusCode, text)
	}
	if !strings.Contains(text, `"way":"drop"`) {
		t.Errorf("ручка не назвала род стопа: %s", text)
	}
	if got := readFile(t, tmuxLog); !strings.Contains(got, "kill-session -t chat-XR-1-1") {
		t.Errorf("tmux-сессия не снята: %s", got)
	}

	// Нашей tmux-сессии уже нет: закрывать нечего, потому что закрыто. Прежде
	// сюда приходил 409 со словами «снимать под перезапуск нечего», экран
	// показывал карточку сбоя, а строка сессии оставалась стоять, и второе
	// нажатие упиралось в тот же отказ (живой случай пользователя).
	writeTmuxFake(t, e.bin, tmuxLog, "")
	resp = doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/stop", `{"drop": true}`)
	text = body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("снятие уже снятой сессии отбито отказом: %d %s", resp.StatusCode, text)
	}
	if !strings.Contains(text, `"way":"gone"`) {
		t.Errorf("ручка не назвала род исхода: %s", text)
	}
	if !strings.Contains(text, "уже закрыта") {
		t.Errorf("исход сказан не по-человечески: %s", text)
	}
	if strings.Contains(text, "нечего") || strings.Contains(text, "error") {
		t.Errorf("сделанное дело сказано словами отказа: %s", text)
	}
	// Прерывание хода отвечает тем же родом и теми же спокойными словами:
	// сессии нет, значит и ход в ней не идёт.
	resp = doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/stop", `{}`)
	text = body(t, resp)
	if resp.StatusCode != http.StatusOK || !strings.Contains(text, `"way":"gone"`) {
		t.Errorf("прерывание хода в снятой сессии отбито отказом: %d %s", resp.StatusCode, text)
	}
	// Разговор, который дашборд не поднимал вовсе, это другой случай: там
	// снимать и правда нечего, и сказано об этом отказом.
	alien := "cccc3333-3333-4333-8333-333333333333"
	writeSession(t, e.home, e.proj, "", alien, plainTalk, time.Now())
	resp = doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+alien+"/stop", `{"drop": true}`)
	text = body(t, resp)
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("снятие чужого окна не отбито: %d %s", resp.StatusCode, text)
	}
	if !strings.Contains(text, "поднимал не дашборд") {
		t.Errorf("отказ чужому окну не назвал причины: %s", text)
	}
}

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

// Правило канала едет каждым заказом чата с дословной подписью доставки:
// реплики панели доезжают межсессионным каналом, харнес оборачивает их рамкой
// «сообщение от другой сессии», и агент отвечал человеку в третьем лице
// («коллега спрашивает», «ответ ему отправлен» в живом чате 93828026). Подпись
// в правиле сверяется с кадром peerFrame: разъедутся, и агент перестанет
// узнавать канал.
func TestChatChannelRuleInEveryOrder(t *testing.T) {
	sign := `from-name="dashboard"`
	if !strings.Contains(channelRule, sign) {
		t.Fatalf("в правиле канала нет дословной подписи %s: %s", sign, channelRule)
	}
	frame, err := peerFrame("привет", "uds:/tmp/cc-socks/1.sock")
	if err != nil {
		t.Fatal(err)
	}
	// Подпись ищется в теле рамки, а не в сырых байтах кадра: JSON экранирует
	// кавычки, и дословная строка там не видна.
	var rec struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal(frame, &rec); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rec.Message.Content, sign) {
		t.Fatalf("кадр канала подписан иначе, правило разъехалось с доставкой: %s", rec.Message.Content)
	}
	fresh := chatCmd("", "opus", "", "посмотри доску", execRotateDefault, nil, "agentctl")
	if !strings.Contains(fresh, sign) {
		t.Errorf("в заказе подъёма нет правила канала: %s", fresh)
	}
	again := chatCmd("", "opus", "aaaa-1111", "и что вышло", execRotateDefault, nil, "agentctl")
	if !strings.Contains(again, sign) {
		t.Errorf("в резюмном заказе нет правила канала: %s", again)
	}
	if got := continuePrompt("XR-1", execRotateDefault, ""); !strings.Contains(got, sign) {
		t.Errorf("во вводной продолжения нет правила канала: %s", got)
	}
	if got := goalContinuePrompt("XR-100", execRotateDefault, ""); !strings.Contains(got, sign) {
		t.Errorf("во вводной продолжения цели нет правила канала: %s", got)
	}
}

// Модель живой сессии, поднятой дашбордом, называет запись подъёма, а не
// транскрипт. Транскрипт узнаёт про смену только с первого ответа новой
// модели, и до него селектор панели показывал прежнюю: человек перезапускал
// разговор с opus, а в списке оставался fable (жалоба пользователя). Чужое
// окно (vscode) своей записи не заводит, и там модель по-прежнему читается из
// транскрипта.
func TestLiveModelComesFromOurLaunch(t *testing.T) {
	e, c := chatEnv(t)
	sid := "cccc7777-7777-4777-8777-777777777777"
	talk := plainTalk + `{"type":"assistant","message":{"role":"assistant","model":"claude-fable-1",` +
		`"content":[{"type":"text","text":"готово"}]},"timestamp":"2026-08-17T10:00:02.000Z"}` + "\n"
	writeSession(t, e.home, e.proj, "", sid, talk, time.Now())
	writeBinds(t, e.home, fmt.Sprintf("2026-08-23T14:40:00 сессия %s задача XR-1 проект demo "+
		"дерево %s транскрипт /tmp/t.jsonl источник заказ повод startup tmux chat-XR-1-1\n", sid, e.proj))
	writeTmuxFake(t, e.bin, filepath.Join(e.home, "tmux.log"), "chat-XR-1-1\t1\t1786000000\n")

	find := func() chatEntry {
		t.Helper()
		resp := doReq(t, c, "GET", e.srv.URL+"/api/projects/demo/chats", "")
		var got struct {
			Chats []chatEntry `json:"chats"`
		}
		if err := json.Unmarshal([]byte(body(t, resp)), &got); err != nil {
			t.Fatal(err)
		}
		for _, c := range got.Chats {
			if c.ID == sid {
				return c
			}
		}
		t.Fatalf("чата нет в списке: %+v", got.Chats)
		return chatEntry{}
	}
	// Записи подъёма нет вовсе: модель приезжает из транскрипта, как и раньше.
	if got := find().LiveModel; got != "fable" {
		t.Fatalf("без записи подъёма модель не из транскрипта: %q", got)
	}
	// Дашборд поднял эту tmux-сессию моделью opus: её и показываем, не дожидаясь
	// первого ответа.
	if err := e.s.chatStoreWrite("tmux-chat-XR-1-1", chatStore{Model: "opus", From: sid}); err != nil {
		t.Fatal(err)
	}
	// Список читается заново: кэш шапок ключуется меткой файла, а модель
	// приезжает мимо него.

	if got := find().LiveModel; got != "opus" {
		t.Fatalf("живая модель разошлась с тем, чем дашборд поднял сессию: %q", got)
	}
}

// Живой случай: одна реплика доехала до сессии пятью одинаковыми копиями
// подряд. Пять отдельных отправок с разницей в минуты, у каждой своя запись, и
// узнать в них повтор дашборду было нечем: ответ ручки до панели не доезжал,
// панель считала реплику неушедшей и слала снова. Ключ записи отправителя
// (msg_id) один и тот же у всех попыток, и по нему повтор дальше не едет.

// countingSock это живой клиент, который считает пришедшие кадры: предмет
// проверки в том, сколько раз реплика доехала до приёмной стороны, а не в том,
// что ответила ручка.
func countingSock(t *testing.T) (string, func() []string) {
	t.Helper()
	path := filepath.Join(sockTempDir(t), "count.sock")
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	var mu sync.Mutex
	got := []string{}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				data, _ := io.ReadAll(c)
				mu.Lock()
				got = append(got, string(data))
				mu.Unlock()
			}(c)
		}
	}()
	return path, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string{}, got...)
	}
}

func sayBody(text, msg string) string {
	return fmt.Sprintf(`{"text": %q, "msg_id": %q}`, text, msg)
}

// Повтор той же записи до сессии не доезжает: кадр у живого клиента один, а
// ручка отвечает, что реплика уже доставлена. Соседняя запись едет как ехала:
// дедупликация ловит повтор, а не всякую вторую реплику.
func TestChatSayDedupsRepeatOfSameRecord(t *testing.T) {
	e, c := chatEnv(t)
	sid := "aaaa1111-1111-4111-8111-111111111111"
	writeSession(t, e.home, e.proj, "", sid, plainTalk, time.Now().Add(-time.Minute))
	sock, frames := countingSock(t)
	writePeerSock(t, e.home, sid, os.Getpid(), sock)
	writeScript(t, e.bin, "tmux", `case "$1" in ls) echo "chat-XR-1-1|1|123";; esac
exit 0`)
	at := e.srv.URL + "/api/projects/demo/chats/" + sid + "/say"

	first := doReq(t, c, "POST", at, sayBody("что там с задачей dk-481", "m-1"))
	if first.StatusCode != http.StatusOK {
		t.Fatalf("первая отправка не прошла: %d %s", first.StatusCode, body(t, first))
	}
	again := doReq(t, c, "POST", at, sayBody("что там с задачей dk-481", "m-1"))
	said := body(t, again)
	if again.StatusCode != http.StatusOK {
		t.Fatalf("повтор ответил отказом, панель будет слать снова: %d %s", again.StatusCode, said)
	}
	if got := frames(); len(got) != 1 {
		t.Fatalf("реплика доехала до сессии %d раз, а сказана один: %q", len(got), got)
	}
	if !strings.Contains(said, "уже") {
		t.Fatalf("повтор не назван повтором: %s", said)
	}

	// Следующая реплика человека это не повтор: у неё своя запись и своя дорога.
	next := doReq(t, c, "POST", at, sayBody("вопрос выше уже не актуален", "m-2"))
	if next.StatusCode != http.StatusOK {
		t.Fatalf("вторая реплика не прошла: %d %s", next.StatusCode, body(t, next))
	}
	if got := frames(); len(got) != 2 {
		t.Fatalf("вторая реплика не доехала: кадров %d, %q", len(got), got)
	}
	// Панель старой версии ключа не везёт вовсе: её реплики едут как ехали.
	old := doReq(t, c, "POST", at, `{"text": "реплика без ключа"}`)
	if old.StatusCode != http.StatusOK {
		t.Fatalf("реплика без ключа записи отбита: %d %s", old.StatusCode, body(t, old))
	}
	if got := frames(); len(got) != 3 {
		t.Fatalf("реплика без ключа не доехала: кадров %d, %q", len(got), got)
	}
}

// Окно «стоп -> реплика -> резюм»: человек пишет в момент, когда сессии уже
// нет (её сняли сменой модели). Реплику увозит вводная резюма, и дожим той же
// записи второго резюма не поднимает: иначе тот же текст приедет и вводной, и
// повтором, а рядом заведётся второй агент.
func TestChatSayRepeatAfterResumeRidesOnce(t *testing.T) {
	e, c := chatEnv(t)
	sid := "bbbb2222-2222-4222-8222-222222222222"
	writeSession(t, e.home, e.proj, "", sid, plainTalk, time.Now().Add(-time.Minute))
	tmuxLog := filepath.Join(e.home, "tmux.log")
	// Сессии нет: tmux ls пуст, живого сокета у чата тоже нет.
	writeScript(t, e.bin, "tmux", `echo "$@" >> "`+tmuxLog+`"
case "$1" in ls) exit 1;; esac
exit 0`)
	writeScript(t, e.bin, "claude", "exit 0")
	at := e.srv.URL + "/api/projects/demo/chats/" + sid + "/say"

	first := doReq(t, c, "POST", at, sayBody("держи реплику в момент смены модели", "m-9"))
	said := body(t, first)
	if first.StatusCode != http.StatusOK {
		t.Fatalf("реплика мёртвому чату не прошла: %d %s", first.StatusCode, said)
	}
	if !strings.Contains(said, "resume") {
		t.Fatalf("реплика поехала не резюмом: %s", said)
	}
	raises := strings.Count(readFile(t, tmuxLog), "new-session")
	if raises != 1 {
		t.Fatalf("резюмов на первую отправку %d, ждали один", raises)
	}

	again := doReq(t, c, "POST", at, sayBody("держи реплику в момент смены модели", "m-9"))
	dup := body(t, again)
	if again.StatusCode != http.StatusOK {
		t.Fatalf("повтор после резюма ответил отказом: %d %s", again.StatusCode, dup)
	}
	if got := strings.Count(readFile(t, tmuxLog), "new-session"); got != raises {
		t.Fatalf("дожим поднял второй резюм: подъёмов %d, было %d", got, raises)
	}
	if !strings.Contains(dup, "уже") {
		t.Fatalf("повтор после резюма не назван повтором: %s", dup)
	}
	// Реплика легла в журнал сказанного один раз: вводная резюма её уже увезла,
	// и вторая запись сделала бы её вечной попутчицей всех следующих резюмов.
	rows := saidLoad(e.home, saidSessionKey(sid))
	seen := 0
	for _, r := range rows {
		if strings.Contains(r.Text, "держи реплику в момент смены модели") {
			seen++
		}
	}
	if seen != 1 {
		t.Fatalf("реплика записана в журнал %d раз: %+v", seen, rows)
	}
}

// Отказ доставки ключ отпускает: клин клиента это повод повторить, и запись,
// не доехавшая ни разу, обязана уехать со второй попытки.
func TestChatSayFailedAttemptStaysRepeatable(t *testing.T) {
	e, c := chatEnv(t)
	sid := "cccc3333-3333-4333-8333-333333333333"
	writeSession(t, e.home, e.proj, "", sid, plainTalk, time.Now().Add(-10*time.Minute))
	writePeerSock(t, e.home, sid, os.Getpid(), deafSock(t))
	writeScript(t, e.bin, "tmux", `case "$1" in ls) echo "chat-XR-1-1|1|123";; esac
exit 0`)
	at := e.srv.URL + "/api/projects/demo/chats/" + sid + "/say"
	if resp := doReq(t, c, "POST", at, sayBody("ау", "m-5")); resp.StatusCode == http.StatusOK {
		t.Fatalf("недоставленная реплика сошла за доставленную: %s", body(t, resp))
	}

	// Клин сняли, канал ожил: та же запись едет своей дорогой, а не считается
	// доставленной по факту первой попытки.
	sock, frames := countingSock(t)
	writePeerSock(t, e.home, sid, os.Getpid(), sock)
	e.s.mu.Lock()
	e.s.deaf = map[string]deafEntry{}
	e.s.mu.Unlock()
	resp := doReq(t, c, "POST", at, sayBody("ау", "m-5"))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("повтор после отказа не поехал: %d %s", resp.StatusCode, body(t, resp))
	}
	if got := frames(); len(got) != 1 {
		t.Fatalf("реплика после отказа доехала %d раз: %q", len(got), got)
	}
}

// Утечка адресации по имени tmux (DK-397 POC). Конвейер задачи подняли заново:
// прежнюю tmux-сессию сняли, новую подняли тем же именем, а реестр при снятии
// не правится, и свёртка sessions.Last всё ещё отдаёт старому разговору это
// имя. Прежде реплика уезжала send-keys в живое имя, то есть в чужую сессию.
// Теперь хозяин имени сверяется по реестру, и адресат остаётся собой.
func TestChatSayDoesNotRideRecycledTmuxName(t *testing.T) {
	e, c := chatEnv(t)
	old := "aaaa5030-1111-4111-8111-111111111111"
	fresh := "bbbb5031-2222-4222-8222-222222222222"
	writeSession(t, e.home, e.proj, "", old, plainTalk, time.Now().Add(-time.Minute))
	// Сокета у снятого разговора нет: дорога падает на имя tmux.
	writeBinds(t, e.home,
		"2026-08-24T12:42:48 сессия "+old+" задача DK-503 проект demo дерево "+e.proj+
			" транскрипт /tmp/t.jsonl источник заказ повод startup tmux task-DK-503\n",
		// Конвейер подняли заново: то же имя реестр отдал другому разговору.
		"2026-08-24T13:55:18 сессия "+fresh+" задача DK-503 проект demo дерево "+e.proj+
			" транскрипт /tmp/t2.jsonl источник заказ повод startup tmux task-DK-503\n")
	// Имя живо, и прежде этого хватало для доставки.
	writeScript(t, e.bin, "tmux", `case "$1" in
ls) echo "task-DK-503|1|123";;
send-keys) echo "$@" >> `+filepath.Join(e.home, "sent.log")+`;;
esac
exit 0`)
	at := e.srv.URL + "/api/projects/demo/chats/" + old + "/say"
	r := doReq(t, c, "POST", at, sayBody("Продолжай работу", "m-1"))
	said := body(t, r)
	sent, _ := os.ReadFile(filepath.Join(e.home, "sent.log"))
	if strings.Contains(string(sent), "Продолжай работу") {
		t.Fatalf("реплика уехала в чужую сессию, занявшую имя task-DK-503: %q", sent)
	}
	if strings.Contains(said, `"way": "send-keys"`) || strings.Contains(said, `"way":"send-keys"`) {
		t.Fatalf("дорога send-keys по занятому имени: %s", said)
	}
}

// Снятый и пересозданный разговор виден словами (DK-397 POC). Имя tmux реестр
// отдал другому разговору, то есть работу подняли заново. Прежде имя просто
// снималось молча, разговор выглядел обычным, и реплика в него уезжала мимо
// человека. Теперь запись несёт причину и адрес выхода.
func TestChatEntryNamesRestartedConversation(t *testing.T) {
	e, c := chatEnv(t)
	old := "aaaa5040-1111-4111-8111-111111111111"
	fresh := "bbbb5041-2222-4222-8222-222222222222"
	writeSession(t, e.home, e.proj, "", old, plainTalk, time.Now().Add(-time.Hour))
	writeSession(t, e.home, e.proj, "", fresh, plainTalk, time.Now().Add(-time.Minute))
	writeBinds(t, e.home,
		"2026-08-24T12:42:48 сессия "+old+" задача DK-503 проект demo дерево "+e.proj+
			" транскрипт /tmp/t.jsonl источник заказ повод startup tmux task-DK-503\n",
		"2026-08-24T13:55:18 сессия "+fresh+" задача DK-503 проект demo дерево "+e.proj+
			" транскрипт /tmp/t2.jsonl источник заказ повод startup tmux task-DK-503\n")
	writeScript(t, e.bin, "tmux", `case "$1" in ls) echo "task-DK-503|1|123";; esac
exit 0`)

	resp := doReq(t, c, "GET", e.srv.URL+"/api/projects/demo/chats", "")
	var got struct {
		Chats []struct {
			ID     string `json:"id"`
			Gone   string `json:"gone"`
			GoneTo string `json:"goneTo"`
			Tmux   string `json:"tmux"`
		} `json:"chats"`
	}
	if err := json.Unmarshal([]byte(body(t, resp)), &got); err != nil {
		t.Fatal(err)
	}
	var dead, live *struct {
		ID     string `json:"id"`
		Gone   string `json:"gone"`
		GoneTo string `json:"goneTo"`
		Tmux   string `json:"tmux"`
	}
	for i := range got.Chats {
		switch got.Chats[i].ID {
		case old:
			dead = &got.Chats[i]
		case fresh:
			live = &got.Chats[i]
		}
	}
	if dead == nil || live == nil {
		t.Fatalf("в списке нет обоих разговоров: %+v", got.Chats)
	}
	if dead.Gone == "" {
		t.Fatal("снятый разговор не назван словами: панель покажет его обычным")
	}
	if dead.GoneTo != fresh {
		t.Fatalf("выход указан не на занявший имя разговор: %q, ждали %q", dead.GoneTo, fresh)
	}
	if live.Gone != "" {
		t.Fatalf("живой разговор объявлен снятым: %q", live.Gone)
	}
}

// Разговор считается занятым только по доказательству. Прежде поле Idle
// оставалось нулевым (то есть «занят») у разговора, чьей записи в реестре
// клиента нет вовсе: процесс давно умер, tmux-сессия жива, и список рисовал
// семичасовой разговор активным (замечание пользователя про сессию, в которой
// давно никто не писал). Слову реестра при этом верят, пока запись свежа:
// клиент, упавший посреди хода, оставляет своё «busy» навсегда.
func TestChatIdleWithoutPeerRecord(t *testing.T) {
	e := newTestEnv(t)
	now := time.Date(2026, 8, 24, 21, 0, 0, 0, time.UTC)
	e.s.now = func() time.Time { return now }
	writeTmuxFake(t, e.bin, filepath.Join(e.home, "tmux.log"), "chat-XR-1\n")
	sid := "aaaa1111-1111-4111-8111-111111111111"
	// Транскрипт молчит семь часов: ни свежей записи, ни висящего вызова.
	old := fmt.Sprintf(`{"type":"assistant","message":{"role":"assistant","content":`+
		`[{"type":"text","text":"давно"}]},"timestamp":%q}`,
		now.Add(-7*time.Hour).Format(time.RFC3339)) + "\n"
	writeSession(t, e.home, e.proj, "", sid, sessionLine("поговорим", "main")+old, now.Add(-7*time.Hour))
	writeBinds(t, e.home, listedBind(sid, "XR-1", "chat-XR-1"))

	idleOf := func(what string) bool {
		t.Helper()
		for _, c := range e.s.chatEntries(e.proj, 20) {
			if c.ID == sid {
				return c.Idle
			}
		}
		t.Fatalf("разговор пропал из списка (%s)", what)
		return false
	}

	// Записи реестра нет вовсе: занятость доказать нечем, и разговор простаивает.
	if !idleOf("без записи реестра") {
		t.Error("разговор без записи реестра объявлен занятым: список рисует его активным")
	}

	// Запись есть, но её «busy» протухло: ход кончился семь часов назад.
	writePeerAged(t, e.home, sid, "chat-XR-1:@1.%1", "busy", now.Add(-7*time.Hour).UnixMilli())
	if !idleOf("протухшее busy") {
		t.Error("протухшее «busy» реестра выдано за идущий ход")
	}

	// Свежая запись со словом busy: ход идёт, и это видно.
	writePeerAged(t, e.home, sid, "chat-XR-1:@1.%1", "busy", now.Add(-time.Minute).UnixMilli())
	if idleOf("свежее busy") {
		t.Error("свежее «busy» реестра потеряно: идущий ход объявлен простоем")
	}
}

// Ответ на вопрос клиента уходит из панели, а не из терминала: номер пункта
// едет ему клавишами, и до отправки ручка сверяет, что вопрос вообще стоит и
// что выбран существующий пункт. Ничего в конфиг подписки дашборд при этом не
// пишет: решение остаётся человеку, меняется только место, где он его
// принимает (решение пользователя).
// Сводку опроса дашборд проходит сам: последний ответ и есть отправка, и
// второе подтверждение человеку не нужно (замечание пользователя по снимку).
// Проходится она только когда отвечено всё: со своим предупреждением сводка
// остаётся человеку, иначе опрос уехал бы неполным.
func TestChatAskPassesReviewItself(t *testing.T) {
	poll := strings.Join([]string{
		"\u001b[39m\u2190  \u001b[48;5;153m \u2610 Место \u001b[49m  \u2714 Submit  \u2192",
		"",
		"Где ломается?",
		"",
		"\u276f 1. [ ] На телефоне",
		"  2. [ ] За роутером",
		"     Next",
		"",
		"Enter to select \u00b7 Tab/Arrow keys to navigate \u00b7 Esc to cancel",
	}, "\n")
	full := strings.Join([]string{
		"\u001b[39m\u2190  \u2612 Место \u001b[48;5;153m \u2714 Submit \u001b[49m \u2192",
		"Review your answers",
		" \u25cf Где ломается?",
		"   \u2192 На телефоне",
		"Ready to submit your answers?",
		"\u276f 1. Submit answers",
		"  2. Cancel",
		"Enter to confirm \u00b7 Esc to cancel",
	}, "\n")
	half := strings.Replace(full, "Review your answers",
		"Review your answers\n\u26a0 You have not answered all questions", 1)

	for _, tc := range []struct {
		name   string
		review string
		passed bool
	}{
		{"отвечено всё", full, true},
		{"отвечено не всё", half, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newTestEnv(t)
			sent := filepath.Join(e.home, "sent.log")
			// Первый снимок это сам опрос, дальнейшие сводка: ровно так панель
			// выглядит после ответа на последний шаг.
			seen := filepath.Join(e.home, "seen")
			writeScript(t, e.bin, "tmux", `case "$1" in
capture-pane)
  if [ -f `+seen+` ]; then printf '%s' `+shQuote(tc.review)+`; else printf '%s' `+shQuote(poll)+`; touch `+seen+`; fi;;
send-keys) shift; echo "$@" >> `+sent+`;;
ls) printf 'chat-7\n';;
esac
exit 0`)
			sid := "aaaa1111-1111-4111-8111-111111111111"
			writeBinds(t, e.home, listedBind(sid, "XR-1", "chat-7"))
			c := e.loggedClient(t)
			at := e.srv.URL + "/api/projects/demo/chats/" + sid + "/ask"

			resp := doReq(t, c, "POST", at, `{"option": 2}`)
			text := body(t, resp)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("ответ на шаг опроса: %d %s", resp.StatusCode, text)
			}
			keys := readFile(t, sent)
			// Сам ответ уехал стрелкой и Enter.
			if !strings.Contains(keys, "Down") || !strings.Contains(keys, "Enter") {
				t.Fatalf("ответ на шаг не подался клавишами: %s", keys)
			}
			// Проход сводки виден вторым Enter и словами в ответе ручки.
			passed := strings.Count(keys, "Enter") > 1
			if passed != tc.passed {
				t.Errorf("сводка пройдена=%v, жду %v: %s", passed, tc.passed, keys)
			}
			if strings.Contains(text, "ответы отправлены") != tc.passed {
				t.Errorf("про проход сводки сказано не так: %s", text)
			}
		})
	}
}

// Шаги опроса это табы, и переход по ним это не ответ: уезжают стрелки, а
// выбранного пункта нет вовсе. Человеку это даёт ходить по вопросам свободно,
// как в самом виджете (замечание пользователя).
func TestChatAskStepSwitch(t *testing.T) {
	pane := strings.Join([]string{
		"\u001b[39m\u2190  \u001b[48;5;153m \u2610 Место \u001b[49m  \u2610 Симптом  \u2714 Submit  \u2192",
		"",
		"Где ломается?",
		"",
		"\u276f 1. [ ] На телефоне",
		"  2. [ ] За роутером",
		"     Next",
		"",
		"Enter to select \u00b7 Tab/Arrow keys to navigate \u00b7 Esc to cancel",
	}, "\n")
	e := newTestEnv(t)
	sent := filepath.Join(e.home, "sent.log")
	writeScript(t, e.bin, "tmux", `case "$1" in
capture-pane) printf '%s' `+shQuote(pane)+`;;
send-keys) shift; echo "$@" >> `+sent+`;;
ls) printf 'chat-7\n';;
esac
exit 0`)
	sid := "aaaa1111-1111-4111-8111-111111111111"
	writeBinds(t, e.home, listedBind(sid, "XR-1", "chat-7"))
	c := e.loggedClient(t)
	at := e.srv.URL + "/api/projects/demo/chats/" + sid + "/ask"

	resp := doReq(t, c, "POST", at, `{"step": 2}`)
	if text := body(t, resp); resp.StatusCode != http.StatusOK {
		t.Fatalf("переход по шагу: %d %s", resp.StatusCode, text)
	}
	keys := strings.TrimSpace(readFile(t, sent))
	if !strings.Contains(keys, "Right") {
		t.Errorf("переход не подался стрелкой: %q", keys)
	}
	if strings.Contains(keys, "Enter") {
		t.Errorf("переход по табу отправил ответ клиенту: %q", keys)
	}
	// Шага за пределами полосы нет, и молчать об этом нельзя.
	resp = doReq(t, c, "POST", at, `{"step": 9}`)
	if text := body(t, resp); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("переход на несуществующий шаг не отбит: %d %s", resp.StatusCode, text)
	}
}

func TestChatAskAnswerSendsKeys(t *testing.T) {
	e := newTestEnv(t)
	sent := filepath.Join(e.home, "sent.log")
	// Подсказка навигации стоит под вариантами, как её печатает клиент: без неё
	// блок вопроса не поднимается вовсе (askOnWidget).
	pane := " Quick safety check: доверяешь каталогу?\n\n \u276f 1. Yes, I trust this folder\n" +
		"   2. No, exit\n\n Enter to confirm \u00b7 Esc to cancel\n"
	writeScript(t, e.bin, "tmux", `case "$1" in
capture-pane) printf '%s' `+shQuote(pane)+`;;
send-keys) echo "$@" >> `+sent+`;;
ls) printf 'chat-7\n';;
esac
exit 0`)
	sid := "aaaa1111-1111-4111-8111-111111111111"
	writeBinds(t, e.home, listedBind(sid, "XR-1", "chat-7"))
	c := e.loggedClient(t)
	at := e.srv.URL + "/api/projects/demo/chats/" + sid + "/ask"

	// Вопрос виден ручкой: текст и варианты по порядку.
	var got struct {
		Ask struct {
			Text    string `json:"text"`
			Options []struct {
				Text string `json:"text"`
				Free bool   `json:"free"`
			} `json:"options"`
			At   int    `json:"at"`
			Keys string `json:"keys"`
		} `json:"ask"`
	}
	resp := doReq(t, c, "GET", at, "")
	text := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("вопрос клиента: %d %s", resp.StatusCode, text)
	}
	if err := json.Unmarshal([]byte(text), &got); err != nil {
		t.Fatalf("ответ не разобрался: %v\n%s", err, text)
	}
	if len(got.Ask.Options) != 2 || got.Ask.Options[0].Text != "Yes, I trust this folder" {
		t.Fatalf("варианты приехали не те: %+v", got.Ask)
	}
	// Способ ответа приезжает вместе с вопросом: у доверия это номер пункта.
	if got.Ask.Keys != "digit" {
		t.Errorf("способ ответа приехал как %q, жду номер пункта", got.Ask.Keys)
	}

	// Ответ уезжает клавишами: номер пункта и Enter.
	resp = doReq(t, c, "POST", at, `{"option": 1}`)
	if text := body(t, resp); resp.StatusCode != http.StatusOK {
		t.Fatalf("ответ на вопрос: %d %s", resp.StatusCode, text)
	}
	keys := readFile(t, sent)
	if !strings.Contains(keys, "-t =chat-7: 1") || !strings.Contains(keys, "Enter") {
		t.Errorf("ответ подан клиенту не клавишами: %s", keys)
	}

	// Несуществующий пункт отбивается словами, а не уезжает клиенту.
	resp = doReq(t, c, "POST", at, `{"option": 9}`)
	if text := body(t, resp); resp.StatusCode != http.StatusBadRequest ||
		!strings.Contains(text, "вариантов") {
		t.Errorf("выбор мимо вариантов: %d %s", resp.StatusCode, text)
	}
	// Разговор без нашей tmux-сессии спрашивать нечем, и это сказано словами, а
	// не отказом: панель спрашивает всякий открытый разговор, и молчание такой
	// же ответ, как сам вопрос (DK-652).
	other := "bbbb2222-2222-4222-8222-222222222222"
	resp = doReq(t, c, "GET", e.srv.URL+"/api/projects/demo/chats/"+other+"/ask", "")
	if text := body(t, resp); resp.StatusCode != http.StatusOK ||
		!strings.Contains(text, "не живёт в нашей tmux") || strings.Contains(text, `"ask"`) {
		t.Errorf("вопрос у чужого окна: %d %s", resp.StatusCode, text)
	}
}

// Доверие каталогу человек подтверждает клиенту сам, и подъём в незнакомом
// каталоге встаёт на вопросе. Дашборд говорит об этом сразу, ответом на подъём,
// и зовёт отвечать в панели: прежде человек минуту смотрел на пустую ленту, а
// потом шёл в tmux (замечание пользователя).
func TestTrustNoteBeforeRaise(t *testing.T) {
	e := newTestEnv(t)
	dir := "/дерево/проекта"
	was := quotaTrust
	t.Cleanup(func() { quotaTrust = was })

	quotaTrust = func(home string) map[string]bool { return map[string]bool{} }
	note := e.s.trustNote(nil, dir)
	if note == "" {
		t.Fatal("про недоверенный каталог дашборд промолчал")
	}
	for _, want := range []string{dir, "кнопками", "не надо"} {
		if !strings.Contains(note, want) {
			t.Errorf("в словах про доверие нет %q: %q", want, note)
		}
	}
	// Подписка названа своим именем: профили у них разные, и доверие тоже.
	if got := e.s.trustNote(&Harness{Name: "вторая", Home: "/дом/второй"}, dir); !strings.Contains(got, "вторая") {
		t.Errorf("слова не называют подписку: %q", got)
	}

	// Каталог доверен: молчание тут и означает, что подъём пройдёт без вопросов.
	quotaTrust = func(home string) map[string]bool { return map[string]bool{dir: true} }
	if got := e.s.trustNote(nil, dir); got != "" {
		t.Errorf("доверенный каталог назван недоверенным: %q", got)
	}
}

// Второй барьер после рубежа виджета: разобранный вопрос, чьи варианты уже
// стоят в ленте репликой или ответом агента, это эхо вывода клиента. Панель
// терминала режет длинные строки по ширине, поэтому вариант ищется в ленте
// подстрокой, а совпасть должны все варианты, а не один.
func TestAskEchoesFeed(t *testing.T) {
	feed := []reply{
		{Role: "assistant", Text: "Пачка на выполнение, в порядке зависимостей:\n" +
			"1. DK-312 (S, ранг 62): рубеж длинного вывода Bash, cmdout забирает хвост\n" +
			"2. DK-313 (S, ранг 58): выжимка агенту вместо полного тела ответа"},
	}
	echo := tmuxAsk{Options: []tmuxPick{
		{Text: "DK-312 (S, ранг 62): рубеж длинного вывода Bash, cmdout забирает"},
		{Text: "DK-313 (S, ранг 58): выжимка агенту вместо полного тела ответа"},
	}}
	if !askEchoesFeed(echo, feed) {
		t.Errorf("список из ответа агента не узнан эхом ленты")
	}
	widget := tmuxAsk{Options: []tmuxPick{
		{Text: "Yes, I trust this folder"},
		{Text: "No, exit, this is not my project"},
	}}
	if askEchoesFeed(widget, feed) {
		t.Errorf("вопрос виджета принят за эхо ленты")
	}
	// Один совпавший вариант это не эхо: у виджета бывает пункт со словами из
	// разговора, и прятать из-за него весь блок нельзя.
	half := tmuxAsk{Options: []tmuxPick{
		{Text: "DK-312 (S, ранг 62): рубеж длинного вывода Bash, cmdout забирает"},
		{Text: "Chat about this instead"},
	}}
	if askEchoesFeed(half, feed) {
		t.Errorf("один совпавший вариант принят за эхо всей ленты")
	}
	if askEchoesFeed(echo, nil) {
		t.Errorf("пустая лента объявлена эхом")
	}
}

// Клиент, по всем признакам стоящий на вопросе, а вопрос с панели не собрался:
// плашки об этом человеку больше нет вовсе (она ничего не объясняла и ничего
// не предлагала, а вылезала и на уже отвеченном опросе), и факт уходит строкой
// в журнал дашборда. Это наш сигнал чинить разбор.
func TestAskQuietGoesToLog(t *testing.T) {
	e, c := chatEnv(t)
	lc := &logCapture{}
	e.s.logf = lc.log
	now := time.Date(2026, 8, 25, 15, 0, 0, 0, time.UTC)
	e.s.now = func() time.Time { return now }
	sid := "eeee5555-5555-4555-8555-555555555555"
	writeSession(t, e.home, e.proj, "", sid, plainTalk, now.Add(-30*time.Second))
	writeBinds(t, e.home, fmt.Sprintf("2026-08-25T14:59:00 сессия %s задача XR-1 проект demo "+
		"дерево %s транскрипт /tmp/t.jsonl источник заказ повод startup tmux chat-13\n", sid, e.proj))
	writeNotifyLog(t, e.home, []string{permissionNotify(sid)})
	// Панель клиента без виджета: слова есть, а разбирать в кнопки нечего.
	quiet := " Работаю дальше, вопросов нет.\n\n\u276f \n"
	writeScript(t, e.bin, "tmux", `case "$1" in
capture-pane) printf '%s' `+shQuote(quiet)+`;;
ls) printf 'chat-13\t1\t1786000000\n';;
esac
exit 0`)

	at := e.srv.URL + "/api/projects/demo/chats/" + sid + "/ask"
	text := body(t, doReq(t, c, "GET", at, ""))
	if strings.Contains(text, `"ask"`) {
		t.Fatalf("из нечитаемой панели собрался вопрос: %s", text)
	}
	if !lc.contains(t, "виджета в снимке панели не разобрать") {
		t.Fatalf("про молчащий виджет не сказано в журнал: %v", lc.lines)
	}
	// Панель переспрашивает вопрос каждые несколько секунд: журнал не должен
	// забиваться одной и той же строкой.
	was := len(lc.lines)
	body(t, doReq(t, c, "GET", at, ""))
	if len(lc.lines) != was {
		t.Fatalf("вторая строка про тот же молчащий виджет: %v", lc.lines[was:])
	}
	// Окно вышло: сказать снова можно, разбор так и не чинили.
	e.s.now = func() time.Time { return now.Add(askQuietWindow + time.Minute) }
	body(t, doReq(t, c, "GET", at, ""))
	if len(lc.lines) == was {
		t.Fatalf("после окна про молчащий виджет не сказано ни разу: %v", lc.lines)
	}
}

// Разобранный виджет в журнал не пишется: чинить нечего, а строка про каждый
// живой опрос забила бы журнал.
func TestAskParsedStaysQuietInLog(t *testing.T) {
	e, c := chatEnv(t)
	lc := &logCapture{}
	e.s.logf = lc.log
	now := time.Date(2026, 8, 25, 15, 0, 0, 0, time.UTC)
	e.s.now = func() time.Time { return now }
	sid := "eeee5555-5555-4555-8555-555555555555"
	writeSession(t, e.home, e.proj, "", sid, plainTalk, now.Add(-30*time.Second))
	writeBinds(t, e.home, fmt.Sprintf("2026-08-25T14:59:00 сессия %s задача XR-1 проект demo "+
		"дерево %s транскрипт /tmp/t.jsonl источник заказ повод startup tmux chat-13\n", sid, e.proj))
	writeNotifyLog(t, e.home, []string{permissionNotify(sid)})
	pane := " Quick safety check: доверяешь каталогу?\n\n \u276f 1. Yes, I trust this folder\n" +
		"   2. No, exit\n\n Enter to confirm \u00b7 Esc to cancel\n"
	writeScript(t, e.bin, "tmux", `case "$1" in
capture-pane) printf '%s' `+shQuote(pane)+`;;
ls) printf 'chat-13\t1\t1786000000\n';;
esac
exit 0`)

	text := body(t, doReq(t, c, "GET", e.srv.URL+"/api/projects/demo/chats/"+sid+"/ask", ""))
	if !strings.Contains(text, `"ask"`) {
		t.Fatalf("живой виджет не разобрался: %s", text)
	}
	if lc.contains(t, "не разобрать") {
		t.Fatalf("про разобранный виджет ушла жалоба в журнал: %v", lc.lines)
	}
}

// Ведущей сессии у задачи нет ни одной: безадресную строку входа забирает
// только та сессия, что задачу ведёт, и обещать тут доставку нельзя. Живой
// случай DK-466: панель отчиталась доставкой, сняла пузырь, а строку подхватила
// посторонняя живая сессия того же чекаута и прочитала чужой вопрос посреди
// своего хода.
func TestTaskMessageUndeliveredWithoutLead(t *testing.T) {
	e, c := parkedEnv(t)
	resp := postTaskMessage(t, c, e, "XR-7", "а почему задача заблокирована")
	text := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("отправка: %d %s", resp.StatusCode, text)
	}
	if !strings.Contains(text, `"undelivered":true`) {
		t.Errorf("ответ не назвал реплику недоставленной: %s", text)
	}
	if !strings.Contains(text, "отвечать некому") {
		t.Errorf("ответ не назвал причину словами: %s", text)
	}
	// Текст человека при этом не теряется: строка лежит во входе и ждёт свою
	// сессию.
	src := readFile(t, filepath.Join(e.proj, ".devkit", "chat", "task-XR-7.in"))
	if !strings.Contains(src, "а почему задача заблокирована") {
		t.Fatalf("реплика пропала из входа задачи:\n%s", src)
	}
}

// Повтор недоставленной реплики: строка уже лежит в очереди задачи, и ответ
// обязан сказать это, оставшись при том же признаке недоставки. Живой случай
// DK-466: повтор уходил мимо общего разбора, без undelivered, панель считала
// реплику доставленной, снимала пузырь, и лента пустела совсем.
func TestTaskMessageRepeatStaysUndelivered(t *testing.T) {
	e, c := parkedEnv(t)
	first := body(t, postTaskMessage(t, c, e, "XR-7", "а почему задача заблокирована"))
	if !strings.Contains(first, `"undelivered":true`) {
		t.Fatalf("первая отправка не названа недоставленной: %s", first)
	}
	resp := postTaskMessage(t, c, e, "XR-7", "а почему задача заблокирована")
	text := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("повтор: %d %s", resp.StatusCode, text)
	}
	if !strings.Contains(text, `"undelivered":true`) {
		t.Errorf("повтор назван доставленным, панель снимет пузырь и опустеет: %s", text)
	}
	if !strings.Contains(text, `"repeat":true`) {
		t.Errorf("повтор не назван повтором: %s", text)
	}
	if !strings.Contains(text, "уже лежит") {
		t.Errorf("ответ не говорит человеку, что реплика уже в очереди: %s", text)
	}
	// Второй строки повтор не завёл: очередь по-прежнему из одной реплики.
	src := readFile(t, filepath.Join(e.proj, ".devkit", "chat", "task-XR-7.in"))
	if got := strings.Count(src, "а почему задача заблокирована"); got != 1 {
		t.Fatalf("строк в очереди %d, ожидалась одна:\n%s", got, src)
	}
}

// Отмена недоставленной реплики снимает её из очереди задачи: убрать пузырь с
// экрана мало, лежащая строка уехала бы агенту первым же ходом.
func TestTaskMessageDeleteDropsLine(t *testing.T) {
	e, c := parkedEnv(t)
	postTaskMessage(t, c, e, "XR-7", "отменяемая реплика").Body.Close()
	postTaskMessage(t, c, e, "XR-7", "остающаяся реплика").Body.Close()
	resp := doReq(t, c, "DELETE", e.srv.URL+"/api/projects/demo/tasks/XR-7/message",
		`{"text": "отменяемая реплика"}`)
	text := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("отмена: %d %s", resp.StatusCode, text)
	}
	if !strings.Contains(text, `"dropped":1`) {
		t.Errorf("ответ не назвал снятую строку: %s", text)
	}
	src := readFile(t, filepath.Join(e.proj, ".devkit", "chat", "task-XR-7.in"))
	if strings.Contains(src, "отменяемая реплика") {
		t.Fatalf("отменённая реплика осталась в очереди:\n%s", src)
	}
	if !strings.Contains(src, "остающаяся реплика") {
		t.Fatalf("отмена снесла чужую строку:\n%s", src)
	}
	// Отмена того, чего уже нет, это не ошибка: строку забрал ход агента, и так
	// человеку и сказано.
	again := body(t, doReq(t, c, "DELETE", e.srv.URL+"/api/projects/demo/tasks/XR-7/message",
		`{"text": "отменяемая реплика"}`))
	if !strings.Contains(again, `"dropped":0`) || !strings.Contains(again, "уже нет") {
		t.Errorf("повторная отмена отвечает не тем: %s", again)
	}
}

// Обратный случай: ведущая сессия жива, строку заберёт она, и недоставленной
// реплика не считается.
func TestTaskMessageDeliveredWithLead(t *testing.T) {
	e, c := parkedEnv(t)
	sideTree(t, e.proj, "xr-7")
	writeSession(t, e.home, e.proj, "-xr-7", "aaaa-7777", plainTalk, time.Now())

	resp := postTaskMessage(t, c, e, "XR-7", "ответ ведущей сессии")
	text := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("отправка: %d %s", resp.StatusCode, text)
	}
	if strings.Contains(text, `"undelivered":true`) {
		t.Errorf("реплика названа недоставленной при живой ведущей сессии: %s", text)
	}
}

// Первая реплика нового разговора видна сразу, а не после подъёма сессии:
// пузырь стоял в ленте и раньше, а «улетело в никуда» читалось из самой ленты,
// которая поверх отправленной реплики продолжала просить её написать (замечание
// пользователя). Сторожит стенд testdata/poc_chatfirst.mjs: отказ виден на
// пузыре, порядку реплик путаться не в чем, а перезагрузка между отправкой и
// подъёмом реплику не уносит.
func TestStaticChatFirstSay(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node не найден: стенд первой реплики пропущен")
	}
	out, err := exec.Command(node, filepath.Join("testdata", "poc_chatfirst.mjs"),
		filepath.Join("static", "app.js")).CombinedOutput()
	if err != nil {
		t.Fatalf("первая реплика нового разговора: %v\n%s", err, out)
	}
	t.Log(strings.TrimSpace(string(out)))
}

// Незачатую запись закрывают из списка, где человек её и видит: пустая уходит
// первым нажатием, запись с набранным текстом сперва спрашивает, а за записью с
// сессией и за разговором с лентой остаётся прежняя дорога, архив. Сторожит
// стенд testdata/poc_chatdrop.mjs.
func TestStaticChatDropRow(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node не найден: стенд закрытия записи пропущен")
	}
	out, err := exec.Command(node, filepath.Join("testdata", "poc_chatdrop.mjs"),
		filepath.Join("static", "app.js")).CombinedOutput()
	if err != nil {
		t.Fatalf("закрытие незачатой записи: %v\n%s", err, out)
	}
	t.Log(strings.TrimSpace(string(out)))
}

// Кнопка разговоров в шапке открывает разговор, а не чат открытой задачи:
// «по идее эта кнопка просто открывает чат, для открытия чата задачи есть
// отдельная кнопка на ней же» (замечание пользователя). Сторожит стенд
// testdata/poc_chatsbtn.mjs.
func TestStaticChatsButtonFree(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node не найден: стенд кнопки разговоров пропущен")
	}
	out, err := exec.Command(node, filepath.Join("testdata", "poc_chatsbtn.mjs"),
		filepath.Join("static", "app.js")).CombinedOutput()
	if err != nil {
		t.Fatalf("кнопка разговоров в шапке: %v\n%s", err, out)
	}
	t.Log(strings.TrimSpace(string(out)))
}

// Разговор без процесса и уборка в архив: одна и та же пара, снимающая и
// поднимающая сессию. Живой случай: «чат, который я вернул из архива, больше не
// работает, при написании в него никакой реакции» (замечание пользователя).
// Архивирование снимает сессию, возврат её не поднимает, и разговор без
// процесса на экране неотличим от живого. Сторожит стенд testdata/poc_nosess.mjs.
func TestStaticChatNoSession(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node не найден: стенд разговора без процесса пропущен")
	}
	out, err := exec.Command(node, filepath.Join("testdata", "poc_nosess.mjs"),
		filepath.Join("static", "app.js")).CombinedOutput()
	if err != nil {
		t.Fatalf("разговор без процесса: %v\n%s", err, out)
	}
	t.Log(strings.TrimSpace(string(out)))
}

// Выбор модели в незачатом разговоре: список приезжает от agentctl, выбранное
// живёт при записи и уезжает в подъём первой репликой, а пустая лестница
// названа словами прямо в списке («нельзя поменять модель в новом чате»,
// замечание пользователя). Сторожит стенд testdata/poc_saymodel.mjs.
func TestStaticChatModelPickBlank(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node не найден: стенд выбора модели пропущен")
	}
	out, err := exec.Command(node, filepath.Join("testdata", "poc_saymodel.mjs"),
		filepath.Join("static", "app.js")).CombinedOutput()
	if err != nil {
		t.Fatalf("выбор модели в новом разговоре: %v\n%s", err, out)
	}
	t.Log(strings.TrimSpace(string(out)))
}

// Курсор в поле ввода у только что заведённого разговора: нажатие «+» это и
// есть намерение писать, а человек платил за него вторым нажатием в поле
// (замечание пользователя). Открытый прежний разговор фокуса не перехватывает.
// Сторожит стенд testdata/poc_saynew.mjs.
func TestStaticChatSayFocusFresh(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node не найден: стенд курсора в поле ввода пропущен")
	}
	out, err := exec.Command(node, filepath.Join("testdata", "poc_saynew.mjs"),
		filepath.Join("static", "app.js")).CombinedOutput()
	if err != nil {
		t.Fatalf("курсор в поле ввода свежего разговора: %v\n%s", err, out)
	}
	t.Log(strings.TrimSpace(string(out)))
}

// Курсор в поле поиска списка разговоров ставится по ширине экрана: на
// телефоне выехавшая за ним клавиатура закрывает пол-экрана раньше, чем человек
// взглянул на список (замечание пользователя), а на ноутбуке она ничего не
// закрывает и курсор к месту. Сторожит стенд testdata/poc_chatfocus.mjs.
func TestStaticChatDropFocusByWidth(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node не найден: стенд курсора в списке разговоров пропущен")
	}
	out, err := exec.Command(node, filepath.Join("testdata", "poc_chatfocus.mjs"),
		filepath.Join("static", "app.js")).CombinedOutput()
	if err != nil {
		t.Fatalf("курсор в списке разговоров: %v\n%s", err, out)
	}
	t.Log(strings.TrimSpace(string(out)))
}

// Повтор и отмена недоставленной реплики на экране: пузырь остаётся на месте и
// говорит, что реплика уже в очереди, а отмена снимает её и из самой очереди.
// Предмет проверки это поведение панели, поэтому статика поднимается в node с
// заглушкой DOM (стенд testdata/poc_taskretry.mjs). Без node шаг пропускается:
// узел стенда, а не рабочей части.
func TestStaticTaskRetryKeepsBubble(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node не найден: стенд повтора реплики пропущен")
	}
	out, err := exec.Command(node, filepath.Join("testdata", "poc_taskretry.mjs"),
		filepath.Join("static", "app.js")).CombinedOutput()
	if err != nil {
		t.Fatalf("повтор недоставленной реплики: %v\n%s", err, out)
	}
	t.Log(strings.TrimSpace(string(out)))
}

// Реплика в задачу без ведущей сессии на экране: пузырь стоит недоставленным с
// причиной, текст человека цел и переживает таймеры панели, а рядом выход к
// задаче. Предмет проверки это собранная разметка, поэтому статика поднимается
// в node с заглушкой DOM (стенд testdata/poc_tasknolead.mjs). Без node шаг
// пропускается: узел стенда, а не рабочей части.
func TestStaticTaskReplyWithoutLead(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node не найден: стенд реплики без ведущей сессии пропущен")
	}
	out, err := exec.Command(node, filepath.Join("testdata", "poc_tasknolead.mjs"),
		filepath.Join("static", "app.js")).CombinedOutput()
	if err != nil {
		t.Fatalf("реплика задаче без ведущей сессии: %v\n%s", err, out)
	}
	t.Log(strings.TrimSpace(string(out)))
}

// wedgedEnv поднимает разговор в твёрдом клине: транскрипт замер десять минут
// назад, процесс жив, а tmux-сессии, в которой он был поднят, больше нет.
func wedgedEnv(t *testing.T) (*testEnv, *http.Client, string, time.Time) {
	t.Helper()
	e, c := chatEnv(t)
	now := time.Date(2026, 8, 22, 15, 0, 0, 0, time.UTC)
	e.s.now = func() time.Time { return now }
	sid := "dddd4444-4444-4444-8444-444444444444"
	writeSession(t, e.home, e.proj, "", sid, plainTalk, now.Add(-10*time.Minute))
	writePeer(t, e.home, sid, os.Getpid())
	writeBinds(t, e.home, fmt.Sprintf("2026-08-22T14:40:00 сессия %s задача XR-1 проект demo "+
		"дерево %s транскрипт /tmp/t.jsonl источник заказ повод startup tmux chat-XR-1-1\n", sid, e.proj))
	writeTmuxFake(t, e.bin, filepath.Join(e.home, "tmux.log"), "")
	return e, c, sid, now
}

func postHeal(t *testing.T, c *http.Client, e *testEnv, sid, ask string) string {
	t.Helper()
	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/heal", ask)
	text := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("заявка на лечение: %d %s", resp.StatusCode, text)
	}
	return text
}

// Твёрдый клин виден полем heal: по нему панель и решает, лечить ли разговор
// сама. Третий род стоящего чата (клиент спросил в терминале) таким полем не
// помечается никогда: там нужен человек живьём, а снятие процесса необратимо.
func TestChatEntryFirmWedgeIsHealable(t *testing.T) {
	e, c, _, _ := wedgedEnv(t)
	got := chatsOf(t, e, c)
	if len(got) != 1 {
		t.Fatalf("чатов в списке %d, ждал один: %+v", len(got), got)
	}
	if !got[0].Heal {
		t.Errorf("твёрдый клин не помечен лечимым: %+v", got[0])
	}
}

// Клин лечится один раз подряд. Второй заход в то же окно получает отказ и
// строку в ленте: перезапуск по кругу хуже самого клина, а снятие процесса
// необратимо.
func TestChatHealClaimOnceThenRefuses(t *testing.T) {
	e, c, sid, now := wedgedEnv(t)

	if text := postHeal(t, c, e, sid, "{}"); !strings.Contains(text, `"claim":true`) {
		t.Fatalf("первая заявка на лечение отклонена: %s", text)
	}
	// Соседняя вкладка в ту же секунду: это то же лечение, а не второй клин, и
	// говорить про повтор тут нечего.
	text := postHeal(t, c, e, sid, "{}")
	if strings.Contains(text, `"claim":true`) {
		t.Errorf("вторая вкладка получила согласие и сняла бы процесс второй раз: %s", text)
	}
	if strings.Contains(text, "завис снова") {
		t.Errorf("гонка вкладок названа повторным клином: %s", text)
	}

	// Клин повторился после перезапуска: отказ со словами, и слова эти ложатся
	// в ленту разговора.
	e.s.now = func() time.Time { return now.Add(5 * time.Minute) }
	text = postHeal(t, c, e, sid, "{}")
	if strings.Contains(text, `"claim":true`) {
		t.Fatalf("повторный клин полез лечиться второй раз: %s", text)
	}
	if !strings.Contains(text, "завис снова") {
		t.Errorf("повторный клин не назван словами: %s", text)
	}
	said := readFile(t, filepath.Join(e.home, "said", "sess-"+sid+".jsonl"))
	if !strings.Contains(said, "больше не перезапускаю") {
		t.Errorf("про повторный клин в ленте не сказано: %s", said)
	}
	if got := strings.Count(said, "больше не перезапускаю"); got != 1 {
		t.Errorf("строк про повторный клин в ленте %d, ждал одну: %s", got, said)
	}
}

// Заявка опирается на то, что сервер видит сам, а не на слова клиента: без
// твёрдого признака клина согласия нет, и процесс никто не трогает.
func TestChatHealRefusesWithoutFirmWedge(t *testing.T) {
	e, c := chatEnv(t)
	now := time.Date(2026, 8, 22, 15, 0, 0, 0, time.UTC)
	e.s.now = func() time.Time { return now }
	sid := "eeee5555-5555-4555-8555-555555555555"
	// Транскрипт свежий: хода нет секунды, и это пауза между ходами, а не клин.
	writeSession(t, e.home, e.proj, "", sid, plainTalk, now.Add(-10*time.Second))
	writePeer(t, e.home, sid, os.Getpid())
	writeTmuxFake(t, e.bin, filepath.Join(e.home, "tmux.log"), "")

	text := postHeal(t, c, e, sid, "{}")
	if strings.Contains(text, `"claim":true`) {
		t.Fatalf("лечение согласовано без твёрдого признака клина: %s", text)
	}
	if !strings.Contains(text, "не трогаю") {
		t.Errorf("отказ не сказал, что ничего не делается: %s", text)
	}
}

// Отчёт о лечении кладёт в ленту одну спокойную строку, пометкой, как
// разделитель смены модели. Карточки-тревоги тут нет: это запись о том, что
// случилось, а не вопрос человеку.
func TestChatHealDoneMarksFeed(t *testing.T) {
	e, c, sid, _ := wedgedEnv(t)
	postHeal(t, c, e, sid, `{"done": true}`)
	said := readFile(t, filepath.Join(e.home, "said", "sess-"+sid+".jsonl"))
	if !strings.Contains(said, "разговор перезапущен, продолжаю") {
		t.Fatalf("строки о перезапуске в ленте нет: %s", said)
	}
	if !strings.Contains(said, `"kind":"mark"`) {
		t.Errorf("строка о перезапуске легла не пометкой, а пузырём: %s", said)
	}

	postHeal(t, c, e, sid, `{"done": false}`)
	said = readFile(t, filepath.Join(e.home, "said", "sess-"+sid+".jsonl"))
	if !strings.Contains(said, "перезапустить не вышло") {
		t.Errorf("провал лечения смолчал: %s", said)
	}
}

// Клин в панели: плашки с кнопкой нет, твёрдый признак лечится сам двумя шагами
// в правильном порядке, отказ сервера и сомнение не трогают ничего. Предмет
// проверки это собранная разметка и порядок вызовов, поэтому статика
// поднимается в node с заглушкой DOM (стенд testdata/poc_wedge.mjs). Без node
// шаг пропускается: узел стенда, а не рабочей части.
func TestStaticWedgeHealsItself(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node не найден: стенд самолечения клина пропущен")
	}
	out, err := exec.Command(node, filepath.Join("testdata", "poc_wedge.mjs"),
		filepath.Join("static", "app.js")).CombinedOutput()
	if err != nil {
		t.Fatalf("самолечение клина: %v\n%s", err, out)
	}
	t.Log(strings.TrimSpace(string(out)))
}

// Смена проекта под открытым разговором, вторая половина жалобы («чат иногда
// переключается на новый пустой диалог»). Адрес сессии переезд переживал и
// раньше, а чат задачи, общий чат доски и новый чат читались «в том проекте,
// что сейчас на доске», и смена доски перечитывала их заново. Предмет проверки
// это адрес панели и её содержимое после нажатия соседнего проекта, поэтому
// статика поднимается в node с заглушкой DOM (стенд testdata/poc_chatkeep.mjs).
// Без node шаг пропускается: узел стенда, а не рабочей части.
func TestStaticChatKeepsProjectOnSwitch(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node не найден: стенд смены проекта под разговором пропущен")
	}
	out, err := exec.Command(node, filepath.Join("testdata", "poc_chatkeep.mjs"),
		filepath.Join("static", "app.js")).CombinedOutput()
	if err != nil {
		t.Fatalf("смена проекта под разговором: %v\n%s", err, out)
	}
	t.Log(strings.TrimSpace(string(out)))
}

// Возврат в разговор из пула поднимает заново живое панели, а кольцо хода в
// шапке оставалось мёртвым: его опрос умирал любым уходом из разговора, и
// законченная работа крутилась на экране оставшимися пунктами до обновления
// страницы (бага пользователя). Стенд testdata/poc_ringwake.mjs поднимает
// статику в node с заглушкой DOM и меряет переподъём опроса кольца. Без node
// шаг пропускается: узел стенда, а не рабочей части.
func TestStaticChatRingWakesOnReturn(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node не найден: стенд переподъёма кольца пропущен")
	}
	out, err := exec.Command(node, filepath.Join("testdata", "poc_ringwake.mjs"),
		filepath.Join("static", "app.js")).CombinedOutput()
	if err != nil {
		t.Fatalf("кольцо не поднялось возвратом в разговор: %v\n%s", err, out)
	}
	t.Log(strings.TrimSpace(string(out)))
}

// Клиент, вставший на вопросе доверия каталогу, в журнале уведомителя не
// оставляет ни строки: сессия ещё не родилась, своего ID у неё нет, и хук не
// сработал ни разу. Прежняя мера тут молчала, и список показывал такой
// разговор живым, а человек узнавал про вопрос, только открыв панель. Ровно
// так простояли два чата xr-proxy (замечание пользователя 2026-08-28), и
// теперь список спрашивает саму панель клиента.
func TestChatEntryAskFromPaneWithoutJournal(t *testing.T) {
	e, c := chatEnv(t)
	now := time.Date(2026, 8, 28, 17, 30, 0, 0, time.UTC)
	e.s.now = func() time.Time { return now }
	sid := "eeee5555-5555-4555-8555-555555555555"
	writeSession(t, e.home, e.proj, "", sid, plainTalk, now.Add(-30*time.Second))
	writeBinds(t, e.home, fmt.Sprintf("2026-08-28T17:29:00 сессия %s задача XR-1 проект demo "+
		"дерево %s транскрипт /tmp/t.jsonl источник заказ повод startup tmux chat-13\n", sid, e.proj))
	writeTmuxFake(t, e.bin, filepath.Join(e.home, "tmux.log"), "chat-13\t1\t1786000000\n")
	old := tmuxAskOfFn
	tmuxAskOfFn = func(string) tmuxAsk { return parseTmuxAsk(liveTrustBarePane) }
	t.Cleanup(func() { tmuxAskOfFn = old })

	got := chatsOf(t, e, c)
	if len(got) != 1 {
		t.Fatalf("чатов в списке %d, ждал один: %+v", len(got), got)
	}
	if got[0].Stuck != stuckAskWord {
		t.Errorf("вопрос с панели не узнан: stuck=%q, ждал %q", got[0].Stuck, stuckAskWord)
	}

	// Панель молчит, значит и слова нет: работающий клиент не стоящий.
	e.s.askSeen = nil
	tmuxAskOfFn = func(string) tmuxAsk { return tmuxAsk{} }
	if got := chatsOf(t, e, c); got[0].Stuck != "" {
		t.Errorf("слово вопроса встало на работающем клиенте: %q", got[0].Stuck)
	}
}

// Истёкший вход клиента (DK-466). Клиент с протухшим OAuth-токеном отвечает
// служебной строкой на любую реплику и не делает ни хода. Знал про такой ответ
// один titleJunk, и то лишь затем, чтобы не пустить его в заголовок: в списке
// и в панели разговор оставался живым, а отказ стоял обычным пузырём ленты.
// Теперь это состояние разговора, и считается оно по последнему ответу агента.
func TestLoginGoneWords(t *testing.T) {
	yes := []string{
		"Login expired. Please run /login",
		"Not logged in",
		"Invalid API key. Please run /login",
		"  login expired  ",
		"OAuth token expired",
	}
	for _, said := range yes {
		if !loginGone(said) {
			t.Errorf("отказ входа не узнан: %q", said)
		}
	}
	no := []string{
		"",
		"готово, ветка собрана",
		// Рассказ про чужой разлогин это рассказ, а не отказ: длина и держит
		// эту границу, потому что других признаков у служебной строки нет.
		"Разобрал инцидент: вчерашние сессии chat-DK-397-1 и chat-DK-397-2 " +
			"отвечали Login expired после чужого /login, потому что живой процесс " +
			"держит старый токен в памяти и сам его не перечитывает. Вылечилось " +
			"снятием их tmux-сессий, продолжение поднял штатный резюм дашборда.",
	}
	for _, said := range no {
		if loginGone(said) {
			t.Errorf("обычный ответ принят за отказ входа: %q", said)
		}
	}
}

// Строка списка называет разлогин словами, и слово это гаснет само: следующий
// настоящий ответ агента снимает состояние без всякой отметки со стороны
// человека.
func TestChatEntryLoginGone(t *testing.T) {
	e, c := chatEnv(t)
	sid := "dddd4660-4660-4660-8660-466046604660"
	said := func(text, at string) string {
		return `{"type":"assistant","message":{"role":"assistant","model":"claude-opus-4",` +
			`"content":[{"type":"text","text":"` + text + `"}]},"timestamp":"` + at + `"}` + "\n"
	}
	talk := plainTalk + said("Login expired. Please run /login", "2026-08-17T10:00:02.000Z")
	writeSession(t, e.home, e.proj, "", sid, talk, time.Now())

	got := chatsOf(t, e, c)
	if len(got) != 1 {
		t.Fatalf("чатов в списке %d, ждал один: %+v", len(got), got)
	}
	if got[0].Login != loginGoneWord {
		t.Fatalf("разлогин не назван словами: login=%q, ждал %q", got[0].Login, loginGoneWord)
	}
	// Слова состояния про дело человека, а не про наше устройство. «Сессия
	// разлогинена» говорило про сессию, которой человек не заводил, и его же
	// приходилось объяснять целым абзацем на плашке (замечание пользователя).
	// Место в строке списка узкое, поэтому слов немного.
	for _, own := range []string{"сесси", "токен", "oauth", "клиент"} {
		if strings.Contains(strings.ToLower(loginGoneWord), own) {
			t.Fatalf("состояние названо нашим устройством (%q): %q", own, loginGoneWord)
		}
	}
	if n := len([]rune(loginGoneWord)); n > 16 {
		t.Fatalf("слова состояния не влезут в строку списка: %d знаков в %q", n, loginGoneWord)
	}
	if got[0].Stuck != "" {
		t.Errorf("разлогин выдан за клин: stuck=%q, а лечится он не перезапуском, а входом", got[0].Stuck)
	}

	// Человек написал после отказа: разговор от этого рабочим не стал.
	after := talk + `{"type":"user","message":{"role":"user","content":"ты тут?"},` +
		`"timestamp":"2026-08-17T10:05:00.000Z"}` + "\n"
	writeSession(t, e.home, e.proj, "", sid, after, time.Now().Add(time.Second))
	if got := chatsOf(t, e, c); got[0].Login != loginGoneWord {
		t.Errorf("реплика человека погасила состояние: login=%q", got[0].Login)
	}

	// Вошли на машине, разговор перезапустили, агент ответил по делу: состояние
	// гаснет само.
	live := after + said("продолжаю с того места, где остановился", "2026-08-17T10:06:00.000Z")
	writeSession(t, e.home, e.proj, "", sid, live, time.Now().Add(2*time.Second))
	if got := chatsOf(t, e, c); got[0].Login != "" {
		t.Errorf("состояние не погасло после живого ответа: login=%q", got[0].Login)
	}
}

// Лента помечает служебную строку сама: панель поднимает по этой пометке
// состояние разлогина, не разбирая английских слов у себя. Сама строка из ленты
// не прячется, это и правда сказал агент.
func TestFeedMarksLoginReply(t *testing.T) {
	e, c := chatEnv(t)
	forgetChunks()
	sid := "cccc4661-4661-4661-8661-466146614661"
	said := func(text, at string) string {
		return `{"type":"assistant","message":{"role":"assistant","model":"claude-opus-4",` +
			`"content":[{"type":"text","text":"` + text + `"}]},"timestamp":"` + at + `"}` + "\n"
	}
	talk := plainTalk +
		said("Login expired. Please run /login", "2026-08-17T10:00:02.000Z") +
		said("продолжаю", "2026-08-17T10:06:00.000Z")
	writeSession(t, e.home, e.proj, "", sid, talk, time.Now())

	resp := doReq(t, c, "GET", e.srv.URL+"/api/projects/demo/sessions/"+sid+"?n=40", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("лента: %d", resp.StatusCode)
	}
	var got struct {
		Items []reply `json:"items"`
	}
	if err := json.Unmarshal([]byte(body(t, resp)), &got); err != nil {
		t.Fatal(err)
	}
	marks := map[string]bool{}
	for _, it := range got.Items {
		if it.Role == "assistant" {
			marks[it.Text] = it.Logout
		}
	}
	if !marks["Login expired. Please run /login"] {
		t.Errorf("служебная строка в ленте не помечена: %+v", got.Items)
	}
	if marks["продолжаю"] {
		t.Errorf("настоящий ответ помечен разлогином: %+v", got.Items)
	}
}

// Разлогин в панели: состояние говорится репликой в самом чате, слова говорят
// порядок починки, кнопка снимает сессию и поднимает её резюмом прерванного
// запроса, а живой ответ агента гасит блок сам и поднимает заново, если вход
// истёк второй раз. Тут же стенд читает style.css: hidden это атрибут, прячет
// его встроенное правило браузера, и авторский display его перебивал. Предмет
// проверки это собранная разметка и порядок вызовов, поэтому статика
// поднимается в node с заглушкой DOM (стенд
// testdata/poc_login.mjs). Без node шаг пропускается: узел стенда, а не рабочей
// части.
func TestStaticLoginGonePlate(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node не найден: стенд разлогиненного разговора пропущен")
	}
	out, err := exec.Command(node, filepath.Join("testdata", "poc_login.mjs"),
		filepath.Join("static", "app.js")).CombinedOutput()
	if err != nil {
		t.Fatalf("разлогин в панели: %v\n%s", err, out)
	}
	t.Log(strings.TrimSpace(string(out)))
}

// Вход клиента в панели: кнопка поднимает вход репликой чата, ссылка стоит
// человеческим текстом с копированием, с самой машины поля кода нет вовсе, а с
// другого устройства код едет своим полем мимо ленты и мимо журнала разговора.
// Стенд лежал в дереве с самого начала работы над входом, но гонять его было
// некому: драйвера у него не было, и падал бы он молча (стенд
// testdata/poc_loginlink.mjs). Без node шаг пропускается: узел стенда, а не
// рабочей части.
func TestStaticLoginLinkFlow(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node не найден: стенд входа клиента пропущен")
	}
	out, err := exec.Command(node, filepath.Join("testdata", "poc_loginlink.mjs"),
		filepath.Join("static", "app.js")).CombinedOutput()
	if err != nil {
		t.Fatalf("вход клиента в панели: %v\n%s", err, out)
	}
	t.Log(strings.TrimSpace(string(out)))
}

// Вид записей входа: раскладка снимается числом с настоящей разметки и
// настоящего style.css и сверяется с записью ленты на узком экране и на
// широком. Пользователь на приёмке сказал, что записи входа читаются гостями:
// «отличаются от стандартных блоков чата по размеру и оформлению». Числа тут
// нужны затем, чтобы расхождение ловилось впредь, а не подгонялось на глаз
// (стенд testdata/poc_loginfit.mjs). Без node шаг пропускается: узел стенда, а
// не рабочей части.
func TestStaticLoginFitsFeed(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node не найден: стенд вида записей входа пропущен")
	}
	out, err := exec.Command(node, filepath.Join("testdata", "poc_loginfit.mjs"),
		filepath.Join("static", "app.js")).CombinedOutput()
	if err != nil {
		t.Fatalf("вид записей входа: %v\n%s", err, out)
	}
	t.Log(strings.TrimSpace(string(out)))
}

// Панель ждущего разговора: пока сессия называет себя в реестре, панель стоит
// со словами о подъёме, а не хоронит разговор и не показывает плашку
// протухшего адреса. Стенд лежал в дереве без драйвера, гонять его было некому
// (стенд testdata/poc_chatlift.mjs). Без node шаг пропускается: узел стенда, а
// не рабочей части.
func TestStaticChatLift(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node не найден: стенд ждущей панели пропущен")
	}
	out, err := exec.Command(node, filepath.Join("testdata", "poc_chatlift.mjs"),
		filepath.Join("static", "app.js")).CombinedOutput()
	if err != nil {
		t.Fatalf("панель ждущего разговора: %v\n%s", err, out)
	}
	t.Log(strings.TrimSpace(string(out)))
}

// Свежесть списка разговоров: открытие спрашивает перечень заново, поэтому в
// нём виден и свой новый чат, и заведённый на стороне, и свежий заголовок, а
// закрытая крестиком запись уходит сразу. Шаги пользователя: завести чат,
// написать в нём, перейти в другой, открыть список, и нового чата там нет до
// перезагрузки страницы (стенд testdata/poc_chatlist.mjs). Без node шаг
// пропускается: узел стенда, а не рабочей части.
func TestStaticChatListFresh(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node не найден: стенд свежести списка чатов пропущен")
	}
	out, err := exec.Command(node, filepath.Join("testdata", "poc_chatlist.mjs"),
		filepath.Join("static", "app.js")).CombinedOutput()
	if err != nil {
		t.Fatalf("свежесть списка чатов: %v\n%s", err, out)
	}
	t.Log(strings.TrimSpace(string(out)))
}

// Выравнивание записей ленты: у текста всех видов один левый край и одна
// правая граница. Пользователь на снимке: «позиция блоков сообщений в чате
// слева или отступ плавает, все блоки чата должны быть одинаковой ширины и
// одинаково выровнены». Виды в наборе сняты с живой ленты разговора, а не
// выдуманы (стенд testdata/poc_feedfit.mjs, набор testdata/feed_kinds.json).
// Без node шаг пропускается: узел стенда, а не рабочей части.
func TestStaticFeedKindsAligned(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node не найден: стенд выравнивания ленты пропущен")
	}
	out, err := exec.Command(node, filepath.Join("testdata", "poc_feedfit.mjs"),
		filepath.Join("static", "app.js")).CombinedOutput()
	if err != nil {
		t.Fatalf("выравнивание записей ленты: %v\n%s", err, out)
	}
	t.Log(strings.TrimSpace(string(out)))
}

// Варианты в блоке вопроса клиента видны кнопками: рамка, заливка, отклик на
// наведение и на нажатие, палец на узком экране. Пользователь прислал снимок
// блока и спросил, что это за странность: нажимать было можно, а понять это по
// виду нельзя, и человек шёл отвечать в терминал (стенд
// testdata/poc_caskopt.mjs). Без node шаг пропускается: узел стенда, а не
// рабочей части.
func TestStaticClientAskOptionsLookClickable(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node не найден: стенд вида вариантов вопроса пропущен")
	}
	out, err := exec.Command(node, filepath.Join("testdata", "poc_caskopt.mjs"),
		filepath.Join("static", "app.js")).CombinedOutput()
	if err != nil {
		t.Fatalf("вид вариантов вопроса клиента: %v\n%s", err, out)
	}
	t.Log(strings.TrimSpace(string(out)))
}

// Единая шапка свёрнутых блоков ленты. Пользователь по снимку чата задачи:
// у блока «Фоновый агент: killed» кнопка разворота стояла вплотную к заголовку
// и без копирования, у вести с длинной сводкой шеврон уезжал за край экрана, а
// сводка не резалась многоточием, как у блоков команд. Шапки всех свёрнутых
// блоков собирает один сборщик (стенд testdata/poc_feedhead.mjs). Без node шаг
// пропускается: узел стенда, а не рабочей части.
func TestStaticFoldHeadsShareBase(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node не найден: стенд шапок свёрнутых блоков пропущен")
	}
	out, err := exec.Command(node, filepath.Join("testdata", "poc_feedhead.mjs"),
		filepath.Join("static", "app.js")).CombinedOutput()
	if err != nil {
		t.Fatalf("шапки свёрнутых блоков: %v\n%s", err, out)
	}
	t.Log(strings.TrimSpace(string(out)))
}

// Вертикальный шаг ленты: у всякой записи поле сверху одним токеном, снизу
// поля нет. Живой случай из чата «Выполнение задачи DK-656»: колонка ленты
// флексовая, поля соседей в ней складываются, и две капсулы фоновой работы
// стояли друг к другу вдвое просторнее, чем к соседним репликам (стенд
// testdata/poc_feedstep.mjs). Без node шаг пропускается: узел стенда, а не
// рабочей части.
func TestStaticFeedStepUniform(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node не найден: стенд шага ленты пропущен")
	}
	out, err := exec.Command(node, filepath.Join("testdata", "poc_feedstep.mjs"),
		filepath.Join("static", "app.js")).CombinedOutput()
	if err != nil {
		t.Fatalf("шаг ленты: %v\n%s", err, out)
	}
	t.Log(strings.TrimSpace(string(out)))
}

// Нить бокового журнала субагента: отрезок её это вся работа, от вызова Agent
// до вести о том, что фоновый агент закончил, и по дороге нить не рвётся.
// Пользователь с экрана: «синяя нить начинается не с сообщения субагента, а со
// следующего сообщения и завершается, не доходя до блока про конец фоновой
// работы» (стенд testdata/poc_subthread.mjs). Без node шаг
// пропускается: узел стенда, а не рабочей части.
func TestStaticSubThreadSpansWork(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node не найден: стенд нити бокового журнала пропущен")
	}
	out, err := exec.Command(node, filepath.Join("testdata", "poc_subthread.mjs"),
		filepath.Join("static", "app.js")).CombinedOutput()
	if err != nil {
		t.Fatalf("нить бокового журнала субагента: %v\n%s", err, out)
	}
	t.Log(strings.TrimSpace(string(out)))
}

// Пересказ съеденного начала разговора в ленте: свёрнутый блок со своим
// заголовком, разворотом и копированием, а не пузырь человека. Живой случай
// из чата «Выполнение XR-279»: харнес кладёт пересказ записью роли user, и
// человек нашёл в чате портянку на несколько тысяч слов, подписанную им самим
// (стенд testdata/poc_compact.mjs). Без node шаг пропускается: узел стенда, а
// не рабочей части.
func TestStaticCompactSummaryFolded(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node не найден: стенд записи о сжатии пропущен")
	}
	out, err := exec.Command(node, filepath.Join("testdata", "poc_compact.mjs"),
		filepath.Join("static", "app.js")).CombinedOutput()
	if err != nil {
		t.Fatalf("запись о сжатии разговора: %v\n%s", err, out)
	}
	t.Log(strings.TrimSpace(string(out)))
}

// Зазор над полем правки: расстояние до соседа сверху одинаково в покое и в
// правке и не ноль. Пользователь выделил поле заголовка на экране задачи: «при
// включении редактирования вот этот блок верхней границей касается элементов в
// блоке выше». В покое коробки поля не видно, и нулевой зазор никого не трогал
// (стенд testdata/poc_editgap.mjs). Без node шаг пропускается: узел стенда, а
// не рабочей части.
func TestStaticEditGapKept(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node не найден: стенд зазора над полем правки пропущен")
	}
	out, err := exec.Command(node, filepath.Join("testdata", "poc_editgap.mjs"),
		filepath.Join("static", "app.js")).CombinedOutput()
	if err != nil {
		t.Fatalf("зазор над полем правки: %v\n%s", err, out)
	}
	t.Log(strings.TrimSpace(string(out)))
}

// Рамка поля правки: у поля внутри карточки рамка одна, поле отличимо от покоя
// и названо в фокусе. Пользователь выделил на экране задачи карточку панели и
// поле внутри неё: «нужно убрать вот эту двойную рамку при включении
// редактирования». Числа тут снимаются с настоящего style.css, чтобы
// расхождение ловилось впредь (стенд testdata/poc_editframe.mjs). Без node шаг
// пропускается: узел стенда, а не рабочей части.
func TestStaticEditFrameSingle(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node не найден: стенд рамки поля правки пропущен")
	}
	out, err := exec.Command(node, filepath.Join("testdata", "poc_editframe.mjs"),
		filepath.Join("static", "app.js")).CombinedOutput()
	if err != nil {
		t.Fatalf("рамка поля правки: %v\n%s", err, out)
	}
	t.Log(strings.TrimSpace(string(out)))
}

// Вид нового чата у привязанного разговора (жалоба пользователя: «нажал + в
// этом чате, а выбора не было; разве он не привязан к задаче?»). Кнопка
// спрашивала задачу панели, а та гаснет у чужой доски и заодно фильтрует
// список разговоров, поэтому привязанный разговор молча заводил свободный чат.
// Предмет проверки это собранная разметка и порядок заказов, поэтому статика
// поднимается в node с заглушкой DOM (стенд testdata/poc_maketask.mjs). Без
// node шаг пропускается: узел стенда, а не рабочей части.
func TestStaticChatMakeTask(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node не найден: стенд вида нового чата пропущен")
	}
	out, err := exec.Command(node, filepath.Join("testdata", "poc_maketask.mjs"),
		filepath.Join("static", "app.js")).CombinedOutput()
	if err != nil {
		t.Fatalf("вид нового чата: %v\n%s", err, out)
	}
	t.Log(strings.TrimSpace(string(out)))
}

// Группировка списка чатов: открытый чат своей подписанной группой сверху,
// под ним подписанная группа активных, дальше дни. Пока список открыт, уборка
// гасит строку на месте, не переставляя соседей, и новый порядок берётся
// только следующим открытием (жалоба пользователя: строки убегали под
// курсором при уборке пачкой, а живой чат трёхдневной давности стоял выше
// сегодняшнего мёртвого без подписи, читаясь поломкой сортировки). Стенд
// testdata/poc_chatgroup.mjs. Без node шаг пропускается: узел стенда, а не
// рабочей части.
func TestStaticChatListGroups(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node не найден: стенд группировки списка чатов пропущен")
	}
	out, err := exec.Command(node, filepath.Join("testdata", "poc_chatgroup.mjs"),
		filepath.Join("static", "app.js")).CombinedOutput()
	if err != nil {
		t.Fatalf("группировка списка чатов: %v\n%s", err, out)
	}
	t.Log(strings.TrimSpace(string(out)))
}

// Уборка текущего разговора в архив переводит панель на следующий из
// оставшихся, без второго захода в список (жалоба пользователя: «после
// закрытия чата диалог сам не закрывается»). Стенд testdata/poc_chatnext.mjs.
// Без node шаг пропускается: узел стенда, а не рабочей части.
func TestStaticChatArchiveCurrentSwitchesPanel(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node не найден: стенд переключения панели пропущен")
	}
	out, err := exec.Command(node, filepath.Join("testdata", "poc_chatnext.mjs"),
		filepath.Join("static", "app.js")).CombinedOutput()
	if err != nil {
		t.Fatalf("переключение панели после уборки текущего разговора: %v\n%s", err, out)
	}
	t.Log(strings.TrimSpace(string(out)))
}
