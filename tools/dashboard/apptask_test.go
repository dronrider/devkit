package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/dronrider/devkit/internal/taskhead"
	"github.com/dronrider/devkit/internal/taskstate"
)

func waitTaskStopped(t *testing.T, e *testEnv, sid string) {
	t.Helper()
	until := time.After(10 * time.Second)
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		if e.s.appTaskFind(sid, "", "") == nil && !e.s.chatStoreRead(sid).TaskRun && !taskhead.Working(taskhead.LockPath(e.home, "XR-002")) {
			return
		}
		select {
		case <-until:
			t.Fatal("конвейер не снят")
		case <-tick.C:
		}
	}
}

func TestAppTaskChildCompletionDoesNotFinishPass(t *testing.T) {
	e, c := appTaskEnv(t, "task-child")
	code, got := appTaskStart(t, e, c)
	if code != http.StatusOK {
		t.Fatalf("%d %+v", code, got)
	}
	sid := got["session"]
	live := e.s.appLive(sid)
	if live == nil {
		t.Fatal("нет процесса")
	}
	// FIFO response follows both child notifications, so their processing is confirmed.
	if _, err := live.proc.call("initialize", map[string]any{}); err != nil {
		t.Fatal(err)
	}
	resp := doReq(t, c, "GET", e.srv.URL+"/api/projects/demo/chats/"+sid+"/status", "")
	var state struct {
		Busy bool `json:"busy"`
	}
	if err := json.Unmarshal([]byte(body(t, resp)), &state); err != nil || !state.Busy {
		t.Fatalf("дочерний ход погасил busy: %+v %v", state, err)
	}
	if !taskhead.Working(taskhead.LockPath(e.home, "XR-002")) {
		t.Fatal("дочерний ход снял замок")
	}
	st := e.s.chatStoreRead(sid)
	token := ""
	for _, line := range strings.Split(readFile(t, filepath.Join(e.proj, "fake-app-log")), "\n") {
		if raw, ok := strings.CutPrefix(line, "effective-env "); ok {
			var env map[string]string
			if json.Unmarshal([]byte(raw), &env) == nil && env[taskhead.LockFromEnv] != "" {
				token = env[taskhead.LockFromEnv]
			}
		}
	}
	if token == "" {
		t.Fatal("клиент не получил токен общего замка")
	}
	hold, err := taskhead.HoldForDelegate(e.home, st.Task, token)
	if err != nil || hold != nil {
		t.Fatalf("делегат своей головы: %v %v", hold, err)
	}
	if external, err := taskhead.HoldForDelegate(e.home, st.Task, ""); err == nil {
		external.Release()
		t.Fatal("внешний делегат обошёл замок")
	}
	log := readFile(t, filepath.Join(e.home, ".devkit", "heads", "XR-002.log"))
	if strings.Contains(log, "голова вышла") {
		t.Fatalf("дочерний ход завершил проход: %s", log)
	}
	code, again := appTaskStart(t, e, c)
	if code != http.StatusConflict {
		t.Fatalf("дубль: %d %+v", code, again)
	}
	if _, err := live.proc.call("test/finish", map[string]any{}); err != nil {
		t.Fatal(err)
	}
	waitTaskStopped(t, e, sid)
}

func TestAppTaskOtherClientLockRefuses(t *testing.T) {
	e, c := appTaskEnv(t, "mute")
	holdHeadLock(t, e.home, "XR-002")
	code, got := appTaskStart(t, e, c)
	if code != http.StatusConflict {
		t.Fatalf("дубль чужого клиента: %d %+v", code, got)
	}
	if raw, err := os.ReadFile(filepath.Join(e.proj, "fake-app-log")); err == nil {
		t.Fatalf("клиент поднят: %s", raw)
	}
}

func TestAppTaskEarlyFailureIsInFeed(t *testing.T) {
	e, c := appTaskEnv(t, "unsupported-reviewer")
	code, got := appTaskStart(t, e, c)
	if code != http.StatusBadGateway {
		t.Fatalf("%d %+v", code, got)
	}
	appWaitFeed(t, e, got["session"], "не поднялся")
	waitTaskStopped(t, e, got["session"])
}

