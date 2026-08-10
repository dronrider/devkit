package main

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Переписка через состояние цели: сообщение уходит в раздел «Входящие» файла
// цели, а не в идущий процесс. Стенды на временном HOME с фикстурными доской
// и файлом цели; git играет фикстура в PATH, настоящих коммитов тесты не
// делают, как и настоящих сессий.

const goalFileFixture = `# XR-100: Цель: пробный цикл

## Цель

DoD: стенд отработал.

## Бюджет

бюджет: week_all <= 10

## Задачи цели

Заводит нарезка первым витком.

## Журнал

- снимок 2026-08-09: week_all 10%

## Итог

Пишет последний виток.
`

// gitFakeOK пишет каждый вызов в журнал и молчит: с точки зрения сервера
// add, commit и push прошли.
func gitFakeOK(logPath string) string {
	return fmt.Sprintf("echo \"$@\" >> %q\nexit 0", logPath)
}

// messagesEnv поднимает окружение с целью XR-100 на доске (runsBoardJSON) и
// её файлом docs/tasks/XR-100.md в синтетическом проекте.
func messagesEnv(t *testing.T, gitBody string) (*testEnv, *http.Client, string) {
	t.Helper()
	e := newTestEnv(t)
	writeScript(t, e.bin, "taskctl", fmt.Sprintf("echo '%s'", runsBoardJSON))
	gitLog := filepath.Join(e.home, "git.log")
	if gitBody == "" {
		gitBody = gitFakeOK(gitLog)
	}
	writeScript(t, e.bin, "git", gitBody)
	dir := filepath.Join(e.proj, "docs", "tasks")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "XR-100.md"), []byte(goalFileFixture), 0o644); err != nil {
		t.Fatal(err)
	}
	return e, e.loggedClient(t), gitLog
}

func postMessage(t *testing.T, c *http.Client, e *testEnv, id, text string) *http.Response {
	t.Helper()
	return doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/goals/"+id+"/message",
		fmt.Sprintf(`{"text": %q}`, text))
}

var inboxLineRe = regexp.MustCompile(`- \d{4}-\d{2}-\d{2} \d{2}:\d{2}, из дашборда: привет виток`)

// Сообщение ложится строкой во «Входящие» файла цели, раздел встаёт перед
// «Журналом», а правка уезжает git-ом: add, commit с ID в subject, push.
func TestMessageLandsInInbox(t *testing.T) {
	e, c, gitLog := messagesEnv(t, "")

	resp := postMessage(t, c, e, "XR-100", "привет виток")
	text := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("отправка сообщения: %d %s", resp.StatusCode, text)
	}
	if !strings.Contains(text, "следующий виток") || !strings.Contains(text, "идущий не увидит") {
		t.Errorf("ответ не называет судьбу сообщения: %s", text)
	}
	doc := readFile(t, filepath.Join(e.proj, "docs", "tasks", "XR-100.md"))
	if !inboxLineRe.MatchString(doc) {
		t.Fatalf("во «Входящих» нет строки с временем и источником:\n%s", doc)
	}
	inbox := strings.Index(doc, "## Входящие")
	journal := strings.Index(doc, "## Журнал")
	if inbox < 0 || journal < 0 || inbox > journal {
		t.Errorf("раздел «Входящие» не встал перед «Журналом»:\n%s", doc)
	}
	git := readFile(t, gitLog)
	for _, want := range []string{
		"add -- docs/tasks/XR-100.md",
		"docs(tasks): XR-100 сообщение с дашборда",
		"push",
	} {
		if !strings.Contains(git, want) {
			t.Errorf("в вызовах git нет %q: %s", want, git)
		}
	}
}

