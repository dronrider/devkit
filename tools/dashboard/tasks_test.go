package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// Правка строки и файла задачи. Стенд гоняет настоящий taskctl на фикстурной
// доске во временном HOME: разбивку ранга, бакет P, порядок Backlog и рубежи
// зависимостей считает утилита, и подменять её здесь фикстурой значило бы
// проверять собственную выдумку. Настоящий тут только taskctl: git играет
// исполняемая фикстура, коммитов и пушей тесты не делают.

var (
	realTaskctlOnce sync.Once
	realTaskctlPath string
	realTaskctlErr  error
)

// buildTaskctl собирает соседний инструмент один раз на прогон пакета:
// сервер зовёт его отдельным процессом, и «go run» тут не подходит, как и в
// интеграционных тестах самого taskctl.
func buildTaskctl(t *testing.T) string {
	t.Helper()
	realTaskctlOnce.Do(func() {
		dir, err := os.MkdirTemp("", "dashboard-taskctl")
		if err != nil {
			realTaskctlErr = err
			return
		}
		realTaskctlPath = filepath.Join(dir, "taskctl")
		cmd := exec.Command("go", "build", "-o", realTaskctlPath, ".")
		cmd.Dir = filepath.Join("..", "taskctl")
		cmd.Env = append(os.Environ(), "GOWORK=off")
		if out, err := cmd.CombinedOutput(); err != nil {
			realTaskctlErr = fmt.Errorf("go build taskctl: %v\n%s", err, out)
		}
	})
	if realTaskctlErr != nil {
		t.Fatal(realTaskctlErr)
	}
	return realTaskctlPath
}

func TestMain(m *testing.M) {
	code := m.Run()
	if realTaskctlPath != "" {
		os.RemoveAll(filepath.Dir(realTaskctlPath))
	}
	os.Exit(code)
}

// taskctlFixture кладёт настоящий бинарь на место исполняемой фикстуры в
// PATH стенда.
func taskctlFixture(t *testing.T, bin string) {
	t.Helper()
	real := buildTaskctl(t)
	path := filepath.Join(bin, "taskctl")
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if err := os.Symlink(real, path); err != nil {
		t.Fatal(err)
	}
}