func TestAppTaskStopRemovesCarrier(t *testing.T) {
	e, c := appTaskEnv(t, "mute")
	code, got := appTaskStart(t, e, c)
	if code != http.StatusOK {
		t.Fatalf("%d %+v", code, got)
	}
	sid := got["session"]
	a := e.s.appTaskFind(sid, "", "")
	if a == nil {
		t.Fatal("нет носителя")
	}
	for path, want := range map[string]os.FileMode{a.dir: 0700, filepath.Join(a.dir, "turn"): 0600} {
		fi, err := os.Stat(path)
		if err != nil || fi.Mode().Perm() != want {
			t.Fatalf("права %s: %v %v", path, fi, err)
		}
	}
	resp := doReq(t, c, "DELETE", e.srv.URL+"/api/projects/demo/runs/XR-002", "")
	raw := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("стоп: %d %s", resp.StatusCode, raw)
	}
	waitTaskStopped(t, e, sid)
	if _, err := os.Stat(a.dir); !os.IsNotExist(err) {
		t.Fatalf("socket остался: %v", err)
	}
	if live := e.s.appLive(sid); live != nil {
		t.Fatal("процесс остался")
	}
	appMode(t, e, "")
	r := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/say", `{"text":"после остановки задачи"}`)
	if raw := body(t, r); r.StatusCode != http.StatusOK {
		t.Fatalf("продолжение после стопа задачи: %d %s", r.StatusCode, raw)
	}
	appWaitBusy(t, e, c, sid, false)
	waitTaskStopped(t, e, sid)
	if st := e.s.chatStoreRead(sid); st.Thread != "th-1" || st.Model != "модель-codex" || st.TaskRun {
		t.Fatalf("настройки после стопа: %+v", st)
	}
	if log := readFile(t, filepath.Join(e.proj, "fake-app-log")); strings.Count(log, "start ") != 1 || !strings.Contains(log, "resume th-1") {
		t.Fatalf("после стопа новая нить: %s", log)
	}
}

func TestAppTaskEndedChatKeepsPolicyAndDropsLockToken(t *testing.T) {
	for _, archived := range []bool{false, true} {
		t.Run(map[bool]string{false: "done-section", true: "archive-row"}[archived], func(t *testing.T) {
			e, c := appTaskEnv(t, "task-done")
			code, got := appTaskStart(t, e, c)
			if code != http.StatusOK {
				t.Fatalf("%d %+v", code, got)
			}
			sid := got["session"]
			waitTaskStopped(t, e, sid)
			if archived {
				if err := os.WriteFile(filepath.Join(e.proj, "board-state"), []byte(`{"sections":[]}`), 0644); err != nil {
					t.Fatal(err)
				}
				putDoc(t, e.proj, "docs/TASKS-archive.md", "# Архив\n\n| ID | Задача | Тип | P | Закрыто | Ссылка |\n|----|--------|-----|---|---------|--------|\n| XR-002 | закрытая | bug | P1 | 2026-11-17 | - |\n")
			} else {
				appTaskBoard(t, e, "done")
			}
			before := e.s.chatStoreRead(sid)
			appMode(t, e, "mute")
			code, raw := appTaskSay(t, e, c, sid, "обычный разговор после закрытия", "")
			if code != http.StatusOK {
				t.Fatalf("%d %s", code, raw)
			}
			after := e.s.chatStoreRead(sid)
			if after.Thread != before.Thread || after.Model != before.Model || after.Harness != before.Harness || after.TaskRun {
				t.Fatalf("closed identity/rights %+v", after)
			}
			if taskhead.Working(taskhead.LockPath(e.home, "XR-002")) || e.s.appTaskFind(sid, "", "") != nil {
				t.Fatal("closed chat raised taskhead")
			}
			log := readFile(t, filepath.Join(e.proj, "fake-app-log"))
			at := strings.LastIndex(log, "policy ")
			if at < 0 || !strings.Contains(log[at:], `"approvalPolicy": "on-request"`) || !strings.Contains(log[at:], `"approvalsReviewer": "auto_review"`) || !strings.Contains(log[at:], `shell_environment_policy.set.DEVKIT_TASK_LOCK_FROM": ""`) {
				t.Fatalf("closed retained permissions/token: %s", log)
			}
			if strings.Count(log, "start ") != 1 || !strings.Contains(log, "resume th-1") {
				t.Fatalf("closed thread changed: %s", log)
			}
			e.s.appDrop(sid, "test cleanup")
		})
	}
}

