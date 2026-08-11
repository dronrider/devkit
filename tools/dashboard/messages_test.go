package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
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

const taskFileFixture = `# XR-002: Обычная задача

## Чего хотим

Задача не цель, сообщений ей не кладут.
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
	// У обычной задачи XR-002 файл есть: без него отказ «не цель» был бы
	// неотличим от отказа «файла нет», и стенд не доказывал бы защиту чужого
	// файла.
	if err := os.WriteFile(filepath.Join(dir, "XR-002.md"), []byte(taskFileFixture), 0o644); err != nil {
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
	// Отказ «не цель» бережёт чужой файл: сообщение в файл обычной задачи не
	// легло, файл остался фикстурой байт в байт.
	if doc := readFile(t, filepath.Join(e.proj, "docs", "tasks", "XR-002.md")); doc != taskFileFixture {
		t.Errorf("отказ «не цель» тронул файл обычной задачи:\n%s", doc)
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

// Предел тела называется своими словами, а не «жду JSON»: JSON сверх предела
// был нормальный. Под пределом сообщение проходит как обычно.
func TestMessageBodyLimit(t *testing.T) {
	e, c, _ := messagesEnv(t, "")

	under := strings.Repeat("а", 4000)
	resp := postMessage(t, c, e, "XR-100", under)
	text := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("сообщение под пределом: %d %s", resp.StatusCode, text)
	}
	doc := readFile(t, filepath.Join(e.proj, "docs", "tasks", "XR-100.md"))
	if !strings.Contains(doc, "из дашборда: "+under) {
		t.Errorf("сообщение под пределом не легло во «Входящие»")
	}

	resp = postMessage(t, c, e, "XR-100", strings.Repeat("б", 20000))
	text = body(t, resp)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("тело сверх предела: %d %s, ожидал 400", resp.StatusCode, text)
	}
	if !strings.Contains(text, "предела 16 КБ") {
		t.Errorf("отказ не называет предел: %s", text)
	}
	if doc := readFile(t, filepath.Join(e.proj, "docs", "tasks", "XR-100.md")); strings.Contains(doc, "ббб") {
		t.Errorf("тело сверх предела дописало файл цели")
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

// Экран чата честен словами макета: сообщение уйдёт агенту, он прочитает его
// при следующем запуске, идущая сессия его не увидит; лежащее во «Входящих»
// подписано тем же обещанием, а стоп остаётся стопом цикла.
func TestStaticChatHonesty(t *testing.T) {
	text := readFile(t, filepath.Join("static", "app.js"))
	for _, want := range []string{
		"Сообщение уйдёт агенту.",
		"Он отреагирует на него на следующей рабочей итерации.",
		"подхвачено следующим витком",
		"Написать агенту...",
		"Остановить агента",
		"во «Входящих» пусто",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("в static/app.js нет честной надписи %q", want)
		}
	}
}

// Вход в чат на всех экранах называется чатом с агентом: «Переписка» ушла из
// кнопок и подписи шапки вместе с жаргоном витков.
func TestStaticChatNamedAgentChat(t *testing.T) {
	text := readFile(t, filepath.Join("static", "app.js"))
	if strings.Count(text, `"Чат с агентом"`) < 2 {
		t.Error("в static/app.js меньше двух кнопок «Чат с агентом»: вход остался «Перепиской»")
	}
	if !strings.Contains(text, `"чат с агентом " + rt.id`) {
		t.Error("шапка экрана чата подписана не чатом с агентом")
	}
	if strings.Contains(text, `"Переписка"`) || strings.Contains(text, `"переписка "`) {
		t.Error("в static/app.js осталась надпись «Переписка»")
	}
}

// Чат открывается хвостом: читается последняя порция реплик, лента сразу
// стоит внизу, а история подаётся кнопкой «раньше» через ?before=.
func TestStaticChatOpensAtTail(t *testing.T) {
	text := readFile(t, filepath.Join("static", "app.js"))
	body := funcBody(t, text, "async function wireChatFeed(")
	for _, want := range []string{`"?n=" + CHAT_TAIL`, `"?before=" + firstSeq`, "more.hidden"} {
		if !strings.Contains(body, want) {
			t.Errorf("в ленте чата нет %q: хвост и история подаются не так", want)
		}
	}
	draw := strings.Index(body, "\n  draw();")
	bottom := strings.Index(body, "feed.scrollTop = feed.scrollHeight;")
	if draw < 0 || bottom < 0 || draw > bottom {
		t.Error("чат прокручивается вниз не после первой отрисовки: открытие хвостом не сработает")
	}
}

// Якорь ленты: перед перерисовкой меряется, стоит ли лента внизу, и низ
// держится только тогда; из истории прокрутку вниз не бросает.
func TestStaticChatKeepsPlace(t *testing.T) {
	text := readFile(t, filepath.Join("static", "app.js"))
	body := funcBody(t, text, "async function wireChatFeed(")
	measure := strings.Index(body, "const bottom = atBottom(feed);")
	rebuild := strings.Index(body, "box.replaceChildren();")
	if measure < 0 || rebuild < 0 || measure > rebuild {
		t.Error("положение ленты мерится после перерисовки: якорь взят с уже сброшенной прокрутки")
	}
	if !strings.Contains(body, "keepPlace(feed, tail)") {
		t.Error("лента чата не возвращается на прежнее место: взгляд в историю сорвёт дострение")
	}
	if strings.Contains(body, "es.onmessage") && strings.Contains(body, "feed.scrollTop = feed.scrollHeight;\n    ") {
		t.Error("дострение чата прокручивает вниз мимо якоря")
	}
}

// Время и день реплики берутся в поясе клиента: вырезка символов из метки
// показывала бы UTC, и на телефоне это враньё.
func TestStaticChatTimeIsLocal(t *testing.T) {
	text := readFile(t, filepath.Join("static", "app.js"))
	for _, name := range []string{"function localTime(", "function localDay(", "function localDayKey("} {
		if !strings.Contains(text, name) {
			t.Errorf("в static/app.js нет %s: время реплик осталось строкой транскрипта", name)
		}
	}
	if !strings.Contains(funcBody(t, text, "function localTime("), "toLocaleTimeString") {
		t.Error("localTime не спрашивает пояс клиента")
	}
	for _, head := range []string{"async function wireChatFeed(", "function replyEl("} {
		if strings.Contains(funcBody(t, text, head), ".slice(11, 16)") {
			t.Errorf("%s режет время строкой вместо пояса клиента", head)
		}
	}
}

// mdSource собирает рендер markdown из статики: тесты гоняют его как есть,
// поэтому вырезаются те же строки, что уедут в браузер, а не их пересказ.
func mdSource(t *testing.T, text string) string {
	t.Helper()
	line := ""
	for _, l := range strings.Split(text, "\n") {
		if strings.HasPrefix(l, "const MD_INLINE") {
			line = l
			break
		}
	}
	if line == "" {
		t.Fatal("в static/app.js нет MD_INLINE: рендер markdown не собрать")
	}
	parts := []string{line}
	for _, head := range []string{
		"function el(", "function mdLink(", "function mdInline(", "function mdRender(",
	} {
		parts = append(parts, funcBody(t, text, head)+"\n}")
	}
	return strings.Join(parts, "\n\n")
}

// mdHTML прогоняет реплики через рендер под node с игрушечным DOM: узлы
// сериализуются с экранированием, поэтому в ответе видно ровно то, что
// увидел бы браузер.
func mdHTML(t *testing.T, inputs []string) []string {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node не найден, юниты рендера markdown не гоняются")
	}
	cases, err := json.Marshal(inputs)
	if err != nil {
		t.Fatal(err)
	}
	dom := `
