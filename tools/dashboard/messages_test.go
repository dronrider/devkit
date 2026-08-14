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
	"sync"
	"testing"
	"time"
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

// liveCycle поднимает цели tmux-сессию оболочки: с ней работа видна живой, и
// ответ ручки говорит про следующий виток, а не про стоящий цикл.
func liveCycle(t *testing.T, e *testEnv) {
	t.Helper()
	writeTmuxFake(t, e.bin, filepath.Join(e.home, "tmux.log"), "goal-XR-100\\n")
}

// Сообщение ложится строкой во «Входящие» файла цели, раздел встаёт перед
// «Журналом», а правка уезжает git-ом: add, commit с ID в subject, push.
func TestMessageLandsInInbox(t *testing.T) {
	e, c, gitLog := messagesEnv(t, "")
	liveCycle(t, e)

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

// Регресс DK-270: goalFile собирал путь жёсткой склейкой docs/tasks/<ID>.md
// мимо ссылки строки доски, а goalDocPath журнала шёл по ссылке. У цели с
// нестандартной ссылкой сообщение легло бы не туда, откуда его читают журнал
// и экран. Сообщение обязано лечь в файл по ссылке, обычное место остаётся
// нетронутым, и журнал обязан читать тот же файл.
func TestMessageLandsAtLinkedGoalPath(t *testing.T) {
	e := newTestEnv(t)
	writeScript(t, e.bin, "taskctl", fmt.Sprintf("echo '%s'", journalBoardJSON("goals/XR-100.md")))
	gitLog := filepath.Join(e.home, "git.log")
	writeScript(t, e.bin, "git", gitFakeOK(gitLog))
	linked := filepath.Join(e.proj, "docs", "goals", "XR-100.md")
	writeDocAt(t, linked, goalDocFixture)
	c := e.loggedClient(t)

	resp := postMessage(t, c, e, "XR-100", "привет по ссылке")
	text := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("отправка сообщения по ссылке: %d %s", resp.StatusCode, text)
	}
	doc := readFile(t, linked)
	if !strings.Contains(doc, "из дашборда: привет по ссылке") {
		t.Fatalf("сообщение не легло в файл по ссылке:\n%s", doc)
	}
	if _, err := os.Stat(filepath.Join(e.proj, "docs", "tasks", "XR-100.md")); err == nil {
		t.Errorf("сообщение попало на обычное место docs/tasks/XR-100.md мимо ссылки")
	}
	if git := readFile(t, gitLog); !strings.Contains(git, "add -- docs/goals/XR-100.md") {
		t.Errorf("коммит не назвал файл по ссылке: %s", git)
	}

	// Тот же файл читает журнал: ручки сходятся на одном пути по ссылке.
	logText := body(t, doReq(t, c, "GET", e.srv.URL+"/api/projects/demo/goals/XR-100/log", ""))
	if !strings.Contains(logText, "docs/goals/XR-100.md") || !strings.Contains(logText, goalDocLines[0]) {
		t.Errorf("журнал не читает файл по той же ссылке: %s", logText)
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

// Повтор того же текста второй строки во «Входящих» не заводит: на слабой
// связи ответ теряется, человек жмёт «Отправить» ещё раз, и виток получал бы
// одно сообщение дважды (DK-281). Ответ при этом остаётся успешным и называет
// лежащую строку. Подхваченное витком из раздела уходит, и тот же текст после
// подхвата кладётся заново: ключ повтора это неподхваченные строки.
func TestMessageRepeatKeepsOneLine(t *testing.T) {
	e, c, _ := messagesEnv(t, "")
	path := filepath.Join(e.proj, "docs", "tasks", "XR-100.md")

	first := body(t, postMessage(t, c, e, "XR-100", "проверь ленту"))
	resp := postMessage(t, c, e, "XR-100", "проверь ленту")
	second := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("повтор отбит: %d %s", resp.StatusCode, second)
	}
	if !strings.Contains(second, "уже лежит во «Входящих»") {
		t.Errorf("повтор не назван словами: %s", second)
	}
	if line(t, first) != line(t, second) {
		t.Errorf("повтор назвал другую строку: %q и %q", line(t, first), line(t, second))
	}
	doc := readFile(t, path)
	if got := strings.Count(doc, "из дашборда: проверь ленту"); got != 1 {
		t.Fatalf("во «Входящих» %d строк одного сообщения, ждал одну:\n%s", got, doc)
	}

	// Другой текст ложится своей строкой: дедупликация не глотает переписку.
	postMessage(t, c, e, "XR-100", "и журнал тоже").Body.Close()
	if got := len(inboxLines(readFile(t, path))); got != 2 {
		t.Errorf("во «Входящих» %d строк, ждал две:\n%s", got, readFile(t, path))
	}

	// Виток подхватил строки и убрал их: тот же текст после этого снова
	// ложится во «Входящие».
	cleared := strings.ReplaceAll(readFile(t, path), "## Входящие", "## Пусто")
	if err := os.WriteFile(path, []byte(cleared), 0o644); err != nil {
		t.Fatal(err)
	}
	postMessage(t, c, e, "XR-100", "проверь ленту").Body.Close()
	if got := len(inboxLines(readFile(t, path))); got != 1 {
		t.Errorf("после подхвата сообщение не легло заново:\n%s", readFile(t, path))
	}
}

// idleFlag достаёт из ответа признак стоящего цикла: клиент разводит плашку по
// нему, а не по разбору русской фразы.
func idleFlag(t *testing.T, resp string) bool {
	t.Helper()
	var v struct {
		Idle bool `json:"idle"`
	}
	if err := json.Unmarshal([]byte(resp), &v); err != nil {
		t.Fatalf("ответ не разобрался: %v (%s)", err, resp)
	}
	return v.Idle
}

// Стоящий цикл назван словами в ответе ручки, и молчаливого «ждёт витка» при
// нём больше нет: до DK-319 ответ обещал следующий виток и законченной работе,
// а человек писал завершившемуся агенту и ждал ответа, которого не будет.
// Строка при этом ложится во «Входящие» как прежде: поднятый виток её
// прочитает.
func TestMessageAtIdleCycleSaysSo(t *testing.T) {
	e, c, _ := messagesEnv(t, "")
	path := filepath.Join(e.proj, "docs", "tasks", "XR-100.md")

	text := body(t, postMessage(t, c, e, "XR-100", "закрывай задачу"))
	if !strings.Contains(text, "цикл цели XR-100 не идёт") || !idleFlag(t, text) {
		t.Errorf("ответ при стоящем цикле не назвал его стоящим: %s", text)
	}
	if strings.Contains(text, "следующий виток") {
		t.Errorf("ответ обещает виток там, где поднимать его некому: %s", text)
	}
	if got := len(inboxLines(readFile(t, path))); got != 1 {
		t.Errorf("строка не легла во «Входящие»:\n%s", readFile(t, path))
	}

	// Повтор той же реплики тоже не молчит: человек жмёт «Отправить» второй раз
	// как раз потому, что ответа не дождался.
	again := body(t, postMessage(t, c, e, "XR-100", "закрывай задачу"))
	if !strings.Contains(again, "не идёт") || !strings.Contains(again, "уже лежит") || !idleFlag(t, again) {
		t.Errorf("повтор при стоящем цикле не назвал стоящий цикл: %s", again)
	}
	if got := len(inboxLines(readFile(t, path))); got != 1 {
		t.Errorf("повтор при стоящем цикле завёл вторую строку:\n%s", readFile(t, path))
	}

	// Поднятый цикл возвращает прежний ответ: обещание следующего витка при
	// живой работе честно, и путать его с отказом нельзя.
	liveCycle(t, e)
	live := body(t, postMessage(t, c, e, "XR-100", "и журнал тоже"))
	if !strings.Contains(live, "следующий виток") || idleFlag(t, live) {
		t.Errorf("ответ при идущем цикле назвал цикл стоящим: %s", live)
	}
}

// meeting делает встречу горутин на шве записи: пришедшие ждут друг друга и
// расходятся, когда собрались все либо когда вышел срок. Без замка все
// попытки доходят до записи разом, под замком дальше первой не проходит
// никто, и разницу видно и по счётчику, и по самому файлу цели.
func meeting(hands int) (probe func(root string), count func() int) {
	var mu sync.Mutex
	arrived := 0
	met := make(chan struct{})
	return func(string) {
			mu.Lock()
			arrived++
			if arrived == hands {
				close(met)
			}
			mu.Unlock()
			select {
			case <-met:
			case <-time.After(300 * time.Millisecond):
			}
		}, func() int {
			mu.Lock()
			defer mu.Unlock()
			return arrived
		}
}

// Разные сообщения, отправленные разом, все доходят до «Входящих». Файл цели
// переписывается целиком поверх прочитанного снимка, поэтому без замка запись
// второго ложится поверх первой и сообщение пропадает молча (замечание ревью
// DK-287): не дубль, а потеря.
func TestMessageConcurrentSendKeepsEveryLine(t *testing.T) {
	e, c, _ := messagesEnv(t, "")
	path := filepath.Join(e.proj, "docs", "tasks", "XR-100.md")

	const hands = 4
	probe, _ := meeting(hands)
	e.s.inboxProbe = probe

	var wg sync.WaitGroup
	for i := 0; i < hands; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			postMessage(t, c, e, "XR-100", fmt.Sprintf("сообщение %d", n)).Body.Close()
		}(i)
	}
	wg.Wait()

	doc := readFile(t, path)
	for i := 0; i < hands; i++ {
		if !strings.Contains(doc, fmt.Sprintf("из дашборда: сообщение %d", i)) {
			t.Errorf("сообщение %d потерялось под чужой записью:\n%s", i, doc)
		}
	}
}

