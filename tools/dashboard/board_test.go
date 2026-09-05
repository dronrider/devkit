package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/dronrider/devkit/internal/chat"
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
	// Кусок кончается закрывающей скобкой в первой колонке: у функции она стоит
	// одна, у словаря с точкой с запятой, и берётся та, что встретилась раньше.
	stop := strings.Index(body, "\n}\n")
	if semi := strings.Index(body, "\n};\n"); semi > 0 && (stop < 0 || semi < stop) {
		stop = semi
	}
	if stop > 0 {
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
	for _, want := range []string{"stopRun(project, row.id)", "startRun(project, row.id",
		"continueTask(project, row.id)", "rowChatBtn(project, row)",
		"ev.stopPropagation()", `"Стоп"`, "actionLabel(sect)"} {
		if !strings.Contains(body, want) {
			t.Errorf("в rowAction нет %q: действие со строки не доведено", want)
		}
	}
	// Кнопок в колонке три: работа, разговор и три точки с выбором подписки и
	// уровня модели. Точки стоят у всякой строки, а там, где выбирать нечего,
	// стоят погашенными с причиной в подсказке: пропадай они у одной строки и
	// стой у соседней, ряд кнопок дёргался бы по колонке (замечание
	// пользователя).
	for _, want := range []string{`el("span", "racts")`,
		`el("button", "btn btn-sm btn-ico rdots")`, `dots.addEventListener("click"`,
		"dots.disabled = true", `main.addEventListener("contextmenu"`,
		`el("div", "pmenu rmenu")`, "popupHold(menu, shutMenu)", "popupsShut(null)"} {
		if !strings.Contains(body, want) {
			t.Errorf("в rowAction нет %q: действия строки снова стоят рядом все разом", want)
		}
	}
	// Долгого нажатия на кнопке запуска тут больше нет: единственным входом к
	// выбору подписки оно быть не может («ты даже не видишь, что такой
	// функционал есть, плюс в мобильном виде это вообще не работает», замечание
	// пользователя), а рядом с видимой кнопкой оно только съедало нажатия.
	if strings.Contains(body, "ROW_PICK_HOLD") {
		t.Error("выбор подписки снова открывается удержанием кнопки запуска: " +
			"о таком входе не догадаться, и пальцем он отнимает обычное нажатие")
	}
	if strings.Contains(body, "api(") {
		t.Error("rowAction ходит на сервер сам, мимо startRun и stopRun: ручка у запуска и стопа одна")
	}
	if !strings.Contains(funcBody(t, text, "function renderRow("), "rowAction(project, row, sect)") {
		t.Error("строка доски рисуется без действия: за запуском снова придётся заходить внутрь задачи")
	}
	if !strings.Contains(funcBody(t, text, "function renderBoard("), "renderRow(project, row, key)") {
		t.Error("строка рисуется без своей секции: статус до кнопки не доходит, и действие снова одно на все")
	}
}

// Что со строкой происходит сейчас, она знает сама: признак идущей работы
// приезжает её полем (row.run), и ни действие, ни отпечаток строки не сводят
// её со списком работ. Сведение по ID отвечало на один вопрос, «есть ли живая
// сессия с таким же номером», и оборванный конвейер выглядел в нём очередью
// (DK-317).
func TestStaticRowRunFromRowData(t *testing.T) {
	text := readFile(t, filepath.Join("static", "app.js"))
	act := funcBody(t, text, "function rowAction(")
	// Само правило вынесено в rowOurRun: его же спрашивает полоса действий
	// задачи, и списком условий оно теперь не повторяется.
	for _, want := range []string{"row.run", `row.run === "tmux"`} {
		if !strings.Contains(funcBody(t, text, "function rowOurRun("), want) {
			t.Errorf("в rowOurRun нет %q: признак работы снова собирается на клиенте", want)
		}
	}
	if !strings.Contains(funcBody(t, text, "function rowOnRun("), "row.run_busy") {
		t.Error("идущий ход строки считается не по признаку сервера: run_busy обязан быть в rowOnRun")
	}
	if !strings.Contains(act, "rowOurRun(row)") {
		t.Error("rowAction выбирает кнопку своим условием, а не общим правилом строки и формы")
	}
	if strings.Contains(act, "works") {
		t.Error("rowAction снова ищет работу в списке works: строка обязана знать про себя сама")
	}
	if sign := funcBody(t, text, "function rowSign("); strings.Contains(sign, "works") {
		t.Error("отпечаток строки собран со списком работ: признак работы входит в него полем строки")
	}
	// Своего чипа у признака работы нет вовсе: приписка «сессии нет» стояла в
	// каждой строке In progress без живой сессии, места занимала, а звать было
	// не к чему (замечание пользователя). Оборванный конвейер от очереди
	// отличает кружок у номера, он же и берёт признак.
	if strings.Contains(text, "function runChip(") {
		t.Error("чип признака работы вернулся в строку доски")
	}
	// Идущая работа сказана кружком у номера, а не чипом со словом: одно и то
	// же состояние стояло в строке дважды (POC ветки poc-chat).
	dot := funcBody(t, text, "function rowDot(")
	for _, want := range []string{`row.run !== "gone"`, "sd-run", "sd-wait", "sd-out", "row.waiting"} {
		if !strings.Contains(dot, want) {
			t.Errorf("в кружке состояния нет %q: состояния строки снова неразличимы", want)
		}
	}
	if !strings.Contains(funcBody(t, text, "function renderRow("), "rowDot(project, row)") {
		t.Error("строка доски рисуется без кружка состояния: на экране его снова нет")
	}
}

// Повторное нажатие того же действия невозможно: до ответа сервера кнопка
// погашена. Пока строка выглядела прежней, второе нажатие уходило вторым
// запуском и возвращалось отказом «работа уже идёт» (журнал дашборда,
// 2026-08-13 21:21).
func TestStaticRowActionGuardsSecondPress(t *testing.T) {
	act := funcBody(t, readFile(t, filepath.Join("static", "app.js")), "function rowAction(")
	// Кнопка работы делает три разных дела (стоп, продолжение, запуск), и
	// погашена обязана быть каждое: считается не наличие строки, а число, иначе
	// новая ветка приезжала бы без защиты молча.
	for _, want := range []string{"main.disabled = true", "main.disabled = false"} {
		if got := strings.Count(act, want); got < 3 {
			t.Errorf("в rowAction %q стоит %d раз при трёх делах кнопки работы: "+
				"второе нажатие снова уйдёт вторым запуском", want, got)
		}
	}
}

// Смена статуса доезжает до открытого списка задач событием уведомителя, а не
// фокусом окна: статус двигает агент у себя, и до DK-317 узнать об этом можно
// было, только уйдя из окна и вернувшись.
func TestStaticBoardEchoOnNotification(t *testing.T) {
	text := readFile(t, filepath.Join("static", "app.js"))
	echo := funcBody(t, text, "function boardEcho(")
	for _, want := range []string{"rt.id", "n.project !== rt.proj", "refresh()"} {
		if !strings.Contains(echo, want) {
			t.Errorf("в boardEcho нет %q: перечитывание доски уходит не туда", want)
		}
	}
	if !strings.Contains(funcBody(t, text, "function wireFlash("), "boardEcho(n)") {
		t.Error("поток уведомлений не перечитывает доску: статус снова доедет только по фокусу окна")
	}
	if strings.Contains(text, "setInterval(() => { refresh()") {
		t.Error("доска ушла в постоянный опрос: он ест батарею телефона, ход идёт на событие")
	}
}

