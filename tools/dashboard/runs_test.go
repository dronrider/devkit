package main

import (
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Доска для тестов запуска: цель XR-100 (заголовок от слова «Цель:») и
// одиночные задачи в трёх статусах, по одной на каждый заказ конвейеру.
const runsBoardJSON = `{"prefix":"XR","sections":[` +
	`{"key":"in-progress","title":"In progress","rows":[` +
	`{"id":"XR-100","title":"Цель: пробный цикл","type":"task","p":"P2","r":41,"r_parts":[25,9,3,0,4],"cost":"XL","link":"-"},` +
	`{"id":"XR-004","title":"Начатая задача","type":"task","p":"P2","r":31,"r_parts":[25,3,1,0,2],"cost":"-","link":"-"}]},` +
	`{"key":"check","title":"Check","rows":[{"id":"XR-003","title":"Задача на проверке","type":"task","p":"P2","r":32,"r_parts":[25,4,1,0,2],"cost":"-","link":"-"}]},` +
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
// конвейера, и заказ ей идёт по статусу строки: из Backlog «Выполни», из
// In progress «Продолжай выполнение», из Check «Закрой». Слова эти те же, что
// человек пишет в чате, и разъехаться со скиллами доски им нельзя.
func TestRunStartTaskPromptBySection(t *testing.T) {
	for _, tc := range []struct{ id, prompt string }{
		{"XR-002", "Выполни XR-002"},
		{"XR-004", "Продолжай выполнение XR-004"},
		{"XR-003", "Закрой XR-003"},
	} {
		t.Run(tc.id, func(t *testing.T) {
			e, c, tmuxLog := runsEnv(t, "")
			resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/runs", fmt.Sprintf(`{"id": %q}`, tc.id))
			text := body(t, resp)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("запуск задачи: %d %s", resp.StatusCode, text)
			}
			for _, want := range []string{`"kind":"task"`, fmt.Sprintf(`"session":"task-%s"`, tc.id)} {
				if !strings.Contains(text, want) {
					t.Errorf("в ответе запуска нет %q: %s", want, text)
				}
			}
			got := readFile(t, tmuxLog)
			// Промпт уходит одной заквоченной строкой: tmux склеивает хвост
			// new-session пробелами и отдаёт шеллу.
			// Пары окружения едут в начале команды те же, что у диалога
			// (chatVars): имя сессии для реестра чатов, настоящий HOME (без
			// него agentctl exec разворачивал тильду раскладки в подложном
			// доме демона, и клиент второй подписки отвечал «Not logged in»)
			// и заглушка опроса фокуса.
			want := "new-session -d -s task-" + tc.id + " -c " + e.proj +
				" DEVKIT_NO_FOCUS=1 HOME='" + realHome() + "'" +
				" DEVKIT_TASK='" + tc.id + "' DEVKIT_TMUX='task-" + tc.id + "'" +
				// Правило плана едет в том же заказе: по нему дашборд рисует
				// деления кольца и блок «План агента». Запасной адрес называет
				// имя tmux-сессии дословно: в контуре второй подписки
				// CLAUDE_CODE_SESSION_ID пуст, и агент DK-269 разыскивал свой
				// ID десяток ходов.
				" claude -p '" + tc.prompt + " " + planRule +
				" Если CLAUDE_CODE_SESSION_ID пуст, веди план файлом ~/.devkit/plans/task-" + tc.id + ".json.'"
			if !strings.Contains(got, want) {
				t.Errorf("tmux позван не так:\n%s\nожидал вхождение %q", got, want)
			}
		})
	}
}

// rowOrder называет заказ той же строкой, что соберёт headless-сессии
// runPrompt: подсказке кнопки разойтись с реальным заказом нечем. У строки
// цели и у проверенной строки с пользовательской приёмкой нет заказа вовсе:
// первую ведёт своя оболочка, вторая закрывается без сессии агента.
func TestRowOrder(t *testing.T) {
	for _, tc := range []struct{ name, sect, id, accept, title, want string }{
		{"backlog", "backlog", "XR-002", "", "Обычная задача", "Выполни XR-002"},
		{"in-progress", "in-progress", "XR-004", "", "Начатая задача", "Продолжай выполнение XR-004"},
		{"check agent", "check", "XR-003", "", "Задача на проверке", "Закрой XR-003"},
		{"check mixed", "check", "XR-005", "mixed", "Смешанная приёмка", "Закрой XR-005"},
		{"check user closes without session", "check", "XR-006", "user", "Пользовательская приёмка", ""},
		{"goal in progress", "in-progress", "XR-100", "", "Цель: пробный цикл", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := rowOrder(tc.sect, tc.id, tc.accept, tc.title); got != tc.want {
				t.Errorf("rowOrder(%q,%q,%q,%q) = %q, ждал %q",
					tc.sect, tc.id, tc.accept, tc.title, got, tc.want)
			}
		})
	}
}

