package main

import (
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Состав цели. Стенд тот же, что у правки строки: настоящий taskctl на
// фикстурной доске, поэтому и строки, и архив ведёт утилита, а не выдумка
// теста. Нарезка лежит разделом «Задачи цели» файла цели, как её пишет скилл
// goal-loop.

// goalDoc кладёт файл цели с нарезкой. Заводит его сама утилита (та же команда
// чинит ссылку в строке доски), текст пишется поверх.
func goalDoc(t *testing.T, e *testEnv, id, body string) {
	t.Helper()
	runTaskctl(t, e.proj, "file", id)
	path := filepath.Join(e.proj, "docs", "tasks", id+".md")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func goalTasksResp(t *testing.T, c *http.Client, e *testEnv, id string) map[string]any {
	t.Helper()
	resp := doReq(t, c, "GET", e.srv.URL+"/api/projects/demo/goals/"+id+"/tasks", "")
	text := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("состав цели %s: %d %s", id, resp.StatusCode, text)
	}
	var v map[string]any
	if err := json.Unmarshal([]byte(text), &v); err != nil {
		t.Fatalf("ответ состава не разобрался: %v\n%s", err, text)
	}
	return v
}

// composed вынимает состав списком «ID статус» вместе с судьбой: пустой
// статус значит, что сервер не сходил ни на доску, ни в архив.
func composed(t *testing.T, got map[string]any) []map[string]any {
	t.Helper()
	list, ok := got["tasks"].([]any)
	if !ok {
		t.Fatalf("в ответе нет состава: %v", got)
	}
	out := []map[string]any{}
	for _, item := range list {
		m, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("задача состава не разобралась: %v", item)
		}
		out = append(out, m)
	}
	return out
}

// closedDate это дата закрытия фикстурной задачи: она стоит в колонке
// «Закрыто» архива, и состав обязан приехать ровно с ней, а не с соседней
// колонкой таблицы.
const closedDate = "2026-01-31"

const goalDocBody = `# XR-100: Цель: пробный цикл

## Задачи цели

Нарезка 2026-08-01, две строки с файлами.

- XR-001 (task, L, R=55). Первая по нарезке, идёт первой.
- XR-002 (task, M, R=30). Вторая, ждёт первую; на доске DK-777 её не касается.

Дорезка 2026-08-05: XR-003, XR-004 (task, M, R=25, добор по замечаниям).

Мысль про соседний инструмент остаётся черновиком XR-900 вне цели.

## Журнал

- 2026-08-05 виток 1
`

// Состав цели читается из раздела «Задачи цели», а статус каждой задачи со
// строки доски либо из архива: закрытая задача с доски уезжает, и без архива
// половина состава долгой цели выглядела бы потерянной. Упомянутые рядом чужие
// ID (черновик, задача чужой доски) задачами цели не считаются.
func TestGoalTasksFromDoc(t *testing.T) {
	e, c, _ := tasksEnv(t)
	runTaskctl(t, e.proj, "add", "--id", "XR-100", "--title", "Цель: пробный цикл",
		"--rank", "25+9+3+0+4", "--cost", "XL", "--accept", "agent")
	goalDoc(t, e, "XR-100", goalDocBody)
	runTaskctl(t, e.proj, "move", "XR-001", "in-progress")
	// Закрытая задача уезжает в архив руками утилиты: в Check её пускают со
	// сценарием проверки, и стенд идёт тем же путём, каким идёт живая задача.
	goalDoc(t, e, "XR-004", "# XR-004: Четвёртая\n\n## Сценарий проверки\n\nАгентский: прогнать тесты.\n")
	runTaskctl(t, e.proj, "move", "XR-004", "check")
	// Файл закрываемой задачи убирается до close: переносит его в архив git mv,
	// а git на стенде играет молчаливая фикстура, и перенос до неё не доедет.
	if err := os.Remove(filepath.Join(e.proj, "docs", "tasks", "XR-004.md")); err != nil {
		t.Fatal(err)
	}
	// Дата закрытия ставится руками: со сверкой точного значения видно, что в
	// состав едет колонка «Закрыто» архива, а не соседняя с ней.
	runTaskctl(t, e.proj, "close", "XR-004", "--date", closedDate)

	got := goalTasksResp(t, c, e, "XR-100")
	if note, hit := got["note"]; hit {
		t.Fatalf("состав нашёлся, а сервер сказал про пустоту: %v", note)
	}
	tasks := composed(t, got)
	var ids []string
	for _, task := range tasks {
		ids = append(ids, task["id"].(string))
	}
	want := []string{"XR-001", "XR-002", "XR-003", "XR-004"}
	if strings.Join(ids, ",") != strings.Join(want, ",") {
		t.Fatalf("состав %v, жду %v в порядке нарезки", ids, want)
	}
	if fate, _ := tasks[0]["fate"].(string); !strings.Contains(fate, "Первая по нарезке") {
		t.Errorf("судьба первой задачи не приехала: %v", tasks[0])
	}
	if sect, _ := tasks[0]["sect"].(string); sect != "in-progress" {
		t.Errorf("статус первой задачи %q, жду in-progress со строки доски", sect)
	}
	if title, _ := tasks[1]["title"].(string); title != "Вторая" {
		t.Errorf("заголовок второй задачи %q, жду со строки доски", title)
	}
	// Дорезка перечислением через запятую: судьба у связки общая, и оба ID это
	// задачи цели, а не упоминания.
	if fate, _ := tasks[2]["fate"].(string); !strings.Contains(fate, "добор по замечаниям") {
		t.Errorf("судьба задачи из дорезки не приехала: %v", tasks[2])
	}
	closed := tasks[3]
	if done, _ := closed["done"].(bool); !done {
		t.Errorf("закрытая задача не помечена закрытой: %v", closed)
	}
	if when, _ := closed["closed"].(string); when != closedDate {
		t.Errorf("дата закрытия %q, жду %q из колонки «Закрыто» архива", when, closedDate)
	}
	if title, _ := closed["title"].(string); title != "Четвёртая" {
		t.Errorf("заголовок закрытой задачи %q, жду из архива", title)
	}
	counts, ok := got["counts"].(map[string]any)
	if !ok {
		t.Fatalf("в ответе нет счётчиков: %v", got)
	}
	for name, want := range map[string]float64{"total": 4, "closed": 1, "running": 1, "ahead": 2} {
		if counts[name] != want {
			t.Errorf("счётчик %s = %v, жду %v: %v", name, counts[name], want, counts)
		}
	}
}