// Действие называется по статусу строки: из Backlog задачу выполняют, начатую
// продолжают, проверенную проверяют и закрывают. Те же слова сервер кладёт в
// промпт конвейеру, и подпись кнопки обязана совпадать с заказом. У Check
// подпись стоит одним словом: нажатие человека это его приёмка, а прогон
// проверки и закрытие идут дальше сами, и о них говорит подсказка кнопки. Двумя
// делами разом подпись звалась до замера ширины: на телефоне «Проверить и
// закрыть» занимала строку целиком (замечание пользователя).
func TestStaticActionLabelBySection(t *testing.T) {
	text := readFile(t, filepath.Join("static", "app.js"))
	labels := funcBody(t, text, "const ACTION_BY_SECT")
	for _, want := range []string{`"in-progress": "Продолжить"`, `check: "Проверить"`, `|| "Выполнить"`} {
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
	for _, want := range []string{"row.after && row.after.length", "main.disabled = true", "сначала "} {
		if !strings.Contains(body, want) {
			t.Errorf("в rowAction нет %q: заблокированная задача снова уходит в конвейер", want)
		}
	}
}

// Расшифровка ранга ушла из строки: в ней остаётся сумма, слагаемые приходят
// всплывающим блоком при наведении и тем же блоком по нажатию, потому что на
// телефоне наведения нет. Подсказка при этом ровно одна: своя. Родную подсказку
// браузера человек забраковал вместе со строкой слагаемых в самой ячейке, и
// вернуться она может тихо, одной строкой `sum.title`.
func TestStaticBoardRankFolded(t *testing.T) {
	text := readFile(t, filepath.Join("static", "app.js"))
	row := funcBody(t, text, "function renderRow(")
	if strings.Contains(row, "r_parts") {
		t.Error("слагаемые ранга снова рисуются в строке доски: место они едят, а нужны изредка")
	}
	cell := funcBody(t, text, "function rankCell(")
	for _, want := range []string{"rtip", "RANK_PARTS", "aria-expanded", "classList.toggle",
		"ev.stopPropagation()"} {
		if !strings.Contains(cell, want) {
			t.Errorf("в rankCell нет %q: слагаемые не достать одним из двух форм-факторов", want)
		}
	}
	for _, gone := range []string{"sum.title", `el("span", "rfold"`} {
		if strings.Contains(cell, gone) {
			t.Errorf("в rankCell вернулась вторая подсказка ранга: %q", gone)
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

// Сессии живут табом доски, а не своим разделом: раздел «Агенты» упразднён, и
// его пункта нет ни в колонке, ни в нижних вкладках. Работы приходят тем же
// ответом, что и список проектов, и каждая встаёт строкой с заголовком задачи
// впереди.
func TestStaticAgentsScreen(t *testing.T) {
	html := readFile(t, filepath.Join("static", "index.html"))
	for _, gone := range []string{`id="nav-agents"`, `id="tab-agents"`, `id="nav-agents-n"`} {
		if strings.Contains(html, gone) {
			t.Errorf("в static/index.html остался %q: раздел «Агенты» упразднён", gone)
		}
	}
	app := readFile(t, filepath.Join("static", "app.js"))
	if !strings.Contains(app, `sess: true, q: parts.slice(2).join("/")`) {
		t.Error("хэша таба сессий нет: таб никуда не ведёт")
	}
	if !strings.Contains(app, `agents: true, q: parts.slice(2).join("/")`) {
		t.Error("старый адрес раздела перестал разбираться: ссылки на него сломаются")
	}
	refresh := funcBody(t, app, "async function paint(")
	if !strings.Contains(refresh, `renderSessions(current.name, current.works, rt.q || "")`) {
		t.Error("таб сессий не рисуется из ответа /api/projects")
	}
	if !strings.Contains(refresh, `goSame(current.name + "/sess" + q)`) {
		t.Error("старый адрес раздела не переезжает на таб сессий")
	}
	row := funcBody(t, app, "function agentRow(")
	if strings.Index(row, `el("span", "tt", w.title`) > strings.Index(row, "workChips(") {
		t.Error("строка начинается не с заголовка задачи")
	}
	// Состав чипов строки разобран с человеком и урезан до того, чего не
	// говорит ничто другое в той же строке: проект повторял таб доски, «агент
	// цели» повторял название работы, «интерактивная сессия» не значила ничего
	// (headless-заходов в списке и так не видно), «разговор о задаче» ушёл
	// словом к номеру задачи, а состояние несут кружок и давность.
	chips := funcBody(t, app, "function workChips(")
	for _, gone := range []string{"агент цели", "конвейер задачи", "интерактивная сессия",
		"разговор о задаче", "мимо дашборда", `el("span", "chip", project)`} {
		if strings.Contains(chips, gone) {
			t.Errorf("чип %q вернулся в строку сессии: состав разобран с человеком", gone)
		}
	}
	for _, want := range []string{`"внешняя"`, "w.harness", "w.model"} {
		if !strings.Contains(chips, want) {
			t.Errorf("в строке сессии нет %s: остаться должны подписка, модель и признак внешней", want)
		}
	}
	// Состояние работы чипом вида больше не называется: о нём говорит чип
	// состояния рядом, словом из общего словаря.
	if strings.Contains(chips, "сессия кончилась") {
		t.Error("состояние работы вернулось в чип вида: о нём говорит чип состояния")
	}
	// Род работы стоит словом у номера задачи, а не чипом.
	if !strings.Contains(funcBody(t, app, "function workKindWord("), `"разговор"`) {
		t.Error("род работы не назван словом у номера задачи")
	}
	// Идущая работа слова в строке не носит: о ней говорят кружок и время.
	if !strings.Contains(funcBody(t, app, "function agentRow("), "WORK_LIVE.busy ? null") {
		t.Error("чип «активна» вернулся в строку сессии: состояние несут кружок и давность")
	}
	// Одно и то же не объясняется двумя способами: чип, подсказка строки и
	// погашенная кнопка закрытия берут слова у одного места.
	if strings.Count(app, "WORK_FOREIGN_TIP") < 3 {
		t.Error("объяснение отсутствия кнопки разъехалось по разным местам")
	}
	if strings.Contains(app, `"снимать нечем"`) {
		t.Error("прежняя непонятная приписка «снимать нечем» вернулась в строку")
	}
	css := readFile(t, filepath.Join("static", "style.css"))
	// Строка сессии стоит колонками таблицы на ноутбуке и раскладывается по
	// областям на телефоне: собран экран обоими форм-факторами.
	for _, want := range []string{".arow .aacts>.cin{", ".arow .live{", ".amoved{",
		".arow{display:grid"} {
		if !strings.Contains(css, want) {
			t.Errorf("в static/style.css нет %q: экран не собран на обоих форм-факторах", want)
		}
	}
}

// Дороги со строки агентов две, и обе стоят у каждой строки: номер задачи
// ведёт на её форму, разговор открывается панелью. Стоп стоит только у работы,
// чьей tmux-сессией дашборд распоряжается.
func TestStaticAgentsRowGates(t *testing.T) {
	app := readFile(t, filepath.Join("static", "app.js"))
	row := funcBody(t, app, "function agentRow(")
	// Разговор открывается панелью поверх экрана: нажатие зовёт openChat, а не
	// уводит по адресу задачи.
	if !strings.Contains(row, `openChat(chatAddr(project, addr))`) {
		t.Error("перехода в разговор агента нет")
	}
	if !strings.Contains(row, "workTaskLink(project, w.id)") {
		t.Error("номер задачи в строке агентов не ссылка на её форму")
	}
	if strings.Contains(row, `goButton(`) {
		t.Error("переход на задачу вернулся кнопкой: номер задачи и есть ссылка")
	}
	// Адрес разговора это сессия, когда дашборд её видит, и задача, когда нет:
	// иначе у работы из реестра чата не было бы вовсе.
	addr := funcBody(t, app, "function workChatAddr(")
	if !strings.Contains(addr, "w.session || w.id") {
		t.Error("адрес разговора строки собран не из сессии и задачи")
	}
	// Стоп стоит у идущего конвейера, а снятие окна у всякой своей сессии:
	// разводит их признак разговора (talk), и оба вопроса решаются своими
	// функциями, а не переписанным условием в строке.
	if !strings.Contains(row, "workRunning(w)") || !strings.Contains(row, `"Стоп"`) {
		t.Error("кнопка стопа стоит не у идущего конвейера")
	}
	if !strings.Contains(row, "closeSessionBtn(project, w)") {
		t.Error("кнопки снятия нет у строки со своей tmux-сессией")
	}
	// Гасит себя кнопка сама, по признаку работы: слов вместо неё в строке
	// больше нет, и решение «есть ли что закрывать» живёт одним местом.
	if !strings.Contains(funcBody(t, app, "function closeSessionBtn("), "workDrops(w)") {
		t.Error("кнопка закрытия не гасится по признаку «есть чем закрыть»")
	}
	running := funcBody(t, app, "function workRunning(")
	if !strings.Contains(running, `w.via === "tmux"`) || !strings.Contains(running, "!w.talk") {
		t.Error("идущий конвейер узнаётся не по tmux-работе без разговора")
	}
	// Почему у строки нет закрытия, объясняет одно место на все точки показа
	// (чип, подсказка строки, погашенная кнопка): разные слова про одно и то
	// же человек читал как разные причины.
	if !strings.Contains(row, "WORK_FOREIGN_TIP") {
		t.Error("чужая работа не объясняет, почему закрытия у неё нет")
	}
}

// Пустота экрана говорит словами и зовёт запустить задачу: раздел открывается
// и тогда, когда ни одной работы не идёт.
func TestStaticAgentsEmpty(t *testing.T) {
	app := readFile(t, filepath.Join("static", "app.js"))
	// Пустота живёт там же, где строки: список перерисовывается по месту, и
	// слова про пустой таб рисует та же сборка строк.
	body := funcBody(t, app, "function paintSessionRows(")
	for _, want := range []string{"Сессий проекта сейчас нет.",
		"Запустите задачу с доски: кнопка «В работу» есть в строке задачи и на её экране."} {
		if !strings.Contains(body, want) {
			t.Errorf("в пустоте таба сессий нет %q", want)
		}
	}
	if !strings.Contains(readFile(t, filepath.Join("static", "style.css")), ".empty b{") {
		t.Error("в стилях нет заголовка пустоты: слова встанут одним куском")
	}
}

// Подпись строки собирается из того, что о работе известно; время работы
// берётся с её начала, а работа без начала остаётся без времени, а не с нулём
// минут. Сквозного сбора работ всех досок тут больше нет: таб показывает
// сессии своего проекта, они приезжают его работами.
func TestStaticAgentsCollect(t *testing.T) {
	heads := []string{"const SECT_WORD = {", "function workSub(", "function workAge("}
	if strings.Contains(readFile(t, filepath.Join("static", "app.js")), "function allWorks(") {
		t.Error("сквозной сбор работ вернулся: таб показывает сессии своего проекта")
	}
	cases := []struct{ expr, want string }{
		// Статус со строки доски идёт в подписи русским словом, а работа без
		// строки остаётся без него: взять его неоткуда. Номера задачи в тексте
		// подписи нет: он стоит перед ней ссылкой на форму задачи.
		{`workSub({id: "DK-247", kind: "task", via: "tmux", sect: "check"})`,
			"на проверке, сессия task-DK-247"},
		{`workSub({id: "DK-247", kind: "task", via: "tmux", sect: "in-progress"})`,
			"в работе, сессия task-DK-247"},
		{`workSub({id: "DK-247", kind: "task", via: "tmux", sect: "backlog"})`,
			"в очереди, сессия task-DK-247"},
		{`workSub({id: "DK-247", kind: "task", via: "tmux", sect: "blocked"})`,
			"заблокирована, сессия task-DK-247"},
		{`workSub({id: "DK-112", kind: "goal", via: "tmux"})`, "сессия goal-DK-112"},
		{`workSub({id: "BB-7", kind: "task", via: "registry"})`, "сессии дашборда нет"},
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

// Производная сессия конвейера (task-XR-004_1_1786532648) с prefix-проверкой
// не отсеивается: у неё тот же префикс доски, а хвост это номер и
// unix-момент запуска, а не ID. liveWorks гоняет ту же проверку, что ручки
// журнала и черновика (goalIDRe), и такая сессия работой не становится
// (DK-279).
func TestLiveWorksSkipsDerivedSession(t *testing.T) {
	e, _, _ := runsEnv(t, `task-XR-004\t1\t1000\ntask-XR-004_1_1786532648\t1\t2000\n`)
	var ids []string
	for _, w := range boardWorks(t, e) {
		ids = append(ids, w.ID)
	}
	want := []string{"XR-004", "XR-112"}
	if !reflect.DeepEqual(ids, want) {
		t.Errorf("работы %v, ожидал %v: производная сессия конвейера стала лишней карточкой", ids, want)
	}
}

// boardRunRow это строка ответа доски, как её читает тест: признак идущей
// работы, который дописывает сервер, и поля от taskctl, по которым видно, что
// разметка провезла строку целиком.
type boardRunRow struct {
	ID          string `json:"id"`
	Run         string `json:"run"`
	RunBusy     bool   `json:"run_busy"`
	RunState    string `json:"run_state"`
	RunChat     string `json:"run_chat"`
	RunStopping bool   `json:"run_stopping"`
	Order       string `json:"order"`
	RParts      []int  `json:"r_parts"`
	Link        string `json:"link"`
}

func boardRows(t *testing.T, e *testEnv) map[string]boardRunRow {
	t.Helper()
	resp := doReq(t, e.loggedClient(t), "GET", e.srv.URL+"/api/projects/demo/board", "")
	var got struct {
		Board struct {
			Sections []struct {
				Rows []boardRunRow `json:"rows"`
			} `json:"sections"`
		} `json:"board"`
	}
	if err := json.Unmarshal([]byte(body(t, resp)), &got); err != nil {
		t.Fatal(err)
	}
	rows := map[string]boardRunRow{}
	for _, sec := range got.Board.Sections {
		for _, r := range sec.Rows {
			rows[r.ID] = r
		}
	}
	return rows
}

// Строка доски несёт признак идущей работы своим полем: у работы с
// tmux-сессией дашборда это tmux (по нему строка и рисует «Стоп»), у строки в
// работе без живой сессии gone, у стоящей задачи признака нет вовсе. До
// DK-317 признака в строке не было, и клиент сводил её со списком работ по ID:
// такое сведение отвечало только про живую сессию с тем же номером, а
// оборванный конвейер выглядел в нём штатной очередью.
func TestBoardRowsCarryRun(t *testing.T) {
	e, _, _ := runsEnv(t, "task-XR-004\t1\t1786000000\n")
	rows := boardRows(t, e)
	// Наших сессий у XR-100 нет ни одной, и признака у строки нет тоже:
	// прежнее other («исполнителя не видно») снято, отсутствие исполнителя
	// человек видит по отсутствию живой работы (решение пользователя).
	want := map[string]string{"XR-004": "tmux", "XR-100": "", "XR-003": "", "XR-002": ""}
	for id, run := range want {
		if got, hit := rows[id]; !hit || got.Run != run {
			t.Errorf("признак работы строки %s %q, ожидал %q", id, got.Run, run)
		}
	}
	// Разметка идёт по строке ответа taskctl, а не по разбору в свой тип:
	// поля, которых сервер не знает, обязаны доехать до клиента целыми.
	if got := rows["XR-004"]; len(got.RParts) != 5 || got.Link != "-" {
		t.Errorf("строка XR-004 после разметки: r_parts %v, link %q, ожидал строку целиком", got.RParts, got.Link)
	}
}

// Строка доски несёт заказ дословно, той же строкой, что уйдёт headless-сессии
// (rowOrder читает её у runPrompt): подсказка кнопки на экране показывает
// готовое поле, а не пересказывает его ветвление вторым разбором на клиенте.
// У строки цели нет заказа: следующий виток сочиняет goal-run.py, а не
// дашборд (DK-286).
func TestBoardRowsCarryOrder(t *testing.T) {
	e, _, _ := runsEnv(t, "")
	rows := boardRows(t, e)
	want := map[string]string{
		"XR-100": "", // цель: следующий виток сочиняет заказ сам
		"XR-004": "Продолжай выполнение XR-004",
		"XR-003": "Закрой XR-003",
		"XR-002": "Выполни XR-002",
	}
	for id, order := range want {
		if got, hit := rows[id]; !hit || got.Order != order {
			t.Errorf("заказ строки %s %q, ожидал %q", id, got.Order, order)
		}
	}
}

// Проверенная строка с пользовательской приёмкой закрывается прямо с экрана
// командой taskctl, без сессии агента (closeFromCheck, DK-289): у неё нет
// заказа, а строке рядом со смешанной приёмкой заказ остаётся.
func TestBoardRowOrderOmittedForUserAcceptClose(t *testing.T) {
	e := newTestEnv(t)
	writeScript(t, e.bin, "taskctl", fmt.Sprintf("echo '%s'", acceptBoardJSON))
	rows := boardRows(t, e)
	if got := rows["XR-006"].Order; got != "" {
		t.Errorf("проверенная строка с пользовательской приёмкой несёт заказ агенту %q, а закрывается без сессии", got)
	}
	if got := rows["XR-005"].Order; got != "Закрой XR-005" {
		t.Errorf("смешанная приёмка в Check осталась без заказа: %q", got)
	}
	if got := rows["XR-007"].Order; got != "Выполни XR-007" {
		t.Errorf("пользовательская приёмка в Backlog осталась без заказа: %q, там ещё нет проверки", got)
	}
}

// Цикл цели, поднятый другой сессией, виден строке тем же признаком: работа
// идёт, но tmux-сессии дашборда за ней нет, и стоп ей со строки не достаётся.
func TestBoardRowRunFromRegistry(t *testing.T) {
	e := newTestEnv(t)
	writeScript(t, e.bin, "taskctl", "echo '"+strings.ReplaceAll(runsBoardJSON, "XR-100", "XR-112")+"'")
	writeTmuxFake(t, e.bin, filepath.Join(e.home, "tmux.log"), "")
	if got := boardRows(t, e)["XR-112"].Run; got != "registry" {
		t.Errorf("признак работы цели из реестра %q, ожидал registry", got)
	}
}

// Наша работа в окне разговора помечается chat, каким бы путём её ни узнали.
// Живой случай: у цели DK-446 ход шёл нашим чатом, работа приезжала записью
// реестра (via=registry), и по этому пути строка выглядела остановленной: вместо
// «Стопа» экран предлагал «Продолжить», а нажатие увело бы вводную продолжения
// в живую сессию посреди её хода. Признак тут свой, а не tmux конвейера: «Стоп»
// у разговора прерывает ход и оставляет разговор жить, а у конвейера снимает
// сессию целиком (DK-716).
func TestRunMarksOwnWorkInChatWindow(t *testing.T) {
	list := []Work{
		{ID: "XR-100", Kind: "goal", Via: "registry", Own: true, Tmux: "chat-XR-100-1",
			Live: workBusy},
		{ID: "XR-002", Via: "registry", Live: workBusy},
		{ID: "XR-003", Via: "registry", Own: true, Tmux: "chat-XR-003-1", Live: workIdle},
		{ID: "XR-004", Via: "tmux", Own: true, Tmux: "task-XR-004", Live: workBusy, Talk: true},
	}
	marks := runMarks(list)
	want := map[string]string{"XR-100": runChat, "XR-002": "registry", "XR-003": runChat}
	for id, mark := range want {
		if got := marks[id]; got != mark {
			t.Errorf("признак работы %s %q, ожидал %q", id, got, mark)
		}
	}
	// Разговор о задаче признака работы строке не даёт: чат её не ведёт.
	if got, hit := marks["XR-004"]; hit {
		t.Errorf("разговор о задаче дал строке признак работы %q", got)
	}
	// Ход идёт там, где работа занята, и признак этот свой: запись реестра
	// стоящей работы остаётся на месте и после конца хода.
	busy := busyMarks(list)
	for _, id := range []string{"XR-100", "XR-002"} {
		if !busy[id] {
			t.Errorf("по строке %s идёт ход, а признака нет", id)
		}
	}
	for _, id := range []string{"XR-003", "XR-004"} {
		if busy[id] {
			t.Errorf("строке %s приписан идущий ход", id)
		}
	}
}

// Работа несёт статус со своей строки доски: подпись на экране «Агенты»
// называет его словом, а работа, чьей строки на доске нет, остаётся без
// статуса, а не с выдуманным.
func TestLiveWorksSectFromBoard(t *testing.T) {
	e, _, _ := runsEnv(t, `goal-XR-100\t1\t100\ntask-XR-003\t1\t100\n`)
	sects := map[string]string{}
	for _, w := range boardWorks(t, e) {
		sects[w.ID] = w.Sect
	}
	want := map[string]string{"XR-100": "in-progress", "XR-003": "check", "XR-112": ""}
	for id, sect := range want {
		if got, ok := sects[id]; !ok || got != sect {
			t.Errorf("статус работы %s %q, ожидал %q", id, got, sect)
		}
	}
}

// Переписка есть только у строки, чей заголовок начинается с «Цель:»:
// isGoalRow ищет строку по всем секциям доски и не путает обычную задачу с
// целью, у чьей строки просто похожий регистр или пробел (DK-296).
func TestIsGoalRow(t *testing.T) {
	heads := []string{"function boardRow(", "function isGoalRow("}
	board := `{sections: [
		{key: "in-progress", rows: [{id: "DK-208", title: "Обычная задача"}]},
		{key: "backlog", rows: [{id: "XR-100", title: "Цель: пробный цикл"}]}
	]}`
	cases := []struct{ expr, want string }{
		{`isGoalRow(${board}, "XR-100")`, "true"},
		{`isGoalRow(${board}, "DK-208")`, "false"},
		{`isGoalRow(${board}, "DK-999")`, "false"},
	}
	for _, c := range cases {
		expr := strings.ReplaceAll(c.expr, "${board}", board)
		if got := jsEval(t, heads, expr); got != c.want {
			t.Errorf("%s = %s, ожидал %s", c.expr, got, c.want)
		}
	}
}

// Обновление экрана не пересобирает списки целиком: доска, накопитель
// черновиков, лента чата и панели экрана агента перерисовываются по месту, и
// человек остаётся там, где стоял, а лента остаётся на выбранном разговоре.
// Тем же стендом проверяется экран выдачи поиска: набор буквы за буквой уходит
// одним запросом и поля не пересобирает (DK-325).
// Предмет проверки это не написанное в исходнике, а прокрутка,
// фокус, раскрытая запись и живой поток событий после перерисовки, поэтому
// статика поднимается в node с игрушечным DOM (стенд testdata/screen_keep.mjs).
// Игрушечный DOM повторяет от браузера то, от чего страдал человек: опустевшая
// коробка сбрасывает прокрутку, снятый с дерева узел теряет фокус. Без node
// шаг пропускается: узел стенда, а не рабочей части.
func TestScreenKeepsPlaceOnRefresh(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node не найден: стенд частичной перерисовки пропущен")
	}
	out, err := exec.Command(node, filepath.Join("testdata", "screen_keep.mjs"),
		filepath.Join("static", "app.js")).CombinedOutput()
	if err != nil {
		t.Fatalf("частичная перерисовка экранов: %v\n%s", err, out)
	}
	t.Log(strings.TrimSpace(string(out)))
}

// Доска на телефоне: заголовок строки идёт словами во всю ширину, чипы стоят
// под ним, разделы переключаются полосой в одну строку, а заведение задачи
// сидит плавающим плюсом над нижними вкладками. Предмет проверки это ширины и
// края, а не написанное в стилях: заголовок схлопывался не одним правилом, а
// сложением флекса с чипами, которые ширину не отдают, и разбор правил такую
// поломку уже пропускал (DK-284). Поэтому стенд открывает страницу дашборда в
// headless-chrome и снимает координаты. Без chrome шаг пропускается: это узел
// стенда, а не рабочей части.
func TestStaticBoardNarrowRow(t *testing.T) {
	// Замер стоит на разметке, которую POC ветки poc-chat заменил: доска собирается без полосы вкладок и кнопки заведения
	// Стенд повторяет прежнюю вёрстку руками, и чинить его до конца POC дороже,
	// чем он стоит. Пропуск назван вслух, чтобы его не приняли за зелень.
	t.Skip("замер ждёт вёрстки, снятой POC: доска перекроена, вкладок и кнопки заведения в ней нет")
	chrome := findChrome()
	if chrome == "" {
		t.Skip("chrome не найден: замер раскладки доски пропущен")
	}
	// Разметка стенда повторяет renderRow и renderBoard руками, и разъехаться
	// с ними она может молча: замер на своей вёрстке зеленел бы и после того,
	// как доску перестали собирать этими блоками.
	app := readFile(t, filepath.Join("static", "app.js"))
	row := funcBody(t, app, "function renderRow(")
	for _, want := range []string{`el("span", "rchips")`, `el("span", "ttl", row.title)`} {
		if !strings.Contains(row, want) {
			t.Fatalf("строка доски собрана не тем блоком (нет %q): замер на стендовой "+
				"разметке перестал говорить о рабочей строке", want)
		}
	}
	board := funcBody(t, app, "function renderBoard(")
	for _, want := range []string{"boardTabsBar(project)", "newTaskFab(project)",
		`sectionClass("shead", key, boardTab)`, `sectionClass("card", key, boardTab)`,
		`"nbar bbar"`} {
		if !strings.Contains(board, want) {
			t.Fatalf("доска собрана не тем блоком (нет %q)", want)
		}
	}
	dir, page := chromeStand(t, "board_narrow.js")

	narrow := chromeMeasure(t, chrome, dir, page, "390,844", "phone")
	if narrow["screen"] != 390 {
		t.Fatalf("окно стенда не 390 пикселей: %v", narrow)
	}
	// Отступы .bmain по 16 и .trow по 14 с каждой стороны: заголовку остаётся
	// 330 из 390, и меньше 280 это прежняя поломка, где от него оставался
	// столбик обрубков.
	if narrow["ttl"] < 280 {
		t.Errorf("на экране 390 заголовок строки занял %d пикселей: чипы снова забрали "+
			"ширину, и от названия остаётся столбик", narrow["ttl"])
	}
	// Три строки при высоте строки в 18 пикселей это 55: заголовок, вставший в
	// столбик по слову, набирает вчетверо больше.
	if narrow["ttl-h"] > 60 {
		t.Errorf("заголовок строки занял %d пикселей высоты: он читается словами в одну-две "+
			"строки, а не столбиком", narrow["ttl-h"])
	}
	if narrow["chips-under"] != 1 {
		t.Error("чипы строки стоят в одной строке с заголовком: заголовку не остаётся ширины")
	}
	if narrow["tabs-row"] != 1 || narrow["tabs"] > 60 {
		t.Errorf("полоса разделов встала не в одну строку: высота %d, один ряд %d",
			narrow["tabs"], narrow["tabs-row"])
	}
	if narrow["tab-clip"] != 0 {
		t.Error("подписи разделов режутся: в одну строку полоса влезла обрубками")
	}
	if narrow["other-tab"] != 0 {
		t.Errorf("раздел из другого таба занимает %d пикселей: полоса разделов не "+
			"переключает список", narrow["other-tab"])
	}
	if narrow["bar"] != 0 {
		t.Errorf("полоса кнопок на телефоне занимает %d пикселей: её место заняли табы "+
			"и плавающий плюс", narrow["bar"])
	}
	if narrow["fab"] < 44 {
		t.Errorf("плавающий плюс шириной %d: заведение задачи с телефона идёт с него, "+
			"и палец просит 44 пикселя", narrow["fab"])
	}
	if narrow["fab-hits-tabs"] != 0 {
		t.Error("плавающий плюс залез на нижние вкладки: он стоит над ними, а не поверх")
	}
	// Поиск на телефоне: поля в шапке нет, вход в него это лупа рядом с
	// колокольчиком, и шапка от неё за экран не вылезает (DK-325).
	if narrow["hfind"] != 0 {
		t.Errorf("на телефоне поле поиска заняло в шапке %d пикселей: там его место "+
			"занимает лупа, а поле открывается на экране выдачи", narrow["hfind"])
	}
	if narrow["hfbtn"] < 44 {
		t.Errorf("лупа поиска шириной %d: вход в поиск с телефона идёт с неё, и палец "+
			"просит 44 пикселя", narrow["hfbtn"])
	}
	if narrow["head-over"] != 0 {
		t.Error("шапка доски вылезла за экран телефона: поле поиска с лупой съели ширину")
	}
	if narrow["head-h"] > 100 {
		t.Errorf("шапка доски заняла %d пикселей высоты: поиск не должен занимать "+
			"второй ряд над доской", narrow["head-h"])
	}

	wide := chromeMeasure(t, chrome, dir, page, "1280,900", "laptop")
	if wide["chips-under"] != 0 {
		t.Error("на ноутбуке чипы уехали под заголовок: там строка остаётся одной строкой")
	}
	if wide["tabs"] != 0 || wide["fab"] != 0 {
		t.Errorf("на ноутбуке появились телефонные табы (%d) и плавающий плюс (%d): "+
			"там доска идёт списком, а кнопки стоят полосой", wide["tabs"], wide["fab"])
	}
	if wide["bar"] == 0 {
		t.Error("на ноутбуке пропала полоса кнопок доски")
	}
	if wide["other-tab"] == 0 {
		t.Error("на ноутбуке видна только часть разделов: табы телефона отрезали остальные")
	}
	if wide["hfind"] < 200 || wide["hfbtn"] != 0 {
		t.Errorf("на ноутбуке поле поиска заняло %d пикселей, лупа %d: там поле видно "+
			"всегда, а лупа остаётся телефону", wide["hfind"], wide["hfbtn"])
	}
	if wide["head-over"] != 0 {
		t.Error("шапка доски вылезла за экран ноутбука")
	}
}

// Полоса разделов раскладывает секции доски по табам, а заведение на главной
// принадлежит карточке проекта: проектов на дашборде несколько, и полоса
// кнопок внизу называла один из них, а до соседнего с главной было не добраться.
func TestStaticBoardTabsAndHomePlus(t *testing.T) {
	heads := []string{"function boardKinds(", "function boardKindHash(", "function boardKindNow("}
	cases := []struct{ expr, want string }{
		{`boardKinds().map((k) => k[1]).join(",")`, "Задачи,Сессии,Черновики"},
		{`boardKindHash("demo", "tasks")`, "demo"},
		{`boardKindHash("demo", "sess")`, "demo/sess"},
		{`boardKindHash("demo", "drafts")`, "demo/drafts"},
		{`boardKindNow("sess")`, "sess"},
		{`boardKindNow("tasks")`, "tasks"},
	}
	for _, c := range cases {
		if got := jsEval(t, heads, c.expr); got != c.want {
			t.Errorf("%s = %q, ожидал %q", c.expr, got, c.want)
		}
	}
	app := readFile(t, filepath.Join("static", "app.js"))
	// Половин у доски больше нет: сессии уехали в свой таб, и все разделы стоят
	// подряд на любой ширине.
	for _, gone := range []string{"function sectionTab(", "function sectionClass(",
		"function markBoardTab(", "let boardTab"} {
		if strings.Contains(app, gone) {
			t.Errorf("механика половин доски осталась в статике: %q", gone)
		}
	}
	home := funcBody(t, app, "function renderHome(")
	if !strings.Contains(home, "makePlus(p.name)") {
		t.Error("у карточки проекта на главной нет плюса заведения")
	}
	for _, gone := range []string{"newTaskButton(", "homeBarLabels("} {
		if strings.Contains(home, gone) {
			t.Errorf("на главной осталась проектная кнопка: %q", gone)
		}
	}
	// Меню заведения одно на все кнопки: у плюса карточки, у кнопки накопителя
	// и у плавающего плюса телефона. Пункт ведёт сразу в свою форму, вид стоит
	// в адресе.
	plus := funcBody(t, app, "function makeMenuAt(")
	for _, want := range []string{`"Задача"`, `"Черновик"`, `"/new/"`} {
		if !strings.Contains(plus, want) {
			t.Errorf("в меню плюса нет %q", want)
		}
	}
	// Своей полосы-переключателя под табами больше нет: третьим табом на
	// телефоне стоят «Сессии», а два ряда переключателей подряд отвечали на
	// один вопрос (замечание пользователя).
	if strings.Contains(app, "function boardTabsBar(") {
		t.Error("полоса-переключатель вернулась под табы доски")
	}
	bar := funcBody(t, app, "function boardKindBar(")
	for _, want := range []string{"boardKinds()", "boardKindHash(project, key)"} {
		if !strings.Contains(bar, want) {
			t.Errorf("полоса табов собрана не полностью: нет %q", want)
		}
	}
	kinds := funcBody(t, app, "function boardKinds(")
	if !strings.Contains(kinds, `"Сессии"`) {
		t.Error("таба сессий нет среди табов доски")
	}
	if strings.Contains(kinds, "narrowScreen()") {
		t.Error("набор табов зависит от ширины экрана: их три на любой")
	}
}

// Составная кнопка запуска меряется настоящим движком (DK-336). Приёмка
// пользователя нашла её распавшейся на две кнопки, а такое не берётся разбором
// правил: зазор рисует не одна строка стилей, а сложение флекса с радиусами и
// границей. Стенд открывает страницу дашборда с той же разметкой, что собирает
// runControl, и снимает края. Без chrome шаг пропускается: это узел стенда, а
// не рабочей части.
func TestStaticRunSplitLayout(t *testing.T) {
	chrome := findChrome()
	if chrome == "" {
		t.Skip("chrome не найден: замер составной кнопки пропущен")
	}
	// Разметка стенда повторяет runControl руками и разъехаться с ним может
	// молча: замер на своей вёрстке зеленел бы и после того, как кнопку
	// перестали собирать этими классами.
	app := readFile(t, filepath.Join("static", "app.js"))
	body := funcBody(t, app, "function runControl(")
	for _, want := range []string{`el("span", "split")`, `el("div", "hpop")`,
		"runPickBody(pop, {", `wide.className + " more2"`, `el("span", "car")`} {
		if !strings.Contains(body, want) {
			t.Fatalf("кнопка собрана не тем блоком (нет %q): замер на стендовой разметке "+
				"перестал говорить о рабочей кнопке", want)
		}
	}
	// Начинка списка (шапка, строки подписок, полоса ярусов и подвал) собрана
	// одной функцией на два места: всплывашку составной кнопки и меню строки
	// доски.
	pick := funcBody(t, app, "function runPickBody(")
	for _, want := range []string{`el("span", "hph", "На какой подписке запустить")`,
		`el("span", "hph", "Уровень модели")`, "harnessRow(h, opts.pin", `el("div", "tbar")`} {
		if !strings.Contains(pick, want) {
			t.Fatalf("тело выбора запуска собрано не тем блоком (нет %q)", want)
		}
	}
	// Подвал под списком снят целиком: он объяснял, откуда ярусы и надолго ли
	// выбор, и пользователь забраковал его прямой оценкой. Подпись полосы
	// зовётся «Уровень модели»: слово «ярус» живёт в правилах доски, а тут на
	// него отвечают именами моделей.
	for _, gone := range []string{"hfoot", "agentctl harness", "Каким ярусом"} {
		if strings.Contains(pick, gone) {
			t.Errorf("в тело выбора запуска вернулось %q", gone)
		}
	}
	// Строка списка подписок это одна полоса: имя и два процента остатка.
	// Прежняя везла ещё чип, две полоски-градусника с датами сброса и возраст
	// снимка, и меню раздувалось вчетверо (замечание пользователя).
	row := funcBody(t, app, "function harnessRow(")
	for _, want := range []string{`el("b", "hname", h.name)`, `el("span", "hq")`,
		`el("span", "hq hnote stale"`, "withTip(row,"} {
		if !strings.Contains(row, want) {
			t.Fatalf("строка списка подписок собрана не тем блоком (нет %q)", want)
		}
	}
	for _, gone := range []string{"quotaRow(b)", `el("span", "h1")`, "по умолчанию\")"} {
		if strings.Contains(row, gone) {
			t.Errorf("в строку списка подписок вернулось %q", gone)
		}
	}
	dir, page := chromeStand(t, "split_run.js")

	laptop := chromeMeasure(t, chrome, dir, page, "1280,900", "down")
	// Стык без зазора: половины читаются одной кнопкой, а не двумя кубиками
	// рядом, и делит их полоска внутри узкой части. Наезд в один пиксель это
	// тот же стык: рамка есть у каждой половины, и сложенные встык они рисуют
	// двойную линию везде, где рамка кнопки видна.
	if laptop["seam"] != 0 && laptop["seam"] != -1 {
		t.Errorf("между половинами кнопки зазор в %d пикселей: приёмка нашла её "+
			"распавшейся на две кнопки", laptop["seam"])
	}
	// Узкая часть равна соседней кнопке панели, а не своему числу: раздутая
	// стрелка тянула составную кнопку вширь, и пользователь просил свести её с
	// соседями точно.
	if laptop["arrow-w"] != laptop["pen-w"] {
		t.Errorf("узкая часть шириной %d, а карандаш рядом %d",
			laptop["arrow-w"], laptop["pen-w"])
	}
	if laptop["arrow-h"] != laptop["wide-h"] {
		t.Errorf("половины кнопки разной высоты: %d и %d", laptop["wide-h"], laptop["arrow-h"])
	}
	if laptop["car"] < 5 || laptop["car"] > 12 {
		t.Errorf("галочка в узкой части шириной %d: макет рисует её рамкой на 7 пикселей",
			laptop["car"])
	}
	if laptop["pop-w"] != 340 {
		t.Errorf("список подписок шириной %d, макет держит 340", laptop["pop-w"])
	}
	if laptop["pop-right"] != 0 {
		t.Errorf("список подписок съехал от правого края кнопки на %d пикселей", laptop["pop-right"])
	}
	if laptop["hrow-h"] < 44 {
		t.Errorf("строка списка высотой %d: по ней жмут пальцем, и макет держит 44",
			laptop["hrow-h"])
	}

	narrow := chromeMeasure(t, chrome, dir, page, "390,844", "down")
	if narrow["screen"] != 390 {
		t.Fatalf("окно стенда не 390 пикселей: %v", narrow)
	}
	// На телефоне мерка та же: панель это ряд одинаковых коробок, и палец в ней
	// живёт ростом всей строки, а не отдельной шириной стрелки. Отдельные 44
	// точки стояли тут до замера соседей и делали составную кнопку шире прочих.
	if narrow["arrow-w"] != narrow["pen-w"] {
		t.Errorf("на телефоне узкая часть шириной %d, а карандаш рядом %d",
			narrow["arrow-w"], narrow["pen-w"])
	}
	if narrow["pop-over"] != 0 {
		t.Errorf("список подписок вылез за край телефона: ширина %d при экране %d",
			narrow["pop-w"], narrow["screen"])
	}
	// Наезд в пиксель тут значит то же, что и на ноутбуке: рамка есть у каждой
	// половины, и сложенные встык они рисуют одну линию, а не зазор.
	if narrow["seam"] != 0 && narrow["seam"] != -1 {
		t.Errorf("на телефоне между половинами кнопки зазор в %d пикселей", narrow["seam"])
	}

	// Раскрытие вверх: под кнопкой у телефона нижние вкладки, и список,
	// раскрытый вниз, уезжает под них целиком.
	upward := chromeMeasure(t, chrome, dir, page, "390,844", "up")
	if upward["pop-above"] != 1 {
		t.Error("список подписок не раскрылся вверх от кнопки: класс up не поднимает его")
	}
	if upward["pop-under-tabs"] != 0 {
		t.Error("раскрытый вверх список всё равно достаёт до нижних вкладок")
	}
}

// Кнопки действий меряются настоящим движком (DK-337). Приёмка нашла, что к
// макетам приведена одна составная кнопка, а прочие остались прежних размеров:
// мелкая кнопка строки была ниже макетной на два пикселя, с другими полями и
// радиусом, а погашенная кнопка ничем не отличалась от живой. Разбор правил
// такое пропускает: высота складывается из правила кнопки с правилами её
// места. Без chrome шаг пропускается: это узел стенда, а не рабочей части.
func TestStaticButtonsLook(t *testing.T) {
	// Замер стоит на разметке, которую POC ветки poc-chat заменил: карточки экрана черновика сняты вместе с draftRunCard
	// Стенд повторяет прежнюю вёрстку руками, и чинить его до конца POC дороже,
	// чем он стоит. Пропуск назван вслух, чтобы его не приняли за зелень.
	t.Skip("замер ждёт вёрстки, снятой POC: кнопки черновика сняты вместе с карточками хода")
	chrome := findChrome()
	if chrome == "" {
		t.Skip("chrome не найден: замер кнопок пропущен")
	}
	// Классы кнопок сверяются по самой статике: замер идёт по стендовой
	// разметке, и разъехаться с рабочей она может молча.
	app := readFile(t, filepath.Join("static", "app.js"))
	row := funcBody(t, app, "function rowAction(")
	if !strings.Contains(row, `el("button", "btn btn-sm btn-danger", "Стоп")`) {
		t.Error("стоп в строке доски собран не красной кнопкой: макет 11 рисует его btn-danger")
	}
	if !strings.Contains(row, `el("button", "btn btn-sm", actionLabel(sect))`) {
		t.Error("заблокированная строка собрана не мелкой кнопкой")
	}
	if !strings.Contains(funcBody(t, app, "function draftRunCard("),
		`el("button", "btn btn-sm btn-danger", "Остановить груминг")`) {
		t.Error("стоп груминга собран не в шапке карточки хода: макет 12 ставит его туда")
	}
	drop := funcBody(t, app, "function draftDropCard(")
	for _, want := range []string{`el("button", "btn btn-danger", "Удалить черновик")`,
		`el("button", "btn", "Отмена")`, `el("div", "drow")`} {
		if !strings.Contains(drop, want) {
			t.Errorf("подтверждение удаления собрано не по макету 12 (нет %q)", want)
		}
	}
	dir, page := chromeStand(t, "buttons.js")

	// Строка доски: мелкая кнопка макета это 30 пикселей высоты, поля по 12 и
	// радиус 8. Погашенная кнопка гасится везде, а не только в полосе действий.
	rowVals := chromeMeasure(t, chrome, dir, page, "1280,900", "row")
	for _, id := range []string{"m-run", "m-stop", "m-wait"} {
		if got := rowVals[id+"-h"]; got != 30 {
			t.Errorf("кнопка %s высотой %d, макет держит 30", id, got)
		}
		if got := rowVals[id+"-r"]; got != 8 {
			t.Errorf("кнопка %s с радиусом %d, макет держит 8", id, got)
		}
		if got := rowVals[id+"-pad"]; got != 12 {
			t.Errorf("кнопка %s с полем %d, макет держит 12", id, got)
		}
		if got := rowVals[id+"-fs"]; got != 12 {
			t.Errorf("кнопка %s кеглем %d, макет держит 12", id, got)
		}
	}
	if rowVals["m-wait-dim"] != 45 {
		t.Errorf("погашенная кнопка строки видна на %d процентов: заблокированная задача "+
			"выглядит нажимаемой", rowVals["m-wait-dim"])
	}
	if rowVals["m-run-dim"] != 100 {
		t.Error("живая кнопка строки нарисована погашенной")
	}
	// Одиночная кнопка запуска скруглена со всех сторон: без узкой части радиус
	// справа такой же, как слева (DK-349).
	for _, id := range []string{"m-run", "m-stop", "m-wait"} {
		if got := rowVals[id+"-rr"]; got != 8 {
			t.Errorf("кнопка %s справа с радиусом %d, левый держит 8 (одиночная должна быть круглой)", id, got)
		}
	}

	// Полоса действий задачи: полноразмерная кнопка это 36 пикселей, поля по 16
	// и радиус 9, а узкая часть при ней шире, чем при мелкой кнопке строки.
	bar := chromeMeasure(t, chrome, dir, page, "1280,900", "bar")
	for _, id := range []string{"m-live", "m-bar"} {
		if bar[id+"-h"] != 36 || bar[id+"-r"] != 9 || bar[id+"-pad"] != 16 || bar[id+"-fs"] != 13 {
			t.Errorf("кнопка полосы действий %s не по макету: высота %d, радиус %d, поле %d, кегль %d",
				id, bar[id+"-h"], bar[id+"-r"], bar[id+"-pad"], bar[id+"-fs"])
		}
	}
	if bar["m-more-h"] != 36 {
		t.Errorf("узкая часть в полосе действий высотой %d, а широкая 36", bar["m-more-h"])
	}
	// Ширина узкой части одна на оба размера: макет 11 задаёт её строкой на
	// весь файл, включая свою полноразмерную составную кнопку в полосе
	// действий. Тридцать два макета 12 остаются экрану черновика, где
	// составной кнопки у дашборда пока нет (DK-337, сверка).
	if bar["m-more-w"] != 30 {
		t.Errorf("узкая часть в полосе действий шириной %d, макет 11 держит 30", bar["m-more-w"])
	}

	// Одиночная кнопка полосы действий (без узкой части) скруглена справа (DK-349).
	single := chromeMeasure(t, chrome, dir, page, "1280,900", "single")
	if got := single["m-bar-rr"]; got != 9 {
		t.Errorf("одиночная кнопка полосы действий справа с радиусом %d, левый держит 9", got)
	}

	// Телефон: строка доски держит палец в 36 пикселей, это правило места, а не
	// кнопки, и макетных 30 там быть не должно.
	narrow := chromeMeasure(t, chrome, dir, page, "390,844", "row")
	if narrow["m-run-h"] < 36 {
		t.Errorf("на телефоне кнопка строки высотой %d: пальцу мало", narrow["m-run-h"])
	}

	// Удаление черновика: причина во всю ширину над кнопками, кнопки полного
	// размера и прижаты вправо (макет 12).
	drops := chromeMeasure(t, chrome, dir, page, "1280,900", "drop")
	if drops["why-full"] != 1 || drops["why-above"] != 1 {
		t.Error("поле причины удаления не стоит своей строкой над кнопками")
	}
	if drops["row-right"] != 0 {
		t.Errorf("кнопки подтверждения не прижаты вправо: до края %d пикселей", drops["row-right"])
	}
	for _, id := range []string{"m-no", "m-drop"} {
		if drops[id+"-h"] != 36 {
			t.Errorf("кнопка подтверждения %s высотой %d, макет держит полный размер 36",
				id, drops[id+"-h"])
		}
	}

	// Шапка карточки хода груминга: стоп мелкой красной кнопкой у правого
	// края, рядом с именем сессии, которую он снимает (макет 12).
	run := chromeMeasure(t, chrome, dir, page, "1280,900", "run")
	if run["m-gstop-h"] != 30 || run["m-gstop-r"] != 8 || run["m-gstop-pad"] != 12 {
		t.Errorf("стоп груминга не мелкой кнопкой: высота %d, радиус %d, поле %d",
			run["m-gstop-h"], run["m-gstop-r"], run["m-gstop-pad"])
	}
	if run["gstop-gap"] < 40 {
		t.Errorf("стоп груминга стоит впритык за именем сессии (%d пикселей): в шапке "+
			"карточки он прижат к правому краю", run["gstop-gap"])
	}

	// Повторная ходка груминга: кнопка под полем и по его правому краю.
	ask := chromeMeasure(t, chrome, dir, page, "1280,900", "ask")
	if ask["again-under"] != 1 || ask["again-right"] != 0 {
		t.Errorf("«Повторить груминг» стоит не под полем справа: под полем %d, до края %d",
			ask["again-under"], ask["again-right"])
	}
}

// Рост строки командной панели меряется настоящим движком: правила высоты
// приходят с трёх сторон (var(--ctl) у карандаша, .btn у кнопок действий, свои
// рамки у половин составной кнопки), и на глаз строка дважды выходила
// ступенькой. Заодно меряется стык половин: рамка есть у каждой, и встык они
// рисуют двойную линию.
func TestStaticTactsRowHeights(t *testing.T) {
	chrome := findChrome()
	if chrome == "" {
		t.Skip("chrome не найден: замер командной панели пропущен")
	}
	// Разметка стенда повторяет taskHead руками и разъехаться с ним может
	// молча: замер на своей вёрстке зеленел бы и после переезда кнопок.
	app := readFile(t, filepath.Join("static", "app.js"))
	for _, want := range []string{`el("div", "tacts")`, `el("div", "tmodes")`,
		`el("button", "tpen")`, `wide.className + " more2"`} {
		if !strings.Contains(app, want) {
			t.Fatalf("панель собрана не теми классами (нет %q): замер перестал говорить "+
				"о рабочем экране", want)
		}
	}
	dir, page := chromeStand(t, "tacts_row.js")

	for _, mode := range []string{"down", "up"} {
		got := chromeMeasure(t, chrome, dir, page, "1280,900", mode)
		what := "с закрытым списком подписок"
		if mode == "up" {
			what = "с раскрытым списком подписок"
		}
		row := got["pen-h"]
		if row == 0 {
			t.Fatalf("замер %s не получился: %v", what, got)
		}
		for _, name := range []string{"read-h", "plain-h", "wide-h", "arrow-h"} {
			if got[name] != row {
				t.Errorf("%s: %s = %d, а карандаш %d: строка панели идёт ступенькой",
					what, name, got[name], row)
			}
		}
		if got["seam"] != 0 && got["seam"] != -1 {
			t.Errorf("%s: стык половин составной кнопки в %d пикселей", what, got["seam"])
		}
		// Скругление только по внешним краям группы, и радиус тот же, что у
		// карандаша: слепая правка радиуса в панели скругляла стык.
		if got["wide-rr"] != 0 || got["arrow-rl"] != 0 {
			t.Errorf("%s: углы стыка составной кнопки скруглены (%d и %d)",
				what, got["wide-rr"], got["arrow-rl"])
		}
		if got["wide-rl"] != got["pen-r"] || got["arrow-rr"] != got["pen-r"] {
			t.Errorf("%s: внешние углы составной кнопки радиусом %d и %d, у карандаша %d",
				what, got["wide-rl"], got["arrow-rr"], got["pen-r"])
		}
		// Узкая половина ровно той же ширины, что и карандаш рядом: панель это
		// ряд одинаковых коробок, и своего числа у стрелки выбора нет
		// (замечание пользователя про кнопку с раскрывающимся списком).
		if got["arrow-w"] != got["pen-w"] {
			t.Errorf("%s: узкая половина шириной %d, а карандаш рядом %d",
				what, got["arrow-w"], got["pen-w"])
		}
	}
}

// Разговорный чат задачу не присваивает: строка остаётся без признака работы,
// пока у неё нет исполнительской сессии. Прежде
// сервер считал своей всякую сессию с привязкой к задаче, и первый же чат по
// DK-460 возвращал на строку кнопки конвейера (жалоба пользователя). Критерий
// один (leadsTask), и проверяется он тем же путём, каким его видит экран:
// ответом ручки доски.
func TestBoardTalkChatDoesNotTakeRow(t *testing.T) {
	e, _, _ := runsEnv(t, "")
	now := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	e.s.now = func() time.Time { return now }
	sid := "cccc3333-3333-4333-8333-333333333333"
	writeSession(t, e.home, e.proj, "", sid, transcriptFixture, now.Add(-time.Minute))
	bind := func(tmux string) {
		writeBinds(t, e.home, fmt.Sprintf("2026-08-22T11:59:00 сессия %s задача XR-100 проект demo "+
			"дерево %s транскрипт /tmp/t.jsonl источник заказ повод startup tmux %s\n", sid, e.proj, tmux))
	}

	bind("chat-XR-100-1")
	if got := boardRows(t, e)["XR-100"]; got.Run != "" {
		t.Errorf("строку присвоил разговорный чат: run=%q, ожидал пустой признак", got.Run)
	}
	// Имя окна тут ни при чём: тот же разговор под именем конвейера строки
	// тоже не присваивает, пока по ней не сделано ни одного хода работы
	// (DK-716, критерий перестал зависеть от имени tmux).
	bind("task-XR-100")
	if got := boardRows(t, e)["XR-100"]; got.Run != "" {
		t.Errorf("строку присвоило имя окна: run=%q, ожидал пустой признак", got.Run)
	}
	// Сам разговор при этом остаётся живой работой раздела «Агенты» и знает
	// свою задачу: у строки там две дороги, в задачу и в чат.
	talk := workByID(projectWorks(t, e), "XR-100")
	if talk == nil || !talk.Talk {
		t.Fatalf("разговор пропал из работ или не помечен разговором: %+v", projectWorks(t, e))
	}

	// Рабочая сессия ту же строку присваивает: запись о работе по задаче
	// кладёт команда доски, и она же говорит, кто строку ведёт.
	writeBinds(t, e.home, fmt.Sprintf("2026-08-22T11:59:00 сессия %s задача XR-100 проект demo "+
		"дерево %s транскрипт /tmp/t.jsonl источник заказ повод startup tmux chat-XR-100-1\n"+
		"2026-08-22T11:59:30 сессия %s задача XR-100 проект demo дерево %s транскрипт - "+
		"источник работа повод «taskctl move XR-100» tmux -\n", sid, e.proj, sid, e.proj))
	if got := boardRows(t, e)["XR-100"]; got.Run == "" {
		t.Errorf("рабочая сессия строку не присвоила: run=%q", got.Run)
	}
	if lead := workByID(projectWorks(t, e), "XR-100"); lead == nil || lead.Talk {
		t.Errorf("рабочая сессия потерялась или помечена разговором: %+v", lead)
	}
}

// workByID достаёт работу задачи из списка: кроме неё в окружении стенда
// живут и другие, чужие этой проверке.
func workByID(list []Work, id string) *Work {
	for i := range list {
		if list[i].ID == id {
			return &list[i]
		}
	}
	return nil
}

// projectWorks читает живые работы проекта тем же ответом, каким их читает
// экран «Агенты».
func projectWorks(t *testing.T, e *testEnv) []Work {
	t.Helper()
	resp := doReq(t, e.loggedClient(t), "GET", e.srv.URL+"/api/projects", "")
	var got struct {
		Projects []struct {
			Name  string `json:"name"`
			Works []Work `json:"works"`
		} `json:"projects"`
	}
	if err := json.Unmarshal([]byte(body(t, resp)), &got); err != nil {
		t.Fatal(err)
	}
	for _, p := range got.Projects {
		if p.Name == "demo" {
			return p.Works
		}
	}
	return nil
}

// writePeerTmux кладёт запись реестра живых сессий клиента с адресом tmux-пары
// и состоянием: по ним сервер и различает идущий ход и досчитавший разговор.
// Процесс берётся свой: запись без живого процесса реестр отсеивает.
func writePeerTmux(t *testing.T, home, sid, tmux, status string) {
	t.Helper()
	dir := filepath.Join(home, ".claude", "sessions")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	pid := os.Getpid()
	rec := fmt.Sprintf(`{"pid":%d,"sessionId":%q,"name":"groom-1","kind":"interactive","tmux":%q,"status":%q}`,
		pid, sid, tmux, status)
	if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("%d.json", pid)), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Груминг черновика идёт живым чатом, и его tmux-сессия переживает конец
// разбора: клиент стоит на приглашении и ждёт человека. Строка с таким соседом
// показывала один «Стоп» и запустить себя не давала, хотя разбор кончился час
// назад (замечание пользователя). Работа это ход агента, а не живой клиент:
// досчитавшая сессия остаётся разговором, и строка возвращается к своим
// кнопкам.
func TestBoardRowFreeWhenTmuxIdle(t *testing.T) {
	e, _, _ := runsEnv(t, "task-XR-002\t1\t1786000000\n")
	now := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	e.s.now = func() time.Time { return now }
	sid := "dddd4444-4444-4444-8444-444444444444"
	writeSession(t, e.home, e.proj, "", sid, transcriptFixture, now.Add(-time.Minute))

	// Записи в реестре нет вовсе: про ход сказать нечего, и работа остаётся
	// работой, со «Стопом» на строке.
	if got := boardRows(t, e)["XR-002"]; got.Run != "tmux" {
		t.Fatalf("признак работы строки без записи реестра %q, ожидал tmux", got.Run)
	}

	// Клиент ведёт ход: строка занята, как и была.
	writePeerTmux(t, e.home, sid, "task-XR-002:@1.%1", "busy")
	if got := boardRows(t, e)["XR-002"]; got.Run != "tmux" {
		t.Errorf("у идущего хода признак работы %q, ожидал tmux: «Стоп» пропал с работающей строки", got.Run)
	}

	// Ход кончился, клиент жив: строка свободна.
	writePeerTmux(t, e.home, sid, "task-XR-002:@1.%1", "idle")
	if got := boardRows(t, e)["XR-002"]; got.Run != "" {
		t.Errorf("после конца хода признак работы %q, ожидал пустой: строка так и стоит под «Стопом»", got.Run)
	}

	// Работа при этом не пропала: раздел «Агенты» видит её разговором, и вход
	// в чат со строки остаётся.
	talk := workByID(projectWorks(t, e), "XR-002")
	if talk == nil || !talk.Talk {
		t.Fatalf("досчитавшая сессия пропала из работ или не помечена разговором: %+v", talk)
	}
}

// writePeerAged кладёт запись реестра со временем последнего касания: время
// там в миллисекундах, и нулевое значит «времени нет вовсе», как у части
// записей на живой машине.
func writePeerAged(t *testing.T, home, sid, tmux, status string, updated int64) {
	t.Helper()
	dir := filepath.Join(home, ".claude", "sessions")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	pid := os.Getpid()
	rec := fmt.Sprintf(`{"pid":%d,"sessionId":%q,"name":"окно-1","kind":"interactive","tmux":%q,"status":%q,"updatedAt":%d}`,
		pid, sid, tmux, status, updated)
	if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("%d.json", pid)), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Экран красил зелёным всякую живую сессию, и три десятка окон, молчавших
// часами, выглядели работающими: по экрану нельзя было сказать, чем занята
// машина (замечание пользователя по снимку). Состояние работы приезжает полем
// и разложено честно: ход идёт, ждут человека, сессия жива и молчит дольше
// рубежа, сессии нет. Запись реестра без состояния и без времени живой не
// считается.
func TestWorkLiveState(t *testing.T) {
	e, _, _ := runsEnv(t, "task-XR-002\t1\t1786000000\n")
	// Время местное нарочно: признак ожидания пишется и читается в поясе
	// машины (chat.WriteAsk), и час по UTC разъехался бы с ним на смещение.
	now := time.Date(2026, 8, 22, 12, 0, 0, 0, time.Local)
	e.s.now = func() time.Time { return now }
	sid := "dddd4444-4444-4444-8444-444444444444"
	silent := now.Add(-3 * time.Hour)
	writeSession(t, e.home, e.proj, "", sid, transcriptFixture, silent)
	writeBinds(t, e.home, listedBind(sid, "XR-002", "task-XR-002"))

	live := func() *Work {
		t.Helper()
		w := workByID(boardWorks(t, e), "XR-002")
		if w == nil {
			t.Fatalf("работа XR-002 пропала из списка")
		}
		return w
	}

	// Реестр молчит и о состоянии, и о времени: живой такая работа выглядеть не
	// должна, а время последнего хода берётся у последней содержательной
	// реплики транскрипта. Файл фикстуры трогали три часа назад, реплика в нём
	// куда старше, и ждём тут именно её: касание файла ходом агента не считается.
	said := time.Date(2026, 8, 10, 10, 0, 6, 0, time.UTC).Unix()
	writePeerAged(t, e.home, sid, "task-XR-002:@1.%1", "", 0)
	if got := live(); got.Live != workIdle || got.Moved != said {
		t.Errorf("запись реестра без состояния дала %q, ход %d: ждал простой и время реплики %d (файл правлен %d)",
			got.Live, got.Moved, said, silent.Unix())
	}

	// Клиент ведёт ход прямо сейчас.
	writePeerAged(t, e.home, sid, "task-XR-002:@1.%1", "busy", now.UnixMilli())
	if got := live(); got.Live != workBusy {
		t.Errorf("идущий ход посчитан состоянием %q, ждал %q", got.Live, workBusy)
	}

	// Реестр говорит busy, а касался записи три часа назад: работой это не
	// считается, иначе зависшее окно горело бы зелёным вечно.
	writePeerAged(t, e.home, sid, "task-XR-002:@1.%1", "busy", silent.UnixMilli())
	if got := live(); got.Live != workIdle {
		t.Errorf("протухшее busy посчитано состоянием %q, ждал %q", got.Live, workIdle)
	}

	// Агент спросил человека: ожидание старше всего, и висящий вызов
	// инструмента ожидания за работу не выдаётся.
	ask := chat.Ask{Until: now.Add(5 * time.Minute), Task: "XR-002", Session: sid,
		Questions: []chat.Question{{Text: "резать строку или поднять цену"}}}
	if err := chat.WriteAsk(e.proj, chat.TaskName("XR-002"), ask); err != nil {
		t.Fatal(err)
	}
	if got := live(); got.Live != workWait {
		t.Errorf("ожидание ответа посчитано состоянием %q, ждал %q", got.Live, workWait)
	}
}

// Тело страницы вбок не ездит никогда, а широкое содержимое ужимается внутри
// своего места. Раздел доски это свой скроллер (.groups просит overflow-y, а
// браузер делает прокручиваемой и вторую ось), и уезжал вбок именно он:
// причина блока приезжает целой фразой человека, а чип её не резал вовсе
// (замечание пользователя про мобильный вид, живая строка DK-466 уносила
// раздел на 385 точек). Тем же способом уносил раздел неразрывный путь в
// подписи сессии и в тексте вопроса клиента.
//
// Меряется настоящим движком и на всех трёх табах доски: разбором правил
// стилей такое не берётся, ширина складывается из сетки, чипов и слов.
func TestBoardNarrowNoSideScroll(t *testing.T) {
	chrome := findChrome()
	if chrome == "" {
		t.Skip("движка нет: замер ширины пропущен")
	}
	dir, page := chromeStand(t, "narrow_scroll.js", tblColsJSON(t))
	for _, tab := range []struct{ key, word string }{
		{"tasks", "задачи"}, {"sess", "сессии"}, {"drafts", "черновики"},
		{"ask", "вопрос клиента"},
	} {
		for _, win := range []string{"390,844", "430,932"} {
			got := chromeMeasure(t, chrome, dir, page, win, tab.key)
			if got["gclient"] <= 0 {
				t.Fatalf("замер %s на %s не собрался: %v", tab.word, win, got)
			}
			if got["over"] > 0 {
				// widest это на сколько дальше правой кромки раздела уехала
				// самая широкая коробка: по нему видно, что ужимать. Имя
				// виноватого стенд кладёт в заголовок окна полем who, его
				// читают глазами при разборе.
				t.Errorf("на экране %s раздел «%s» ездит вбок на %d точек "+
					"(самая широкая коробка за кромкой на %d): содержимое обязано "+
					"ужиматься внутри своего места", win, tab.word, got["over"], got["widest"])
			}
			// Кружок состояния не прижат к тексту вплотную: на узком экране
			// он стоит слева от заголовка, и слипшаяся пара читается опечаткой.
			// Ширина в ноль это инлайновый span, которому ширина не указ: так
			// кружок пропадал с экрана вовсе.
			if gap, ok := got["dotgap"]; ok && gap != -1000 {
				if gap == -2000 {
					t.Errorf("на экране %s (%s) кружок состояния нулевой ширины: "+
						"инлайновому узлу ширина не указ", win, tab.word)
				} else if gap < 6 {
					t.Errorf("на экране %s (%s) кружок состояния прижат к тексту: зазор %d точек",
						win, tab.word, gap)
				}
			}
			// Тело страницы не ездит вбок ни при каких условиях.
			if got["doc"] > got["screen"] {
				t.Errorf("на экране %s (%s) вбок поехала сама страница: %d при окне %d",
					win, tab.word, got["doc"], got["screen"])
			}
		}
	}
}

// Шапка колонок стоит ростом со строку, с симметричными отступами и ровно над
// ячейками строки. Пользователь забраковал прежний вид ровно этими словами:
// «шапка огромная и с непонятными несимметричными отступами», «заголовки
// колонок подвешенные в воздухе». Разбором правил стилей такое не берётся:
// высота складывается из отступов, роста подписи и рамки, а место колонки из
// сетки, зазоров и отступов карточки, поэтому меряет настоящий движок.
// Рубеж бокового отступа ячейки таблицы. Число тут не из головы, оно стоит в
// static/style.css одним правилом на th и td, и стенд сверяет замер с ним.
// Ходило оно в обе стороны: пять человек забраковал снизу («текст колонок стоит
// слишком близко к разделителю»), двенадцать сверху («отступы сжирают место»),
// восемь тоже сверху («те же паддинги по 8 легко сокращаются в 2 раза»). Стоит
// на четырёх: столько же отделяет друг от друга соседей внутри самой ячейки.
const tblSideMin = 4

// Он же сверху. Рубеж парный нарочно: снизу подпись прилипает к разделителю,
// сверху отступы «сжирают место» (слова пользователя), и оба края держит одно
// число из :root, а не два разных пожелания в двух местах.
const tblSideMax = 4

func TestBoardTableHeadFitsRow(t *testing.T) {
	chrome := findChrome()
	if chrome == "" {
		t.Skip("движка нет: замер шапки пропущен")
	}
	dir, page := chromeStand(t, "tbl_head.js")
	for _, tab := range []struct{ key, word string }{
		{"tasks", "задачи"}, {"sess", "сессии"}, {"drafts", "черновики"},
	} {
		got := chromeMeasure(t, chrome, dir, page, "1400,900", tab.key)
		if got["cells"] == 0 || got["kids"] == 0 {
			t.Fatalf("замер шапки раздела «%s» не собрался: %v", tab.word, got)
		}
		// Ячейка шапки на каждую ячейку строки: колонка без подписи всё равно
		// занимает своё место, иначе соседние подписи съезжают на одну.
		if got["cells"] != got["kids"] {
			t.Errorf("в разделе «%s» колонок шапки %d, а ячеек строки %d: подписи встанут мимо",
				tab.word, got["cells"], got["kids"])
		}
		t.Logf("раздел «%s»: %v", tab.word, got)
		if got["off"] != 0 {
			t.Errorf("в разделе «%s» колонка подписи разошлась с колонкой ячейки на %d точек: "+
				"в настоящей таблице колонка у них одна, и расхождение тут значит "+
				"разъехавшуюся разметку", tab.word, got["off"])
		}
		// Кромка написанного, а не только кромка ячейки: боковые отступы у th и
		// td обязаны быть одни и те же, иначе подпись стоит над своей колонкой,
		// но не над своим текстом, а глазом человек ловит именно это.
		if got["offin"] != 0 {
			t.Errorf("в разделе «%s» подпись колонки разошлась с началом ячейки на %d точек",
				tab.word, got["offin"])
		}
		if got["pad"] != 0 {
			t.Errorf("в разделе «%s» отступы шапки несимметричны: сверху и снизу расходятся на %d точек",
				tab.word, got["pad"])
		}
		// Боковой отступ подписи. Границу колонки движок рисует ровно по кромке
		// ячейки, и отделяет от неё подпись только этот отступ: в пять точек
		// подпись читалась приклеенной к разделителю (замечание пользователя).
		// Сторожится тройка: отступ у шапки тот же, что у строки; слева он тот
		// же, что справа; и его хватает, чтобы подпись от разделителя отошла.
		if got["sideh"] != got["sidec"] {
			t.Errorf("в разделе «%s» боковой отступ шапки %d, а у ячейки строки %d: "+
				"подпись встанет над своей колонкой, но не над своим текстом",
				tab.word, got["sideh"], got["sidec"])
		}
		if got["sidesym"] != 0 {
			t.Errorf("в разделе «%s» боковые отступы несимметричны: слева и справа расходятся на %d точек",
				tab.word, got["sidesym"])
		}
		if got["sideh"] < tblSideMin {
			t.Errorf("в разделе «%s» подпись колонки прижата к разделителю: отступ %d точек при %d",
				tab.word, got["sideh"], tblSideMin)
		}
		if got["sideh"] > tblSideMax {
			t.Errorf("в разделе «%s» боковой отступ ячейки %d точек при %d: "+
				"лишние точки колонка отнимает у названия", tab.word, got["sideh"], tblSideMax)
		}
		// Ростом со строку: полторы-две строки высоты человек и забраковал.
		// Нижняя граница тут тоже нужна, схлопнутая в полоску шапка это не
		// «размером со строку», а другая беда.
		if got["headh"] > got["rowh"]+4 {
			t.Errorf("в разделе «%s» шапка выше строки: %d против %d",
				tab.word, got["headh"], got["rowh"])
		}
		if got["headh"] < 30 {
			t.Errorf("в разделе «%s» шапка схлопнулась в полоску: %d точек", tab.word, got["headh"])
		}
		// Границу тянут у всякой колонки, кроме последней: справа от неё
		// двигать нечего.
		if got["grips"] != got["cells"]-1 {
			t.Errorf("в разделе «%s» ручек тяги %d при %d колонках: тянуть можно не всякую границу",
				tab.word, got["grips"], got["cells"])
		}
		if got["gripw"] <= 0 {
			t.Errorf("в разделе «%s» ручка тяги нулевой ширины: мышью в неё не попасть", tab.word)
		}
	}
}

// Кнопка работы стоит на одном и том же месте во всякой строке доски, и ряд
// кнопок влезает в свою колонку. Пользователь забраковал прежний вид словами
// «логика главной кнопки непонятна»: главную кнопку выбирало невидимое
// состояние строки, у одних строк In progress первой стояла кнопка чата, у
// других запуск. Кнопки развели по местам, но одного порядка в разметке мало:
// прижатый вправо ряд уезжал на ширину кнопки там, где третьей кнопки у строки
// не было, и глаз ловил именно это. Третьей кнопки теперь нет ни у одной
// строки, и замер сторожит, что состав так и остался одинаковым.
//
// Разбором стилей такое не берётся: место кнопки складывается из ширины
// колонки, отступов ячейки, зазоров ряда и того, куда ряд жмётся.
func TestBoardRowActionsAligned(t *testing.T) {
	chrome := findChrome()
	if chrome == "" {
		t.Skip("движка нет: замер колонки действий пропущен")
	}
	dir, page := chromeStand(t, "tbl_acts.js", tblColsJSON(t))
	for _, win := range []string{"1400,900", "1100,900"} {
		got := chromeMeasure(t, chrome, dir, page, win, "tasks")
		if got["rows"] != 2 {
			t.Fatalf("замер на окне %s не собрался: %v", win, got)
		}
		t.Logf("окно %s: %v", win, got)
		if got["btns0"] != 3 || got["btns1"] != 3 {
			t.Fatalf("стенд собрал не тот состав кнопок (%d и %d): замер говорил бы "+
				"о другой строке", got["btns0"], got["btns1"])
		}
		if got["workoff"] != 0 {
			t.Errorf("на окне %s кнопка работы разъехалась между строками на %d точек: "+
				"место у неё обязано быть одно, иначе по строке не сказать, что даст нажатие",
				win, got["workoff"])
		}
		// Три кнопки значками с зазором и боковыми отступами по шесть точек:
		// колонка того же состава занимала 136 точек, и ужата она отступами, а
		// не выброшенной кнопкой (решение пользователя).
		if got["cellw"] > 116 {
			t.Errorf("на окне %s колонка действий шире рубежа: %d точек", win, got["cellw"])
		}
		// Колонка вмещает свой ряд кнопок: вылезший за кромку ряд наезжает на
		// дату соседней колонки.
		if got["spill"] > 0 {
			t.Errorf("на окне %s ряд кнопок вылез за колонку на %d точек при её ширине %d",
				win, got["spill"], got["cellw"])
		}
	}
}

// Причина блока и причина провала приезжают в строку целой фразой человека, и
// чип с ними обязан резаться кромкой своего места: нерезаный уносил раздел
// доски в горизонтальную прокрутку на 385 точек при окне 390 (замер живой
// доски devkit, строка DK-466). Полный текст при этом не теряется, он уходит
// в подсказку.
//
// Замер настоящим движком (TestBoardNarrowNoSideScroll) эту пару не
// воспроизводит: в одиночной строке стенда чип ужимается сам, а на живой доске
// он вставал во всю длину. Поэтому тут сторожится сам рубеж: класс на месте и
// в разметке, и в стилях.
func TestBoardBlockChipClipped(t *testing.T) {
	app := readFile(t, filepath.Join("static", "app.js"))
	for _, want := range []string{
		`withFull(el("span", "chip c-block cwhy", "блок: " + row.block), row.block)`,
		`withFull(el("span", "chip c-block cwhy", "провал: " + row.fail), row.fail)`,
	} {
		if !strings.Contains(app, want) {
			t.Errorf("причина в строке доски идёт нерезаным чипом без подсказки: нет %q", want)
		}
	}
	css := readFile(t, filepath.Join("static", "style.css"))
	rule := ""
	if at := strings.Index(css, ".cwhy{"); at >= 0 {
		if end := strings.Index(css[at:], "}"); end > 0 {
			rule = css[at : at+end]
		}
	}
	if rule == "" {
		t.Fatal("правила .cwhy в стилях нет: чип причины резать нечем")
	}
	for _, want := range []string{"max-width:100%", "overflow:hidden", "text-overflow:ellipsis"} {
		if !strings.Contains(rule, want) {
			t.Errorf("чип причины не режется кромкой: в .cwhy нет %s (%s)", want, rule)
		}
	}
}

// Транскрипт трогают снаружи и при мёртвом содержимом: у живого случая (сессия
// adf6218c, разговор брошен трое суток назад) файл был правлен в тот же день, а
// последние записи внутри трёхдневной давности и обе служебные. Давность работы
// шла по времени правки файла, и экран говорил «простаивает 1 минуту» о работе,
// которой не было трое суток.
const staleSaidFixture = `{"type":"user","message":{"role":"user","content":"возьми задачу XR-005 в работу"},"timestamp":"2026-08-22T12:00:00.000Z","gitBranch":"main"}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Взял, смотрю доску."}]},"timestamp":"2026-08-22T12:20:00.000Z"}
`

// Служебный хвост того же разговора: постановка реплики в очередь, отметки
// харнеса с пустым телом и ход инструмента без вывода. Пузырём в ленте не стоит
// ни одна из этих записей, и давность они двигать не должны.
const serviceTailFixture = staleSaidFixture +
	`{"type":"queue-operation","operation":"enqueue","timestamp":"2026-08-25T16:00:00.000Z"}
{"type":"system","subtype":"stop_hook_summary","message":{"role":"system","content":""},"timestamp":"2026-08-25T17:00:00.000Z"}
{"type":"system","subtype":"turn_duration","timestamp":"2026-08-25T17:08:00.000Z"}
`

// Разговор, где содержательных реплик нет вовсе: одни служебные записи.
const noSaidFixture = `{"type":"queue-operation","operation":"enqueue","timestamp":"2026-08-22T12:00:00.000Z"}
{"type":"system","subtype":"turn_duration","timestamp":"2026-08-22T12:20:00.000Z"}
`

// movedEnv поднимает сервер с одним транскриптом проекта и отдаёт ответ
// workState по нему. Время правки файла ставится свежим нарочно: тест сторожит
// ровно то, что давность его не слушает.
func movedEnv(t *testing.T, body string, mtime, now time.Time) (string, int64, bool) {
	t.Helper()
	home := t.TempDir()
	proj := filepath.Join(home, "projects", "xr")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	sid := "adf6218c-fca0-4c7f-b908-e4af7854d7b9"
	writeSession(t, home, proj, "", sid, body, mtime)
	s := newServer(&Config{Home: home}, nil, nil)
	s.now = func() time.Time { return now }
	return s.workState(proj, "", sid, "task-XR-5", map[string]peer{}, map[string]peer{})
}

func TestWorkMovedFollowsSaidReply(t *testing.T) {
	now := time.Date(2026, 8, 25, 17, 9, 0, 0, time.UTC)
	live, moved, silent := movedEnv(t, staleSaidFixture, now.Add(-time.Minute), now)
	want := time.Date(2026, 8, 22, 12, 20, 0, 0, time.UTC).Unix()
	if moved != want {
		t.Errorf("давность идёт не по последней реплике: moved %d, жду %d (правка файла %d)",
			moved, want, now.Add(-time.Minute).Unix())
	}
	if silent {
		t.Errorf("разговор с репликами помечен как разговор без реплик")
	}
	if live != workIdle {
		t.Errorf("состояние живой сессии без хода: %q, жду %q", live, workIdle)
	}
}

func TestWorkMovedIgnoresServiceRecords(t *testing.T) {
	now := time.Date(2026, 8, 25, 17, 9, 0, 0, time.UTC)
	_, moved, silent := movedEnv(t, serviceTailFixture, now.Add(-time.Minute), now)
	want := time.Date(2026, 8, 22, 12, 20, 0, 0, time.UTC).Unix()
	if moved != want {
		t.Errorf("служебный хвост сдвинул давность: moved %d, жду %d", moved, want)
	}
	if silent {
		t.Errorf("разговор с репликами помечен как разговор без реплик")
	}
}

func TestWorkMovedSaysWhenNoReplies(t *testing.T) {
	now := time.Date(2026, 8, 25, 17, 9, 0, 0, time.UTC)
	_, moved, silent := movedEnv(t, noSaidFixture, now.Add(-time.Minute), now)
	if !silent {
		t.Errorf("разговор без реплик не назван таковым, экран скажет о нём давность как о ходе")
	}
	want := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC).Unix()
	if moved != want {
		t.Errorf("давность разговора без реплик идёт не от его начала: moved %d, жду %d", moved, want)
	}
}

// Как строка сессии произносит давность: разговор с давними репликами мерится
// ими, а не касанием файла, а разговор без реплик говорит об этом словами.
// Предмет проверки это собранная разметка, поэтому статика поднимается в node с
// заглушкой DOM (стенд testdata/poc_saidage.mjs). Без node шаг пропускается:
// узел стенда, а не рабочей части.
func TestStaticSessionAgeBySaid(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node не найден: стенд давности строки сессии пропущен")
	}
	out, err := exec.Command(node, filepath.Join("testdata", "poc_saidage.mjs"),
		filepath.Join("static", "app.js")).CombinedOutput()
	if err != nil {
		t.Fatalf("давность строки сессии: %v\n%s", err, out)
	}
	t.Log(strings.TrimSpace(string(out)))
}

// Тяга колонок вытягивала строку за карточку: пользователь растянул колонку
// «Задача», и кнопка запуска встала на самой кромке, кнопка разговора уехала на
// фон страницы, а номер задачи обрубился слева. Предел стоял на одну колонку, а
// сумму колонок с шириной таблицы никто не сверял. Сторожит укладку стенд
// testdata/poc_tblfit.mjs: тяга в любую сторону в любом разделе, ширины из
// памяти на узком окне и само сужение окна.
func TestStaticTableColsFitCard(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node не найден: стенд укладки колонок пропущен")
	}
	out, err := exec.Command(node, filepath.Join("testdata", "poc_tblfit.mjs"),
		filepath.Join("static", "app.js")).CombinedOutput()
	if err != nil {
		t.Fatalf("укладка колонок в ширину таблицы: %v\n%s", err, out)
	}
	t.Log(strings.TrimSpace(string(out)))
}

// Формы мерялись своими числами в каждом месте, и отступы разъезжались: между
// описанием и рангом стояло двадцать точек, между прочими блоками
// четырнадцать, а «выровнять» означало подогнать пять чисел на глаз (замечание
// пользователя). Теперь шаг отступа и радиус названы лестницей в :root, и
// сторож держит рубеж: блоки формы берут величины оттуда, а не пишут свои px.
func TestStaticFormsUseSharedSteps(t *testing.T) {
	css := readFile(t, filepath.Join("static", "style.css"))
	// Сама лестница обязана быть на месте: без неё правило ниже проверяло бы
	// ссылки на несуществующие величины.
	for _, name := range []string{"--sp1:", "--sp2:", "--sp3:", "--sp4:", "--fgap:",
		"--rad1:", "--rad2:", "--rad3:"} {
		if !strings.Contains(css, name) {
			t.Fatalf("в :root нет величины %s: лестницу отступов и радиусов завести забыли", name)
		}
	}
	// Блоки формы и её окружения: карточка, ранг, сетка описания, полосы и
	// коробки правки.
	blocks := []string{".card", ".tgrid", ".rcard", ".rtop", ".rbig", ".rcard .rtop",
		".rcard .rbody", ".rcard .rbig", ".nbar", ".ktabs", ".rankbox", ".swch",
		".dnote", ".chd", ".phd", ".pbd"}
	// Свойства, которыми меряется рыхлость формы. Ширины и высоты сюда не идут:
	// предмет тут ритм отступов и скругление рамки.
	props := regexp.MustCompile(`(?:^|;)\s*(border-radius|margin-top|padding|gap)\s*:\s*([^;}]+)`)
	px := regexp.MustCompile(`\b\d+(?:\.\d+)?px\b`)
	for _, sel := range blocks {
		for _, decl := range cssDecls(css, sel) {
			for _, hit := range props.FindAllStringSubmatch(decl, -1) {
				if px.MatchString(hit[2]) {
					t.Errorf("блок формы %s пишет свои числа вместо общей лестницы: %s:%s",
						sel, hit[1], strings.TrimSpace(hit[2]))
				}
			}
		}
	}
}

// cssDecls отдаёт тела правил ровно этого селектора: селектор ищется целиком,
// поэтому «.card» не притягивает «.dcard» и «.card > .prow».
func cssDecls(css, sel string) []string {
	var out []string
	for i := 0; i+len(sel) <= len(css); i++ {
		if css[i:i+len(sel)] != sel {
			continue
		}
		if i > 0 && !strings.ContainsRune(" \t\n{},>~", rune(css[i-1])) {
			continue
		}
		rest := css[i+len(sel):]
		cut := strings.IndexAny(rest, "{,")
		if cut < 0 || rest[cut] == ',' || strings.TrimSpace(rest[:cut]) != "" {
			continue
		}
		end := strings.IndexByte(rest[cut:], '}')
		if end < 0 {
			continue
		}
		out = append(out, rest[cut+1:cut+end])
	}
	return out
}

// bindTmux собирает строку реестра с именем tmux-сессии: имя это и есть след
// подъёма дашбордом, ради него стенды сюда и ходят.
func bindTmux(stamp, sid, task, tmux string) string {
	return fmt.Sprintf("%s сессия %s задача %s проект demo дерево /tmp/demo "+
		"транскрипт /tmp/t.jsonl источник заказ повод startup tmux %s\n", stamp, sid, task, tmux)
}

// Своей работа считается по записи реестра, а не по образцу имени сессии.
// Прежде своей объявлялась всякая tmux-сессия вида task-<ID>, хотя завести её
// может и рука в терминале, и shipctl: признак стоял на форме имени, а данных
// под ним не было никаких (жалоба пользователя, own=true при пустой сессии).
// Записи с именем кладёт только подъём дашборда, через DEVKIT_TMUX.
func TestLiveWorksOwnStandsOnBindRecord(t *testing.T) {
	e, _, _ := runsEnv(t, `task-XR-004\t1\t1000\n`)
	sid := "aaaa1111-1111-4111-8111-111111111111"

	// Реестр пуст: сессию заводили мимо дашборда.
	writeBinds(t, e.home, "")
	got := workByID(boardWorks(t, e), "XR-004")
	if got == nil {
		t.Fatal("работа XR-004 пропала из списка")
	}
	if got.Own || got.Tmux != "" {
		t.Errorf("сессия без записи реестра объявлена своей: own=%v tmux=%q", got.Own, got.Tmux)
	}

	// Запись с именем: работу поднял дашборд, и признак опирается на имя.
	writeBinds(t, e.home, bindTmux("2026-08-22T11:00:00", sid, "XR-004", "task-XR-004"))
	got = workByID(boardWorks(t, e), "XR-004")
	if got == nil || !got.Own || got.Tmux != "task-XR-004" {
		t.Errorf("работа с записью реестра не признана своей: %+v", got)
	}
}

// Цикл цели ходит не только своей tmux-сессией goal-<ID>: витки идут и живым
// чатом дашборда, и носитель работы тогда чужой, chat-<ID>-<n>. Прежде такая
// цель приезжала мёртвой и без признака происхождения вовсе, а экран писал
// «идёт вне дашборда», хотя человек вёл разговор ровно отсюда (жалоба
// пользователя на DK-446).
func TestLiveWorksRegistryGoalKnowsItsChat(t *testing.T) {
	e, _, _ := runsEnv(t, `chat-XR-112-1\t1\t1000\n`)
	sid := "bbbb2222-2222-4222-8222-222222222222"

	// Носителя нет: цикл и правда подняли в другом месте.
	writeBinds(t, e.home, "")
	got := workByID(boardWorks(t, e), "XR-112")
	if got == nil || got.Own || got.Tmux != "" || got.Session != "" {
		t.Fatalf("цель без своего чата объявлена своей: %+v", got)
	}

	// Чат цели поднят дашбордом: цель узнаётся своей через него.
	writeBinds(t, e.home, bindTmux("2026-08-22T11:00:00", sid, "XR-112", "chat-XR-112-1"))
	got = workByID(boardWorks(t, e), "XR-112")
	if got == nil {
		t.Fatal("цель XR-112 пропала из списка")
	}
	if !got.Own || got.Tmux != "chat-XR-112-1" || got.Session != sid {
		t.Errorf("цель, которую ведёт чат дашборда, не признана своей: %+v", got)
	}
}

// Имя мёртвой tmux-сессии своей работы не делает: закрывать по нему нечего, а
// признак own обещает человеку живую кнопку закрытия.
func TestLiveWorksOwnIgnoresDeadTmuxName(t *testing.T) {
	e, _, _ := runsEnv(t, `chat-XR-112-1\t1\t1000\n`)
	writeBinds(t, e.home, bindTmux("2026-08-22T11:00:00",
		"cccc3333-3333-4333-8333-333333333333", "XR-112", "chat-XR-112-9"))
	got := workByID(boardWorks(t, e), "XR-112")
	if got == nil || got.Own || got.Tmux != "" {
		t.Errorf("цель признана своей по имени сессии, которой на машине нет: %+v", got)
	}
}

// Работа, чьей строки на доске нет (задача закрыта и уехала в архив),
// подписывается заголовком своего разговора. Прежде в списке стоял голый
// номер, хотя заголовок у разговора был и внутри чата его видно (замечание
// пользователя): лестница заголовка тут та же, что у списка чатов.
func TestLiveWorksTitleFallsBackToChat(t *testing.T) {
	e, _, _ := runsEnv(t, `task-XR-777\t1\t1000\n`)
	sid := "dddd4444-4444-4444-8444-444444444444"
	writeSession(t, e.home, e.proj, "", sid,
		`{"type":"summary","summary":"Краснота регрессионного теста"}`+"\n"+transcriptFixture,
		time.Now())
	writeBinds(t, e.home, bindTmux("2026-08-22T11:00:00", sid, "XR-777", "task-XR-777"))
	got := workByID(boardWorks(t, e), "XR-777")
	if got == nil {
		t.Fatal("работа XR-777 пропала из списка")
	}
	if got.Title != "Краснота регрессионного теста" {
		t.Errorf("работа без строки на доске подписана %q, ждал заголовок разговора", got.Title)
	}
}

// То же и у работы, узнанной транскриптом: заголовок разговора доезжает до
// строки, а не остаётся в окне чата.
func TestSessionWorkTitleFallsBackToChat(t *testing.T) {
	e, _, _ := runsEnv(t, `chat-XR-777-1\t1\t1000\n`)
	sid := "eeee5555-5555-4555-8555-555555555555"
	writeSession(t, e.home, e.proj, "", sid,
		`{"type":"summary","summary":"Разбор накопителя черновиков"}`+"\n"+transcriptFixture,
		time.Now())
	writeBinds(t, e.home, bindTmux("2026-08-22T11:00:00", sid, "XR-777", "chat-XR-777-1"))
	got := workByID(boardWorks(t, e), "XR-777")
	if got == nil {
		t.Fatal("работа XR-777 пропала из списка")
	}
	if got.Title != "Разбор накопителя черновиков" {
		t.Errorf("разговор без строки на доске подписан %q, ждал его заголовок", got.Title)
	}
}

// tblColsJSON вычитывает TBL_COLS прямо из static/app.js: стенд замера
// раскладывает колонки теми же ширинами, какими их раскладывает экран. Копия
// чисел в стенде разошлась бы с кодом молча, и замер сторожил бы вымысел.
func tblColsJSON(t *testing.T) string {
	t.Helper()
	app := readFile(t, filepath.Join("static", "app.js"))
	at := strings.Index(app, "const TBL_COLS = {")
	if at < 0 {
		t.Fatal("в static/app.js нет TBL_COLS: описание колонок переехало, стенд замера ослеп")
	}
	end := strings.Index(app[at:], "\n};")
	if end < 0 {
		t.Fatal("описание TBL_COLS не кончается: разметку блока сменили")
	}
	block := app[at : at+end]
	sect := regexp.MustCompile(`(?m)^  (\w+): \[`)
	one := regexp.MustCompile(`\{ key: "(\w+)"[^}]*\}`)
	field := func(src, name string) string {
		m := regexp.MustCompile(name + `: "([^"]*)"`).FindStringSubmatch(src)
		if m == nil {
			return ""
		}
		return m[1]
	}
	out := map[string][]map[string]any{}
	names := sect.FindAllStringSubmatchIndex(block, -1)
	if len(names) == 0 {
		t.Fatal("в TBL_COLS не нашлось ни одного раздела")
	}
	for at, name := range names {
		tail := len(block)
		if at+1 < len(names) {
			tail = names[at+1][0]
		}
		body := block[name[1]:tail]
		key := block[name[2]:name[3]]
		for _, col := range one.FindAllString(body, -1) {
			w := 0
			if m := regexp.MustCompile(`w: (\d+)`).FindStringSubmatch(col); m != nil {
				w, _ = strconv.Atoi(m[1])
			}
			out[key] = append(out[key], map[string]any{
				"key":   field(col, "key"),
				"label": field(col, "label"),
				"ico":   field(col, "ico"),
				"first": field(col, "first") != "",
				"flex":  strings.Contains(col, "flex: true"),
				"w":     w,
			})
		}
		if len(out[key]) == 0 {
			t.Fatalf("в разделе %q описание колонок не разобралось", key)
		}
	}
	raw, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	return "window.TBLFIT = " + string(raw) + ";"
}

// Колонка обязана вмещать собственное содержимое. Длинное название задачи
// режется многоточием, и это верно: под название колонки не хватит никогда. А
// короткая метка (чип уровня разбора, номер, дата, подпись кнопки, слово в
// шапке) обрубается только от нехватки ширины, и «средн» вместо «средний» это
// дефект колонки, а не кромка.
//
// Пользователь поймал это на снимке накопителя: чип уровня резался по правому
// краю в обоих словах. Разбором стилей такое не берётся, ширину слова считает
// шрифт, поэтому меряет настоящий движок и меряет тем же, чем видит человек:
// написанное шире того места, где оно стоит.
func TestBoardTableLabelsNotCut(t *testing.T) {
	chrome := findChrome()
	if chrome == "" {
		t.Skip("движка нет: замер меток пропущен")
	}
	dir, page := chromeStand(t, "tbl_fit.js", tblColsJSON(t))
	for _, tab := range []struct{ key, word string }{
		{"tasks", "задачи"}, {"sess", "сессии"}, {"drafts", "черновики"},
	} {
		got := chromeMeasure(t, chrome, dir, page, "1400,900", tab.key)
		if got["cols"] == 0 {
			t.Fatalf("замер раздела «%s» не собрался: %v", tab.word, got)
		}
		t.Logf("раздел «%s»: %v", tab.word, got)
		for name, cut := range got {
			col, ok := strings.CutPrefix(name, "cut_")
			if !ok {
				continue
			}
			// Точка допуска на округление: ширины движок отдаёт целыми, а
			// считает дробными, и лишняя точка тут не обрубок.
			if cut > 1 {
				t.Errorf("в разделе «%s» колонка %q режет своё содержимое, не хватает %d точек: "+
					"короткая метка обрубается только от нехватки ширины", tab.word, col, cut)
			}
			if head := got["head_"+col]; head > 1 {
				t.Errorf("в разделе «%s» колонка %q режет собственную подпись в шапке, "+
					"не хватает %d точек", tab.word, col, head)
			}
		}
		if tab.key != "sess" {
			continue
		}
		// Колонка хода несёт кружок в девять точек, и раздуваться ей нечем. Пока
		// подпись у неё была словом, она занимала сперва 136 точек со словом
		// «Состояние», потом 80 со словом «Ход», и место это ело у названия
		// работы. Подпись у неё теперь значком, а сторожится не число (число
		// правит человек тягой границы), а порядок: колонка под значок уже
		// колонки под текст.
		for _, near := range []struct{ key, word string }{
			{"moved", "Активность"}, {"act", "хвост с кнопками"},
		} {
			if got["w_live"] >= got["w_"+near.key] {
				t.Errorf("колонка хода шире колонки «%s»: %d против %d, "+
					"а несёт она кружок, а не слова",
					near.word, got["w_live"], got["w_"+near.key])
			}
		}
	}
}

// Отступы ячеек и ширины колонок не держат пустого места. Пользователь поймал
// это глазом: «колонку с приоритетом в черновиках нужно уменьшить, там огромный
// отступ в начале и возможно в конце. И вообще нужно уменьшить отступы во всех
// колонках и во всех таблицах, кажется они сжирают место».
//
// Мест тут два, и они разные. Первое это расширенный отступ первой ячейки: он
// заведён под кружок состояния, который висит слева от номера задачи, и там он
// нужен. У сессий кружок стоит в ячейке один, у накопителя его нет вовсе, а
// пятнадцать лишних точек колонка приоритета всё равно держала. Второе это
// запас ширины сверх собственного содержимого: колонка стоит числом из
// TBL_COLS, содержимое меряет шрифт, и разойтись они могут молча.
//
// Разбором стилей ни то, ни другое не берётся, поэтому меряет движок: отступы
// готовой раскладки и самую узкую ширину, при которой колонка ещё ничего не
// режет.
func TestBoardTableCellsNoDeadSpace(t *testing.T) {
	chrome := findChrome()
	if chrome == "" {
		t.Skip("движка нет: замер отступов пропущен")
	}
	dir, page := chromeStand(t, "tbl_fit.js", tblColsJSON(t))
	got := map[string]map[string]int{}
	for _, kind := range []string{"tasks", "sess", "drafts"} {
		got[kind] = chromeMeasure(t, chrome, dir, page, "1400,900", kind)
	}
	side := got["tasks"]["l_padin"]
	if side == 0 {
		t.Fatal("боковой отступ внутренней ячейки не замерился")
	}
	// Расширенный отступ остаётся там, где кружок стоит рядом с текстом.
	if got["tasks"]["l_padl"] <= side {
		t.Errorf("у колонки номера отступ первой ячейки %d при боковом %d: "+
			"кружок состояния висит в нём слева от номера, и место ему нужно",
			got["tasks"]["l_padl"], side)
	}
	for _, tab := range []struct{ key, word string }{
		{"sess", "сессии"}, {"drafts", "черновики"},
	} {
		if pad := got[tab.key]["l_padl"]; pad != side {
			t.Errorf("в разделе «%s» первая ячейка стоит отступом %d при боковом %d: "+
				"кружка рядом с текстом там нет, и расширять отступ не под что",
				tab.word, pad, side)
		}
	}
	// Вертикальный отступ ячейки и высота строки. Отступ снизу ограничен ровно
	// так же, как сверху: строка обязана вместить свою начинку целиком, иначе
	// ужатие оборачивается теснотой, а не отвоёванным местом.
	for _, tab := range []struct{ key, word string }{
		{"tasks", "задачи"}, {"sess", "сессии"}, {"drafts", "черновики"},
	} {
		got := got[tab.key]
		if got["l_padt"] > tblRowPyMax || got["l_padb"] > tblRowPyMax {
			t.Errorf("в разделе «%s» вертикальный отступ ячейки %d/%d при рубеже %d: "+
				"лишние точки строка тратит на воздух", tab.word,
				got["l_padt"], got["l_padb"], tblRowPyMax)
		}
		if got["l_padt"] == 0 || got["l_padb"] == 0 {
			t.Errorf("в разделе «%s» вертикального отступа у ячейки нет вовсе: "+
				"кнопки хвоста лягут на разделитель строк", tab.word)
		}
		if got["l_padt"] != got["l_padb"] {
			t.Errorf("в разделе «%s» отступы ячейки несимметричны: сверху %d, снизу %d",
				tab.word, got["l_padt"], got["l_padb"])
		}
		// Высота строки это начинка плюс два отступа, и меньше её не бывает:
		// иначе двухстрочная подпись работы или ряд кнопок режется кромкой.
		if need := got["l_fill"] + got["l_padt"] + got["l_padb"]; got["l_rowh"] < need {
			t.Errorf("в разделе «%s» строка ростом %d при начинке %d и отступах %d: "+
				"содержимое не влезает", tab.word, got["l_rowh"], got["l_fill"], got["l_padt"])
		}
	}
	// Запас ширины сверх содержимого. Хвост с кнопками тут не судится: ширину
	// ему задаёт набор кнопок, который в строке меняется, а не самая длинная
	// метка.
	for _, tab := range []struct{ key, word string }{
		{"tasks", "задачи"}, {"sess", "сессии"}, {"drafts", "черновики"},
	} {
		t.Logf("раздел «%s»: %v", tab.word, look(got[tab.key]))
		for name, min := range got[tab.key] {
			col, ok := strings.CutPrefix(name, "min_")
			if !ok || col == "act" {
				continue
			}
			if free := got[tab.key]["w_"+col] - min; free > tblColFree {
				t.Errorf("в разделе «%s» колонка %q держит %d точек пустого места "+
					"при запасе %d: место это она отнимает у названия",
					tab.word, col, free, tblColFree)
			}
		}
	}
}

// Запас ширины колонки сверх её содержимого: на букву длиннее в номере задачи и
// на разряд в ранге. Больше это уже пустое место, которого человек и не досчитался.
const tblColFree = 12

// Рубеж вертикального отступа ячейки. Число названо пользователем прямо: «по
// высоте можно тоже сократить убрав лишние паддинги для всей строки с 11 до 5».
// Ниже пятёрки начинка ячейки прилипает к разделителю строк, выше строка
// раздувается.
const tblRowPyMax = 5

// Моргание кнопки разговора красится основным цветом пульта, тем же, каким
// красится кнопка запуска: на зелёном --run метка терялась на тёмном фоне
// строки («анимация плохо видна», замечание пользователя). Цвет берётся
// переменной, а не числом: со сменой темы он не должен разъехаться с кнопкой.
func TestChatLiveBlinkUsesAccent(t *testing.T) {
	css := readFile(t, filepath.Join("static", "style.css"))
	// Кнопка запуска и есть источник цвета: разойдись эти два места, «взять тот
	// же цвет» снова стало бы подгонкой на глаз.
	if !strings.Contains(css, ".btn-acc{background:var(--acc)") {
		t.Fatal("кнопка запуска красится не переменной --acc: брать моргание не с чего")
	}
	rule := regexp.MustCompile(`\.chatlive\{([^}]*)\}`).FindStringSubmatch(css)
	if rule == nil {
		t.Fatal("правила .chatlive в стилях нет: метка живого разговора пропала")
	}
	if !strings.Contains(rule[1], "var(--acc)") {
		t.Errorf("рамка живого разговора красится не основным цветом: %s", rule[1])
	}
	frames := regexp.MustCompile(`@keyframes dklive\{([^@]*?)\}\s*\n`).FindStringSubmatch(css)
	if frames == nil {
		t.Fatal("дорожки моргания dklive в стилях нет")
	}
	if !strings.Contains(frames[1], "var(--acc)") {
		t.Errorf("моргание идёт не основным цветом: %s", frames[1])
	}
	if strings.Contains(rule[1], "var(--run)") || strings.Contains(frames[1], "var(--run)") {
		t.Errorf("моргание осталось зелёным --run, на фоне строки его не видно: %s %s",
			rule[1], frames[1])
	}
	// Такт: заметно, но не мельтешит. Быстрее полутора секунд метка дёргается,
	// медленнее четырёх её принимают за неподвижную рамку.
	said := regexp.MustCompile(`\.chatlive\{animation:dklive ([0-9.]+)s`).FindStringSubmatch(css)
	if said == nil {
		t.Fatal("у моргания нет такта: правила animation в .chatlive не нашлось")
	}
	secs, err := strconv.ParseFloat(said[1], 64)
	if err != nil {
		t.Fatalf("такт моргания не читается числом: %q", said[1])
	}
	if secs < 1.5 || secs > 4 {
		t.Errorf("такт моргания %.1f с: заметно и не мельтеша это от полутора до четырёх", secs)
	}
}

// Кнопку работы в строке выбирает живость нашей сессии, а не путь, которым
// работу узнали: у идущей цели (via=registry) вместо «Стопа» стояло
// «Продолжить», и нажатие увело бы вводную продолжения в живой ход. Сторожит
// стенд testdata/poc_rowrun.mjs.
func TestStaticRowRunButton(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node не найден: стенд кнопки работы пропущен")
	}
	out, err := exec.Command(node, filepath.Join("testdata", "poc_rowrun.mjs"),
		filepath.Join("static", "app.js")).CombinedOutput()
	if err != nil {
		t.Fatalf("кнопка работы в строке: %v\n%s", err, out)
	}
	t.Log(strings.TrimSpace(string(out)))
}

// Список подписок выбирает сторону раскрытия по месту: у кнопки, перенесённой
// на второй ряд, слева места нет, и растущий влево список обрезался краем
// главной части экрана. Сторожит стенд testdata/poc_hpop.mjs.
func TestStaticHpopSide(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node не найден: стенд стороны списка подписок пропущен")
	}
	out, err := exec.Command(node, filepath.Join("testdata", "poc_hpop.mjs"),
		filepath.Join("static", "app.js")).CombinedOutput()
	if err != nil {
		t.Fatalf("сторона раскрытия списка подписок: %v\n%s", err, out)
	}
	t.Log(strings.TrimSpace(string(out)))
}

