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
		`{"text": "мысль с телефона: доска не заводится с дашборда", "prio": "mid"}`)
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
	// Строки на доске у черновика нет: он ждёт груминга.
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
			`{"text": `+strconv.Quote(text)+`, "prio": "mid"}`)
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
		`{"text": `+strconv.Quote(long)+`, "prio": "mid"}`)
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
	// вложении, а не на длинной записи. Первая строка идёт заголовком, и
	// длинное тело едет под ней: простыню одной строкой утилита отбивает сама
	// (TASKFORM.md, форма черновика).
	tail := strings.TrimSpace(strings.Repeat("мысль с телефона, ", 64))
	fits := "мысль с телефона\n\n" + tail
	resp = doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/drafts",
		`{"text": `+strconv.Quote(fits)+`, "prio": "mid"}`)
	got := newResp(t, resp, "запись длинного черновика в пределе")
	id, _ := got["id"].(string)
	if id == "" {
		t.Fatalf("черновик в пределе не завёлся: %v", got)
	}
	// Простыня одной строкой отбивается порогом первой строки утилиты, и отказ
	// приходит без совета про stdin: с дашборда текст и так идёт на stdin.
	resp = doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/drafts",
		`{"text": `+strconv.Quote(strings.TrimSpace(strings.Repeat("мысль с телефона, ", 8)))+`, "prio": "mid"}`)
	text = body(t, resp)
	if resp.StatusCode == http.StatusOK || !strings.Contains(text, "72") || strings.Contains(text, "stdin") {
		t.Fatalf("отказ по первой строке: %d %s, ожидал отказ с порогом и без совета про stdin", resp.StatusCode, text)
	}
	// Тело без разметки утилита кладёт в подраздел «Ситуация» под заголовком.
	if doc := readFile(t, filepath.Join(e.proj, "docs", "tasks", "drafts", id+".md")); !strings.HasPrefix(doc, "# "+id+": мысль с телефона\n") || !strings.Contains(doc, "### Ситуация\n\n"+tail+"\n") {
		t.Errorf("текст в пределе не доехал до файла целиком:\n%s", doc)
	}
}

