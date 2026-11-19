package main

// Стенд дороги app-server: подменной agentctl разворачивает заказ в подменной
// клиент чужого протокола, а тот говорит JSONL проверенного контракта
// (DK-1624). Живой клиент стендом не зовётся: раскладка имена не проверяет, и
// дорогое входное окружение тут ни при чём. Поведением клиента правит файл
// fake-app-mode в каталоге разговора, а его заказы пишет fake-app-log.

import (
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dronrider/devkit/internal/chat"
	"github.com/dronrider/devkit/internal/draftproposal"
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
import fcntl, json, os, sys, time

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

writer = None

def claim_thread(mid):
    global writer
    if not os.path.exists("fake-app-enforce-writer-lock") or writer is not None:
        return True
    writer = open("fake-app-writer-lock", "a")
    try:
        fcntl.flock(writer, fcntl.LOCK_EX | fcntl.LOCK_NB)
    except BlockingIOError:
        writer.close()
        writer = None
        emit({"id": mid, "error": {"code": -32600, "message": "thread already has an active writer"}})
        return False
    return True

def configure_tools(params):
    # Process DEVKIT variables are filtered by the client before tool launch.
    # Only explicit per-thread values reach the command environment.
    config = params.get("config") or {}
    prefix = "shell_environment_policy.set."
    env = {k[len(prefix):]: v for k, v in config.items() if k.startswith(prefix)}
    log("effective-env " + json.dumps(env))
    log("groom " + env.get("DEVKIT_GROOM_SOURCE", "") + " " + env.get("DEVKIT_GROOM_MODE", ""))
    log("session " + env.get("DEVKIT_CHAT_SESSION_ID", ""))

def configured(params, thread):
    result = {"thread": {"id": thread}, "approvalPolicy": params.get("approvalPolicy"), "approvalsReviewer": params.get("approvalsReviewer"), "sandbox": {"type": "workspaceWrite"}}
    field = mode().removeprefix("missing-")
    if mode().startswith("missing-"):
        result.pop(field, None)
    if mode() == "ignored-reviewer": result["approvalsReviewer"] = "user"
    if mode() == "ignored-policy": result["approvalPolicy"] = "never"
    if mode() == "ignored-sandbox": result["sandbox"] = {"type": "dangerFullAccess"}
    return result

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
        if not claim_thread(mid): continue
        if mode() == "startup-barrier":
            with open(os.path.join(os.getcwd(), "fake-app-handshake"), "w") as f: f.write(str(os.getpid()))
            while not os.path.exists(os.path.join(os.getcwd(), "fake-app-release")): time.sleep(0.01)
        if mode() == "unsupported-reviewer" and params.get("approvalsReviewer"):
            emit({"id": mid, "error": {"message": "approvalsReviewer не поддерживается"}})
            continue
        configure_tools(params)
        log("start " + str(params.get("model", "")))
        log("policy " + json.dumps(params))
        emit({"id": mid, "result": configured(params, "th-1")})
    elif m == "thread/resume":
        if mode() == "resume-barrier":
            with open(os.path.join(os.getcwd(), "fake-app-handshake"), "w") as f: f.write(str(os.getpid()))
            while not os.path.exists(os.path.join(os.getcwd(), "fake-app-release")): time.sleep(0.01)
        if mode() == "resume-error":
            emit({"id": mid, "error": {"message": "rollout is unreadable"}})
            continue
        if not claim_thread(mid): continue
        if mode() == "unsupported-reviewer" and params.get("approvalsReviewer"):
            emit({"id": mid, "error": {"message": "approvalsReviewer не поддерживается"}})
            continue
        configure_tools(params)
        log("resume " + str(params.get("threadId", "")))
        log("policy " + json.dumps(params))
        emit({"id": mid, "result": configured(params, params.get("threadId"))})
    elif m == "turn/start":
        log("turn " + str(params.get("effort", "")))
        log("input " + json.dumps(params.get("input")))
        text = ""
        for part in params.get("input") or []:
            text = text + " " + str(part.get("text", ""))
        if mode() == "early":
            note("item/completed", {"item": {"type": "agentMessage",
                "text": "ответ на реплику:" + text}})
            note("turn/completed", {"threadId": "th-1", "turn": {"id": "turn-1", "status": "completed"}})
            emit({"id": mid, "result": {"turn": {"id": "turn-1"}}})
            continue
        emit({"id": mid, "result": {"turn": {"id": "turn-1"}}})
        if mode() == "approval-denied":
            note("item/completed", {"item": {"type": "commandExecution", "command": "agentctl plan", "aggregatedOutput": "automatic approval review rejected: service access denied", "exitCode": 1}})
            note("turn/completed", {"threadId": "th-1", "turn": {"id": "turn-1", "status": "completed"}})
            continue
        if mode() == "task-child":
            note("turn/completed", {"threadId": "child-thread", "turn": {"id": "turn-1", "status": "completed"}})
            note("turn/completed", {"threadId": "th-1", "turn": {"id": "child-turn", "status": "completed"}})
            continue
        if mode() == "reply-open":
            with open(os.path.join(os.getcwd(), "task-state"), "w") as f: f.write("in-progress")
        if mode() == "task-check":
            with open(os.path.join(os.getcwd(), "task-state"), "w") as f: f.write("check")
        if mode() == "task-done":
            with open(os.path.join(os.getcwd(), "task-state"), "w") as f: f.write("архиве")
        if mode() == "die":
            sys.stderr.write("токен кончился\n")
            sys.exit(3)
        if mode() in ("mute", "deaf"):
            continue
        note("item/completed", {"item": {"type": "commandExecution",
            "command": "ls -la", "aggregatedOutput": "итог команды", "exitCode": 0}})
        note("item/completed", {"item": {"type": "agentMessage",
            "text": "ответ на реплику:" + text}})
        note("turn/completed", {"threadId": "th-1", "turn": {"id": "turn-1", "status": "completed"}})
    elif m == "test/finish":
        if not params.get("keepOpen"):
            with open(os.path.join(os.getcwd(), "task-state"), "w") as f: f.write("архиве")
        note("turn/completed", {"threadId": "th-1", "turn": {"id": "turn-1", "status": "completed"}})
        emit({"id": mid, "result": {}})
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
	t.Cleanup(func() {
		e.srv.Close()
		e.s.appTasksClose()
		e.s.appMu.Lock()
		chats := maps.Clone(e.s.appChats)
		e.s.appMu.Unlock()
		for id, chat := range chats {
			if chat.proc != nil {
				e.s.appDrop(id, "конец теста")
			}
		}
		e.s.appWorkers.Wait()
	})
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

func TestAppChatCatalogModelsReachClient(t *testing.T) {
	for _, model := range []string{"gpt-6.2-sol", "gpt-6.2-astra"} {
		t.Run(model, func(t *testing.T) {
			e, c := appEnv(t)
			catalog := strings.Replace(appHarnessJSON, `"protocol": "codex", "models":`,
				`"chat": ["gpt-6.2-sol", "gpt-6.2-astra", "gpt-6.1-sol"], "protocol": "codex", "models":`, 1)
			writeScript(t, e.bin, "agentctl", `case "$1" in
  exec) shift 4; exec "$@" ;;
  *) printf '%s' '`+catalog+`' ;;
esac`)
			resp := doReq(t, c, "GET", e.srv.URL+"/api/projects/demo/chats", "")
			var listed struct {
				Models []chatModelOpt `json:"models"`
			}
			if err := json.Unmarshal([]byte(body(t, resp)), &listed); err != nil {
				t.Fatal(err)
			}
			found := false
			for _, option := range listed.Models {
				if option.Model == "gpt-6.1-sol" {
					t.Fatalf("прежняя модель осталась в выборе: %+v", listed.Models)
				}
				if option.Model == model && option.Harness == "codextest" {
					found = true
					if option.Default || option.Tier != "" {
						t.Fatalf("модель каталога стала ярусной: %+v", option)
					}
				}
			}
			if !found {
				t.Fatalf("модель %s отсутствует в выборе: %+v", model, listed.Models)
			}
			resp = doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats", fmt.Sprintf(`{"text":"проверка каталога", "model":%q, "harness":"codextest"}`, model))
			var started struct {
				Session string `json:"session"`
			}
			got := body(t, resp)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("запуск %s: %d %s", model, resp.StatusCode, got)
			}
			if err := json.Unmarshal([]byte(got), &started); err != nil {
				t.Fatal(err)
			}
			appWaitFeed(t, e, started.Session, "ответ на реплику")
			if st := e.s.chatStoreRead(started.Session); st.Model != model || st.Harness != "codextest" {
				t.Fatalf("выбор не сохранился: %+v", st)
			}
			if log := readFile(t, filepath.Join(e.proj, "fake-app-log")); !strings.Contains(log, "start "+model+"\n") {
				t.Fatalf("точный ID не передан клиенту: %s", log)
			}
		})
	}
}

