package main

import (
	"encoding/json"
	"testing"
)

// TestBoardRunsKeepsTaskctlStage: строка этапа приходит готовой из taskctl
// list --json, и разметка строк дашборда её не трогает: ни гасит у строки без
// работы, ни считает заново (DK-910). Брошенную строку называет само поле.
func TestBoardRunsKeepsTaskctlStage(t *testing.T) {
	raw := json.RawMessage(`{"prefix":"XR","sections":[{"key":"in-progress","rows":[
		{"id":"XR-001","title":"Живая","sect":"in-progress","stage":"ревью","stage_since":1755252000,"stage_round":2,"stage_age":"12 минут","stage_session":"сессия жива"},
		{"id":"XR-002","title":"Оборванная","sect":"in-progress","stage":"разработка","stage_since":1755234000,"stage_round":1,"stage_age":"5 часов","stage_session":"сессии нет, брошена"},
		{"id":"XR-003","title":"Без этапа","sect":"in-progress"}]}]}`)
	works := []Work{{ID: "XR-001", Kind: "task", Via: "tmux"}}
	var doc struct {
		Sections []struct {
			Rows []boardRow `json:"rows"`
		} `json:"sections"`
	}
	if err := json.Unmarshal(boardRuns(raw, works, nil, nil), &doc); err != nil {
		t.Fatal(err)
	}
	rows := map[string]boardRow{}
	for _, sec := range doc.Sections {
		for _, row := range sec.Rows {
			rows[row.ID] = row
		}
	}
	live := rows["XR-001"]
	if live.Run != "tmux" || live.Stage != "ревью" || live.StageSince != 1755252000 || live.StageRound != 2 || live.StageSession != "сессия жива" {
		t.Fatalf("живая строка: %+v", live)
	}
	gone := rows["XR-002"]
	if gone.Run != "" || gone.Stage != "разработка" || gone.StageAge != "5 часов" || gone.StageSession != "сессии нет, брошена" {
		t.Fatalf("брошенная строка: %+v", gone)
	}
	if bare := rows["XR-003"]; bare.Stage != "" || bare.StageSession != "" {
		t.Fatalf("строка без этапа получила поля: %+v", bare)
	}
}
