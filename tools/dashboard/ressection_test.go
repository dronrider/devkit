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
	at := strings.Index(body, "padding:")
	if at < 0 {
		return ""
	}
	val := body[at+len("padding:"):]
	if end := strings.IndexAny(val, ";}"); end >= 0 {
		val = val[:end]
	}
	fields := strings.Fields(val)
	if len(fields) == 0 || fields[0] == "0" {
		return ""
	}
	return fields[0]
}