// Конец хода может прийти вплотную к ответу на turn/start и даже раньше него:
// поток не обещает порядок между ответами и уведомлениями, и занятость,
// открытая после ответа, опаздывает за собственным погашением и висит «ход
// идёт» вечно. Режим early и ловит этот порядок строк.
func TestAppChatTurnEndBeforeReply(t *testing.T) {
	e, c := appEnv(t)
	appMode(t, e, "early")
	sid := appStartChat(t, e, c, "быстрый ход")
	appWaitFeed(t, e, sid, "ответ на реплику")
	appWaitBusy(t, e, c, sid, false)
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
	if got := body(t, resp); resp.StatusCode != http.StatusOK {
		t.Fatalf("реплика после остановки не ушла: %d %s", resp.StatusCode, got)
	}
	appWaitFeed(t, e, sid, "ответ на реплику: третий вопрос")
	appWaitBusy(t, e, c, sid, false)
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

// Ручной уровень глубины переживает смерть процесса: выбор ложится в запись
// разговора каждым изменением, и следующий подъём берёт его из записи, а не
// возвращается к умолчанию.
func TestAppChatEffortSurvivesDeath(t *testing.T) {
	e, c := appEnv(t)
	sid := appStartChat(t, e, c, "первый вопрос")
	appWaitFeed(t, e, sid, "ответ на реплику")

	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/say",
		`{"text": "второй вопрос", "msg_id": "m-2", "effort": "high"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("реплика с ручным уровнем не ушла: %d %s", resp.StatusCode, body(t, resp))
	}
	appWaitFeed(t, e, sid, "ответ на реплику")
	if log := readFile(t, filepath.Join(e.proj, "fake-app-log")); !strings.Contains(log, "turn high") {
		t.Fatalf("ручной уровень не доехал до клиента: %s", log)
	}

	// Смерть процесса и следующий подъём: уровень берётся из записи разговора.
	e.s.appDrop(sid, "тест смерти")
	resp = doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/say",
		`{"text": "третий вопрос", "msg_id": "m-3"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("реплика после смерти не ушла: %d %s", resp.StatusCode, body(t, resp))
	}
	appWaitFeed(t, e, sid, "ответ на реплику")
	log := readFile(t, filepath.Join(e.proj, "fake-app-log"))
	if strings.Count(log, "turn high") != 2 {
		t.Fatalf("уровень глубины не пережил смерть процесса: %s", log)
	}
	if st := e.s.chatStoreRead(sid); st.CxEffort != "high" {
		t.Fatalf("запись разговора не держит уровень глубины: %+v", st)
	}
}

// Поток ленты чужого протокола нумерует записи по строкам файла: дописанная
// после открытия потока запись приезжает один раз, следующим номером файла, и
// не дублирует уже отправленные ключи. Записи и смещение хвоста берутся из
// одного снимка файла, иначе запись между снимками уезжала в панель дважды.
func TestAppFeedStreamKeysFollowFile(t *testing.T) {
	e, c := appEnv(t)
	sid := appStartChat(t, e, c, "первый вопрос")
	appWaitFeed(t, e, sid, "ответ на реплику")

	resp := doReq(t, c, "GET", e.srv.URL+"/api/projects/demo/sessions/"+sid+"?stream=1", "")
	defer resp.Body.Close()
	if err := e.s.appFeedAppend(sid, []reply{{Role: "user", Text: "дописанная реплика"}}); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 8192)
	seen := ""
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) && !strings.Contains(seen, "дописанная реплика") {
		n, err := resp.Body.Read(buf)
		seen += string(buf[:n])
		if err != nil {
			break
		}
	}
	if !strings.Contains(seen, "дописанная реплика") {
		t.Fatalf("поток не дочитал дописанную запись: %q", seen)
	}
	items := e.s.appFeedRead(sid)
	last := items[len(items)-1]
	if last.Text != "дописанная реплика" {
		t.Fatalf("дописанной записи нет в ленте: %+v", items)
	}
	if got := strings.Count(seen, `"key":"`+last.Key+`"`); got != 1 {
		t.Fatalf("дописанная запись уехала в поток %d раз под ключом %s: %q", got, last.Key, seen)
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

func TestAppChatTitleFromFirstMessage(t *testing.T) {
	e, c := appEnv(t)
	sid := appStartChat(t, e, c, "Привет, проверь список файлов. Потом ответь")
	appWaitFeed(t, e, sid, "ответ на реплику")
	for i := 0; i < 2; i++ {
		resp := doReq(t, c, "GET", e.srv.URL+"/api/projects/demo/chats?days=0", "")
		var list struct {
			Chats []chatEntry `json:"chats"`
		}
		if err := json.Unmarshal([]byte(body(t, resp)), &list); err != nil {
			t.Fatal(err)
		}
		found := false
		for _, row := range list.Chats {
			if row.ID == sid {
				found = true
				if row.Title != "проверь список файлов" {
					t.Fatalf("название: %q", row.Title)
				}
			}
		}
		if !found {
			t.Fatalf("разговор %s не найден", sid)
		}
		feed := doReq(t, c, "GET", e.srv.URL+"/api/projects/demo/sessions/"+sid, "")
		var got struct {
			Head struct {
				First string `json:"first"`
			} `json:"head"`
		}
		if err := json.Unmarshal([]byte(body(t, feed)), &got); err != nil {
			t.Fatal(err)
		}
		if got.Head.First != "Привет, проверь список файлов. Потом ответь" {
			t.Fatalf("первая реплика: %q", got.Head.First)
		}
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

func TestAppChatApprovalPolicy(t *testing.T) {
	for _, grooming := range []bool{false, true} {
		t.Run(fmt.Sprint(grooming), func(t *testing.T) {
			e, c := appEnv(t)
			sid := ""
			if grooming {
				appDraftFixture(t, e, "XR-77")
				r := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/drafts/XR-77/groom", `{"harness":"codextest"}`)
				var got struct{ Session string }
				raw := body(t, r)
				if r.StatusCode != 200 || json.Unmarshal([]byte(raw), &got) != nil {
					t.Fatalf("groom %d %s", r.StatusCode, raw)
				}
				sid = got.Session
			} else {
				sid = appStartChat(t, e, c, "первый ход")
			}
			appWaitBusy(t, e, c, sid, false)
			e.s.appDrop(sid, "возобновление")
			r := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/say", `{"text":"продолжить"}`)
			if raw := body(t, r); r.StatusCode != 200 {
				t.Fatalf("resume %d %s", r.StatusCode, raw)
			}
			defer e.s.appDrop(sid, "конец теста")
			policies := 0
			for _, line := range strings.Split(readFile(t, filepath.Join(e.proj, "fake-app-log")), "\n") {
				if !strings.HasPrefix(line, "policy ") {
					continue
				}
				var p map[string]any
				if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "policy ")), &p); err != nil {
					t.Fatal(err)
				}
				config, ok := p["config"].(map[string]any)
				count := 2
				if grooming {
					count = 5
				}
				if !ok || len(config) != count {
					t.Fatalf("не узкая конфигурация окружения: %v", p)
				}
				toolEnv := map[string]any{}
				for key, value := range config {
					if !strings.HasPrefix(key, "shell_environment_policy.set.") {
						t.Fatalf("лишняя настройка окружения: %s", key)
					}
					toolEnv[strings.TrimPrefix(key, "shell_environment_policy.set.")] = value
				}
				if len(toolEnv) != count || toolEnv["DEVKIT_CHAT_SESSION_ID"] != sid || toolEnv["DEVKIT_HARNESS"] != "codextest" {
					t.Fatalf("tool env: %v", config)
				}
				if grooming && (toolEnv["DEVKIT_TASK"] != "XR-77" || toolEnv["DEVKIT_GROOM_SOURCE"] != "XR-77" || toolEnv["DEVKIT_GROOM_MODE"] != "groom") {
					t.Fatalf("groom tool env: %v", toolEnv)
				}
				if p["approvalPolicy"] != "on-request" || p["approvalsReviewer"] != "auto_review" || p["sandbox"] != "workspace-write" {
					t.Fatalf("policy: %v", p)
				}
				policies++
			}
			if policies != 2 {
				t.Fatalf("нет старта и продолжения: %d", policies)
			}
		})
	}
}