// Командная панель формы меряется настоящим движком поверх настоящего app.js
// (POC DK-397). Стенды раскладки до сих пор повторяли вёрстку руками, и замер
// говорил о разметке, которую экран мог давно сменить; тут страницу собирает
// сам клиент, а сервер подменяет testdata/live_mock.js.
//
// Предмет замера два. Раскрытый список подписок обязан стоять в границах
// экрана на любой ширине: на телефоне кнопка панели переносится на свою строку
// и встаёт у левого края, а список висел на её правом крае и уходил за границу
// («выпадающее меню вылезает за пределы экрана», замечание пользователя).
// Второй предмет это ширина самих кнопок: подписи резались ради телефона, и
// без замера сокращение слова ничего не говорит о ширине, которую складывают
// поля, значок и кегль.
func TestStaticFormPopFits(t *testing.T) {
	chrome := findChrome()
	if chrome == "" {
		t.Skip("chrome не найден: замер командной панели пропущен")
	}
	dir, page := chromeLiveStand(t, "form_pop.js")
	// Зазор до края: список, прижатый вплотную к границе, читается как
	// обрезанный, и то же число держит HPOP_EDGE в app.js.
	const edge = 8
	// Поля основной кнопки. Пол взят не на глаз: внутри кнопки значок отделён от
	// слова шестью точками, и поле уже этого зазора поставило бы слово ближе к
	// краю кнопки, чем к её же значку. Потолок на две точки выше пола: панель
	// стоит своей строкой на телефоне, и каждая лишняя точка по краям читается
	// длиной.
	const padFloor, padCap = 8, 8
	for _, form := range []struct {
		bar   string
		label string
		btn   int
		wide  int
	}{
		{"draft", "Грумить", 125, 92},
		{"task", "Выполнить", 145, 110},
		{"check", "Проверить", 145, 110},
	} {
		narrow := chromeMeasure(t, chrome, dir, page, "390,844", form.bar)
		t.Logf("%s на телефоне: %v", form.bar, narrow)
		if narrow["screen"] != 390 {
			t.Fatalf("%s: окно стенда не 390 пикселей: %v", form.bar, narrow)
		}
		if got := narrow["label-len"]; got != len([]rune(form.label)) {
			t.Errorf("%s: подпись кнопки длиной %d букв, а ждали «%s»", form.bar, got, form.label)
		}
		if narrow["btn-w"] > form.btn {
			t.Errorf("%s: кнопка «%s» шириной %d при потолке %d: на телефоне она занимает "+
				"строку целиком", form.bar, form.label, narrow["btn-w"], form.btn)
		}
		if narrow["wide-w"] > form.wide {
			t.Errorf("%s: широкая половина кнопки «%s» шириной %d при потолке %d",
				form.bar, form.label, narrow["wide-w"], form.wide)
		}
		// Стрелка выбора подписки стоит в одном ряду с кнопками-значками панели
		// и обязана быть ровно их ширины: своего числа у неё нет, и раздутая
		// стрелка тянула составную кнопку вширь (замечание пользователя).
		if narrow["arrow-w"] != narrow["kin-w"] {
			t.Errorf("%s: узкая часть с выбором подписки шириной %d при соседних кнопках "+
				"панели в %d", form.bar, narrow["arrow-w"], narrow["kin-w"])
		}
		// Поля основной кнопки: слово не должно липнуть к краю, но и лишних
		// точек по бокам панель не носит. Потолок тут общий на обе стороны.
		if narrow["pad-left"] > padCap || narrow["pad-right"] > padCap {
			t.Errorf("%s: поля кнопки «%s» шире потолка %d: слева %d, справа %d",
				form.bar, form.label, padCap, narrow["pad-left"], narrow["pad-right"])
		}
		if narrow["pad-left"] < padFloor || narrow["pad-right"] < padFloor {
			t.Errorf("%s: поля кнопки «%s» уже пола %d: подпись липнет к краю, "+
				"слева %d, справа %d", form.bar, form.label, padFloor,
				narrow["pad-left"], narrow["pad-right"])
		}
		if narrow["gap-left"] < edge {
			t.Errorf("%s: раскрытый список подписок отстоит от левого края главной части "+
				"на %d пикселей: он уехал за границу экрана", form.bar, narrow["gap-left"])
		}
		if narrow["gap-right"] < edge {
			t.Errorf("%s: раскрытый список подписок отстоит от правого края окна на %d "+
				"пикселей: он уехал за границу экрана", form.bar, narrow["gap-right"])
		}

		// На ноутбуке места вдоволь, и списку двигаться незачем: он висит
		// правым краем на кнопке, как и рисует макет.
		wide := chromeMeasure(t, chrome, dir, page, "1280,900", form.bar)
		if wide["pop-hang"] != 0 {
			t.Errorf("%s: на ноутбуке список подписок съехал от правого края кнопки на %d "+
				"пикселей", form.bar, wide["pop-hang"])
		}
		if wide["gap-left"] < edge || wide["gap-right"] < edge {
			t.Errorf("%s: на ноутбуке список вышел за границы: слева %d, справа %d",
				form.bar, wide["gap-left"], wide["gap-right"])
		}
	}
}