// Выбранная подписка доезжает до команды сессии: она заворачивается в
// agentctl exec, и клиент поднимается тот, который назвала раскладка. До
// DK-326 запуск всегда шёл первой подпиской, а вторая простаивала.
func TestRunStartOnChosenHarness(t *testing.T) {
	e, c, tmuxLog := runsEnv(t, "")
	writeAgentctlFake(t, e.bin, harnessJSONFixture)
	writeScript(t, e.bin, "клиент-2", "exit 0")
	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/runs",
		`{"id": "XR-002", "harness": "втораяtest"}`)
	text := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("запуск на второй подписке: %d %s", resp.StatusCode, text)
	}
	// Ответ называет подписку: подмена квоты иначе ничем не видна.
	for _, want := range []string{`"harness":"втораяtest"`, "поднят на подписке втораяtest"} {
		if !strings.Contains(text, want) {
			t.Errorf("в ответе запуска нет %q: %s", want, text)
		}
	}
	// agentctl зовётся полным путём: tmux-сессия наследует PATH дашборда, а под
	// launchd он системный, и утилит devkit в нём может не быть. HOME в заказе
	// настоящий: exec разворачивает тильду раскладки харнеса, и в подложном
	// доме демона CLAUDE_CONFIG_DIR второй подписки указывал в пустой каталог.
	want := "new-session -d -s task-XR-002 -c " + e.proj +
		" DEVKIT_NO_FOCUS=1 HOME='" + realHome() + "'" +
		" DEVKIT_TASK='XR-002' DEVKIT_TMUX='task-XR-002' '" + filepath.Join(e.bin, "agentctl") +
		"' exec --harness 'втораяtest' -- 'клиент-2' --permission-mode auto -p 'Выполни XR-002 " + planRule +
		" Если CLAUDE_CODE_SESSION_ID пуст, веди план файлом ~/.devkit/plans/task-XR-002.json.'"
	if got := readFile(t, tmuxLog); !strings.Contains(got, want) {
		t.Errorf("tmux позван не так:\n%s\nожидал вхождение %q", got, want)
	}
}

// Модель конвейера чужой подписки оседает в памяти диалога: заказ её не
// называет (клиент подписки берёт свою по умолчанию), а панель без записи
// показывала бы в селекторе умолчание первой подписки поверх второй (живой
// случай, запуск DK-269 показывал opus). Пишется ярус pro раскладки.
func TestRunStartRecordsHarnessModel(t *testing.T) {
	e, c, _ := runsEnv(t, "")
	fixture := strings.Replace(harnessJSONFixture,
		`"bin": "клиент-2", "env": ["CONFIG_DIR"]`,
		`"bin": "клиент-2", "env": ["CONFIG_DIR"], "models": [`+
			`{"tier": "base", "model": "глм-база"}, {"tier": "pro", "model": "глм-про"}]`, 1)
	writeAgentctlFake(t, e.bin, fixture)
	writeScript(t, e.bin, "клиент-2", "exit 0")
	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/runs",
		`{"id": "XR-002", "harness": "втораяtest"}`)
	if text := body(t, resp); resp.StatusCode != http.StatusOK {
		t.Fatalf("запуск на второй подписке: %d %s", resp.StatusCode, text)
	}
	if got := e.s.chatStoreRead("tmux-task-XR-002").Model; got != "глм-про" {
		t.Errorf("память диалога конвейера держит модель %q, ждал модель яруса pro подписки", got)
	}
}

