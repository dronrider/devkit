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

// Список накопителя и текст записи несут заказ дословно, той же строкой, что
// унесёт headless-сессии groomPrompt: подсказка кнопки «Провести груминг»
// читает готовое поле вместо того, чтобы собирать его второй раз на клиенте
// (DK-286).
func TestDraftsCarryOrder(t *testing.T) {
	e, c, _ := tasksEnv(t)
	doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/drafts",
		`{"text": "уведомитель шумит из песочницы"}`).Body.Close()

	list := draftsResp(t, c, e)
	drafts, _ := list["drafts"].([]any)
	if len(drafts) != 1 {
		t.Fatalf("в накопителе %d черновиков, жду 1: %v", len(drafts), list)
	}
	first, _ := drafts[0].(map[string]any)
	if order, _ := first["order"].(string); order != "Проведи груминг XR-005" {
		t.Errorf("заказ строки накопителя %q, ждал «Проведи груминг XR-005»", order)
	}

	resp := doReq(t, c, "GET", e.srv.URL+"/api/projects/demo/drafts/XR-005", "")
	text := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("текст черновика: %d %s", resp.StatusCode, text)
	}
	if !strings.Contains(text, `"order":"Проведи груминг XR-005"`) {
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
			`{"text": `+strconv.Quote(text)+`}`).Body.Close()
	}
	runTaskctl(t, e.proj, "draft", "prio", "XR-005", "high")
	runTaskctl(t, e.proj, "draft", "prio", "XR-007", "low")

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

// «Провести груминг» поднимает сессию разбора той же механикой, что и конвейер
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

// Раздел «Черновики» на экране: список записей, груминг своим именем, свой
// экран у записи и вход в раздел с доски и с главной.
func TestStaticDraftsSection(t *testing.T) {
	text := readFile(t, filepath.Join("static", "app.js"))
	for _, want := range []string{
		"Черновики",
		"Провести груминг",
		"async function renderDrafts(",
		"async function renderDraft(",
		"function draftsButton(",
		"async function groomDraft(",
		"async function dropDraft(",
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
		`{"text": `+strconv.Quote(text)+`}`)
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

func draftOutcomeResp(t *testing.T, c *http.Client, e *testEnv, id string) map[string]any {
	t.Helper()
	resp := doReq(t, c, "GET", e.srv.URL+"/api/projects/demo/drafts/"+id+"/outcome", "")
	text := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("исход груминга %s: %d %s", id, resp.StatusCode, text)
	}
	var v map[string]any
	if err := json.Unmarshal([]byte(text), &v); err != nil {
		t.Fatalf("ответ об исходе не разобрался: %v\n%s", err, text)
	}
	return v
}

