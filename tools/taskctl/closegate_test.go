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

// mergedDoc кладёт файл задачи с готовым сценарием, «Проверкой» и разделом
// «Выкат» из данных строк записи: слияние пишется как его пишет shipctl
// (tools/shipctl/record.go), строки идут в порядке кругов работы.
func mergedDoc(id string, record ...string) string {
	return "# " + id + "\n" + fixtureScenario + "\n## Выкат\n\n" +
		strings.Join(record, "\n") + "\n" + fixtureVerification
}

// TestCloseNeedsSmoke: регрессия DK-1363. Сверку отметки smoke с последним
// выкатом делал только отбор closable, а ручной close уносил строку Check с
// непроверенным выкатом в архив, и сломанный прод оставался за закрытой
// строкой. Отказ называет команды прогона и отметки, строка остаётся на доске.
func TestCloseNeedsSmoke(t *testing.T) {
	root := setup(t)
	writeTask(t, root, "XR-005", mergedDoc("XR-005", "- 2026-10-12 слито: 1a2b3c4d5e"))
	if _, err := cmdMove(root, "XR-005", SectCheck, "", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdDebtDone(root, "XR-005", DebtDoneParams{Note: "пересечений нет"}); err != nil {
		t.Fatal(err)
	}
	_, err := cmdClose(root, CloseParams{ID: "XR-005", Date: "2026-10-23"})
	if err == nil {
		t.Fatal("close с непроверенным выкатом должен падать")
	}
	for _, want := range []string{"agentctl stage XR-005 проверка", "shipctl smoke XR-005"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("в отказе нет команды %q: %v", want, err)
		}
	}
	if !onBoard(t, root, "XR-005") {
		t.Fatal("строка уехала с доски при отказе ворот")
	}
}

// TestCloseSmokeCoverPasses: отметка smoke, стоящая после последнего слияния,
// открывает закрытие, выкат проверен и строка уезжает с доски.
func TestCloseSmokeCoverPasses(t *testing.T) {
	root := setup(t)
	writeTask(t, root, "XR-005", mergedDoc("XR-005",
		"- 2026-10-12 слито: 1a2b3c4d5e",
		"- smoke прогнан, 2026-10-13"))
	if _, err := cmdMove(root, "XR-005", SectCheck, "", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdDebtDone(root, "XR-005", DebtDoneParams{Note: "пересечений нет"}); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdClose(root, CloseParams{ID: "XR-005", Date: "2026-10-23"}); err != nil {
		t.Fatalf("действующая отметка smoke ворот держать не должна: %v", err)
	}
	if onBoard(t, root, "XR-005") {
		t.Fatal("строка с проверенным выкатом не закрылась")
	}
}

// TestCloseStaleSmokeRefused: круг доработки после возврата из Check дописывает
// новое слияние, и отметка прошлого круга новый выкат не прикрывает: до новой
// отметки закрытие отказывает.
func TestCloseStaleSmokeRefused(t *testing.T) {
	root := setup(t)
	writeTask(t, root, "XR-005", mergedDoc("XR-005",
		"- 2026-10-10 слито: 1a2b3c4d5e",
		"- smoke прогнан, 2026-10-11",
		"- 2026-10-20 слито: 9f8e7d6c5b"))
	if _, err := cmdMove(root, "XR-005", SectCheck, "", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdDebtDone(root, "XR-005", DebtDoneParams{Note: "пересечений нет"}); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdClose(root, CloseParams{ID: "XR-005", Date: "2026-10-23"}); err == nil {
		t.Fatal("отметка прошлого круга не должна прикрывать новый выкат")
	}
	if !onBoard(t, root, "XR-005") {
		t.Fatal("строка уехала с доски при отказе ворот")
	}
}

// TestCloseWithoutDeployPasses: строки без раздела «Выкат» (LLD, дока, разбор)
// закрываются как раньше, ворот smoke они не касаются.
func TestCloseWithoutDeployPasses(t *testing.T) {
	root := setup(t)
	writeTask(t, root, "XR-005", "# XR-005\n"+fixtureScenario+fixtureVerification)
	if _, err := cmdMove(root, "XR-005", SectCheck, "", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdDebtDone(root, "XR-005", DebtDoneParams{Note: "пересечений нет"}); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdClose(root, CloseParams{ID: "XR-005", Date: "2026-10-23"}); err != nil {
		t.Fatalf("строка без выката обязана закрываться: %v", err)
	}
	if onBoard(t, root, "XR-005") {
		t.Fatal("строка без выката не закрылась")
	}
}
