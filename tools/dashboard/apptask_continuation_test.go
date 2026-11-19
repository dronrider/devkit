package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/dronrider/devkit/internal/taskhead"
	"github.com/dronrider/devkit/internal/taskstate"
)

func appTaskBoard(t *testing.T, e *testEnv, section string) {
	t.Helper()
	raw := `{"prefix":"XR","sections":[{"key":"` + section + `","rows":[{"id":"XR-002","title":"Ожидает решения"}]}]}`
	if err := os.WriteFile(filepath.Join(e.proj, "board-state"), []byte(raw), 0644); err != nil {
		t.Fatal(err)
	}
}

func appTaskSay(t *testing.T, e *testEnv, c *http.Client, sid, text, claim string) (int, string) {
	t.Helper()
	raw, _ := json.Marshal(map[string]string{"text": text, "msg_id": claim})
	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/say", string(raw))
	return resp.StatusCode, body(t, resp)
}

func TestAppTaskContinuationLifecycle(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(map[bool]string{false: "saved", true: "legacy"}[legacy], func(t *testing.T) {
			e, c := appTaskEnv(t, "task-check")
			code, got := appTaskStart(t, e, c)
			if code != http.StatusOK {
				t.Fatalf("start %d %+v", code, got)
			}
			sid := got["session"]
			waitTaskStopped(t, e, sid)
			before := e.s.chatStoreRead(sid)
			appTaskBoard(t, e, "check")
			if legacy {
				raw, _ := json.Marshal(before)
				var record map[string]any
				json.Unmarshal(raw, &record)
				delete(record, "taskRunID")
				delete(record, "taskRunRoot")
				raw, _ = json.Marshal(record)
				var old chatStore
				json.Unmarshal(raw, &old)
				if err := e.s.chatStoreWrite(sid, old); err != nil {
					t.Fatal(err)
				}
			}
			// Simulate lost in-memory registry after daemon restart; only persisted state remains.
			e.s.appTasksClose()
			appMode(t, e, "mute")
			putDoc(t, e.proj, ".devkit/task-XR-002.tail", "old rotation summary")
			text := "Согласование пока не принято.\nСохрани вопрос целиком: $HOME, `literal`, ё."
			code, raw := appTaskSay(t, e, c, sid, text, "continue-one")
			if code != http.StatusOK {
				t.Fatalf("continue %d %s", code, raw)
			}
			after := e.s.chatStoreRead(sid)
			if after.Thread != before.Thread || after.Model != before.Model || !after.TaskRun {
				t.Fatalf("identity/ownership lost: %+v", after)
			}
			owner := taskhead.Owner(taskhead.LockPath(e.home, "XR-002"))
			if owner <= 0 || owner != after.TaskRunPID || !taskhead.Working(taskhead.LockPath(e.home, "XR-002")) {
				t.Fatalf("no working owner %d %+v", owner, after)
			}
			g, err := taskstate.Acquire(e.home, e.proj, "XR-002")
			if err != nil {
				t.Fatal(err)
			}
			if g.State.Run == nil || g.State.Run.PID != owner || g.State.Stopped {
				t.Fatalf("state not resumed %+v", g.State)
			}
			g.Close()
			hold, err := taskhead.HoldForDelegate(e.home, "XR-002", "")
			if err == nil {
				hold.Release()
				t.Fatal("second worker accepted")
			}
			own, err := taskhead.HoldForDelegate(e.home, "XR-002", strconv.Itoa(owner))
			if err != nil || own != nil {
				t.Fatalf("own delegate refused %v", err)
			}
			code, raw = appTaskSay(t, e, c, sid, "parallel", "")
			if code != http.StatusConflict {
				t.Fatalf("parallel %d %s", code, raw)
			}
			log := readFile(t, filepath.Join(e.proj, "fake-app-log"))
			at := strings.LastIndex(log, "policy ")
			for _, want := range []string{`"approvalPolicy": "on-request"`, `"approvalsReviewer": "auto_review"`, `"sandbox": "workspace-write"`} {
				if at < 0 || !strings.Contains(log[at:], want) {
					t.Fatalf("policy missing %s: %s", want, log)
				}
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
				t.Fatalf("human input not delivered exactly once: %d %s", exact, log)
			}
			feed := e.s.appFeedRead(sid)
			count := 0
			for _, it := range feed {
				if it.Role == "user" && it.Text == text {
					count++
				}
			}
			if count != 1 {
				t.Fatalf("full human text count %d %+v", count, feed)
			}
			if err := os.WriteFile(filepath.Join(e.proj, "task-state"), []byte("in-progress"), 0644); err != nil {
				t.Fatal(err)
			}
			live := e.s.appLive(sid)
			if live == nil {
				t.Fatal("no live turn")
			}
			if _, err := live.proc.call("test/finish", map[string]any{"keepOpen": true}); err != nil {
				t.Fatal(err)
			}
			waitTaskStopped(t, e, sid)
			if log := readFile(t, filepath.Join(e.proj, "fake-app-log")); strings.Count(log, "turn high") != 2 || strings.Count(log, "start ") != 1 || strings.Count(log, "resume th-1") != 1 {
				t.Fatalf("automatic passes or new thread: %s", log)
			}
		})
	}
}

