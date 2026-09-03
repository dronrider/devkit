package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/dronrider/devkit/internal/chat"
)

// Раздел «Черновики»: список накопителя, текст записи, груминг с его исходом и
// удаление записи. Стенд тот же, что у заведения: настоящий taskctl на
// фикстурной доске, tmux и claude исполняемыми фикстурами.

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
			`{"text": `+strconv.Quote(text)+`, "prio": "mid"}`).Body.Close()
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

// Правка записи с экрана: текст уезжает целиком той же ручкой, что читает его
// экран, пустой не затирает запись, а пропавший файл отбивается словами. Без
// этого экран черновика умел бы только читать, и правку записи приходилось бы
// вести в редакторе.
func TestDraftPutText(t *testing.T) {
	e, c, _ := tasksEnv(t)
	doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/drafts",
		`{"text": "уведомитель шумит из песочницы", "prio": "mid"}`).Body.Close()

	// База правки едет с текстом от той же ручки, что его отдаёт: без неё
	// сверять правку не с чем, и ручка её не принимает.
	resp := doReq(t, c, "PUT", e.srv.URL+"/api/projects/demo/drafts/XR-005",
		`{"text": "уведомитель шумит из песочницы\n\nвторым абзацем правка с экрана", "base": `+
			strconv.Quote(draftBase(t, c, e, "XR-005"))+`}`)
	text := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("правка черновика: %d %s", resp.StatusCode, text)
	}
	if !strings.Contains(text, "docs/tasks/drafts/XR-005.md") {
		t.Errorf("ответ не назвал файл записи: %s", text)
	}
	got := readFile(t, filepath.Join(e.proj, "docs", "tasks", "drafts", "XR-005.md"))
	if !strings.Contains(got, "вторым абзацем правка с экрана") {
		t.Errorf("правка до файла не доехала:\n%s", got)
	}
	if !strings.HasSuffix(got, "\n") {
		t.Errorf("файл записи остался без перевода строки в конце:\n%q", got)
	}

	// Пустой текст затёр бы запись, и удаление у черновика своё, с причиной.
	resp = doReq(t, c, "PUT", e.srv.URL+"/api/projects/demo/drafts/XR-005", `{"text": "   ", "prio": "mid"}`)
	text = body(t, resp)
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(text, "затёр бы запись") {
		t.Fatalf("пустая правка: %d %s, ожидал 400 со словами", resp.StatusCode, text)
	}
	if after := readFile(t, filepath.Join(e.proj, "docs", "tasks", "drafts", "XR-005.md")); after != got {
		t.Errorf("отбитая правка тронула файл:\n%s", after)
	}

	resp = doReq(t, c, "PUT", e.srv.URL+"/api/projects/demo/drafts/XR-404", `{"text": "нет такой записи", "prio": "mid"}`)
	text = body(t, resp)
	if resp.StatusCode != http.StatusNotFound || !strings.Contains(text, "черновика XR-404") {
		t.Fatalf("правка пропавшей записи: %d %s, ожидал 404 со словами", resp.StatusCode, text)
	}

	// Ручка изменяющая: чужая страница из браузера до неё не дотягивается.
	req, err := http.NewRequest("PUT", e.srv.URL+"/api/projects/demo/drafts/XR-005",
		strings.NewReader(`{"text": "правка с чужой страницы", "prio": "mid"}`))
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
		t.Errorf("правка черновика с чужим Origin: %d, ожидал 403", resp.StatusCode)
	}
}

// Список накопителя и текст записи несут заказ дословно, той же строкой, что
// унесёт headless-сессии groomPrompt: подсказка кнопки «Грумить»
// читает готовое поле вместо того, чтобы собирать его второй раз на клиенте
// (DK-286).
func TestDraftsCarryOrder(t *testing.T) {
	e, c, _ := tasksEnv(t)
	doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/drafts",
		`{"text": "уведомитель шумит из песочницы", "prio": "mid"}`).Body.Close()

	list := draftsResp(t, c, e)
	drafts, _ := list["drafts"].([]any)
	if len(drafts) != 1 {
		t.Fatalf("в накопителе %d черновиков, жду 1: %v", len(drafts), list)
	}
	first, _ := drafts[0].(map[string]any)
	// Заказ едет дословно тем же текстом, каким его собирает groomPrompt: слова
	// заказа сторожит TestDraftWaitingFromAsk, а тут проверяется, что до экрана
	// доезжает именно он, без пересборки на клиенте.
	if order, _ := first["order"].(string); order != groomPrompt("XR-005", "") {
		t.Errorf("заказ строки накопителя %q, ждал %q", order, groomPrompt("XR-005", ""))
	}

	resp := doReq(t, c, "GET", e.srv.URL+"/api/projects/demo/drafts/XR-005", "")
	text := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("текст черновика: %d %s", resp.StatusCode, text)
	}
	var withOrder struct {
		Order string `json:"order"`
	}
	json.Unmarshal([]byte(text), &withOrder)
	if withOrder.Order != groomPrompt("XR-005", "") {
		t.Errorf("заказ экрана записи не приехал: %s", text)
	}
}

// Накопитель приезжает в порядке разбора taskctl (DK-383): высокий уровень
// первым, немаркированный последним, и поле prio доезжает до строки как есть,
// чип уровня собирает его словами на клиенте.
func TestDraftsSortedByPrio(t *testing.T) {
	e, c, _ := tasksEnv(t)
	for _, text := range []string{"первая идея", "вторая идея", "третья идея"} {
		doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/drafts",
			`{"text": `+strconv.Quote(text)+`, "prio": "mid"}`).Body.Close()
	}
	runTaskctl(t, e.proj, "draft", "prio", "XR-005", "high")
	runTaskctl(t, e.proj, "draft", "prio", "XR-007", "low")
	// Немаркированным черновик остаётся только после снятия метки: запись без
	// уровня отбивается (DK-520), а группа в порядке разбора нужна тем, что
	// записаны раньше.
	runTaskctl(t, e.proj, "draft", "prio", "XR-006", "--clear")

	list := draftsResp(t, c, e)
	drafts, _ := list["drafts"].([]any)
	if len(drafts) != 3 {
		t.Fatalf("в накопителе %d черновиков, жду 3: %v", len(drafts), list)
	}
	for i, id := range []string{"XR-005", "XR-007", "XR-006"} {
		item, _ := drafts[i].(map[string]any)
		if got, _ := item["id"].(string); got != id {
			t.Errorf("место %d занимает %s, жду %s: %v", i, got, id, item)
		}
	}
	first, _ := drafts[0].(map[string]any)
	if prio, _ := first["prio"].(string); prio != "high" {
		t.Errorf("поле prio первого черновика %q, жду high: %v", prio, first)
	}
	plain, _ := drafts[2].(map[string]any)
	if prio, _ := plain["prio"].(string); prio != "" {
		t.Errorf("у немаркированного черновика оказалось имя уровня %q", prio)
	}
}

