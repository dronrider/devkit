package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/dronrider/devkit/internal/chat"
	"github.com/dronrider/devkit/internal/taskstate"
)

func TestRunChatReceiptReplaysOriginalWire(t *testing.T) {
	for _, road := range []string{"tmux", "appserver"} {
		t.Run(road, func(t *testing.T) {
			e, c, log, command, harness := runReceiptEnv(t, road)
			mkProject(t, filepath.Join(filepath.Dir(e.proj), "other"))
			payload := fmt.Sprintf(`{"id":"XR-002","intent":"chat","msg_id":%q,"text":"Первая реплика","effort":"high","harness":%q}`, testRunReceiptKey(e.s.now()), harness)
			lookup := strings.TrimSuffix(payload, "}") + `,"receipt_only":true}`
			start := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/runs", payload)
			wire := body(t, start)
			if start.StatusCode != http.StatusOK || !strings.Contains(wire, "Первая реплика") {
				t.Fatalf("исход запуска: %d %s", start.StatusCode, wire)
			}
			// Новый сервер получает только дом прежнего сервера. Подтверждение
			// первого запроса клиент считает потерянным.
			restarted := newTestEnvAt(t, e.home, e.proj, e.bin)
			c = restarted.loggedClient(t)
			for _, retry := range []string{lookup, payload} {
				resp := doReq(t, c, "POST", restarted.srv.URL+"/api/projects/demo/runs", retry)
				if got := body(t, resp); resp.StatusCode != http.StatusOK || got != wire {
					t.Fatalf("повтор: %d %s, исходный ответ %s", resp.StatusCode, got, wire)
				}
			}
			for _, tc := range []struct{ project, payload string }{
				{"other", lookup},
				{"demo", strings.Replace(lookup, "XR-002", "XR-003", 1)},
				{"demo", strings.Replace(lookup, "Первая реплика", "Другой текст", 1)},
				{"demo", strings.Replace(lookup, "high", "low", 1)},
			} {
				resp := doReq(t, c, "POST", restarted.srv.URL+"/api/projects/"+tc.project+"/runs", tc.payload)
				if got := body(t, resp); resp.StatusCode != http.StatusConflict || strings.Contains(got, "Первая реплика") {
					t.Fatalf("чужая квитанция: %d %s", resp.StatusCode, got)
				}
			}
			if log := readFile(t, log); strings.Count(log, command) != 1 {
				t.Fatalf("первый ход запустился повторно: %s", log)
			}
		})
	}
}

func TestRunChatReceiptRetriesBeforeDispatch(t *testing.T) {
	for _, road := range []string{"tmux", "appserver"} {
		t.Run(road, func(t *testing.T) {
			e, _, log, command, harness := runReceiptEnv(t, road)
			request := runStartRequest{ID: "XR-002", Intent: "chat", MsgID: testRunReceiptKey(e.s.now()), Text: "Исходная реплика", Harness: harness, Effort: "high"}
			writeRunReceiptMarker(t, e, request, "before_dispatch")
			e.srv.Close()
			restarted := newTestEnvAt(t, e.home, e.proj, e.bin)
			if road == "appserver" {
				t.Cleanup(restarted.s.appWorkers.Wait)
				t.Cleanup(restarted.s.appTasksClose)
			}
			c := restarted.loggedClient(t)
			url := restarted.srv.URL + "/api/projects/demo/runs"
			payload := fmt.Sprintf(`{"id":%q,"intent":"chat","msg_id":%q,"text":%q,"harness":%q,"effort":"high"}`, request.ID, request.MsgID, request.Text, harness)
			lookup := strings.TrimSuffix(payload, "}") + `,"receipt_only":true}`
			resp := doReq(t, c, "POST", url, lookup)
			if got := body(t, resp); resp.StatusCode != http.StatusNotFound || !strings.Contains(got, `"missing":true`) {
				t.Fatalf("квитанция до передачи носителю: %d %s", resp.StatusCode, got)
			}
			other := strings.Replace(payload, request.Text, "Другой текст", 1)
			resp = doReq(t, c, "POST", url, other)
			if got := body(t, resp); resp.StatusCode != http.StatusConflict {
				t.Fatalf("повтор с другим текстом: %d %s", resp.StatusCode, got)
			}
			if calls := readFile(t, log); strings.Contains(calls, command) {
				t.Fatalf("носитель вызван до повтора: %s", calls)
			}
			resp = doReq(t, c, "POST", url, payload)
			wire := body(t, resp)
			if resp.StatusCode != http.StatusOK || !strings.Contains(wire, request.Text) {
				t.Fatalf("повтор исходной реплики: %d %s", resp.StatusCode, wire)
			}
			for _, retry := range []string{lookup, payload} {
				resp := doReq(t, c, "POST", url, retry)
				if got := body(t, resp); resp.StatusCode != http.StatusOK || got != wire {
					t.Fatalf("подтверждённая квитанция: %d %s, исходный ответ %s", resp.StatusCode, got, wire)
				}
			}
			if calls := readFile(t, log); strings.Count(calls, command) != 1 {
				t.Fatalf("вызовы носителя после повтора: %s", calls)
			}
		})
	}
}