// runTaskctl зовёт утилиту напрямую: так стенд заводит фикстурную доску и так
// же читает её назад, не веря ответу сервера на слово.
func runTaskctl(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command(realTaskctlPath, append(args, "-C", dir)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("taskctl %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// tasksEnv поднимает сервер на доске с цепочкой XR-001 -> XR-002 -> XR-003
// («после» в эту сторону) и одиночной XR-004. Доску заводит сама утилита:
// формат строки её, и переписывать его в тесте руками значит разойтись с ней
// на первой же правке.
func tasksEnv(t *testing.T) (*testEnv, *http.Client, string) {
	t.Helper()
	e := newTestEnv(t)
	taskctlFixture(t, e.bin)
	sandboxRealNotifier(t, e)
	if err := os.Remove(filepath.Join(e.proj, "docs", "TASKS.md")); err != nil {
		t.Fatal(err)
	}
	// Доска заводится настоящим git в PATH: taskctl init ищет корень
	// репозитория через «rev-parse --show-toplevel», и подставленная фикстура
	// git увела бы доску в текущий каталог. Фикстура встаёт после, когда
	// стенду нужны молчаливые add, commit и push.
	runTaskctl(t, e.proj, "init", "--prefix", "XR", "--name", "demo")
	for _, row := range [][]string{
		{"--id", "XR-001", "--title", "Первая", "--rank", "50+3+1+0+1", "--cost", "L"},
		{"--id", "XR-002", "--title", "Вторая", "--rank", "25+2+1+0+2", "--cost", "M"},
		{"--id", "XR-003", "--title", "Третья", "--rank", "25+0+0+0+0"},
		{"--id", "XR-004", "--title", "Четвёртая", "--rank", "0+1+0+0+0"},
	} {
		runTaskctl(t, e.proj, append([]string{"add"}, row...)...)
	}
	runTaskctl(t, e.proj, "dep", "add", "XR-002", "XR-001")
	runTaskctl(t, e.proj, "dep", "add", "XR-003", "XR-002")
	gitLog := filepath.Join(e.home, "git.log")
	writeScript(t, e.bin, "git", gitFakeOK(gitLog))
	return e, e.loggedClient(t), gitLog
}

// sandboxRealNotifier не даёт настоящему taskctl зовущему настоящий
// hooks/notify.py (move на Check и на блокер, RULES.board.md «Ветки, ревью и
// деплой» п. 8) дотянуться до живого журнала машины. taskctl ищет notify.py
// вплоть до ~/projects/devkit (notifyScript в tools/taskctl/notify.go), а сам
// notify.py пишет журнал по HOME процесса, не зная о песочнице прогона: без
// подмены обоих на дом стенда строки «demo: XR-004 в Check» ложатся в живой
// ~/.devkit/notify.log (DK-283). HOME подменяется на дом стенда, а
// hooks/notify.py остаётся настоящим: символьная ссылка кладёт его туда же,
// где его находит notifyScript, - рядом с синтетическим корнем, тем же
// образцом, каким подменяет HOME смок (tools/dashboard/smoke.go).
func sandboxRealNotifier(t *testing.T, e *testEnv) {
	t.Helper()
	t.Setenv("HOME", e.home)
	checkout := devkitCheckout()
	if checkout == "" {
		t.Fatal("чекаут devkit не нашёлся: прогону нужен настоящий hooks/notify.py, указать чекаут: DEVKIT_HOME=<путь>")
	}
	root := filepath.Dir(e.proj)
	if err := os.MkdirAll(filepath.Join(root, "devkit"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(checkout, "hooks"), filepath.Join(root, "devkit", "hooks")); err != nil {
		t.Fatal(err)
	}
}

// Move в Check зовёт настоящий hooks/notify.py тем же порядком, что и живой
// прогон, - контракт формата журнала стоит проверять на нём, а не на
// выдумке теста (DK-112). Без подмены HOME он находит и пишет журнал по
// живому дому машины, и на ленту дашборда попадают чужие строки вроде
// «demo: XR-004 в Check» (DK-283). Регрессия: журнал стенда обязан лечь в
// дом песочницы прогона, а не куда-то ещё.
func TestTaskctlMoveNotifiesIntoSandboxHome(t *testing.T) {
	e, _, _ := tasksEnv(t)
	goalDoc(t, e, "XR-004", "# XR-004: Четвёртая\n\n## Сценарий проверки\n\nАгентский: прогнать тесты.\n")
	runTaskctl(t, e.proj, "move", "XR-004", "check")

	logPath := filepath.Join(e.home, ".devkit", "notify.log")
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("журнал уведомителя не лёг в дом песочницы %s: %v", logPath, err)
	}
	if !strings.Contains(string(data), "task_check") {
		t.Fatalf("журнал песочницы без повода task_check: %s", data)
	}
}

func taskURL(e *testEnv, id, tail string) string {
	return e.srv.URL + "/api/projects/demo/tasks/" + id + tail
}

// getTask читает ответ ручки задачи разобранным: так проверяется и сам JSON.
func getTask(t *testing.T, c *http.Client, e *testEnv, id string) map[string]any {
	t.Helper()
	resp := doReq(t, c, "GET", taskURL(e, id, ""), "")
	text := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("чтение задачи %s: %d %s", id, resp.StatusCode, text)
	}
	var v map[string]any
	if err := json.Unmarshal([]byte(text), &v); err != nil {
		t.Fatalf("ответ задачи не разобрался: %v\n%s", err, text)
	}
	return v
}

func taskRowField(t *testing.T, task map[string]any, name string) any {
	t.Helper()
	row, ok := task["row"].(map[string]any)
	if !ok {
		t.Fatalf("в ответе нет строки доски: %v", task)
	}
	return row[name]
}

// depIDs вынимает ID одной стороны зависимостей вместе с заголовками: пустой
// заголовок значит, что сервер не сходил на доску за соседями.
func depIDs(t *testing.T, task map[string]any, side string) []string {
	t.Helper()
	list, ok := task[side].([]any)
	if !ok {
		t.Fatalf("в ответе нет стороны %q: %v", side, task)
	}
	out := []string{}
	for _, item := range list {
		d, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("сторона %q не разобралась: %v", side, item)
		}
		out = append(out, fmt.Sprintf("%v %v", d["id"], d["title"]))
	}
	return out
}

// Правка заголовка, цены и одного слагаемого ранга: строка на доске меняется,
// сумму R и бакет P пересчитывает taskctl, а Backlog переставляется по рангу.
func TestTaskPatchRowAndRank(t *testing.T) {
	e, c, gitLog := tasksEnv(t)

	resp := doReq(t, c, "PATCH", taskURL(e, "XR-002", ""),
		`{"title": "Вторая, переписанная", "cost": "L", "r_parts": [75, null, null, null, null]}`)
	text := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("правка строки: %d %s", resp.StatusCode, text)
	}

	task := getTask(t, c, e, "XR-002")
	if got := taskRowField(t, task, "title"); got != "Вторая, переписанная" {
		t.Errorf("заголовок не поменялся: %v", got)
	}
	if got := taskRowField(t, task, "cost"); got != "L" {
		t.Errorf("цена не поменялась: %v", got)
	}
	// 75+2+1+0+2 = 80, бакет P0 при R >= 75: считает утилита, дашборд только
	// передал слагаемое.
	if got := taskRowField(t, task, "r"); got != float64(80) {
		t.Errorf("сумма R не пересчитана: %v", got)
	}
	if got := taskRowField(t, task, "p"); got != "P0" {
		t.Errorf("бакет P не пересчитан: %v", got)
	}
	// Остальные слагаемые остались прежними: правилось одно.
	if got := fmt.Sprint(taskRowField(t, task, "r_parts")); got != "[75 2 1 0 2]" {
		t.Errorf("соседние слагаемые поехали: %v", got)
	}

	// Порядок строк выводится из ранга: XR-002 встала выше XR-001 сама.
	resp = doReq(t, c, "GET", e.srv.URL+"/api/projects/demo/board", "")
	board := body(t, resp)
	first, second := strings.Index(board, "XR-002"), strings.Index(board, "XR-001")
	if first < 0 || second < 0 || first > second {
		t.Errorf("Backlog не отсортирован по рангу после правки: %s", board)
	}

	git := readFile(t, gitLog)
	for _, want := range []string{"add -- docs/TASKS.md", "docs(tasks): XR-002 правка строки с дашборда", "push"} {
		if !strings.Contains(git, want) {
			t.Errorf("в вызовах git нет %q: %s", want, git)
		}
	}
}