// Пустота состава различима словами: файла цели нет вовсе, раздела в нём нет,
// раздел заведён и пуст. Пустой список без слов неотличим от неотрисованного
// экрана. Файл цели кладёт сам add вместе со строкой, и его отсутствие это
// дыра: файл снят руками, как у XR-101.
func TestGoalTasksEmptyDistinct(t *testing.T) {
	e, c, _ := tasksEnv(t)
	runTaskctl(t, e.proj, "add", "--id", "XR-101", "--title", "Цель: без файла", "--rank", "25+5+1+0+1", "--accept", "agent")
	if err := os.Remove(filepath.Join(e.proj, "docs", "tasks", "XR-101.md")); err != nil {
		t.Fatal(err)
	}
	runTaskctl(t, e.proj, "add", "--id", "XR-102", "--title", "Цель: без раздела", "--rank", "25+5+1+0+1", "--accept", "agent")
	goalDoc(t, e, "XR-102", "# XR-102: Цель: без раздела\n\n## Цель\n\nПока только слова.\n")
	runTaskctl(t, e.proj, "add", "--id", "XR-103", "--title", "Цель: раздел пуст", "--rank", "25+5+1+0+1", "--accept", "agent")
	goalDoc(t, e, "XR-103", "# XR-103: Цель: раздел пуст\n\n## Задачи цели\n\nНарезки ещё не было.\n")

	for _, tc := range []struct{ id, want string }{
		{"XR-101", "файла цели"},
		{"XR-102", "нет раздела «Задачи цели»"},
		{"XR-103", "задач в нём не названо"},
	} {
		got := goalTasksResp(t, c, e, tc.id)
		note, _ := got["note"].(string)
		if !strings.Contains(note, tc.want) {
			t.Errorf("%s: слова о пустоте %q, жду про %q", tc.id, note, tc.want)
		}
		if tasks := composed(t, got); len(tasks) != 0 {
			t.Errorf("%s: пустой состав приехал списком %v", tc.id, tasks)
		}
	}
}

// Состав цели на экране: сабтаски с прогрессом по макету «06 Цель», живые
// сверху, закрытые свёрнуты одной строкой.
func TestStaticGoalComposition(t *testing.T) {
	text := readFile(t, filepath.Join("static", "app.js"))
	for _, want := range []string{
		"Задачи цели",
		"async function goalComposition(",
		"function goalTaskRow(",
		"закрыта ",
		" закрыто",
		" в работе",
		" впереди",
		"Свернуть закрытые",
		"нарезка из ",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("в static/app.js нет надписи %q", want)
		}
	}
	cut := strings.Index(text, "async function goalComposition(")
	if cut < 0 {
		t.Fatal("в static/app.js нет goalComposition")
	}
	part := text[cut:]
	if stop := strings.Index(part, "\nasync function renderTask("); stop > 0 {
		part = part[:stop]
	}
	// Живые сверху, закрытые под разворотом: у долгой цели закрытых больше, чем
	// помещается на экран, а смотрят в состав ради несделанного.
	if !strings.Contains(part, "tasks.filter((t) => !t.done)") || !strings.Contains(part, `el("div", "more"`) {
		t.Error("состав не делит задачи на живые и свёрнутые закрытые")
	}
	// Пустота приезжает словами сервера, а не пустой карточкой.
	if !strings.Contains(part, "r.body.note") {
		t.Error("состав молчит про пустоту: слова сервера на экран не выведены")
	}
	// Состав рисуется только у цели: у обычной задачи его нет.
	task := text[strings.Index(text, "async function renderTask("):]
	if !strings.Contains(task, "/^Цель:/.test(row.title") || !strings.Contains(task, "goalComposition(project") {
		t.Error("renderTask не рисует состав цели по заголовку от слова «Цель:»")
	}
	css := readFile(t, filepath.Join("static", "style.css"))
	for _, want := range []string{".srow", ".prog", ".chd"} {
		if !strings.Contains(css, want) {
			t.Errorf("в static/style.css нет правила %q", want)
		}
	}
}