// Без выбора всё остаётся как было: прежний клиент и прежняя команда, без
// обёртки. Экран, который списка не прочитал, работает ровно как до задачи.
func TestRunStartWithoutHarnessKeepsOldWay(t *testing.T) {
	e, c, tmuxLog := runsEnv(t, "")
	writeAgentctlFake(t, e.bin, harnessJSONFixture)
	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/runs", `{"id": "XR-002"}`)
	if text := body(t, resp); resp.StatusCode != http.StatusOK {
		t.Fatalf("запуск без выбора: %d %s", resp.StatusCode, text)
	}
	got := readFile(t, tmuxLog)
	if !strings.Contains(got, " claude -p 'Выполни XR-002 "+planRule+
		" Если CLAUDE_CODE_SESSION_ID пуст, веди план файлом ~/.devkit/plans/task-XR-002.json.'") {
		t.Errorf("запуск без выбора пошёл не прежней дорогой:\n%s", got)
	}
	if strings.Contains(got, "agentctl exec") {
		t.Errorf("запуск без выбора завернулся в exec:\n%s", got)
	}
}

// Подписка, которой на машине нет, отбивается словами и до всякой сессии:
// молча уехать на подписку по умолчанию нельзя, человек выбрал имя.
func TestRunStartUnknownHarness(t *testing.T) {
	e, c, tmuxLog := runsEnv(t, "")
	writeAgentctlFake(t, e.bin, harnessJSONFixture)
	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/runs",
		`{"id": "XR-002", "harness": "четвёртаяtest"}`)
	text := body(t, resp)
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(text, "на машине нет") {
		t.Fatalf("незнакомая подписка: %d %s, ожидал 400 со словами", resp.StatusCode, text)
	}
	if strings.Contains(readFile(t, tmuxLog), "new-session") {
		t.Errorf("сессия поднялась вопреки отказу:\n%s", readFile(t, tmuxLog))
	}
}

// Клиент выбранной подписки ищется в PATH до подъёма сессии, как и прежний:
// сессия с ненайденной командой умерла бы молча.
func TestRunStartChosenClientMissing(t *testing.T) {
	// Фикстуры клиента второй подписки в PATH стенда нет вовсе, а прежний
	// claude на месте: ищется именно клиент выбранной подписки.
	e, c, _ := runsEnv(t, "")
	writeAgentctlFake(t, e.bin, harnessJSONFixture)
	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/runs",
		`{"id": "XR-002", "harness": "втораяtest"}`)
	text := body(t, resp)
	if resp.StatusCode != http.StatusBadGateway || !strings.Contains(text, "клиент-2 не нашёлся") {
		t.Fatalf("ненайденный клиент подписки: %d %s, ожидал 502 с его именем", resp.StatusCode, text)
	}
}