func TestAppTaskCompletionMatchesThreadAndTurn(t *testing.T) {
	for _, tc := range []struct {
		thread, turn, status string
		complete, fail       bool
	}{
		{"child", "main-turn", "completed", false, false},
		{"main-thread", "child", "completed", false, false},
		{"main-thread", "main-turn", "completed", true, false},
		{"main-thread", "main-turn", "failed", true, true},
	} {
		params := json.RawMessage(fmt.Sprintf(`{"threadId":%q,"turn":{"id":%q,"status":%q}}`, tc.thread, tc.turn, tc.status))
		done, err := appTaskCompletion(params, "main-thread", "main-turn")
		if done != tc.complete || (err != nil) != tc.fail {
			t.Errorf("%s: %t %v", params, done, err)
		}
	}
}

func TestAppTaskStartAndStopWithoutTmux(t *testing.T) {
	e, c := appTaskEnv(t, "mute")
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal(err)
	}
	os.Remove(filepath.Join(e.bin, "tmux"))
	for name, path := range map[string]string{"python3": python, "env": "/usr/bin/env", "dirname": "/usr/bin/dirname", "cat": "/bin/cat"} {
		if err := os.Symlink(path, filepath.Join(e.bin, name)); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", e.bin)
	if _, err := exec.LookPath("tmux"); err == nil {
		t.Fatal("tmux остался в PATH")
	}
	code, got := appTaskStart(t, e, c)
	if code != http.StatusOK {
		t.Fatalf("start без tmux: %d %+v", code, got)
	}
	resp := doReq(t, c, "DELETE", e.srv.URL+"/api/projects/demo/runs/XR-002", "")
	raw := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("stop без tmux: %d %s", resp.StatusCode, raw)
	}
	waitTaskStopped(t, e, got["session"])
}

func assertAppTaskManualStop(t *testing.T, e *testEnv) {
	t.Helper()
	guard, err := taskstate.Acquire(e.home, e.proj, "XR-002")
	if err != nil {
		t.Fatal(err)
	}
	defer guard.Close()
	if !guard.State.Stopped {
		t.Fatal("явная остановка не сохранена")
	}
}

func TestAppTaskChatStopVariantsPersistState(t *testing.T) {
	for _, request := range []string{`{}`, `{"kill":true}`, `{"drop":true}`} {
		t.Run(request, func(t *testing.T) {
			e, c := appTaskEnv(t, "mute")
			code, got := appTaskStart(t, e, c)
			if code != http.StatusOK {
				t.Fatalf("%d %+v", code, got)
			}
			resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+got["session"]+"/stop", request)
			raw := body(t, resp)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("stop: %d %s", resp.StatusCode, raw)
			}
			assertAppTaskManualStop(t, e)
			waitTaskStopped(t, e, got["session"])
			if e.s.appLive(got["session"]) != nil {
				t.Fatal("клиент пережил stop")
			}
		})
	}
}

