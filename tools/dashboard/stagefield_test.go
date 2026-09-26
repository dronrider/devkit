package main

import (
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestBoardRunsKeepsTaskctlStage: строка этапа приходит готовой из taskctl
// list --json, и разметка строк дашборда её не трогает: ни гасит у строки без
// работы, ни считает заново (DK-910). Брошенную строку называет само поле.
func TestBoardRunsKeepsTaskctlStage(t *testing.T) {
	raw := json.RawMessage(`{"prefix":"XR","sections":[{"key":"in-progress","rows":[
		{"id":"XR-001","title":"Живая","sect":"in-progress","stage":"ревью","stage_since":1755252000,"stage_round":2,"stage_age":"12 минут","stage_session":"сессия жива"},
		{"id":"XR-002","title":"Оборванная","sect":"in-progress","stage":"разработка","stage_since":1755234000,"stage_round":1,"stage_age":"5 часов","stage_session":"сессии нет, брошена"},
		{"id":"XR-003","title":"Без этапа","sect":"in-progress"},
		{"id":"XR-004","title":"Ждёт","sect":"in-progress","stage":"ждёт человека","stage_since":1755248400,"stage_age":"3 часа","stage_at":"проверка"}]}]}`)
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
	// Ожидание не несёт своей сессии, а stage_at называет этап работы, на
	// котором задача встала (DK-1119): его лента строки списка и подсвечивает
	// вместо собственного деления, которого у ожиданий нет.
	wait := rows["XR-004"]
	if wait.Stage != "ждёт человека" || wait.StageAt != "проверка" || wait.StageSession != "" {
		t.Fatalf("ожидание: %+v", wait)
	}
}

// TestStaticStageMark: колонка хода строки списка и шапка со степпером формы
// (DK-1119, макет «14 Этап задачи, ход 2», варианты 2a и 2b) различают четыре
// состояния (живая сессия, молчащая, брошенная, ожидание) цветом словаря и
// подсказкой, без приписки слов о сессии видимым текстом; строка без записи
// этапа колонку не ломает. Сторожит стенд testdata/poc_stagemark.mjs.
func TestStaticStageMark(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node не найден: стенд этапа задачи пропущен")
	}
	out, err := exec.Command(node, filepath.Join("testdata", "poc_stagemark.mjs"),
		filepath.Join("static", "app.js")).CombinedOutput()
	if err != nil {
		t.Fatalf("этап задачи в строке и на форме: %v\n%s", err, out)
	}
	t.Log(strings.TrimSpace(string(out)))
}
