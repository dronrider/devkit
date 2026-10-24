package main

import (
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dronrider/devkit/internal/chat"
	"github.com/dronrider/devkit/internal/mainchat"
)

// Связь цели с её задачами поверх основных чатов (LLD DK-1422, стык с
// DK-1214): лента цели собирается из витков журналом назначений, решение цели
// едет безадресной строкой во входы записей задач, а строка диспетчера задачи
// возвращается адресно голове носителя цели либо ждёт следующего витка во
// «Входящих» файла цели.

// goalTasksFixture это файл цели с нарезкой: раздел «Задачи цели» называет
// XR-004 и XR-003, у XR-002 цели нет.
const goalTasksFixture = `# XR-100: Цель: пробный цикл

## Цель

DoD: стенд отработал.

## Задачи цели

- XR-004: первая задача
- XR-003: вторая задача

## Журнал

- снимок 2026-08-09: week_all 10%
`

func goalWithTasks(t *testing.T, e *testEnv) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(e.proj, "docs", "tasks", "XR-100.md"),
		[]byte(goalTasksFixture), 0o644); err != nil {
		t.Fatal(err)
	}
}

func taskInbox(t *testing.T, e *testEnv, id string) []string {
	t.Helper()
	data := readFile(t, chat.Path(e.proj, chat.TaskName(id)))
	var lines []string
	for _, line := range strings.Split(strings.TrimSpace(data), "\n") {
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

// Лента цели собирается из витков: журнал назначений несёт строку на каждый
// подъём витка, свёртка отдаёт действующего носителя, а отрезки стоят по
// порядку с закрытым прежним витком и открытым действующим. Ручка адреса
// называет и обе стороны связи: у задачи цель, у цели её задачи, и переходы
// работают в обе стороны (LLD DK-1422, решения 1 и 6).
func TestGoalChatCarrierFromTurns(t *testing.T) {
	e, c, _ := messagesEnv(t, "")
	goalWithTasks(t, e)
	stamp := time.Date(2026, 10, 23, 21, 14, 0, 0, time.UTC)
	first := "abab1212-1212-4212-8212-121212121212"
	second := "cdcd3434-3434-4343-8343-343434343434"
	writeMainChat(t, e.home,
		mainchat.Line(stamp, mainchat.Bind{Record: "XR-100", Chat: first, Project: "demo", Source: mainchat.ByWork})+
			mainchat.Line(stamp.Add(time.Minute), mainchat.Bind{Record: "XR-100", Chat: second, Project: "demo", Source: mainchat.ByWork}))
	code, got := taskChatGet(t, c, e.srv.URL+"/api/projects/demo/tasks/XR-100/chat")
	if code != http.StatusOK {
		t.Fatalf("ручка адреса цели ответила %d", code)
	}
	if got.Chat != second || got.Source != mainchat.ByWork {
		t.Fatalf("действующий виток не стал носителем: %+v", got)
	}
	if len(got.Segments) != 2 ||
		got.Segments[0].Chat != first || got.Segments[0].Until == "" ||
		got.Segments[1].Chat != second || got.Segments[1].Until != "" {
		t.Fatalf("отрезки витков собрались неверно: %+v", got.Segments)
	}
	// linkFields это стороны связи в ответе ручки адреса: у задачи цель, у
	// цели задачи. Разбор свежий на каждый запрос, отсутствие поля и пустое
	// значение не различаются.
	type linkFields struct {
		Tasks []string `json:"tasks"`
		Goal  string   `json:"goal"`
	}
	linksOf := func(id string) linkFields {
		t.Helper()
		var links linkFields
		if err := json.Unmarshal([]byte(body(t, doReq(t, c, "GET",
			e.srv.URL+"/api/projects/demo/tasks/"+id+"/chat", ""))), &links); err != nil {
			t.Fatal(err)
		}
		return links
	}
	goalLinks := linksOf("XR-100")
	if len(goalLinks.Tasks) != 2 || goalLinks.Tasks[0] != "XR-004" || goalLinks.Tasks[1] != "XR-003" || goalLinks.Goal != "" {
		t.Fatalf("у цели нет чипов задач: %+v", goalLinks)
	}
	taskLinks := linksOf("XR-004")
	if taskLinks.Goal != "XR-100" || len(taskLinks.Tasks) != 0 {
		t.Fatalf("у задачи нет чипа цели: %+v", taskLinks)
	}
	// Задача вне состава связи не получает: чип вёл бы в чужую цель.
	freeLinks := linksOf("XR-002")
	if freeLinks.Goal != "" || len(freeLinks.Tasks) != 0 {
		t.Fatalf("задача без цели получила связь: %+v", freeLinks)
	}
}

// Решение цели доезжает диспетчерам затронутых задач безадресной строкой во
// вход записи: без списка берутся все задачи цели со строкой доски, строка
// берётся любой сессией дерева задачи (LLD DK-1422, решение 4), и подтверждением
// служит подъём строки подхватом.
func TestGoalDecideDeliversToTaskInputs(t *testing.T) {
	e, c, _ := messagesEnv(t, "")
	goalWithTasks(t, e)
	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/goals/XR-100/decide",
		`{"text": "первая задача закрывается нарезкой"}`)
	text := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("доставка решения: %d %s", resp.StatusCode, text)
	}
	said := "решение цели XR-100: первая задача закрывается нарезкой"
	for _, id := range []string{"XR-004", "XR-003"} {
		lines := taskInbox(t, e, id)
		if len(lines) != 1 || !strings.Contains(lines[0], said) {
			t.Fatalf("вход записи %s не получил решение: %q", id, lines)
		}
		if addressee := chat.Addressee(lines[0]); addressee != "" {
			t.Fatalf("решение уехало адресно %q, а берёт его любая сессия дерева задачи", addressee)
		}
	}
	if got := taskInbox(t, e, "XR-002"); len(got) != 0 {
		t.Fatalf("задача вне состава получила решение: %q", got)
	}
	var out struct {
		Sent []struct {
			ID    string   `json:"id"`
			Trees []string `json:"trees"`
		} `json:"sent"`
	}
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Sent) != 2 {
		t.Fatalf("ответ не назвал обе задачи: %s", text)
	}
	for _, item := range out.Sent {
		if len(item.Trees) != 1 {
			t.Fatalf("дерево доставки задачи %s названо неверно: %+v", item.ID, item.Trees)
		}
	}
}

