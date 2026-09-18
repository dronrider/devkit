package main

import (
	"path/filepath"
	"strings"
	"testing"
)

// Поправки к рангу (бонус за дешевизну и подтяжка итога от держимой задачи)
// доезжают до экрана задачи вместе со строкой. Прежде разбор ответа taskctl их
// выбрасывал: в типе строки не было ни своей суммы, ни хвоста, и форма
// складывала пять ручек сама. Одна и та же задача показывала 64 в списке и 38
// в форме, а источник подтяжки искали грепом по доске (DK-650).
func TestTaskRowCarriesRankAdjustments(t *testing.T) {
	e, c, _ := tasksEnv(t)
	// Подтяжка: держатель не может стоять в очереди ниже того, кого он держит,
	// и итог дорогой задачи переезжает на него целиком (DK-428). XR-004 держит
	// свежую XR-005, та дороже, и её 55 становятся итогом держателя.
	runTaskctl(t, e.proj, "add", "--id", "XR-005", "--title", "Дорогая",
		"--rank", "50+5+0+0+0", "--cost", "L", "--accept", "agent")
	runTaskctl(t, e.proj, "dep", "add", "XR-005", "XR-004")

	// Бонус за дешевизну: у XR-002 цена M, своя сумма 30, итог 31.
	bonus := getTask(t, c, e, "XR-002")
	if got := taskRowField(t, bonus, "r"); got != float64(31) {
		t.Errorf("итог XR-002 в ответе %v, ждал 31: столько же стоит в списке", got)
	}
	if got := taskRowField(t, bonus, "r_own"); got != float64(30) {
		t.Errorf("своя сумма XR-002 в ответе %v, ждал 30: без неё форме нечего "+
			"показать рядом с итогом", got)
	}
	adj := rowAdjustments(t, bonus)
	if len(adj) != 1 || adj[0]["name"] != "M" {
		t.Fatalf("хвост поправок XR-002 в ответе %v, ждал бонус за цену M", adj)
	}
	if adj[0]["delta"] != float64(1) {
		t.Errorf("дельта бонуса XR-002 в ответе %v, ждал 1", adj[0]["delta"])
	}

	// Подтяжка: своя сумма единица, итог чужой, и хвост называет, чей именно.
	pull := getTask(t, c, e, "XR-004")
	if got := taskRowField(t, pull, "r"); got != float64(55) {
		t.Errorf("итог XR-004 в ответе %v, ждал подтянутые 55", got)
	}
	if got := taskRowField(t, pull, "r_own"); got != float64(1) {
		t.Errorf("своя сумма XR-004 в ответе %v, ждал 1", got)
	}
	adj = rowAdjustments(t, pull)
	if len(adj) != 1 || adj[0]["from"] != "XR-005" {
		t.Fatalf("хвост поправок XR-004 в ответе %v, ждал подтяжку от XR-005: "+
			"без ID источника его ищут грепом по доске", adj)
	}
}

// rowAdjustments вынимает хвост поправок из ответа ручки задачи разобранным.
func rowAdjustments(t *testing.T, task map[string]any) []map[string]any {
	t.Helper()
	list, ok := taskRowField(t, task, "adjustments").([]any)
	if !ok {
		t.Fatalf("в строке ответа нет хвоста поправок: %v", task["row"])
	}
	out := []map[string]any{}
	for _, item := range list {
		one, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("поправка пришла не объектом: %v", item)
		}
		out = append(out, one)
	}
	return out
}