// Кнопка закрытия панели разговора меряется настоящим движком (POC DK-397).
// Крестик стоял бледным значком без рамки и фона, вдвое меньше соседних кнопок
// шапки, и попасть в него пальцем было нечем («малозаметный крестик»,
// замечание пользователя). Своего стиля у кнопки нет: числа она берёт у
// соседей, и стенд сверяет её именно с ними, а не с записанной константой.
func TestStaticChatShutLook(t *testing.T) {
	chrome := findChrome()
	if chrome == "" {
		t.Skip("chrome не найден: замер кнопки закрытия панели пропущен")
	}
	dir, page := chromeLiveStand(t, "chat_head.js")
	for _, window := range []string{"1280,900", "390,844"} {
		got := chromeMeasure(t, chrome, dir, page, window, "chat")
		t.Logf("окно %s: %v", window, got)
		if got["kin-w"] == 0 || got["kin-h"] == 0 {
			t.Fatalf("окно %s: соседние кнопки шапки не померились: %v", window, got)
		}
		if got["shut-w"] < got["kin-w"] || got["shut-h"] < got["kin-h"] {
			t.Errorf("окно %s: кнопка закрытия %dx%d меньше соседней кнопки шапки %dx%d: "+
				"пальцем в неё не попасть", window, got["shut-w"], got["shut-h"],
				got["kin-w"], got["kin-h"])
		}
		if got["glyph"] < got["kin-glyph"] {
			t.Errorf("окно %s: крестик шириной %d при значке соседа %d: он теряется в шапке",
				window, got["glyph"], got["kin-glyph"])
		}
		if got["border"] < got["kin-border"] {
			t.Errorf("окно %s: у кнопки закрытия нет рамки соседей: %d против %d",
				window, got["border"], got["kin-border"])
		}
		if got["filled"] != 1 {
			t.Errorf("окно %s: кнопка закрытия стоит без фона соседних кнопок и читается "+
				"голым значком", window)
		}
		if got["same-ink"] != 1 {
			t.Errorf("окно %s: крестик нарисован бледнее соседних кнопок шапки", window)
		}
	}
}