// Второе сообщение дописывается в тот же раздел, порядок отправки сохранён.
func TestMessageAppendsToExistingInbox(t *testing.T) {
	e, c, _ := messagesEnv(t, "")
	for _, text := range []string{"первое", "второе"} {
		resp := postMessage(t, c, e, "XR-100", text)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("отправка %q: %d", text, resp.StatusCode)
		}
	}
	doc := readFile(t, filepath.Join(e.proj, "docs", "tasks", "XR-100.md"))
	if n := strings.Count(doc, "## Входящие"); n != 1 {
		t.Fatalf("разделов «Входящие» %d, ожидал один:\n%s", n, doc)
	}
	first := strings.Index(doc, "из дашборда: первое")
	second := strings.Index(doc, "из дашборда: второе")
	if first < 0 || second < 0 || first > second {
		t.Errorf("сообщения легли не по порядку отправки:\n%s", doc)
	}
}

// Сообщение это одна строка списка: переводы строк схлопываются в пробелы,
// иначе хвост многострочного текста выпал бы из разбора «Входящих».
func TestMessageMultilineCollapsed(t *testing.T) {
	e, c, _ := messagesEnv(t, "")
	resp := postMessage(t, c, e, "XR-100", "первая строка\nвторая\n\nтретья")
	resp.Body.Close()
	doc := readFile(t, filepath.Join(e.proj, "docs", "tasks", "XR-100.md"))
	if !strings.Contains(doc, "из дашборда: первая строка вторая третья") {
		t.Errorf("многострочный текст не схлопнулся в одну строку:\n%s", doc)
	}
}

// Отказы называются словами: строки нет на доске, строка не цель, файла цели
// нет, пустое сообщение.
func TestMessageRefusals(t *testing.T) {
	e, c, gitLog := messagesEnv(t, "")
	cases := []struct {
		name, id, text string
		code           int
		want           string
	}{
		{"нет строки", "XR-999", "привет", http.StatusNotFound, "нет строки XR-999"},
		{"не цель", "XR-002", "привет", http.StatusBadRequest, "не цель"},
		{"пустой текст", "XR-100", "  \n ", http.StatusBadRequest, "пустое сообщение"},
	}
	for _, tc := range cases {
		resp := postMessage(t, c, e, tc.id, tc.text)
		text := body(t, resp)
		if resp.StatusCode != tc.code || !strings.Contains(text, tc.want) {
			t.Errorf("%s: %d %s, ожидал %d с %q", tc.name, resp.StatusCode, text, tc.code, tc.want)
		}
	}
	if err := os.Remove(filepath.Join(e.proj, "docs", "tasks", "XR-100.md")); err != nil {
		t.Fatal(err)
	}
	resp := postMessage(t, c, e, "XR-100", "привет")
	text := body(t, resp)
	if resp.StatusCode != http.StatusNotFound || !strings.Contains(text, "файла цели docs/tasks/XR-100.md") {
		t.Errorf("без файла цели: %d %s, ожидал 404 со словами про файл", resp.StatusCode, text)
	}
	if git := readFile(t, gitLog); strings.Contains(git, "commit") {
		t.Errorf("отказ дошёл до git commit: %s", git)
	}
}

// Провал пуша не съедает сообщение молча: запись на месте, а причина
// называется полем note.
func TestMessagePushFailureNamed(t *testing.T) {
	e, c, gitLog := messagesEnv(t, "")
	writeScript(t, e.bin, "git", fmt.Sprintf(
		"echo \"$@\" >> %q\ncase \"$3\" in\npush)\n  echo 'нет доступа к origin' >&2; exit 1;;\nesac\nexit 0", gitLog))

	resp := postMessage(t, c, e, "XR-100", "привет виток")
	text := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("отправка при сломанном push: %d %s", resp.StatusCode, text)
	}
	if !strings.Contains(text, "git push не прошёл") || !strings.Contains(text, "нет доступа к origin") {
		t.Errorf("провал пуша не назван: %s", text)
	}
	doc := readFile(t, filepath.Join(e.proj, "docs", "tasks", "XR-100.md"))
	if !strings.Contains(doc, "из дашборда: привет виток") {
		t.Errorf("сообщение пропало вместе с провалом пуша:\n%s", doc)
	}
}

