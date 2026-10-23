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

// gitCommitFiles коммитит в тестовом репозитории перечисленные файлы и
// возвращает сокращённый sha: запись «Выката» держит именно такие, и состав
// коммита читается по нему тем же git show, каким читает его ворота.
func gitCommitFiles(t *testing.T, root, msg string, files map[string]string) string {
	t.Helper()
	for rel, body := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		gitOut(t, root, "add", "--", rel)
	}
	gitOut(t, root, "commit", "-q", "-m", msg)
	return gitOut(t, root, "rev-parse", "--short", "HEAD")
}

// closeThroughCheck проводит строку через ворота перевода и долга: close
// спрашивает их перед воротами smoke, и без этого тест мерил бы чужой отказ.
func closeThroughCheck(t *testing.T, root, id string) {
	t.Helper()
	if _, err := cmdMove(root, id, SectCheck, "", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdDebtDone(root, id, DebtDoneParams{Note: "пересечений нет"}); err != nil {
		t.Fatal(err)
	}
}

// TestCloseDocsMergeWithoutSmoke: регрессия DK-1424. Слияние одной
// документации прод не меняет, и очередь выката такой задачи не видит
// (codeCommits в shipctl), а close требовал отметку smoke на ту же запись:
// проверенная агентская дока не закрывалась ни вручную, ни автоматикой.
// Теперь docs/-слияние закрывается без отметки, запись слияния сохраняется.
func TestCloseDocsMergeWithoutSmoke(t *testing.T) {
	root := setup(t)
	gitSetup(t, root)
	sha := gitCommitFiles(t, root, "docs(lld): XR-005 схема",
		map[string]string{"docs/lld/xr-005.md": "схема\n"})
	writeTask(t, root, "XR-005", mergedDoc("XR-005", "- 2026-10-23 слито: "+sha))
	markRehearsed(t, root)
	closeThroughCheck(t, root, "XR-005")
	if _, err := cmdClose(root, CloseParams{ID: "XR-005", Date: "2026-10-23"}); err != nil {
		t.Fatalf("слияние одной документации не выкат, отметка не нужна: %v", err)
	}
	if onBoard(t, root, "XR-005") {
		t.Fatal("строка с docs-слиянием не закрылась")
	}
	data, err := os.ReadFile(filepath.Join(root, "docs", "tasks", "archive", "2026", "XR-005.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "слито: "+sha) {
		t.Fatal("запись слияния не сохранилась в архивном файле")
	}
}

// TestCloseMixedMergeNeedsSmoke: код в записи слияния рядом с документацией
// делает слияние выкатом, и без отметки smoke закрытие отказывает: различение
// проходит по составу коммитов, а не по доле доки в них.
func TestCloseMixedMergeNeedsSmoke(t *testing.T) {
	root := setup(t)
	gitSetup(t, root)
	docs := gitCommitFiles(t, root, "docs(lld): XR-005 схема",
		map[string]string{"docs/lld/xr-005.md": "схема\n"})
	code := gitCommitFiles(t, root, "feat: XR-005 правка",
		map[string]string{"tools/x/main.go": "package main\n"})
	writeTask(t, root, "XR-005", mergedDoc("XR-005",
		"- 2026-10-23 слито: "+docs+", "+code))
	markRehearsed(t, root)
	closeThroughCheck(t, root, "XR-005")
	if _, err := cmdClose(root, CloseParams{ID: "XR-005", Date: "2026-10-23"}); err == nil {
		t.Fatal("коммит с кодом в записи делает слияние выкатом, отметка нужна")
	}
	if !onBoard(t, root, "XR-005") {
		t.Fatal("строка уехала с доски при отказе ворот")
	}
}

// TestCloseCodeMergeSmokeCycle: круг выката на настоящих коммитах кода.
// Непроверенный выкат держит закрытие, новый выкат следом отметку прошлого
// круга не прикрывает (DK-1363, DK-1133), и только отметка на последний выкат
// открывает его: защитные случаи исходного ворота сохраняются вместе с
// различением docs.
func TestCloseCodeMergeSmokeCycle(t *testing.T) {
	root := setup(t)
	gitSetup(t, root)
	first := gitCommitFiles(t, root, "feat: XR-005 правка",
		map[string]string{"tools/x/main.go": "package main\n"})
	writeTask(t, root, "XR-005", mergedDoc("XR-005", "- 2026-10-12 слито: "+first))
	markRehearsed(t, root)
	closeThroughCheck(t, root, "XR-005")
	if _, err := cmdClose(root, CloseParams{ID: "XR-005", Date: "2026-10-23"}); err == nil {
		t.Fatal("настоящий выкат без отметки smoke обязан держать закрытие")
	}
	second := gitCommitFiles(t, root, "feat: XR-005 доработка",
		map[string]string{"tools/x/main.go": "package main\nfunc f() {}\n"})
	markRehearsed(t, root)
	writeTask(t, root, "XR-005", mergedDoc("XR-005",
		"- 2026-10-12 слито: "+first,
		"- smoke прогнан, 2026-10-13",
		"- 2026-10-20 слито: "+second))
	markRehearsed(t, root)
	if _, err := cmdClose(root, CloseParams{ID: "XR-005", Date: "2026-10-23"}); err == nil {
		t.Fatal("новый выкат после отметки прошлого круга обязан держать закрытие")
	}
	if !onBoard(t, root, "XR-005") {
		t.Fatal("строка уехала с доски при отказе ворот")
	}
	writeTask(t, root, "XR-005", mergedDoc("XR-005",
		"- 2026-10-12 слито: "+first,
		"- smoke прогнан, 2026-10-13",
		"- 2026-10-20 слито: "+second,
		"- smoke прогнан, 2026-10-21"))
	markRehearsed(t, root)
	if _, err := cmdClose(root, CloseParams{ID: "XR-005", Date: "2026-10-23"}); err != nil {
		t.Fatalf("действующая отметка smoke ворот держать не должна: %v", err)
	}
	if onBoard(t, root, "XR-005") {
		t.Fatal("строка с проверенным выкатом не закрылась")
	}
}

// readFile это чтение файла теста одним вызовом: круг доработки дописывает
// строки записи, и каждый раз читать файл заново многословнее.
func readFile(t *testing.T, p string) string {
	t.Helper()
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
