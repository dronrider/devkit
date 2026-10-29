package main

import (
	"net/http"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Правка срока идёт той же ручкой строки, что и остальные ячейки (DK-1464):
// дашборд передаёт дату в taskctl set --due, утилита ставит суффикс и
// валидирует календарь, ответ ручки задачи несёт свежее поле due. Прочерк
// снимает срок, кривая дата отбивается отказом утилиты.
func TestTaskPatchDue(t *testing.T) {
	e, c, _ := tasksEnv(t)

	resp := doReq(t, c, "PATCH", taskURL(e, "XR-002", ""), `{"due": "2026-11-05"}`)
	if text := body(t, resp); resp.StatusCode != http.StatusOK {
		t.Fatalf("правка срока: %d %s", resp.StatusCode, text)
	}
	task := getTask(t, c, e, "XR-002")
	if got := taskRowField(t, task, "due"); got != "2026-11-05" {
		t.Errorf("срок не встал в ответ ручки задачи: %v", got)
	}

	resp = doReq(t, c, "PATCH", taskURL(e, "XR-002", ""), `{"due": "завтра"}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("кривая дата прошла: %d %s", resp.StatusCode, body(t, resp))
	}

	resp = doReq(t, c, "PATCH", taskURL(e, "XR-002", ""), `{"due": "-"}`)
	if text := body(t, resp); resp.StatusCode != http.StatusOK {
		t.Fatalf("снятие срока: %d %s", resp.StatusCode, text)
	}
	task = getTask(t, c, e, "XR-002")
	if got := taskRowField(t, task, "due"); got != nil {
		t.Errorf("срок не снялся: %v", got)
	}
}

// Блок срока и контроля открыт на форме задачи сразу, без карандаша: срок,
// спрятанный за режимом правки, и контроль отдельной карточкой без стиля
// нашёл человек на свежем выкате (DK-1503, находки 4-6). Стенд поднимает
// статику в node с игрушечным DOM (testdata/poc_taskdue.mjs) и держит блок
// одним, с пикером даты, общим стилем полей и правкой без входа в правку.
func TestTaskDueBlockOpen(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node не найден: стенд блока срока пропущен")
	}
	out, err := exec.Command(node, filepath.Join("testdata", "poc_taskdue.mjs"),
		filepath.Join("static", "app.js")).CombinedOutput()
	if err != nil {
		t.Fatalf("блок срока и контроля: %v\n%s", err, out)
	}
	t.Log(strings.TrimSpace(string(out)))
}