func TestAppTaskCancelDuringHandshake(t *testing.T) {
	for _, shutdown := range []bool{false, true} {
		t.Run(fmt.Sprint(shutdown), func(t *testing.T) {
			e, c := appTaskEnv(t, "startup-barrier")
			type outcome struct {
				Code int
				Body string
				Err  error
			}
			started := make(chan outcome, 1)
			go func() {
				resp, err := c.Post(e.srv.URL+"/api/projects/demo/runs", "application/json", strings.NewReader(`{"id":"XR-002","harness":"codextest","tier":"pro","effort":"high"}`))
				if err != nil {
					started <- outcome{Err: err}
					return
				}
				defer resp.Body.Close()
				raw, err := io.ReadAll(resp.Body)
				started <- outcome{Code: resp.StatusCode, Body: string(raw), Err: err}
			}()
			handshake := filepath.Join(e.proj, "fake-app-handshake")
			tick := time.NewTicker(10 * time.Millisecond)
			defer tick.Stop()
			deadline := time.After(10 * time.Second)
			var pid int
			for pid == 0 {
				if raw, err := os.ReadFile(handshake); err == nil {
					pid, _ = strconv.Atoi(string(raw))
				}
				if pid > 0 {
					break
				}
				select {
				case <-deadline:
					t.Fatal("нет барьера handshake")
				case <-tick.C:
				}
			}
			a := e.s.appTaskFind("", e.proj, "XR-002")
			if a == nil {
				t.Fatal("нет carrier на барьере")
			}
			if shutdown {
				e.s.appTasksClose()
			} else {
				resp := doReq(t, c, "DELETE", e.srv.URL+"/api/projects/demo/runs/XR-002", "")
				raw := body(t, resp)
				if resp.StatusCode != http.StatusOK {
					t.Fatalf("stop: %d %s", resp.StatusCode, raw)
				}
				assertAppTaskManualStop(t, e)
			}
			select {
			case result := <-started:
				if result.Err != nil || result.Code != http.StatusBadGateway {
					t.Fatalf("start после cancel: %+v", result)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("start завис после cancel")
			}
			waitTaskStopped(t, e, a.sid)
			if taskhead.Alive(pid) {
				t.Fatal("клиент handshake пережил cancel")
			}
			if e.s.appLive(a.sid) != nil || e.s.chatStoreRead(a.sid).TaskRun {
				t.Fatal("cancel воскресил TaskRun")
			}
			log := readFile(t, filepath.Join(e.proj, "fake-app-log"))
			if strings.Contains(log, "turn ") {
				t.Fatalf("turn после cancel: %s", log)
			}
		})
	}
}

func TestAppTaskStaleRecordCannotGrantTaskRights(t *testing.T) {
	e, c := appTaskEnv(t, "mute")
	sid := "asstaletask"
	st := chatStore{Task: "XR-002", Project: "demo", Model: "модель-codex", Harness: "codextest", Protocol: "codex", Thread: "old-thread", TaskRun: true, TaskRunPID: os.Getpid(), CxEffort: "high"}
	if err := e.s.chatStoreWrite(sid, st); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { e.s.appDrop(sid, "конец теста") })
	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/say", `{"text":"обычная реплика после рестарта"}`)
	raw := body(t, resp)
	if resp.StatusCode != http.StatusConflict || !strings.Contains(raw, "не подтверждён") {
		t.Fatalf("say stale: %d %s", resp.StatusCode, raw)
	}
	if log, err := os.ReadFile(filepath.Join(e.proj, "fake-app-log")); err == nil {
		t.Fatalf("stale started client: %s", log)
	}
}

func TestAppTaskRenamedHarness(t *testing.T) {
	e, c := appTaskEnv(t, "task-done")
	name := "customtask"
	profileDir := filepath.Join(filepath.Dir(e.proj), "devkit", "kit", "harness")
	if err := os.Rename(filepath.Join(profileDir, "codextest.toml"), filepath.Join(profileDir, name+".toml")); err != nil {
		t.Fatal(err)
	}
	snapshot := strings.ReplaceAll(appHarnessJSON, `"codextest"`, `"`+name+`"`)
	writeScript(t, e.bin, "agentctl", `case "$1" in
 exec) shift 4; exec "$@" ;;
 *) printf '%s' '`+snapshot+`' ;;
esac`)
	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/runs", `{"id":"XR-002","harness":"customtask","tier":"pro","effort":"high"}`)
	raw := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("имя профиля определило транспорт: %d %s", resp.StatusCode, raw)
	}
	var got map[string]string
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatal(err)
	}
	st := e.s.chatStoreRead(got["session"])
	if got["harness"] != name || st.Harness != name || st.Task != "XR-002" || st.Thread == "" {
		t.Fatalf("переименованный профиль не сохранился: %+v %+v", got, st)
	}
	waitTaskStopped(t, e, got["session"])
}