// Повтор того же решения не помечает задачу отсутствующей: строка уже лежит
// во входе записи, второй раз она не заводится, и подхват заберёт одну. Это
// дедуп-успех, как у relay и ручек чата, а не отказ дерева.
func TestGoalDecideRepeatMarksDup(t *testing.T) {
	e, c, _ := messagesEnv(t, "")
	goalWithTasks(t, e)
	ask := `{"text": "первая задача закрывается нарезкой"}`
	first := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/goals/XR-100/decide", ask)
	if first.StatusCode != http.StatusOK {
		t.Fatalf("первая доставка: %d %s", first.StatusCode, body(t, first))
	}
	second := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/goals/XR-100/decide", ask)
	text := body(t, second)
	if second.StatusCode != http.StatusOK {
		t.Fatalf("повтор доставки: %d %s", second.StatusCode, text)
	}
	var out struct {
		Sent []map[string]any `json:"sent"`
	}
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Sent) != 2 {
		t.Fatalf("повтор не назвал обе задачи: %s", text)
	}
	for _, item := range out.Sent {
		if item["dup"] != true {
			t.Fatalf("повтор не назван дедупом у задачи %v: %s", item["id"], text)
		}
		if _, has := item["note"]; has {
			t.Fatalf("лежащая строка помечена отказом дерева у задачи %v: %s", item["id"], text)
		}
	}
	for _, id := range []string{"XR-004", "XR-003"} {
		if lines := taskInbox(t, e, id); len(lines) != 1 {
			t.Fatalf("повтор завёл вторую строку у %s: %q", id, lines)
		}
	}
}

