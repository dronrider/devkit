package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Взвод строки и цепочка уровнями с экрана (DK-942). Стенд гоняет настоящий
// taskctl на фикстурной доске: рёбра, взвод и его отказы считает утилита, и
// подменять её тут фикстурой значило бы проверять выдумку сервера.

// chainURL это ручка цепочки проекта стенда.
func chainURL(e *testEnv) string { return e.srv.URL + "/api/projects/demo/chain" }

// boardText читает доску стенда как есть: по ней видно и рёбра, и суффикс
// взвода, и то, что отказ цепочки не оставил половины записи.
func boardText(t *testing.T, e *testEnv) string {
	t.Helper()
	return readFile(t, filepath.Join(e.proj, "docs", "TASKS.md"))
}

// chainResp разбирает ответ ручки цепочки.
func chainResp(t *testing.T, c *http.Client, e *testEnv, ask string) (int, map[string]any) {
	t.Helper()
	resp := doReq(t, c, "POST", chainURL(e), ask)
	text := body(t, resp)
	var v map[string]any
	if err := json.Unmarshal([]byte(text), &v); err != nil {
		t.Fatalf("ответ цепочки не разобрался: %v\n%s", err, text)
	}
	return resp.StatusCode, v
}

// planLines вынимает план уровнями из ответа предпросмотра.
func planLines(t *testing.T, v map[string]any) []string {
	t.Helper()
	list, ok := v["plan"].([]any)
	if !ok {
		t.Fatalf("в ответе нет плана: %v", v)
	}
	out := []string{}
	for _, item := range list {
		out = append(out, fmt.Sprint(item))
	}
	return out
}

// freshChainEnv это стенд с чистой очередью: фикстурные рёбра XR-002 и XR-003
// снимаются, потому что цепочка ставит свои, и прежние мешали бы читать итог.
func freshChainEnv(t *testing.T) (*testEnv, *http.Client, string) {
	t.Helper()
	e, c, gitLog := tasksEnv(t)
	runTaskctl(t, e.proj, "dep", "rm", "XR-002", "XR-001")
	runTaskctl(t, e.proj, "dep", "rm", "XR-003", "XR-002")
	return e, c, gitLog
}

// DoD: цепочка из трёх строк на двух уровнях собирается диалогом и ложится на
// доску теми же рёбрами, что даёт `chain`. Сходимость проверяется не на глаз:
// план предпросмотра сверяется с планом той же команды, позванной напрямую.
func TestChainLaysThreeRowsOnTwoLevels(t *testing.T) {
	e, c, gitLog := freshChainEnv(t)
	ask := `{"after": ["XR-001"], "levels": [["XR-002", "XR-003"], ["XR-004"]], "dry_run": true}`

	code, v := chainResp(t, c, e, ask)
	if code != http.StatusOK {
		t.Fatalf("предпросмотр цепочки: %d %v", code, v)
	}
	got := strings.Join(planLines(t, v), "\n")
	want := runTaskctl(t, e.proj, "chain", "--after", "XR-001", "XR-002 XR-003", "XR-004", "--dry-run")
	if got != want {
		t.Fatalf("план диалога разошёлся с планом самой команды:\nдиалог:\n%s\nкоманда:\n%s", got, want)
	}

	code, v = chainResp(t, c, e, `{"after": ["XR-001"], "levels": [["XR-002", "XR-003"], ["XR-004"]]}`)
	if code != http.StatusOK {
		t.Fatalf("запись цепочки: %d %v", code, v)
	}
	// Рёбра и взвод читаются у самой утилиты, а не по ответу ручки на слово.
	for id, after := range map[string]string{
		"XR-002": "XR-001",
		"XR-003": "XR-001",
		"XR-004": "XR-002,XR-003",
	} {
		var d struct {
			After []string `json:"after"`
			Armed bool     `json:"armed"`
		}
		out := runTaskctl(t, e.proj, "dep", "list", id, "--json")
		if err := json.Unmarshal([]byte(out), &d); err != nil {
			t.Fatalf("dep list %s не разобрался: %v\n%s", id, err, out)
		}
		if strings.Join(d.After, ",") != after {
			t.Errorf("%s после %v, ожидал %s", id, d.After, after)
		}
		if !d.Armed {
			t.Errorf("%s легла без взвода: цепочка взводит каждый уровень", id)
		}
	}
	git := readFile(t, gitLog)
	// Коммит один на всю цепочку, и ID в его subject это первая строка первого
	// уровня: по ней цепочку ищут в истории доски.
	for _, want := range []string{"add -- docs/TASKS.md", "docs(tasks): XR-002 цепочка с дашборда", "push"} {
		if !strings.Contains(git, want) {
			t.Errorf("в вызовах git нет %q: %s", want, git)
		}
	}
	if n := strings.Count(git, "цепочка с дашборда"); n != 1 {
		t.Errorf("коммитов цепочки %d, жду один на всю запись: %s", n, git)
	}
}

