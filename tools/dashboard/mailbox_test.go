package main

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Почта любой живой сессии (DK-345): реплика уходит в ящик .devkit/mail дерева
// работы с адресатом в строке, доставляет её почтальон hooks/inbox.py. Стенды
// на фикстурной доске (runsBoardJSON) и транскриптах из writeSession, боковое
// дерево задачи рисуется каталогом рядом с проектом.

const plainTalk = `{"type":"user","message":{"role":"user","content":"работа идёт"},"timestamp":"2026-08-17T10:00:01.000Z","gitBranch":"main"}` + "\n"

// Доска без набитых нулями номеров: хвост бокового дерева xr-4 узнаёт задачу
// XR-4 без правки номера, цель XR-100 остаётся для отказа своей ручкой.
const mailboxBoardJSON = `{"prefix":"XR","sections":[` +
	`{"key":"in-progress","title":"In progress","rows":[` +
	`{"id":"XR-4","title":"Начатая задача","type":"task","p":"P2","r":31,"r_parts":[25,3,1,0,2],"cost":"-","link":"-"},` +
	`{"id":"XR-100","title":"Цель: пробный цикл","type":"task","p":"P2","r":41,"r_parts":[25,9,3,0,4],"cost":"XL","link":"-"}]}]}`

// mailboxEnv поднимает окружение с доской XR и клиентом.
func mailboxEnv(t *testing.T) (*testEnv, *http.Client) {
	t.Helper()
	e := newTestEnv(t)
	writeScript(t, e.bin, "taskctl", fmt.Sprintf("echo '%s'", mailboxBoardJSON))
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

// Реплика в сессию задачи ложится в ящик task-<ID> её бокового дерева, строкой
// с адресатом сессии и подписью дашборда, тем же форматом, что разбирает
// почтальон.
func TestSessionMessageLandsInTaskBox(t *testing.T) {
	e, c := mailboxEnv(t)
	tree := sideTree(t, e.proj, "xr-4")
	writeSession(t, e.home, e.proj, "-xr-4", "aaaa-1111", plainTalk, time.Now())

	resp := postSessionMessage(t, c, e, "aaaa-1111", "привет исполнитель")
	text := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("отправка: %d %s", resp.StatusCode, text)
	}
	if !strings.Contains(text, "ящик task-XR-4") || !strings.Contains(text, "почтальон доставит") {
		t.Errorf("ответ не называет ящик и доставку: %s", text)
	}
	box := readFile(t, filepath.Join(tree, ".devkit", "mail", "task-XR-4.inbox"))
	if !strings.Contains(box, ", сессии aaaa-1111, из дашборда: привет исполнитель") {
		t.Fatalf("в ящике нет строки с адресатом и подписью:\n%s", box)
	}
	if strings.Contains(readFile(t, filepath.Join(e.proj, ".devkit", "mail", "sess-aaaa-1111.inbox")), "привет") {
		t.Errorf("реплика легла в личный ящик основного дерева, хотя у сессии есть задача")
	}
}

// Сессия без распознанной задачи получает личный ящик в главном дереве: ящик
// называется сессией, и письмо в нём адресуется ей же.
func TestSessionMessageLandsInPersonalBox(t *testing.T) {
	e, c := mailboxEnv(t)
	writeSession(t, e.home, e.proj, "", "bbbb-2222", plainTalk, time.Now())

	resp := postSessionMessage(t, c, e, "bbbb-2222", "чем занят")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("отправка: %d %s", resp.StatusCode, body(t, resp))
	}
	box := readFile(t, filepath.Join(e.proj, ".devkit", "mail", "sess-bbbb-2222.inbox"))
	if !strings.Contains(box, ", сессии bbbb-2222, из дашборда: чем занят") {
		t.Fatalf("в личном ящике нет строки:\n%s", box)
	}
}

// Повтор того же текста второй строки не заводит: лежащее письмо уже ждёт
// сессию, и дубль приехал бы ей дважды.
func TestSessionMessageRepeatKeepsOneLetter(t *testing.T) {
	e, c := mailboxEnv(t)
	writeSession(t, e.home, e.proj, "", "bbbb-2222", plainTalk, time.Now())
	for i := 0; i < 2; i++ {
		resp := postSessionMessage(t, c, e, "bbbb-2222", "один вопрос")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("отправка %d: %d %s", i, resp.StatusCode, body(t, resp))
		}
	}
	box := readFile(t, filepath.Join(e.proj, ".devkit", "mail", "sess-bbbb-2222.inbox"))
	if got := strings.Count(box, "один вопрос"); got != 1 {
		t.Fatalf("писем в ящике %d, ожидалось одно:\n%s", got, box)
	}
}

// Сессия задачи-цели отправляется к ручке цели: у цели свой носитель,
// «Входящие» её файла, и второй ящик рядом расколол бы переписку надвое.
func TestSessionMessageRefusesGoalSession(t *testing.T) {
	e, c := mailboxEnv(t)
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

// Протухший транскрипт это честный «не идёт»: письмо ляжет и дождётся хода,
// но обещать доставку сейчас ручка не вправе (та же честность, что у DK-319).
func TestSessionMessageAtStaleSessionSaysSo(t *testing.T) {
	e, c := mailboxEnv(t)
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

// Неизвестная сессия и удалённое боковое дерево отказываются словами: письмо
// без читателя лежало бы мёртвым грузом, и молчать об этом нельзя.
func TestSessionMessageRefusals(t *testing.T) {
	e, c := mailboxEnv(t)
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