// «Грумить» поднимает сессию разбора живым чатом: tmux-сессия с
// интерактивным клиентом, заказом теми же словами, какими груминг просят в
// чате, и парами окружения, которыми поднятая сессия называет себя в реестре.
func TestDraftGroomPrompt(t *testing.T) {
	e, c, _ := tasksEnv(t)
	tmuxLog := filepath.Join(e.home, "tmux.log")
	writeTmuxFake(t, e.bin, tmuxLog, "")
	writeScript(t, e.bin, "claude", "exit 0")
	writeAgentctlFake(t, e.bin, harnessTiersFixture)
	doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/drafts",
		`{"text": "дашборд не показывает накопитель черновиков", "prio": "mid"}`).Body.Close()

	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/drafts/XR-005/groom", "")
	text := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("груминг черновика: %d %s", resp.StatusCode, text)
	}
	if !strings.Contains(text, `"session":"task-XR-005"`) {
		t.Errorf("имя сессии груминга не то: %s", text)
	}
	// Дом в парах окружения зависит от машины, поэтому сверяются два куска:
	// начало команды с именем сессии и её хвост с задачей и заказом.
	got := readFile(t, tmuxLog)
	for _, want := range []string{
		"new-session -d -s task-XR-005 -c " + e.proj + " ",
		// Правило плана цепляется к заказу на самом запуске: по этому плану
		// дашборд рисует деления кольца и блок «План агента». Рядом с ним едет
		// правило канала: грумер разговаривает с человеком, и узнавать его в
		// доставке панели обязан так же, как агент чата.
		"DEVKIT_TASK='XR-005' DEVKIT_TMUX='task-XR-005' claude --model 'модель-pro' '" +
			groomPrompt("XR-005", "") + " " + planRule +
			" Если CLAUDE_CODE_SESSION_ID пуст, веди план файлом ~/.devkit/plans/task-XR-005.json. " +
			channelRule + "'",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("сессия груминга поднята не так:\n%s\nжду %q", got, want)
		}
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

// Раздел «Черновики» на экране: список записей, груминг своим именем, свой
// экран у записи и вход в раздел с доски и с главной.
func TestStaticDraftsSection(t *testing.T) {
	text := readFile(t, filepath.Join("static", "app.js"))
	for _, want := range []string{
		"Черновики",
		"Грумить",
		"async function renderDrafts(",
		"async function renderDraft(",
		"async function groomDraft(",
		"async function dropDraft(",
		"/drafts",
		"груминга",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("в static/app.js нет надписи %q", want)
		}
	}
	// Черновик пишется с телефона, и завести его с главной есть чем: пункт
	// меню у плюса карточки проекта открывает ту же форму заведения, что и
	// кнопка с доски.
	plus := funcBody(t, text, "function makeMenuAt(")
	if !strings.Contains(plus, `"Черновик"`) {
		t.Error("в меню заведения нет пункта черновика")
	}
	// С доски вход в накопитель это второй таб экрана, а не раздел меню:
	// черновики лежат на той же доске, и разделом стояли наравне с «Агентами»,
	// у которых обзор всех проектов сразу (решение пользователя).
	if strings.Contains(readFile(t, filepath.Join("static", "index.html")), `id="nav-drafts"`) {
		t.Error("раздел черновиков вернулся в меню: накопитель это таб доски")
	}
	kinds := funcBody(t, text, "function boardKinds(")
	for _, want := range []string{`"Задачи"`, `"Сессии"`, `"Черновики"`} {
		if !strings.Contains(kinds, want) {
			t.Errorf("в табах доски нет %q", want)
		}
	}
	if !strings.Contains(funcBody(t, text, "function boardKindHash("), `"/drafts"`) {
		t.Error("таб черновиков не ведёт на адрес накопителя")
	}
	if !strings.Contains(funcBody(t, text, "async function renderDrafts("), `boardKindBar(project, "drafts")`) {
		t.Error("накопитель открывается без табов доски: дороги назад к задачам нет")
	}
	if !strings.Contains(funcBody(t, text, "function renderBoard("), `boardKindBar(project, "tasks")`) {
		t.Error("на доске нет таба черновиков")
	}
	if strings.Contains(funcBody(t, text, "function renderBoard("), "draftsButton(") {
		t.Error("кнопка черновиков вернулась на доску")
	}
	// Раздел это свой экран хэша, иначе с телефона на него не сослаться.
	if !strings.Contains(text, `parts[1] === "drafts"`) {
		t.Error("у раздела черновиков нет своего хэша: route его не узнаёт")
	}
	if !strings.Contains(text, "renderDrafts(current.name, current.works)") {
		t.Error("экран черновиков не подключён к разбору хэша")
	}
	// У записи свой экран: на него ведёт строка накопителя, и на него же
	// ссылаются с телефона.
	if !strings.Contains(text, `parts[1] === "draft"`) {
		t.Error("у экрана черновика нет своего хэша: route его не узнаёт")
	}
	if !strings.Contains(text, "renderDraft(current.name, current.works, rt.id)") {
		t.Error("экран черновика не подключён к разбору хэша")
	}
	// Уровень разбора рисуется чипом в строке накопителя: список отсортирован
	// taskctl, и перемена порядка должна быть видна на экране (DK-383).
	if !strings.Contains(text, "DRAFT_PRIO") || !strings.Contains(text, "d.prio") {
		t.Error("в static/app.js нет чипа уровня разбора")
	}
	for _, word := range []string{"высокий", "средний", "низкий"} {
		if !strings.Contains(text, word) {
			t.Errorf("в static/app.js нет русского слова уровня %q", word)
		}
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
		`{"text": "мысль про накопитель", "prio": "mid"}`).Body.Close()

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
		strings.NewReader(`{"text": "новая мысль", "prio": "mid"}`))
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

// draftsEnv это стенд накопителя: доска и утилита те же, что у задач, а
// git-фикстура переносит и удаляет файлы по-настоящему. Молчаливый git из
// tasksEnv отвечает удачей на всё, и «git rm» черновика оставлял бы файл на
// диске: исход груминга читается как раз следами файлов, и на такой фикстуре
// проверка доказывала бы обратное живому прогону.
func draftsEnv(t *testing.T) (*testEnv, *http.Client, string) {
	t.Helper()
	e, c, _ := tasksEnv(t)
	gitLog := filepath.Join(e.home, "git.log")
	writeScript(t, e.bin, "git", fmt.Sprintf(`printf '%%s\n' "$*" >> %q
case "$3" in
rm) shift 6; rm -f "$@" ;;
mv) mv "$4" "$5" ;;
esac
exit 0`, gitLog))
	return e, c, gitLog
}

// makeDraft записывает черновик ручкой дашборда и отдаёт выданный ID.
func makeDraft(t *testing.T, c *http.Client, e *testEnv, text string) string {
	t.Helper()
	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/drafts",
		`{"text": `+strconv.Quote(text)+`, "prio": "mid"}`)
	got := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("черновик %q не записался: %d %s", text, resp.StatusCode, got)
	}
	var v struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(got), &v); err != nil || v.ID == "" {
		t.Fatalf("ID черновика не приехал: %v %s", err, got)
	}
	return v.ID
}