// Один текст, отправленный разом, ложится одной строкой. Случай не
// выдуманный: очередь исходящих дожимает сообщение своим циклом в каждой
// открытой вкладке чата, и по событию online обе шлют его почти одновременно
// (DK-287). Одного числа строк тут мало: при одинаковом тексте и совпавшей до
// минуты метке потерянная запись выглядит так же, как отбитый повтор, поэтому
// стенд смотрит ещё и сколько запросов дошло до записи. Под замком дальше
// сверки проходит один, остальные видят его строку и уходят повтором.
func TestMessageConcurrentSendKeepsOneLine(t *testing.T) {
	e, c, _ := messagesEnv(t, "")
	path := filepath.Join(e.proj, "docs", "tasks", "XR-100.md")

	const hands = 4
	probe, arrived := meeting(hands)
	e.s.inboxProbe = probe

	var wg sync.WaitGroup
	for i := 0; i < hands; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			postMessage(t, c, e, "XR-100", "проверь ленту").Body.Close()
		}()
	}
	wg.Wait()

	if got := arrived(); got != 1 {
		t.Errorf("до записи дошло запросов: %d, ждал один: сверка с лежащим идёт не под замком", got)
	}
	doc := readFile(t, path)
	if got := strings.Count(doc, "из дашборда: проверь ленту"); got != 1 {
		t.Fatalf("во «Входящих» %d строк одного сообщения, ждал одну:\n%s", got, doc)
	}
}