func TestAppChatPolicyFailClosed(t *testing.T) {
	for _, thread := range []string{"", "legacy-never"} {
		for _, mode := range []string{"unsupported-reviewer", "ignored-reviewer", "ignored-policy", "ignored-sandbox", "missing-approvalPolicy", "missing-approvalsReviewer", "missing-sandbox"} {
			t.Run(thread+"/"+mode, func(t *testing.T) {
				e, _ := appEnv(t)
				appMode(t, e, mode)
				sid := "aspolicy-refusal"
				stored := chatStore{Project: "demo", Model: "модель-codex", Harness: "codextest", Thread: thread, Protocol: "codex"}
				if err := e.s.chatStoreWrite(sid, stored); err != nil {
					t.Fatal(err)
				}
				before := readFile(t, filepath.Join(chatStoreDir(e.home), sid+".json"))
				c, err := e.s.appSpawn(sid, e.s.chatHarnessOf(stored.Model, stored.Harness), stored.Model, e.proj, "demo", thread)
				if err == nil || c != nil || e.s.appLive(sid) != nil {
					t.Fatalf("неподтверждённая политика принята: %+v %v", c, err)
				}
				want := "клиент не подтвердил workspace-write"
				if mode == "unsupported-reviewer" {
					want = "approvalsReviewer не поддерживается"
				}
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("причина потеряна: %v", err)
				}
				log := readFile(t, filepath.Join(e.proj, "fake-app-log"))
				if strings.Contains(log, "turn ") || strings.Contains(log, "start ") && thread != "" {
					t.Fatalf("ход или новая нить после отказа: %s", log)
				}
				if readFile(t, filepath.Join(chatStoreDir(e.home), sid+".json")) != before {
					t.Fatal("сохранённый разговор изменился при отказе")
				}
			})
		}
	}
}