// Предпросмотр доску не трогает: план это тот же `--dry-run`, и человек читает
// рёбра до записи, а не после.
func TestChainDryRunLeavesBoardAlone(t *testing.T) {
	e, c, gitLog := freshChainEnv(t)
	was := boardText(t, e)

	code, v := chainResp(t, c, e, `{"after": ["XR-001"], "levels": [["XR-002"]], "dry_run": true}`)
	if code != http.StatusOK {
		t.Fatalf("предпросмотр: %d %v", code, v)
	}
	plan := planLines(t, v)
	if len(plan) != 1 || !strings.Contains(plan[0], "уровень 1: XR-002 [после XR-001] [взвод]") {
		t.Errorf("план предпросмотра %v", plan)
	}
	if now := boardText(t, e); now != was {
		t.Errorf("предпросмотр переписал доску:\n%s", now)
	}
	if git := readFile(t, gitLog); strings.Contains(git, "цепочка") {
		t.Errorf("предпросмотр закоммитил доску: %s", git)
	}
}

// Отказ на любом уровне останавливает всю цепочку без единой записи на доску, и
// причина приезжает словами утилиты. Взводится только строка Backlog, и
// начатая строка второго уровня валит цепочку целиком, включая первый уровень.
func TestChainRefusalWritesNothing(t *testing.T) {
	e, c, _ := freshChainEnv(t)
	runTaskctl(t, e.proj, "move", "XR-004", "in-progress")
	was := boardText(t, e)

	code, v := chainResp(t, c, e, `{"after": ["XR-001"], "levels": [["XR-002"], ["XR-004"]]}`)
	if code != http.StatusBadRequest {
		t.Fatalf("отказ цепочки ждал 400, а был %d: %v", code, v)
	}
	said := fmt.Sprint(v["error"])
	if !strings.Contains(said, "XR-004") || !strings.Contains(said, "Backlog") {
		t.Errorf("причина отказа не назвала строку и рубеж: %q", said)
	}
	if now := boardText(t, e); now != was {
		t.Errorf("отказ второго уровня оставил запись первого:\n%s", now)
	}
}

// Пустой уровень и чужой номер до подпроцесса не доходят, а человек получает
// ту же причину словами.
func TestChainRefusesEmptyLevelAndStrangeID(t *testing.T) {
	e, c, _ := freshChainEnv(t)
	for ask, want := range map[string]string{
		`{"levels": []}`:                            "хотя бы один уровень",
		`{"levels": [[]]}`:                          "уровень 1 пуст",
		`{"levels": [["не задача"]]}`:               "не похоже на ID задачи",
		`{"after": ["ой"], "levels": [["XR-002"]]}`: "не похоже на ID задачи",
	} {
		code, v := chainResp(t, c, e, ask)
		if code != http.StatusBadRequest {
			t.Errorf("запрос %s ждал 400, а был %d: %v", ask, code, v)
			continue
		}
		if said := fmt.Sprint(v["error"]); !strings.Contains(said, want) {
			t.Errorf("запрос %s: причина %q, ждал %q", ask, said, want)
		}
	}
}