// Замок стоит на репозитории, а не на всём дашборде: под ним держится и
// коммит с пушем, а недоступный origin одного проекта запирал бы отправку во
// все доски разом (замечание ревью DK-287). Стенд держит запись одного
// проекта и смотрит, что сообщение соседнего уходит, не дожидаясь его.
func TestMessageInboxLockIsPerProject(t *testing.T) {
	e, c, _ := messagesEnv(t, "")
	// Второй проект того же дома: доску ему отдаёт та же фикстура taskctl, и
	// цель XR-100 у него своя.
	other := filepath.Join(filepath.Dir(e.proj), "vtoroy")
	mkProject(t, other)
	dir := filepath.Join(other, "docs", "tasks")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "XR-100.md"), []byte(goalFileFixture), 0o644); err != nil {
		t.Fatal(err)
	}

	holding := make(chan struct{})
	release := make(chan struct{})
	e.s.inboxProbe = func(root string) {
		if root != e.proj {
			return
		}
		close(holding)
		<-release
	}

	held := make(chan struct{})
	go func() {
		defer close(held)
		postMessage(t, c, e, "XR-100", "первый проект").Body.Close()
	}()
	<-holding

	free := make(chan struct{})
	go func() {
		defer close(free)
		doReq(t, c, "POST", e.srv.URL+"/api/projects/vtoroy/goals/XR-100/message",
			`{"text": "второй проект"}`).Body.Close()
	}()
	select {
	case <-free:
	case <-time.After(3 * time.Second):
		t.Error("замок одного проекта держит отправку в остальные: он общий на весь дашборд")
	}
	close(release)
	<-held

	doc := readFile(t, filepath.Join(other, "docs", "tasks", "XR-100.md"))
	if !strings.Contains(doc, "из дашборда: второй проект") {
		t.Errorf("сообщение соседнего проекта не легло:\n%s", doc)
	}
}