// chromeLiveStand собирает страницу стенда из настоящей статики: мок сети,
// сам app.js и замерочный скрипт. От chromeStand он отличается тем, что клиент
// тут работает целиком, а не подменяется рукописной вёрсткой.
func chromeLiveStand(t *testing.T, probeName string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	page := filepath.Join(dir, "stand.html")
	html := readFile(t, filepath.Join("static", "index.html"))
	if html == "" {
		t.Fatal("static/index.html не прочитан")
	}
	src := func(name string) string {
		path, err := filepath.Abs(name)
		if err != nil {
			t.Fatal(err)
		}
		return `<script src="file://` + path + `"></script>`
	}
	css, err := filepath.Abs(filepath.Join("static", "style.css"))
	if err != nil {
		t.Fatal(err)
	}
	html = strings.Replace(html, "/assets/style.css", "file://"+css, 1)
	// app.js грузится обычным скриптом, а не модулем: замерочному скрипту нужны
	// функции экрана, а модуль держит их при себе. Разбор модулем сторожит
	// отдельный тест, тут же важна собранная страница.
	live := src(filepath.Join("testdata", "live_mock.js")) +
		src(filepath.Join("static", "app.js")) +
		src(filepath.Join("testdata", probeName))
	html = strings.Replace(html,
		`<script type="module" src="/assets/app.js"></script>`, live, 1)
	if !strings.Contains(html, probeName) {
		t.Fatal("замерочный скрипт не встал на место app.js: разметка index.html разъехалась с тестом")
	}
	if err := os.WriteFile(page, []byte(html), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir, page
}

// Боковая колонка сворачивается и возвращается тем же движением, состояние
// переживает перезагрузку, а освободившееся место достаётся содержимому.
// Сторожит стенд testdata/poc_side.mjs.
func TestStaticSideFold(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node не найден: стенд сворачивания колонки пропущен")
	}
	out, err := exec.Command(node, filepath.Join("testdata", "poc_side.mjs"),
		filepath.Join("static", "app.js")).CombinedOutput()
	if err != nil {
		t.Fatalf("сворачивание боковой колонки: %v\n%s", err, out)
	}
	t.Log(strings.TrimSpace(string(out)))
}