// Названная вручную задача проверяется членством: решение чужой задаче везла бы
// связь, которой нет, и человек узнал бы об этом молчанием.
func TestGoalDecideRefusesForeignTask(t *testing.T) {
	e, c, _ := messagesEnv(t, "")
	goalWithTasks(t, e)
	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/goals/XR-100/decide",
		`{"text": "так", "tasks": ["XR-002"]}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("чужая задача принялась в доставку: %d %s", resp.StatusCode, body(t, resp))
	}
	if !strings.Contains(body(t, resp), "не из состава цели") {
		t.Fatalf("отказ не называет состав: %s", body(t, resp))
	}
}

// Член нарезки без строки доски решения не получает: диспетчера у него нет,
// и строка во входе ждала бы читателя, которого не будет. Молчаливый пропуск
// названной задачи человек бы не заметил, поэтому отказ.
func TestGoalDecideRefusesTaskWithoutRow(t *testing.T) {
	e, c, _ := messagesEnv(t, "")
	goalWithTasks(t, e)
	doc := filepath.Join(e.proj, "docs", "tasks", "XR-100.md")
	if err := os.WriteFile(doc, []byte(strings.Replace(goalTasksFixture,
		"- XR-003: вторая задача", "- XR-003: вторая задача\n- XR-005: не нарезана", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/goals/XR-100/decide",
		`{"text": "так", "tasks": ["XR-005"]}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("задача без строки доски принялась в доставку: %d %s", resp.StatusCode, body(t, resp))
	}
	if !strings.Contains(body(t, resp), "без строки доски") {
		t.Fatalf("отказ не называет причину: %s", body(t, resp))
	}
	// Без явного списка доставка берёт задачи со строками: не нарезанный
	// член пропускается молча, решать его судьбу позже.
	resp = doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/goals/XR-100/decide",
		`{"text": "так"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("доставка без списка споткнулась о не нарезанного члена: %d %s",
			resp.StatusCode, body(t, resp))
	}
	if got := taskInbox(t, e, "XR-005"); len(got) != 0 {
		t.Fatalf("не нарезанный член получил решение: %q", got)
	}
}

// Строка диспетчера задачи уезжает идущей цели адресно: её берёт только
// названный виток из личного входа сессии, и уезжающей голове она не
// достаётся (LLD DK-1422, решение 4).
func TestTaskRelayToLiveTurn(t *testing.T) {
	e, c, _ := messagesEnv(t, "")
	goalWithTasks(t, e)
	goalLock(t, e.proj, "XR-100", os.Getpid(), goalTurnSID)
	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/tasks/XR-004/relay",
		`{"text": "подтверждаю получение решения"}`)
	text := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("строка диспетчеру цели: %d %s", resp.StatusCode, text)
	}
	var out struct {
		Way     string `json:"way"`
		Session string `json:"session"`
	}
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatal(err)
	}
	if out.Way != "turn" || out.Session != goalTurnSID {
		t.Fatalf("строка не названа адресной витку: %s", text)
	}
	lines := taskInbox(t, e, "XR-004")
	if len(lines) != 0 {
		t.Fatalf("строка диспетчеру легла во вход записи задачи: %q", lines)
	}
	data := readFile(t, chat.Path(e.proj, "sess-"+goalTurnSID))
	if !strings.Contains(data, "от диспетчера задачи XR-004: подтверждаю получение решения") {
		t.Fatalf("вход витка не получил строку:\n%s", data)
	}
	for _, line := range strings.Split(strings.TrimSpace(data), "\n") {
		if line == "" {
			continue
		}
		if got := chat.Addressee(line); got != goalTurnSID {
			t.Fatalf("строка входа витка безадресна (%q): её взял бы любой ход дерева", got)
		}
	}
}

// Стоящей цели строка ложится во «Входящие» файла цели и уезжает коммитом:
// следующий виток прочитает её при подъёме, повтор строки не заводится.
func TestTaskRelayToStoppedGoal(t *testing.T) {
	e, c, gitLog := messagesEnv(t, "")
	goalWithTasks(t, e)
	ask := `{"text": "вопрос о составе: кто закрывает XR-003?"}`
	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/tasks/XR-004/relay", ask)
	text := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("строка стоящей цели: %d %s", resp.StatusCode, text)
	}
	if !strings.Contains(text, `"way":"inbox"`) {
		t.Fatalf("строка не пошла во «Входящие»: %s", text)
	}
	doc := readFile(t, filepath.Join(e.proj, "docs", "tasks", "XR-100.md"))
	if !strings.Contains(doc, ", от задачи XR-004: вопрос о составе: кто закрывает XR-003?") {
		t.Fatalf("во «Входящих» нет строки от задачи:\n%s", doc)
	}
	git := readFile(t, gitLog)
	if !strings.Contains(git, "docs(tasks): XR-100 строка от задачи XR-004 во «Входящие»") {
		t.Fatalf("строка не уехала коммитом:\n%s", git)
	}
	// Повтор той же строки не заводит вторую: виток прочитал бы вопрос дважды.
	resp = doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/tasks/XR-004/relay", ask)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body(t, resp), `"dup":true`) {
		t.Fatalf("повтор строки не распознан: %d %s", resp.StatusCode, body(t, resp))
	}
	if n := strings.Count(readFile(t, filepath.Join(e.proj, "docs", "tasks", "XR-100.md")), "вопрос о составе"); n != 1 {
		t.Fatalf("вопрос лег %d раз, жду один", n)
	}
}

// Задача без цели строки не отправляет: прямой вопрос остаётся в чате задачи,
// и отказ называет это словами.
func TestTaskRelayWithoutGoal(t *testing.T) {
	e, c, _ := messagesEnv(t, "")
	goalWithTasks(t, e)
	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/tasks/XR-002/relay",
		`{"text": "есть вопрос"}`)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("задача без цели отправила строку: %d %s", resp.StatusCode, body(t, resp))
	}
	if !strings.Contains(body(t, resp), "ни в одной цели") {
		t.Fatalf("отказ не называет причину: %s", body(t, resp))
	}
}

// TestStaticGoalMainWay: как панель стоит на носителе цели из журнала и куда
// уезжают строки связи. Стенд testdata/poc_goalmain.mjs над настоящим app.js
// проверяет носителя из журнала у идущего и стоящего цикла, чипы перехода и
// обе строки действий. Без node шаг пропускается.
func TestStaticGoalMainWay(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node не найден: стенд связи цели с задачами пропущен")
	}
	out, err := exec.Command(node, filepath.Join("testdata", "poc_goalmain.mjs"),
		filepath.Join("static", "app.js")).CombinedOutput()
	if err != nil {
		t.Fatalf("связь цели с задачами в панели: %v\n%s", err, out)
	}
	t.Log(strings.TrimSpace(string(out)))
}