// Своей дашборд считает строку своего же вида: дата, время и подпись в
// начале. Рукописная строка, где та же фраза попалась в тексте, повтором не
// считается и настоящую отправку не отбивает (замечание ревью DK-281).
func TestMessageHandWrittenLineIsNotRepeat(t *testing.T) {
	e, c, _ := messagesEnv(t, "")
	path := filepath.Join(e.proj, "docs", "tasks", "XR-100.md")
	doc := readFile(t, path)
	hand := "- заметка себе: строка, из дашборда: проверь ленту, выглядит своей\n"
	if err := os.WriteFile(path, []byte(addInboxLine(doc, strings.TrimSuffix(hand, "\n"))), 0o644); err != nil {
		t.Fatal(err)
	}

	resp := postMessage(t, c, e, "XR-100", "проверь ленту, выглядит своей")
	text := body(t, resp)
	if strings.Contains(text, "уже лежит во «Входящих»") {
		t.Fatalf("рукописная строка сошла за свою и отбила отправку: %s", text)
	}
	if got := len(inboxLines(readFile(t, path))); got != 2 {
		t.Errorf("во «Входящих» %d строк, ждал две:\n%s", got, readFile(t, path))
	}
}

// line достаёт поле line из ответа ручки message.
func line(t *testing.T, resp string) string {
	t.Helper()
	var v struct {
		Line string `json:"line"`
	}
	if err := json.Unmarshal([]byte(resp), &v); err != nil {
		t.Fatalf("ответ не разобрался: %v (%s)", err, resp)
	}
	return v.Line
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
		"ждёт витка",
		"Написать агенту...",
		"Остановить агента",
		"во «Входящих» пусто",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("в static/app.js нет честной надписи %q", want)
		}
	}
}

// Реплика человека несёт своё состояние и встаёт в ленту до ответа сервера
// (DK-281): отправку и потерю связи видно на самой реплике, а не строкой в
// углу экрана, неушедшая остаётся в очереди и дожимается сама (DK-287), а
// подхваченная витком не исчезает, а подписывается прочитанной.
func TestStaticChatMessageStates(t *testing.T) {
	text := readFile(t, filepath.Join("static", "app.js"))
	for _, want := range []string{"в очереди", "ждёт витка", "прочитано агентом"} {
		if !strings.Contains(text, want) {
			t.Errorf("в static/app.js нет состояния реплики %q", want)
		}
	}
	// Кнопки повтора на реплике нет: неушедшее дожимает сам дашборд (DK-287).
	if strings.Contains(text, "Повторить") {
		t.Error("на реплике осталась кнопка «Повторить»: отправка снова уперлась в человека")
	}
	body := funcBody(t, text, "function makeOutbox(")
	// Пузырь рисуется до запроса, а не после ответа: порядок и есть предмет
	// починки.
	shown := strings.Index(body, `state: "queued"`)
	post := strings.Index(body, `await api(url, { method: "POST"`)
	if strings.Index(body, "mine.push(m)") < 0 || shown < 0 || post < 0 {
		t.Error("реплика встаёт в ленту после ответа сервера: на слабой связи отправка снова выглядит непрошедшей")
	}
	if !strings.Contains(body, `m.state = "queued"`) {
		t.Error("провал отправки не оставляет реплику в очереди")
	}
	if !strings.Contains(body, `m.state = "read"`) && !strings.Contains(body, `: "read"`) {
		t.Error("подхваченная витком реплика не подписывается прочитанной")
	}
	if !strings.Contains(body, "sentWrite(project, id, mine)") {
		t.Error("свои отправки не запоминаются: после перезагрузки след прочитанного пропадёт")
	}
}