// GET отдаёт лежащие строки «Входящих»: по ним клиент показывает «ждёт
// витка»; пустой раздел называется словами, а не пустым списком без причины.
func TestMessagePendingList(t *testing.T) {
	e, c, _ := messagesEnv(t, "")

	resp := doReq(t, c, "GET", e.srv.URL+"/api/projects/demo/goals/XR-100/message", "")
	text := body(t, resp)
	if resp.StatusCode != http.StatusOK || !strings.Contains(text, "пусто") {
		t.Errorf("пустые «Входящие» без слов: %d %s", resp.StatusCode, text)
	}

	for _, msg := range []string{"первое", "второе"} {
		r := postMessage(t, c, e, "XR-100", msg)
		r.Body.Close()
	}
	resp = doReq(t, c, "GET", e.srv.URL+"/api/projects/demo/goals/XR-100/message", "")
	text = body(t, resp)
	for _, want := range []string{"из дашборда: первое", "из дашборда: второе"} {
		if !strings.Contains(text, want) {
			t.Errorf("в pending нет %q: %s", want, text)
		}
	}
	if strings.Contains(text, "note") {
		t.Errorf("note при непустых «Входящих»: %s", text)
	}
}

// Без входа 401 и ни одной строки данных, чужой Origin на изменяющем методе
// отбивается до всякой записи.
func TestMessageLoginAndOrigin(t *testing.T) {
	e, c, _ := messagesEnv(t, "")
	plain := plainClient()
	for _, call := range []struct{ method, body string }{
		{"GET", ""},
		{"POST", `{"text": "привет"}`},
	} {
		resp := doReq(t, plain, call.method, e.srv.URL+"/api/projects/demo/goals/XR-100/message", call.body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s без входа: %d, ожидал 401", call.method, resp.StatusCode)
		}
	}
	req, err := http.NewRequest("POST", e.srv.URL+"/api/projects/demo/goals/XR-100/message",
		strings.NewReader(`{"text": "привет"}`))
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
		t.Errorf("чужой Origin: %d, ожидал 403", resp.StatusCode)
	}
	doc := readFile(t, filepath.Join(e.proj, "docs", "tasks", "XR-100.md"))
	if strings.Contains(doc, "Входящие") {
		t.Errorf("отбитый запрос дописал файл цели:\n%s", doc)
	}
}

// Файл цели без «Журнала» тоже принимает сообщение: раздел встаёт в конец
// файла, а не теряется.
func TestAddInboxLineWithoutJournal(t *testing.T) {
	doc := "# XR-7: Цель: без журнала\n\n## Цель\n\nТекст.\n"
	got := addInboxLine(doc, "- 2026-08-10 12:00, из дашборда: привет")
	if !strings.HasSuffix(got, "## Входящие\n\n- 2026-08-10 12:00, из дашборда: привет\n") {
		t.Errorf("раздел не встал в конец файла:\n%q", got)
	}
	if lines := inboxLines(got); len(lines) != 1 || !strings.Contains(lines[0], "привет") {
		t.Errorf("вставленная строка не читается назад: %v", lines)
	}
}

// «Входящие» в середине файла: строки раздела читаются до следующего
// заголовка, чужие списки («Журнал») в pending не попадают.
func TestInboxLinesStopAtNextSection(t *testing.T) {
	doc := "## Входящие\n\n- 2026-08-10 12:00, из дашборда: первое\n\n## Журнал\n\n- снимок 2026-08-09: week_all 10%\n"
	lines := inboxLines(doc)
	if len(lines) != 1 || !strings.Contains(lines[0], "первое") {
		t.Errorf("разбор «Входящих» зацепил соседний раздел: %v", lines)
	}
}

// Экран переписки честен словами: сообщение ляжет в файл цели и уйдёт
// следующему витку, идущий его не увидит; лежащее показывается как «ждёт
// витка», а стоп остаётся стопом цикла.
func TestStaticChatHonesty(t *testing.T) {
	text := readFile(t, filepath.Join("static", "app.js"))
	for _, want := range []string{
		"ляжет в файл цели",
		"уйдёт следующему витку",
		"идущий виток его не увидит",
		"ждёт витка",
		"Стоп цикла",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("в static/app.js нет честной надписи %q", want)
		}
	}
}
