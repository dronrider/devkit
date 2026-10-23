package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// regcheck:test-begin
//
// Регрессия DK-489: принятая LLD закрывалась с приёмкой человека, а кандидаты
// нарезки оставались текстом в документе. Ворота closeCutGate держат закрытие
// до заведения кандидатов строками или целью.

// lldCutEnv ставит XR-003 строкой с живым документом дизайна: файл задачи
// ссылается на docs/lld/XR-003-design.md, а текст дизайна передаётся тестом.
// Сценарий и отметка обкатки в файле задачи уже готовы, до ворот нарезки
// закрытие доходит без отказов.
func lldCutEnv(t *testing.T, design string) string {
	t.Helper()
	root := setup(t)
	if err := os.MkdirAll(filepath.Join(root, "docs", "lld"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "docs", "lld", "XR-003-design.md"),
		[]byte(design), 0o644); err != nil {
		t.Fatal(err)
	}
	doc := "# XR-003\n\nДокумент решений: [docs/lld/XR-003-design.md](../lld/XR-003-design.md).\n" +
		fixtureScenario + fixtureVerification
	if err := os.WriteFile(filepath.Join(root, "docs", "tasks", "XR-003.md"), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

// TestCloseLLDNeedsCutSection: регрессия DK-489. LLD закрывалась с приёмкой
// человека, а кандидаты нарезки оставались текстом в документе: у DK-459 так
// чуть не повисли восемь правок режима POC. Теперь закрытие строки с
// документом без раздела «Нарезка» отказывает и называет шаг.
func TestCloseLLDNeedsCutSection(t *testing.T) {
	root := lldCutEnv(t, "# XR-003: дизайн\n\n## Решение 1. Шаг\n\nСемь этапов спрятаны в решении.\n")
	_, err := cmdClose(root, CloseParams{ID: "XR-003", Date: "2026-10-22"})
	if err == nil {
		t.Fatal("close LLD без раздела «Нарезка» должен падать")
	}
	if !strings.Contains(err.Error(), "Нарезка") || !strings.Contains(err.Error(), "docs/lld/XR-003-design.md") {
		t.Fatalf("отказ не называет раздел и документ: %v", err)
	}
	if !onBoard(t, root, "XR-003") {
		t.Fatal("строка уехала с доски при отказе ворот")
	}
}

// TestCloseLLDHoldsUncutCandidates: кандидаты раздела без ID держат закрытие,
// отказ называет число незаведённых.
func TestCloseLLDHoldsUncutCandidates(t *testing.T) {
	root := lldCutEnv(t, "## Нарезка\n\n- кандидат 1 (task, M, R=38). Первая строка.\n"+
		"- кандидат 2 (task, S, R=35). Вторая строка. После кандидата 1.\n"+
		"- XR-002 (task, M, R=38). Уже заведённая.\n")
	_, err := cmdClose(root, CloseParams{ID: "XR-003", Date: "2026-10-22"})
	if err == nil {
		t.Fatal("close LLD с незаведёнными кандидатами должен падать")
	}
	if !strings.Contains(err.Error(), "2 из 3") {
		t.Fatalf("отказ не называет число незаведённых кандидатов: %v", err)
	}
}

// TestCloseLLDCutRowsPass: кандидаты с ID живых строк закрываются, строка LLD
// уезжает в архив.
func TestCloseLLDCutRowsPass(t *testing.T) {
	root := lldCutEnv(t, "## Нарезка\n\n- XR-002 (task, M, R=38). Первая строка.\n"+
		"- XR-004 (task, S, R=35). Вторая строка. После XR-002.\n")
	if _, err := cmdClose(root, CloseParams{ID: "XR-003", Date: "2026-10-22"}); err != nil {
		t.Fatalf("нарезанная LLD не закрылась: %v", err)
	}
	arch, err := LoadArchive(archivePath(root))
	if err != nil {
		t.Fatal(err)
	}
	if !arch.has("XR-003") {
		t.Fatal("строка LLD не уехала в архив")
	}
}

// TestCloseLLDCutGoalMark: пометка цели покрывает раздел одним составом, но
// ID обязан быть целью или закрытой строкой, обычная строка доски целью не
// считается.
func TestCloseLLDCutGoalMark(t *testing.T) {
	root := lldCutEnv(t, "## Нарезка\n\n- цель XR-002, кандидаты уехали в раздел Задачи цели файла цели.\n")
	_, err := cmdClose(root, CloseParams{ID: "XR-003", Date: "2026-10-22"})
	if err == nil {
		t.Fatal("пометка обычной строки целью должна падать")
	}
	if !strings.Contains(err.Error(), "целью не является") {
		t.Fatalf("отказ не называет причину: %v", err)
	}

	root = lldCutEnv(t, "## Нарезка\n\n- цель XR-100, кандидаты уехали в раздел Задачи цели файла цели.\n")
	if _, err := cmdAdd(root, AddParams{ID: "XR-100", Title: "Цель: довести дизайн до доски",
		Type: "task", Rank: "50+8+1+0+4", Cost: "XL", Accept: "agent"}); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdClose(root, CloseParams{ID: "XR-003", Date: "2026-10-22"}); err != nil {
		t.Fatalf("пометка живой цели держит закрытие: %v", err)
	}
}

// TestCloseLLDCutDeadIDFails: ID заведённого кандидата обязан существовать на
// доске или в архиве, опечатка держит закрытие.
func TestCloseLLDCutDeadIDFails(t *testing.T) {
	root := lldCutEnv(t, "## Нарезка\n\n- XR-999 (task, M, R=38). Опечатка в ID.\n")
	_, err := cmdClose(root, CloseParams{ID: "XR-003", Date: "2026-10-22"})
	if err == nil {
		t.Fatal("close с опечатанным ID кандидата должен падать")
	}
	if !strings.Contains(err.Error(), "XR-999") {
		t.Fatalf("отказ не называет мёртвый ID: %v", err)
	}
}

// TestCloseLLDCutExceptionPasses: отложенная решением дизайна нарезка гасит
// ворот пометкой-исключением, как у живой DK-1318 до пробы DK-1413.
func TestCloseLLDCutExceptionPasses(t *testing.T) {
	root := lldCutEnv(t, "## Нарезка\n\n- кандидат 1 (task, M, R=38). Ждёт пробы.\n")
	p := filepath.Join(root, "docs", "tasks", "XR-003.md")
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	body := string(data) + "- Исключение: нарезка отложена (нарезка едет после пробы DK-1413)\n"
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdClose(root, CloseParams{ID: "XR-003", Date: "2026-10-22"}); err != nil {
		t.Fatalf("пометка отложенной нарезки не погасила ворот: %v", err)
	}
}

// TestCloseLLDWithoutDesignPasses: строка без документа дизайна воротами
// нарезки не держится, как не держалась и раньше: отсутствие самого дизайна
// держат ворота заведения, а не закрытия.
func TestCloseLLDWithoutDesignPasses(t *testing.T) {
	root := setup(t)
	if _, err := cmdClose(root, CloseParams{ID: "XR-003", Date: "2026-10-22"}); err != nil {
		t.Fatalf("LLD без документа держит закрытие: %v", err)
	}
}

// regcheck:test-end