// Зависимости видны в обе стороны: XR-002 ждёт XR-001 и держит XR-003,
// соседи названы заголовками, а не голыми ID.
func TestTaskDepsBothSides(t *testing.T) {
	e, c, _ := tasksEnv(t)

	task := getTask(t, c, e, "XR-002")
	if got := depIDs(t, task, "after"); len(got) != 1 || got[0] != "XR-001 Первая" {
		t.Errorf("сторона «после» не та: %v", got)
	}
	if got := depIDs(t, task, "blocks"); len(got) != 1 || got[0] != "XR-003 Третья" {
		t.Errorf("сторона «держит» не та: %v", got)
	}
	// У краёв цепочки по одной стороне, и пустая сторона это пустой список, а
	// не пропавшее поле.
	head := getTask(t, c, e, "XR-001")
	if got := depIDs(t, head, "after"); len(got) != 0 {
		t.Errorf("у начала цепочки взялось «после»: %v", got)
	}
	if got := depIDs(t, head, "blocks"); len(got) != 1 || got[0] != "XR-002 Вторая" {
		t.Errorf("начало цепочки не держит XR-002: %v", got)
	}
}

// Зависимость ставится и снимается через API, а рубежи остаются в утилите:
// цикл она не пропускает и говорит это своими словами.
func TestTaskDepAddAndRemove(t *testing.T) {
	e, c, gitLog := tasksEnv(t)

	resp := doReq(t, c, "POST", taskURL(e, "XR-004", "/deps"), `{"id": "XR-001"}`)
	text := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("зависимость XR-004 после XR-001: %d %s", resp.StatusCode, text)
	}
	if got := depIDs(t, getTask(t, c, e, "XR-004"), "after"); len(got) != 1 || got[0] != "XR-001 Первая" {
		t.Errorf("зависимость не встала: %v", got)
	}
	if git := readFile(t, gitLog); !strings.Contains(git, "docs(tasks): XR-004 зависимость от XR-001 с дашборда") {
		t.Errorf("коммит доски не назвал зависимость: %s", git)
	}

	resp = doReq(t, c, "DELETE", taskURL(e, "XR-004", "/deps/XR-001"), "")
	text = body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("снятие зависимости: %d %s", resp.StatusCode, text)
	}
	if got := depIDs(t, getTask(t, c, e, "XR-004"), "after"); len(got) != 0 {
		t.Errorf("зависимость не снялась: %v", got)
	}

	// Цикл: XR-001 после XR-003, которая уже ждёт XR-002, а та ждёт XR-001.
	resp = doReq(t, c, "POST", taskURL(e, "XR-001", "/deps"), `{"id": "XR-003"}`)
	text = body(t, resp)
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(text, "цикл зависимостей") {
		t.Errorf("цикл прошёл или отказ без слов утилиты: %d %s", resp.StatusCode, text)
	}
}