// Полная строка встаёт на доску руками утилиты: ранг, бакет P и место в
// Backlog считает она, файл задачи заводится тем же заходом.
func TestTaskCreateRow(t *testing.T) {
	e, c, gitLog := tasksEnv(t)

	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/tasks",
		`{"title": "Пятая, с дашборда", "type": "task", "cost": "S", "r_parts": [50, 4, 2, 0, 1]}`)
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
	// 50+4+2+0+1 = 57 плюс бонус за цену S, итог 59 и бакет P1. Считает всё
	// утилита, дашборд передал слагаемые и цену.
	task := getTask(t, c, e, "XR-005")
	if r := taskRowField(t, task, "r"); r != float64(59) {
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

// Файл задачи заводится вместе со строкой всегда (DK-394): ручка не предлагает
// строку без файла, и коммит создания берёт оба пути, а не только доску.
func TestTaskCreateRowAlwaysWithFile(t *testing.T) {
	e, c, gitLog := tasksEnv(t)

	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/tasks",
		`{"title": "Пятая", "r_parts": [25, 1, 1, 0, 0]}`)
	got := newResp(t, resp, "заведение строки с файлом")
	if file, _ := got["file"].(string); file != "docs/tasks/XR-005.md" {
		t.Errorf("файл задачи не назван в ответе: %v", got)
	}
	if !isFile(filepath.Join(e.proj, "docs", "tasks", "XR-005.md")) {
		t.Errorf("файла docs/tasks/XR-005.md нет после заведения")
	}
	// Тип по умолчанию task: строка без него всё равно полная.
	if typ := taskRowField(t, getTask(t, c, e, "XR-005"), "type"); typ != "task" {
		t.Errorf("тип по умолчанию не task: %v", typ)
	}
	// Файл едет в коммит создания строки безусловно: без него запушенная доска
	// ссылалась бы ячейкой на файл, которого в origin нет.
	if git := readFile(t, gitLog); !strings.Contains(git, "docs(tasks): XR-005 строка и файл задачи заведены с дашборда") {
		t.Errorf("коммит доски не назвал заведение строки и файла: %s", git)
	}
	if git := readFile(t, gitLog); !strings.Contains(git, "add -- docs/TASKS.md docs/tasks/XR-005.md") {
		t.Errorf("коммит создания не взял файл задачи: %s", git)
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
	// Черновик без уровня разбора отбивается на сервере, не доходя до утилиты:
	// уровень спрашивается на записи (DK-520), и отказ говорит про шкалу, а не
	// про форму команды taskctl.
	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/drafts", `{"text": "мысль без уровня"}`)
	if text := body(t, resp); resp.StatusCode != http.StatusBadRequest ||
		!strings.Contains(text, "уровень разбора") {
		t.Errorf("черновик без уровня: %d %s, ожидал 400 со словами про уровень", resp.StatusCode, text)
	}
	if _, err := os.Stat(filepath.Join(e.proj, "docs", "tasks", "drafts")); !os.IsNotExist(err) {
		t.Errorf("черновик без уровня завёл накопитель: %v", err)
	}
	resp = doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/drafts", `{"text": "мысль", "prio": "срочно"}`)
	if text := body(t, resp); resp.StatusCode != http.StatusBadRequest ||
		!strings.Contains(text, "уровень разбора") {
		t.Errorf("уровень мимо шкалы: %d %s, ожидал 400", resp.StatusCode, text)
	}

	// Пустой черновик отбивается там же и не заводит файла.
	resp = doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/drafts", `{"text": "   ", "prio": "mid"}`)
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

// Вид приёмки едет в add с формы (DK-301): умолчание агентское и без суффикса,
// смешанный и пользовательский несут барьер и причину, и причина ложится в
// раздел «Приёмка» файла задачи рядом со строкой барьера.
func TestTaskCreateAcceptKinds(t *testing.T) {
	e, c, gitLog := tasksEnv(t)

	// Умолчание: без поля accept строка агентская, суффикса вида нет.
	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/tasks",
		`{"title": "Пятая, агентская по умолчанию", "r_parts": [25, 1, 1, 0, 0]}`)
	newResp(t, resp, "заведение агентской строки")
	if title, _ := taskRowField(t, getTask(t, c, e, "XR-005"), "title").(string); strings.Contains(title, "[приёмка:") {
		t.Errorf("агентское умолчание повесило суффикс: %v", title)
	}

	// Смешанный вид: суффикс на доске, барьер и причина в разделе приёмки.
	resp = doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/tasks",
		`{"title": "Шестая, смешанная", "r_parts": [25, 1, 1, 0, 0], "accept": "mixed", "barrier": "глаза", "reason": "вид экрана на телефоне"}`)
	newResp(t, resp, "заведение смешанной строки")
	if kind := taskRowField(t, getTask(t, c, e, "XR-006"), "accept"); kind != "mixed" {
		t.Errorf("вид смешанной строки не доехал: %v", kind)
	}
	if board := readFile(t, filepath.Join(e.proj, "docs", "TASKS.md")); !strings.Contains(board, "Шестая, смешанная [приёмка: mixed]") {
		t.Errorf("суффикс приёмки не встал в строку доски:\n%s", board)
	}
	doc := readFile(t, filepath.Join(e.proj, "docs", "tasks", "XR-006.md"))
	for _, want := range []string{"- вид: mixed", "- барьер «глаза»: вид экрана на телефоне"} {
		if !strings.Contains(doc, want) {
			t.Errorf("в разделе «Приёмка» нет %q:\n%s", want, doc)
		}
	}

	// Пользовательский вид с барьером «согласие» и пустой причиной: строка
	// заводится, скелет приёмки без причины.
	resp = doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/tasks",
		`{"title": "Седьмая, согласие", "r_parts": [25, 1, 1, 0, 0], "accept": "user", "barrier": "согласие"}`)
	newResp(t, resp, "заведение пользовательской строки")
	if kind := taskRowField(t, getTask(t, c, e, "XR-007"), "accept"); kind != "user" {
		t.Errorf("вид пользовательской строки не доехал: %v", kind)
	}
	doc = readFile(t, filepath.Join(e.proj, "docs", "tasks", "XR-007.md"))
	if !strings.Contains(doc, "- барьер «согласие»") {
		t.Errorf("в файле задачи нет барьера «согласие»:\n%s", doc)
	}

	// Коммит дашборда берёт и файл приёмки, а не только доску.
	if git := readFile(t, gitLog); !strings.Contains(git, "docs/TASKS.md docs/tasks/XR-006.md") {
		t.Errorf("коммит смешанной строки не взял файл приёмки: %s", git)
	}
}

