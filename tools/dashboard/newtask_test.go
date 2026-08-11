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

// Заведение задачи и черновика. Стенд тот же, что у правки строки: настоящий
// taskctl на фикстурной доске, git исполняемой фикстурой. Сверка идёт файлом,
// а не ответом ручки: черновик обязан лечь в накопитель, строка на доску.

// newResp читает ответ ручки заведения разобранным: клиенту из него нужен ID.
func newResp(t *testing.T, resp *http.Response, what string) map[string]any {
	t.Helper()
	text := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("%s: %d %s", what, resp.StatusCode, text)
	}
	var v map[string]any
	if err := json.Unmarshal([]byte(text), &v); err != nil {
		t.Fatalf("ответ %s не разобрался: %v\n%s", what, err, text)
	}
	return v
}

// Черновик ложится в docs/tasks/drafts/ с текстом и коммитом, а каталога
// накопителя на проекте до этого нет: заводит его сама утилита, и отказа тут
// быть не должно.
func TestDraftCreate(t *testing.T) {
	e, c, gitLog := tasksEnv(t)
	drafts := filepath.Join(e.proj, "docs", "tasks", "drafts")
	if _, err := os.Stat(drafts); !os.IsNotExist(err) {
		t.Fatalf("накопитель черновиков уже есть до первой записи: %v", err)
	}

	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/drafts",
		`{"text": "мысль с телефона: доска не заводится с дашборда"}`)
	got := newResp(t, resp, "запись черновика")
	id, _ := got["id"].(string)
	if id != "XR-005" {
		t.Fatalf("ID черновика не тот: %v", got)
	}
	if file, _ := got["file"].(string); file != "docs/tasks/drafts/XR-005.md" {
		t.Errorf("путь черновика не назван: %v", got["file"])
	}
	doc := readFile(t, filepath.Join(drafts, "XR-005.md"))
	if !strings.Contains(doc, "мысль с телефона: доска не заводится с дашборда") {
		t.Errorf("текст не доехал до файла черновика:\n%s", doc)
	}
	// Строки на доске у черновика нет: он ждёт грумминга.
	if board := readFile(t, filepath.Join(e.proj, "docs", "TASKS.md")); strings.Contains(board, "XR-005") {
		t.Errorf("черновик встал строкой на доску:\n%s", board)
	}
	git := readFile(t, gitLog)
	for _, want := range []string{
		"add -- docs/tasks/drafts/XR-005.md",
		"docs(tasks): XR-005 черновик записан с дашборда",
		"push",
	} {
		if !strings.Contains(git, want) {
			t.Errorf("в вызовах git нет %q: %s", want, git)
		}
	}
}

// Текст едет на вход подпроцесса, а не аргументом: аргументом мысль из одного
// слова латиницей отбивает страж подкоманд taskctl (draft list, defer, attach,
// drop), а начатая с дефиса уходит в разбор флагов. С телефона приходит и то,
// и другое, и терять такую запись нельзя.
func TestDraftAwkwardText(t *testing.T) {
	e, c, _ := tasksEnv(t)
	for i, text := range []string{"fix", "-p не работает после обновления"} {
		resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/drafts",
			`{"text": `+strconv.Quote(text)+`}`)
		got := newResp(t, resp, "запись черновика "+text)
		id, _ := got["id"].(string)
		if id == "" {
			t.Fatalf("черновик %q не завёлся: %v", text, got)
		}
		doc := readFile(t, filepath.Join(e.proj, "docs", "tasks", "drafts", id+".md"))
		if !strings.Contains(doc, text) {
			t.Errorf("текст %q не доехал до файла (запись %d):\n%s", text, i, doc)
		}
	}
}