function esc(s) {
  return String(s).replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/"/g, "&quot;");
}
class N {
  constructor(tag) { this.tag = tag; this.kids = []; this.attrs = {}; }
  get tagName() { return this.tag.toUpperCase(); }
  set className(v) { if (v) this.attrs.class = v; }
  set textContent(v) { this.kids = [{ text: String(v) }]; }
  set href(v) { this.attrs.href = v; }
  set target(v) { this.attrs.target = v; }
  set rel(v) { this.attrs.rel = v; }
  append(...nodes) { for (const n of nodes) this.kids.push(n); }
}
const document = {
  createElement: (tag) => new N(tag),
  createTextNode: (text) => ({ text: String(text) }),
};
function html(n) {
  if (n.text !== undefined) return esc(n.text);
  const at = Object.entries(n.attrs).map(([k, v]) => " " + k + '="' + esc(v) + '"').join("");
  return "<" + n.tag + at + ">" + n.kids.map(html).join("") + "</" + n.tag + ">";
}
`
	src := dom + "\n" + mdSource(t, readFile(t, filepath.Join("static", "app.js"))) + "\n" +
		"console.log(JSON.stringify(" + string(cases) + ".map((s) => html(mdRender(s)))));\n"
	path := filepath.Join(t.TempDir(), "md.mjs")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(node, path).CombinedOutput()
	if err != nil {
		t.Fatalf("рендер не отработал под node: %v\n%s", err, out)
	}
	var got []string
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(out))), &got); err != nil {
		t.Fatalf("разбор ответа рендера: %v\n%s", err, out)
	}
	return got
}

// Разметка из реплики остаётся буквами: тег script, картинка с onerror и
// закрывающий тег внутри кода уезжают в текст, а не в DOM. Проверка идёт
// рендером, а не чтением исходника: экранирование обещано пользователю.
func TestMarkdownEscapesInjection(t *testing.T) {
	got := mdHTML(t, []string{
		"<script>alert(1)</script> и **жирный**",
		"<img src=x onerror=alert(1)>",
		"`</code><script>alert(2)</script>`",
	})
	for i, out := range got {
		if strings.Contains(out, "<script") || strings.Contains(out, "<img") {
			t.Errorf("случай %d: разметка реплики попала в DOM: %s", i, out)
		}
	}
	if !strings.Contains(got[0], "&lt;script&gt;alert(1)&lt;/script&gt;") {
		t.Errorf("тег не показан словами: %s", got[0])
	}
	if !strings.Contains(got[0], "<b>жирный</b>") {
		t.Errorf("жирный не отрендерился: %s", got[0])
	}
	if !strings.Contains(got[1], "&lt;img src=x onerror=alert(1)&gt;") {
		t.Errorf("картинка с onerror не показана словами: %s", got[1])
	}
	if !strings.Contains(got[2], "<code>&lt;/code&gt;&lt;script&gt;alert(2)&lt;/script&gt;</code>") {
		t.Errorf("строчный код не закрылся экранированием: %s", got[2])
	}
}

// Ссылки кликабельны, но только http и https: javascript: в реплике остаётся
// текстом, а чужая вкладка открывается без доступа к нашей.
func TestMarkdownLinksSafe(t *testing.T) {
	got := mdHTML(t, []string{
		"[док](https://example.com/a?b=1)",
		"смотри https://example.com/b тут",
		"[клик](javascript:alert(1))",
	})
	want := `<a href="https://example.com/a?b=1" target="_blank" rel="noopener noreferrer">док</a>`
	if !strings.Contains(got[0], want) {
		t.Errorf("ссылка не кликабельна: %s", got[0])
	}
	if !strings.Contains(got[1], `<a href="https://example.com/b"`) {
		t.Errorf("голый адрес не стал ссылкой: %s", got[1])
	}
	if strings.Contains(got[2], "<a ") || strings.Contains(got[2], "javascript:") {
		t.Errorf("javascript: в реплике стал ссылкой: %s", got[2])
	}
	if !strings.Contains(got[2], "клик") {
		t.Errorf("текст отвергнутой ссылки пропал: %s", got[2])
	}
}

// Заголовки, списки, код-блок и строчный код рендерятся своим разбором:
// внешней библиотеки в статике нет, а разметка реплик всё же читается.
func TestMarkdownBlocks(t *testing.T) {
	got := mdHTML(t, []string{
		"# Виток 12\n\nтекст с `кодом`\n\n- раз\n- два\n\n```\nls -la <тут>\n```\n\n1. первый\n2. второй",
	})
	for _, want := range []string{
		`<div class="mdh mdh1">Виток 12</div>`,
		"<code>кодом</code>",
		"<ul><li>раз</li><li>два</li></ul>",
		"<pre>ls -la &lt;тут&gt;</pre>",
		"<ol><li>первый</li><li>второй</li></ol>",
	} {
		if !strings.Contains(got[0], want) {
			t.Errorf("в рендере нет %q: %s", want, got[0])
		}
	}
}

// Плашка про судьбу сообщения закрывается крестиком, а лента чата прижата к
// полю ввода: свежие реплики стоят внизу, у самого поля, а пустота короткой
// переписки остаётся сверху (макет «04 Переписка»).
func TestStaticChatNoteAndAnchor(t *testing.T) {
	app := readFile(t, filepath.Join("static", "app.js"))
	body := funcBody(t, app, "function renderChat(")
	for _, want := range []string{`el("button", "nx")`, `icon("close")`, "note.remove()", "Закрыть"} {
		if !strings.Contains(body, want) {
			t.Errorf("в плашке чата нет %q: закрыть её нечем", want)
		}
	}
	if !strings.Contains(body, "STOP_TIP") {
		t.Error("у кнопки остановки в чате нет подсказки о последствиях")
	}
	css := readFile(t, filepath.Join("static", "style.css"))
	for _, want := range []string{".chatfeed .mlist{margin-top:auto}", ".cnote .nx{"} {
		if !strings.Contains(css, want) {
			t.Errorf("в static/style.css нет %q: лента чата не прижата к полю ввода", want)
		}
	}
}