// Файл задачи заводит утилита (она же чинит ссылку в строке), а текст правится
// целиком и виден повторным чтением.
func TestTaskFileCreateAndEdit(t *testing.T) {
	e, c, gitLog := tasksEnv(t)
	path := filepath.Join(e.proj, "docs", "tasks", "XR-002.md")

	// Пока файла нет, ответ говорит это словами и не притворяется пустым.
	task := getTask(t, c, e, "XR-002")
	if note, _ := task["note"].(string); !strings.Contains(note, "taskctl file") {
		t.Errorf("отсутствие файла не названо словами: %v", task["note"])
	}
	resp := doReq(t, c, "PUT", taskURL(e, "XR-002", "/file"), `{"text": "# XR-002\n"}`)
	text := body(t, resp)
	if resp.StatusCode != http.StatusNotFound || !strings.Contains(text, "Завести файл") {
		t.Errorf("правка несуществующего файла: %d %s", resp.StatusCode, text)
	}

	resp = doReq(t, c, "POST", taskURL(e, "XR-002", "/file"), "{}")
	text = body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("заведение файла: %d %s", resp.StatusCode, text)
	}
	if !isFile(path) {
		t.Fatalf("файла docs/tasks/XR-002.md нет после заведения")
	}
	task = getTask(t, c, e, "XR-002")
	if link, _ := taskRowField(t, task, "link").(string); !strings.Contains(link, "tasks/XR-002.md") {
		t.Errorf("ссылка в строке не почищена утилитой: %v", link)
	}

	resp = doReq(t, c, "PUT", taskURL(e, "XR-002", "/file"),
		`{"text": "# XR-002: Вторая\n\n## Чего хотим\n\nПравка с телефона."}`)
	text = body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("правка файла: %d %s", resp.StatusCode, text)
	}
	if doc := readFile(t, path); !strings.HasSuffix(doc, "Правка с телефона.\n") {
		t.Errorf("текст не доехал до файла:\n%s", doc)
	}
	if got, _ := getTask(t, c, e, "XR-002")["text"].(string); !strings.Contains(got, "Правка с телефона.") {
		t.Errorf("правка не видна повторным чтением: %q", got)
	}
	git := readFile(t, gitLog)
	for _, want := range []string{
		"docs(tasks): XR-002 файл задачи заведён с дашборда",
		"docs(tasks): XR-002 правка файла задачи с дашборда",
		"add -- docs/tasks/XR-002.md",
	} {
		if !strings.Contains(git, want) {
			t.Errorf("в вызовах git нет %q: %s", want, git)
		}
	}
}

// Отказы называются словами, а доска остаётся нетронутой: кривое слагаемое
// ранга отбивает утилита, пустая правка и попытка переставить строку мимо
// ранга отбиваются на месте.
func TestTaskPatchRefusals(t *testing.T) {
	e, c, _ := tasksEnv(t)
	boardPath := filepath.Join(e.proj, "docs", "TASKS.md")
	before := readFile(t, boardPath)

	cases := []struct {
		name, id, body string
		code           int
		want           string
	}{
		{"кривая серьёзность", "XR-002", `{"r_parts": [3, null, null, null, null]}`,
			http.StatusBadRequest, "серьёзность"},
		{"слагаемых не пять", "XR-002", `{"r_parts": [25, 2]}`,
			http.StatusBadRequest, "жду 5 по RANKING.md"},
		{"место мимо ранга", "XR-002", `{"position": 1}`,
			http.StatusBadRequest, "ставить строку на место N нечем"},
		{"нет строки", "XR-777", `{"cost": "S"}`,
			http.StatusNotFound, "нет строки XR-777"},
		{"ранг и слагаемые разом", "XR-002", `{"rank": "25+2+1+0+2", "r_parts": [25, 2, 1, 0, 2]}`,
			http.StatusBadRequest, "но не оба сразу"},
	}
	for _, tc := range cases {
		resp := doReq(t, c, "PATCH", taskURL(e, tc.id, ""), tc.body)
		text := body(t, resp)
		if resp.StatusCode != tc.code || !strings.Contains(text, tc.want) {
			t.Errorf("%s: %d %s, ожидал %d со словами %q", tc.name, resp.StatusCode, text, tc.code, tc.want)
		}
	}
	if after := readFile(t, boardPath); after != before {
		t.Errorf("отбитая правка тронула доску:\n%s", after)
	}
}