func TestRunChatReceiptRejectsUncertainOrLockedMarker(t *testing.T) {
	for _, tc := range []struct{ name, phase string }{
		{"старый формат", ""},
		{"передача носителю возможна", "dispatching"},
		{"замок занят", "before_dispatch"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, c, log := runsEnv(t, "")
			request := runStartRequest{ID: "XR-002", Intent: "chat", MsgID: testRunReceiptKey(e.s.now())}
			path := writeRunReceiptMarker(t, e, request, tc.phase)
			payload := fmt.Sprintf(`{"id":%q,"intent":"chat","msg_id":%q}`, request.ID, request.MsgID)
			requests := []string{payload}
			if tc.phase == "before_dispatch" {
				lock, err := chat.Lock(path + ".lock")
				if err != nil {
					t.Fatal(err)
				}
				defer lock.Close()
			} else {
				requests = append(requests, strings.TrimSuffix(payload, "}")+`,"receipt_only":true}`)
			}
			for _, request := range requests {
				resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/runs", request)
				if got := body(t, resp); resp.StatusCode != http.StatusBadGateway || !strings.Contains(got, `"unknown":true`) {
					t.Fatalf("исход запуска: %d %s", resp.StatusCode, got)
				}
			}
			if calls := readFile(t, log); strings.Contains(calls, "new-session") {
				t.Fatalf("неподтверждённый исход вызвал носитель: %s", calls)
			}
		})
	}
}

func TestRunChatReceiptDispatchMarkerWriteFailure(t *testing.T) {
	for _, road := range []string{"tmux", "appserver"} {
		t.Run(road, func(t *testing.T) {
			e, c, log, command, harness := runReceiptEnv(t, road)
			key := testRunReceiptKey(e.s.now())
			path := testRunReceiptPath(t, e.home, key)
			// Вердикт запрашивается после первого маркера. Подмена его файла
			// каталогом ломает переименование при записи второй фазы.
			agent := readFile(t, filepath.Join(e.bin, scriptBody("agentctl")))
			writeScript(t, e.bin, "agentctl", fmt.Sprintf("if [ \"$1\" = pick ]; then mv %s %s && mkdir %s || exit 1; fi\n%s",
				shQuote(path), shQuote(path+".before"), shQuote(path), agent))
			payload := fmt.Sprintf(`{"id":"XR-002","intent":"chat","msg_id":%q,"harness":%q}`, key, harness)
			resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/runs", payload)
			if got := body(t, resp); resp.StatusCode != http.StatusBadGateway || !strings.Contains(got, "не удалось сохранить квитанцию перед запуском") {
				t.Fatalf("запись второй фазы: %d %s", resp.StatusCode, got)
			}
			if saved := readFile(t, path+".before"); !strings.Contains(saved, `"phase":"before_dispatch"`) {
				t.Fatalf("маркер до запроса вердикта: %s", saved)
			}
			if calls := readFile(t, log); strings.Contains(calls, command) {
				t.Fatalf("носитель вызван после сбоя записи: %s", calls)
			}
		})
	}
}

func runReceiptEnv(t *testing.T, road string) (*testEnv, *http.Client, string, string, string) {
	t.Helper()
	if road == "tmux" {
		e, c, log := runsEnv(t, "")
		return e, c, log, "new-session", ""
	}
	e, c := appTaskEnv(t, "task-done")
	return e, c, filepath.Join(e.proj, "fake-app-log"), "input ", "codextest"
}

func testRunReceiptKey(stamp time.Time) string {
	return "m-" + strconv.FormatInt(stamp.UnixMilli(), 36) + "-1-77ab"
}