// Текст за пределом отбивается своими словами и до утилиты не доезжает: в
// черновик кладётся мысль, а не вложение, и молчаливо обрезанная запись хуже
// отказа. Предел считается по телу запроса, поэтому кладётся текст заведомо
// длиннее draftTextLimit.
func TestDraftTextLimit(t *testing.T) {
	e, c, gitLog := tasksEnv(t)

	long := strings.Repeat("мысль без конца, ", draftTextLimit/8)
	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/drafts",
		`{"text": `+strconv.Quote(long)+`}`)
	text := body(t, resp)
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(text, "длиннее предела") {
		t.Fatalf("текст за пределом: %d %s, ожидал 400 со словами про предел", resp.StatusCode, text)
	}
	if _, err := os.Stat(filepath.Join(e.proj, "docs", "tasks", "drafts")); !os.IsNotExist(err) {
		t.Errorf("отбитый текст завёл накопитель черновиков: %v", err)
	}
	if git := readFile(t, gitLog); strings.Contains(git, "черновик записан с дашборда") {
		t.Errorf("отбитый текст дошёл до коммита доски: %s", git)
	}

	// Мысль в предел укладывается и записывается тем же полем: рубеж стоит на
	// вложении, а не на длинной записи.
	fits := strings.TrimSpace(strings.Repeat("мысль с телефона, ", 64))
	resp = doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/drafts",
		`{"text": `+strconv.Quote(fits)+`}`)
	got := newResp(t, resp, "запись длинного черновика в пределе")
	id, _ := got["id"].(string)
	if id == "" {
		t.Fatalf("черновик в пределе не завёлся: %v", got)
	}
	if doc := readFile(t, filepath.Join(e.proj, "docs", "tasks", "drafts", id+".md")); !strings.Contains(doc, fits) {
		t.Errorf("текст в пределе не доехал до файла целиком:\n%s", doc)
	}
}

// Полная строка встаёт на доску руками утилиты: ранг, бакет P и место в
// Backlog считает она, файл задачи заводится тем же заходом по флагу.
func TestTaskCreateRow(t *testing.T) {
	e, c, gitLog := tasksEnv(t)

	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/tasks",
		`{"title": "Пятая, с дашборда", "type": "task", "cost": "S", "r_parts": [50, 4, 2, 0, 1], "file": true}`)
	got := newResp(t, resp, "заведение строки")
	id, _ := got["id"].(string)
	if id != "XR-005" {
		t.Fatalf("ID новой строки не тот: %v", got)
	}
	if file, _ := got["file"].(string); file != "docs/tasks/XR-005.md" {
		t.Errorf("файл задачи не заведён: %v", got)
	}
	if !isFile(filepath.Join(e.proj, "docs", "tasks", "XR-005.md")) {
		t.Errorf("файла docs/tasks/XR-005.md нет после заведения")
	}
	board := readFile(t, filepath.Join(e.proj, "docs", "TASKS.md"))
	if !strings.Contains(board, "Пятая, с дашборда") {
		t.Errorf("строки нет на доске:\n%s", board)
	}
	// 50+4+2+0+1 = 57, бакет P1: считает утилита, дашборд передал слагаемые.
	task := getTask(t, c, e, "XR-005")
	if r := taskRowField(t, task, "r"); r != float64(57) {
		t.Errorf("сумма R не та: %v", r)
	}
	if p := taskRowField(t, task, "p"); p != "P1" {
		t.Errorf("бакет P не тот: %v", p)
	}
	if cost := taskRowField(t, task, "cost"); cost != "S" {
		t.Errorf("цена не та: %v", cost)
	}
	if link, _ := taskRowField(t, task, "link").(string); !strings.Contains(link, "tasks/XR-005.md") {
		t.Errorf("ссылка на файл задачи не встала в строку: %v", link)
	}
	git := readFile(t, gitLog)
	for _, want := range []string{
		"add -- docs/TASKS.md docs/tasks/XR-005.md",
		"docs(tasks): XR-005 строка и файл задачи заведены с дашборда",
	} {
		if !strings.Contains(git, want) {
			t.Errorf("в вызовах git нет %q: %s", want, git)
		}
	}
}

