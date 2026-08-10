package main

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Доска для тестов запуска: цель XR-100 (заголовок от слова «Цель:») и
// одиночная задача XR-002.
const runsBoardJSON = `{"prefix":"XR","sections":[` +
	`{"key":"in-progress","title":"In progress","rows":[{"id":"XR-100","title":"Цель: пробный цикл","type":"task","p":"P2","r":41,"r_parts":[25,9,3,0,4],"cost":"XL","link":"-"}]},` +
	`{"key":"backlog","title":"Backlog","rows":[{"id":"XR-002","title":"Обычная задача","type":"task","p":"P2","r":30,"r_parts":[25,2,1,0,2],"cost":"-","link":"-"}]}]}`

// writeTmuxFake кладёт фикстуру tmux: пишет каждый вызов в журнал, на ls
// отвечает списком сессий; пустой список это ненулевой код, как у живого tmux
// без своего сервера.
func writeTmuxFake(t *testing.T, bin, logPath, sessions string) {
	t.Helper()
	body := fmt.Sprintf("echo \"$@\" >> %q\ncase \"$1\" in\nls)\n", logPath)
	if sessions == "" {
		body += "  exit 1;;\nesac\nexit 0"
	} else {
		body += fmt.Sprintf("  printf '%s';;\nesac\nexit 0", sessions)
	}
	writeScript(t, bin, "tmux", body)
}

// writeGoalRunFake кладёт фикстуру оболочки goal-run.py в синтетический
// чекаут devkit внутри корня конфига.
func writeGoalRunFake(t *testing.T, root, pyBody string) {
	t.Helper()
	dir := filepath.Join(root, "devkit", "kit", "skills", "goal-loop")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "goal-run.py"), []byte(pyBody), 0o755); err != nil {
		t.Fatal(err)
	}
}

// goalRunOKBody отвечает как настоящая оболочка при удачном подъёме цикла.
func goalRunOKBody(callsLog string) string {
	return fmt.Sprintf(`import sys
with open(%q, "a") as f:
    f.write(" ".join(sys.argv[1:]) + "\n")
print("цикл цели %%s поднят в tmux-сессии goal-%%s" %% (sys.argv[1], sys.argv[1]))
`, callsLog)
}

// runsEnv поднимает окружение с доской runsBoardJSON, журналируемым tmux и
// фикстурой claude в PATH.
func runsEnv(t *testing.T, sessions string) (*testEnv, *http.Client, string) {
	t.Helper()
	e := newTestEnv(t)
	writeScript(t, e.bin, "taskctl", fmt.Sprintf("echo '%s'", runsBoardJSON))
	tmuxLog := filepath.Join(e.home, "tmux.log")
	writeTmuxFake(t, e.bin, tmuxLog, sessions)
	writeScript(t, e.bin, "claude", "exit 0")
	return e, e.loggedClient(t), tmuxLog
}

func doReq(t *testing.T, c *http.Client, method, url, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(data)
}

// Запуск цели зовёт оболочку goal-run.py с ID и корнем проекта: тот же
// механизм, что и руками, своего подъёма цикла у дашборда нет.
func TestRunStartGoal(t *testing.T) {
	e, c, _ := runsEnv(t, "")
	callsLog := filepath.Join(e.home, "goal-run.calls")
	writeGoalRunFake(t, filepath.Dir(e.proj), goalRunOKBody(callsLog))

	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/runs", `{"id": "XR-100"}`)
	text := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("запуск цели: %d %s", resp.StatusCode, text)
	}
	for _, want := range []string{`"kind":"goal"`, `"session":"goal-XR-100"`, "поднят"} {
		if !strings.Contains(text, want) {
			t.Errorf("в ответе запуска нет %q: %s", want, text)
		}
	}
	got := readFile(t, callsLog)
	if !strings.Contains(got, "XR-100 -C "+e.proj) {
		t.Errorf("goal-run позван не так: %q", got)
	}
}

// Одиночная задача поднимается tmux-сессией task-<ID> с headless-сессией
// конвейера: claude -p с промптом про скиллы доски, из корня проекта.
func TestRunStartTask(t *testing.T) {
	e, c, tmuxLog := runsEnv(t, "")

	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/runs", `{"id": "XR-002"}`)
	text := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("запуск задачи: %d %s", resp.StatusCode, text)
	}
	for _, want := range []string{`"kind":"task"`, `"session":"task-XR-002"`} {
		if !strings.Contains(text, want) {
			t.Errorf("в ответе запуска нет %q: %s", want, text)
		}
	}
	got := readFile(t, tmuxLog)
	// Промпт уходит одной заквоченной строкой: tmux склеивает хвост
	// new-session пробелами и отдаёт шеллу.
	want := "new-session -d -s task-XR-002 -c " + e.proj +
		" claude -p 'возьми задачу XR-002 в работу и доведи её конвейером по скиллам board-task и board-ship'"
	if !strings.Contains(got, want) {
		t.Errorf("tmux позван не так:\n%s\nожидал вхождение %q", got, want)
	}
}

