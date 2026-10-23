package taskform

import (
	"strings"
	"testing"
)

// regcheck:test-begin
//
// Раздел «Нарезка» документа LLD (DK-489): кандидаты читаются построчно,
// пометка цели покрывает раздел одним составом, а пометка-исключение гасит
// ворота тем же разбором, что и остальные.

// TestCutSectionReadsCandidates: раздел «Нарезка» разбирается по строкам
// списка, голова строки отличает заведённого кандидата с ID от незаведённого
// со словом «кандидат», а проза и таблицы в разбор не попадают (DK-489).
func TestCutSectionReadsCandidates(t *testing.T) {
	doc := "# DK-489: дизайн\n\n## Что происходит\n\nПроза раздела.\n\n" +
		"## Нарезка\n\nСтрок три, идут цепочкой.\n\n" +
		"- кандидат 1 (task, M, R=38). Файл набора шаблонов.\n" +
		"- кандидат 2 (task, S, R=35). Сборка плана. После кандидата 1.\n" +
		"- DK-1150 (task, M, R=38). Ворота читают этапы шаблона.\n" +
		"\n| Строка | Тип |\n| --- | --- |\n| таблица | task |\n\n" +
		"### Подраздел с прозой\n\nПродолжение.\n\n## DoD\n\nДругой раздел.\n"
	rows, goal, found := CutSection(doc)
	if !found {
		t.Fatal("раздел «Нарезка» не найден")
	}
	if goal != "" {
		t.Fatalf("пометка цели прилипла без строки цели: %q", goal)
	}
	if len(rows) != 3 {
		t.Fatalf("кандидатов %d, ожидалось 3: %+v", len(rows), rows)
	}
	if rows[0].TaskID != "" {
		t.Fatalf("незаведённый кандидат получил ID %q", rows[0].TaskID)
	}
	if rows[2].TaskID != "DK-1150" {
		t.Fatalf("заведённый кандидат без ID: %+v", rows[2])
	}
	if !strings.Contains(rows[1].Text, "После кандидата 1") {
		t.Fatalf("текст кандидата обрезан: %+v", rows[1])
	}
}

// TestCutSectionGoalMark: пометка цели читается строкой «- цель <ID>» и
// покрывает раздел одним составом, слово «цель» с чужим продолжением
// остаётся кандидатом.
func TestCutSectionGoalMark(t *testing.T) {
	doc := "## Нарезка\n\n- цель DK-490, кандидаты уехали в раздел Задачи цели файла цели.\n"
	rows, goal, found := CutSection(doc)
	if !found || goal != "DK-490" {
		t.Fatalf("пометка цели не прочиталась: found=%v goal=%q", found, goal)
	}
	if len(rows) != 0 {
		t.Fatalf("пометка попала в кандидаты: %+v", rows)
	}
	fake := "## Нарезка\n\n- цель обновления реестра сама по себе.\n"
	rows, goal, found = CutSection(fake)
	if !found || goal != "" || len(rows) != 1 {
		t.Fatalf("кандидат со словом «цель» в имени прочитался пометкой: found=%v goal=%q rows=%+v",
			found, goal, rows)
	}
}

// TestCutSectionMissingAndFenced: чужой заголовок разделом не считается,
// синоним «Предварительная нарезка реализации» у живой DK-1318 тем более, а
// строка кандидата внутри ограждения остаётся вложенным выводом.
func TestCutSectionMissingAndFenced(t *testing.T) {
	doc := "## Предварительная нарезка реализации\n\n- кандидат 1 (task, M). Шаг.\n"
	if rows, goal, found := CutSection(doc); found {
		t.Fatalf("чужой заголовок прочитался разделом: %+v goal=%q", rows, goal)
	}
	fenced := "## Нарезка\n\nПример списка:\n\n```\n- кандидат 1 (task, M). Шаг.\n```\n"
	if rows, goal, found := CutSection(fenced); found && (len(rows) > 0 || goal != "") {
		t.Fatalf("ограждённая строка попала в разбор: %+v goal=%q", rows, goal)
	}
	if rows, _, found := CutSection("## Нарезка\n\nПроза без списка.\n"); !found || len(rows) != 0 {
		t.Fatalf("пустой раздел: found=%v rows=%+v", found, rows)
	}
}

// TestCutExceptionMark: пометка-исключение гасит ворот нарезки тем же
// разбором, что и остальные ворота (DK-489).
func TestCutExceptionMark(t *testing.T) {
	doc := "# XR-003\n\n## Ход работы\n\n- Исключение: нарезка отложена (до пробы DK-1413)\n"
	if !Exception(doc, GateCut) {
		t.Fatal("пометка-исключение нарезки не прочиталась")
	}
	if Exception(doc, GateRehearsal) {
		t.Fatal("пометка нарезки погасила чужие ворота")
	}
}

// regcheck:test-end
