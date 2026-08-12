package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// funcBody вырезает тело функции статики по её началу: тесты разметки
// смотрят на конкретную функцию, а не на весь файл, иначе искомое находится в
// соседнем экране. Приём тот же, что у проверки порядка PATCH-PUT.
func funcBody(t *testing.T, text, head string) string {
	t.Helper()
	cut := strings.Index(text, head)
	if cut < 0 {
		t.Fatalf("в static/app.js нет %s", head)
	}
	body := text[cut:]
	if stop := strings.Index(body, "\n}\n"); stop > 0 {
		body = body[:stop]
	}
	return body
}

// Действие идёт прямо со строки доски и зовёт те же ручки запуска и стопа, что
// экран задачи: своей команды у строки нет, иначе рубежи ручек пришлось бы
// держать дважды. Переход внутрь задачи при этом не срабатывает.
func TestStaticBoardRowActions(t *testing.T) {
	text := readFile(t, filepath.Join("static", "app.js"))
	body := funcBody(t, text, "function rowAction(")
	for _, want := range []string{"stopRun(project, row.id)", "startRun(project, row.id)",
		"ev.stopPropagation()", `"Стоп"`, "actionLabel(sect)", "ведёт другая сессия"} {
		if !strings.Contains(body, want) {
			t.Errorf("в rowAction нет %q: действие со строки не доведено", want)
		}
	}
	if strings.Contains(body, "api(") {
		t.Error("rowAction ходит на сервер сам, мимо startRun и stopRun: ручка у запуска и стопа одна")
	}
	if !strings.Contains(funcBody(t, text, "function renderRow("), "rowAction(project, row, works, sect)") {
		t.Error("строка доски рисуется без действия: за запуском снова придётся заходить внутрь задачи")
	}
	if !strings.Contains(funcBody(t, text, "function renderBoard("), "renderRow(project, row, works, key)") {
		t.Error("строка рисуется без своей секции: статус до кнопки не доходит, и действие снова одно на все")
	}
}

// Действие называется по статусу строки: из Backlog задачу выполняют, начатую
// продолжают, проверенную закрывают. Те же слова сервер кладёт в промпт
// конвейеру, и подпись кнопки обязана совпадать с заказом.
func TestStaticActionLabelBySection(t *testing.T) {
	text := readFile(t, filepath.Join("static", "app.js"))
	labels := funcBody(t, text, "const ACTION_BY_SECT")
	for _, want := range []string{`"in-progress": "Продолжить"`, `check: "Закрыть"`, `|| "Выполнить"`} {
		if !strings.Contains(labels, want) {
			t.Errorf("в подписях действий нет %q", want)
		}
	}
	if strings.Contains(text, `"В работу"`) {
		t.Error("в static/app.js осталась кнопка «В работу»: одна подпись на все статусы шлёт конвейеру не тот заказ")
	}
	// Заблокированная маркером строка действия не получает: кнопка стоит
	// погашенной с причиной, а запуск с неё не уходит.
	body := funcBody(t, text, "function rowAction(")
	for _, want := range []string{"row.after && row.after.length", "wait.disabled = true", "сначала "} {
		if !strings.Contains(body, want) {
			t.Errorf("в rowAction нет %q: заблокированная задача снова уходит в конвейер", want)
		}
	}
}

// Расшифровка ранга ушла из строки: в ней остаётся сумма, слагаемые приходят
// подсказкой при наведении и разворотом по нажатию, потому что на телефоне
// наведения нет.
func TestStaticBoardRankFolded(t *testing.T) {
	text := readFile(t, filepath.Join("static", "app.js"))
	row := funcBody(t, text, "function renderRow(")
	if strings.Contains(row, "r_parts") {
		t.Error("слагаемые ранга снова рисуются в строке доски: место они едят, а нужны изредка")
	}
	cell := funcBody(t, text, "function rankCell(")
	for _, want := range []string{"sum.title", "aria-expanded", "classList.toggle", "ev.stopPropagation()"} {
		if !strings.Contains(cell, want) {
			t.Errorf("в rankCell нет %q: слагаемые не достать одним из двух форм-факторов", want)
		}
	}
}