func TestAppTaskFirstTextDeliveredOnce(t *testing.T) {
	e, c := appTaskEnv(t, "mute")
	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/runs", `{"id":"XR-002","harness":"codextest","tier":"pro","text":"Различимая первая фраза"}`)
	raw := body(t, resp)
	if resp.StatusCode != 200 {
		t.Fatalf("start: %d %s", resp.StatusCode, raw)
	}
	var result map[string]string
	json.Unmarshal([]byte(raw), &result)
	if !strings.Contains(result["wire"], "Различимая первая фраза") {
		t.Fatalf("нет провода первой реплики: %s", raw)
	}
	log := readFile(t, filepath.Join(e.proj, "fake-app-log"))
	orders := 0
	for _, line := range strings.Split(log, "\n") {
		if !strings.HasPrefix(line, "input ") {
			continue
		}
		var input []map[string]string
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "input ")), &input); err != nil {
			t.Fatal(err)
		}
		for _, part := range input {
			orders += strings.Count(part["text"], "Различимая первая фраза")
		}
	}
	if orders != 1 {
		t.Fatalf("первая фраза не в единственном заказе: %s", log)
	}
	count := 0
	for _, item := range e.s.appFeedRead(result["session"]) {
		if item.Role == "user" && strings.Contains(item.Text, "Различимая первая фраза") {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("экземпляров первой реплики: %d", count)
	}
	resp = doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/runs", `{"id":"XR-002","harness":"codextest","text":"Различимая первая фраза"}`)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("второй запуск: %d %s", resp.StatusCode, body(t, resp))
	}
	resp.Body.Close()
}

// Префикс вложения перед общим заказом позволяет ленте конвейера показать
// файл после исчезновения локального пузыря первой реплики.
func TestAppTaskFirstAttachmentInHistory(t *testing.T) {
	e, c := appTaskEnv(t, "mute")
	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/runs",
		`{"id":"XR-002","intent":"chat","harness":"codextest","tier":"pro","text":"<fragment file=\"/stand/first.txt\">\nвставлен фрагмент 8 КБ\n</fragment>\n"}`)
	raw := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("запуск: %d %s", resp.StatusCode, raw)
	}
	var result map[string]string
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, item := range e.s.appFeedRead(result["session"]) {
		if item.Role != "user" {
			continue
		}
		n++
		if len(item.Frags) != 1 || item.Frags[0] != "/stand/first.txt" ||
			strings.Contains(item.Text, "<fragment") || item.Wire != result["wire"] {
			t.Errorf("вложение в истории: %+v", item)
		}
	}
	if n != 1 {
		t.Fatalf("экземпляров первой реплики: %d", n)
	}
}

