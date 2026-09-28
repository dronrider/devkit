package main

import (
	"encoding/json"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestBoardRunsKeepsTaskctlStage: строка этапа приходит готовой из taskctl
// list --json, и разметка строк дашборда её не трогает: ни гасит у строки без
// работы, ни считает заново (DK-910). Брошенную строку называет само поле.
func TestBoardRunsKeepsTaskctlStage(t *testing.T) {
	raw := json.RawMessage(`{"prefix":"XR","sections":[{"key":"in-progress","rows":[
		{"id":"XR-001","title":"Живая","sect":"in-progress","stage":"ревью","stage_since":1755252000,"stage_round":2,"stage_age":"12 минут","stage_session":"сессия жива"},
		{"id":"XR-002","title":"Оборванная","sect":"in-progress","stage":"разработка","stage_since":1755234000,"stage_round":1,"stage_age":"5 часов","stage_session":"сессии нет, брошена"},
		{"id":"XR-003","title":"Без этапа","sect":"in-progress"},
		{"id":"XR-004","title":"Ждёт","sect":"in-progress","stage":"ждёт человека","stage_since":1755248400,"stage_age":"3 часа","stage_at":"проверка"}]}]}`)
	works := []Work{{ID: "XR-001", Kind: "task", Via: "tmux"}}
	var doc struct {
		Sections []struct {
			Rows []boardRow `json:"rows"`
		} `json:"sections"`
	}
	if err := json.Unmarshal(boardRuns(raw, works, nil, nil), &doc); err != nil {
		t.Fatal(err)
	}
	rows := map[string]boardRow{}
	for _, sec := range doc.Sections {
		for _, row := range sec.Rows {
			rows[row.ID] = row
		}
	}
	live := rows["XR-001"]
	if live.Run != "tmux" || live.Stage != "ревью" || live.StageSince != 1755252000 || live.StageRound != 2 || live.StageSession != "сессия жива" {
		t.Fatalf("живая строка: %+v", live)
	}
	gone := rows["XR-002"]
	if gone.Run != "" || gone.Stage != "разработка" || gone.StageAge != "5 часов" || gone.StageSession != "сессии нет, брошена" {
		t.Fatalf("брошенная строка: %+v", gone)
	}
	if bare := rows["XR-003"]; bare.Stage != "" || bare.StageSession != "" {
		t.Fatalf("строка без этапа получила поля: %+v", bare)
	}
	// Ожидание не несёт своей сессии, а stage_at называет этап работы, на
	// котором задача встала (DK-1119): его лента строки списка и подсвечивает
	// вместо собственного деления, которого у ожиданий нет.
	wait := rows["XR-004"]
	if wait.Stage != "ждёт человека" || wait.StageAt != "проверка" || wait.StageSession != "" {
		t.Fatalf("ожидание: %+v", wait)
	}
}

// TestStaticStageMark: колонка хода строки списка и шапка со степпером формы
// (DK-1119, макет «14 Этап задачи, ход 2», варианты 2a и 2b) различают четыре
// состояния (живая сессия, молчащая, брошенная, ожидание) цветом словаря и
// подсказкой, без приписки слов о сессии видимым текстом; строка без записи
// этапа колонку не ломает. Сторожит стенд testdata/poc_stagemark.mjs.
func TestStaticStageMark(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node не найден: стенд этапа задачи пропущен")
	}
	out, err := exec.Command(node, filepath.Join("testdata", "poc_stagemark.mjs"),
		filepath.Join("static", "app.js")).CombinedOutput()
	if err != nil {
		t.Fatalf("этап задачи в строке и на форме: %v\n%s", err, out)
	}
	t.Log(strings.TrimSpace(string(out)))
}