// Крупное число карточки ранга это итог строки, тот же, что в списке доски, а
// своя сумма пяти ручек стоит рядом мельче и пересчитывается на месте. Пока
// карточка складывала ручки сама, форма и список говорили о задаче с хвостом
// поправок разное, и оба числа стояли без объяснения (DK-650).
func TestStaticRankCardShowsRowTotal(t *testing.T) {
	app := readFile(t, filepath.Join("static", "app.js"))
	body := funcBody(t, app, "function formPage(")
	from := strings.Index(body, `const rank = el("div", "card rcard rfolded")`)
	to := strings.Index(body, `const grid = el("div", "tgrid")`)
	if from < 0 || to < from {
		t.Fatal("блок ранга в форме не нашёлся: смотреть крупное число негде")
	}
	card := body[from:to]
	if strings.Contains(card, "sum.textContent = String(form.parts.reduce") {
		t.Error("крупное число карточки снова складывается из пяти ручек: у строки " +
			"с хвостом поправок оно разойдётся с числом в списке")
	}
	for _, want := range []string{"cfg.rankRow", `typeof rankRow.r === "number"`,
		`sum.textContent = String(total)`, "своя сумма", "подтянут от",
		`el("button", "rfrom"`, "goKeepingChat(cfg.project"} {
		if !strings.Contains(card, want) {
			t.Errorf("в карточке ранга нет %q: итог строки и его разбор оттуда не прочесть", want)
		}
	}
	// Своя сумма живёт в том же обходе, что и поля правки: без пересчёта на
	// месте правка ручкой не видна до сохранения вовсе.
	if !strings.Contains(card, "form.parts.reduce") {
		t.Error("своя сумма в карточке не считается: правка ручкой перестала быть видна до сохранения")
	}
	if !strings.Contains(app, "rankRow: row") {
		t.Error("экран задачи не отдаёт форме строку доски: итог брать неоткуда")
	}
	// Та же разница видна и в подсказке ячейки списка: под слагаемыми на 38
	// стояло «Ранг 64», и подсказка объясняла ровно ничего.
	rows := funcBody(t, app, "function rankRows(")
	if !strings.Contains(rows, "row.r_own") || !strings.Contains(rows, "Своя сумма") {
		t.Error("разбор ранга для списка не знает своей суммы и поправок: подсказке нечего показать")
	}
	css := readFile(t, filepath.Join("static", "style.css"))
	if !strings.Contains(css, ".rcard .radj{") || !strings.Contains(css, ".rcard .rfrom{") {
		t.Error("у хвоста разбора нет своих стилей: он встанет строкой крупного итога")
	}
}

// Читалка экрана слышит тот же разбор ранга, что виден в подсказке. Оба текста
// собираются из одного помощника, и вторым списком слов aria-label не
// набирается: с ним читалка перечисляла пять слагаемых на 38 под итогом 64, а
// поправки называла одна подсказка (замечание ревью DK-650).
func TestStaticRankLabelSaysAdjustments(t *testing.T) {
	app := readFile(t, filepath.Join("static", "app.js"))
	rows := funcBody(t, app, "function rankRows(")
	for _, want := range []string{"RANK_PARTS.map", "row.r_own", "Своя сумма",
		"Подтянут от", `"Цена "`} {
		if !strings.Contains(rows, want) {
			t.Errorf("в общем разборе ранга нет %q: читалке и подсказке достанется разное", want)
		}
	}
	cell := funcBody(t, app, "function rankCell(")
	if !strings.Contains(cell, "const rows = rankRows(row, parts)") {
		t.Error("ячейка ранга не зовёт общий разбор: подсказка и aria-label разъедутся снова")
	}
	if !strings.Contains(cell, `const said = rows.map(`) ||
		!strings.Contains(cell, `sum.setAttribute("aria-label", "ранг " + row.r + ", слагаемые: " + said)`) {
		t.Error("aria-label кнопки ранга собран мимо общего разбора: у строки с хвостом " +
			"поправок читалка снова услышит слагаемые на 38 под итогом 64")
	}
	if !strings.Contains(cell, "for (const one of rows) tip.append(") {
		t.Error("подсказка ранга рисуется мимо общего разбора: у неё и у aria-label два источника")
	}
	for _, gone := range []string{"Своя сумма", "Подтянут от", "row.r_own"} {
		if strings.Contains(cell, gone) {
			t.Errorf("строка разбора %q снова живёт в самой ячейке: следующая поправка "+
				"приедет в подсказку и не приедет в aria-label", gone)
		}
	}
}