func testRunReceiptPath(t *testing.T, home, key string) string {
	t.Helper()
	ms, err := strconv.ParseInt(strings.Split(key, "-")[1], 36, 64)
	if err != nil {
		t.Fatal(err)
	}
	day := time.UnixMilli(ms).UTC().Format("2006-01-02")
	return filepath.Join(chatStoreDir(home), ".run-starts", day, fmt.Sprintf("%x", sha256.Sum256([]byte(key))))
}

func writeRunReceiptMarker(t *testing.T, e *testEnv, req runStartRequest, phase string) string {
	t.Helper()
	fields, err := json.Marshal([]string{"demo", e.proj, req.ID, req.Intent, req.Text, req.Harness, req.Tier, req.Effort})
	if err != nil {
		t.Fatal(err)
	}
	path := testRunReceiptPath(t, e.home, req.MsgID)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	marker := struct {
		Fingerprint string `json:"fingerprint"`
		Phase       string `json:"phase,omitempty"`
	}{fmt.Sprintf("%x", sha256.Sum256(fields)), phase}
	if err := writeChatStartJSON(path, marker); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRunChatReceiptLookupDoesNotStart(t *testing.T) {
	e, c, log := runsEnv(t, "")
	lookup := fmt.Sprintf(`{"id":"XR-002","intent":"chat","msg_id":%q,"receipt_only":true}`, testRunReceiptKey(e.s.now()))
	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/runs", lookup)
	if got := body(t, resp); resp.StatusCode != http.StatusNotFound || !strings.Contains(got, `"missing":true`) {
		t.Fatalf("нет квитанции: %d %s", resp.StatusCode, got)
	}
	if got := readFile(t, log); strings.Contains(got, "new-session") {
		t.Fatalf("чтение квитанции запустило работу: %s", got)
	}
}

func TestRunChatReceiptPreservesUnknownAfterTurnDispatch(t *testing.T) {
	e, c := appTaskEnv(t, "recover-die")
	payload := fmt.Sprintf(`{"id":"XR-002","harness":"codextest","intent":"chat","msg_id":%q,"text":"Реплика с потерянным подтверждением"}`, testRunReceiptKey(e.s.now()))
	lookup := strings.TrimSuffix(payload, "}") + `,"receipt_only":true}`
	for _, request := range []string{payload, lookup, payload} {
		resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/runs", request)
		if got := body(t, resp); resp.StatusCode != http.StatusBadGateway || !strings.Contains(got, `"unknown":true`) {
			t.Fatalf("неподтверждённый исход: %d %s", resp.StatusCode, got)
		}
	}
	if log := readFile(t, filepath.Join(e.proj, "fake-app-log")); strings.Count(log, "input ") != 1 {
		t.Fatalf("первый запрос не дошёл до клиента ровно один раз: %s", log)
	}
}

func TestRunChatReceiptMarkerPrecedesRecoveryAndSweep(t *testing.T) {
	e, c, log := runsEnv(t, "task-XR-004\\n")
	const id = "XR-004"
	key := testRunReceiptKey(e.s.now())
	e.s.chatRaised("task-"+id, "", id, "demo")
	writePeerTmux(t, e.home, "cccc4444-4444-4444-8444-444444444444", "task-"+id+":@2.%2", "idle")
	if err := taskstate.SetStopped(e.home, e.proj, id, true); err != nil {
		t.Fatal(err)
	}
	path := testRunReceiptPath(t, e.home, key)
	// Доска читается после проверки квитанции. Каталог на месте её
	// файла ломает атомарную запись маркера перед первым действием.
	writeScript(t, e.bin, "taskctl", "mkdir "+shQuote(path)+"\nprintf '%s' "+shQuote(runsBoardJSON))
	payload := fmt.Sprintf(`{"id":%q,"intent":"chat","msg_id":%q}`, id, key)
	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/runs", payload)
	raw := body(t, resp)
	state, err := taskstate.Acquire(e.home, e.proj, id)
	if err != nil {
		t.Fatal(err)
	}
	stopped := state.State.Stopped
	state.Close()
	if !stopped {
		t.Error("ручной стоп снят до сохранения квитанции")
	}
	if calls := readFile(t, log); strings.Contains(calls, "kill-session") || strings.Contains(calls, "new-session") {
		t.Errorf("действие в tmux началось до сохранения квитанции: %s", calls)
	}
	if resp.StatusCode != http.StatusBadGateway || !strings.Contains(raw, `"run_receipt":true`) || strings.Contains(raw, `"unknown":true`) {
		t.Fatalf("неподтверждённая запись маркера до запуска: %d %s", resp.StatusCode, raw)
	}
}

func TestRunChatReceiptConfirmsAndReplaysPreflightFailure(t *testing.T) {
	for _, tc := range []struct {
		name, reason string
	}{
		{"board", "тестовый отказ доски"},
		{"login", "вход в клиента истёк"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, c, log := runsEnv(t, "")
			if tc.name == "board" {
				writeScript(t, e.bin, "taskctl", "echo 'тестовый отказ доски' >&2\nexit 2")
			} else {
				writeAgentctlVerdict(t, e.bin, harnessTiersFixture, "pro", "втораяtest", "Login expired. Please run /login")
				writeScript(t, e.bin, "клиент-2", "exit 0")
			}
			payload := fmt.Sprintf(`{"id":"XR-002","intent":"chat","msg_id":%q}`, testRunReceiptKey(e.s.now()))
			url := e.srv.URL + "/api/projects/demo/runs"
			resp := doReq(t, c, "POST", url, payload)
			raw := body(t, resp)
			if resp.StatusCode != http.StatusBadGateway || !strings.Contains(raw, `"run_receipt":true`) ||
				!strings.Contains(raw, tc.reason) || strings.Contains(raw, `"unknown":true`) {
				t.Fatalf("отказ до вызова носителя: %d %s", resp.StatusCode, raw)
			}
			writeScript(t, e.bin, "taskctl", "printf '%s' "+shQuote(runsBoardJSON))
			writeAgentctlPick(t, e.bin, harnessTiersFixture, "pro")
			lookup := strings.TrimSuffix(payload, "}") + `,"receipt_only":true}`
			resp = doReq(t, c, "POST", url, lookup)
			if got := body(t, resp); resp.StatusCode != http.StatusBadGateway || got != raw {
				t.Fatalf("сохранённый отказ: %d %s, прежний ответ %s", resp.StatusCode, got, raw)
			}
			if calls := readFile(t, log); strings.Contains(calls, "new-session") {
				t.Fatalf("чтение отказа запустило работу: %s", calls)
			}
		})
	}
}

