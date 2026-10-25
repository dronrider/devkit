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