// Кнопку работы на форме задачи выбирает идущий ход, а не живое окно: у DK-543
// сессия была жива, агент в ней простаивал, а форма предлагала «Стоп» вместо
// пуска. Правило это одно на строку доски и на форму. Сторожит стенд
// testdata/poc_taskrun.mjs.
func TestStaticTaskRunButton(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node не найден: стенд кнопки работы на форме пропущен")
	}
	out, err := exec.Command(node, filepath.Join("testdata", "poc_taskrun.mjs"),
		filepath.Join("static", "app.js")).CombinedOutput()
	if err != nil {
		t.Fatalf("кнопка работы на форме задачи: %v\n%s", err, out)
	}
	t.Log(strings.TrimSpace(string(out)))
}

// Раздел сессий держал две колонки под то, что человек прочитал как одно: про
// «Идёт» и «Активность» он сказал, что они показывают похоже одно и то же, а
// про колонку хода, что под неё занято слишком много места, восемь десятков
// точек под кружок в девять.
// Сторожит стенд testdata/poc_sesscol.mjs: подписи у колонки состояния
// нет, ширина её меньше порога, сортировка по ней жива, колонки возраста нет, а
// сам возраст стоит в подсказке даты активности.
func TestStaticSessionColumns(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node не найден: стенд колонок сессий пропущен")
	}
	out, err := exec.Command(node, filepath.Join("testdata", "poc_sesscol.mjs"),
		filepath.Join("static", "app.js")).CombinedOutput()
	if err != nil {
		t.Fatalf("колонки раздела сессий: %v\n%s", err, out)
	}
	t.Log(strings.TrimSpace(string(out)))
}