// Кривой ID отбивается ситом до похода на доску, и на всех шести ручках:
// в путь ID приезжает из адреса, и «..%2F» доходит до обработчика уже с
// разобранным слэшем. Дырка тут это обход пути к чужому файлу, поэтому
// проверяются обе стороны, и {id}, и {dep}.
func TestTaskIDSieve(t *testing.T) {
	e, c, _ := tasksEnv(t)
	const bad = "..%2FXR-002"
	calls := []struct{ method, url, body string }{
		{"GET", taskURL(e, bad, ""), ""},
		{"PATCH", taskURL(e, bad, ""), `{"cost": "S"}`},
		{"POST", taskURL(e, bad, "/file"), "{}"},
		{"PUT", taskURL(e, bad, "/file"), `{"text": "текст"}`},
		{"POST", taskURL(e, bad, "/deps"), `{"id": "XR-001"}`},
		{"DELETE", taskURL(e, bad, "/deps/XR-001"), ""},
		// Сито ID второй стороны: та же дырка приезжает и в теле, и в хвосте
		// пути зависимости. В теле она едет JSON-ом, без экранирования.
		{"POST", taskURL(e, "XR-004", "/deps"), `{"id": "../XR-001"}`},
		{"DELETE", taskURL(e, "XR-004", "/deps/"+bad), ""},
	}
	for _, call := range calls {
		resp := doReq(t, c, call.method, call.url, call.body)
		text := body(t, resp)
		if resp.StatusCode != http.StatusBadRequest || !strings.Contains(text, "не похоже на ID задачи") {
			t.Errorf("%s %s: %d %s, ожидал 400 со словами про ID", call.method, call.url, resp.StatusCode, text)
		}
	}
}

// Без входа не отдаётся и не принимается ни одна правка, чужой Origin
// отбивается до всякой записи.
func TestTaskEditAuthAndOrigin(t *testing.T) {
	e, c, _ := tasksEnv(t)
	boardPath := filepath.Join(e.proj, "docs", "TASKS.md")
	before := readFile(t, boardPath)
	plain := plainClient()

	calls := []struct{ method, url, body string }{
		{"GET", taskURL(e, "XR-002", ""), ""},
		{"PATCH", taskURL(e, "XR-002", ""), `{"cost": "S"}`},
		{"POST", taskURL(e, "XR-002", "/file"), "{}"},
		{"PUT", taskURL(e, "XR-002", "/file"), `{"text": "текст"}`},
		{"POST", taskURL(e, "XR-004", "/deps"), `{"id": "XR-001"}`},
		{"DELETE", taskURL(e, "XR-002", "/deps/XR-001"), ""},
	}
	for _, call := range calls {
		resp := doReq(t, plain, call.method, call.url, call.body)
		text := body(t, resp)
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s %s без входа: %d, ожидал 401", call.method, call.url, resp.StatusCode)
		}
		if strings.Contains(text, "Вторая") {
			t.Errorf("%s %s без входа отдал строку доски: %s", call.method, call.url, text)
		}
	}
	for _, call := range calls[1:] {
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
			t.Errorf("%s %s с чужим Origin: %d, ожидал 403", call.method, call.url, resp.StatusCode)
		}
	}
	if after := readFile(t, boardPath); after != before {
		t.Errorf("отбитый запрос тронул доску:\n%s", after)
	}
	if isFile(filepath.Join(e.proj, "docs", "tasks", "XR-002.md")) {
		t.Errorf("отбитый запрос завёл файл задачи")
	}
}