// DoD: переключатель ставит и снимает `[взвод]`. Суффикс читается с самой
// доски, а правка коммитится тем же порядком, что и прочие правки строки.
func TestArmToggleputsAndDropsSuffix(t *testing.T) {
	e, c, gitLog := tasksEnv(t)

	resp := doReq(t, c, "POST", taskURL(e, "XR-004", "/arm"), `{"on": true}`)
	if text := body(t, resp); resp.StatusCode != http.StatusOK {
		t.Fatalf("взвод: %d %s", resp.StatusCode, text)
	}
	if b := boardText(t, e); !strings.Contains(b, "| XR-004 | Четвёртая [взвод] |") {
		t.Fatalf("суффикс взвода не лёг в строку:\n%s", b)
	}
	git := readFile(t, gitLog)
	if !strings.Contains(git, "docs(tasks): XR-004 взведена с дашборда") {
		t.Errorf("взвод не закоммичен: %s", git)
	}

	resp = doReq(t, c, "POST", taskURL(e, "XR-004", "/arm"), `{"on": false}`)
	if text := body(t, resp); resp.StatusCode != http.StatusOK {
		t.Fatalf("снятие взвода: %d %s", resp.StatusCode, text)
	}
	if b := boardText(t, e); strings.Contains(b, "[взвод]") {
		t.Fatalf("суффикс взвода остался на доске:\n%s", b)
	}
	if git := readFile(t, gitLog); !strings.Contains(git, "docs(tasks): XR-004 взвод снят с дашборда") {
		t.Errorf("снятие взвода не закоммичено: %s", git)
	}
}

// DoD: отказ `arm` виден в карточке с причиной. Рубежи держит утилита, и её
// слова уезжают на экран как есть: открытая развилка называет саму развилку,
// начатая строка называет секцию.
func TestArmRefusalTellsWhy(t *testing.T) {
	e, c, _ := tasksEnv(t)
	runTaskctl(t, e.proj, "move", "XR-004", "in-progress")

	resp := doReq(t, c, "POST", taskURL(e, "XR-004", "/arm"), `{"on": true}`)
	text := body(t, resp)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("отказ взвода ждал 400, а был %d: %s", resp.StatusCode, text)
	}
	var v map[string]any
	if err := json.Unmarshal([]byte(text), &v); err != nil {
		t.Fatalf("ответ отказа не разобрался: %v\n%s", err, text)
	}
	said := fmt.Sprint(v["error"])
	if !strings.Contains(said, "взводится только строка Backlog") {
		t.Errorf("причина отказа %q не названа словами утилиты", said)
	}
	// Тело запроса без признака это не «снять» и не «поставить»: молчаливое
	// умолчание тут стоило бы взвода, которого никто не просил.
	resp = doReq(t, c, "POST", taskURL(e, "XR-002", "/arm"), `{}`)
	if text := body(t, resp); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("запрос без признака ждал 400, а был %d: %s", resp.StatusCode, text)
	}
}

// DoD: три состояния взвода приходят экрану из `dep list --json`. Ручка задачи
// отдаёт их полем arm рядом с зависимостями: карточка выбирает по разряду вид
// метки, а словами пометки объясняет её человеку.
func TestTaskCarriesArmState(t *testing.T) {
	e, c, _ := tasksEnv(t)

	// Строка без взвода и с неснятым ребром о взводе не говорит: сказать о нём
	// нечего, и карточка показывает один переключатель.
	task := getTask(t, c, e, "XR-002")
	if got := task["armed"]; got != false {
		t.Errorf("невзведённая строка пришла со взводом: %v", got)
	}
	if got, hit := task["arm"]; hit {
		t.Errorf("у строки без взвода есть состояние: %v", got)
	}

	resp := doReq(t, c, "POST", taskURL(e, "XR-002", "/arm"), `{"on": true}`)
	if text := body(t, resp); resp.StatusCode != http.StatusOK {
		t.Fatalf("взвод: %d %s", resp.StatusCode, text)
	}
	task = getTask(t, c, e, "XR-002")
	if got := task["armed"]; got != true {
		t.Errorf("взведённая строка пришла без признака: %v", got)
	}
	arm, ok := task["arm"].(map[string]any)
	if !ok {
		t.Fatalf("состояние взвода не пришло: %v", task["arm"])
	}
	if arm["state"] != "ждёт" {
		t.Errorf("разряд состояния %v, ждал «ждёт»: ребро на XR-001 не снято", arm["state"])
	}
	if said := fmt.Sprint(arm["note"]); !strings.Contains(said, "XR-001") {
		t.Errorf("пометка взвода не назвала предпосылку: %q", said)
	}
}

