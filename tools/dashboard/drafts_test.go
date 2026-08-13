package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Раздел «Черновики»: список накопителя, текст записи и «Оформить». Стенд тот
// же, что у заведения: настоящий taskctl на фикстурной доске, tmux и claude
// исполняемыми фикстурами.

func draftsResp(t *testing.T, c *http.Client, e *testEnv) map[string]any {
	t.Helper()
	resp := doReq(t, c, "GET", e.srv.URL+"/api/projects/demo/drafts", "")
	text := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("накопитель черновиков: %d %s", resp.StatusCode, text)
	}
	var v map[string]any
	if err := json.Unmarshal([]byte(text), &v); err != nil {
		t.Fatalf("ответ накопителя не разобрался: %v\n%s", err, text)
	}
	return v
}

// Накопитель виден списком с ID, первой строкой и возрастом словами утилиты, а
// текст черновика читается целиком: разбирать запись с телефона нельзя, не
// прочитав её.
func TestDraftsListAndText(t *testing.T) {
	e, c, _ := tasksEnv(t)

	empty := draftsResp(t, c, e)
	if list, ok := empty["drafts"].([]any); !ok || len(list) != 0 {
		t.Fatalf("пустой накопитель приехал не пустым списком: %v", empty["drafts"])
	}
	if note, _ := empty["note"].(string); !strings.Contains(note, "пуст") {
		// Пустой список без слов неотличим от неотрисованного раздела.
		t.Errorf("пустой накопитель молчит вместо слов: %v", empty)
	}

	for _, text := range []string{
		"уведомитель шумит из песочницы",
		"дашборд не показывает накопитель черновиков",
	} {
		doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/drafts",
			`{"text": `+strconv.Quote(text)+`}`).Body.Close()
	}
	got := draftsResp(t, c, e)
	list, _ := got["drafts"].([]any)
	if len(list) != 2 {
		t.Fatalf("в накопителе %d черновиков, жду 2: %v", len(list), got)
	}
	first, _ := list[0].(map[string]any)
	if id, _ := first["id"].(string); id != "XR-005" {
		t.Errorf("ID первого черновика %v, жду XR-005", first["id"])
	}
	if title, _ := first["title"].(string); title != "уведомитель шумит из песочницы" {
		t.Errorf("первая строка черновика не приехала: %v", first)
	}
	if words, _ := first["age_words"].(string); words == "" {
		t.Errorf("возраст словами не приехал: %v", first)
	}

	resp := doReq(t, c, "GET", e.srv.URL+"/api/projects/demo/drafts/XR-005", "")
	text := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("текст черновика: %d %s", resp.StatusCode, text)
	}
	if !strings.Contains(text, "уведомитель шумит из песочницы") {
		t.Errorf("текст черновика не приехал: %s", text)
	}
	if !strings.Contains(text, "docs/tasks/drafts/XR-005.md") {
		t.Errorf("путь файла черновика не назван: %s", text)
	}

	resp = doReq(t, c, "GET", e.srv.URL+"/api/projects/demo/drafts/XR-404", "")
	text = body(t, resp)
	if resp.StatusCode != http.StatusNotFound || !strings.Contains(text, "черновика XR-404") {
		t.Fatalf("несуществующий черновик: %d %s, ожидал 404 со словами", resp.StatusCode, text)
	}
}