// TestStaticStageColorHasInk: у всякого класса цвета этапа, который ставит
// разметка, в стилях есть своё правило --k. Класс без правила оставляет
// переменную пустой, и покрашенное ею просто пропадает: сегмент «проверка» в
// степпере формы стоял неокрашенным у строки, которую ждёт человек (класс
// k-you, замечание 1 приёмки второго круга). Разбором стенда такое не берётся:
// стенд смотрит на имя класса, а цвет приходит из стилей.
func TestStaticStageColorHasInk(t *testing.T) {
	app := readFile(t, filepath.Join("static", "app.js"))
	css := readFile(t, filepath.Join("static", "style.css"))
	seen := map[string]bool{}
	for _, hit := range regexp.MustCompile(`"(k-[a-z]+)"`).FindAllStringSubmatch(app, -1) {
		seen[hit[1]] = true
	}
	if len(seen) == 0 {
		t.Fatal("в static/app.js нет ни одного класса цвета этапа: разметка переехала, сторож ослеп")
	}
	for name := range seen {
		if !strings.Contains(css, "."+name+"{--k:") {
			t.Errorf("класс цвета %s стоит в разметке, а правила .%s{--k:...} в стилях нет: "+
				"слово, точка и деление ленты останутся неокрашенными", name, name)
		}
	}
}

// TestStaticStageGlassIcon: пометка машинного ожидания рисуется значком
// разметки, а не рамками в стилях. Рамкой часы выходили коробкой с чертой
// посередине и на песочные часы не походили вовсе («значок ожидания не похож
// на часы», замечание 5 приёмки второго круга). Значок лежит там же, где
// прочие значки экрана, и зовётся из разметки строки и шапки формы.
func TestStaticStageGlassIcon(t *testing.T) {
	html := readFile(t, filepath.Join("static", "index.html"))
	if !strings.Contains(html, `<svg data-ico="i-glass"`) {
		t.Error("в наборе значков нет i-glass: песочные часы рисовать нечем")
	}
	app := readFile(t, filepath.Join("static", "app.js"))
	if !strings.Contains(app, `icon("i-glass")`) {
		t.Error("пометка ожидания не берёт значок часов из набора разметки")
	}
	css := readFile(t, filepath.Join("static", "style.css"))
	for _, gone := range []string{".act2 b .hg:after", ".now2 .hgf:after"} {
		if strings.Contains(css, gone) {
			t.Errorf("часы снова рисуются рамкой в стилях (%s): фигура выходит коробкой", gone)
		}
	}
	// Значок берёт размер строки, а не сам себе: без правила он встал бы во
	// всю ширину коробки значка.
	for _, want := range []string{".act2 b .hg .gico{", ".now2 .hgf .gico{"} {
		if !strings.Contains(css, want) {
			t.Errorf("значку часов не задан размер (%s)", want)
		}
	}
}

// TestStaticStageMarkTap: пометка ожидания открывает подсказку нажатием.
// Родная подсказка браузера живёт наведением, а на телефоне наведения нет, и
// пометка стояла там немой (замечание 3 приёмки второго круга). Цель касания
// растит пустой слой поверх значка, и меньше 24 точек она быть не должна.
func TestStaticStageMarkTap(t *testing.T) {
	css := readFile(t, filepath.Join("static", "style.css"))
	rule := ""
	if at := strings.Index(css, ".mtip>button:after{"); at >= 0 {
		if end := strings.Index(css[at:], "}"); end > 0 {
			rule = css[at : at+end]
		}
	}
	if rule == "" {
		t.Fatal("у пометки ожидания нет слоя касания: пальцем в значок не попасть")
	}
	for _, want := range []string{"width:max(100%,24px)", "height:24px"} {
		if !strings.Contains(rule, want) {
			t.Errorf("цель касания пометки меньше 24 точек: в слое нет %s (%s)", want, rule)
		}
	}
	if !strings.Contains(css, ".mtip.on .mtipbox") {
		t.Error("нажатие на пометку ничего не открывает: правила .mtip.on в стилях нет")
	}
}