// Возраст строки днями сменился датой последней правки: считает её taskctl
// (поле moved), клиент только показывает и своей арифметики не заводит.
func TestStaticBoardMovedDate(t *testing.T) {
	text := readFile(t, filepath.Join("static", "app.js"))
	if !strings.Contains(text, "row.moved") {
		t.Error("в static/app.js нет row.moved: дату последней правки строке брать неоткуда")
	}
	if strings.Contains(text, "не двигалась") {
		t.Error("в static/app.js осталась пометка «строка не двигалась N дней», её сменила дата")
	}
	if strings.Contains(funcBody(t, text, "function renderRow("), "new Date(") {
		t.Error("строка считает дату сама: она приходит с сервера, выдуманной даты быть не должно")
	}
}

// Уйти на главную можно с любого экрана: переход держит логотип в левом
// верхнем углу (на телефоне он же стоит слева в шапке), на телефоне то же
// место занимает нижняя вкладка, а сама главная это список проектов.
func TestStaticHomeFromEveryScreen(t *testing.T) {
	html := readFile(t, filepath.Join("static", "index.html"))
	for _, want := range []string{`id="logo-side"`, `id="logo-top"`, `id="nav-home"`, `id="tab-home"`, "На главную", "Главная"} {
		if !strings.Contains(html, want) {
			t.Errorf("в static/index.html нет %q: перехода на главную с экрана нет", want)
		}
	}
	if strings.Contains(html, `id="gohome"`) {
		t.Error("в static/index.html осталась кнопка «На главную»: её место занял логотип")
	}
	text := readFile(t, filepath.Join("static", "app.js"))
	for _, want := range []string{`"logo-side", "logo-top", "nav-home", "tab-home"`, "function renderHome(", "home: true"} {
		if !strings.Contains(text, want) {
			t.Errorf("в static/app.js нет %q: главная не собрана", want)
		}
	}
	if !strings.Contains(funcBody(t, text, "function markNav("), `["home", ["nav-home", "tab-home"]]`) {
		t.Error("раздел главной не подсвечивается: открытый экран неотличим от доски")
	}
}

// Живая работа подписана заголовком со строки доски: имя сессии goal-XR-100 о
// занятии агента не говорит ничего. Работа, чьей строки на доске нет, остаётся
// при своём ID.
func TestLiveWorksTitleFromBoard(t *testing.T) {
	e, _, _ := runsEnv(t, `goal-XR-100\ntask-XR-002\n`)
	titles := map[string]string{}
	for _, w := range boardWorks(t, e) {
		titles[w.ID] = w.Title
	}
	want := map[string]string{"XR-100": "Цель: пробный цикл", "XR-002": "Обычная задача", "XR-112": ""}
	for id, title := range want {
		if got, ok := titles[id]; !ok || got != title {
			t.Errorf("работа %s подписана %q, ожидал %q", id, got, title)
		}
	}
}

// Формулировка «ведётся снаружи» ушла из ответов и статики: она не говорила ни
// кто ведёт работу, ни почему стоп из дашборда ей не положен.
func TestNoOutsideWording(t *testing.T) {
	all, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	// Тесты сами называют ушедшую формулировку: греп идёт по коду, статике и
	// доке, а не по этому файлу.
	var files []string
	for _, path := range all {
		if !strings.HasSuffix(path, "_test.go") {
			files = append(files, path)
		}
	}
	static, err := filepath.Glob(filepath.Join("static", "*"))
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range append(files, append(static, "README.md")...) {
		if strings.Contains(readFile(t, path), "ведётся снаружи") {
			t.Errorf("%s всё ещё говорит «ведётся снаружи»", path)
		}
	}
}

// jsEval гоняет куски статики под node и печатает ответ выражения: логика
// экрана проверяется работой, а не чтением исходника. Функции вырезаются те
// же, что уедут в браузер.
func jsEval(t *testing.T, heads []string, expr string) string {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node не найден, юниты статики не гоняются")
	}
	text := readFile(t, filepath.Join("static", "app.js"))
	var parts []string
	for _, head := range heads {
		parts = append(parts, funcBody(t, text, head)+"\n}")
	}
	src := strings.Join(parts, "\n\n") + "\nconsole.log(String(" + expr + "));\n"
	path := filepath.Join(t.TempDir(), "unit.mjs")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(node, path).CombinedOutput()
	if err != nil {
		t.Fatalf("кусок статики не отработал под node: %v\n%s", err, out)
	}
	return strings.TrimSpace(string(out))
}