// Ответ на вопрос груминга уходит новой ходкой: уточнение едет в заказ той же
// сессии разбора, писать в закончившуюся дашборд не умеет и не изображает.
func TestDraftGroomAsk(t *testing.T) {
	e, c, _ := draftsEnv(t)
	tmuxLog := filepath.Join(e.home, "tmux.log")
	writeTmuxFake(t, e.bin, tmuxLog, "")
	writeScript(t, e.bin, "claude", "exit 0")
	writeAgentctlFake(t, e.bin, harnessTiersFixture)
	id := makeDraft(t, c, e, "две записи об одном и том же")

	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/drafts/"+id+"/groom",
		`{"ask": "оставить эту, вторую снять"}`)
	text := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("повторный груминг: %d %s", resp.StatusCode, text)
	}
	if !strings.Contains(text, "уточнение уехало в заказ") {
		t.Errorf("ответ не говорит, что уточнение уехало новой ходкой: %s", text)
	}
	want := "claude --model 'модель-pro' '" + groomPrompt(id, "оставить эту, вторую снять") +
		" " + planRule +
		" Если CLAUDE_CODE_SESSION_ID пуст, веди план файлом ~/.devkit/plans/task-" + id + ".json. " +
		channelRule + "'"
	if got := readFile(t, tmuxLog); !strings.Contains(got, want) {
		t.Errorf("уточнение не доехало до заказа сессии:\n%s\nжду %q", got, want)
	}
}

// Уточнение это свободный текст человека, и до сессии он едет через shell:
// tmux склеивает хвост new-session пробелами и отдаёт строку шеллу. Кавычка и
// обратные кавычки в уточнении не должны ни рвать команду, ни исполняться,
// поэтому заказ из журнала фикстуры разбирается настоящим shell, а не глазами
// (замечание ревью DK-321).
func TestDraftGroomAskQuoting(t *testing.T) {
	e, c, _ := draftsEnv(t)
	tmuxLog := filepath.Join(e.home, "tmux.log")
	writeTmuxFake(t, e.bin, tmuxLog, "")
	writeScript(t, e.bin, "claude", "exit 0")
	writeAgentctlFake(t, e.bin, harnessTiersFixture)
	id := makeDraft(t, c, e, "запись с непростым уточнением")

	ask := "оставить 'эту', а `rm -rf /` не трогать"
	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/drafts/"+id+"/groom",
		`{"ask": `+strconv.Quote(ask)+`}`)
	if got := body(t, resp); resp.StatusCode != http.StatusOK {
		t.Fatalf("груминг с уточнением в кавычках: %d %s", resp.StatusCode, got)
	}
	logged := readFile(t, tmuxLog)
	// Перед заказом в команде стоит ярус («--model 'модель-pro'»), и разбирается
	// тут сам заказ: он лежит последним аргументом, за первой же кавычкой после
	// имени клиента.
	cut := strings.LastIndex(logged, "claude ")
	if cut < 0 {
		t.Fatalf("сессия с заказом не поднялась:\n%s", logged)
	}
	tail := logged[cut+len("claude "):]
	at := strings.Index(tail, "'Проведи груминг")
	if at < 0 {
		t.Fatalf("заказа в команде нет:\n%s", logged)
	}
	quoted := strings.TrimSpace(tail[at:])
	// Тот же разбор, что сделает шелл tmux: заказ обязан прийти одной строкой и
	// ровно тем текстом, который написал человек. Порванная цитата уронила бы
	// сам shell, а неэкранированные обратные кавычки подставили бы сюда вывод
	// команды.
	out, err := exec.Command("sh", "-c", "printf '%s' "+quoted).Output()
	if err != nil {
		t.Fatalf("заказ с кавычками не разобрался шеллом: %v\n%s", err, quoted)
	}
	want := groomPrompt(id, ask) + " " + planRule +
		" Если CLAUDE_CODE_SESSION_ID пуст, веди план файлом ~/.devkit/plans/task-" + id + ".json. " +
		channelRule
	if string(out) != want {
		t.Errorf("заказ доехал до шелла не тем текстом:\n%s\nжду\n%s", out, want)
	}
}

// Удаление черновика с экрана: причина обязательна и уезжает в коммит доски,
// файла после команды нет. До DK-321 черновик снимался только терминалом.
func TestDraftDrop(t *testing.T) {
	e, c, gitLog := draftsEnv(t)
	id := makeDraft(t, c, e, "мусор из одного слова show")
	path := filepath.Join(e.proj, "docs", "tasks", "drafts", id+".md")

	resp := doReq(t, c, "DELETE", e.srv.URL+"/api/projects/demo/drafts/"+id, `{"reason": "   "}`)
	text := body(t, resp)
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(text, "жду причину") {
		t.Fatalf("удаление без причины: %d %s, ожидал 400 со словами", resp.StatusCode, text)
	}
	if !isFile(path) {
		t.Fatalf("отбитый запрос всё равно удалил файл %s", path)
	}

	resp = doReq(t, c, "DELETE", e.srv.URL+"/api/projects/demo/drafts/"+id,
		`{"reason": "след промаха мимо подкоманды, разбирать нечего"}`)
	text = body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("удаление черновика: %d %s", resp.StatusCode, text)
	}
	if isFile(path) {
		t.Errorf("файл черновика остался на месте: %s", path)
	}
	want := "docs(tasks): " + id + " черновик удалён с дашборда: след промаха мимо подкоманды, разбирать нечего"
	if got := readFile(t, gitLog); !strings.Contains(got, want) {
		t.Errorf("причина не уехала в коммит доски:\n%s\nжду %q", got, want)
	}

	// Второе нажатие с устаревшего экрана: удалять нечего, и причина этому
	// называется словами.
	resp = doReq(t, c, "DELETE", e.srv.URL+"/api/projects/demo/drafts/"+id, `{"reason": "ещё раз"}`)
	text = body(t, resp)
	if resp.StatusCode != http.StatusNotFound || !strings.Contains(text, "удалять нечего") {
		t.Fatalf("удаление пропавшего черновика: %d %s, ожидал 404 со словами", resp.StatusCode, text)
	}
}

// Ручка удаления изменяющая: без входа и с чужим Origin она не срабатывает, а
// файл остаётся на месте.
func TestDraftDropAuthAndOrigin(t *testing.T) {
	e, c, _ := draftsEnv(t)
	id := makeDraft(t, c, e, "мысль про накопитель")
	path := filepath.Join(e.proj, "docs", "tasks", "drafts", id+".md")
	url := e.srv.URL + "/api/projects/demo/drafts/" + id

	resp := doReq(t, plainClient(), "DELETE", url, `{"reason": "чужими руками"}`)
	if text := body(t, resp); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("удаление без входа: %d %s, ожидал 401", resp.StatusCode, text)
	}
	req, err := http.NewRequest("DELETE", url, strings.NewReader(`{"reason": "чужой страницей"}`))
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
		t.Errorf("удаление с чужим Origin: %d, ожидал 403", resp.StatusCode)
	}
	if !isFile(path) {
		t.Errorf("отбитый запрос удалил файл %s", path)
	}
}

