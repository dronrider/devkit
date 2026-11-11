package main

// Стенд дороги app-server: подменной agentctl разворачивает заказ в подменной
// клиент чужого протокола, а тот говорит JSONL проверенного контракта
// (DK-1624). Живой клиент стендом не зовётся: раскладка имена не проверяет, и
// дорогое входное окружение тут ни при чём. Поведением клиента правит файл
// fake-app-mode в каталоге разговора, а его заказы пишет fake-app-log.

import (
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// appHarnessJSON это раскладка стенда: подписка по умолчанию и две подписки
// чужого протокола, вторая нужна смене модели.
const appHarnessJSON = `{"default": "перваяtest", "source": "фикстура", "harnesses": [
  {"name": "перваяtest", "enabled": true, "default": true, "bin": "клиент-1",
   "models": [{"tier": "pro", "model": "модель-pro"}]},
  {"name": "codextest", "enabled": true, "default": false, "bin": "клиент-app",
   "protocol": "codex", "models": [{"tier": "pro", "model": "модель-codex"}]},
  {"name": "codex2test", "enabled": true, "default": false, "bin": "клиент-app",
   "protocol": "codex", "models": [{"tier": "pro", "model": "модель-codex2"}]}
]}`

// appClientPy это подменной клиент: отвечает на запросы контракта, элементы
// хода шлёт уведомлениями, а режимом из fake-app-mode умеет молчать ходом
// (mute), умирать на реплике (die) и не слышать прерывание (deaf).
const appClientPy = `
import json, os, sys

def emit(obj):
    sys.stdout.write(json.dumps(obj, ensure_ascii=False) + "\n")
    sys.stdout.flush()

def note(method, params):
    emit({"method": method, "params": params})

def mode():
    try:
        return open(os.path.join(os.getcwd(), "fake-app-mode")).read().strip()
    except OSError:
        return ""

def log(line):
    with open(os.path.join(os.getcwd(), "fake-app-log"), "a") as f:
        f.write(line + "\n")

for raw in sys.stdin:
    raw = raw.strip()
    if not raw:
        continue
    msg = json.loads(raw)
    mid = msg.get("id")
    m = msg.get("method", "")
    params = msg.get("params") or {}
    if m == "initialize":
        emit({"id": mid, "result": {}})
    elif m == "thread/start":
        log("start " + str(params.get("model", "")))
        emit({"id": mid, "result": {"thread": {"id": "th-1"}}})
    elif m == "thread/resume":
        log("resume " + str(params.get("threadId", "")))
        emit({"id": mid, "result": {"thread": {"id": params.get("threadId")}}})
    elif m == "turn/start":
        log("turn")
        text = ""
        for part in params.get("input") or []:
            text = text + " " + str(part.get("text", ""))
        emit({"id": mid, "result": {}})
        if mode() == "die":
            sys.stderr.write("токен кончился\n")
            sys.exit(3)
        if mode() in ("mute", "deaf"):
            continue
        note("item/completed", {"item": {"type": "commandExecution",
            "command": "ls -la", "aggregatedOutput": "итог команды", "exitCode": 0}})
        note("item/completed", {"item": {"type": "agentMessage",
            "text": "ответ на реплику:" + text}})
        note("turn/completed", {"turn": {"status": "completed"}})
    elif m == "turn/interrupt":
        log("interrupt")
        if mode() == "deaf":
            continue
        emit({"id": mid, "result": {}})
        note("turn/completed", {"turn": {"status": "interrupted"}})
`

// appEnv поднимает стенд с подменным клиентом чужого протокола: agentctl
// отвечает раскладкой и разворачивает заказ exec в сам клиент.
func appEnv(t *testing.T) (*testEnv, *http.Client) {
	t.Helper()
	e := newTestEnv(t)
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 не в PATH: подменному серверу приложения нечем выполниться")
	}
	writeScript(t, e.bin, "agentctl", `case "$1" in
  exec) shift 4; exec "$@" ;;
  *) printf '%s' '`+appHarnessJSON+`' ;;
esac`)
	writeScript(t, e.bin, "клиент-app", `exec python3 "$(dirname "$0")/клиент-app.py" "$@"`)
	if err := os.WriteFile(filepath.Join(e.bin, "клиент-app.py"), []byte(appClientPy), 0o644); err != nil {
		t.Fatal(err)
	}
	return e, e.loggedClient(t)
}