func TestAppChatPolicyIndependentOfBinding(t *testing.T) {
	for _, binding := range []string{"", "XR-002", "XR-100"} {
		for _, thread := range []string{"", "legacy-never"} {
			t.Run(binding+"/"+thread, func(t *testing.T) {
				e, _ := appEnv(t)
				sid := "aspolicy-binding"
				st := chatStore{Project: "demo", Task: binding, Model: "модель-codex", Harness: "codextest", Thread: thread, Protocol: "codex"}
				if err := e.s.chatStoreWrite(sid, st); err != nil {
					t.Fatal(err)
				}
				c, err := e.s.appSpawn(sid, e.s.chatHarnessOf(st.Model, st.Harness), st.Model, e.proj, "demo", thread)
				if err != nil {
					t.Fatal(err)
				}
				defer e.s.appDrop(sid, "конец теста")
				log := readFile(t, filepath.Join(e.proj, "fake-app-log"))
				for _, want := range []string{`"approvalPolicy": "on-request"`, `"approvalsReviewer": "auto_review"`, `"sandbox": "workspace-write"`} {
					if !strings.Contains(log, want) {
						t.Fatalf("политика зависит от привязки: %s", log)
					}
				}
				if thread != "" && c.thread != thread {
					t.Fatalf("нить изменена: %s", c.thread)
				}
				if c.automatic || strings.Contains(log, "DEVKIT_TASK_LOCK_FROM") {
					t.Fatalf("выданы права владельца: %s", log)
				}
			})
		}
	}
}