// Строки нет на доске, значит и запускать нечего: 404 с именем доски.
func TestRunStartUnknownRow(t *testing.T) {
	e, c, _ := runsEnv(t, "")
	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/runs", `{"id": "XR-999"}`)
	text := body(t, resp)
	if resp.StatusCode != http.StatusNotFound || !strings.Contains(text, "нет строки XR-999") {
		t.Fatalf("запуск несуществующей строки: %d %s", resp.StatusCode, text)
	}
}

// Живая tmux-сессия той же работы это конфликт, а не второй запуск поверх.
func TestRunStartAlreadyRunning(t *testing.T) {
	e, c, _ := runsEnv(t, `task-XR-002\ngoal-XR-100\n`)
	for _, id := range []string{"XR-002", "XR-100"} {
		resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/runs", fmt.Sprintf(`{"id": %q}`, id))
		text := body(t, resp)
		if resp.StatusCode != http.StatusConflict || !strings.Contains(text, "уже идёт") {
			t.Errorf("запуск %s поверх живой сессии: %d %s, ожидал 409 про «уже идёт»", id, resp.StatusCode, text)
		}
	}
}

// Код 3 у оболочки это занятый замок: конфликт со словами самой оболочки, а
// не безликая ошибка.
func TestRunStartGoalBusyLock(t *testing.T) {
	e, c, _ := runsEnv(t, "")
	writeGoalRunFake(t, filepath.Dir(e.proj), `import sys
sys.stderr.write("цикл цели XR-100 уже идёт, замок .devkit/goal-XR-100.lock\n")
sys.exit(3)
`)
	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/runs", `{"id": "XR-100"}`)
	text := body(t, resp)
	if resp.StatusCode != http.StatusConflict || !strings.Contains(text, "замок") {
		t.Fatalf("занятый замок: %d %s, ожидал 409 со словами про замок", resp.StatusCode, text)
	}
}

// Оболочки нет ни в одном корне, и это названная ошибка, а не молчание: без
// чекаута devkit цикл цели поднимать нечем.
func TestRunStartGoalRunMissing(t *testing.T) {
	e, c, _ := runsEnv(t, "")
	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/runs", `{"id": "XR-100"}`)
	text := body(t, resp)
	if resp.StatusCode != http.StatusBadGateway || !strings.Contains(text, "goal-run.py не нашёлся") {
		t.Fatalf("без goal-run: %d %s, ожидал 502 с именем пропажи", resp.StatusCode, text)
	}
}

// Ненайденный tmux называется и на запуске: сессию поднимать нечем.
func TestRunStartNoTmux(t *testing.T) {
	e, c, _ := runsEnv(t, "")
	if err := os.Remove(filepath.Join(e.bin, "tmux")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", e.bin)
	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/runs", `{"id": "XR-002"}`)
	text := body(t, resp)
	if resp.StatusCode != http.StatusBadGateway || !strings.Contains(text, "tmux не нашёлся") {
		t.Fatalf("без tmux: %d %s", resp.StatusCode, text)
	}
}

// Ненайденный claude ловится до подъёма сессии: сессия с ненайденной
// командой умерла бы молча, и стоп был бы неотличим от запуска.
func TestRunStartNoClaude(t *testing.T) {
	e, c, _ := runsEnv(t, "")
	if err := os.Remove(filepath.Join(e.bin, "claude")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", e.bin)
	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/runs", `{"id": "XR-002"}`)
	text := body(t, resp)
	if resp.StatusCode != http.StatusBadGateway || !strings.Contains(text, "claude не нашёлся") {
		t.Fatalf("без claude: %d %s", resp.StatusCode, text)
	}
}

// Стоп цели: строка про стоп уходит в журнал цикла через goal-run --say, tmux
// снимает сессию, ответ называет состояние стопом.
func TestRunStopGoal(t *testing.T) {
	e, c, tmuxLog := runsEnv(t, `goal-XR-100\n`)
	callsLog := filepath.Join(e.home, "goal-run.calls")
	writeGoalRunFake(t, filepath.Dir(e.proj), goalRunOKBody(callsLog))

	resp := doReq(t, c, "DELETE", e.srv.URL+"/api/projects/demo/runs/XR-100", "")
	text := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("стоп цели: %d %s", resp.StatusCode, text)
	}
	for _, want := range []string{`"state":"стоп"`, `"session":"goal-XR-100"`, "новый запуск"} {
		if !strings.Contains(text, want) {
			t.Errorf("в ответе стопа нет %q: %s", want, text)
		}
	}
	if got := readFile(t, callsLog); !strings.Contains(got, "--say стоп из дашборда") {
		t.Errorf("строка про стоп не ушла в журнал цикла: %q", got)
	}
	if got := readFile(t, tmuxLog); !strings.Contains(got, "kill-session -t =goal-XR-100") {
		t.Errorf("tmux-сессия цели не снята: %s", got)
	}
}

// Провал записи строки про стоп не отменяет сам стоп, но и не молчит: в
// ответе появляется note с причиной.
func TestRunStopGoalSayFailureNamed(t *testing.T) {
	e, c, tmuxLog := runsEnv(t, `goal-XR-100\n`)
	writeGoalRunFake(t, filepath.Dir(e.proj), `import sys
sys.stderr.write("журнал цели не пишется\n")
sys.exit(2)
`)
	resp := doReq(t, c, "DELETE", e.srv.URL+"/api/projects/demo/runs/XR-100", "")
	text := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("стоп при сломанном --say: %d %s", resp.StatusCode, text)
	}
	if !strings.Contains(text, "журнал цели не пишется") {
		t.Errorf("провал записи строки про стоп остался безымянным: %s", text)
	}
	if got := readFile(t, tmuxLog); !strings.Contains(got, "kill-session -t =goal-XR-100") {
		t.Errorf("сессия не снята: %s", got)
	}
}