// Разбор черновика это такая же работа агента, как конвейер задачи, и подписка
// у него выбирается так же: платить за груминг человек хочет той квотой,
// которую выбрал (замечание пользователя). Прежде выбора не было вовсе, и
// разбор всегда шёл подпиской по умолчанию.
func TestDraftGroomOnChosenHarness(t *testing.T) {
	e, c, _ := draftsEnv(t)
	tmuxLog := filepath.Join(e.home, "tmux.log")
	writeTmuxFake(t, e.bin, tmuxLog, "")
	writeScript(t, e.bin, "claude", "exit 0")
	writeScript(t, e.bin, "клиент-2", "exit 0")
	writeAgentctlFake(t, e.bin, harnessJSONFixture)
	id := makeDraft(t, c, e, "мысль с телефона про подписку груминга")

	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/drafts/"+id+"/groom",
		`{"harness": "втораяtest"}`)
	text := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("груминг на второй подписке: %d %s", resp.StatusCode, text)
	}
	for _, want := range []string{`"harness":"втораяtest"`, "поднят на подписке втораяtest"} {
		if !strings.Contains(text, want) {
			t.Errorf("в ответе груминга нет %q: %s", want, text)
		}
	}
	// Клиент чужой подписки поднимается её же обвязкой, как у задачи и у чата.
	want := "' exec --harness 'втораяtest' -- 'клиент-2' --permission-mode auto 'Проведи груминг " + id
	if got := readFile(t, tmuxLog); !strings.Contains(got, want) {
		t.Errorf("разбор поднят мимо выбранной подписки:\n%s\nжду %q", got, want)
	}
}

// Имя подписки сверяется с раскладкой машины: экран, устаревший на смену
// конфига, поднимал бы разбор неизвестно на чём.
func TestDraftGroomUnknownHarnessRefused(t *testing.T) {
	e, c, _ := draftsEnv(t)
	tmuxLog := filepath.Join(e.home, "tmux.log")
	writeTmuxFake(t, e.bin, tmuxLog, "")
	writeScript(t, e.bin, "claude", "exit 0")
	writeAgentctlFake(t, e.bin, harnessJSONFixture)
	id := makeDraft(t, c, e, "черновик под несуществующую подписку")

	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/drafts/"+id+"/groom",
		`{"harness": "какой-то-третий"}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("неизвестная подписка прошла: %d %s", resp.StatusCode, body(t, resp))
	}
	if got := readFile(t, tmuxLog); strings.Contains(got, "new-session") {
		t.Errorf("сессию всё равно подняли: %s", got)
	}
}

// Без выбора подписки обвязки в заказе нет вовсе: разбор идёт клиентом
// подписки по умолчанию. Ярус при этом назван явно, см. стенд ниже.
func TestDraftGroomWithoutHarnessStaysPlain(t *testing.T) {
	e, c, _ := draftsEnv(t)
	tmuxLog := filepath.Join(e.home, "tmux.log")
	writeTmuxFake(t, e.bin, tmuxLog, "")
	writeScript(t, e.bin, "claude", "exit 0")
	writeAgentctlFake(t, e.bin, harnessTiersFixture)
	id := makeDraft(t, c, e, "черновик без выбора подписки")

	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/drafts/"+id+"/groom", `{}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("груминг без выбора: %d %s", resp.StatusCode, body(t, resp))
	}
	got := readFile(t, tmuxLog)
	if strings.Contains(got, "exec --harness") {
		t.Errorf("разбор без выбора завернули в обвязку подписки: %s", got)
	}
	if !strings.Contains(got, "claude --model 'модель-pro' 'Проведи груминг "+id) {
		t.Errorf("заказ разбора поехал не тем клиентом: %s", got)
	}
}

// Ярус разбора называется явно и по умолчанию это pro. Прежде команда шла
// клиентом без модели вовсе, то есть дефолтом самого клиента: у пользователя
// это верхний ярус, самая дорогая подписка, которую он не выбирал (замечание
// пользователя). Второй подписке ярус доезжает той же дорогой: её клиент
// принимает имя из своей лестницы (DK-750).
func TestDraftGroomTier(t *testing.T) {
	setup := func(t *testing.T) (*testEnv, *http.Client, string, string) {
		t.Helper()
		e, c, _ := draftsEnv(t)
		tmuxLog := filepath.Join(e.home, "tmux.log")
		writeTmuxFake(t, e.bin, tmuxLog, "")
		writeScript(t, e.bin, "claude", "exit 0")
		writeAgentctlFake(t, e.bin, harnessTiersFixture)
		return e, c, tmuxLog, makeDraft(t, c, e, "черновик про ярус разбора")
	}

	t.Run("основная подписка едет ярусом pro", func(t *testing.T) {
		e, c, tmuxLog, id := setup(t)
		resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/drafts/"+id+"/groom",
			`{"harness": "перваяtest"}`)
		text := body(t, resp)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("груминг на основной подписке: %d %s", resp.StatusCode, text)
		}
		if !strings.Contains(text, `"tier":"pro"`) || !strings.Contains(text, `"model":"модель-pro"`) {
			t.Errorf("ответ не назвал ярус с моделью: %s", text)
		}
		if got := readFile(t, tmuxLog); !strings.Contains(got, "--model 'модель-pro'") {
			t.Errorf("команда разбора поехала без модели яруса: %s", got)
		}
	})

	t.Run("выбранный ярус доезжает до команды", func(t *testing.T) {
		e, c, tmuxLog, id := setup(t)
		resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/drafts/"+id+"/groom",
			`{"harness": "перваяtest", "tier": "base"}`)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("груминг выбранным ярусом: %d %s", resp.StatusCode, body(t, resp))
		}
		got := readFile(t, tmuxLog)
		if !strings.Contains(got, "--model 'модель-base'") || strings.Contains(got, "модель-pro") {
			t.Errorf("выбранный ярус не доехал до команды: %s", got)
		}
	})

	t.Run("второй подписке ярус доезжает её же моделью", func(t *testing.T) {
		e, c, tmuxLog, id := setup(t)
		resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/drafts/"+id+"/groom",
			`{"harness": "втораяtest"}`)
		text := body(t, resp)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("груминг на второй подписке: %d %s", resp.StatusCode, text)
		}
		got := readFile(t, tmuxLog)
		if !strings.Contains(got, "exec --harness 'втораяtest'") {
			t.Errorf("разбор второй подписки поехал мимо её обвязки: %s", got)
		}
		if !strings.Contains(got, "--model 'вторая-pro'") {
			t.Errorf("разбор второй подписки потерял модель её яруса: %s", got)
		}
		if !strings.Contains(text, `"model":"вторая-pro"`) {
			t.Errorf("ответ не назвал модель яруса второй подписки: %s", text)
		}
	})

	t.Run("незнакомый ярус отбивается", func(t *testing.T) {
		e, c, tmuxLog, id := setup(t)
		resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/drafts/"+id+"/groom",
			`{"harness": "перваяtest", "tier": "космос"}`)
		text := body(t, resp)
		if resp.StatusCode != http.StatusBadRequest || !strings.Contains(text, "ярусы подписки") {
			t.Fatalf("незнакомый ярус: %d %s, ожидал 400 со списком ярусов", resp.StatusCode, text)
		}
		if got := readFile(t, tmuxLog); strings.Contains(got, "new-session") {
			t.Errorf("сессию всё равно подняли: %s", got)
		}
	})
}