func TestAppTaskModelChoicePreservesRun(t *testing.T) {
	e, c := appTaskEnv(t, "mute")
	appTaskRecoveryFixture(t, e)
	catalog := strings.Replace(appHarnessJSON, `"model": "модель-codex"}]`, `"model": "модель-codex"},{"tier":"base","model":"модель-next"}]`, 1)
	writeScript(t, e.bin, "agentctl", `case "$1" in
 exec) shift 4; exec "$@" ;;
 *) printf '%s' '`+catalog+`' ;;
esac`)
	code, first := appTaskStart(t, e, c)
	if code != http.StatusOK {
		t.Fatalf("запуск: %d %+v", code, first)
	}
	sid := first["session"]
	live := e.s.appLive(sid)
	if live == nil || !live.busyNow() {
		t.Fatal("первый ход не начался")
	}
	initial := e.s.chatStoreRead(sid)
	logPath := filepath.Join(e.proj, "fake-app-log")
	initialLog := readFile(t, logPath)
	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/model",
		`{"model":"модель-next","harness":"codextest"}`)
	if raw := body(t, resp); resp.StatusCode != http.StatusOK {
		t.Fatalf("выбор: %d %s", resp.StatusCode, raw)
	}
	if e.s.appLive(sid) != live || !live.busyNow() || readFile(t, logPath) != initialLog {
		t.Fatal("выбор запустил новый ход или прервал первый")
	}
	if current := e.s.chatStoreRead(sid); current.Model != initial.Model || current.TaskRunID != initial.TaskRunID {
		t.Fatalf("выбор изменил происхождение запуска: %+v", current)
	}
	resp = doReq(t, c, "DELETE", e.srv.URL+"/api/projects/demo/runs/XR-002", "")
	if raw := body(t, resp); resp.StatusCode != http.StatusOK {
		t.Fatalf("остановка между ходами: %d %s", resp.StatusCode, raw)
	}
	waitTaskStopped(t, e, sid)
	putDoc(t, e.proj, "fake-app-mode", "reply-open")
	resp = doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/say",
		`{"text":"реплика с новым выбором","msg_id":"model-choice","effort":"low"}`)
	if raw := body(t, resp); resp.StatusCode != http.StatusOK {
		t.Fatalf("реплика: %d %s", resp.StatusCode, raw)
	}
	appWaitFeed(t, e, sid, "ответ на реплику: реплика с новым выбором")
	var params map[string]any
	log := readFile(t, logPath)
	for _, line := range strings.Split(log, "\n") {
		if raw, ok := strings.CutPrefix(line, "params "); ok {
			params = nil
			if err := json.Unmarshal([]byte(raw), &params); err != nil {
				t.Fatal(err)
			}
		}
	}
	if strings.Count(log, "input ") != 2 || params["model"] != "модель-next" ||
		params["threadId"] != initial.Thread || params["effort"] != "low" {
		t.Fatalf("следующая реплика потеряла модель, нить или глубину: %s", log)
	}
	if current := e.s.chatStoreRead(sid); current.Model != initial.Model || current.Thread != initial.Thread {
		t.Fatalf("реплика изменила исходную модель или нить: %+v", current)
	}
}

func TestAppTaskDuplicateReceiptKeepsMessageID(t *testing.T) {
	e, c := appTaskEnv(t, "mute")
	appTaskRecoveryFixture(t, e)
	code, first := appTaskStart(t, e, c)
	if code != http.StatusOK {
		t.Fatalf("запуск: %d %+v", code, first)
	}
	sid := first["session"]
	resp := doReq(t, c, "DELETE", e.srv.URL+"/api/projects/demo/runs/XR-002", "")
	if raw := body(t, resp); resp.StatusCode != http.StatusOK {
		t.Fatalf("остановка: %d %s", resp.StatusCode, raw)
	}
	waitTaskStopped(t, e, sid)
	putDoc(t, e.proj, "fake-app-mode", "reply-open")
	const text = "Два одинаковых текста"
	say := func(claim, way string) string {
		t.Helper()
		code, raw := appTaskSay(t, e, c, sid, text, claim)
		var receipt map[string]string
		if err := json.Unmarshal([]byte(raw), &receipt); code != http.StatusOK || err != nil ||
			receipt["way"] != way || receipt["app_message_id"] == "" {
			t.Fatalf("квитанция %s: %d %s", claim, code, raw)
		}
		return receipt["app_message_id"]
	}
	a := say("one", "appserver")
	waitTaskStopped(t, e, sid)
	b := say("lost-response", "appserver")
	waitTaskStopped(t, e, sid)
	if a == b {
		t.Fatal("две отдельные реплики получили одну квитанцию")
	}
	if got := say("lost-response", "dup"); got != b {
		t.Fatalf("повтор потерял ID второй реплики: %s, жду %s", got, b)
	}
	var feedIDs []string
	for _, it := range e.s.appFeedRead(sid) {
		if it.Role == "user" && it.Text == text && it.App != nil && it.App.Method == "devkit/appTaskMessage" {
			if id, ok := it.App.Item["id"].(string); ok {
				feedIDs = append(feedIDs, id)
			}
		}
	}
	if len(feedIDs) != 2 || feedIDs[0] != a || feedIDs[1] != b {
		t.Fatalf("две реплики в ленте: %v, жду %s и %s", feedIDs, a, b)
	}
}