// У цели выбор подписки не работает: цикл поднимает оболочка goal-run своей
// сессией, и передать ей имя нечем. Отказ словами честнее молчаливого запуска
// на подписке по умолчанию.
func TestRunStartGoalRefusesHarness(t *testing.T) {
	e, c, _ := runsEnv(t, "")
	writeAgentctlFake(t, e.bin, harnessJSONFixture)
	writeGoalRunFake(t, filepath.Dir(e.proj), goalRunOKBody(filepath.Join(e.home, "goal-run.calls")))
	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/runs",
		`{"id": "XR-100", "harness": "втораяtest"}`)
	text := body(t, resp)
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(text, "оболочка цикла goal-run") {
		t.Fatalf("выбор подписки у цели: %d %s, ожидал 400 с причиной", resp.StatusCode, text)
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

// writeNotifyFake кладёт фикстуру уведомителя в тот же синтетический чекаут
// devkit, где лежит оболочка цикла: живой hooks/notify.py тут не зовётся,
// иначе тест давал бы баннер на машине разработчика.
func writeNotifyFake(t *testing.T, root, callsLog string) {
	t.Helper()
	dir := filepath.Join(root, "devkit", "hooks")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf(`import sys
with open(%q, "a") as f:
    f.write("\t".join(sys.argv[1:]) + "\n")
`, callsLog)
	if err := os.WriteFile(filepath.Join(dir, "notify.py"), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

// Стоп из дашборда говорит о себе тем же уведомителем, что виток и taskctl
// move: без строки в журнале уведомителя снятую сессию видит только тот, кто
// нажал кнопку, а лента для того и заведена, чтобы стоп доехал до второго
// устройства.
func TestRunStopNotifies(t *testing.T) {
	e, c, _ := runsEnv(t, `goal-XR-100\n`)
	writeGoalRunFake(t, filepath.Dir(e.proj), goalRunOKBody(filepath.Join(e.home, "goal-run.calls")))
	notifyCalls := filepath.Join(e.home, "notify.calls")
	writeNotifyFake(t, filepath.Dir(e.proj), notifyCalls)

	resp := doReq(t, c, "DELETE", e.srv.URL+"/api/projects/demo/runs/XR-100", "")
	text := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("стоп цели: %d %s", resp.StatusCode, text)
	}
	got := readFile(t, notifyCalls)
	if !strings.HasPrefix(strings.TrimSpace(got), "--reason\trun_stop\t") {
		t.Fatalf("уведомитель позван без повода стопа: %q", got)
	}
	// Задача и проект едут своими ключами (DK-323): лента ведёт от события к
	// строке доски по полю, а «Поднять виток» поднимает работу того проекта,
	// где стоп случился, а не открытого на экране.
	for _, want := range []string{"--task\tXR-100\t", "--project\tdemo\t"} {
		if !strings.Contains(got, want) {
			t.Errorf("уведомитель позван без поля %q: %q", want, got)
		}
	}
	for _, want := range []string{"demo", "XR-100", "стоп из дашборда"} {
		if !strings.Contains(got, want) {
			t.Errorf("в уведомлении о стопе нет %q: %q", want, got)
		}
	}
	if strings.Contains(text, "note") {
		t.Errorf("удавшееся уведомление оставило приписку: %s", text)
	}
}

// Ненайденный уведомитель стоп не отменяет, но и не молчит: сессия снята, а
// приписка говорит, что в ленте строки не будет.
func TestRunStopWithoutNotifierNamed(t *testing.T) {
	e, c, tmuxLog := runsEnv(t, `task-XR-002\n`)
	resp := doReq(t, c, "DELETE", e.srv.URL+"/api/projects/demo/runs/XR-002", "")
	text := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("стоп без уведомителя: %d %s", resp.StatusCode, text)
	}
	if !strings.Contains(text, "hooks/notify.py не нашёлся") {
		t.Errorf("ненайденный уведомитель прошёл молча: %s", text)
	}
	if got := readFile(t, tmuxLog); !strings.Contains(got, "kill-session -t =task-XR-002") {
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

// Стоп привязан к проекту, которым позван: сессию с чужим префиксом
// (goal-DK-777 при доске с префиксом XR) запрос с доски demo не снимает и
// журнал через --say в чужом корне не заводит. Замечание ревью DK-218.
func TestRunStopForeignPrefixUntouched(t *testing.T) {
	e, c, tmuxLog := runsEnv(t, `goal-DK-777\n`)
	callsLog := filepath.Join(e.home, "goal-run.calls")
	writeGoalRunFake(t, filepath.Dir(e.proj), goalRunOKBody(callsLog))

	resp := doReq(t, c, "DELETE", e.srv.URL+"/api/projects/demo/runs/DK-777", "")
	text := body(t, resp)
	if resp.StatusCode != http.StatusNotFound || !strings.Contains(text, "не идёт") {
		t.Fatalf("стоп чужой сессии: %d %s, ожидал 404 про «не идёт»", resp.StatusCode, text)
	}
	if got := readFile(t, tmuxLog); strings.Contains(got, "kill-session") {
		t.Errorf("чужая сессия снята: %s", got)
	}
	if got := readFile(t, callsLog); got != "" {
		t.Errorf("goal-run позван в чужой проект: %q", got)
	}
}

// Цель из реестра без tmux-сессии ведёт другая сессия (живой чат): дашборд её
// не убивает и говорит об этом словами.
func TestRunStopGoalLedOutside(t *testing.T) {
	e, c, _ := runsEnv(t, "")
	resp := doReq(t, c, "DELETE", e.srv.URL+"/api/projects/demo/runs/XR-112", "")
	text := body(t, resp)
	if resp.StatusCode != http.StatusConflict || !strings.Contains(text, "стоп отсюда недоступен") {
		t.Fatalf("стоп чужого цикла: %d %s, ожидал 409 про недоступный отсюда стоп", resp.StatusCode, text)
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
	e, c, tmuxLog := runsEnv(t, `task-XR-002\n`)
	for _, call := range []struct{ method, url, body string }{
		{"POST", e.srv.URL + "/api/projects/demo/runs", `{"id": "XR-002"}`},
		{"DELETE", e.srv.URL + "/api/projects/demo/runs/XR-002", ""},
	} {
		req, err := http.NewRequest(call.method, call.url, strings.NewReader(call.body))
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
			t.Errorf("чужой Origin на %s: %d, ожидал 403", call.method, resp.StatusCode)
		}
	}
	if got := readFile(t, tmuxLog); strings.Contains(got, "new-session") || strings.Contains(got, "kill-session") {
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
		text := strings.ToLower(dropComments(readFile(t, filepath.Join("static", e.Name()))))
		for _, bad := range []string{"пауза", "паузу", "паузы", "pause"} {
			if strings.Contains(text, bad) {
				t.Errorf("в static/%s есть слово %q, а стоп называется стопом", e.Name(), bad)
			}
		}
	}
}

// dropComments выбрасывает строки-комментарии статики: обещание тут про слова
// на экране, а в пояснениях к коду пауза живёт своей жизнью и говорит про
// задержку перед переподключением потока, а не про остановку агента.
func dropComments(text string) string {
	var keep []string
	for _, ln := range strings.Split(text, "\n") {
		if strings.HasPrefix(strings.TrimSpace(ln), "//") {
			continue
		}
		keep = append(keep, ln)
	}
	return strings.Join(keep, "\n")
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

// logCapture собирает строки логирования для проверки
type logCapture struct {
	lines []string
}

func (l *logCapture) log(format string, args ...any) {
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
}

func (l *logCapture) contains(t *testing.T, substr string) bool {
	t.Helper()
	for _, line := range l.lines {
		if strings.Contains(line, substr) {
			return true
		}
	}
	return false
}

// runsEnvWithLog поднимает окружение с журналированием
func runsEnvWithLog(t *testing.T, sessions string) (*testEnv, *http.Client, string, *logCapture) {
	t.Helper()
	home := t.TempDir()
	root := filepath.Join(home, "projects")
	proj := filepath.Join(root, "demo")
	mkProject(t, proj)

	bin := t.TempDir()
	writeScript(t, bin, "taskctl", fmt.Sprintf("echo '%s'", runsBoardJSON))
	tmuxLog := filepath.Join(home, "tmux.log")
	writeTmuxFake(t, bin, tmuxLog, sessions)
	writeScript(t, bin, "claude", "exit 0")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	goals := filepath.Join(home, ".devkit", "goals")
	if err := os.MkdirAll(goals, 0o755); err != nil {
		t.Fatal(err)
	}
	entry := "goal = XR-112\nroot = " + proj + "\n"
	if err := os.WriteFile(filepath.Join(goals, "XR-112.watch"), []byte(entry), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := &Config{Home: home, Roots: []string{root}, Port: defaultPort, Token: "test-token"}
	lc := &logCapture{}
	s := newServer(cfg, os.DirFS("static"), lc.log)
	srv := httptest.NewServer(s.handler())
	t.Cleanup(srv.Close)

	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar}
	resp, _ := c.Post(srv.URL+"/api/login", "application/json",
		strings.NewReader(`{"token": "test-token"}`))
	resp.Body.Close()

	return &testEnv{srv: srv, s: s, cfg: cfg, home: home, proj: proj, bin: bin}, c, tmuxLog, lc
}

// Ошибки на запуск должны логироваться: чужой Origin
func TestRunStartForeignOriginLogged(t *testing.T) {
	e, c, _, lc := runsEnvWithLog(t, "")
	req, _ := http.NewRequest("POST", e.srv.URL+"/api/projects/demo/runs", strings.NewReader(`{"id": "XR-002"}`))
	req.Header.Set("Origin", "http://evil.example")
	req.Header.Set("Content-Type", "application/json")
	resp, _ := c.Do(req)
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("ожидал 403, получил %d", resp.StatusCode)
	}
	if !lc.contains(t, "чужой Origin 403") {
		t.Errorf("запуск с чужим Origin не залогировался: %v", lc.lines)
	}
}

// Ошибки на запуск должны логироваться: битое тело запроса
func TestRunStartBadRequestLogged(t *testing.T) {
	e, c, _, lc := runsEnvWithLog(t, "")
	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/runs", `{}`)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("ожидал 400, получил %d", resp.StatusCode)
	}
	if !lc.contains(t, "битое тело запроса 400") {
		t.Errorf("запуск с ошибкой тела не залогировался: %v", lc.lines)
	}
}

// Ошибки на запуск должны логироваться: неизвестная строка
func TestRunStartUnknownRowLogged(t *testing.T) {
	e, c, _, lc := runsEnvWithLog(t, "")
	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/runs", `{"id": "XR-999"}`)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("ожидал 404, получил %d", resp.StatusCode)
	}
	if !lc.contains(t, "нет строки на доске 404") {
		t.Errorf("запуск неизвестной строки не залогировался: %v", lc.lines)
	}
}

// Ошибки на стоп должны логироваться: чужой Origin
func TestRunStopForeignOriginLogged(t *testing.T) {
	e, c, _, lc := runsEnvWithLog(t, `task-XR-002\n`)
	req, _ := http.NewRequest("DELETE", e.srv.URL+"/api/projects/demo/runs/XR-002", strings.NewReader(""))
	req.Header.Set("Origin", "http://evil.example")
	resp, _ := c.Do(req)
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("ожидал 403, получил %d", resp.StatusCode)
	}
	if !lc.contains(t, "чужой Origin 403") {
		t.Errorf("стоп с чужим Origin не залогировался: %v", lc.lines)
	}
}

