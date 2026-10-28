package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dronrider/devkit/internal/checkrun"
)

// Контроль задачи по сроку (DK-1470) на носителе дашборда: строка работы с
// контролем в окне срока получает инспектора тем же отбором, что тик сторожка,
// а кнопка «дожать» зовёт ту же дорогу HTTP-ручкой press.

// controlBoardJSON это доска стенда: XR-004 в работе со сроком, который давно
// прошёл, и контролем за два дня до него. Окно контроля, открывшись, уже не
// закрывается, поэтому фикстура статична и от дня прогона не зависит.
const controlBoardJSON = `{"prefix":"XR","sections":[` +
	`{"key":"in-progress","title":"In progress","rows":[` +
	`{"id":"XR-100","title":"Цель: пробный цикл","type":"task","p":"P2","r":41,"r_parts":[25,9,3,0,4],"cost":"XL","link":"-"},` +
	`{"id":"XR-004","title":"Начатая задача","type":"task","p":"P2","r":31,"r_parts":[25,3,1,0,2],"cost":"-","link":"-","due":"2026-01-01","control":2}]},` +
	`{"key":"check","title":"Check","rows":[]},` +
	`{"key":"backlog","title":"Backlog","rows":[{"id":"XR-002","title":"Обычная задача","type":"task","p":"P2","r":30,"r_parts":[25,2,1,0,2],"cost":"-","link":"-"}]}]}`

// controlEnv это стенд запуска с доской контроля: права, обвязка выката и
// фикстуры подписок те же, что у прогона сценария.
func controlEnv(t *testing.T) (*testEnv, string) {
	t.Helper()
	e, _, tmuxLog := runsEnv(t, "")
	writeScript(t, e.bin, "taskctl", fmt.Sprintf("echo '%s'", controlBoardJSON))
	writeDeployLocal(t, e.proj, "deploy = true\nautonomous = true\n")
	writePermsFake(t, filepath.Dir(e.proj), false)
	return e, tmuxLog
}

// controlOne поднимает инспектора контроля по одной строке на стенде.
func controlOne(t *testing.T, e *testEnv, id string) checkrun.Report {
	t.Helper()
	raw, err := e.s.projectBoard(e.proj)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := parseBoardRows(raw)
	if err != nil {
		t.Fatal(err)
	}
	return e.s.controlRunsOf(&Project{Name: "demo", Path: e.proj}, rows, []string{id})[0]
}

// Просроченный контроль поднимает инспектора в то же окно task-<ID>, что и
// прогон сценария, с заказом контроля: риски черновиком, напоминание
// исполнителю, потолки DK-400 словами и дожим просрочки.
func TestControlRunRaisesInspector(t *testing.T) {
	e, tmuxLog := controlEnv(t)

	rep := controlOne(t, e, "XR-004")
	if !rep.Raised || rep.Failed {
		t.Fatalf("инспектор контроля не поднят: %+v", rep)
	}
	if !strings.Contains(rep.Line, "task-XR-004") {
		t.Errorf("отчёт не называет сессию: %q", rep.Line)
	}
	got := readFile(t, tmuxLog)
	for _, want := range []string{
		"new-session -d -s task-XR-004",
		checkrun.ControlOrderHead + "XR-004",
		`taskctl draft --prio high "Риски срыва сроков по XR-004`,
		"taskctl remind XR-004",
		"Потолки DK-400 объезжать нельзя",
		"Срок просрочен",
		hiddenEnv,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("в заказе контроля нет %q: %s", want, got)
		}
	}
}

// Контроль вне окна и строка без контроля поднимают нечего, и это не поломка:
// окно решает общий отбор, а не кнопка.
func TestControlRunSkipsWhatNeedsNoLift(t *testing.T) {
	e, tmuxLog := controlEnv(t)
	writeScript(t, e.bin, "taskctl",
		fmt.Sprintf("echo '%s'", strings.Replace(controlBoardJSON, "2026-01-01", "2999-01-01", 1)))

	rep := controlOne(t, e, "XR-004")
	if rep.Raised || rep.Failed {
		t.Fatalf("окно закрыто, а подъём пошёл: %+v", rep)
	}
	if !strings.Contains(rep.Line, "окно контроля ещё не настало") {
		t.Errorf("отчёт не назвал закрытое окно: %q", rep.Line)
	}

	rep = controlOne(t, e, "XR-002")
	if rep.Raised || rep.Failed {
		t.Fatalf("строка без контроля подняла инспектора: %+v", rep)
	}
	if !strings.Contains(rep.Line, "строка без контроля") {
		t.Errorf("отчёт не назвал отсутствие контроля: %q", rep.Line)
	}
	if got := readFile(t, tmuxLog); strings.Contains(got, "new-session") {
		t.Errorf("сессия поднята там, где поднимать нечего: %s", got)
	}
}

// Кнопка «дожать» зовёт ту же дорогу подъёма: ручка press поднимает инспектора
// по строке на контроле и отвечает словами отчёта, а строке без контроля
// отказывает словами, а не пустотой.
func TestTaskPressLiftsInspector(t *testing.T) {
	e, tmuxLog := controlEnv(t)
	c := e.loggedClient(t)

	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/tasks/XR-004/press", "")
	text := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("дожим: %d %s", resp.StatusCode, text)
	}
	var got struct {
		ID      string `json:"id"`
		Raised  bool   `json:"raised"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal([]byte(text), &got); err != nil {
		t.Fatal(err)
	}
	if got.ID != "XR-004" || !got.Raised || !strings.Contains(got.Message, "task-XR-004") {
		t.Fatalf("ответ дожима: %+v", got)
	}
	if log := readFile(t, tmuxLog); !strings.Contains(log, "new-session -d -s task-XR-004") {
		t.Errorf("инспектор по кнопке не поднят: %s", log)
	}

	resp = doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/tasks/XR-002/press", "")
	text = body(t, resp)
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(text, "не на контроле") {
		t.Fatalf("дожим строки без контроля: %d %s", resp.StatusCode, text)
	}
}