func TestAppTaskQueueUsesReplyEffort(t *testing.T) {
	e, c := appTaskEnv(t, "mute")
	appTaskRecoveryFixture(t, e)
	code, first := appTaskStart(t, e, c)
	if code != http.StatusOK {
		t.Fatalf("запуск: %d %+v", code, first)
	}
	sid := first["session"]
	e.s.appTasksClose()
	waitTaskStopped(t, e, sid)
	e.s.appWorkers.Wait()
	waitAppTaskRecoveryCarrierExit(t, e, sid)
	p, ok := e.s.projectByName("demo")
	if !ok {
		t.Fatal("проект не найден")
	}
	if err := e.s.appTaskRecover(p, sid); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ id, effort string }{{"one", "low"}, {"two", "high"}} {
		resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/say",
			fmt.Sprintf(`{"text":%q,"msg_id":%q,"effort":%q}`, tc.id, tc.id, tc.effort))
		if raw := body(t, resp); resp.StatusCode != http.StatusAccepted {
			t.Fatalf("очередь: %d %s", resp.StatusCode, raw)
		}
	}
	live := e.s.appLive(sid)
	for _, text := range []string{"one", "two"} {
		if _, err := live.proc.call("test/finish", map[string]bool{"keepOpen": true}); err != nil {
			t.Fatal(err)
		}
		appWaitFeed(t, e, sid, text)
	}
	log := readFile(t, filepath.Join(e.proj, "fake-app-log"))
	effortByText := map[string]string{}
	for _, line := range strings.Split(log, "\n") {
		if wire, ok := strings.CutPrefix(line, "params "); ok {
			var params struct {
				Effort string
				Input  []struct{ Text string }
			}
			if err := json.Unmarshal([]byte(wire), &params); err != nil {
				t.Fatal(err)
			}
			if len(params.Input) == 1 {
				effortByText[params.Input[0].Text] = params.Effort
			}
		}
	}
	if effortByText["one"] != "low" || effortByText["two"] != "high" {
		t.Fatalf("глубина двух реплик: %v, жду low и high", effortByText)
	}
}

func TestAppTaskReceiptLookupDoesNotSend(t *testing.T) {
	e := newTestEnv(t)
	client := e.loggedClient(t)
	const sid = "as-receipt"
	st := chatStore{Project: "demo", Protocol: "codex", TaskRunID: "closed-run"}
	if err := e.s.chatStoreWrite(sid, st); err != nil {
		t.Fatal(err)
	}
	const claim = "original-claim"
	entry, err := e.s.appTaskQueueAdd(sid, claim, "прежняя реплика")
	if err != nil {
		t.Fatal(err)
	}
	if err := e.s.appTaskQueueMark(sid, entry.ID, appTaskDelivery{sent: true, accepted: true}); err != nil {
		t.Fatal(err)
	}
	url := e.srv.URL + "/api/projects/demo/chats/" + sid + "/say"
	for _, tc := range []struct {
		claim string
		code  int
	}{{claim, http.StatusOK}, {"unknown-claim", http.StatusNotFound}, {"", http.StatusBadRequest}} {
		request := map[string]any{"msg_id": tc.claim, "receipt_only": true}
		if tc.claim == "unknown-claim" {
			request["text"] = "этот текст не должен отправиться"
		}
		data, err := json.Marshal(request)
		if err != nil {
			t.Fatal(err)
		}
		resp := doReq(t, client, "POST", url, string(data))
		raw := body(t, resp)
		if resp.StatusCode != tc.code {
			t.Errorf("квитанция %q: %d %s, жду %d", tc.claim, resp.StatusCode, raw, tc.code)
			continue
		}
		if resp.StatusCode == http.StatusOK {
			var got map[string]string
			if err := json.Unmarshal([]byte(raw), &got); err != nil || got["way"] != "dup" ||
				got["was"] != "appserver" || got["app_message_id"] != entry.ID {
				t.Errorf("исходная квитанция: %s, жду %q", raw, entry.ID)
			}
		}
	}
	err = e.s.appTaskQueue(sid, func(entries *[]appTaskMessage) bool {
		if len(*entries) != 1 || (*entries)[0].ID != entry.ID || !(*entries)[0].Accepted {
			t.Errorf("запрос квитанции изменил очередь: %+v", *entries)
		}
		return false
	})
	if err != nil {
		t.Fatal(err)
	}
}