// Разбор нарезки на голом тексте: элемент списка, перечисление через запятую,
// перенос строки внутри элемента и чужие ID рядом. Проверяется отдельно от
// сервера, потому что правил тут больше, чем ходов на доску.
func TestGoalTasksParse(t *testing.T) {
	tasks, section := goalTasksFromDoc(goalDocBody, "XR")
	if !section {
		t.Fatal("раздел «Задачи цели» не нашёлся")
	}
	var ids []string
	for _, task := range tasks {
		ids = append(ids, task.ID)
	}
	if got := strings.Join(ids, ","); got != "XR-001,XR-002,XR-003,XR-004" {
		t.Fatalf("разбор дал %q, жду четыре задачи нарезки без чужих ID", got)
	}
	// Перенос строки внутри элемента списка не режет судьбу пополам.
	long := "## Задачи цели\n\n- XR-007 (task, M, R=30). Длинная судьба,\n  которая не влезла\n  в одну строку.\n"
	tasks, _ = goalTasksFromDoc(long, "XR")
	if len(tasks) != 1 || !strings.Contains(tasks[0].Fate, "которая не влезла в одну строку") {
		t.Fatalf("судьба с переносами разобралась как %v", tasks)
	}
	if _, section := goalTasksFromDoc("## Цель\n\nбез нарезки\n", "XR"); section {
		t.Fatal("раздела «Задачи цели» нет, а разбор его нашёл")
	}
}

// Задача цели, которая пока лежит записью накопителя. Строки на доске у неё
// нет и в архиве её нет тоже, но это не потеря состава, а этап: состав говорит
// про неё «черновик», берёт заголовок из самой записи и оставляет дорогу на её
// экран. Фраза про отсутствие строки остаётся только там, где за ID и правда
// ничего не лежит.
func TestGoalTasksDraft(t *testing.T) {
	e, c, _ := tasksEnv(t)
	runTaskctl(t, e.proj, "add", "--id", "XR-110", "--title", "Цель: с черновиком",
		"--rank", "25+9+3+0+4", "--cost", "XL", "--accept", "agent")
	goalDoc(t, e, "XR-110", `# XR-110: Цель: с черновиком

## Задачи цели

- XR-011 (task, M, R=30). Первая, пока записью накопителя.
- XR-012 (task, M, R=20). Вторая, за ней нет вовсе ничего.
`)
	dir := filepath.Join(e.proj, "docs", "tasks", "drafts")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "XR-011.md"),
		[]byte("# XR-011: экран записи открывается из состава\n\nТело записи.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tasks := composed(t, goalTasksResp(t, c, e, "XR-110"))
	if len(tasks) != 2 {
		t.Fatalf("состав %v, жду две задачи", tasks)
	}
	draft := tasks[0]
	if is, _ := draft["draft"].(bool); !is {
		t.Errorf("задача записью накопителя не помечена черновиком: %v", draft)
	}
	if note, _ := draft["note"].(string); note != "" {
		t.Errorf("у черновика осталась приписка %q, жду метку вместо неё", note)
	}
	if title, _ := draft["title"].(string); title != "экран записи открывается из состава" {
		t.Errorf("заголовок черновика %q, жду из файла записи", title)
	}
	bare := tasks[1]
	if is, _ := bare["draft"].(bool); is {
		t.Errorf("задача без записи помечена черновиком: %v", bare)
	}
	if note, _ := bare["note"].(string); !strings.Contains(note, "ни на доске") {
		t.Errorf("у задачи без записи приписка %q, жду прежние слова", note)
	}
}

// Черновик в составе цели помечен словом, а не объяснением, открывается со
// строки и стоит под указателем мыши. Сторожит стенд testdata/poc_goaldraft.mjs.
// Без node шаг пропускается: узел стенда, а не рабочей машины.
func TestStaticGoalDraftRow(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node не найден: стенд состава цели пропущен")
	}
	out, err := exec.Command(node, filepath.Join("testdata", "poc_goaldraft.mjs"),
		filepath.Join("static", "app.js")).CombinedOutput()
	if err != nil {
		t.Fatalf("черновик в составе цели: %v\n%s", err, out)
	}
	t.Log(strings.TrimSpace(string(out)))
}