func TestAppGroomPreparedAgreement(t *testing.T) {
	for _, form := range []bool{false, true} {
		t.Run(fmt.Sprint(form), func(t *testing.T) {
			e, c := appEnv(t)
			appDraftFixture(t, e, "XR-77")
			r := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/drafts/XR-77/groom", `{"harness":"codextest","mode":"run","effort":"high"}`)
			var got struct{ Session string }
			raw := body(t, r)
			if r.StatusCode != 200 || json.Unmarshal([]byte(raw), &got) != nil {
				t.Fatalf("groom: %d %s", r.StatusCode, raw)
			}
			sid := got.Session
			defer e.s.appDrop(sid, "конец теста")
			appWaitBusy(t, e, c, sid, false)
			if !strings.Contains(readFile(t, filepath.Join(e.proj, "fake-app-log")), "session "+sid) {
				t.Fatal("ID панели не передан клиенту")
			}
			var effective map[string]string
			for _, line := range strings.Split(readFile(t, filepath.Join(e.proj, "fake-app-log")), "\n") {
				if strings.HasPrefix(line, "effective-env ") {
					if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "effective-env ")), &effective); err != nil {
						t.Fatal(err)
					}
				}
			}
			if len(effective) != 5 || effective["DEVKIT_CHAT_SESSION_ID"] != sid || effective["DEVKIT_HARNESS"] != "codextest" || effective["DEVKIT_TASK"] != "XR-77" || effective["DEVKIT_GROOM_SOURCE"] != "XR-77" || effective["DEVKIT_GROOM_MODE"] != "run" {
				t.Fatalf("effective command env: %v", effective)
			}
			bin := buildTaskctl(t)
			old := taskctlBin
			taskctlBin = bin
			t.Cleanup(func() { taskctlBin = old })
			kit, _ := filepath.Abs("../..")
			t.Setenv("DEVKIT_HOME", kit)
			putDoc(t, e.proj, "docs/TASKS-archive.md", "# Архив\n\n| ID | Задача | Тип | P | Закрыто | Ссылка |\n|---|---|---|---|---|---|\n")
			cli := func(input string, args ...string) string {
				t.Helper()
				cmd := exec.Command(bin, append([]string{"-C", e.proj}, args...)...)
				cmd.Env = standSpawnEnv("DEVKIT_CHAT_SESSION_ID="+effective["DEVKIT_CHAT_SESSION_ID"], "DEVKIT_TASK="+effective["DEVKIT_TASK"], "DEVKIT_HARNESS="+effective["DEVKIT_HARNESS"], "DEVKIT_GROOM_SOURCE="+effective["DEVKIT_GROOM_SOURCE"], "DEVKIT_GROOM_MODE="+effective["DEVKIT_GROOM_MODE"], "CLAUDE_CODE_SESSION_ID=", "DEVKIT_TMUX=", "DEVKIT_HOME="+kit, "HOME="+e.home)
				cmd.Stdin = strings.NewReader(input)
				out, err := cmd.CombinedOutput()
				if err != nil {
					t.Fatalf("CLI %v: %s %v", args, out, err)
				}
				return string(out)
			}
			var view struct{ Snapshot string }
			if err := json.Unmarshal([]byte(cli("", "draft", "prepared", "XR-77")), &view); err != nil {
				t.Fatal(err)
			}
			material := "## Что происходит\n\nПроверка записи.\n\n## Чего хотим\n\nЗапись проверяется.\n\n## DoD\n\n- Видно результат.\n\n## Ранг\n\n`0+1+1+0+1 = 3`, P3.\n\n- Серьёзность 0: работа продолжается.\n- Ценность 1: один сценарий.\n- Неопределённость 1: место известно.\n- Поправка на баг 0: новая работа.\n- Рычаг 1: убирает повтор.\n"
			payload, _ := json.Marshal(map[string]any{"complete": true, "conclusion": "Создать задачу", "proposals": []draftproposal.Proposal{{ID: "one", Revision: 1, Outcome: "add", Title: "Проверка продолжения", Text: material, BoardType: "task", Rank: "0+1+1+0+1", Size: "S", Accept: "agent"}}})
			cli(string(payload), "draft", "prepared", "save", "XR-77", "--base", view.Snapshot)
			var q chat.Question
			if err := json.Unmarshal([]byte(cli("", "draft", "prepared", "ask", "XR-77")), &q); err != nil || q.Prepared == nil {
				t.Fatalf("question %+v %v", q, err)
			}
			ask, ok := chat.ReadAsk(chat.AskPath(e.proj, chat.TaskName("XR-77")))
			if !ok || ask.Session != sid {
				t.Fatalf("identity: %+v", ask)
			}
			r = doReq(t, c, "GET", e.srv.URL+"/api/projects/demo/chats/"+sid+"/ask", "")
			raw = body(t, r)
			if r.StatusCode != 200 || !strings.Contains(raw, `"prepared"`) {
				t.Fatalf("вопрос не показан: %d %s", r.StatusCode, raw)
			}
			source := q.Prepared.Sources[0]
			req := draftproposal.ConfirmRequest{Snapshots: []draftproposal.Snapshot{{Source: source.Source, Digest: source.Basis, Revision: source.Revision}}, Decisions: []draftproposal.Decision{{Source: source.Source, Proposal: source.Proposals[0].ID, Revision: source.Proposals[0].Revision, Action: "accept"}}}
			data, _ := json.Marshal(map[string]any{"text": "принять", "proposal": proposalAnswer{Mode: "apply", Anchor: "XR-77", Tree: e.proj, Snapshots: req.Snapshots, Decisions: req.Decisions}})
			url := e.srv.URL + "/api/projects/demo/chats/" + sid + "/say"
			if form {
				data, _ = json.Marshal(req)
				url = e.srv.URL + "/api/projects/demo/drafts/prepared/confirm"
			}
			appMode(t, e, "mute")
			comment, _ := json.Marshal(map[string]any{"text": "пояснить перед согласием", "proposal": proposalAnswer{Mode: "comment", Anchor: "XR-77", Tree: e.proj, Snapshots: req.Snapshots}})
			r = doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/say", string(comment))
			if out := body(t, r); r.StatusCode != 200 {
				t.Fatalf("comment: %d %s", r.StatusCode, out)
			}
			appWaitFeed(t, e, sid, "пояснить перед согласием")
			if _, ok := chat.ReadAsk(chat.AskPath(e.proj, chat.TaskName("XR-77"))); !ok {
				t.Fatal("комментарий снял вопрос")
			}
			r = doReq(t, c, "POST", url, string(data))
			if out := body(t, r); r.StatusCode != 502 || !strings.Contains(out, "ход уже идёт") {
				t.Fatalf("нет отказа доставки: %d %s", r.StatusCode, out)
			}
			_, pending := chat.ReadAsk(chat.AskPath(e.proj, chat.TaskName("XR-77")))
			if form && !pending {
				t.Fatal("ошибка доставки с формы сняла вопрос")
			}
			if !form && len(e.s.proposalContexts(sid)) == 0 {
				t.Fatal("нет сохранённой квитанции для повтора из чата")
			}
			e.s.appDrop(sid, "согласие после перезапуска")
			appMode(t, e, "")
			r = doReq(t, c, "POST", url, string(data))
			raw = body(t, r)
			if r.StatusCode != 200 {
				t.Fatalf("agreement: %d %s", r.StatusCode, raw)
			}
			appWaitFeed(t, e, sid, "ответ на реплику: Ответ панели обработан taskctl")
			state, err := draftproposal.ParsePrepared([]byte(readFile(t, draftproposal.PreparedPath(e.proj, "XR-77"))))
			if err != nil || len(state.Proposals) != 1 || state.Proposals[0].Result == nil || len(state.Proposals[0].Result.IDs) != 1 {
				t.Fatalf("нет ID созданной задачи: %+v %v", state, err)
			}
			created := state.Proposals[0].Result.IDs[0]
			appWaitFeed(t, e, sid, created)
			if _, err := os.Stat(filepath.Join(e.proj, "docs", "tasks", created+".md")); err != nil {
				t.Fatal(err)
			}
			if _, ok := chat.ReadAsk(chat.AskPath(e.proj, chat.TaskName("XR-77"))); ok {
				t.Fatal("вопрос остался после согласия")
			}
			if lines := chat.ReadLines(chat.Path(e.proj, chat.TaskName("XR-77"))); len(lines) != 0 {
				t.Fatalf("согласие ушло в терминальный вход: %v", lines)
			}
			st := e.s.chatStoreRead(sid)
			if st.GroomMode != "run" || st.GroomSource != "XR-77" || st.GroomTree != e.proj || st.Model != "модель-codex" || st.Harness != "codextest" || st.CxEffort != "high" || st.Thread != "th-1" {
				t.Fatalf("run continuation: %+v", st)
			}
		})
	}
}