func TestAppTaskModelSwitchPreservesContinuation(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(map[bool]string{false: "saved", true: "legacy"}[legacy], func(t *testing.T) {
			e, c := appTaskEnv(t, "task-check")
			catalog := strings.Replace(appHarnessJSON, `"protocol": "codex", "models":`,
				`"chat": ["gpt-6.2-sol", "gpt-6.2-astra"], "protocol": "codex", "models":`, 1)
			writeScript(t, e.bin, "agentctl", `case "$1" in
  exec) shift 4; exec "$@" ;;
  *) printf '%s' '`+catalog+`' ;;
esac`)
			code, got := appTaskStart(t, e, c)
			if code != http.StatusOK {
				t.Fatalf("start %d %+v", code, got)
			}
			sid := got["session"]
			waitTaskStopped(t, e, sid)
			appTaskBoard(t, e, "check")
			before := e.s.chatStoreRead(sid)
			before.Model, before.CxEffort = "gpt-6.1-sol", "medium"
			if legacy {
				before.TaskRunID, before.TaskRunRoot, before.TaskRunSession = "", "", ""
			}
			if err := e.s.chatStoreWrite(sid, before); err != nil {
				t.Fatal(err)
			}
			g, err := taskstate.Acquire(e.home, e.proj, before.Task)
			if err != nil {
				t.Fatal(err)
			}
			g.State.Run.Model = before.Model
			if err := g.Save(); err != nil {
				t.Fatal(err)
			}
			g.Close()
			feed := readFile(t, e.s.appFeedFile(sid))
			resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/model",
				`{"model":"модель-codex2","harness":"codex2test"}`)
			if resp.StatusCode != http.StatusConflict || readFile(t, e.s.appFeedFile(sid)) != feed {
				t.Fatalf("task subscription changed %d %s", resp.StatusCode, body(t, resp))
			}
			resp.Body.Close()
			if current := e.s.chatStoreRead(sid); current.Thread != before.Thread || current.Model != before.Model || current.Harness != before.Harness {
				t.Fatalf("rejected task subscription changed record %+v", current)
			}
			code, raw := appTaskSay(t, e, c, sid, "не терять эту реплику", "legacy-held")
			if code != http.StatusConflict || !strings.Contains(raw, "перед продолжением выберите") {
				t.Fatalf("legacy turn started %d %s", code, raw)
			}
			choice := `{"model":"gpt-6.2-astra","harness":"codextest"}`
			if legacy {
				choice = `{"model":"gpt-6.2-astra"}`
			}
			resp = doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/model", choice)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("switch %d %s", resp.StatusCode, body(t, resp))
			}
			resp.Body.Close()
			after := e.s.chatStoreRead(sid)
			if after.Thread != before.Thread || after.Task != before.Task || after.TaskRunPID != before.TaskRunPID || after.TaskRunID == "" || after.CxEffort != before.CxEffort {
				t.Fatalf("task context lost %+v", after)
			}
			g, err = taskstate.Acquire(e.home, e.proj, before.Task)
			if err != nil {
				t.Fatal(err)
			}
			if g.State.Run.Model != "gpt-6.1-sol" {
				t.Fatal("historical run model rewritten")
			}
			g.Close()
			resp = doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/say",
				`{"text":"не терять эту реплику","msg_id":"legacy-held","effort":"high"}`)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("continuation %d %s", resp.StatusCode, body(t, resp))
			}
			resp.Body.Close()
			waitTaskStopped(t, e, sid)
			if after := e.s.chatStoreRead(sid); after.Thread != before.Thread || after.Model != "gpt-6.2-astra" || after.Task != before.Task || after.CxEffort != "high" {
				t.Fatalf("continued in another context %+v", after)
			}
			if got := readFile(t, e.s.appFeedFile(sid)); !strings.HasPrefix(got, feed) {
				t.Fatal("history lost")
			}
			log := readFile(t, filepath.Join(e.proj, "fake-app-log"))
			deliveries := 0
			for _, line := range strings.Split(log, "\n") {
				if params, ok := strings.CutPrefix(line, "params "); ok {
					var turn struct {
						Input []struct {
							Text string `json:"text"`
						} `json:"input"`
						Effort string `json:"effort"`
					}
					if err := json.Unmarshal([]byte(params), &turn); err != nil {
						t.Fatal(err)
					}
					for _, part := range turn.Input {
						if part.Text == "не терять эту реплику" {
							deliveries++
							if turn.Effort != "high" {
								t.Fatalf("явная глубина не дошла клиенту: %+v", turn)
							}
						}
					}
				}
			}
			if strings.Count(log, "start ") != 1 || !strings.Contains(log, "resume "+before.Thread) ||
				!strings.Contains(log, `"model": "gpt-6.2-astra"`) || deliveries != 1 {
				t.Fatalf("wrong model, thread or duplicate delivery: %s", log)
			}
		})
	}
}