// Остаток прошлого разбора грумингу не помеха. Разбор идёт живым чатом, и его
// tmux-сессия переживает конец хода: клиент стоит на приглашении. Работой такая
// сессия не считается, строка показывает свою кнопку, и отказ ручки стоял бы
// поперёк собственной кнопки экрана. Остаток снимается, на его месте встаёт
// заказанный разбор.
func TestDraftGroomOverIdleLeftover(t *testing.T) {
	e, c, _ := tasksEnv(t)
	tmuxLog := filepath.Join(e.home, "tmux.log")
	writeTmuxFake(t, e.bin, tmuxLog, `task-XR-005\n`)
	writeScript(t, e.bin, "claude", "exit 0")
	doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/drafts",
		`{"text": "дашборд не показывает накопитель черновиков", "prio": "mid"}`).Body.Close()

	// Клиент прошлого разбора жив, а хода в нём нет.
	writePeerTmux(t, e.home, "eeee5555-5555-4555-8555-555555555555", "task-XR-005:@2.%2", "idle")

	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/drafts/XR-005/groom", "")
	text := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("груминг поверх досчитавшего разбора: %d %s, ожидал подъём", resp.StatusCode, text)
	}
	got := readFile(t, tmuxLog)
	if !strings.Contains(got, "kill-session -t task-XR-005") {
		t.Errorf("остаток прошлого разбора не снят: %s", got)
	}
	if !strings.Contains(got, "new-session -d -s task-XR-005") {
		t.Errorf("новый разбор не поднялся: %s", got)
	}
}

// Упоминание черновика в разговоре идёт голым ID, а строки на доске у записи
// накопителя ещё нет: ссылка приводила на экран задачи, тот упирался в «нет
// строки», и с виду ссылка не открывалась вовсе (замечание пользователя).
// Отказ ручки задачи теперь называет запись накопителя отдельным полем, и по
// нему экран уходит туда, куда человек метил.
func TestTaskOfDraftIDNamesDraft(t *testing.T) {
	e, c, _ := tasksEnv(t)
	doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/drafts",
		`{"text": "ссылка на черновик из чата не открывается", "prio": "mid"}`).Body.Close()

	resp := doReq(t, c, "GET", e.srv.URL+"/api/projects/demo/tasks/XR-005", "")
	text := body(t, resp)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("ID черновика на ручке задачи: %d %s, ожидал 404", resp.StatusCode, text)
	}
	var got map[string]string
	if err := json.Unmarshal([]byte(text), &got); err != nil {
		t.Fatalf("отказ не разобрался: %v\n%s", err, text)
	}
	if got["draft"] != "XR-005" {
		t.Fatalf("отказ не назвал запись накопителя: %s", text)
	}
	if !strings.Contains(got["error"], "запись накопителя") ||
		!strings.Contains(got["error"], "docs/tasks/drafts/XR-005.md") {
		t.Errorf("отказ по черновику сказан не своими словами: %s", text)
	}

	// ID, за которым нет ни строки, ни файла, черновиком не притворяется:
	// экран остаётся на отказе, а не уезжает на пустую запись.
	resp = doReq(t, c, "GET", e.srv.URL+"/api/projects/demo/tasks/XR-777", "")
	text = body(t, resp)
	// Разбор идёт в свежую карту: json.Unmarshal подмешивает поля в занятую, и
	// поле прежнего ответа доехало бы сюда само собой.
	var plain map[string]string
	if err := json.Unmarshal([]byte(text), &plain); err != nil {
		t.Fatalf("отказ не разобрался: %v\n%s", err, text)
	}
	if plain["draft"] != "" || !strings.Contains(plain["error"], "нет строки XR-777") {
		t.Fatalf("ID без строки и без файла назван черновиком: %s", text)
	}
}

// Груминг идёт живым разговором и спрашивает в нём же (решение 1 LLD DK-354):
// вопросов на форме записи больше нет, а ожидание ответа видно кружком на
// кнопке чата. Признак тот же, что у строки доски: его кладёт taskctl ask во
// вход разговора, и разговор груминга носит имя task-<ID>, туда же кладёт ответ
// панель. Заодно тут сторожится сам заказ: он обязан звать агента спрашивать в
// разговоре, а не кончать заход вопросом.
func TestDraftWaitingFromAsk(t *testing.T) {
	e, c, _ := tasksEnv(t)
	doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/drafts",
		`{"text": "ссылка на черновик из чата не открывается", "prio": "mid"}`).Body.Close()

	order := draftsResp(t, c, e)["drafts"].([]any)[0].(map[string]any)["order"]
	if said, _ := order.(string); !strings.Contains(said, "taskctl ask XR-005") ||
		!strings.Contains(said, "вопросом заход не кончай") {
		t.Errorf("заказ груминга не велит спрашивать в разговоре: %v", order)
	}

	// Признак кладёт taskctl ask; тут он пишется тем же пакетом, что и у
	// инструмента, чтобы стенд не расходился с ним форматом.
	ask := chat.Ask{Until: time.Now().Add(5 * time.Minute), Task: "XR-005", Session: "sid-1",
		Questions: []chat.Question{{Text: "резать строку или поднять цену"}}}
	if err := chat.WriteAsk(e.proj, chat.TaskName("XR-005"), ask); err != nil {
		t.Fatal(err)
	}

	got := draftsResp(t, c, e)["drafts"].([]any)[0].(map[string]any)
	wait, ok := got["waiting"].(map[string]any)
	if !ok {
		t.Fatalf("строка накопителя не знает про ожидание ответа: %v", got)
	}
	if wait["state"] != "ждёт ответа" || wait["note"] != "спросил агент" {
		t.Errorf("ожидание записано не теми словами: %v", wait)
	}

	resp := doReq(t, c, "GET", e.srv.URL+"/api/projects/demo/drafts/XR-005", "")
	text := body(t, resp)
	if !strings.Contains(text, "ждёт ответа") || !strings.Contains(text, "спросил агент") {
		t.Errorf("экран записи не знает про ожидание ответа: %s", text)
	}

	// Ответ пришёл, инструмент снял признак: ожидания больше нет ни в списке,
	// ни на экране записи, и кружку гаснуть есть по чему.
	if err := chat.DropAsk(e.proj, chat.TaskName("XR-005")); err != nil {
		t.Fatal(err)
	}
	after := draftsResp(t, c, e)["drafts"].([]any)[0].(map[string]any)
	if _, still := after["waiting"]; still {
		t.Errorf("ожидание осталось после ответа: %v", after)
	}
	resp = doReq(t, c, "GET", e.srv.URL+"/api/projects/demo/drafts/XR-005", "")
	if text := body(t, resp); strings.Contains(text, "ждёт ответа") {
		t.Errorf("экран записи держит ожидание после ответа: %s", text)
	}
}