// Без флага файла заводится одна строка: файл дописывается позже кнопкой
// экрана задачи, и коммит доски называет ровно то, что случилось.
func TestTaskCreateRowWithoutFile(t *testing.T) {
	e, c, gitLog := tasksEnv(t)

	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/tasks",
		`{"title": "Пятая", "r_parts": [25, 1, 1, 0, 0]}`)
	got := newResp(t, resp, "заведение строки без файла")
	if _, hit := got["file"]; hit {
		t.Errorf("файл задачи завёлся без просьбы: %v", got)
	}
	if isFile(filepath.Join(e.proj, "docs", "tasks", "XR-005.md")) {
		t.Errorf("файл docs/tasks/XR-005.md завёлся без просьбы")
	}
	// Тип по умолчанию task: строка без него всё равно полная.
	if typ := taskRowField(t, getTask(t, c, e, "XR-005"), "type"); typ != "task" {
		t.Errorf("тип по умолчанию не task: %v", typ)
	}
	if git := readFile(t, gitLog); !strings.Contains(git, "docs(tasks): XR-005 строка заведена с дашборда") {
		t.Errorf("коммит доски не назвал заведение строки: %s", git)
	}
}

// Отказы называются словами, и доска остаётся нетронутой: кривой ранг и
// пустой заголовок отбивает утилита, поправку на баг у типа task ручка, теми
// же словами, что и PATCH.
func TestTaskCreateRefusals(t *testing.T) {
	e, c, _ := tasksEnv(t)
	boardPath := filepath.Join(e.proj, "docs", "TASKS.md")
	before := readFile(t, boardPath)

	cases := []struct {
		name, body, want string
	}{
		{"кривая серьёзность", `{"title": "Пятая", "r_parts": [3, 1, 1, 0, 0]}`, "серьёзность"},
		{"без ранга", `{"title": "Пятая"}`, "разбивке ранга"},
		{"без заголовка", `{"r_parts": [25, 1, 1, 0, 0]}`, "нужен --title"},
		{"слагаемых не пять", `{"title": "Пятая", "r_parts": [25, 1]}`, "жду 5 по RANKING.md"},
		{"поправка на баг у task", `{"title": "Пятая", "r_parts": [25, 1, 1, 5, 0]}`,
			"поправка на баг у типа task не ставится"},
		{"ранг и слагаемые разом", `{"title": "Пятая", "rank": "25+1+1+0+0", "r_parts": [25, 1, 1, 0, 0]}`,
			"но не оба сразу"},
		{"незнакомый тип", `{"title": "Пятая", "type": "story", "r_parts": [25, 1, 1, 0, 0]}`, "тип"},
	}
	for _, tc := range cases {
		resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/tasks", tc.body)
		text := body(t, resp)
		if resp.StatusCode != http.StatusBadRequest || !strings.Contains(text, tc.want) {
			t.Errorf("%s: %d %s, ожидал 400 со словами %q", tc.name, resp.StatusCode, text, tc.want)
		}
	}
	// Пустой черновик отбивается там же и не заводит файла.
	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/drafts", `{"text": "   "}`)
	if text := body(t, resp); resp.StatusCode != http.StatusBadRequest ||
		!strings.Contains(text, "пустой черновик") {
		t.Errorf("пустой черновик: %d %s", resp.StatusCode, text)
	}
	if after := readFile(t, boardPath); after != before {
		t.Errorf("отбитое заведение тронуло доску:\n%s", after)
	}
	if _, err := os.Stat(filepath.Join(e.proj, "docs", "tasks", "drafts")); !os.IsNotExist(err) {
		t.Errorf("отбитый черновик завёл накопитель: %v", err)
	}
	// У поправки на баг тот же отказ и у типа bug её принимают: рубеж про тип,
	// а не про само слагаемое.
	resp = doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/tasks",
		`{"title": "Пятая, дефект", "type": "bug", "r_parts": [25, 1, 1, 5, 0]}`)
	if text := body(t, resp); resp.StatusCode != http.StatusOK {
		t.Fatalf("поправка на баг у типа bug: %d %s", resp.StatusCode, text)
	}
}