func TestRunChatReceiptKeyTime(t *testing.T) {
	e, c, log := runsEnv(t, "")
	now := e.s.now().UTC().Truncate(time.Millisecond)
	e.s.now = func() time.Time { return now }
	url := e.srv.URL + "/api/projects/demo/runs"
	for _, tc := range []struct {
		key  string
		read bool
		want int
		flag string
	}{
		{"m-test-key", false, http.StatusBadRequest, ""},
		{testRunReceiptKey(now.Add(5*time.Minute + time.Millisecond)), false, http.StatusBadRequest, ""},
		{testRunReceiptKey(now.Add(5 * time.Minute)), true, http.StatusNotFound, `"missing":true`},
		{testRunReceiptKey(now.Add(-7*24*time.Hour + time.Millisecond)), true, http.StatusNotFound, `"missing":true`},
		{testRunReceiptKey(now.Add(-7 * 24 * time.Hour)), true, http.StatusGone, `"expired":true`},
		{testRunReceiptKey(now.Add(-7 * 24 * time.Hour)), false, http.StatusGone, `"expired":true`},
	} {
		request := fmt.Sprintf(`{"id":"XR-002","intent":"chat","msg_id":%q,"receipt_only":%t}`, tc.key, tc.read)
		resp := doReq(t, c, http.MethodPost, url, request)
		if got := body(t, resp); resp.StatusCode != tc.want || !strings.Contains(got, tc.flag) {
			t.Errorf("ключ %s, чтение %t: %d %s, жду %d %s", tc.key, tc.read, resp.StatusCode, got, tc.want, tc.flag)
		}
	}
	if _, err := os.Stat(runReceiptDir(e.home)); !os.IsNotExist(err) {
		t.Errorf("запрос создал хранилище квитанций: %v", err)
	}
	if calls := readFile(t, log); strings.Contains(calls, "new-session") {
		t.Errorf("запрос вызвал носитель: %s", calls)
	}
}