// Число записей накопителя едет вместе со списком проектов: им подписан таб
// черновиков на доске, а спрашивать ради баджа накопитель своей ручкой значило
// бы ходить за ним с каждого экрана. Считается оно чтением каталога, без
// подпроцесса taskctl.
func TestProjectsCountDrafts(t *testing.T) {
	e, c, _ := tasksEnv(t)
	drafts := func() int {
		t.Helper()
		resp := doReq(t, c, "GET", e.srv.URL+"/api/projects", "")
		var got struct {
			Projects []struct {
				Name   string `json:"name"`
				Drafts int    `json:"drafts"`
			} `json:"projects"`
		}
		if err := json.Unmarshal([]byte(body(t, resp)), &got); err != nil {
			t.Fatal(err)
		}
		for _, p := range got.Projects {
			if p.Name == "demo" {
				return p.Drafts
			}
		}
		t.Fatalf("проекта demo в списке нет: %+v", got)
		return 0
	}
	if n := drafts(); n != 0 {
		t.Errorf("пустой накопитель насчитал %d записей", n)
	}
	for _, text := range []string{"первая мысль", "вторая мысль"} {
		doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/drafts",
			`{"text": `+strconv.Quote(text)+`, "prio": "mid"}`).Body.Close()
	}
	if n := drafts(); n != 2 {
		t.Errorf("накопитель из двух записей насчитал %d", n)
	}
	// Посторонний файл записью не считается: накопитель это .md рядом с ними.
	if err := os.WriteFile(filepath.Join(e.proj, "docs", "tasks", "drafts", ".DS_Store"),
		[]byte("мусор"), 0o644); err != nil {
		t.Fatal(err)
	}
	if n := drafts(); n != 2 {
		t.Errorf("посторонний файл посчитан записью: %d", n)
	}
}

// Строка накопителя показывает дату правки записи, а не её возраст днями: возраст
// отвечал не на тот вопрос, а дата стоит в том же виде, что у строки доски
// (замечание пользователя). Считает её сервер по файлу записи.
func TestDraftsCarryMoved(t *testing.T) {
	e, c, _ := tasksEnv(t)
	doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/drafts",
		`{"text": "уведомитель шумит из песочницы", "prio": "mid"}`).Body.Close()
	file := filepath.Join(e.proj, "docs", "tasks", "drafts", "XR-005.md")
	when := time.Date(2026, 3, 17, 12, 0, 0, 0, time.Local)
	if err := os.Chtimes(file, when, when); err != nil {
		t.Fatalf("время правки записи не проставилось: %v", err)
	}

	list := draftsResp(t, c, e)
	drafts, _ := list["drafts"].([]any)
	if len(drafts) != 1 {
		t.Fatalf("в накопителе %d черновиков, жду 1: %v", len(drafts), list)
	}
	first, _ := drafts[0].(map[string]any)
	if moved, _ := first["moved"].(string); moved != "2026-03-17" {
		t.Errorf("дата правки записи приехала как %q, жду 2026-03-17: %v", first["moved"], first)
	}
	// Рядом с днём едет точное время: в ячейке стоит день, а подсказка по
	// наведению показывает час с минутой и давность («нужно отображать точную
	// дату с временем», замечание пользователя). Из одного дня их не собрать.
	at, _ := first["moved_at"].(float64)
	if int64(at) != when.Unix() {
		t.Errorf("точное время правки записи приехало как %v, жду %d: %v",
			first["moved_at"], when.Unix(), first)
	}
}

// Разбор поднимается с одного нажатия: подтверждения перед ним нет, число
// выбранных стоит в подписи кнопки, на время подъёма кнопка гаснет, а
// пропущенные записи названы строкой итога. Предмет проверки это собранная
// разметка и порядок вызовов, поэтому статика поднимается в node с заглушкой
// DOM (стенд testdata/poc_groomnow.mjs). Без node шаг пропускается: узел
// стенда, а не рабочей части.
func TestStaticGroomRunsWithoutConfirm(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node не найден: стенд запуска разбора пропущен")
	}
	out, err := exec.Command(node, filepath.Join("testdata", "poc_groomnow.mjs"),
		filepath.Join("static", "app.js")).CombinedOutput()
	if err != nil {
		t.Fatalf("запуск разбора: %v\n%s", err, out)
	}
	t.Log(strings.TrimSpace(string(out)))
}

// Живой случай пользователя: чат записи накопителя открыли пустым, следом
// подняли разбор, кружок ожил, а лента осталась пустой до перезагрузки. Всё
// пришивание было заперто на адрес new, а чат записи открывается её же ID.
func TestStaticGroomSewsPanel(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node не найден: стенд пришивания разбора пропущен")
	}
	out, err := exec.Command(node, filepath.Join("testdata", "poc_groomsew.mjs"),
		filepath.Join("static", "app.js")).CombinedOutput()
	if err != nil {
		t.Fatalf("пришивание панели записи: %v\n%s", err, out)
	}
	t.Log(strings.TrimSpace(string(out)))
}

// Тот же случай со стороны кода: подъём разбора называет свою сессию, пустая
// панель с привязкой ждёт её опросом, а ворота пачки судят по живой работе, а
// не по памяти подъёма (иначе повторный разбор запирался бы на четверть часа).
func TestStaticGroomLiftAndGate(t *testing.T) {
	app := readFile(t, filepath.Join("static", "app.js"))
	groom := funcBody(t, app, "async function groomDraft(")
	for _, want := range []string{"markRunLive(project, id, r.body.session)",
		"chatSewHere(project, id, r.body.session)"} {
		if !strings.Contains(groom, want) {
			t.Errorf("подъём разбора не оставляет следа сессии: нет %q", want)
		}
	}
	if !strings.Contains(app, "function chatSewHere(") {
		t.Error("опрос реестра с уже открытой панели заводить нечем")
	}
	// Пустая панель с привязкой ждёт сессию тем же опросом, что и адрес new.
	// Третьим в это же условие встал незачатый разговор (DK-397 POC): он ждёт
	// ровно ту же сессию, и заводить ему свой опрос было бы нечем.
	if !strings.Contains(app, "if (!again && (chatIsNew(st.addr) || st.blank || (!st.sid && st.task))) {") {
		t.Error("пустая панель записи не заводит опроса реестра: разбор доедет перезагрузкой")
	}
	state := funcBody(t, app, "async function chatState(")
	if !strings.Contains(state, `chatLiftOf(project, CHAT_NEW + ":" + st.task) ||`) ||
		!strings.Contains(state, "workSession(st.task, works)") {
		t.Error("панель записи не берёт имя поднявшейся сессии ни из памяти подъёма, ни из работ")
	}
	start := funcBody(t, app, "async function draftGroomStart(")
	if !strings.Contains(start, "workBusy(id, works)") {
		t.Error("ворота пачки судят идущий разбор памятью подъёма, а не живой работой")
	}
}