// Без входа не заводится ничего, чужой Origin отбивается до всякой записи.
func TestNewTaskAuthAndOrigin(t *testing.T) {
	e, c, _ := tasksEnv(t)
	boardPath := filepath.Join(e.proj, "docs", "TASKS.md")
	before := readFile(t, boardPath)
	calls := []struct{ url, body string }{
		{e.srv.URL + "/api/projects/demo/tasks", `{"title": "Пятая", "r_parts": [25, 1, 1, 0, 0]}`},
		{e.srv.URL + "/api/projects/demo/drafts", `{"text": "мысль"}`},
	}
	for _, call := range calls {
		resp := doReq(t, plainClient(), "POST", call.url, call.body)
		if text := body(t, resp); resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("POST %s без входа: %d %s, ожидал 401", call.url, resp.StatusCode, text)
		}
		req, err := http.NewRequest("POST", call.url, strings.NewReader(call.body))
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
			t.Errorf("POST %s с чужим Origin: %d, ожидал 403", call.url, resp.StatusCode)
		}
	}
	if after := readFile(t, boardPath); after != before {
		t.Errorf("отбитый запрос тронул доску:\n%s", after)
	}
	if _, err := os.Stat(filepath.Join(e.proj, "docs", "tasks", "drafts")); !os.IsNotExist(err) {
		t.Errorf("отбитый запрос завёл накопитель черновиков: %v", err)
	}
}

// Экран заведения честен словами: черновик стоит первым и назван черновиком,
// полная строка лежит под разворотом со слагаемыми из RANKING.md, кнопка
// заведения есть и на доске, и на главной.
func TestStaticNewTaskForm(t *testing.T) {
	text := readFile(t, filepath.Join("static", "app.js"))
	for _, want := range []string{
		"Новая задача",
		"Записать черновик",
		"Что нужно сделать и зачем",
		"Полная задача",
		"Завести задачу",
		"завести файл задачи по шаблону (taskctl file)",
		"docs/tasks/drafts/",
		"function renderNew(",
		"function newTaskButton(",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("в static/app.js нет надписи %q", want)
		}
	}
	// Кнопка на обоих экранах: на доске и на главной, иначе с телефона до
	// заведения надо сначала дойти до нужного проекта.
	for _, fn := range []string{"function renderBoard(", "function renderHome("} {
		cut := strings.Index(text, fn)
		if cut < 0 {
			t.Fatalf("в static/app.js нет %s", fn)
		}
		part := text[cut:]
		if stop := strings.Index(part, "\n}\n"); stop > 0 {
			part = part[:stop]
		}
		if !strings.Contains(part, "newTaskButton(") {
			t.Errorf("в %s нет кнопки заведения", fn)
		}
	}
	// Слагаемые ранга не переписаны второй раз: форма берёт их из того же
	// списка, что и экран задачи.
	if n := strings.Count(text, "const RANK_PARTS"); n != 1 {
		t.Errorf("списков слагаемых в static/app.js %d, жду один на всю статику", n)
	}
	// Повторная отправка не плодит дублей: кнопки гаснут на время запроса.
	cut := strings.Index(text, "async function sendNew(")
	if cut < 0 {
		t.Fatal("в static/app.js нет sendNew: кнопку заведения нечем гасить")
	}
	part := text[cut:]
	if stop := strings.Index(part, "\n}\n"); stop > 0 {
		part = part[:stop]
	}
	if !strings.Contains(part, "disabled = true") || !strings.Contains(part, "finally") {
		t.Error("sendNew не гасит кнопки на время запроса либо не включает их назад")
	}
}