func TestRunChatReceiptPruneKeepsLaunchClosedAfterClockRollback(t *testing.T) {
	e, c, log := runsEnv(t, "")
	now := e.s.now()
	key := testRunReceiptKey(now.Add(-6 * 24 * time.Hour))
	payload := fmt.Sprintf(`{"id":"XR-002","intent":"chat","msg_id":%q}`, key)
	url := e.srv.URL + "/api/projects/demo/runs"
	resp := doReq(t, c, http.MethodPost, url, payload)
	if got := body(t, resp); resp.StatusCode != http.StatusOK {
		t.Fatalf("первый запуск: %d %s", resp.StatusCode, got)
	}
	path := testRunReceiptPath(t, e.home, key)
	day := filepath.Dir(path)
	root := filepath.Dir(day)
	keep := filepath.Dir(testRunReceiptPath(t, e.home, testRunReceiptKey(now)))
	putDoc(t, keep, "current", "живой каталог")
	putDoc(t, root, "unrelated", "посторонний файл")
	lock, err := lockRunReceipts(day, syscall.LOCK_SH)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { lock.Close() })
	pruneAt := now.Add(3 * 24 * time.Hour)
	if err := pruneRunReceipts(root, pruneAt); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("уборка задела квитанцию действующего запроса: %v", err)
	}
	lock.Close()
	if err := pruneRunReceipts(root, pruneAt); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(day); !os.IsNotExist(err) {
		t.Errorf("просроченный каталог с квитанцией и замком остался: %v", err)
	}
	if readFile(t, filepath.Join(keep, "current")) != "живой каталог" || readFile(t, filepath.Join(root, "unrelated")) != "посторонний файл" {
		t.Fatal("уборка изменила соседние данные")
	}
	// Уборка уже видела время на трое суток позже. Новый сервер с часами
	// до истечения срока всё равно должен отказать по сохранённой границе.
	restarted := newTestEnvAt(t, e.home, e.proj, e.bin)
	c = restarted.loggedClient(t)
	url = restarted.srv.URL + "/api/projects/demo/runs"
	lookup := strings.TrimSuffix(payload, "}") + `,"receipt_only":true}`
	for _, request := range []string{lookup, payload} {
		resp := doReq(t, c, http.MethodPost, url, request)
		if got := body(t, resp); resp.StatusCode != http.StatusGone || !strings.Contains(got, `"expired":true`) {
			t.Errorf("повтор после отката часов: %d %s", resp.StatusCode, got)
		}
	}
	if calls := readFile(t, log); strings.Count(calls, "new-session") != 1 {
		t.Errorf("уборка разрешила повтор: %s", calls)
	}
}

func TestRunChatReceiptLogsLockOpenFailure(t *testing.T) {
	e, c, log, served := runsEnvWithLog(t, "")
	key := testRunReceiptKey(e.s.now())
	path := testRunReceiptPath(t, e.home, key)
	if err := os.MkdirAll(path+".lock", 0700); err != nil {
		t.Fatal(err)
	}
	payload := fmt.Sprintf(`{"id":"XR-002","intent":"chat","msg_id":%q}`, key)
	resp := doReq(t, c, http.MethodPost, e.srv.URL+"/api/projects/demo/runs", payload)
	if got := body(t, resp); resp.StatusCode != http.StatusBadGateway || !strings.Contains(got, `"unknown":true`) || strings.Contains(got, e.home) {
		t.Errorf("отказ замка: %d %s", resp.StatusCode, got)
	}
	if !served.contains(t, `квитанция запуска "XR-002" в demo: замок квитанции:`) {
		t.Error("отказ открытия замка не виден оператору")
	}
	if calls := readFile(t, log); strings.Contains(calls, "new-session") {
		t.Errorf("отказ открытия замка вызвал носитель: %s", calls)
	}
}

func TestRunChatReceiptLogsCorruptRecord(t *testing.T) {
	e, c, _, served := runsEnvWithLog(t, "")
	key := testRunReceiptKey(e.s.now())
	path := testRunReceiptPath(t, e.home, key)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{broken"), 0600); err != nil {
		t.Fatal(err)
	}
	query := fmt.Sprintf(`{"id":"XR-002","intent":"chat","msg_id":%q,"receipt_only":true}`, key)
	resp := doReq(t, c, http.MethodPost, e.srv.URL+"/api/projects/demo/runs", query)
	got := body(t, resp)
	if resp.StatusCode != http.StatusBadGateway || !strings.Contains(got, `"unknown":true`) || strings.Contains(got, "invalid character") {
		t.Errorf("ответ на битую квитанцию: %d %s", resp.StatusCode, got)
	}
	if !served.contains(t, `квитанция запуска "XR-002" в demo: чтение квитанции: invalid character`) {
		t.Error("ошибка разбора квитанции не видна оператору")
	}
}