func TestAppGroomUnsupportedReviewer(t *testing.T) {
	for _, mode := range []string{"unsupported-reviewer", "ignored-reviewer"} {
		t.Run(mode, func(t *testing.T) {
			e, c := appEnv(t)
			appDraftFixture(t, e, "XR-77")
			appMode(t, e, mode)
			r := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/drafts/XR-77/groom", `{"harness":"codextest"}`)
			raw := body(t, r)
			why := "approvalsReviewer не поддерживается"
			if mode == "ignored-reviewer" {
				why = "клиент не подтвердил workspace-write"
			}
			if r.StatusCode != 502 || !strings.Contains(raw, why) {
				t.Fatalf("нет конкретного отказа: %d %s", r.StatusCode, raw)
			}
			rows := e.s.appListRows("demo")
			if len(rows) != 1 {
				t.Fatalf("отказ не виден: %+v", rows)
			}
			appWaitFeed(t, e, rows[0].ID, why)
			if log := readFile(t, filepath.Join(e.proj, "fake-app-log")); strings.Contains(log, "turn ") {
				t.Fatalf("клиент начал ход с другой политикой: %s", log)
			}
		})
	}
}

func TestAppGroomStoreFailure(t *testing.T) {
	e, c := appEnv(t)
	appDraftFixture(t, e, "XR-77")
	dir := chatStoreDir(e.home)
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir, []byte("нет каталога чатов"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/drafts/XR-77/groom", `{"harness":"codextest","mode":"run"}`)
	raw := body(t, r)
	if r.StatusCode != 502 || !strings.Contains(raw, "контекст груминга не записался") {
		t.Fatalf("контекст потерян при успешном старте: %d %s", r.StatusCode, raw)
	}
	if _, err := os.Stat(filepath.Join(e.proj, "fake-app-log")); !os.IsNotExist(err) {
		t.Fatalf("клиент поднят после потери контекста: %v", err)
	}
}

func TestAppChatExternalWriterConflict(t *testing.T) {
	e, client := appEnv(t)
	if err := os.WriteFile(filepath.Join(e.proj, "fake-app-enforce-writer-lock"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	sid := appStartChat(t, e, client, "первый вопрос")
	appWaitBusy(t, e, client, sid, false)
	e.s.appDrop(sid, "перезапуск дашборда")
	before := e.s.chatStoreRead(sid)
	feed := readFile(t, e.s.appFeedFile(sid))
	h := e.s.chatHarnessOf(before.Model, before.Harness)
	owner, err := e.s.appServerStart(h, e.proj, binPath(agentctlBin), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(owner.kill)
	if _, err = owner.call("initialize", map[string]any{}); err != nil {
		t.Fatal(err)
	}
	if _, err = owner.call("thread/resume", map[string]any{"threadId": before.Thread}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		r := doReq(t, client, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/say", `{"text":"продолжить","msg_id":"retry-writer"}`)
		raw := body(t, r)
		var got map[string]string
		if r.StatusCode != http.StatusConflict || json.Unmarshal([]byte(raw), &got) != nil || got["refused"] == "" {
			t.Fatalf("конфликт writer: %d %s", r.StatusCode, raw)
		}
		for _, want := range []string{before.Thread, "другом клиенте Codex", "Закройте", "повторите отправку", "История и выбранная модель сохранены"} {
			if !strings.Contains(got["refused"], want) {
				t.Errorf("нет следующего шага %q: %s", want, raw)
			}
		}
		if e.s.appLive(sid) != nil || owner.gone() {
			t.Fatal("конфликт оставил новый процесс или снял внешнего владельца")
		}
		if got := e.s.chatStoreRead(sid); got.Thread != before.Thread || got.Model != before.Model || got.Harness != before.Harness || got.CxEffort != before.CxEffort || got.Protocol != before.Protocol {
			t.Fatalf("конфликт изменил настройки: %+v", got)
		}
		if got := readFile(t, e.s.appFeedFile(sid)); got != feed {
			t.Fatal("непринятая реплика изменила историю")
		}
		if _, err = owner.call("initialize", map[string]any{}); err != nil {
			t.Fatalf("внешний владелец повреждён: %v", err)
		}
	}
	if log := readFile(t, filepath.Join(e.proj, "fake-app-log")); strings.Count(log, "turn ") != 1 || strings.Count(log, "start ") != 1 {
		t.Fatalf("конфликт отправил ход или создал новую нить: %s", log)
	}
	owner.kill()
	r := doReq(t, client, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/say", `{"text":"продолжить","msg_id":"retry-writer"}`)
	if raw := body(t, r); r.StatusCode != http.StatusOK {
		t.Fatalf("после штатного выхода владельца: %d %s", r.StatusCode, raw)
	}
	appWaitBusy(t, e, client, sid, false)
	r = doReq(t, client, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/say", `{"text":"продолжить","msg_id":"retry-writer"}`)
	if raw := body(t, r); r.StatusCode != http.StatusOK || !strings.Contains(raw, `"way":"dup"`) {
		t.Fatalf("повтор после доставки: %d %s", r.StatusCode, raw)
	}
	log := readFile(t, filepath.Join(e.proj, "fake-app-log"))
	if strings.Count(log, "turn ") != 2 || strings.Count(log, "start ") != 1 || strings.Count(log, "resume th-1") != 2 {
		t.Fatalf("возобновление сменило нить или повторило ход: %s", log)
	}
	if got := readFile(t, e.s.appFeedFile(sid)); !strings.HasPrefix(got, feed) || strings.Count(got, `"role":"user"`) != 2 {
		t.Fatalf("история после продолжения: %s", got)
	}
}

func TestAppChatResumeOtherError(t *testing.T) {
	e, client := appEnv(t)
	sid := appStartChat(t, e, client, "первый вопрос")
	appWaitBusy(t, e, client, sid, false)
	e.s.appDrop(sid, "перезапуск")
	appMode(t, e, "resume-error")
	r := doReq(t, client, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/say", `{"text":"продолжить"}`)
	if raw := body(t, r); r.StatusCode != http.StatusBadGateway || !strings.Contains(raw, "rollout is unreadable") || strings.Contains(raw, `"refused"`) {
		t.Fatalf("иная ошибка resume превратилась в writer: %d %s", r.StatusCode, raw)
	}
}

func TestAppChatExternalWriterBrowser(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal("node обязателен для проверки отказа отправки")
	}
	out, err := exec.Command(node, "testdata/appchat_conflict.mjs", "static/app.js").CombinedOutput()
	if err != nil {
		t.Fatalf("отказ writer в панели: %v\n%s", err, out)
	}
}

func TestAppChatStopContinuesSameThread(t *testing.T) {
	for _, mode := range []string{"mute", "deaf"} {
		t.Run(mode, func(t *testing.T) {
			e, client := appEnv(t)
			if err := os.WriteFile(filepath.Join(e.proj, "fake-app-enforce-writer-lock"), nil, 0600); err != nil {
				t.Fatal(err)
			}
			appMode(t, e, mode)
			sid := appStartChat(t, e, client, "первый вопрос")
			before := e.s.chatStoreRead(sid)
			r := doReq(t, client, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/stop", "{}")
			if raw := body(t, r); r.StatusCode != http.StatusOK {
				t.Fatalf("стоп: %d %s", r.StatusCode, raw)
			}
			appWaitBusy(t, e, client, sid, false)
			appMode(t, e, "")
			r = doReq(t, client, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/say", `{"text":"продолжить после стопа","msg_id":"after-stop"}`)
			if raw := body(t, r); r.StatusCode != http.StatusOK {
				t.Fatalf("после стопа: %d %s", r.StatusCode, raw)
			}
			appWaitBusy(t, e, client, sid, false)
			after := e.s.chatStoreRead(sid)
			if after.Thread != before.Thread || after.Model != before.Model || after.Harness != before.Harness {
				t.Fatalf("после стопа сменились нить или модель: %+v", after)
			}
			log := readFile(t, filepath.Join(e.proj, "fake-app-log"))
			if strings.Count(log, "turn ") != 2 || strings.Count(log, "start ") != 1 {
				t.Fatalf("после стопа создалась новая нить: %s", log)
			}
			if mode == "deaf" && !strings.Contains(log, "resume th-1") {
				t.Fatalf("после снятия процесса нить не продолжилась: %s", log)
			}
		})
	}
}
