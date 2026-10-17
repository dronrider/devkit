package main

import (
	"regexp"
	"strings"
	"testing"
)

// Раздел ресурсов после возврата 2026-10-16: одна таблица владельцев вместо
// двух блоков, повторявших друг друга строем, один топ по памяти со ссылкой
// в разговор на имени, плотные строки и число живых сессий рядом со средней
// нагрузкой. Разметку собирает клиент, поэтому проверка читает его текст и
// правила стилей: ушедшее не должно возвращаться молча.
func TestStaticResSectionAfterRework(t *testing.T) {
	js := readStatic(t, "app.js")
	if strings.Contains(js, "view.top_cpu") {
		t.Error("клиент читает топ по процессору: возврат просил один топ, по памяти")
	}
	if !strings.Contains(js, "resTop(view.top_mem)") {
		t.Error("топ раздела не собирается по памяти")
	}
	if got := strings.Count(js, "dual.append(resBar("); got != 2 {
		t.Errorf("полосок в строке владельца %d, жду две: процессор и память", got)
	}
	if !strings.Contains(js, "живых сессий ") {
		t.Error("число живых сессий не показывается")
	}
	if !strings.Contains(js, "r.proj && r.proj !== at ? r.proj + CHAT_PROJ_SEP + r.id : r.id") {
		t.Error("имя строки топа не открывает разговор чужой доски адресом с проектом")
	}
	var compact, dual bool
	for _, rule := range cssRules(readStatic(t, "style.css")) {
		switch rule[0] {
		case ".qrow.rtopmem":
			compact = strings.Contains(rule[1], "margin-bottom:4px")
		case ".rdual":
			dual = strings.Contains(rule[1], "display:grid")
		}
	}
	if !compact {
		t.Error("строки топа не уплотнены: без сжатого отступа список снова раздувается")
	}
	if !dual {
		t.Error("пара полосок владельца не собрана решёткой: строки наезжают друг на друга")
	}
}

// Возврат 2026-10-17: список сессий раздела раздулся по высоте, потому что
// строки владельцев несли класс rrow, занятый строкой ранговой карточки, и её
// правило из глубины style.css догоняло раздел паддингами с рамкой; топ
// деляли правила шапки ранга rtop. Проверка держит границу: классы строк
// раздела читаются из клиента, и ни одно правило стилей не приносит им
// вертикального отступа или рамки.
func TestStaticResRowsOwnClasses(t *testing.T) {
	js := readStatic(t, "app.js")
	rowCls := regexp.MustCompile(`const row = el\("div", "([^"]+)"\);`)
	var classes []string
	for _, head := range []string{"function resOwners(view) {", "function resTop(rows) {"} {
		m := rowCls.FindStringSubmatch(jsBody(js, head))
		if m == nil {
			t.Fatalf("строка раздела не собирается в %s", head)
		}
		classes = append(classes, strings.Fields(m[1])...)
	}
	for _, cls := range classes {
		for _, rule := range cssRules(readStatic(t, "style.css")) {
			if !cssSubject(rule[0], cls) {
				continue
			}
			if pad := verticalPad(rule[1]); pad != "" {
				t.Errorf("класс %s строки раздела получает вертикальный отступ %s от правила %s: коллизия с чужим блоком раздувает список", cls, pad, rule[0])
			}
			if strings.Contains(rule[1], "border-top") || strings.Contains(rule[1], "border-bottom") {
				t.Errorf("класс %s строки раздела получает рамку от правила %s: коллизия с чужим блоком раздувает список", cls, rule[0])
			}
		}
	}
}

// verticalPad называет ненулевую вертикальную часть паддинга правила: у
// «padding:8px 14px 8px 18px» это 8px, у «padding:0» и правил без паддинга
// пустая строка.
func verticalPad(body string) string {
	fields := strings.Fields(paddingValue(body))
	if len(fields) == 0 || fields[0] == "0" {
		return ""
	}
	return fields[0]
}

// paddingValue вынимает значение паддинга правила целиком: у
// «padding:12px 10px 7px» это сама тройка, у правил без паддинга пустая
// строка.
func paddingValue(body string) string {
	at := strings.Index(body, "padding:")
	if at < 0 {
		return ""
	}
	val := body[at+len("padding:"):]
	if end := strings.IndexAny(val, ";}"); end >= 0 {
		val = val[:end]
	}
	return strings.TrimSpace(val)
}