// Ошибки на стоп должны логироваться: работа не идёт
func TestRunStopNotRunningLogged(t *testing.T) {
	e, c, _, lc := runsEnvWithLog(t, "")
	resp := doReq(t, c, "DELETE", e.srv.URL+"/api/projects/demo/runs/XR-002", "")
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("ожидал 404, получил %d", resp.StatusCode)
	}
	if !lc.contains(t, "работа не идёт 404") {
		t.Errorf("стоп неидущей работы не залогировался: %v", lc.lines)
	}
}

// Ошибки на стоп должны логироваться: интерактивная сессия
func TestRunStopInteractiveSessionLogged(t *testing.T) {
	e, c, _, lc := runsEnvWithLog(t, `claude_interactive\n`)
	// Помечаем сессию как интерактивную через реестр целей
	resp := doReq(t, c, "DELETE", e.srv.URL+"/api/projects/demo/runs/XR-112", "")
	resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("ожидал 409, получил %d", resp.StatusCode)
	}
	if !lc.contains(t, "цикл ведёт другая сессия 409") {
		t.Errorf("стоп цикла из реестра не залогировался: %v", lc.lines)
	}
}

// Ошибки на запуск должны логироваться: проект не найден
func TestRunStartProjectNotFoundLogged(t *testing.T) {
	e, c, _, lc := runsEnvWithLog(t, "")
	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/unknown/runs", `{"id": "XR-002"}`)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("ожидал 404, получил %d", resp.StatusCode)
	}
	if !lc.contains(t, "запуск отклонён: проект unknown не найден 404") {
		t.Errorf("запуск неизвестного проекта не залогировался с именем ручки: %v", lc.lines)
	}
}

