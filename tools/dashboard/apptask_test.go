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
		if e.s.appTaskFind(sid, "", "") == nil && !taskhead.Working(taskhead.LockPath(e.home, "XR-002")) {
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
	if st := e.s.chatStoreRead(sid); st.Thread != "th-1" || st.Model != "модель-codex" || st.TaskRun {
		t.Fatalf("настройки после стопа: %+v", st)
	}
	if log := readFile(t, filepath.Join(e.proj, "fake-app-log")); strings.Count(log, "start ") != 1 || !strings.Contains(log, "resume th-1") {
		t.Fatalf("после стопа новая нить: %s", log)
	}
}

func TestAppTaskEndedChatUsesNeverAndDropsLockToken(t *testing.T) {
	e, c := appTaskEnv(t, "task-done")
	code, got := appTaskStart(t, e, c)
	if code != http.StatusOK {
		t.Fatalf("%d %+v", code, got)
	}
	sid := got["session"]
	waitTaskStopped(t, e, sid)
	st := e.s.chatStoreRead(sid)
	if st.TaskRun {
		t.Fatal("завершённый конвейер оставил права task-run")
	}
	if err := os.WriteFile(filepath.Join(e.proj, "fake-app-mode"), []byte("mute"), 0644); err != nil {
		t.Fatal(err)
	}
	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/say", `{"text":"просто разговор"}`)
	raw := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("обычный чат: %d %s", resp.StatusCode, raw)
	}
	log := readFile(t, filepath.Join(e.proj, "fake-app-log"))
	if after := e.s.chatStoreRead(sid); after.Thread != st.Thread || after.Model != st.Model || after.Harness != st.Harness || strings.Count(log, "start ") != 1 || !strings.Contains(log, "resume th-1") {
		t.Fatalf("завершённая задача потеряла нить/модель: %+v\n%s", after, log)
	}
	at := strings.LastIndex(log, "policy ")
	if at < 0 || !strings.Contains(log[at:], `"approvalPolicy": "never"`) || !strings.Contains(log[at:], `shell_environment_policy.set.DEVKIT_TASK_LOCK_FROM": ""`) {
		t.Fatalf("после task-run остались права/токен: %s", log)
	}
	e.s.appDrop(sid, "конец теста")
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
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("say stale: %d %s", resp.StatusCode, raw)
	}
	log := readFile(t, filepath.Join(e.proj, "fake-app-log"))
	if !strings.Contains(log, `"approvalPolicy": "never"`) || strings.Contains(log, `"approvalsReviewer": "auto_review"`) || !strings.Contains(log, `shell_environment_policy.set.DEVKIT_TASK_LOCK_FROM": ""`) {
		t.Fatalf("stale выдал права: %s", log)
	}
	if e.s.chatStoreRead(sid).TaskRun {
		t.Fatal("stale TaskRun сохранился после обычной реплики")
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