func TestAppTaskContinuationRefusesUnprovenState(t *testing.T) {
	for _, kind := range []string{"missing", "unavailable", "changed-run", "other-chat", "other-head"} {
		t.Run(kind, func(t *testing.T) {
			e, c := appTaskEnv(t, "task-check")
			code, got := appTaskStart(t, e, c)
			if code != http.StatusOK {
				t.Fatalf("%d %+v", code, got)
			}
			sid := got["session"]
			waitTaskStopped(t, e, sid)
			appTaskBoard(t, e, "check")
			switch kind {
			case "missing":
				os.WriteFile(filepath.Join(e.proj, "board-state"), []byte(`{"sections":[]}`), 0644)
			case "unavailable":
				writeScript(t, e.bin, "taskctl", "exit 2")
			case "changed-run":
				g, err := taskstate.Acquire(e.home, e.proj, "XR-002")
				if err != nil {
					t.Fatal(err)
				}
				g.State.Run.ID = "another-run"
				g.Save()
				g.Close()
			case "other-chat":
				st := e.s.chatStoreRead(sid)
				sid = "asothercopy"
				e.s.chatStoreWrite(sid, st)
			case "other-head":
				holdHeadLock(t, e.home, "XR-002")
			}
			before := readFile(t, filepath.Join(e.proj, "fake-app-log"))
			code, raw := appTaskSay(t, e, c, sid, "must refuse", "")
			if code != http.StatusConflict {
				t.Fatalf("%d %s", code, raw)
			}
			if after := readFile(t, filepath.Join(e.proj, "fake-app-log")); before != after {
				t.Fatalf("refusal started client: %s", after)
			}
		})
	}
}

