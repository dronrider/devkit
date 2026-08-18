package main

import (
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

// Реплика в сессию задачи ложится в разговор task-<ID> её бокового дерева,
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
	if !strings.Contains(text, "разговор task-XR-4") || !strings.Contains(text, "подхват доставит") {
		t.Errorf("ответ не называет разговор и доставку: %s", text)
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