// Кружок проекта считается по данным, а не по настроению: работа идёт это
// зелёный, задача в Check это красный «требуется внимание», пусто это серый.
// Подписи «тихо» у серого нет: кружок и есть всё, что о проекте известно.
func TestStaticProjectDotState(t *testing.T) {
	heads := []string{"function projectState(", "function projectWhy(", "function plural("}
	cases := []struct{ input, dot, why string }{
		{`{works: [{id: "DK-1"}, {id: "DK-2"}], sections: {check: 0}}`, "pd-run pulse",
			"2 задачи в работе: DK-1, DK-2"},
		{`{works: [], sections: {check: 1}}`, "pd-warn", "ждёт проверки: 1 задача в Check"},
		{`{works: [], sections: {check: 0, backlog: 9}}`, "", ""},
	}
	for _, c := range cases {
		if got := jsEval(t, heads, "projectState("+c.input+").cls"); got != c.dot {
			t.Errorf("кружок для %s: %q, жду %q", c.input, got, c.dot)
		}
		if got := jsEval(t, heads, "projectWhy("+c.input+")"); got != c.why {
			t.Errorf("причина для %s: %q, жду %q", c.input, got, c.why)
		}
	}
	app := readFile(t, filepath.Join("static", "app.js"))
	if strings.Contains(app, `"тихо"`) {
		t.Error("в static/app.js осталась подпись «тихо»: у тихого проекта её сменил серый кружок")
	}
	css := readFile(t, filepath.Join("static", "style.css"))
	for _, want := range []string{".pdot{", ".pd-run{", ".pd-warn{"} {
		if !strings.Contains(css, want) {
			t.Errorf("в static/style.css нет стиля кружка %q", want)
		}
	}
}

// Легенда кружков живёт у заголовка главной: на ноутбуке всплывает по знаку,
// на телефоне тот же знак разворачивает её нажатием, потому что наведения
// там нет.
func TestStaticDotLegend(t *testing.T) {
	app := readFile(t, filepath.Join("static", "app.js"))
	body := funcBody(t, app, "function dotLegend(")
	for _, want := range []string{"Статусы индикатора", "tipq", "tipbox", `classList.toggle("on")`} {
		if !strings.Contains(body, want) {
			t.Errorf("в легенде кружков нет %q", want)
		}
	}
	for _, want := range []string{"нет активных задач", "идёт работа агентов",
		"требуется внимание, задача приостановлена или ожидает пользователя"} {
		if !strings.Contains(app, want) {
			t.Errorf("в static/app.js нет строки легенды %q", want)
		}
	}
	if !strings.Contains(funcBody(t, app, "function renderHome("), "dotLegend()") {
		t.Error("легенда не попала на главную")
	}
}

// Логотип это и есть переход на главную, и на самой главной он погашен:
// подсветка по наведению обещала бы переход, которого нет.
func TestStaticLogoIsHomeLink(t *testing.T) {
	app := readFile(t, filepath.Join("static", "app.js"))
	nav := funcBody(t, app, "function markNav(")
	for _, want := range []string{`"logo-side", "logo-top"`, `classList.toggle("here"`, "Вы и так на главной"} {
		if !strings.Contains(nav, want) {
			t.Errorf("в markNav нет %q: логотип на главной не гасится", want)
		}
	}
	css := readFile(t, filepath.Join("static", "style.css"))
	for _, want := range []string{".logo{", ".logo:hover", ".logo.here"} {
		if !strings.Contains(css, want) {
			t.Errorf("в static/style.css нет стиля логотипа %q", want)
		}
	}
}

