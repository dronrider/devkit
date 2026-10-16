package main

import (
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
		case ".qrow.rtop":
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