// appStartChat поднимает разговор чужого протокола первой репликой и возвращает
// его адрес.
func appStartChat(t *testing.T, e *testEnv, c *http.Client, text string) string {
	t.Helper()
	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats",
		`{"text": "`+text+`", "model": "модель-codex", "harness": "codextest"}`)
	got := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("подъём чата чужого протокола: %d %s", resp.StatusCode, got)
	}
	var r struct {
		Way     string `json:"way"`
		Session string `json:"session"`
		Model   string `json:"model"`
	}
	if err := json.Unmarshal([]byte(got), &r); err != nil {
		t.Fatalf("ответ подъёма не разобрался: %v", err)
	}
	if r.Way != "appserver" || !strings.HasPrefix(r.Session, "as") || r.Model != "модель-codex" {
		t.Fatalf("подъём ответил не дорогой app-server: %+v", r)
	}
	return r.Session
}

// appWaitFeed ждёт запись ленты со словами: элементы хода приходят
// уведомлениями, и терпеливый опрос здесь честнее сна на глаз.
func appWaitFeed(t *testing.T, e *testEnv, sid, want string) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		for _, it := range e.s.appFeedRead(sid) {
			if strings.Contains(it.Text, want) {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("лента разговора %s не дождалась %q: %+v", sid, want, e.s.appFeedRead(sid))
}

// appWaitBusy ждёт названного состояния хода из ручки состояния.
func appWaitBusy(t *testing.T, e *testEnv, c *http.Client, sid string, busy bool) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		resp := doReq(t, c, "GET", e.srv.URL+"/api/projects/demo/chats/"+sid+"/status", "")
		var st struct {
			Live bool `json:"live"`
			Busy bool `json:"busy"`
		}
		if err := json.Unmarshal([]byte(body(t, resp)), &st); err == nil && st.Busy == busy {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("состояние разговора %s не дождалось busy=%v", sid, busy)
}

func appMode(t *testing.T, e *testEnv, mode string) {
	t.Helper()
	if mode == "" {
		os.Remove(filepath.Join(e.proj, "fake-app-mode"))
		return
	}
	if err := os.WriteFile(filepath.Join(e.proj, "fake-app-mode"), []byte(mode), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Подъём, ход и лента: реплика человека лежит в ленте со своим проводом, ход
// клиента оставил вызов команды и ответ, а запись разговора держит протокол,
// модель и нить клиента для продолжения.
func TestAppChatStartTurnFeed(t *testing.T) {
	e, c := appEnv(t)
	sid := appStartChat(t, e, c, "проверь список файлов")
	appWaitFeed(t, e, sid, "ответ на реплику")

	st := e.s.chatStoreRead(sid)
	if st.Protocol != "codex" || st.Model != "модель-codex" || st.Harness != "codextest" {
		t.Fatalf("запись разговора не держит протокол и пару модели: %+v", st)
	}
	if st.Thread != "th-1" {
		t.Fatalf("нить клиента не записана: %q", st.Thread)
	}
	var user, tool, answer bool
	for _, it := range e.s.appFeedRead(sid) {
		switch {
		case it.Role == "user" && strings.Contains(it.Text, "проверь список файлов"):
			user = it.Wire == it.Text
		case it.Role == "tool" && it.Note == "ls -la":
			tool = strings.Contains(it.Text, "итог команды")
		case it.Role == "assistant" && strings.Contains(it.Text, "ответ на реплику"):
			answer = true
		}
	}
	if !user || !tool || !answer {
		t.Fatalf("лента неполна: реплика %v, команда %v, ответ %v", user, tool, answer)
	}

	// Лента читается той же ручкой, что транскриптная: шапка называет протокол,
	// а записи приходят с ключами файла.
	resp := doReq(t, c, "GET", e.srv.URL+"/api/projects/demo/sessions/"+sid, "")
	got := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("лента разговора: %d %s", resp.StatusCode, got)
	}
	var feed struct {
		Head struct {
			Protocol string `json:"protocol"`
			Reply    string `json:"reply"`
		} `json:"head"`
		Items []reply `json:"items"`
		Total int     `json:"total"`
	}
	if err := json.Unmarshal([]byte(got), &feed); err != nil {
		t.Fatal(err)
	}
	if feed.Head.Protocol != "codex" || feed.Head.Reply != "session" || feed.Total < 3 {
		t.Fatalf("шапка ленты чужого протокола не собралась: %+v", feed)
	}
	if feed.Items[0].Key != "app:0" {
		t.Fatalf("ключ первой записи ленты не по файлу: %q", feed.Items[0].Key)
	}

	// Нить и модель подъёма уехали в клиент: заказ это проверяет.
	if log := readFile(t, filepath.Join(e.proj, "fake-app-log")); !strings.Contains(log, "start модель-codex") {
		t.Fatalf("клиент поднялся не выбранной моделью: %s", log)
	}
}

// Реплика в живой разговор, стоп прерыванием и продолжение резюмом нити: одна
// дорога на все состояния, и лента переживает снятие процесса.
func TestAppChatSayStopResume(t *testing.T) {
	e, c := appEnv(t)
	sid := appStartChat(t, e, c, "первый вопрос")
	appWaitFeed(t, e, sid, "ответ на реплику")
	appWaitBusy(t, e, c, sid, false)

	// Молчаливый ход: реплика принята, ход стоит занятым.
	appMode(t, e, "mute")
	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/say",
		`{"text": "второй вопрос", "msg_id": "m-2"}`)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body(t, resp), "appserver") {
		t.Fatalf("реплика живого разговора не ушла: %d %s", resp.StatusCode, body(t, resp))
	}
	appWaitBusy(t, e, c, sid, true)

	resp = doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/stop", "{}")
	if !strings.Contains(body(t, resp), "stopped") {
		t.Fatalf("ход не остановился прерыванием: %s", body(t, resp))
	}
	appWaitBusy(t, e, c, sid, false)

	// Ход после остановки идёт тем же процессом и той же нитью: остановка
	// оборвала ход, а не разговор, и второй подъём не заводится.
	appMode(t, e, "")
	resp = doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/say",
		`{"text": "третий вопрос", "msg_id": "m-3"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("реплика после остановки не ушла: %d %s", resp.StatusCode, body(t, resp))
	}
	appWaitFeed(t, e, sid, "ответ на реплику")
	log := readFile(t, filepath.Join(e.proj, "fake-app-log"))
	if strings.Count(log, "start ") != 1 || strings.Count(log, "turn") != 3 {
		t.Fatalf("разговор после остановки пошёл не той нитью: %s", log)
	}
}

// Занятый ход не принимает вторую реплику: она снимала бы очередь и путала
// человека, чей ответ какой.
func TestAppChatSayBusyConflict(t *testing.T) {
	e, c := appEnv(t)
	sid := appStartChat(t, e, c, "первый вопрос")
	appWaitFeed(t, e, sid, "ответ на реплику")
	appMode(t, e, "mute")
	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/say",
		`{"text": "второй вопрос", "msg_id": "m-2"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("реплика молчаливого хода не ушла: %d %s", resp.StatusCode, body(t, resp))
	}
	appWaitBusy(t, e, c, sid, true)
	resp = doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/say",
		`{"text": "третий вопрос", "msg_id": "m-3"}`)
	if resp.StatusCode != http.StatusConflict || !strings.Contains(body(t, resp), "ход уже идёт") {
		t.Fatalf("занятый ход принял реплику: %d %s", resp.StatusCode, body(t, resp))
	}
}

