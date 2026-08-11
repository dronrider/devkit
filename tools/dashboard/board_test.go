package main

import (
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
		"ev.stopPropagation()", `"Стоп"`, `"В работу"`, "ведёт другая сессия"} {
		if !strings.Contains(body, want) {
			t.Errorf("в rowAction нет %q: действие со строки не доведено", want)
		}
	}
	if strings.Contains(body, "api(") {
		t.Error("rowAction ходит на сервер сам, мимо startRun и stopRun: ручка у запуска и стопа одна")
	}
	if !strings.Contains(funcBody(t, text, "function renderRow("), "rowAction(project, row, works)") {
		t.Error("строка доски рисуется без действия: за запуском снова придётся заходить внутрь задачи")
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

// Уйти на главную можно с любого экрана: кнопка в шапке стоит над всеми
// экранами, на телефоне то же место занимает нижняя вкладка, а сама главная
// это список проектов.
func TestStaticHomeFromEveryScreen(t *testing.T) {
	html := readFile(t, filepath.Join("static", "index.html"))
	for _, want := range []string{`id="gohome"`, `id="nav-home"`, `id="tab-home"`, "На главную", "Главная"} {
		if !strings.Contains(html, want) {
			t.Errorf("в static/index.html нет %q: перехода на главную с экрана нет", want)
		}
	}
	text := readFile(t, filepath.Join("static", "app.js"))
	for _, want := range []string{`"gohome", "nav-home", "tab-home"`, "function renderHome(", "home: true"} {
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
