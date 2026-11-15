package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dronrider/devkit/internal/taskhead"
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
	if !taskhead.Working(taskhead.LockPath(e.home, "XR-002")) {
		t.Fatal("дочерний ход снял замок")
	}
	st := e.s.chatStoreRead(sid)
	hold, err := taskhead.HoldForDelegate(e.home, st.Task, fmt.Sprint(st.TaskRunPID))
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