// Отказы вида у ручки те же, что у воротов add: пустой барьер у не агентского
// вида и чужой ключ отбиваются словами утилиты, доска не трогается.
func TestTaskCreateAcceptRefusals(t *testing.T) {
	e, c, _ := tasksEnv(t)
	boardPath := filepath.Join(e.proj, "docs", "TASKS.md")
	before := readFile(t, boardPath)
	cases := []struct {
		name, body, want string
	}{
		{"без барьера", `{"title": "Пятая", "r_parts": [25, 1, 1, 0, 0], "accept": "user"}`, "нужен --barrier"},
		{"чужой барьер", `{"title": "Пятая", "r_parts": [25, 1, 1, 0, 0], "accept": "user", "barrier": "чего"}`, "закрытого списка"},
		{"чужой вид", `{"title": "Пятая", "r_parts": [25, 1, 1, 0, 0], "accept": "robot"}`, "не из {agent, mixed, user}"},
	}
	for _, tc := range cases {
		resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/tasks", tc.body)
		text := body(t, resp)
		if resp.StatusCode != http.StatusBadRequest || !strings.Contains(text, tc.want) {
			t.Errorf("%s: %d %s, ожидал 400 со словами %q", tc.name, resp.StatusCode, text, tc.want)
		}
	}
	if after := readFile(t, boardPath); after != before {
		t.Errorf("отбитое заведение тронуло доску:\n%s", after)
	}
}