// Живой случай DK-482..486: разбор пачки поднял пять сессий, агенты работали, а
// чаты в панели стояли пустыми. Записи этих сессий легли в реестр дома машины,
// а дашборд со своим домом читал свой файл. Две половины починки: заказ разбора
// собирает окружение той же общей сборкой, что и прочие дороги подъёма, и
// поднятая сессия видна дашборду, в каком бы из двух домов ни оказалась её
// запись.
func TestDraftGroomOrderAndVisibility(t *testing.T) {
	e, c, _ := tasksEnv(t)
	tmuxLog := filepath.Join(e.home, "tmux.log")
	writeTmuxFake(t, e.bin, tmuxLog, "")
	writeScript(t, e.bin, "claude", "exit 0")
	writeAgentctlFake(t, e.bin, harnessTiersFixture)
	doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/drafts",
		`{"text": "накопитель черновиков не виден в панели", "prio": "mid"}`).Body.Close()

	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/drafts/XR-005/groom", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("груминг черновика: %d %s", resp.StatusCode, body(t, resp))
	}
	// Окружение заказа то же, что отдаёт общая сборка: своего набора у разбора
	// нет ни одной пары.
	want := e.s.launchEnv("XR-005", "task-XR-005", "")
	if got := readFile(t, tmuxLog); !strings.Contains(got, want) {
		t.Fatalf("заказ разбора собрал окружение по-своему:\n%s\nждал вхождение %q", got, want)
	}

	// Хук старта поднятой сессии пишет запись по своему HOME, то есть в дом
	// машины. Дашборд обязан увидеть её и оттуда.
	machine := t.TempDir()
	was := realHomeFn
	realHomeFn = func() string { return machine }
	t.Cleanup(func() { realHomeFn = was })
	writeBindsAt(t, machine, "2026-08-25T23:58:04 сессия 7749edb9-2222 задача XR-005 проект demo "+
		"дерево "+e.proj+" транскрипт /tmp/t.jsonl источник заказ повод startup tmux task-XR-005\n")

	sid, rec := e.s.binds().Leads("XR-005")
	if sid != "7749edb9-2222" {
		t.Fatalf("сессия разбора не видна дашборду сразу после подъёма: %q", sid)
	}
	if rec.Tmux != "task-XR-005" {
		t.Errorf("имя tmux-сессии разбора потерялось: %+v", rec)
	}
}

// Табличный вид трёх разделов доски (DK-353, POC DK-397): шапка колонок вместо
// заголовка карточки, порядок нажатием на подпись и память выбора в
// localStorage своим ключом на раздел. Порядок самих утилит при этом прежний,
// по возрастанию ID: груминг читает накопитель вместе с хвостом доски, и
// перестановка сбила бы ему пачку.
func TestStaticBoardTableHead(t *testing.T) {
	text := readFile(t, filepath.Join("static", "app.js"))
	for _, want := range []string{
		`const TBL_SORT_KEY = {`,
		`drafts: "devkit.dash.drafts.sort"`,
		`tasks: "devkit.dash.tasks.sort"`,
		`sess: "devkit.dash.sess.sort"`,
		`const TBL_SORT_OLD = { fresh: "date:desc", title: "title:asc" }`,
		"function tblHead(",
		"function tblSortNext(",
		"function draftsSorted(",
		"function tasksSorted(",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("табличный вид собран не тем блоком: нет %q", want)
		}
	}
	// Кнопка о двух положениях снята вместе со своими стилями: её работу
	// забрала шапка, а оставленная рядом отвечала бы на тот же вопрос вторым
	// способом.
	for _, gone := range []string{"draftSortBtn(", "DRAFT_SORT_MODES", "свежие сверху"} {
		if strings.Contains(text, gone) {
			t.Errorf("кнопка порядка о двух положениях осталась в клиенте: есть %q", gone)
		}
	}
	// Колонка важности есть только у накопителя, ранг только у доски, ход
	// работы только у сессий: приём один, состав колонок свой. Заголовок у
	// колонки хода рисованный: слово требовало под себя восемьдесят точек под
	// кружок в девять, а снятый заголовок человек забраковал отдельно.
	cols := funcBody(t, text, "const TBL_COLS = {")
	for _, want := range []string{`label: "Приоритет"`, `label: "Ранг"`,
		`key: "live", label: "Ход", ico: "`} {
		if !strings.Contains(cols, want) {
			t.Errorf("в колонках разделов нет %q", want)
		}
	}
	// Дата последней активности сессии стоит колонкой, а колонки возраста
	// рядом нет: человек прочитал «Идёт» и «Активность» как одно и то же, и
	// возраст уехал в подсказку той же даты.
	if !strings.Contains(cols, `label: "Активность"`) {
		t.Error("в колонках сессий нет даты последней активности")
	}
	if strings.Contains(cols, `key: "age"`) {
		t.Error("колонка возраста сессии осталась рядом с активностью")
	}
	// Отметка выбора и приоритет живут одной колонкой накопителя: врозь они
	// занимали две, и подпись «Приоритет» переставала влезать.
	if strings.Contains(cols, `key: "pick"`) {
		t.Error("отметка выбора осталась своей колонкой накопителя")
	}
	// Подсказка сортировки говорит по-русски: прежнее «Поставить список по
	// колонке» человек читать отказался.
	if strings.Contains(text, `"Поставить список`) {
		t.Error("подсказка колонки осталась прежней: «Поставить список по»")
	}
	heap := funcBody(t, text, "async function renderDrafts(")
	if !strings.Contains(heap, `tblHead("drafts"`) {
		t.Error("шапки колонок в накопителе нет")
	}
	if !strings.Contains(heap, "draftsSorted(drafts)") {
		t.Error("список накопителя рисуется порядком ответа сервера, а не выбранным")
	}
	if !strings.Contains(funcBody(t, text, "function renderBoard("), `tblHead("tasks"`) {
		t.Error("шапки колонок над доской нет")
	}
	if !strings.Contains(funcBody(t, text, "function paintSessionRows("), `tblHead("sess"`) {
		t.Error("шапки колонок у сессий нет")
	}
	// Перестановка идёт на месте, без второго похода за списком: данных для
	// порядка в уже полученном ответе хватает.
	if strings.Contains(funcBody(t, text, "function tblHead("), "api(") {
		t.Error("шапка ходит на сервер: список перебирается на клиенте")
	}
	css := readFile(t, filepath.Join("static", "style.css"))
	if strings.Contains(css, ".chd .dsort{") {
		t.Error("стили кнопки порядка остались без самой кнопки")
	}
	// Имя классов у шапки своё (tblh): словом thead в этой же таблице стилей
	// зовётся шапка экрана задачи, и общее имя перебивало ей раскладку.
	if strings.Contains(css, ".thead{display:grid") {
		t.Error("шапка колонок зовётся общим именем thead и ломает шапку экрана задачи")
	}
	// Колонки считает движок по colgroup, а не наше правило сетки: своих сеток
	// у шапки со строкой было две, и подписи вставали мимо ячеек.
	if !strings.Contains(css, "table-layout:fixed") {
		t.Error("таблица раздела не просит движок считать колонки по colgroup")
	}
	// Прежний приём: строка стояла своей сеткой, шапка своей, а связывали их
	// переменные ширин. Подписи всё равно вставали мимо ячеек, и возврат к
	// этому надо ловить сразу.
	if strings.Contains(css, "grid-template-columns:var(--tc-") {
		t.Error("шапка со строкой снова стоят двумя сетками, связанными переменными ширин")
	}
	// Ширину колонки правит человек тягой границы, а не наведение мыши.
	// Прежний приём схлопывал ранг с датой в ноль на время наведения и правил
	// ту же беду за человека и рывком.
	if strings.Contains(css, ".trow:hover{grid-template-columns:") ||
		strings.Contains(css, ".dsrow:hover{grid-template-columns:") {
		t.Error("ширины колонок всё ещё подменяются наведением мыши")
	}
	// Телефон: колонок туда не влезает ни одна раскладка, и таблица переводится
	// в блочный вид, иначе раздел уезжает горизонтальной прокруткой.
	if !strings.Contains(css, "  .tbl{display:block}\n  .tbl>colgroup{display:none}") {
		t.Error("на узком экране таблица держит колонки и унесёт страницу вбок")
	}
}