// Провал пуша правку не отменяет и не съедает молча: строка изменена, причина
// названа полем note.
func TestTaskPatchPushFailureNamed(t *testing.T) {
	e, c, gitLog := tasksEnv(t)
	writeScript(t, e.bin, "git", fmt.Sprintf(
		"echo \"$@\" >> %q\ncase \"$3\" in\npush)\n  echo 'нет доступа к origin' >&2; exit 1;;\nesac\nexit 0", gitLog))

	resp := doReq(t, c, "PATCH", taskURL(e, "XR-002", ""), `{"cost": "S"}`)
	text := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("правка при сломанном push: %d %s", resp.StatusCode, text)
	}
	if !strings.Contains(text, "git push не прошёл") || !strings.Contains(text, "нет доступа к origin") {
		t.Errorf("провал пуша не назван: %s", text)
	}
	if got := taskRowField(t, getTask(t, c, e, "XR-002"), "cost"); got != "S" {
		t.Errorf("правка пропала вместе с провалом пуша: %v", got)
	}
}

// Поправка на баг это шкала RANKING.md, а не свободное поле: у типа task
// слагаемое отбивается ручкой, у bug оно штатно. Тип и слагаемые едут одним
// запросом, поэтому сверяется то, что получится после правки.
func TestTaskPatchBugPartByType(t *testing.T) {
	e, c, _ := tasksEnv(t)
	boardPath := filepath.Join(e.proj, "docs", "TASKS.md")
	before := readFile(t, boardPath)

	// Строка типа task: поправка на баг отбивается и списком слагаемых, и
	// разбивкой строкой.
	for _, in := range []string{
		`{"r_parts": [null, null, null, 5, null]}`,
		`{"rank": "25+2+1+5+2"}`,
		`{"type": "task", "r_parts": [null, null, null, 5, null]}`,
	} {
		resp := doReq(t, c, "PATCH", taskURL(e, "XR-002", ""), in)
		text := body(t, resp)
		if resp.StatusCode != http.StatusBadRequest || !strings.Contains(text, "поправка на баг у типа task не ставится") {
			t.Errorf("поправка на баг у task прошла: %s -> %d %s", in, resp.StatusCode, text)
		}
	}
	if after := readFile(t, boardPath); after != before {
		t.Errorf("отбитая правка тронула доску:\n%s", after)
	}

	// У дефекта то же слагаемое штатно и встаёт вместе со сменой типа.
	resp := doReq(t, c, "PATCH", taskURL(e, "XR-002", ""),
		`{"type": "bug", "r_parts": [null, null, null, 5, null]}`)
	text := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("поправка на баг у bug не прошла: %d %s", resp.StatusCode, text)
	}
	task := getTask(t, c, e, "XR-002")
	if got := fmt.Sprint(taskRowField(t, task, "r_parts")); got != "[25 2 1 5 2]" {
		t.Errorf("разбивка ранга не та: %v", got)
	}
	if got := taskRowField(t, task, "type"); got != "bug" {
		t.Errorf("тип не поменялся: %v", got)
	}

	// Обратный ход: тип назад в task при стоящей поправке отбивается, менять
	// надо оба поля разом, и это же делает единая кнопка экрана.
	resp = doReq(t, c, "PATCH", taskURL(e, "XR-002", ""), `{"type": "task"}`)
	text = body(t, resp)
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(text, "смени тип на bug") {
		t.Errorf("возврат типа в task при поправке на баг: %d %s", resp.StatusCode, text)
	}
	resp = doReq(t, c, "PATCH", taskURL(e, "XR-002", ""),
		`{"type": "task", "r_parts": [null, null, null, 0, null]}`)
	if text = body(t, resp); resp.StatusCode != http.StatusOK {
		t.Fatalf("возврат типа вместе со снятой поправкой: %d %s", resp.StatusCode, text)
	}
}

// Экран задачи честен словами: слагаемые ранга названы по RANKING.md, обе
// стороны зависимостей подписаны, а перетаскивания мимо ранга нет. Правка
// собрана одной формой: кнопка сохранения одна, отдельной кнопки правки нет.
func TestStaticTaskEditHonesty(t *testing.T) {
	text := readFile(t, filepath.Join("static", "app.js"))
	for _, want := range []string{
		"Серьёзность", "Ценность", "Неопределённость", "Поправка на баг", "Рычаг",
		"по RANKING.md",
		"перетаскивания мимо ранга нет",
		"Заблокировано задачами",
		"Блокирует выполнение задач",
		"Завести файл",
		"Сохранить",
		"Отменить правку",
		"поправка на баг у типа task не ставится",
		"Бакет считается из суммы ранга, рукой не ставится",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("в static/app.js нет надписи %q", want)
		}
	}
	if strings.Contains(text, `"Править"`) {
		t.Error("в static/app.js осталась отдельная кнопка «Править»: правка снова разваливается на два похода")
	}
	if n := strings.Count(text, `"Сохранить"`); n != 1 {
		t.Errorf("кнопок сохранения в static/app.js %d, жду одну на всю форму", n)
	}
}

