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
)

func TestAppPlanPersistenceAndOwnership(t *testing.T) {
	e, c := appEnv(t)
	sid := appStartChat(t, e, c, "первый чат")
	other := appStartChat(t, e, c, "второй чат")
	appWaitBusy(t, e, c, sid, false)
	appWaitBusy(t, e, c, other, false)
	update := func(id, thread, turn, plan string) {
		t.Helper()
		e.s.appNote(id, "turn/plan/updated", json.RawMessage(`{"threadId":"`+thread+`","turnId":"`+turn+`","plan":`+plan+`}`))
	}
	const first = `[{"step":"Собрать факты","status":"completed"},{"step":"Исправить","status":"inProgress"},{"step":"Проверить","status":"pending"}]`
	update(sid, "th-1", "turn-1", first)
	update(other, "th-1", "turn-1", `[{"step":"Другая работа","status":"pending"}]`)
	get := func(id string) Pulse {
		t.Helper()
		resp := doReq(t, c, "GET", e.srv.URL+"/api/projects/demo/pulse?sid="+id, "")
		raw := body(t, resp)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("pulse %d %s", resp.StatusCode, raw)
		}
		var p Pulse
		if err := json.Unmarshal([]byte(raw), &p); err != nil {
			t.Fatal(err)
		}
		return p
	}
	p := get(sid)
	if len(p.Plan) != 3 || p.Plan[0].Text != "Собрать факты" || p.Plan[0].State != "completed" || p.Plan[1].State != "in_progress" || p.Plan[2].State != "pending" {
		t.Fatalf("план: %+v", p.Plan)
	}
	update(sid, "child-thread", "turn-1", `[]`)
	update(sid, "th-1", "old-turn", `[]`)
	if got := get(sid); len(got.Plan) != 3 {
		t.Fatalf("чужое событие заменило план: %+v", got)
	}
	if got := get(other); len(got.Plan) != 1 || got.Plan[0].Text != "Другая работа" {
		t.Fatalf("планы смешались: %+v", got)
	}
	update(sid, "th-1", "turn-1", `[{"step":"Готово","status":"completed"}]`)
	if got := get(sid); len(got.Plan) != 1 || got.Plan[0].Text != "Готово" {
		t.Fatalf("обновление: %+v", got)
	}
	// Новый сервер читает последнее состояние с диска без живого процесса.
	restarted := &server{cfg: e.cfg}
	if got := restarted.appPlanRead(sid); len(got) != 1 || got[0].Text != "Готово" {
		t.Fatalf("после рестарта: %+v", got)
	}
}

func TestAppPlanReadDiagnostics(t *testing.T) {
	var logs []string
	s := &server{cfg: &Config{Home: t.TempDir()}, logf: func(format string, args ...any) {
		logs = append(logs, fmt.Sprintf(format, args...))
	}}
	const sid = "as-plan-test"
	path := s.appPlanPath(sid)
	if got := s.appPlanRead(sid); len(got) != 0 || len(logs) != 0 {
		t.Fatalf("отсутствующий план: %+v, журнал: %v", got, logs)
	}
	check := func(cause string) {
		t.Helper()
		logs = nil
		got := s.appPlanRead(sid)
		if len(got) != 1 || got[0].Src != planSrcErr || got[0].Text != "План Codex не прочитался" {
			t.Fatalf("состояние кольца: %+v", got)
		}
		if len(logs) != 1 || !strings.Contains(logs[0], sid) || !strings.Contains(logs[0], cause) {
			t.Fatalf("в журнале нет sid и причины %q: %v", cause, logs)
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	check("unexpected end of JSON input")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	// Каталог вместо файла гарантирует отказ чтения и при запуске от root.
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}
	_, readErr := os.ReadFile(path)
	if readErr == nil {
		t.Fatal("стенд не вызвал ошибку чтения")
	}
	check(readErr.Error())
}

func TestAppPlanWaitsForConfirmedTurn(t *testing.T) {
	for _, order := range []string{"notice-first", "response-first"} {
		t.Run(order, func(t *testing.T) {
			e, client := appEnv(t)
			const fixture = `
        if mode() in ("notice-first", "response-first"):
            open("plan-turn-requested", "w").close()
            while not os.path.exists("plan-turn-release"): time.sleep(0.01)
            turn = {"id": "turn-2"}
            result = {"id": mid, "result": {"turn": turn}}
            started = {"threadId": "th-1", "turn": turn}
            plan = {"threadId": "th-1", "turnId": "turn-2", "plan": [{"step": "new plan", "status": "completed"}]}
            if mode() == "notice-first":
                note("turn/started", started)
                note("turn/plan/updated", plan)
                emit(result)
            else:
                emit(result)
                note("turn/plan/updated", plan)
                note("turn/started", started)
            note("turn/completed", {"threadId": "th-1", "turn": {"id": "turn-2", "status": "completed"}})
            continue
`
			anchor := `        if mode() == "rejected-model":`
			script := strings.Replace(appClientPy, anchor, fixture+anchor, 1)
			if err := os.WriteFile(filepath.Join(e.bin, "клиент-app.py"), []byte(script), 0o644); err != nil {
				t.Fatal(err)
			}
			sid := appStartChat(t, e, client, "first turn")
			appWaitBusy(t, e, client, sid, false)
			live := e.s.appLive(sid)
			notePlan := func(turn, step string) {
				e.s.appNote(sid, "turn/plan/updated", json.RawMessage(fmt.Sprintf(
					`{"threadId":"th-1","turnId":%q,"plan":[{"step":%q,"status":"completed"}]}`, turn, step)))
			}
			assertPlan := func(want string) {
				t.Helper()
				got := e.s.appPlanRead(sid)
				if len(got) != 1 || got[0].Text != want {
					t.Errorf("план %+v, жду %q", got, want)
				}
			}
			notePlan("turn-1", "first plan")
			assertPlan("first plan")
			appMode(t, e, order)
			done := make(chan error, 1)
			go func() { done <- e.s.appTurn(live, "second turn", false) }()
			release := func() { putDoc(t, e.proj, "plan-turn-release", "") }
			t.Cleanup(release)
			request := filepath.Join(e.proj, "plan-turn-requested")
			deadline := time.After(5 * time.Second)
			tick := time.NewTicker(10 * time.Millisecond)
			defer tick.Stop()
			for !isFile(request) {
				select {
				case <-tick.C:
				case <-deadline:
					t.Fatal("клиент не получил второй turn/start")
				}
			}
			e.s.appNote(sid, "turn/started", json.RawMessage(`{"threadId":"th-1","turn":{"id":"turn-1"}}`))
			e.s.appNote(sid, "turn/started", json.RawMessage(`{"threadId":"other","turn":{"id":"turn-2"}}`))
			notePlan("turn-1", "late old plan")
			notePlan("turn-2", "not confirmed")
			assertPlan("first plan")
			release()
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			if _, err := live.proc.call("initialize", nil); err != nil {
				t.Fatal(err)
			}
			assertPlan("new plan")
			notePlan("turn-1", "late again")
			assertPlan("new plan")
		})
	}
}