// Строка доски несёт то же состояние взвода, что и карточка: чип на строке
// списка рисуется полем arm, и своего счёта у экрана нет ни там, ни тут.
func TestBoardRowCarriesArmState(t *testing.T) {
	e, c, _ := tasksEnv(t)
	resp := doReq(t, c, "POST", taskURL(e, "XR-002", "/arm"), `{"on": true}`)
	if text := body(t, resp); resp.StatusCode != http.StatusOK {
		t.Fatalf("взвод: %d %s", resp.StatusCode, text)
	}

	board := body(t, doReq(t, c, "GET", e.srv.URL+"/api/projects/demo/board", ""))
	var got struct {
		Board struct {
			Sections []struct {
				Rows []struct {
					ID  string `json:"id"`
					Arm *struct {
						State string `json:"state"`
						Note  string `json:"note"`
					} `json:"arm"`
				} `json:"rows"`
			} `json:"sections"`
		} `json:"board"`
	}
	if err := json.Unmarshal([]byte(board), &got); err != nil {
		t.Fatalf("ответ доски не разобрался: %v\n%s", err, board)
	}
	for _, sec := range got.Board.Sections {
		for _, row := range sec.Rows {
			if row.ID != "XR-002" {
				continue
			}
			if row.Arm == nil || row.Arm.State != "ждёт" {
				t.Fatalf("строка доски без состояния взвода: %+v", row.Arm)
			}
			if !strings.Contains(row.Arm.Note, "XR-001") {
				t.Fatalf("пометка строки не назвала предпосылку: %+v", row.Arm)
			}
			return
		}
	}
	t.Fatalf("XR-002 нет в ответе доски: %s", board)
}

// Экран собран из того, что ручки и правда отдают: список задач с поиском,
// переключатель автозапуска и диалог цепочки зовут ровно эти ручки, а взвод со
// своим состоянием читают полем строки. Стенд статики сторожит именно стык:
// переименованная ручка или потерянное поле роняют экран молча.
func TestStaticChainAndArmWiring(t *testing.T) {
	text := readFile(t, filepath.Join("static", "app.js"))
	for _, want := range []string{
		`taskPath(project, id, "/arm"), "POST", { on }`,
		`"/chain"`,
		`dry_run: true`,
		`dry_run: false`,
		"function taskPick(",
		"function armChip(",
		"function chainOpen(",
		"chainOpen(project, [id])",
		"chainOpen(project, [])",
		// Кнопка записи погашена до первого плана: пустая цепочка уходила бы
		// на сервер, и человек читал бы его отказ вместо плана.
		"write.disabled = true",
		// Отказ взвода виден в самой карточке, а не только строкой результата
		// наверху экрана (DoD DK-942): решение о взводе принимают у
		// переключателя, там же читают причину.
		"bad.textContent = r.body.error",
		"inp.checked = !want",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("в static/app.js нет %q: экран разошёлся с ручками сервера", want)
		}
	}
	// Поле ввода одного номера в карточке зависимостей больше не стоит: номер
	// надо было знать заранее, и человек шёл за ним на доску.
	deps := funcBody(t, text, "function depsCard(")
	if strings.Contains(deps, `inp.placeholder = "DK-NNN"`) {
		t.Error("карточка зависимостей снова принимает один ID полем ввода")
	}
	if !strings.Contains(deps, "taskPick(project, {") || !strings.Contains(deps, "armRow(project, id") {
		t.Error("карточка зависимостей без списка задач или без переключателя автозапуска")
	}
	// Стили нового диалога и списка лежат там же, где остальные: без них диалог
	// открывается голой простынёй поверх страницы.
	css := readFile(t, filepath.Join("static", "style.css"))
	for _, want := range []string{".chainlens{", ".chainbox{", ".tplist{", ".darm{", ".darmbad{"} {
		if !strings.Contains(css, want) {
			t.Errorf("в static/style.css нет %q", want)
		}
	}
}

// Ручка цепочки живёт на месте: маршрут поднимается сервером, а не остаётся
// мёртвым кодом. Заодно сторожится и адрес, на который ходит экран.
func TestChainRouteIsWired(t *testing.T) {
	e, c, _ := tasksEnv(t)
	resp := doReq(t, c, "POST", chainURL(e), `{"levels": [["XR-004"]], "dry_run": true}`)
	text := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("ручка цепочки: %d %s", resp.StatusCode, text)
	}
	if !strings.Contains(text, "уровень 1: XR-004") {
		t.Fatalf("план без первого уровня: %s", text)
	}
	// Файла задачи у строки может не быть, и тогда отказывает сама утилита: тут
	// проверяется, что путь до неё цел.
	if _, err := os.Stat(filepath.Join(e.proj, "docs", "tasks", "XR-004.md")); err != nil {
		t.Fatalf("файла задачи стенда нет: %v", err)
	}
}