// «Оформить» поднимает сессию груминга той же механикой, что и конвейер
// задачи: tmux-сессия с headless-сессией конвейера и заказом теми же словами,
// какими груминг просят в чате.
func TestDraftGroomPrompt(t *testing.T) {
	e, c, _ := tasksEnv(t)
	tmuxLog := filepath.Join(e.home, "tmux.log")
	writeTmuxFake(t, e.bin, tmuxLog, "")
	writeScript(t, e.bin, "claude", "exit 0")
	doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/drafts",
		`{"text": "дашборд не показывает накопитель черновиков"}`).Body.Close()

	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/drafts/XR-005/groom", "")
	text := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("груминг черновика: %d %s", resp.StatusCode, text)
	}
	if !strings.Contains(text, `"session":"task-XR-005"`) {
		t.Errorf("имя сессии груминга не то: %s", text)
	}
	want := "new-session -d -s task-XR-005 -c " + e.proj + " claude -p 'Проведи груминг XR-005'"
	if got := readFile(t, tmuxLog); !strings.Contains(got, want) {
		t.Errorf("сессия груминга поднята не так:\n%s\nжду %q", got, want)
	}

	// Поверх живой работы с тем же ID вторая сессия не поднимается: разбирать
	// один черновик двумя агентами нечего.
	writeTmuxFake(t, e.bin, tmuxLog, `task-XR-005\n`)
	resp = doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/drafts/XR-005/groom", "")
	text = body(t, resp)
	if resp.StatusCode != http.StatusConflict || !strings.Contains(text, "уже идёт") {
		t.Fatalf("груминг поверх живой сессии: %d %s, ожидал 409", resp.StatusCode, text)
	}
}

// Раздел «Черновики» на экране: список с текстом по нажатию, «Оформить» и вход
// с доски и с главной.
func TestStaticDraftsSection(t *testing.T) {
	text := readFile(t, filepath.Join("static", "app.js"))
	for _, want := range []string{
		"Черновики",
		"Оформить",
		"async function renderDrafts(",
		"function draftsButton(",
		"async function groomDraft(",
		"async function draftText(",
		"/drafts",
		"груминга",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("в static/app.js нет надписи %q", want)
		}
	}
	// Вход стоит на обоих экранах: черновик пишется с телефона, и разбирать его
	// приходится оттуда же.
	for _, fn := range []string{"function renderBoard(", "function renderHome("} {
		cut := strings.Index(text, fn)
		if cut < 0 {
			t.Fatalf("в static/app.js нет %s", fn)
		}
		part := text[cut:]
		if stop := strings.Index(part, "\n}\n"); stop > 0 {
			part = part[:stop]
		}
		if !strings.Contains(part, "draftsButton(") {
			t.Errorf("в %s нет входа в раздел черновиков", fn)
		}
	}
	// Раздел это свой экран хэша, иначе с телефона на него не сослаться.
	if !strings.Contains(text, `parts[1] === "drafts"`) {
		t.Error("у раздела черновиков нет своего хэша: route его не узнаёт")
	}
	if !strings.Contains(text, "renderDrafts(current.name)") {
		t.Error("экран черновиков не подключён к разбору хэша")
	}
}

// Без входа груминг не поднимается, чужой Origin отбивается до подъёма сессии:
// ручка изменяющая, чужая страница из браузера дотянуться до неё не должна.
func TestDraftGroomAuthAndOrigin(t *testing.T) {
	e, c, _ := tasksEnv(t)
	tmuxLog := filepath.Join(e.home, "tmux.log")
	writeTmuxFake(t, e.bin, tmuxLog, "")
	writeScript(t, e.bin, "claude", "exit 0")
	doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/drafts",
		`{"text": "мысль про накопитель"}`).Body.Close()

	url := e.srv.URL + "/api/projects/demo/drafts/XR-005/groom"
	resp := doReq(t, plainClient(), "POST", url, "")
	if text := body(t, resp); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("груминг без входа: %d %s, ожидал 401", resp.StatusCode, text)
	}
	req, err := http.NewRequest("POST", url, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Origin", "http://evil.example")
	resp, err = c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("груминг с чужим Origin: %d, ожидал 403", resp.StatusCode)
	}
	if got := readFile(t, tmuxLog); strings.Contains(got, "new-session") {
		t.Errorf("отбитый запрос поднял сессию: %s", got)
	}
}