// Без входа не заводится ничего, чужой Origin отбивается до всякой записи.
func TestNewTaskAuthAndOrigin(t *testing.T) {
	e, c, _ := tasksEnv(t)
	boardPath := filepath.Join(e.proj, "docs", "TASKS.md")
	before := readFile(t, boardPath)
	calls := []struct{ url, body string }{
		{e.srv.URL + "/api/projects/demo/tasks", `{"title": "Пятая", "r_parts": [25, 1, 1, 0, 0]}`},
		{e.srv.URL + "/api/projects/demo/drafts", `{"text": "мысль", "prio": "mid"}`},
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

// Экран заведения честен словами: форма одна на задачу и черновик, поля те
// же, что у правки задачи, и кнопка заведения есть и на доске, и на главной.
func TestStaticNewTaskForm(t *testing.T) {
	text := readFile(t, filepath.Join("static", "app.js"))
	for _, want := range []string{
		"Новая задача",
		// Пара кнопок записи черновика (DK-370, LLD DK-354 решение 5):
		// одиночной «Записать черновик» и промежуточного экрана между ними
		// нет, обе дороги закрыты возвратами самих кнопок.
		"Сохранить и грумить",
		"Что нужно сделать и зачем",
		"Завести задачу",
		"function renderNew(",
		// Вид приёмки на форме (DK-301): закрытые списки вида и барьера, поле
		// причины и рубеж у не агентского вида без барьера.
		"const ACCEPT_VALUES",
		"const BARRIER_VALUES",
		"Почему обход не годится",
		"приёмка повисает без причины",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("в static/app.js нет надписи %q", want)
		}
	}
	// Список барьера начинается пустой заглушкой: pickField выбирает первый
	// пункт сам, и без заглушки не переключенный список молча уезжал бы
	// барьером «глаза», которого человек не называл. Заглушка стоит в списке
	// значений, а не приписывается к первому option текстом, и отбивается
	// рубежом формы до отправки (замечание ревью DK-301).
	barriers := funcBody(t, text, "const BARRIER_VALUES")
	if !strings.Contains(barriers, `"", "глаза"`) {
		t.Error("список барьера не начинается пустой заглушкой: выбор по умолчанию уезжает первым настоящим ключом")
	}
	if strings.Contains(text, "ACCEPT_NAMES") || strings.Contains(text, "BARRIER_HINTS") {
		t.Error("в static/app.js есть неиспользуемые константы ACCEPT_NAMES или BARRIER_HINTS")
	}
	// Чекбокса файла на форме нет: строка без файла это дыра, и заведение не
	// предлагает её (DK-394).
	if strings.Contains(text, "nfcheck") || strings.Contains(text, "newForm.file") {
		t.Error("в static/app.js остался чекбокс заведения файла задачи")
	}
	// Кнопка стоит в накопителе, а на главной то же заведение живёт пунктом
	// меню у плюса карточки: иначе с телефона до заведения надо сначала дойти
	// до нужного проекта.
	// Заведение спрашивает вид выпадашкой у самой кнопки, и пункт ведёт сразу
	// в свою форму: отдельный экран выбора стоил лишнего перехода (решение
	// пользователя). Меню одно на все кнопки заведения.
	if menu := funcBody(t, text, "function makeMenuAt("); !strings.Contains(menu, `"/new/"`) {
		t.Error("меню заведения не ведёт в форму")
	}
	for _, fn := range []string{"function makePlus(", "function newTaskFab("} {
		if body := funcBody(t, text, fn); !strings.Contains(body, "makeMenuAt(btn") {
			t.Errorf("%s не открывает меню заведения", fn)
		}
	}
	// В накопителе своей кнопки заведения больше нет: тот же выбор «черновик
	// или задача» живёт меню плюса рядом с полем поиска, и вторая кнопка на том
	// же экране отвечала на уже заданный вопрос (решение пользователя).
	if body := funcBody(t, text, "function renderDrafts("); strings.Contains(body, "newTaskButton(") {
		t.Error("в таб черновиков вернулась своя кнопка заведения")
	}
	// Кнопки заведения, которую никуда не кладут, в статике нет: разбор входов
	// в заведение спотыкался о неё как о четвёртую дорогу, которой нет.
	if strings.Contains(text, "function newTaskButton(") {
		t.Error("в static/app.js вернулась кнопка заведения, которую никто не рисует")
	}
	// С доски заведение это кнопка в шапке рядом с поиском: полоса кнопок над
	// строками доски занимала место, ради которого экран и открыт.
	if !strings.Contains(readFile(t, filepath.Join("static", "index.html")), `id="make-btn"`) {
		t.Error("в шапке нет кнопки заведения задачи")
	}
	// Кнопка шапки открывает то же меню, что плюс карточки и плавающий плюс, а
	// не ведёт прямо на форму задачи: прежде черновик с доски было не завести
	// вовсе (замечание пользователя).
	if strings.Contains(text, `["make-btn", "/new"]`) {
		t.Error("кнопка шапки снова ведёт прямо на форму задачи, минуя выбор вида")
	}
	if !strings.Contains(text, `document.getElementById("make-btn").addEventListener(`) ||
		!strings.Contains(text, "if (project) makeMenuAt(btn, project, btn)") {
		t.Error("кнопка заведения в шапке не открывает меню выбора вида")
	}
	if strings.Contains(funcBody(t, text, "function renderBoard("), "newTaskButton(") {
		t.Error("полоса кнопок вернулась на доску")
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

// Заведение спрашивает, что заводят, и открывает свою форму: у черновика поля
// черновика, у задачи поля строки доски, и мешать их в одной форме больше
// нечему. Несохранённое в работу не берётся, и сказано это словами.
func TestStaticNewFormSwitch(t *testing.T) {
	text := readFile(t, filepath.Join("static", "app.js"))
	for _, want := range []string{
		"Черновику доступен только груминг",
		"в работу его не взять",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("в static/app.js нет надписи %q", want)
		}
	}
	// Подписей под формой не осталось ни у черновика, ни у задачи: они
	// пересказывали устройство (куда ляжет файл, кто выдаст ID, что откроется
	// после записи) и объясняли кнопки, чьи подписи их и называют (замечание
	// пользователя). Пометка про груминг выше это не подпись формы, а
	// единственная особенность черновика, которой не видно глазами.
	for _, gone := range []string{
		"Ляжет в docs/tasks/drafts/, ID выдаст taskctl",
		"Встанет в Backlog сразу, место выведется из ранга",
		"Взять в работу можно с карточки задачи",
		"«Сохранить» вернёт в накопитель",
		"Файл задачи docs/tasks/<ID>.md заведётся вместе со строкой",
	} {
		if strings.Contains(text, gone) {
			t.Errorf("на форме заведения снова стоит подпись %q", gone)
		}
	}
	cut := strings.Index(text, "function renderNew(")
	if cut < 0 {
		t.Fatal("в static/app.js нет renderNew")
	}
	form := text[cut:]
	if stop := strings.Index(form, "\n}\n"); stop > 0 {
		form = form[:stop]
	}
	// Форма одна: и черновик, и строка уезжают одной кнопкой с одного поля.
	if !strings.Contains(form, "makeDraft(project") || !strings.Contains(form, "makeTask(project") {
		t.Error("форма заведения не отправляет оба случая: переключатель некуда вести")
	}
	// Взять в работу с формы нечего: запуска на этом экране нет вовсе.
	if strings.Contains(form, "startRun(") {
		t.Error("на форме заведения есть запуск работы: у несохранённой задачи нет ни ID, ни статуса")
	}
	// Форма собрана под свой вид: полей чужого вида на ней нет вовсе, и гасить
	// нечего. Прежде она была одна на оба случая, поля черновика стояли
	// вперемешку с полями строки доски, а гашеные чипы объясняли, чего у
	// черновика нет («каша полей», замечание пользователя).
	if strings.Contains(form, `classList.toggle("off"`) {
		t.Error("форма снова гасит чужие поля вместо того, чтобы их не носить")
	}
	if !strings.Contains(form, "draft ? { title: true }") {
		t.Error("состав полей формы не зависит от вида заводимого")
	}
	// Отдельного экрана выбора нет вовсе: он стоил лишнего перехода там, где
	// хватает выпадашки у кнопки (решение пользователя).
	if strings.Contains(text, "renderMakePick") {
		t.Error("экран выбора вернулся вместо выпадашки у кнопки")
	}
	// Разделы SCQA спрашиваются порознь, своим полем каждый: одним полем
	// черновик писался простынёй (просьба пользователя).
	if !strings.Contains(text, "DRAFT_PLACEHOLDER") || !strings.Contains(text, "DRAFT_TEMPLATE") {
		t.Error("форма черновика молчит про разделы")
	}
	// Дороги на доску в теле формы нет: она вела туда же, куда ведёт шапка
	// страницы, и стояла на экране второй такой же (замечание пользователя).
	if strings.Contains(form, "crumb:") {
		t.Error("дублирующая крошка «Доска <проект>» вернулась на форму заведения")
	}
	css := readFile(t, filepath.Join("static", "style.css"))
	for _, want := range []string{".pmenu", ".pmrow", ".dnote"} {
		if !strings.Contains(css, want) {
			t.Errorf("в static/style.css нет правила %q", want)
		}
	}
}

// Форма черновика встречает человека шаблоном разделов в единственном поле:
// заголовок первой строкой, дальше «### Ситуация» и соседи с пустыми местами
// под текст. Полей на каждый раздел форма не заводит: «нужно было просто в
// поле редактирования вставить шаблон с разделами, а пользователь сам заполнит
// их, так гораздо гибче» (решение пользователя). Правит шаблон человек руками,
// и рубеж формы тот же, что у утилиты записи: непустая первая строка.
func TestStaticDraftTemplateForm(t *testing.T) {
	app := readFile(t, filepath.Join("static", "app.js"))
	for _, want := range []string{`const DRAFT_TEMPLATE = ["", "", "### Ситуация"`,
		`"### Осложнение"`, `"### Вопрос"`, `"### Гипотеза"`} {
		if !strings.Contains(app, want) {
			t.Errorf("в шаблоне черновика нет %q", want)
		}
	}
	// Полей на разделы нет вовсе, как и сборки текста из них.
	for _, gone := range []string{"DRAFT_SECTIONS", "function draftText(", `"nfsec"`,
		"черновика\")", "form.sit", "form.comp"} {
		if strings.Contains(app, gone) {
			t.Errorf("отдельные поля разделов остались в статике: нашлось %q", gone)
		}
	}
	made := funcBody(t, app, "function renderNew(")
	// Шаблон кладётся один раз за заход: перерисовка по фокусу окна не должна
	// возвращать его в очищенное рукой поле.
	for _, want := range []string{"const seeded = draft && !newForm.seeded && !newForm.title.trim();",
		"if (seeded) newForm.title = DRAFT_TEMPLATE;",
		"const text = newForm.title.trim();"} {
		if !strings.Contains(made, want) {
			t.Errorf("шаблон кладётся не тем блоком: нет %q", want)
		}
	}
	// Поле у обеих форм одно, и нетронутый шаблон на форму задачи не уезжает:
	// там он встал бы заголовком строки.
	if !strings.Contains(made, "if (!draft && newForm.seeded && newForm.title === DRAFT_TEMPLATE) {") {
		t.Error("шаблон черновика уезжает на форму задачи заголовком строки")
	}
	// Курсор встаёт на первую строку, где человек и начинает.
	if !strings.Contains(made, "view.title.setSelectionRange(0, 0)") {
		t.Error("курсор не ставится в поле шаблона: человек ищет начало сам")
	}
	// Рубеж ровно утилитин: непустая первая строка и её длина, разделов он не
	// спрашивает.
	stop := funcBody(t, app, "function draftFormRefusal(")
	if !strings.Contains(stop, `form.title || "").split("\n", 1)[0]`) {
		t.Error("рубеж формы черновика меряет не первую строку")
	}
	for _, gone := range []string{"ситуация не написана", "осложнение не написано"} {
		if strings.Contains(app, gone) {
			t.Errorf("форма всё ещё отбивает запись за раздел: %q", gone)
		}
	}
	if !strings.Contains(app, "DRAFT_TITLE_LIMIT = 72") {
		t.Error("порога заголовка черновика на форме нет: о нём узнают только с ответа сервера")
	}
}

// Выход с формы заведения: кнопка рядом с записью и Escape. Пустая форма
// закрывается сразу, набранная спрашивает и уходит по второму нажатию, туда
// же, откуда пришли (замечание пользователя: «передумал, а выйти нечем»).
func TestStaticNewFormExit(t *testing.T) {
	text := readFile(t, filepath.Join("static", "app.js"))
	form := funcBody(t, text, "function formPage(")
	if !strings.Contains(form, `barBtn("btn bquit"`) || !strings.Contains(form, "cfg.onQuit(quit)") {
		t.Error("у формы нет выхода рядом с кнопками записи")
	}
	made := funcBody(t, text, "function renderNew(")
	for _, want := range []string{"onQuit: (btn) => { leaveNew(btn); }",
		`quitLabel: draft ? "Не записывать" : "Не заводить"`,
		"if (newFormFilled() && !quitArmed) {",
		`btn.rename(draft ? "Черновик не записан, выйти?" : "Задача не заведена, выйти?")`,
		`goKeepingChat(draft ? project + "/drafts" : project)`,
		"newEscape = () => { leaveNew(view.quit); };"} {
		if !strings.Contains(made, want) {
			t.Errorf("выход с формы собран не тем блоком: нет %q", want)
		}
	}
	// Клавиша живёт ровно на своём экране и не отбирает Escape у всплывашек.
	key := funcBody(t, text, "function wireNewKey(")
	for _, want := range []string{`ev.key !== "Escape" || popupsOpen.size`, "!route().make || !newEscape"} {
		if !strings.Contains(key, want) {
			t.Errorf("Escape формы заведения ловится не по месту: нет %q", want)
		}
	}
	if !strings.Contains(text, "\nwireNewKey();") {
		t.Error("Escape формы заведения никто не подключает")
	}
	// Взведённый выход виден собой, а не одним лишь текстом кнопки.
	if !strings.Contains(readFile(t, filepath.Join("static", "style.css")), ".bquit.armed{") {
		t.Error("взведённый выход с формы ничем не отличается от обычного")
	}
}

// Пара кнопок на форме записи (DK-370, LLD DK-354 решение 5): «Сохранить»
// возвращает в накопитель, «Сохранить и грумить» той же ручкой пишет запись и
// поднимает разбор. Промежуточного экрана «Черновик записан» между формой и
// списком нет вовсе.
func TestStaticDraftSaveButtons(t *testing.T) {
	text := readFile(t, filepath.Join("static", "app.js"))
	for _, want := range []string{
		`saveLabel: draft ? "Сохранить" : "Завести задачу"`,
		`label: "Сохранить и грумить"`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("пара кнопок записи собрана не тем блоком: нет %q", want)
		}
	}
	for _, gone := range []string{
		"function draftDone(",
		`"Записать черновик"`,
		`"Записать ещё"`,
	} {
		if strings.Contains(text, gone) {
			t.Errorf("промежуточный экран записи остался в коде: нашлось %q", gone)
		}
	}
	// Обе кнопки живут и гаснут вместе: рубеж у них один, ручка одна, и
	// разошедшийся вид сказал бы неправду о том, что можно нажать.
	form := funcBody(t, text, "function formPage(")
	for _, want := range []string{"more.disabled = save.disabled", "more.hidden = save.hidden"} {
		if !strings.Contains(form, want) {
			t.Errorf("вторая кнопка сохранения живёт своим рубежом: нет %q", want)
		}
	}
	// Разбор поднимается той же ручкой, что и с накопителя, и уводит на экран
	// записи с ходом разбора.
	made := funcBody(t, text, "function renderNew(")
	if !strings.Contains(made, `groomDraft(project, done.id, project + "/draft/" + done.id)`) {
		t.Error("«Сохранить и грумить» не поднимает разбор с переходом на экран записи")
	}
	if !strings.Contains(made, `goKeepingChat(project + "/drafts")`) {
		t.Error("«Сохранить» не возвращает в накопитель")
	}
}