// Возврат 2026-10-17: разделы колонки раздулись отступами, потому что шапки
// несли класс shead, занятый заголовком секции таблицы задач, и его позднее
// правило каскадом приносило шапкам чужие паддинги с кеглем. Проверка держит
// границу: классы шапок читаются из разметки колонки, и кроме компактной
// базы и квотной подложки ни одно правило с паддингом их не касается.
func TestStaticColumnSectHeadsOwnClass(t *testing.T) {
	html := readStatic(t, "index.html")
	if !strings.Contains(readStatic(t, "app.js"), `el("div", "shead", sec.title)`) {
		t.Fatal("заголовок секции таблицы задач собирается не по классу shead: разбор смотрит не туда")
	}
	headRe := regexp.MustCompile(`<button class="([^"]+)" id="(?:projects|res|quota)-head">`)
	heads := headRe.FindAllStringSubmatch(html, -1)
	if len(heads) != 3 {
		t.Fatalf("шапок разделов колонки нашлось %d: разбор смотрит не туда", len(heads))
	}
	allowed := map[string]bool{
		"12px 10px 7px": true,
		"14px 10px 2px": true,
	}
	rules := cssRules(readStatic(t, "style.css"))
	for _, m := range heads {
		for _, cls := range strings.Fields(m[1]) {
			if cls == "shead" {
				t.Errorf("шапка раздела несёт класс shead таблицы задач: позднее правило каскадом раздувает её отступами")
			}
			var base, quota bool
			for _, rule := range rules {
				if !cssSubject(rule[0], cls) {
					continue
				}
				pad := paddingValue(rule[1])
				if pad == "" {
					continue
				}
				if !allowed[pad] {
					t.Errorf("класс %s шапки раздела получает паддинг «%s» от правила %s: коллизия с чужим блоком раздувает раздел", cls, pad, rule[0])
				}
				if rule[0] == "."+cls {
					base = true
				}
				if rule[0] == ".squota ."+cls {
					quota = true
				}
			}
			if !base || !quota {
				t.Errorf("у класса %s нет компактной базы или квотной подложки: шапки разделов остались без правил", cls)
			}
		}
	}
}

// Возврат 2026-10-17: сводка ресурсов была громоздкой, со словами «процессор»
// и «память» и скобками гигабайтов, и стояла дважды, в заголовке раздела и в
// теле. Проверка держит новую границу: слова заменены значками из шаблона,
// память идёт мегабайтами занятого и свободного через разделитель, сводка
// живёт только в теле раздела.
func TestStaticResSummaryIcons(t *testing.T) {
	js := readStatic(t, "app.js")
	html := readStatic(t, "index.html")
	owners := jsBody(js, "function resOwners(view) {")
	if owners == "" {
		t.Fatal("таблица владельцев не собирается: разбор смотрит не туда")
	}
	for _, piece := range []string{`icon("i-cpu")`, `icon("i-ram")`,
		`resMB(mem.used_kb) + "/" + resMB(mem.total_kb - mem.used_kb)`} {
		if !strings.Contains(owners, piece) {
			t.Errorf("сводка не несёт %s: значки и пара мегабайт обязаны стоять в теле раздела", piece)
		}
	}
	if strings.Contains(owners, "процессор") || strings.Contains(owners, " из ") {
		t.Error("сводка всё ещё пишет слова или гигабайты в скобках")
	}
	if strings.Contains(js, "res-brief") || strings.Contains(html, "res-brief") {
		t.Error("сводка дублируется строкой заголовка раздела: проценты машины видны кольцами у иконки")
	}
	if !strings.Contains(html, `data-ico="i-cpu"`) || !strings.Contains(html, `data-ico="i-ram"`) {
		t.Error("значки процессора и памяти не лежат в шаблоне icons")
	}
	var sized bool
	for _, rule := range cssRules(readStatic(t, "style.css")) {
		if rule[0] == ".rsum svg" && strings.Contains(rule[1], "width:") {
			sized = true
		}
	}
	if !sized {
		t.Error("значкам сводки не задан размер: svg без ширины браузер рисует 300 на 150")
	}
}

// Возврат 2026-10-17: раздел подписок переименован из «Квота подписок».
// Проверка держит новое имя в шапке колонки и в карточке на главной.
func TestStaticQuotaSectionTitle(t *testing.T) {
	for _, s := range [][2]string{
		{"index.html", readStatic(t, "index.html")},
		{"app.js", readStatic(t, "app.js")},
	} {
		if strings.Contains(s[1], "Квота подписок") {
			t.Errorf("%s всё ещё называет раздел «Квота подписок»", s[0])
		}
	}
	if !strings.Contains(readStatic(t, "index.html"), "<span>Подписки</span>") {
		t.Error("шапка раздела подписок не названа «Подписки»")
	}
	if !strings.Contains(readStatic(t, "app.js"), `el("h4", "", "Подписки")`) {
		t.Error("карточка подписок на главной не названа «Подписки»")
	}
}