// Подсказка даты объясняла, что это за дата, вместо того чтобы показать её
// точнее («идиотская подпись», замечание пользователя): в ячейке стоит день, а
// часа не видно нигде. Сторожит стенд testdata/poc_datetip.mjs все четыре
// места, где дата стоит: строку доски, крошки экрана задачи, запись накопителя
// и строку сессии.
func TestStaticDateTips(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node не найден: стенд подсказок даты пропущен")
	}
	out, err := exec.Command(node, filepath.Join("testdata", "poc_datetip.mjs"),
		filepath.Join("static", "app.js")).CombinedOutput()
	if err != nil {
		t.Fatalf("подсказки даты: %v\n%s", err, out)
	}
	t.Log(strings.TrimSpace(string(out)))
}

// Заголовок у колонки хода сняли прошлым заходом, лишь бы ужать её с
// восьмидесяти точек. Человек это забраковал: «в сессиях ты вообще убрал
// название колонки Ход, от этого стало только хуже. Надо было просто сделать
// колонку компактнее сохранив возможность сортировки по ней. Например заменив
// заголовок иконкой и уменьшив отступы в колонке». Сторожит стенд
// testdata/poc_headico.mjs: заголовок значком, сортировка по нажатию жива,
// подсказка называет колонку словами, а ужата колонка отступом.
func TestStaticHeadIcon(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node не найден: стенд заголовка со значком пропущен")
	}
	out, err := exec.Command(node, filepath.Join("testdata", "poc_headico.mjs"),
		filepath.Join("static", "app.js")).CombinedOutput()
	if err != nil {
		t.Fatalf("заголовок колонки хода: %v\n%s", err, out)
	}
	t.Log(strings.TrimSpace(string(out)))
}

// Подсказок у ранга было две, и человек забраковал обе: «одна появляется
// непосредственно в строке сразу и наползает на контент следующей колонки, а
// вторая всплывающая, которая появляется через секунду. При этом обе подсказки
// плохие». Сторожит стенд testdata/poc_ranktip.mjs: ни строки слагаемых в самой
// ячейке, ни родной подсказки браузера, а вместо них один блок с пятью
// показателями RANKING.md и итогом, стоящий поверх строки.
func TestStaticRankTip(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node не найден: стенд подсказки ранга пропущен")
	}
	out, err := exec.Command(node, filepath.Join("testdata", "poc_ranktip.mjs"),
		filepath.Join("static", "app.js")).CombinedOutput()
	if err != nil {
		t.Fatalf("подсказка ранга: %v\n%s", err, out)
	}
	t.Log(strings.TrimSpace(string(out)))
}

// Три раздела доски собирались разными заходами и разошлись по виду: размеры
// значков, высота строки, отступы ячеек, кегль подписей, зазоры между кнопками
// («стиль отображения контента разный на всех табах, иконки даже везде разного
// размера», замечание пользователя). Величина складывается из правила раздела,
// общего правила таблицы и медиазапроса, и разбором стилей такое не берётся:
// меряется готовая раскладка, а сравниваются разделы между собой.
func TestBoardTabsSameLook(t *testing.T) {
	chrome := findChrome()
	if chrome == "" {
		t.Skip("движка нет: замер вида разделов пропущен")
	}
	dir, page := chromeStand(t, "tbl_fit.js", tblColsJSON(t))
	got := map[string]map[string]int{}
	for _, kind := range []string{"tasks", "sess", "drafts"} {
		got[kind] = chromeMeasure(t, chrome, dir, page, "1400,900", kind)
		t.Logf("вид раздела %s: %v", kind, look(got[kind]))
	}
	// Что обязано совпадать: отступы ячейки, величина кнопки-значка и самого
	// значка, зазор кнопок в хвосте, кегль заголовка и подписи, чип.
	for _, one := range []struct{ key, word string }{
		{"l_padin", "боковой отступ внутренней ячейки слева"},
		{"l_padinr", "боковой отступ внутренней ячейки справа"},
		{"l_padr", "боковой отступ последней ячейки"},
		{"l_padt", "верхний отступ ячейки"},
		{"l_padb", "нижний отступ ячейки"},
		{"l_align", "выравнивание ячейки по вертикали"},
		{"l_actgap", "зазор между кнопками в хвосте строки"},
		{"l_ico", "ширина кнопки-значка"},
		{"l_icoh", "высота кнопки-значка"},
		{"l_icosvg", "величина значка"},
		{"l_icorad", "скругление кнопки-значка"},
		{"l_ttlfs", "кегль заголовка строки"},
		{"l_ttlw", "насыщенность заголовка строки"},
		{"l_subfs", "кегль подписи"},
	} {
		want := got["tasks"][one.key]
		for _, kind := range []string{"sess", "drafts"} {
			if got[kind][one.key] != want {
				t.Errorf("%s: у задач %d, у раздела %s %d. Вид у трёх разделов один",
					one.word, want, kind, got[kind][one.key])
			}
		}
	}
	// Чип и его коробка это тот же чип и та же коробка во всех трёх разделах:
	// у сессий чипы стояли россыпью рядом с заголовком, без коробки и своим
	// зазором.
	for _, one := range []struct{ key, word string }{
		{"l_chiph", "высота чипа"}, {"l_chipfs", "кегль чипа"},
		{"l_chipgap", "зазор чипов в коробке"},
	} {
		want := got["tasks"][one.key]
		for _, kind := range []string{"sess", "drafts"} {
			if got[kind][one.key] != want {
				t.Errorf("%s: у задач %d, у раздела %s %d", one.word, want,
					kind, got[kind][one.key])
			}
		}
	}
}