// Ошибки на запуск должны логироваться: tmux не нашёлся
func TestRunStartNoTmuxLogged(t *testing.T) {
	e, c, _, lc := runsEnvWithLog(t, "")
	if err := os.Remove(os.ExpandEnv(filepath.Join(e.bin, "tmux"))); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", e.bin)
	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/runs", `{"id": "XR-002"}`)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("ожидал 502, получил %d", resp.StatusCode)
	}
	if !lc.contains(t, "tmux не нашёлся 502") {
		t.Errorf("запуск без tmux не залогировался: %v", lc.lines)
	}
}

// Ошибки на стоп должны логироваться: проект не найден
func TestRunStopProjectNotFoundLogged(t *testing.T) {
	e, c, _, lc := runsEnvWithLog(t, "")
	resp := doReq(t, c, "DELETE", e.srv.URL+"/api/projects/unknown/runs/XR-002", "")
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("ожидал 404, получил %d", resp.StatusCode)
	}
	if !lc.contains(t, "стоп отклонён: проект unknown не найден 404") {
		t.Errorf("стоп с неизвестным проектом не залогировался с именем ручки: %v", lc.lines)
	}
}

// Ошибки на стоп должны логироваться: tmux не нашёлся
func TestRunStopNoTmuxLogged(t *testing.T) {
	e, c, _, lc := runsEnvWithLog(t, "")
	if err := os.Remove(os.ExpandEnv(filepath.Join(e.bin, "tmux"))); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", e.bin)
	resp := doReq(t, c, "DELETE", e.srv.URL+"/api/projects/demo/runs/XR-002", "")
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("ожидал 502, получил %d", resp.StatusCode)
	}
	if !lc.contains(t, "tmux не нашёлся 502") {
		t.Errorf("стоп без tmux не залогировался: %v", lc.lines)
	}
}