// Обрыв связи это исключение из fetch, а не ответ со статусом, и проверкой
// текста исходника такой случай не берётся: он виден только исполнением. То
// же с автоповтором очереди, где предмет проверки это растущая пауза, событие
// online и переживший перезагрузку список. Стенд поднимает статику в node с
// заглушкой DOM, игрушечными часами и игрушечными «Входящими», рвёт связь и
// смотрит, что осталось на экране, в хранилище браузера и в файле цели. Без
// node шаг пропускается: узел стенда, а не рабочей части, и валить из-за него
// пакет не за что.
func TestChatOutboxQueueSendsItself(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node не найден: стенд очереди исходящих пропущен")
	}
	cmd := exec.Command(node, filepath.Join("testdata", "outbox_offline.mjs"),
		filepath.Join("static", "app.js"))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("очередь исходящих на оборванной связи: %v\n%s", err, out)
	}
	t.Log(strings.TrimSpace(string(out)))
}

// Второе нажатие «Отправить» подряд не уходит вслепую: кнопка на время
// отправки гаснет, а серверная дедупликация добивает случай оборванного
// ответа.
func TestStaticChatSendGuard(t *testing.T) {
	text := readFile(t, filepath.Join("static", "app.js"))
	body := funcBody(t, text, "function renderChat(")
	if !strings.Contains(body, "send.disabled = true") || !strings.Contains(body, "send.disabled = false") {
		t.Error("кнопка отправки не гаснет на время запроса: двойное нажатие снова шлёт два запроса")
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

// Прямая ссылка на чат обычной задачи (DK-208) не собирает заголовок
// goal-<id> и не рисует ленту переписки: экран отвечает словами и ведёт
// назад на задачу, вместо того чтобы прятать отказ ручки в подвале
// «Входящих» (DK-296).
func TestStaticChatRefusesNonGoal(t *testing.T) {
	app := readFile(t, filepath.Join("static", "app.js"))
	body := funcBody(t, app, "function renderChat(")
	if !strings.Contains(body, "if (!isGoalRow(board, id)) {") {
		t.Error("экран чата не проверяет строку доски: обычная задача снова попадёт в ленту")
	}
	refusal := body[strings.Index(body, "if (!isGoalRow(board, id)) {"):]
	ret := strings.Index(refusal, "\n  }")
	if ret > 0 {
		refusal = refusal[:ret]
	}
	if !strings.Contains(refusal, "не цель") {
		t.Error("отказ по прямой ссылке не называет причину «не цель»")
	}
	if !strings.Contains(refusal, `location.hash = project + "/" + id;`) {
		t.Error("отказ по прямой ссылке не ведёт обратно на задачу")
	}
	if !strings.Contains(refusal, "return;") {
		t.Error("отказ не останавливает отрисовку: под ним всё равно соберётся лента чата")
	}
	if !strings.Contains(body, `head.append(el("h2", "", "goal-" + id));`) {
		t.Error("заголовок чата цели ушёл: goal-<id> остаётся её именем")
	}
	if !strings.Contains(funcBody(t, app, "async function paint("),
		"renderChat(current.name, r.body.works, rt.id, board)") {
		t.Error("экран чата рисуется без доски: гейту нечем проверить, цель ли это")
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
	rebuild := strings.Index(body, "sync(box, items);")
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
// переписки остаётся сверху (макет «04 Переписка»). Крестик прячет плашку, а не
// снимает её с дерева: при вставшем цикле она возвращается словами отказа
// (DK-319), и снятую возвращать было бы нечем.
func TestStaticChatNoteAndAnchor(t *testing.T) {
	app := readFile(t, filepath.Join("static", "app.js"))
	body := funcBody(t, app, "function renderChat(")
	for _, want := range []string{`el("button", "nx")`, `icon("close")`, "note.hidden = true", "Закрыть"} {
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