func TestAppTaskContinuationUpgradesIdleOrdinaryClient(t *testing.T) {
	for _, busy := range []bool{false, true} {
		t.Run(map[bool]string{false: "idle", true: "busy"}[busy], func(t *testing.T) {
			e, c := appTaskEnv(t, "task-check")
			code, got := appTaskStart(t, e, c)
			if code != http.StatusOK {
				t.Fatalf("%d %+v", code, got)
			}
			sid := got["session"]
			waitTaskStopped(t, e, sid)
			appTaskBoard(t, e, "check")
			appMode(t, e, "mute")
			if err := os.WriteFile(filepath.Join(e.proj, "fake-app-enforce-writer-lock"), nil, 0600); err != nil {
				t.Fatal(err)
			}
			st := e.s.chatStoreRead(sid)
			ordinary, err := e.s.appSpawn(sid, e.s.chatHarnessOf(st.Model, st.Harness), st.Model, e.proj, "demo", st.Thread)
			if err != nil {
				t.Fatal(err)
			}
			ordinary.mu.Lock()
			ordinary.busy = busy
			ordinary.mu.Unlock()
			code, raw := appTaskSay(t, e, c, sid, "продолжение", "")
			if busy {
				if code != http.StatusConflict || ordinary.proc.gone() {
					t.Fatalf("busy taken over: %d %s", code, raw)
				}
				e.s.appDrop(sid, "test cleanup")
				return
			}
			if code != http.StatusOK {
				t.Fatalf("idle upgrade %d %s", code, raw)
			}
			if !ordinary.proc.gone() || !e.s.chatStoreRead(sid).TaskRun {
				t.Fatal("idle not upgraded")
			}
			log := readFile(t, filepath.Join(e.proj, "fake-app-log"))
			at := strings.LastIndex(log, "policy ")
			if at < 0 || !strings.Contains(log[at:], `"approvalsReviewer": "auto_review"`) {
				t.Fatalf("no upgrade %s", log)
			}
		})
	}
}

func TestAppTaskManualBindingRemainsOrdinary(t *testing.T) {
	e, c := appTaskEnv(t, "mute")
	sid := "asmanualbinding"
	st := chatStore{Task: "XR-002", Project: "demo", Model: "модель-codex", Harness: "codextest", Protocol: "codex", Thread: "th-manual"}
	if err := e.s.chatStoreWrite(sid, st); err != nil {
		t.Fatal(err)
	}
	e.s.mainChatAssign("XR-002", sid, "demo", "выбор")
	code, raw := appTaskSay(t, e, c, sid, "обычный разговор", "")
	if code != http.StatusOK {
		t.Fatalf("%d %s", code, raw)
	}
	defer e.s.appDrop(sid, "test cleanup")
	log := readFile(t, filepath.Join(e.proj, "fake-app-log"))
	if !strings.Contains(log, `"approvalPolicy": "on-request"`) || !strings.Contains(log, `"approvalsReviewer": "auto_review"`) || taskhead.Working(taskhead.LockPath(e.home, "XR-002")) {
		t.Fatalf("manual binding elevated: %s", log)
	}
}

