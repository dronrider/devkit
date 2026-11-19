package main

import (
	"encoding/json"
	"net/http"
	"testing"
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