// Смерть процесса посреди хода не оставляет разговор в вечном ожидании: обрыв
// называется словами в ленте, состояние отдаёт мёртвый процесс, а следующая
// реплика поднимает разговор заново резюмом нити.
func TestAppChatDeathMidTurn(t *testing.T) {
	e, c := appEnv(t)
	appMode(t, e, "die")
	sid := appStartChat(t, e, c, "первый вопрос")
	appWaitFeed(t, e, sid, "ход прерван")
	appWaitBusy(t, e, c, sid, false)

	resp := doReq(t, c, "GET", e.srv.URL+"/api/projects/demo/chats/"+sid+"/status", "")
	if !strings.Contains(body(t, resp), `"live":false`) {
		t.Fatalf("мёртвый процесс остался живым на экран: %s", body(t, resp))
	}

	appMode(t, e, "")
	resp = doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/say",
		`{"text": "второй вопрос", "msg_id": "m-2"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("реплика после смерти не ушла: %d %s", resp.StatusCode, body(t, resp))
	}
	appWaitFeed(t, e, sid, "ответ на реплику")
	if log := readFile(t, filepath.Join(e.proj, "fake-app-log")); !strings.Contains(log, "resume th-1") {
		t.Fatalf("разговор не продолжился после смерти процесса: %s", log)
	}
}

// Клиент, не принявший прерывание, снимается процессом: ход кончается в любом
// исходе, и лента называет случившееся словами.
func TestAppChatStopKillsDeafClient(t *testing.T) {
	e, c := appEnv(t)
	appMode(t, e, "deaf")
	sid := appStartChat(t, e, c, "первый вопрос")
	appWaitBusy(t, e, c, sid, true)

	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/stop", "{}")
	got := body(t, resp)
	if !strings.Contains(got, "killed") {
		t.Fatalf("глухой к прерыванию клиент не снят: %s", got)
	}
	appWaitFeed(t, e, sid, "ход остановлен снятием процесса")
	appWaitBusy(t, e, c, sid, false)
}

// Смена модели снимает живой процесс и чистит нить: модель зашита в нить
// клиента, и новая заводится следующим ходом, а лента сохраняется.
func TestAppChatModelSwitch(t *testing.T) {
	e, c := appEnv(t)
	sid := appStartChat(t, e, c, "первый вопрос")
	appWaitFeed(t, e, sid, "ответ на реплику")

	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/model",
		`{"model": "модель-codex2", "harness": "codex2test"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("смена модели не записалась: %d %s", resp.StatusCode, body(t, resp))
	}
	st := e.s.chatStoreRead(sid)
	if st.Model != "модель-codex2" || st.Harness != "codex2test" || st.Thread != "" || st.Protocol != "codex" {
		t.Fatalf("смена модели не почистила запись разговора: %+v", st)
	}

	// Следующая реплика заводит свежую нить уже новой моделью.
	resp = doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/say",
		`{"text": "второй вопрос", "msg_id": "m-2"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("реплика после смены модели не ушла: %d %s", resp.StatusCode, body(t, resp))
	}
	appWaitFeed(t, e, sid, "ответ на реплику")
	if log := readFile(t, filepath.Join(e.proj, "fake-app-log")); !strings.Contains(log, "start модель-codex2") {
		t.Fatalf("свежая нить поднялась не новой моделью: %s", log)
	}
}

// Уборка в архив снимает процесс разговора, а строка списка приходит из своего
// каталога лент с протоколом и состоянием.
func TestAppChatListAndArchive(t *testing.T) {
	e, c := appEnv(t)
	sid := appStartChat(t, e, c, "первый вопрос")
	appWaitFeed(t, e, sid, "ответ на реплику")

	resp := doReq(t, c, "GET", e.srv.URL+"/api/projects/demo/chats?days=0", "")
	var list struct {
		Chats []chatEntry `json:"chats"`
	}
	if err := json.Unmarshal([]byte(body(t, resp)), &list); err != nil {
		t.Fatal(err)
	}
	var hit *chatEntry
	for i := range list.Chats {
		if list.Chats[i].ID == sid {
			hit = &list.Chats[i]
		}
	}
	if hit == nil {
		t.Fatalf("разговор чужого протокола не пришёл в список: %+v", list.Chats)
	}
	if hit.Protocol != "codex" || hit.State != chatLive || hit.First == "" {
		t.Fatalf("строка списка дороги app-server не собралась: %+v", hit)
	}

	arch := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/archive",
		`{"archived": true}`)
	if arch.StatusCode != http.StatusOK || !strings.Contains(body(t, arch), "процесс клиента снят") {
		t.Fatalf("уборка в архив не сняла процесс: %d %s", arch.StatusCode, body(t, arch))
	}
	if e.s.appLive(sid) != nil {
		t.Fatal("процесс разговора жив после уборки в архив")
	}
}

// Перевод уровня глубины: верх дашборда выше потолка клиента, и всё, что выше,
// идёт его потолком.
func TestAppEffort(t *testing.T) {
	for given, want := range map[string]string{
		"low": "low", "medium": "medium", "high": "high",
		"xhigh": "xhigh", "max": "xhigh", "": "xhigh", "чужое": "xhigh",
	} {
		if got := appEffort(given); got != want {
			t.Errorf("уровень %q перевёлся как %q, жду %q", given, got, want)
		}
	}
}

// Неизвестный вид элемента хода не выбрасывается молча: он ложится в ленту
// воротами инструментов с именем вида, а провал команды красит запись.
func TestAppItemReplies(t *testing.T) {
	fail := appItemReplies(map[string]any{"type": "commandExecution", "command": "make test",
		"aggregatedOutput": "ошибка сборки", "exitCode": float64(2)}, "2026-11-12T10:00:00Z")
	if len(fail) != 1 || !fail[0].Fail || fail[0].Note != "make test" {
		t.Fatalf("провал команды не покрашен: %+v", fail)
	}
	other := appItemReplies(map[string]any{"type": "fileChange", "name": "README.md",
		"output": "строка добавлена"}, "2026-11-12T10:00:00Z")
	if len(other) != 1 || other[0].Tool != "fileChange" || other[0].Role != "tool" {
		t.Fatalf("неизвестный вид потерялся: %+v", other)
	}
	if got := appItemReplies(map[string]any{"type": "agentMessage", "text": "  "}, ""); len(got) != 0 {
		t.Fatalf("пустой ответ не пропущен: %+v", got)
	}
}