// Исход груминга читается следами на диске, а не гаданием: строка на доске,
// раздел «Из черновика <ID>» в чужом файле задачи, пометка об отложенном и
// пропавший файл это четыре разных ответа. До DK-321 экран накопителя не знал
// ни одного из них.
func TestDraftOutcomeTraces(t *testing.T) {
	e, c, _ := draftsEnv(t)

	// Разбора не было: запись лежит в накопителе как записана.
	fresh := makeDraft(t, c, e, "груминг сюда ещё не заходил")
	got := draftOutcomeResp(t, c, e, fresh)
	if got["state"] != "open" {
		t.Errorf("нетронутая запись приехала с исходом %v: %v", got["state"], got)
	}
	if file, _ := got["file"].(string); file != "docs/tasks/drafts/"+fresh+".md" {
		t.Errorf("путь файла записи не назван: %v", got)
	}

	// Отложен: пометка с причиной лежит в разделе «Грумминг» файла черновика.
	deferred := makeDraft(t, c, e, "мысль про повторный случай")
	runTaskctl(t, e.proj, "draft", "defer", deferred, "ждём повторного случая с git status")
	got = draftOutcomeResp(t, c, e, deferred)
	if got["state"] != "deferred" {
		t.Fatalf("отложенная запись приехала с исходом %v: %v", got["state"], got)
	}
	if reason, _ := got["reason"].(string); reason != "ждём повторного случая с git status" {
		t.Errorf("причина отложенного не приехала: %v", got)
	}
	if when, _ := got["deferred"].(string); when == "" {
		t.Errorf("дата пометки не приехала: %v", got)
	}

	// Похожая на пометку строка в свободном тексте записи исходом не считается:
	// «отложен» ставит разбор разделом «Грумминг», а не человек прозой.
	sneaky := makeDraft(t, c, e, "мысль про откладывание\n\nв прошлый раз пометка "+
		"выглядела так:\n- 2026-08-10, отложен: ждём смежника")
	got = draftOutcomeResp(t, c, e, sneaky)
	if got["state"] != "open" {
		t.Errorf("строка посреди прозы сошла за пометку разбора: %v", got)
	}

	// Приписан: текст уехал разделом в файл стоящей задачи, и в ответе стоит
	// номер приёмника, а не одно «черновика нет».
	attached := makeDraft(t, c, e, "то же самое, что XR-002")
	runTaskctl(t, e.proj, "draft", "attach", attached, "XR-002")
	got = draftOutcomeResp(t, c, e, attached)
	if got["state"] != "attached" {
		t.Fatalf("приписанная запись приехала с исходом %v: %v", got["state"], got)
	}
	if task, _ := got["task"].(string); task != "XR-002" {
		t.Errorf("приёмник приписки не назван: %v", got)
	}
	if file, _ := got["task_file"].(string); file != "docs/tasks/XR-002.md" {
		t.Errorf("файл приёмника не назван: %v", got)
	}

	// Оформлен: у черновика появилась строка на доске, и в ответе едет она сама.
	promoted := makeDraft(t, c, e, "мысль, из которой вышла задача")
	runTaskctl(t, e.proj, "add", "--id", promoted, "--title", "Из черновика", "--rank", "25+1+0+0+0", "--accept", "agent")
	got = draftOutcomeResp(t, c, e, promoted)
	if got["state"] != "row" {
		t.Fatalf("оформленная запись приехала с исходом %v: %v", got["state"], got)
	}
	row, _ := got["row"].(map[string]any)
	if id, _ := row["id"].(string); id != promoted {
		t.Errorf("строка доски не приехала вместе с исходом: %v", got)
	}

	// Удалён: следов нет ни одного, и сказано про это словами, а не пустотой.
	dropped := makeDraft(t, c, e, "мусор из одного слова show")
	doReq(t, c, "DELETE", e.srv.URL+"/api/projects/demo/drafts/"+dropped,
		`{"reason": "промах мимо подкоманды"}`).Body.Close()
	got = draftOutcomeResp(t, c, e, dropped)
	if got["state"] != "dropped" {
		t.Fatalf("удалённая запись приехала с исходом %v: %v", got["state"], got)
	}
	if note, _ := got["note"].(string); !strings.Contains(note, "коммит") {
		t.Errorf("про причину удаления, уехавшую в коммит, экрану не сказано: %v", got)
	}
}

// Груминг, кончившийся вопросом, следа на диске не оставляет, и вопрос лежит
// только в транскрипте: экран берёт последнее слово агента из разговора той же
// записи.
func TestDraftOutcomeQuestion(t *testing.T) {
	e, c, _ := draftsEnv(t)
	id := makeDraft(t, c, e, "две записи об одном и том же")
	transcript := `{"type":"user","message":{"role":"user","content":"Проведи груминг ` + id + `"},"timestamp":"2026-08-13T10:00:00.000Z","gitBranch":"main"}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Смотрю накопитель."}]},"timestamp":"2026-08-13T10:00:01.000Z"}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Какой из двух дублей оставить?"}]},"timestamp":"2026-08-13T10:00:02.000Z"}
`
	writeSession(t, e.home, e.proj, "", "aaaabbbbcccc1111", transcript, time.Now())

	got := draftOutcomeResp(t, c, e, id)
	if got["state"] != "open" {
		t.Fatalf("запись без следов разбора приехала с исходом %v: %v", got["state"], got)
	}
	if q, _ := got["question"].(string); q != "Какой из двух дублей оставить?" {
		t.Errorf("последнее слово груминга не приехало: %v", got)
	}
	if sid, _ := got["session"].(string); sid != "aaaabbbbcccc1111" {
		t.Errorf("разговор, из которого взят вопрос, не назван: %v", got)
	}
}

