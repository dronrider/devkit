package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dronrider/devkit/internal/stage"
)

// devStage кладёт задаче незакрытый этап разработки названной моделью: по нему
// расчёт проверяющего узнаёт автора правки.
func devStage(t *testing.T, root, id, model string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, "docs", "tasks"), 0o755); err != nil {
		t.Fatal(err)
	}
	doc := "# " + id + "\n\n## Ход работы\n\n"
	if err := os.WriteFile(filepath.Join(root, "docs", "tasks", id+".md"), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	note := "субагент " + model + "/high по вердикту pick"
	if err := stage.Open(stage.Home(), stage.MainRoot(root), id, stage.Dev, note, time.Now()); err != nil {
		t.Fatal(err)
	}
}

// TestCmdCheckStepsOverDevModel: сценарий прогоняет не автор правки, и на
// задаче ценой M вердикт роли ревью приходит тем же ярусом base, каким шла
// разработка. Расчёт проверяющего обязан поднять ступень, иначе поднятый по
// нему субагент отработает впустую, а taskctl close его прогон отобьёт
// (DK-1116).
func TestCmdCheckStepsOverDevModel(t *testing.T) {
	isolateQuota(t)
	root := writeBoard(t)
	devStage(t, root, "T-007", "sonnet")
	out, err := cmdCheck(root, "T-007")
	if err != nil {
		t.Fatalf("check T-007: %v", err)
	}
	for _, want := range []string{"model: opus", "tier: pro", "ярус base поднят ступенью до pro", "разработку вёл sonnet"} {
		if !strings.Contains(out, want) {
			t.Fatalf("в расчёте нет %q:\n%s", want, out)
		}
	}
}

// Разработку вела другая модель, значит ступень не нужна: проверяющий идёт
// ярусом вердикта роли ревью.
func TestCmdCheckKeepsTierOnForeignDevModel(t *testing.T) {
	isolateQuota(t)
	root := writeBoard(t)
	devStage(t, root, "T-007", "opus")
	out, err := cmdCheck(root, "T-007")
	if err != nil {
		t.Fatalf("check T-007: %v", err)
	}
	if !strings.Contains(out, "model: sonnet") || !strings.Contains(out, "tier: base") {
		t.Fatalf("ярус вердикта не удержан:\n%s", out)
	}
	if strings.Contains(out, "ступенью") {
		t.Fatalf("ступень поднята без совпадения моделей:\n%s", out)
	}
}

// Исполнителя разработки записи не назвали: сверять не с кем, ступени нет, и
// это сказано словами.
func TestCmdCheckWithoutDevRecord(t *testing.T) {
	isolateQuota(t)
	root := writeBoard(t)
	out, err := cmdCheck(root, "T-007")
	if err != nil {
		t.Fatalf("check T-007: %v", err)
	}
	if !strings.Contains(out, "model: sonnet") || !strings.Contains(out, "исполнителя разработки записи не назвали") {
		t.Fatalf("молчание записей не названо:\n%s", out)
	}
}

// Лестница исчерпана: разработку вела модель верхнего яруса, поднимать другой
// некем, и команда отказывает вместо того, чтобы назвать автора правки.
func TestCmdCheckRefusesOnExhaustedLadder(t *testing.T) {
	isolateQuota(t)
	root := writeBoard(t)
	devStage(t, root, "T-007", "fable")
	doc := "# T-007\n\nМодель: max\n\n## Ход работы\n\n"
	if err := os.WriteFile(filepath.Join(root, "docs", "tasks", "T-007.md"), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := cmdCheck(root, "T-007")
	if err == nil {
		t.Fatal("расчёт на исчерпанной лестнице обязан отказывать")
	}
	for _, want := range []string{"прогонять некем", "fable", "ступени выше"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("в отказе нет %q: %v", want, err)
		}
	}
}
