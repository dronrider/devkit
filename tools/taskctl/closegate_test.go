package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// closeReady кладёт задаче файл без отметки обкатки: сценарий на месте, раздел
// «Проверка» непустой, и до DK-685 такого файла закрытию хватало.
func closeReady(t *testing.T, root, id string) {
	t.Helper()
	doc := "# " + id + "\n" + fixtureScenario + "\n## Проверка\n\n- прогон пройден, вывод вложен.\n"
	if err := os.WriteFile(filepath.Join(root, "docs", "tasks", id+".md"), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
}

// onBoard отвечает, осталась ли строка на доске: отказавший close не имеет
// права увезти её в архив.
func onBoard(t *testing.T, root, id string) bool {
	t.Helper()
	b, err := LoadBoard(boardPath(root))
	if err != nil {
		t.Fatal(err)
	}
	return b.find(id) != nil
}

// TestCloseNeedsRehearsal: регрессия DK-685. Ворот обкатки стоял только на
// move check, и закрытие из In progress уносило в архив задачу, чей сценарий
// никто не гонял. Отказ называет ту же команду, что и перевод в Check, а
// строка остаётся на доске.
func TestCloseNeedsRehearsal(t *testing.T) {
	root := setup(t)
	closeReady(t, root, "XR-005")
	_, err := cmdClose(root, CloseParams{ID: "XR-005", Date: "2026-09-24"})
	if err == nil {
		t.Fatal("close агентской задачи без обкатки должен падать")
	}
	if !strings.Contains(err.Error(), "taskctl rehearse XR-005") {
		t.Fatalf("отказ не называет команду обкатки: %v", err)
	}
	if !onBoard(t, root, "XR-005") {
		t.Fatal("строка уехала с доски при отказе ворот")
	}
}

// TestCloseRehearsalException: пометка-исключение гасит ворот закрытия тем же
// порядком, что и ворот перевода в Check.
func TestCloseRehearsalException(t *testing.T) {
	root := setup(t)
	closeReady(t, root, "XR-005")
	p := filepath.Join(root, "docs", "tasks", "XR-005.md")
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	body := string(data) + "\n- Исключение: обкатка (шаги гоняются только на проде)\n"
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdClose(root, CloseParams{ID: "XR-005", Date: "2026-09-24"}); err != nil {
		t.Fatalf("пометка-исключение ворот не погасила: %v", err)
	}
}

// TestCloseNeedsScenario: закрытие без раздела «Сценарий проверки» отбивается,
// как отбивался перевод в Check. До DK-685 такая задача уезжала в архив без
// способа себя проверить.
func TestCloseNeedsScenario(t *testing.T) {
	root := setup(t)
	dropScenario(t, root, "XR-005")
	_, err := cmdClose(root, CloseParams{ID: "XR-005", Date: "2026-09-24"})
	if err == nil {
		t.Fatal("close без сценария должен падать")
	}
	if !strings.Contains(err.Error(), "Сценарий проверки") {
		t.Fatalf("отказ не называет причину: %v", err)
	}
	if !onBoard(t, root, "XR-005") {
		t.Fatal("строка уехала с доски при отказе ворот")
	}
}

// TestCloseNeedsAcceptance: у неагентского вида закрытие спрашивает раздел
// «Приёмка» с перебором обходов, тот же, что спрашивает move check.
func TestCloseNeedsAcceptance(t *testing.T) {
	root := setup(t)
	if _, err := cmdAdd(root, AddParams{ID: "XR-100", Title: "С видом", Type: "task",
		Rank: "0+1+1+0+1", Accept: "user", Barrier: "событие"}); err != nil {
		t.Fatal(err)
	}
	doc := "# XR-100: С видом\n" + fixtureScenario
	if err := os.WriteFile(filepath.Join(root, "docs", "tasks", "XR-100.md"), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := cmdClose(root, CloseParams{ID: "XR-100", Date: "2026-09-24"})
	if err == nil {
		t.Fatal("close user-задачи без «Приёмки» должен падать")
	}
	if !strings.Contains(err.Error(), "Приёмка") {
		t.Fatalf("отказ не называет раздел: %v", err)
	}
	if !onBoard(t, root, "XR-100") {
		t.Fatal("строка уехала с доски при отказе ворот")
	}
}