// Доска для проверки вида приёмки: в Check стоят смешанная и пользовательская
// строки, в Backlog пользовательская. Вид приезжает полем accept, куда taskctl
// кладёт разобранный суффикс заголовка «[приёмка: ...]».
const acceptBoardJSON = `{"prefix":"XR","sections":[` +
	`{"key":"in-progress","title":"In progress","rows":[]},` +
	`{"key":"check","title":"Check","rows":[` +
	`{"id":"XR-005","title":"Смешанная приёмка","accept":"mixed","type":"task","p":"P2","r":30,"r_parts":[25,2,1,0,2],"cost":"-","link":"-"},` +
	`{"id":"XR-006","title":"Пользовательская приёмка","accept":"user","type":"task","p":"P2","r":30,"r_parts":[25,2,1,0,2],"cost":"-","link":"-"}]},` +
	`{"key":"backlog","title":"Backlog","rows":[` +
	`{"id":"XR-007","title":"Пользовательская в очереди","accept":"user","type":"task","p":"P2","r":30,"r_parts":[25,2,1,0,2],"cost":"-","link":"-"}]}]}`

// gitFakeMoving пишет вызовы в журнал и по-настоящему двигает файл на «git mv»:
// close уносит файл задачи в архив его руками, а молчаливая фикстура оставила
// бы утилиту с ненайденным местом назначения.
func gitFakeMoving(logPath string) string {
	return fmt.Sprintf(`echo "$@" >> %q
[ "$3" = mv ] && mv "$4" "$5"
exit 0`, logPath)
}

// userAcceptDoc это файл задачи с пользовательской приёмкой: раздела
// «Сценарий проверки» требуют ворота move check, раздела «Приёмка» с барьером и
// перебором обходов ворота не агентского вида (LLD DK-292).
const userAcceptDoc = `# XR-004: Четвёртая

## Сценарий проверки

Открыть доску, нажать «Закрыть» у строки в Check.

## Приёмка

- вид: user
- барьер «согласие»: закрывать задачу решает человек, машине этого не отдают
  - спросить у витка: не годится, согласие даёт человек, а не агент
`

// Проверенная задача с пользовательской приёмкой закрывается прямо с экрана:
// taskctl close вместо сессии агента. До DK-289 кнопка «Закрыть» поднимала
// tmux-сессию с заказом «Закрой XR-004», то есть платила минутами ожидания и
// квотой за подтверждение того, что человек уже принял глазами. Стенд гоняет
// настоящий taskctl: вид приёмки живёт суффиксом строки доски, и подменять его
// выдумкой значило бы проверять не тот путь, каким вид приезжает на самом деле.
func TestRunStartUserAcceptClosesWithoutSession(t *testing.T) {
	e, c, gitLog := tasksEnv(t)
	writeScript(t, e.bin, "git", gitFakeMoving(gitLog))
	tmuxLog := filepath.Join(e.home, "tmux.log")
	writeTmuxFake(t, e.bin, tmuxLog, "")
	writeScript(t, e.bin, "claude", "exit 0")
	runTaskctl(t, e.proj, "set", "XR-004", "--accept", "user")
	goalDoc(t, e, "XR-004", userAcceptDoc)
	runTaskctl(t, e.proj, "move", "XR-004", "check")

	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/runs", `{"id": "XR-004"}`)
	text := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("закрытие принятой задачи: %d %s", resp.StatusCode, text)
	}
	for _, want := range []string{`"kind":"close"`, "XR-004 закрыта"} {
		if !strings.Contains(text, want) {
			t.Errorf("в ответе закрытия нет %q: %s", want, text)
		}
	}
	if got := readFile(t, tmuxLog); strings.Contains(got, "new-session") {
		t.Errorf("закрытие принятой задачи подняло сессию агента:\n%s", got)
	}
	if got := runTaskctl(t, e.proj, "list", "--json"); strings.Contains(got, "XR-004") {
		t.Errorf("строка XR-004 осталась на доске: %s", got)
	}
	if got := readFile(t, filepath.Join(e.proj, "docs", "TASKS-archive.md")); !strings.Contains(got, "XR-004") {
		t.Errorf("закрытая задача не легла в архив:\n%s", got)
	}
	// Коммит доски идёт следом за закрытием: доска это общий источник правды, и
	// закрытие, оставшееся некоммиченным, разъезжается с remote. В pathspec
	// коммита оба конца переезда файла, иначе в него попала бы его половина.
	got := readFile(t, gitLog)
	if !strings.Contains(got, "docs(tasks): XR-004 закрыта с дашборда") {
		t.Errorf("коммит доски не позван с ID в subject:\n%s", got)
	}
	if !strings.Contains(got, "docs/tasks/archive/") || !strings.Contains(got, "docs/tasks/XR-004.md") {
		t.Errorf("в коммит уехал не весь переезд файла задачи:\n%s", got)
	}
}

