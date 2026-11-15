package main

// Стенд дороги app-server: подменной agentctl разворачивает заказ в подменной
// клиент чужого протокола, а тот говорит JSONL проверенного контракта
// (DK-1624). Живой клиент стендом не зовётся: раскладка имена не проверяет, и
// дорогое входное окружение тут ни при чём. Поведением клиента правит файл
// fake-app-mode в каталоге разговора, а его заказы пишет fake-app-log.

import (
	"encoding/json"
	"fmt"
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

def configure_tools(params):
    # Process DEVKIT variables are filtered by the client before tool launch.
    # Only explicit per-thread values reach the command environment.
    config = params.get("config") or {}
    prefix = "shell_environment_policy.set."
    env = {k[len(prefix):]: v for k, v in config.items() if k.startswith(prefix)}
    log("effective-env " + json.dumps(env))
    log("groom " + env.get("DEVKIT_GROOM_SOURCE", "") + " " + env.get("DEVKIT_GROOM_MODE", ""))
    log("session " + env.get("DEVKIT_CHAT_SESSION_ID", ""))

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
        if mode() == "unsupported-reviewer" and params.get("approvalsReviewer"):
            emit({"id": mid, "error": {"message": "approvalsReviewer не поддерживается"}})
            continue
        configure_tools(params)
        log("start " + str(params.get("model", "")))
        log("policy " + json.dumps(params))
        emit({"id": mid, "result": {"thread": {"id": "th-1"}, "approvalPolicy": params.get("approvalPolicy"), "approvalsReviewer": "user" if mode() == "ignored-reviewer" else params.get("approvalsReviewer"), "sandbox": {"type": "workspaceWrite"}}})
    elif m == "thread/resume":
        if mode() == "unsupported-reviewer" and params.get("approvalsReviewer"):
            emit({"id": mid, "error": {"message": "approvalsReviewer не поддерживается"}})
            continue
        configure_tools(params)
        log("resume " + str(params.get("threadId", "")))
        log("policy " + json.dumps(params))
        emit({"id": mid, "result": {"thread": {"id": params.get("threadId")}, "approvalPolicy": params.get("approvalPolicy"), "approvalsReviewer": params.get("approvalsReviewer"), "sandbox": {"type": "workspaceWrite"}}})
    elif m == "turn/start":
        log("turn " + str(params.get("effort", "")))
        text = ""
        for part in params.get("input") or []:
            text = text + " " + str(part.get("text", ""))
        if mode() == "early":
            note("item/completed", {"item": {"type": "agentMessage",
                "text": "ответ на реплику:" + text}})
            note("turn/completed", {"turn": {"status": "completed"}})
            emit({"id": mid, "result": {}})
            continue
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

func TestAppGroomApprovalPolicy(t *testing.T) {
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
				want := "never"
				if grooming {
					want = "on-request"
					if p["approvalsReviewer"] != "auto_review" {
						t.Fatalf("reviewer: %v", p)
					}
				} else if p["approvalsReviewer"] != nil {
					t.Fatalf("обычный чат поменял reviewer: %v", p)
				}
				if p["approvalPolicy"] != want {
					t.Fatalf("policy: %v", p)
				}
				if policies == 0 && p["sandbox"] != "workspace-write" {
					t.Fatalf("песочница потеряна: %v", p)
				}
				policies++
			}
			if policies != 2 {
				t.Fatalf("нет старта и продолжения: %d", policies)
			}
		})
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