// Единая кнопка склеивает две ручки на клиенте, и порядок тут держит правку:
// сначала строка (PATCH), потом файл (PUT), а отказ первой останавливает всё.
// Уехавший файл при отбитой строке это половина сохранённой правки, и
// заметить её потом нечем. Приём тот же, что у ранней проверки черновика в
// renderTask: честный признак порядка кода.
func TestStaticTaskSaveOrder(t *testing.T) {
	text := readFile(t, filepath.Join("static", "app.js"))
	cut := strings.Index(text, "async function saveTaskDraft(")
	if cut < 0 {
		t.Fatal("в static/app.js нет saveTaskDraft: единой кнопке нечем сохранять")
	}
	body := text[cut:]
	if stop := strings.Index(body, "\n}\n"); stop > 0 {
		body = body[:stop]
	}
	patch, put := strings.Index(body, `"PATCH"`), strings.Index(body, `"PUT"`)
	if patch < 0 || put < 0 {
		t.Fatalf("saveTaskDraft зовёт не обе ручки: PATCH %d, PUT %d", patch, put)
	}
	if patch > put {
		t.Error("в saveTaskDraft файл уезжает раньше строки: правка строки может не пройти уже после записи файла")
	}
	if !strings.Contains(body[patch:put], "return false") {
		t.Error("в saveTaskDraft PUT уходит, не спросив исход PATCH: отбитая строка оставит файл переписанным")
	}
	// Черновик держится до полного успеха: сброс раньше PUT потерял бы поля
	// на отказе второй ручки.
	if drop := strings.Index(body, "taskDraft.dirty = false"); drop < 0 || drop < put {
		t.Error("в saveTaskDraft черновик сбрасывается раньше записи файла: правка пропадёт на отказе PUT")
	}
}

// Живое обновление при открытой правке не подменяет ввод: пока в черновике
// есть правка, экран не перерисовывается, а уехавшая строка отмечается
// пометкой с кнопкой. Молчаливая подмена признана дефектом на пользовательской
// проверке.
func TestStaticTaskLiveUpdateKeepsDraft(t *testing.T) {
	text := readFile(t, filepath.Join("static", "app.js"))
	for _, want := range []string{
		"задача обновилась, перечитать",
		"Перечитать",
		"taskDraft.dirty",
		"function taskSeen(",
		"function taskStale(",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("в static/app.js нет части спокойного обновления %q", want)
		}
	}
	// Ранний возврат из renderTask при живом черновике: без него перерисовка
	// по фокусу окна затирает поля.
	cut := strings.Index(text, "async function renderTask(")
	if cut < 0 {
		t.Fatal("в static/app.js нет renderTask")
	}
	head := text[cut:]
	if stop := strings.Index(head, "groups.replaceChildren()"); stop < 0 ||
		!strings.Contains(head[:stop], "taskDraft.dirty") {
		t.Error("renderTask чистит экран, не спросив черновик: правка в форме затирается свежими данными")
	}
}

