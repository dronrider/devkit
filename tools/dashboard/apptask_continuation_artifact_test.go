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
	"testing"

	"github.com/dronrider/devkit/internal/taskhead"
	"github.com/dronrider/devkit/internal/taskstate"
)

func TestAppTaskContinuationInstalledArtifact(t *testing.T) {
	if os.Getenv("DEVKIT_TASK_RUN_ARTIFACT") == "" {
		t.Skip("DEVKIT_TASK_RUN_ARTIFACT не назван")
	}
	e, c, restart := appArtifactRestartable(t, "task-check")
	code, got := appTaskStart(t, e, c)
	if code != http.StatusOK {
		t.Fatalf("initial %d %+v", code, got)
	}
	sid := got["session"]
	waitTaskStopped(t, e, sid)
	before := e.s.chatStoreRead(sid)
	appTaskBoard(t, e, "check")
	// Exercise migration from the installed historical record format.
	raw, _ := json.Marshal(before)
	var record map[string]any
	json.Unmarshal(raw, &record)
	for _, key := range []string{"taskRunRoot", "taskRunID", "taskRunSession"} {
		delete(record, key)
	}
	raw, _ = json.Marshal(record)
	var legacy chatStore
	json.Unmarshal(raw, &legacy)
	if err := e.s.chatStoreWrite(sid, legacy); err != nil {
		t.Fatal(err)
	}
	restart()
	c = e.loggedClient(t)
	appMode(t, e, "mute")
	text := "Решение ещё не принято.\nСохрани весь вопрос: $HOME и `literal`."
	code, rawText := appTaskSay(t, e, c, sid, text, "artifact-continuation")
	if code != http.StatusOK {
		t.Fatalf("resume %d %s", code, rawText)
	}
	after := e.s.chatStoreRead(sid)
	owner := taskhead.Owner(taskhead.LockPath(e.home, "XR-002"))
	if after.Thread != before.Thread || after.Model != before.Model || after.Harness != before.Harness || !after.TaskRun || owner != after.TaskRunPID || !taskhead.Working(taskhead.LockPath(e.home, "XR-002")) {
		t.Fatalf("ownership/identity %+v owner %d", after, owner)
	}
	g, err := taskstate.Acquire(e.home, e.proj, "XR-002")
	if err != nil {
		t.Fatal(err)
	}
	if g.State.Run == nil || g.State.Run.PID != owner || g.State.Stopped {
		t.Fatalf("lifecycle %+v", g.State)
	}
	g.Close()
	log := readFile(t, filepath.Join(e.proj, "fake-app-log"))
	lastEnv := map[string]string{}
	for _, line := range strings.Split(log, "\n") {
		if raw, ok := strings.CutPrefix(line, "effective-env "); ok {
			json.Unmarshal([]byte(raw), &lastEnv)
		}
	}
	if lastEnv["DEVKIT_TASK"] != "XR-002" || lastEnv[taskhead.LockFromEnv] != strconv.Itoa(owner) {
		t.Fatalf("tool environment %+v", lastEnv)
	}
	hold, err := taskhead.HoldForDelegate(e.home, "XR-002", lastEnv[taskhead.LockFromEnv])
	if err != nil || hold != nil {
		t.Fatalf("own delegate %v", err)
	}
	hold, err = taskhead.HoldForDelegate(e.home, "XR-002", "")
	if err == nil {
		hold.Release()
		t.Fatal("external executor accepted")
	}
	code, rawText = appTaskSay(t, e, c, sid, text, "artifact-continuation")
	if code != http.StatusOK || !strings.Contains(rawText, `"way":"dup"`) {
		t.Fatalf("duplicate %d %s", code, rawText)
	}
	code, rawText = appTaskSay(t, e, c, sid, "parallel", "")
	if code != http.StatusConflict {
		t.Fatalf("parallel %d %s", code, rawText)
	}
	restart()
	c = e.loggedClient(t)
	waitTaskStopped(t, e, sid)
	code, rawText = appTaskSay(t, e, c, sid, "Продолжи после перезапуска во время хода.", "artifact-active-restart")
	if code != http.StatusOK {
		t.Fatalf("active restart %d %s", code, rawText)
	}
	resumed := e.s.chatStoreRead(sid)
	if resumed.TaskRunPID == owner || !taskhead.Working(taskhead.LockPath(e.home, "XR-002")) {
		t.Fatalf("active restart did not restore owner %+v", resumed)
	}
	resp := doReq(t, c, "DELETE", e.srv.URL+"/api/projects/demo/runs/XR-002", "")
	if raw := body(t, resp); resp.StatusCode != http.StatusOK {
		t.Fatalf("stop %d %s", resp.StatusCode, raw)
	}
	waitTaskStopped(t, e, sid)
	assertAppTaskManualStop(t, e)
	restart()
	c = e.loggedClient(t)
	appMode(t, e, "reply-open")
	code, rawText = appTaskSay(t, e, c, sid, "Ответь одним ходом, оставь задачу открытой.", "artifact-second")
	if code != http.StatusOK {
		t.Fatalf("after stop %d %s", code, rawText)
	}
	waitTaskStopped(t, e, sid)
	selected := taskRunPath(e.cfg.Roots)
	runtime := readFile(t, selected)
	canonical, err := filepath.EvalSymlinks(selected)
	if err != nil {
		t.Fatal(err)
	}
	expected := fmt.Sprintf("оболочка ответа %s sha256 %x", canonical, sha256.Sum256([]byte(runtime)))
	if trace := readFile(t, filepath.Join(e.home, ".devkit", "heads", "XR-002.log")); !strings.Contains(trace, expected) {
		t.Fatalf("actual selected runtime not confirmed: wanted %s\n%s", expected, trace)
	}
	t.Logf("actual runtime confirmed: %s", expected)
	log = readFile(t, filepath.Join(e.proj, "fake-app-log"))
	if strings.Count(log, "start ") != 1 || strings.Count(log, "resume th-1") != 3 || strings.Count(log, "turn high") != 4 {
		t.Fatalf("thread or automatic passes: %s", log)
	}
	exact := 0
	for _, line := range strings.Split(log, "\n") {
		if raw, ok := strings.CutPrefix(line, "input "); ok {
			var input []map[string]string
			json.Unmarshal([]byte(raw), &input)
			if len(input) == 1 && input[0]["text"] == text {
				exact++
			}
		}
	}
	if exact != 1 {
		t.Fatalf("full message delivered %d times: %s", exact, log)
	}
	last := strings.LastIndex(log, "policy ")
	for _, want := range []string{`"approvalPolicy": "on-request"`, `"approvalsReviewer": "auto_review"`, `"sandbox": "workspace-write"`} {
		if last < 0 || !strings.Contains(log[last:], want) {
			t.Fatalf("policy missing %s: %s", want, log)
		}
	}
	if state := readFile(t, filepath.Join(e.proj, "task-state")); state != "in-progress" {
		t.Fatalf("task unexpectedly closed: %s", state)
	}
	t.Log("PASS artifact: legacy migration, actual daemon restart, same thread/model, full message once, permissions RPC, own delegate, foreign/parallel refusal, stop and one-pass release on open task")
}