// draftBase спрашивает базу правки у той же ручки, что читает текст записи:
// экран берёт её оттуда же и возвращает при сохранении.
func draftBase(t *testing.T, c *http.Client, e *testEnv, id string) string {
	t.Helper()
	resp := doReq(t, c, "GET", e.srv.URL+"/api/projects/demo/drafts/"+id, "")
	got := body(t, resp)
	var v struct {
		Hash string `json:"hash"`
	}
	if err := json.Unmarshal([]byte(got), &v); err != nil {
		t.Fatalf("текст записи не разобрался: %v %s", err, got)
	}
	return v.Hash
}

// Правка сверяется с базой: экран получает хэш текста вместе с текстом и
// возвращает его при сохранении. Писателей у записи двое, человек с экрана и
// разбор, и без сверки правка одного молча затирала бы правку другого. Ответ
// на разошедшуюся базу несёт текущий текст с хэшем: экран показывает оба, и
// набранное человеком не пропадает.
func TestDraftPutBase(t *testing.T) {
	e, c, _ := tasksEnv(t)
	doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/drafts",
		`{"text": "уведомитель шумит из песочницы", "prio": "mid"}`).Body.Close()
	file := filepath.Join(e.proj, "docs", "tasks", "drafts", "XR-005.md")

	base := draftBase(t, c, e, "XR-005")
	if base == "" {
		t.Fatal("текст записи приехал без базы правки")
	}

	resp := doReq(t, c, "PUT", e.srv.URL+"/api/projects/demo/drafts/XR-005",
		`{"text": "уведомитель шумит из песочницы\n\nправка с базой", "base": `+strconv.Quote(base)+`}`)
	text := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("правка с верной базой: %d %s", resp.StatusCode, text)
	}
	var saved struct {
		Hash string `json:"hash"`
	}
	if err := json.Unmarshal([]byte(text), &saved); err != nil || saved.Hash == "" {
		t.Fatalf("ответ правки не назвал новую базу: %v %s", err, text)
	}
	if saved.Hash == base {
		t.Errorf("база после правки не поменялась: %s", saved.Hash)
	}
	if got := draftBase(t, c, e, "XR-005"); got != saved.Hash {
		t.Errorf("база в ответе правки %s, а у записи %s", saved.Hash, got)
	}

	// Та же база второй раз: так выглядит вторая вкладка, читавшая запись до
	// первой правки.
	was := readFile(t, file)
	resp = doReq(t, c, "PUT", e.srv.URL+"/api/projects/demo/drafts/XR-005",
		`{"text": "правка второго окна", "base": `+strconv.Quote(base)+`}`)
	text = body(t, resp)
	if resp.StatusCode != http.StatusConflict || !strings.Contains(text, "изменилась") {
		t.Fatalf("правка с разошедшейся базой: %d %s, ожидал 409 со словами", resp.StatusCode, text)
	}
	var back struct {
		Text string `json:"text"`
		Hash string `json:"hash"`
	}
	if err := json.Unmarshal([]byte(text), &back); err != nil {
		t.Fatalf("отказ не разобрался: %v %s", err, text)
	}
	if back.Text != was || back.Hash != saved.Hash {
		t.Errorf("отказ не принёс текущий текст с базой: %q %q", back.Text, back.Hash)
	}
	if after := readFile(t, file); after != was {
		t.Errorf("отбитая правка тронула файл:\n%s", after)
	}

	// Правка без базы это правка вслепую: сверять её не с чем.
	resp = doReq(t, c, "PUT", e.srv.URL+"/api/projects/demo/drafts/XR-005",
		`{"text": "правка вслепую", "prio": "mid"}`)
	text = body(t, resp)
	if resp.StatusCode != http.StatusConflict || !strings.Contains(text, "без базы") {
		t.Fatalf("правка без базы: %d %s, ожидал 409 со словами", resp.StatusCode, text)
	}
	if after := readFile(t, file); after != was {
		t.Errorf("правка без базы тронула файл:\n%s", after)
	}
}

// Замок разбора: пока по записи идёт грумминг, файл принадлежит агенту, и
// правка с экрана отбивается. Отпирает замок живое ожидание ответа: агент спит
// в инструменте ожидания, файла не трогает, и ответ правкой текста это законная
// дорога.
func TestDraftPutUnderGroom(t *testing.T) {
	e, c, _ := draftsEnv(t)
	writeTmuxFake(t, e.bin, filepath.Join(e.home, "tmux.log"), "task-XR-005\n")
	id := makeDraft(t, c, e, "две записи об одном и том же")
	file := filepath.Join(e.proj, "docs", "tasks", "drafts", id+".md")
	base := draftBase(t, c, e, id)
	was := readFile(t, file)

	resp := doReq(t, c, "PUT", e.srv.URL+"/api/projects/demo/drafts/"+id,
		`{"text": "правка под разбором", "base": `+strconv.Quote(base)+`}`)
	text := body(t, resp)
	if resp.StatusCode != http.StatusConflict || !strings.Contains(text, "идёт разбор") {
		t.Fatalf("правка под живым разбором: %d %s, ожидал 409 со словами", resp.StatusCode, text)
	}
	if after := readFile(t, file); after != was {
		t.Errorf("правка под разбором тронула файл:\n%s", after)
	}

	ask := chat.Ask{Until: time.Now().Add(5 * time.Minute), Task: id, Session: "sid-1",
		Questions: []chat.Question{{Text: "оставить эту или снять"}}}
	if err := chat.WriteAsk(e.proj, chat.TaskName(id), ask); err != nil {
		t.Fatal(err)
	}
	resp = doReq(t, c, "PUT", e.srv.URL+"/api/projects/demo/drafts/"+id,
		`{"text": "ответ правкой текста", "base": `+strconv.Quote(base)+`}`)
	text = body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("правка при живом ожидании: %d %s, ожидал 200", resp.StatusCode, text)
	}
	if got := readFile(t, file); !strings.Contains(got, "ответ правкой текста") {
		t.Errorf("правка при живом ожидании до файла не доехала:\n%s", got)
	}
}

// Экранная половина замка: карандаш, плашка и база правки. Проверяется она
// прогоном самой статики в node (стенд testdata/poc_draftlock.mjs), а не
// поиском строк в исходнике: предмет тут собранная разметка и поведение
// обработчиков. Без node шаг пропускается: узел стенда, а не рабочей части.
func TestStaticDraftEditorLock(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node не найден: стенд замка редактора записи пропущен")
	}
	out, err := exec.Command(node, filepath.Join("testdata", "poc_draftlock.mjs"),
		filepath.Join("static", "app.js")).CombinedOutput()
	if err != nil {
		t.Fatalf("замок редактора записи: %v\n%s", err, out)
	}
	t.Log(strings.TrimSpace(string(out)))
}