// Сохранение и действия стоят одной полосой над содержимым (макет
// «02 Задача»): отдельной карточки действий у задачи нет, надписи про пустую
// правку нет вовсе (о ней говорит погашенная кнопка), а «Отменить правку»
// появляется только тогда, когда отменять есть что.
func TestStaticTaskActionBar(t *testing.T) {
	app := readFile(t, filepath.Join("static", "app.js"))
	body := funcBody(t, app, "async function renderTask(")
	for _, want := range []string{`el("div", "card abar")`, "drop.hidden = true", "drop.hidden = !dirty",
		"save.disabled = !dirty", `el("span", "div")`, "taskActions(project, id, row, works)"} {
		if !strings.Contains(body, want) {
			t.Errorf("в полосе действий задачи нет %q", want)
		}
	}
	if !strings.Contains(funcBody(t, app, "function taskActions("), "Остановить агента") {
		t.Error("в полосе действий задачи нет стопа живой работы")
	}
	for _, gone := range []string{"Правки нет", `el("div", "card act")`, "Изменённое уедет одной кнопкой"} {
		if strings.Contains(app, gone) {
			t.Errorf("в static/app.js осталось %q: форма снова объясняет себя надписью", gone)
		}
	}
	if strings.Contains(body, "Порядок в Backlog выводится из ранга") {
		t.Error("надпись про порядок в Backlog вернулась на экран задачи")
	}
	css := readFile(t, filepath.Join("static", "style.css"))
	for _, want := range []string{".abar{", ".abar .div{", ".btn-danger{"} {
		if !strings.Contains(css, want) {
			t.Errorf("в static/style.css нет стиля полосы действий %q", want)
		}
	}
}

// Полоса действий задачи берёт действие из статуса строки, теми же словами,
// что и доска: у формы своей подписи нет, иначе экран задачи и строка обещали
// бы конвейеру разное. Заблокированная маркером задача действия не получает.
func TestStaticTaskActionBySection(t *testing.T) {
	app := readFile(t, filepath.Join("static", "app.js"))
	body := funcBody(t, app, "function taskActions(")
	for _, want := range []string{"actionLabel(row.sect)", "startRun(project, id)",
		"row.after && row.after.length", "wait.disabled = true",
		"taskActionHint(isGoal, row.sect, id)"} {
		if !strings.Contains(body, want) {
			t.Errorf("в полосе действий задачи нет %q", want)
		}
	}
	hint := funcBody(t, app, "function taskActionHint(")
	for _, want := range []string{"goal-run", "Начатую задачу конвейер продолжит",
		"Проверенную задачу конвейер закроет", "headless-сессия конвейера доски"} {
		if !strings.Contains(hint, want) {
			t.Errorf("надпись под действием не говорит про %q: откуда смотреть за работой, неясно", want)
		}
	}
}

// Пояснения ушли с экрана в подсказки по наведению: бакет P рассказывает о
// себе сам, дата стоит без слова «правка», последствия остановки висят на
// кнопке остановки.
func TestStaticTaskTips(t *testing.T) {
	app := readFile(t, filepath.Join("static", "app.js"))
	if !strings.Contains(funcBody(t, app, "function withTip("), "node.title = text") {
		t.Error("подсказка не садится на сам элемент: withTip не выставляет title")
	}
	for _, want := range []string{
		`withTip(p, P_HINT)`,
		`withTip(el("span", "stale dashed", row.moved)`,
		"дата последней правки задачи на доске",
		"Сессия агента будет завершена, при возобновлении состояние агента",
	} {
		if !strings.Contains(app, want) {
			t.Errorf("в static/app.js нет подсказки %q", want)
		}
	}
	for _, gone := range []string{`"правка " + row.moved`, `"правка строки "`, `"hint phint"`} {
		if strings.Contains(app, gone) {
			t.Errorf("в static/app.js осталась надпись-указка %q", gone)
		}
	}
	// Стоп называется одинаково везде: на задаче, на экране агента и в чате.
	if n := strings.Count(app, `"Остановить агента"`); n < 3 {
		t.Errorf("кнопок «Остановить агента» %d, жду три экрана: задача, агент, чат", n)
	}
}

// Зависимости названы словами, а маркер [после ...] со строки доски говорит
// то же самое: «после DK-248» требовало от читателя достроить, кто кого ждёт.
func TestStaticDepsNamedInWords(t *testing.T) {
	app := readFile(t, filepath.Join("static", "app.js"))
	chips := funcBody(t, app, "function rowChips(")
	for _, want := range []string{"заблокирована задачей ", "заблокирована задачами "} {
		if !strings.Contains(chips, want) {
			t.Errorf("в чипах строки нет %q", want)
		}
	}
	if strings.Contains(chips, `"после "`) {
		t.Error("в чипах строки остался маркер «после»: кто кого ждёт, читателю приходится достраивать")
	}
	deps := funcBody(t, app, "function depsCard(")
	for _, want := range []string{"Заблокировано задачами", "Блокирует выполнение задач"} {
		if !strings.Contains(deps, want) {
			t.Errorf("в карточке зависимостей нет заголовка %q", want)
		}
	}
}