// Экран «Агенты» собран из ответа со списком проектов: своей ручки у него нет,
// works приходят одним запросом, и каждая работа встаёт строкой с заголовком
// задачи впереди.
func TestStaticAgentsScreen(t *testing.T) {
	html := readFile(t, filepath.Join("static", "index.html"))
	for _, want := range []string{`id="nav-agents"`, `id="tab-agents"`, `id="nav-agents-n"`} {
		if !strings.Contains(html, want) {
			t.Errorf("в static/index.html нет %q: пункт «Агенты» не ожил", want)
		}
	}
	if strings.Contains(html, `class="sitem off">Агенты`) {
		t.Error("пункт «Агенты» остался погашенной заглушкой")
	}
	app := readFile(t, filepath.Join("static", "app.js"))
	if !strings.Contains(app, `if (h === "/agents") return { proj: "", id: "", agents: true };`) {
		t.Error("хэша экрана «Агенты» нет: раздел никуда не ведёт")
	}
	if !strings.Contains(funcBody(t, app, "function markNav("), `["agents", ["nav-agents", "tab-agents"]]`) {
		t.Error("раздел «Агенты» не подсвечивается: открытый экран неотличим от доски")
	}
	if !strings.Contains(app, `for (const id of ["nav-agents", "tab-agents"]) {`) {
		t.Error("пункт колонки и вкладка телефона не ведут на экран")
	}
	refresh := funcBody(t, app, "async function refresh(")
	if !strings.Contains(refresh, "renderAgents(projects)") {
		t.Error("экран «Агенты» не рисуется из ответа /api/projects")
	}
	if strings.Contains(refresh, `"/agents") + "/board"`) {
		t.Error("экран «Агенты» ходит за доской: works уже пришли со списком проектов")
	}
	row := funcBody(t, app, "function agentRow(")
	if strings.Index(row, `el("span", "tt", w.title`) > strings.Index(row, "workChips(") {
		t.Error("строка начинается не с заголовка задачи")
	}
	for _, want := range []string{"агент цели", "конвейер задачи", "ведёт другая сессия",
		"интерактивная сессия"} {
		if !strings.Contains(funcBody(t, app, "function workChips("), want) {
			t.Errorf("вид работы %q не назван чипом", want)
		}
	}
	css := readFile(t, filepath.Join("static", "style.css"))
	for _, want := range []string{".arow{", ".aacts{", ".atime{", ".arow{flex-wrap:wrap"} {
		if !strings.Contains(css, want) {
			t.Errorf("в static/style.css нет %q: экран не собран на обоих форм-факторах", want)
		}
	}
}

// Переходы строки те же, что на экране агента: чат открыт одной цели, стоп
// стоит только у работы, чьей tmux-сессией дашборд распоряжается, а поднятой
// мимо него остаётся переход на задачу.
func TestStaticAgentsRowGates(t *testing.T) {
	app := readFile(t, filepath.Join("static", "app.js"))
	row := funcBody(t, app, "function agentRow(")
	if !strings.Contains(row, `goButton("Живой статус", project + "/agent/" + w.id)`) {
		t.Error("перехода в живой статус нет")
	}
	if !strings.Contains(row, `if (w.kind === "goal") acts.append(goButton("Чат с агентом"`) {
		t.Error("чат открыт не одной целью: у обычной задачи он ведёт в тупик")
	}
	if !strings.Contains(row, `if (w.via === "tmux") {`) || !strings.Contains(row, `"Остановить"`) {
		t.Error("кнопка стопа стоит не у tmux-работы")
	}
	if strings.Index(row, `w.via === "registry"`) > strings.Index(row, `w.via === "tmux"`) {
		t.Error("ветка реестровой работы стоит после стопа: кнопка достанется и ей")
	}
	if !strings.Contains(row, "работа поднята мимо дашборда: остановить можно там, где поднята") {
		t.Error("реестровая работа не объясняет, почему стопа у неё нет")
	}
	if !strings.Contains(row, `goButton("Открыть задачу"`) {
		t.Error("у реестровой работы нет перехода на задачу")
	}
}

// Пустота экрана говорит словами и зовёт запустить задачу: раздел открывается
// и тогда, когда ни одной работы не идёт.
func TestStaticAgentsEmpty(t *testing.T) {
	app := readFile(t, filepath.Join("static", "app.js"))
	body := funcBody(t, app, "function renderAgents(")
	for _, want := range []string{"Агентов сейчас нет.",
		"Запустите задачу с доски: кнопка «В работу» есть в строке задачи и на её экране."} {
		if !strings.Contains(body, want) {
			t.Errorf("в пустоте экрана «Агенты» нет %q", want)
		}
	}
	if !strings.Contains(readFile(t, filepath.Join("static", "style.css")), ".empty b{") {
		t.Error("в стилях нет заголовка пустоты: слова встанут одним куском")
	}
}

