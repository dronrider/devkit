package main

// Проектный слой шаблонов на воротах (DK-1144, DoD): добавленная организацией
// работа с gate = check спрашивается наравне со встроенной, отказ называет
// этап и след, пометка «- Исключение» по имени этапа его гасит, а сухой прогон
// печатает этапы вместе со следами.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// auditTemplate пишет проектный шаблон audit с одним внешним этапом: строка
// называет его суффиксом, и замещение одноимённого целиком (DK-972.2) здесь не
// мешает, у встроенного набора имени audit нет. След этапа задаёт вызывающий;
// слово берётся из словаря следов, чужое слово набор не читает вовсе.
func auditTemplate(t *testing.T, root, trace, gate string) {
	t.Helper()
	dir := filepath.Join(root, ".devkit", "plans")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "name = \"audit\"\ntitle = \"Аудит\"\ntypes = [\"task\"]\n\n[audit]\ntitle = \"аудит\"\nskill = \"board-task\"\nby = \"сам\"\ntrace = \"" + trace + "\"\ngate = [\"" + gate + "\"]\n"
	if err := os.WriteFile(filepath.Join(dir, "audit.toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// auditRow зовёт строке XR-005 суффикс шаблона audit.
func auditRow(t *testing.T, root string) {
	t.Helper()
	b, err := LoadBoard(boardPath(root))
	if err != nil {
		t.Fatal(err)
	}
	row := b.find("XR-005")
	row.Title += " [план: audit]"
	b.updateLine(row.LineIdx, formatRow(row))
	if err := b.Save(); err != nil {
		t.Fatal(err)
	}
}

// TestMoveCheckNamesProjectStage: у задачи с проектным этапом move check
// падает, называя сам этап и ожидаемый след. След «ревью» в файле фикстуры
// нет, и проверяющий его называет.
func TestMoveCheckNamesProjectStage(t *testing.T) {
	root := setup(t)
	auditTemplate(t, root, "review", "check")
	auditRow(t, root)
	_, err := cmdMove(root, "XR-005", SectCheck, "", CommitOpts{})
	if err == nil {
		t.Fatal("move в Check мимо проектного этапа должен падать")
	}
	for _, want := range []string{"аудит", "ревью"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("в отказе нет %q: %v", want, err)
		}
	}
	if s := sect(t, root, "XR-005"); s != SectInProgress {
		t.Fatalf("строка уехала при отказе: %s", s)
	}
}

// TestMoveCheckProjectStageException: пометка-исключение по имени этапа гасит
// и проектную работу, ворота больше её не спрашивают.
func TestMoveCheckProjectStageException(t *testing.T) {
	root := setup(t)
	auditTemplate(t, root, "review", "check")
	auditRow(t, root)
	p := filepath.Join(root, "docs", "tasks", "XR-005.md")
	doc, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	with := string(doc) + "\n## Ход работы\n\n- Исключение: аудит (машине нечем его гонять)\n"
	if err := os.WriteFile(p, []byte(with), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdMove(root, "XR-005", SectCheck, "", CommitOpts{}); err != nil {
		t.Fatalf("пометка-исключение проектный этап не погасила: %v", err)
	}
}

// TestMoveDryPrintsStages: сухой прогон перечисляет этапы со следами и
// вердиктом по каждому, чтобы вызывающий видел, из чего ворота решили.
func TestMoveDryPrintsStages(t *testing.T) {
	root := setup(t)
	auditTemplate(t, root, "слово", "check")
	auditRow(t, root)
	out, err := cmdMoveDry(root, "XR-005", SectCheck)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "этапы: аудит (слово): на слове агента") {
		t.Fatalf("сухой прогон не назвал этап со следом: %q", out)
	}
}

// lldRow переводит XR-005 в тип LLD: на живой доске тип пишется с большой
// буквы, а фикстура держит task.
func lldRow(t *testing.T, root string) {
	t.Helper()
	b, err := LoadBoard(boardPath(root))
	if err != nil {
		t.Fatal(err)
	}
	row := b.find("XR-005")
	row.Type = "LLD"
	b.updateLine(row.LineIdx, formatRow(row))
	if err := b.Save(); err != nil {
		t.Fatal(err)
	}
}

// TestMoveCheckLLDFindsTemplate: без сведения регистра выбора шаблона
// LLD-строки не находили lld и ехали в Check без сценария, хотя условие
// задачи держит поведение встроенного шаблона прежним (ревью DK-1144,
// замечание 1). Отказ обязан назвать этап и шаблон.
func TestMoveCheckLLDFindsTemplate(t *testing.T) {
	root := setup(t)
	lldRow(t, root)
	// Файл без сценария: из этапов lld с gate = check непройден только
	// «сценарий», слияние в дереве без гита видно отсутствием ветки.
	if err := os.WriteFile(filepath.Join(root, "docs", "tasks", "XR-005.md"), []byte("# XR-005\n"+fixtureTaskBody), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := cmdMove(root, "XR-005", SectCheck, "", CommitOpts{})
	if err == nil {
		t.Fatal("LLD-строка без сценария прошла в Check мимо шаблона")
	}
	for _, want := range []string{"сценарий", "lld"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("в отказе нет %q: %v", want, err)
		}
	}
	giveScenario(t, root, "XR-005")
	if _, err := cmdMove(root, "XR-005", SectCheck, "", CommitOpts{}); err != nil {
		t.Fatalf("со сценарием move check не прошёл: %v", err)
	}
}

// lldStageTemplate пишет проектный шаблон, выбираемый строчным типом lld, с
// этапом, чьё условие называет тип так же строчными: без сведения регистра в
// отборе этапов этап выпадал бы из ворот у строки с большой буквы.
func lldStageTemplate(t *testing.T, root string) {
	t.Helper()
	dir := filepath.Join(root, ".devkit", "plans")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "name = \"audit\"\ntitle = \"Аудит\"\ntypes = [\"lld\"]\n\n[audit]\ntitle = \"аудит\"\nskill = \"board-task\"\nby = \"сам\"\ntrace = \"review\"\ngate = [\"check\"]\ntypes = [\"lld\"]\n"
	if err := os.WriteFile(filepath.Join(dir, "audit.toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestMoveCheckStageTypeLowercase: условие этапа по типу сравнивается без
// учёта регистра, как и выбор шаблона, иначе проектный этап с types = ["lld"]
// молчал бы у строки типа LLD.
func TestMoveCheckStageTypeLowercase(t *testing.T) {
	root := setup(t)
	lldStageTemplate(t, root)
	lldRow(t, root)
	giveScenario(t, root, "XR-005")
	_, err := cmdMove(root, "XR-005", SectCheck, "", CommitOpts{})
	if err == nil {
		t.Fatal("проектный этап с условием типа выпал из ворот")
	}
	if !strings.Contains(err.Error(), "аудит") {
		t.Fatalf("в отказе нет этапа «аудит»: %v", err)
	}
}