func TestAppTaskContinuationExternalWriterConflict(t *testing.T) {
	e, c := appTaskEnv(t, "task-check")
	code, got := appTaskStart(t, e, c)
	if code != http.StatusOK {
		t.Fatalf("%d %+v", code, got)
	}
	sid := got["session"]
	waitTaskStopped(t, e, sid)
	appTaskBoard(t, e, "check")
	appMode(t, e, "mute")
	if err := os.WriteFile(filepath.Join(e.proj, "fake-app-enforce-writer-lock"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	st := e.s.chatStoreRead(sid)
	owner, err := e.s.appServerStart(e.s.chatHarnessOf(st.Model, st.Harness), e.proj, binPath(agentctlBin), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.kill()
	if _, err = owner.call("initialize", map[string]any{}); err != nil {
		t.Fatal(err)
	}
	if _, err = owner.call("thread/resume", map[string]any{"threadId": st.Thread}); err != nil {
		t.Fatal(err)
	}
	feed := readFile(t, e.s.appFeedFile(sid))
	for i := 0; i < 2; i++ {
		code, raw := appTaskSay(t, e, c, sid, "полная реплика после чужого writer", "writer-continuation")
		if code != http.StatusConflict || !strings.Contains(raw, "другом клиенте Codex") {
			t.Fatalf("conflict %d %s", code, raw)
		}
		waitTaskStopped(t, e, sid)
		if _, err = owner.call("initialize", map[string]any{}); err != nil {
			t.Fatalf("owner damaged %v", err)
		}
		after := e.s.chatStoreRead(sid)
		if after.Thread != st.Thread || after.Model != st.Model {
			t.Fatalf("identity changed %+v", after)
		}
		log := readFile(t, filepath.Join(e.proj, "fake-app-log"))
		if strings.Count(log, "turn high") != 1 || strings.Count(log, "start ") != 1 {
			t.Fatalf("delivered during conflict %s", log)
		}
		if got := readFile(t, e.s.appFeedFile(sid)); !strings.HasPrefix(got, feed) || strings.Contains(got, "полная реплика после чужого writer") {
			t.Fatalf("history/delivery %s", got)
		}
	}
	owner.kill()
	code, raw := appTaskSay(t, e, c, sid, "полная реплика после чужого writer", "writer-continuation")
	if code != http.StatusOK {
		t.Fatalf("retry %d %s", code, raw)
	}
	code, raw = appTaskSay(t, e, c, sid, "полная реплика после чужого writer", "writer-continuation")
	if code != http.StatusOK || !strings.Contains(raw, `"way":"dup"`) {
		t.Fatalf("dup %d %s", code, raw)
	}
	log := readFile(t, filepath.Join(e.proj, "fake-app-log"))
	if strings.Count(log, "turn high") != 2 || strings.Count(log, "start ") != 1 {
		t.Fatalf("retry lost thread or duplicated %s", log)
	}
}

func TestAppTaskContinuationParallelHandshake(t *testing.T) {
	e, c := appTaskEnv(t, "task-check")
	code, got := appTaskStart(t, e, c)
	if code != http.StatusOK {
		t.Fatalf("%d %+v", code, got)
	}
	sid := got["session"]
	waitTaskStopped(t, e, sid)
	appTaskBoard(t, e, "check")
	appMode(t, e, "resume-barrier")
	type result struct {
		code int
		raw  string
	}
	started := make(chan result, 1)
	go func() {
		code, raw := appTaskSay(t, e, c, sid, "first continuation", "one")
		started <- result{code, raw}
	}()
	deadline := time.After(10 * time.Second)
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		if _, err := os.Stat(filepath.Join(e.proj, "fake-app-handshake")); err == nil {
			break
		}
		select {
		case <-deadline:
			t.Fatal("no resume barrier")
		case <-tick.C:
		}
	}
	before := e.s.chatStoreRead(sid)
	code, raw := appTaskSay(t, e, c, sid, "parallel continuation", "two")
	if code != http.StatusConflict {
		t.Fatalf("parallel %d %s", code, raw)
	}
	if after := e.s.chatStoreRead(sid); after.TaskRunPID != before.TaskRunPID || after.Thread != before.Thread {
		t.Fatalf("parallel changed owner %+v %+v", before, after)
	}
	code, raw = appTaskSay(t, e, c, sid, "first continuation", "one")
	if code != http.StatusOK || !strings.Contains(raw, `"way":"dup"`) {
		t.Fatalf("pending duplicate %d %s", code, raw)
	}
	if err := os.WriteFile(filepath.Join(e.proj, "fake-app-release"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	select {
	case first := <-started:
		if first.code != http.StatusOK {
			t.Fatalf("first lost %d %s", first.code, first.raw)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("first stuck")
	}
	waitTaskStopped(t, e, sid)
	log := readFile(t, filepath.Join(e.proj, "fake-app-log"))
	if strings.Count(log, "turn high") != 2 || strings.Count(log, "resume th-1") != 1 {
		t.Fatalf("duplicate worker %s", log)
	}
}