// Закрытие мимо сессии включается ровно для пользовательской приёмки в Check:
// смешанной закрытие ещё предстоит агентской частью, а пользовательская строка
// в Backlog это работа, которую никто не проверял.
func TestRunStartKeepsSessionBesidesUserCheck(t *testing.T) {
	for _, tc := range []struct{ id, prompt string }{
		{"XR-005", "Закрой XR-005"},
		{"XR-007", "Выполни XR-007"},
	} {
		t.Run(tc.id, func(t *testing.T) {
			e := newTestEnv(t)
			writeScript(t, e.bin, "taskctl", fmt.Sprintf("echo '%s'", acceptBoardJSON))
			tmuxLog := filepath.Join(e.home, "tmux.log")
			writeTmuxFake(t, e.bin, tmuxLog, "")
			writeScript(t, e.bin, "claude", "exit 0")
			c := e.loggedClient(t)

			resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/runs", fmt.Sprintf(`{"id": %q}`, tc.id))
			text := body(t, resp)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("запуск задачи: %d %s", resp.StatusCode, text)
			}
			if !strings.Contains(text, `"kind":"task"`) {
				t.Errorf("вид приёмки увёл запуск мимо сессии: %s", text)
			}
			if got := readFile(t, tmuxLog); !strings.Contains(got, "claude -p '"+tc.prompt+" "+planRule+
				" Если CLAUDE_CODE_SESSION_ID пуст, веди план файлом ~/.devkit/plans/task-"+tc.id+".json.'") {
				t.Errorf("сессия поднята не с тем заказом:\n%s\nждал %q", got, tc.prompt)
			}
		})
	}
}

// Устаревший экран получает настоящую причину отказа: закрытая задача уехала в
// архив, а «на доске нет строки» читается как поломка, хотя закрытие прошло.
// Второе нажатие с телефона по строке, закрытой с ноутбука, до DK-289 отвечало
// именно так. Сам экран задачи при этом открывается: закрытую задачу читают
// строкой архива, и отказ тут был бы тупиком выдачи поиска.
func TestRunStartClosedRowNamesArchive(t *testing.T) {
	e, c, _ := runsEnv(t, "")
	arch := "# Архив (префикс XR)\n\n| ID | Задача | Тип | P | Закрыто | Ссылка |\n" +
		"|--------|--------|-----|---|---------|--------|\n" +
		"| XR-009 | Принятая глазами | task | P2 | 2026-08-13 | - |\n"
	if err := os.WriteFile(filepath.Join(e.proj, "docs", "TASKS-archive.md"), []byte(arch), 0o644); err != nil {
		t.Fatal(err)
	}
	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/runs", `{"id": "XR-009"}`)
	text := body(t, resp)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("нажатие по закрытой задаче: %d %s", resp.StatusCode, text)
	}
	for _, want := range []string{"уже закрыта 2026-08-13", "экран устарел"} {
		if !strings.Contains(text, want) {
			t.Errorf("нажатие по закрытой задаче не назвало причину %q: %s", want, text)
		}
	}
	resp = doReq(t, c, "GET", e.srv.URL+"/api/projects/demo/tasks/XR-009", "")
	text = body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("экран закрытой задачи: %d %s", resp.StatusCode, text)
	}
	for _, want := range []string{`"closed":"2026-08-13"`, "Принятая глазами", "осталась одной строкой архива"} {
		if !strings.Contains(text, want) {
			t.Errorf("экран закрытой задачи пришёл без %q: %s", want, text)
		}
	}
}