// Своих чисел у раздела быть не должно: отступ ячейки, зазор ряда, кегль
// подписи и величина значка живут лестницей в :root, как живут там шаг формы и
// радиус. Пока число стояло прямо в правиле раздела, «свести вид» означало
// подогнать три правила на глаз, и они расходились от каждой правки.
func TestBoardTabsUseSharedSteps(t *testing.T) {
	css := readFile(t, filepath.Join("static", "style.css"))
	for _, name := range []string{"--rowpy:", "--rowpx:", "--rowgap:", "--actgap:",
		"--chipgap:", "--rowfs:", "--subfs:", "--icob:", "--icosvg:"} {
		if !strings.Contains(css, name) {
			t.Fatalf("в :root нет величины %s: лестницу вида строки завести забыли", name)
		}
	}
	// Правила разделов: строка доски, строка сессии, запись накопителя и их
	// ячейки. Величины вида в них обязаны приезжать переменной.
	// Имена классов сравниваются целиком, а не куском строки: `.trow` куском
	// ловит `.trow2`, а это чужая родня. Вторая строка живёт в ленте разговора,
	// её поля привязаны к нити слева, и лестница вида строки доски ей не указ
	// (краснота ветки poc-chat, где сторож начал ловить правила ленты).
	marks := []string{".trow", ".arow", ".dsrow", ".aacts", ".dtt", ".dimp", ".dwhen",
		".twhen", ".amoved", ".atime", ".racts", ".rchips", ".ab"}
	named := make([]*regexp.Regexp, 0, len(marks))
	for _, mark := range marks {
		named = append(named, regexp.MustCompile(regexp.QuoteMeta(mark)+`(?:[^0-9A-Za-z_-]|$)`))
	}
	watch := regexp.MustCompile(`(?:^|;)\s*(padding|padding-top|padding-bottom|padding-left|` +
		`padding-right|gap|row-gap|column-gap|font|font-size)\s*:\s*([^;}]+)`)
	num := regexp.MustCompile(`\d+(\.\d+)?px`)
	rules := regexp.MustCompile(`(?s)([^{}]+)\{([^{}]*)\}`)
	for _, rule := range rules.FindAllStringSubmatch(css, -1) {
		sel := strings.TrimSpace(rule[1])
		if strings.HasPrefix(sel, "@") || strings.Contains(sel, ":root") {
			continue
		}
		hit := false
		for _, re := range named {
			if re.MatchString(sel) {
				hit = true
				break
			}
		}
		if !hit {
			continue
		}
		for _, said := range watch.FindAllStringSubmatch(rule[2], -1) {
			if num.MatchString(said[2]) {
				t.Errorf("правило %q держит своё число: %s: %s. Величины вида строки "+
					"берутся лестницей :root, иначе разделы разъезжаются",
					sel, said[1], strings.TrimSpace(said[2]))
			}
		}
	}
}

// look отбирает из замера величины вида: в журнале рядом с ними ширины колонок
// только мешают.
func look(vals map[string]int) map[string]int {
	out := map[string]int{}
	for name, val := range vals {
		if strings.HasPrefix(name, "l_") {
			out[strings.TrimPrefix(name, "l_")] = val
		}
	}
	return out
}

// Сторож замера ширины сам обязан быть под сторожем: без него стенд узкой
// ширины мерил не тот экран и зеленел на любом разъезде. Полный Chrome на macOS
// окно уже пятисот точек не открывает, и просьба про 390 возвращалась замером
// пятисот (проверено на этой машине: заголовок стенда приезжал со screen=500).
func TestChromeMeasureChecksWindow(t *testing.T) {
	// Браузер отдал не ту ширину: замер обязан быть отвергнут словами про то,
	// чем это лечится.
	err := sameWindow("390,844", map[string]int{"screen": 500, "over": 0})
	if err == nil {
		t.Fatal("замер на окне 500 сошёл за замер на 390: сторож ширины молчит")
	}
	for _, want := range []string{"500", "390", "chrome-headless-shell"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("отказ не называет %q: %v", want, err)
		}
	}
	// Ширина сошлась: замер годен.
	if err := sameWindow("390,844", map[string]int{"screen": 390}); err != nil {
		t.Errorf("верный замер отвергнут: %v", err)
	}
	// Стенд про ширину ничего не говорит: судить его нечем и не за что.
	if err := sameWindow("390,844", map[string]int{"seam": 0}); err != nil {
		t.Errorf("замер без поля screen отвергнут: %v", err)
	}
}

// Кнопки в хвосте строки одинаковы во всех трёх разделах, и одинаковы они на
// телефоне тоже. Пользователь поймал обратное: «сейчас кнопки в задаче,
// сессии, черновике на мобилке разного размера и расположения, да и компоновка
// похоже отличается». Прежний заход свёл величины на широком экране, а
// телефонная раскладка осталась своя у каждого раздела: строка доски держала
// кнопку в 36 точек, запись накопителя в 30, а сессия растягивала свои во всю
// ширину и уносила отдельной полосой под строку.
//
// Разбором стилей это не берётся: размер кнопки складывается из правил кнопки,
// раскладки строки и того, куда жмётся её хвост. Меряет настоящий движок в той
// самой ширине, в какой человек это и увидел.
func TestBoardRowButtonsSameOnPhone(t *testing.T) {
	chrome := findChrome()
	if chrome == "" {
		t.Skip("движка нет: замер кнопок строки пропущен")
	}
	dir, page := chromeStand(t, "tbl_btns.js", tblColsJSON(t))
	kinds := []struct{ key, word string }{
		{"tasks", "задачи"}, {"sess", "сессии"}, {"drafts", "накопитель"},
	}
	got := map[string]map[string]int{}
	for _, kind := range kinds {
		one := chromeMeasure(t, chrome, dir, page, "390,844", kind.key)
		if one["btns"] == 0 {
			t.Fatalf("замер раздела «%s» не собрался: %v", kind.word, one)
		}
		t.Logf("раздел «%s» на 390: %v", kind.word, one)
		got[kind.key] = one
	}
	// Величины хвоста сверяются с разделом задач: он самый нагруженный, и
	// расходятся с ним остальные два.
	same := []struct{ field, word string }{
		{"btnwmin", "ширина кнопки"},
		{"btnhmin", "высота кнопки"},
		{"gap", "зазор между кнопками"},
		{"padl", "левый отступ колонки"},
		{"padr", "правый отступ колонки"},
		{"tail", "отступ хвоста от правого края строки"},
	}
	for _, kind := range kinds[1:] {
		for _, one := range same {
			if got[kind.key][one.field] != got["tasks"][one.field] {
				t.Errorf("на 390 у раздела «%s» %s %d точек, а у задач %d: величина обязана быть одна",
					kind.word, one.word, got[kind.key][one.field], got["tasks"][one.field])
			}
		}
	}
	for _, kind := range kinds {
		one := got[kind.key]
		// Кнопка значком квадратная: растянутая во всю ширину это другая
		// кнопка, и раздел с ней стоит не тем же видом, что соседний.
		if one["btnwmax"] != one["btnwmin"] || one["btnhmax"] != one["btnhmin"] {
			t.Errorf("в разделе «%s» кнопки разного размера в одной строке: ширина %d..%d, высота %d..%d",
				kind.word, one["btnwmin"], one["btnwmax"], one["btnhmin"], one["btnhmax"])
		}
		if one["btnwmax"] != one["btnhmax"] {
			t.Errorf("в разделе «%s» кнопка значком растянута: %dx%d вместо квадрата",
				kind.word, one["btnwmax"], one["btnhmax"])
		}
		// Палец: рубеж телефонной кнопки стоит по нижней границе, ниже неё в
		// кнопку не попадают.
		if one["btnhmax"] < 36 {
			t.Errorf("в разделе «%s» кнопка мельче пальца: %d точек", kind.word, one["btnhmax"])
		}
		// Хвост стоит в первой полосе строки, а не отдельной под ней: полоса
		// под строкой растила раздел сессий втрое против соседних.
		if one["top"]*2 > one["rowh"] {
			t.Errorf("в разделе «%s» хвост уехал отдельной полосой: верх хвоста в %d точках при строке в %d",
				kind.word, one["top"], one["rowh"])
		}
		// И не тянется во всю ширину строки: ряд значков это ряд значков.
		if one["actw"]*2 > one["roww"] {
			t.Errorf("в разделе «%s» хвост растянут на %d точек при строке в %d",
				kind.word, one["actw"], one["roww"])
		}
	}
}

// На нажатие отвечает одна кнопка, а не строка под ней. Пользователь поймал
// обратное: «при нажатии кнопок на строке задач происходит выделение блоков
// внутри серым контуром, которое сохраняется, и весь блок моргает однократно
// светлым голубым фоном; на нажатие должна реагировать только конкретная
// кнопка, а не весь блок».
//
// Красит строку тут не дашборд, а браузер: подсветка нажатия достаётся
// ближайшему нажимаемому предку, кольцо фокуса остаётся на кнопке после
// нажатия мышью, а наведение на телефоне разбирается из того же нажатия.
// Значения по умолчанию у всех трёх лежат в самом движке, и разбором стилей их
// не увидеть, поэтому меряет движок.
func TestBoardRowPressAnswersButton(t *testing.T) {
	chrome := findChrome()
	if chrome == "" {
		t.Skip("движка нет: замер отклика на нажатие пропущен")
	}
	dir, page := chromeStand(t, "tbl_tap.js")
	got := chromeMeasure(t, chrome, dir, page, "390,844", "tasks")
	t.Logf("отклик на нажатие: %v", got)
	for _, one := range []struct{ field, word string }{
		{"taptask", "строки доски"}, {"tapsess", "строки сессии"}, {"tapdraft", "записи накопителя"},
	} {
		if got[one.field] != 0 {
			t.Errorf("нажатие красит всю строку: подсветка %s непрозрачна на %d%%",
				one.word, got[one.field])
		}
	}
	if got["tapbtn"] != 0 {
		t.Errorf("подсветка браузера красит и саму кнопку на %d%%: отклик у неё свой", got["tapbtn"])
	}
	// Кольцо фокуса и отклик самой кнопки живут состояниями, нажать которые в
	// снятой разметке нечем: их сторожит разбор правил. Кольцо ходьбе с
	// клавиатуры остаётся, доступность тут не предмет правки.
	css := readFile(t, filepath.Join("static", "style.css"))
	for _, want := range []struct{ rule, why string }{
		{".btn:focus-visible{outline:", "у кнопки нет кольца фокуса для ходьбы с клавиатуры"},
		{".btn:focus:not(:focus-visible){outline:none}", "кольцо фокуса остаётся после нажатия мышью"},
		{".btn:not([disabled]):active{", "у самой кнопки нет отклика на нажатие"},
	} {
		if !strings.Contains(css, want.rule) {
			t.Errorf("%s: в стилях нет правила %s", want.why, want.rule)
		}
	}
}

// Воздух вокруг карточки списка: сверху отбивка полосы табов, справа поле
// страницы до панели разговора. Пользователь: «вертикальные отступы между
// главным меню и таблицей и таблицей и чатом нужно сократить вдвое». Зазор этот
// не одно правило, а сумма нескольких, и складывает её движок, поэтому меряется
// картинка, а не текст стилей. Прежние величины стояли по шестнадцать точек
// сбоку и по двенадцать с девятью сверху; половина от них и есть рубеж.
func TestBoardTableAirAroundTable(t *testing.T) {
	chrome := findChrome()
	if chrome == "" {
		t.Skip("движка для снимка нет")
	}
	dir, page := chromeStand(t, "tbl_air.js")
	for _, kind := range []string{"tasks", "sess", "drafts"} {
		got := chromeMeasure(t, chrome, dir, page, "1400,900", kind)
		if got["screen"] != 1400 {
			t.Fatalf("окно стенда не 1400 пикселей: %v", got)
		}
		// Нижняя граница разумности у воздуха вокруг таблицы та же, что внутри
		// ряда: соседи, отбитые теснее, чем ячейки одной строки, читаются
		// слипшимися.
		floor := got["rowgap"]
		if floor <= 0 {
			t.Fatalf("раздел %q: зазор внутри ряда не прочитался: %v", kind, got)
		}
		for _, c := range []struct {
			key  string
			max  int
			what string
		}{
			{"airright", 8, "поле между карточкой списка и панелью разговора"},
			{"barmt", 5, "отбивка полосы табов от шапки экрана"},
			{"tabpb", 5, "низ полосы табов над её чертой"},
		} {
			if got[c.key] > c.max {
				t.Errorf("раздел %q: %s занял %d точек при рубеже %d: воздух вокруг "+
					"таблицы снова прежний", kind, c.what, got[c.key], c.max)
			}
			if got[c.key] < floor {
				t.Errorf("раздел %q: %s ужат до %d точек при зазоре внутри ряда %d: "+
					"таблица липнет к соседу", kind, c.what, got[c.key], floor)
			}
		}
		// Верхний зазор целиком: отбивка полосы, её низ и черта. Двадцать две
		// точки было, одиннадцать это ровно половина.
		up := got["barmt"] + got["tabpb"] + got["barbb"] + got["tblmt"] + got["groupspt"]
		if up > 11 {
			t.Errorf("раздел %q: между полосой табов и карточкой списка %d точек "+
				"воздуха при рубеже 11: %v", kind, up, got)
		}
		if got["airtop"] < 0 {
			t.Errorf("раздел %q: карточка списка залезла на полосу табов на %d точек",
				kind, -got["airtop"])
		}
	}
	// Числа воздуха живут общими переменными, а не россыпью по местам: иначе
	// следующая правка ужмёт одно место из трёх и зазоры разъедутся.
	css := readFile(t, filepath.Join("static", "style.css"))
	for _, want := range []struct {
		rule string
		why  string
	}{
		{"--air:", "боковое поле экрана доски не заведено переменной"},
		{"--airy:", "вертикальный воздух вокруг карточки не заведён переменной"},
		{"padding:var(--airy) var(--air) 0", "поля экрана доски держат своё число"},
		{"margin-top:var(--airy)", "отбивка полосы табов держит своё число"},
		{"padding:0 2px var(--airy)", "низ полосы табов держит своё число"},
	} {
		if !strings.Contains(css, want.rule) {
			t.Errorf("%s: правила %s в стилях нет", want.why, want.rule)
		}
	}
}

// Кружок состояния стоит по центру ячейки во всех разделах, где он есть, и не
// вылезает за кромку строки на узком экране. Пользователь: «в разделе сессий
// кружок состояния хода стоит не по центру ячейки по вертикали». Кружок
// откреплён от потока, и середину ему считает движок из высоты ячейки, поэтому
// сторож меряет расстояния до обеих кромок, а не читает правило.
func TestBoardTableDotCentred(t *testing.T) {
	chrome := findChrome()
	if chrome == "" {
		t.Skip("движка для снимка нет")
	}
	dir, page := chromeStand(t, "tbl_dot.js")
	for _, kind := range []string{"tasks", "sess"} {
		got := chromeMeasure(t, chrome, dir, page, "1400,900", kind)
		if got["doth"] <= 0 {
			t.Fatalf("раздел %q: кружка состояния в строке нет: %v", kind, got)
		}
		// Точка допуска это округление до целого пикселя: при нечётной разнице
		// высот ячейки и кружка ровного пополам не бывает.
		off := got["top"] - got["bot"]
		if off < 0 {
			off = -off
		}
		if off > 1 {
			t.Errorf("раздел %q: кружок стоит на %d точек мимо центра ячейки "+
				"(сверху %d, снизу %d, ячейка %d)", kind, off, got["top"], got["bot"],
				got["cellh"])
		}
	}
	// Узкий экран: строка ложится сеткой, и кружок там вынесен в поле слева.
	// Вынесенный за кромку строки, он режется, а невидимое состояние это то же,
	// что состояние без признака.
	for _, kind := range []string{"tasks", "sess"} {
		got := chromeMeasure(t, chrome, dir, page, "390,844", kind)
		if got["screen"] != 390 {
			t.Fatalf("окно стенда не 390 пикселей: %v", got)
		}
		if got["outleft"] > 0 {
			t.Errorf("на экране 390 кружок раздела %q вынесен за кромку строки на %d "+
				"точек и режется", kind, got["outleft"])
		}
		if got["outlist"] > 0 {
			t.Errorf("на экране 390 кружок раздела %q вышел за кромку списка на %d точек",
				kind, got["outlist"])
		}
	}
	// Середина берётся сдвигом кружка, а не половиной его размера числом: кружки
	// в разделах разной величины, и одно число ставило один из них мимо центра.
	css := readFile(t, filepath.Join("static", "style.css"))
	if strings.Contains(css, "calc(50% - 4.5px)") {
		t.Error("кружок снова центрируется вписанной половиной своего размера: " +
			"правило calc(50% - 4.5px) вернулось в стили")
	}
}