// Ответ на вопрос груминга уходит новой ходкой: уточнение едет в заказ той же
// сессии разбора, писать в закончившуюся дашборд не умеет и не изображает.
func TestDraftGroomAsk(t *testing.T) {
	e, c, _ := draftsEnv(t)
	tmuxLog := filepath.Join(e.home, "tmux.log")
	writeTmuxFake(t, e.bin, tmuxLog, "")
	writeScript(t, e.bin, "claude", "exit 0")
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
	want := "claude -p 'Проведи груминг " + id + ". Человек уточняет: оставить эту, вторую снять'"
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
	id := makeDraft(t, c, e, "запись с непростым уточнением")

	ask := "оставить 'эту', а `rm -rf /` не трогать"
	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/drafts/"+id+"/groom",
		`{"ask": `+strconv.Quote(ask)+`}`)
	if got := body(t, resp); resp.StatusCode != http.StatusOK {
		t.Fatalf("груминг с уточнением в кавычках: %d %s", resp.StatusCode, got)
	}
	logged := readFile(t, tmuxLog)
	cut := strings.LastIndex(logged, "claude -p ")
	if cut < 0 {
		t.Fatalf("сессия с заказом не поднялась:\n%s", logged)
	}
	quoted := strings.TrimSpace(logged[cut+len("claude -p "):])
	// Тот же разбор, что сделает шелл tmux: заказ обязан прийти одной строкой и
	// ровно тем текстом, который написал человек. Порванная цитата уронила бы
	// сам shell, а неэкранированные обратные кавычки подставили бы сюда вывод
	// команды.
	out, err := exec.Command("sh", "-c", "printf '%s' "+quoted).Output()
	if err != nil {
		t.Fatalf("заказ с кавычками не разобрался шеллом: %v\n%s", err, quoted)
	}
	want := "Проведи груминг " + id + ". Человек уточняет: " + ask
	if string(out) != want {
		t.Errorf("заказ доехал до шелла не тем текстом:\n%s\nжду\n%s", out, want)
	}
}

// Пометка «отложен» читается только в разделе «Грумминг»: запись это свободный
// текст человека, и строка того же вида посреди прозы выдавала бы за исход
// разбора то, чего разбор не ставил (замечание ревью DK-321).
func TestDraftDeferredOnlyInGroomSection(t *testing.T) {
	free := "мысль про откладывание\n\nв прошлый раз пометка выглядела так:\n" +
		"- 2026-08-10, отложен: ждём смежника\n"
	if when, reason := draftDeferred(free); when != "" || reason != "" {
		t.Errorf("похожая строка в свободном тексте сошла за пометку: %q %q", when, reason)
	}
	marked := free + "\n" + draftGroomHeading + "\n\n- 2026-08-12, отложен: ждём повторного случая\n"
	when, reason := draftDeferred(marked)
	if when != "2026-08-12" || reason != "ждём повторного случая" {
		t.Errorf("пометка раздела прочиталась как %q %q", when, reason)
	}
	// Раздел кончается следующим заголовком того же уровня, и строка за ним
	// принадлежит уже не разбору.
	after := "черновик\n\n" + draftGroomHeading + "\n\n- разбор заходил, исхода нет\n\n" +
		"## Хвост\n\n- 2026-08-13, отложен: не отсюда\n"
	if when, _ := draftDeferred(after); when != "" {
		t.Errorf("строка за концом раздела сошла за пометку: %q", when)
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

// След приписки ищется точным номером и мимо накопителя: «Из черновика XR-05»
// и «Из черновика XR-050» это разные записи, а заголовок внутри самого
// черновика приёмником не считается.
func TestDraftAttachedToExactID(t *testing.T) {
	dir := t.TempDir()
	tasks := filepath.Join(dir, "docs", "tasks")
	if err := os.MkdirAll(filepath.Join(tasks, "drafts"), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(rel, body string) {
		if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(rel)), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("docs/tasks/XR-010.md", "# XR-010: приёмник\n\n## Из черновика XR-050 (записан 2026-08-05)\n\nтекст записи\n")
	write("docs/tasks/drafts/XR-060.md", "мысль про приписку\n\n## Из черновика XR-060\n")

	task, rel := draftAttachedTo(dir, "XR-050")
	if task != "XR-010" || rel != "docs/tasks/XR-010.md" {
		t.Errorf("приёмник приписки найден как %q (%q), жду XR-010", task, rel)
	}
	if task, _ := draftAttachedTo(dir, "XR-05"); task != "" {
		t.Errorf("соседний номер XR-05 сошёл за приписку к %q", task)
	}
	if task, _ := draftAttachedTo(dir, "XR-060"); task != "" {
		t.Errorf("заголовок внутри самого накопителя сошёл за приписку к %q", task)
	}
}