// Стоп одиночной задачи снимает её tmux-сессию.
func TestRunStopTask(t *testing.T) {
	e, c, tmuxLog := runsEnv(t, `task-XR-002\n`)
	resp := doReq(t, c, "DELETE", e.srv.URL+"/api/projects/demo/runs/XR-002", "")
	text := body(t, resp)
	if resp.StatusCode != http.StatusOK || !strings.Contains(text, `"state":"стоп"`) {
		t.Fatalf("стоп задачи: %d %s", resp.StatusCode, text)
	}
	if got := readFile(t, tmuxLog); !strings.Contains(got, "kill-session -t =task-XR-002") {
		t.Errorf("tmux-сессия задачи не снята: %s", got)
	}
}

// Цель из реестра без tmux-сессии ведётся снаружи (живой чат): дашборд её не
// убивает и говорит об этом словами.
func TestRunStopGoalLedOutside(t *testing.T) {
	e, c, _ := runsEnv(t, "")
	resp := doReq(t, c, "DELETE", e.srv.URL+"/api/projects/demo/runs/XR-112", "")
	text := body(t, resp)
	if resp.StatusCode != http.StatusConflict || !strings.Contains(text, "ведётся снаружи") {
		t.Fatalf("стоп чужого цикла: %d %s, ожидал 409 про «ведётся снаружи»", resp.StatusCode, text)
	}
}

// Работы нет ни в tmux, ни в реестре: 404, а не тихий «ок».
func TestRunStopNotRunning(t *testing.T) {
	e, c, _ := runsEnv(t, "")
	resp := doReq(t, c, "DELETE", e.srv.URL+"/api/projects/demo/runs/XR-002", "")
	text := body(t, resp)
	if resp.StatusCode != http.StatusNotFound || !strings.Contains(text, "не идёт") {
		t.Fatalf("стоп неидущей работы: %d %s", resp.StatusCode, text)
	}
}

// Без входа запуск и стоп недоступны, как и всё API.
func TestRunsRequireLogin(t *testing.T) {
	e, _, _ := runsEnv(t, "")
	c := plainClient()
	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/runs", `{"id": "XR-002"}`)
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("запуск без входа: %d, ожидал 401", resp.StatusCode)
	}
	resp = doReq(t, c, "DELETE", e.srv.URL+"/api/projects/demo/runs/XR-002", "")
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("стоп без входа: %d, ожидал 401", resp.StatusCode)
	}
}

// Изменяющие ручки сверяют Origin, как вход и выход: CSRF-запрос из чужого
// браузера отбивается до всякого дела.
func TestRunsForeignOrigin(t *testing.T) {
	e, c, tmuxLog := runsEnv(t, "")
	req, err := http.NewRequest("POST", e.srv.URL+"/api/projects/demo/runs", strings.NewReader(`{"id": "XR-002"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Origin", "http://evil.example")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("чужой Origin на запуске: %d, ожидал 403", resp.StatusCode)
	}
	if got := readFile(t, tmuxLog); strings.Contains(got, "new-session") {
		t.Errorf("чужой Origin дошёл до tmux: %s", got)
	}
}

// Стоп называется стопом и на экране: слова «пауза» в статике нет ни в одном
// файле, это обещание постановки DK-218.
func TestStaticNoPauseWord(t *testing.T) {
	entries, err := os.ReadDir("static")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		text := strings.ToLower(readFile(t, filepath.Join("static", e.Name())))
		for _, bad := range []string{"пауза", "паузу", "паузы", "pause"} {
			if strings.Contains(text, bad) {
				t.Errorf("в static/%s есть слово %q, а стоп называется стопом", e.Name(), bad)
			}
		}
	}
}

// Оболочка ищется и в корне, который сам есть чекаут devkit, не только в
// подкаталоге.
func TestGoalRunPathRootItself(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "kit", "skills", "goal-loop")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "goal-run.py")
	if err := os.WriteFile(path, []byte("pass\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := goalRunPath([]string{root}); got != path {
		t.Fatalf("оболочка в самом корне не нашлась: %q", got)
	}
	if got := goalRunPath([]string{t.TempDir()}); got != "" {
		t.Fatalf("в пустом корне нашлось лишнее: %q", got)
	}
}