// Экран считает работы всех проектов сразу, тем же списком, что и счётчик у
// пункта колонки; время работы берётся с её начала, а работа без начала
// остаётся без времени, а не с нулём минут.
func TestStaticAgentsCollect(t *testing.T) {
	heads := []string{"function allWorks(", "function workSub(", "function workAge("}
	projects := `[{name: "devkit", works: [{id: "DK-112", kind: "goal", via: "tmux"}]},` +
		`{name: "xr", works: []},` +
		`{name: "byblos", works: [{id: "BB-7", kind: "task", via: "registry"}, {kind: "session", via: "session", session: "abc", note: "задача не узнана"}]}]`
	if got := jsEval(t, heads, "allWorks("+projects+").length"); got != "3" {
		t.Errorf("собрано %s работ, ожидал 3 со всех проектов", got)
	}
	if got := jsEval(t, heads, `allWorks(`+projects+`).map((x) => x.project).join(",")`); got != "devkit,byblos,byblos" {
		t.Errorf("проекты работ %q: список собран не по всем доскам", got)
	}
	cases := []struct{ expr, want string }{
		{`workSub({id: "DK-112", kind: "goal", via: "tmux"})`, "DK-112, сессия goal-DK-112"},
		{`workSub({id: "BB-7", kind: "task", via: "registry"})`, "BB-7, сессии дашборда нет"},
		{`workSub({kind: "session", via: "session", session: "abc", note: "задача не узнана"})`,
			"задача не узнана, сессия abc"},
		{"workAge(0, 1000000)", ""},
		{"workAge(1000, 1000 * 1000 + 52 * 60 * 1000)", "52 мин"},
		{"workAge(1000, 1000 * 1000 + 75 * 60 * 1000)", "1 ч 15 мин"},
	}
	for _, c := range cases {
		if got := jsEval(t, heads, c.expr); got != c.want {
			t.Errorf("%s дал %q, ожидал %q", c.expr, got, c.want)
		}
	}
}

// Ответ со списком проектов несёт живые работы каждого из них: экран
// «Агенты» собирается из одного запроса, и работа второй доски в него
// попадает наравне с первой.
func TestProjectsWorksEveryProject(t *testing.T) {
	e := newTestEnv(t)
	other := filepath.Join(e.home, "projects", "other")
	mkProject(t, other)
	// Фикстура taskctl отвечает по спрошенному каталогу: у второго проекта своя
	// доска с префиксом ZZ, иначе обе получили бы одни и те же сессии.
	writeScript(t, e.bin, "taskctl", fmt.Sprintf(
		"case \"$*\" in\n*other*) echo '%s';;\n*) echo '%s';;\nesac",
		strings.ReplaceAll(boardFixtureJSON, `"XR`, `"ZZ`), boardFixtureJSON))
	writeScript(t, e.bin, "tmux", "printf 'goal-XR-9\\t1\\t1000\\ntask-ZZ-5\\t1\\t2000\\n'")

	resp := doReq(t, e.loggedClient(t), "GET", e.srv.URL+"/api/projects", "")
	var got struct {
		Projects []projectInfo `json:"projects"`
	}
	if err := json.Unmarshal([]byte(body(t, resp)), &got); err != nil {
		t.Fatal(err)
	}
	works := map[string][]string{}
	for _, p := range got.Projects {
		for _, w := range p.Works {
			works[p.Name] = append(works[p.Name], w.ID)
		}
	}
	if len(works["demo"]) != 2 || works["demo"][0] != "XR-9" {
		t.Errorf("работы demo %v, ожидал tmux-работу XR-9 и цель реестра", works["demo"])
	}
	if len(works["other"]) != 1 || works["other"][0] != "ZZ-5" {
		t.Errorf("работы other %v, ожидал ZZ-5: работы второй доски в ответ не попали", works["other"])
	}
}

// Работа из tmux несёт момент начала: по нему экран «Агенты» говорит, сколько
// она идёт. У цели из реестра начала не видно, и время в ответе нулевое, а не
// выдуманное.
func TestLiveWorksStartedFromTmux(t *testing.T) {
	e, _, _ := runsEnv(t, `goal-XR-100\t1\t1786000000\n`)
	starts := map[string]int64{}
	for _, w := range boardWorks(t, e) {
		starts[w.ID] = w.Started
	}
	if starts["XR-100"] != 1786000000 {
		t.Errorf("начало работы XR-100 %d, ожидал момент создания tmux-сессии", starts["XR-100"])
	}
	if starts["XR-112"] != 0 {
		t.Errorf("у цели из реестра начало %d, а его неоткуда взять", starts["XR-112"])
	}
}