// Черновика нет, оформлять нечего: сессия не поднимается, а причина называется
// словами.
func TestDraftGroomMissing(t *testing.T) {
	e, c, _ := tasksEnv(t)
	tmuxLog := filepath.Join(e.home, "tmux.log")
	writeTmuxFake(t, e.bin, tmuxLog, "")
	writeScript(t, e.bin, "claude", "exit 0")

	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/drafts/XR-404/groom", "")
	text := body(t, resp)
	if resp.StatusCode != http.StatusNotFound || !strings.Contains(text, "оформлять нечего") {
		t.Fatalf("груминг пропавшего черновика: %d %s, ожидал 404 со словами", resp.StatusCode, text)
	}
	if got := readFile(t, tmuxLog); strings.Contains(got, "new-session") {
		t.Errorf("сессия поднялась под пропавший черновик: %s", got)
	}
}

// Ошибки на груминг должны логироваться: чужой Origin
func TestDraftGroomForeignOriginLogged(t *testing.T) {
	e, c, _, lc := runsEnvWithLog(t, "")
	// Пишем черновик
	req, _ := http.NewRequest("POST", e.srv.URL+"/api/projects/demo/drafts",
		strings.NewReader(`{"text": "новая мысль"}`))
	req.Header.Set("Content-Type", "application/json")
	c.Do(req)

	// Груминг с чужим Origin
	req, _ = http.NewRequest("POST", e.srv.URL+"/api/projects/demo/drafts/XR-005/groom", nil)
	req.Header.Set("Origin", "http://evil.example")
	resp, _ := c.Do(req)
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("ожидал 403, получил %d", resp.StatusCode)
	}
	if !lc.contains(t, "чужой Origin 403") {
		t.Errorf("груминг с чужим Origin не залогировался: %v", lc.lines)
	}
}

// Ошибки на груминг должны логироваться: кривой ID
func TestDraftGroomBadIDLogged(t *testing.T) {
	e, c, _, lc := runsEnvWithLog(t, "")
	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/drafts/bad-id/groom", "")
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("ожидал 400, получил %d", resp.StatusCode)
	}
	if !lc.contains(t, "кривой ID 400") {
		t.Errorf("груминг с кривым ID не залогировался: %v", lc.lines)
	}
}

// Ошибки на груминг должны логироваться: пропал черновик
func TestDraftGroomMissingLogged(t *testing.T) {
	e, c, _, lc := runsEnvWithLog(t, "")
	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/drafts/XR-404/groom", "")
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("ожидал 404, получил %d", resp.StatusCode)
	}
	if !lc.contains(t, "файл черновика не найден 404") {
		t.Errorf("груминг пропавшего черновика не залогировался: %v", lc.lines)
	}
}

// Ошибки на груминг должны логироваться: проект не найден
func TestDraftGroomProjectNotFoundLogged(t *testing.T) {
	e, c, _, lc := runsEnvWithLog(t, "")
	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/unknown/drafts/XR-005/groom", "")
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("ожидал 404, получил %d", resp.StatusCode)
	}
	if !lc.contains(t, "груминг отклонён: проект unknown не найден 404") {
		t.Errorf("груминг с неизвестным проектом не залогировался с именем ручки: %v", lc.lines)
	}
}

// Ошибки на груминг должны логироваться: tmux не нашёлся
func TestDraftGroomNoTmuxLogged(t *testing.T) {
	e, c, _, lc := runsEnvWithLog(t, "")

	// Создаём черновик на диск
	draftsDir := filepath.Join(e.proj, "docs", "tasks", "drafts")
	if err := os.MkdirAll(draftsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	draftFile := filepath.Join(draftsDir, "XR-005.md")
	if err := os.WriteFile(draftFile, []byte("новая мысль\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Удалим tmux
	if err := os.Remove(filepath.Join(e.bin, "tmux")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", e.bin)
	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/drafts/XR-005/groom", "")
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("ожидал 502, получил %d", resp.StatusCode)
	}
	if !lc.contains(t, "груминг XR-005 в demo отклонён: tmux не нашёлся 502") {
		t.Errorf("груминг без tmux не залогировался в журнал: %v", lc.lines)
	}
}
