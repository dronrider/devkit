package main

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/dronrider/devkit/internal/stage"
)

// stageHome уводит записи этапов во временный дом: они лежат на уровне машины, и
// без подмены тест писал бы в живой ~/.devkit/runs.
func stageHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	return home
}

// openStages отмечает этапы задачи так, как это делает конвейер: pick --record
// на каждый вердикт, без единой правки рабочего дерева.
func openStages(t *testing.T, root, id string, kinds ...string) {
	t.Helper()
	base := time.Date(2026, 8, 15, 10, 0, 0, 0, time.Local)
	for i, kind := range kinds {
		note := "субагент opus/high по вердикту pick"
		if err := stage.Open(stage.Home(), stage.MainRoot(root), id, kind, note, base.Add(time.Duration(i)*time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestMoveFlushesStagesIntoTaskFile(t *testing.T) {
	stageHome(t)
	root := setup(t)
	openStages(t, root, "XR-002", stage.Dev, stage.Review)
	if _, err := cmdMove(root, "XR-002", SectInProgress, "", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(taskFilePath(root, "XR-002"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, "## Ход работы") {
		t.Fatalf("раздела «Ход работы» не завелось:\n%s", text)
	}
	if !strings.Contains(text, "- Разработка: субагент opus/high по вердикту pick, 2026-08-15 10:00-11:00.") {
		t.Fatalf("этап разработки не записан:\n%s", text)
	}
	if !strings.Contains(text, "- Ревью: субагент opus/high по вердикту pick, 2026-08-15 11:00-") {
		t.Fatalf("этап ревью не записан:\n%s", text)
	}
	// Пакет уехал целиком, и запись за ним не осталась: иначе следующий переход
	// записал бы те же этапы второй раз, а дашборд выдавал бы закрытый этап за
	// живой.
	rec, err := stage.Load(stage.Path(stage.Home(), stage.MainRoot(root), "XR-002"))
	if err != nil {
		t.Fatal(err)
	}
	if len(rec.Stages) != 0 {
		t.Fatalf("запись пережила пакет: %+v", rec.Stages)
	}
}

func TestMoveWithoutStagesLeavesTaskFileAlone(t *testing.T) {
	stageHome(t)
	root := setup(t)
	before, err := os.ReadFile(taskFilePath(root, "XR-002"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cmdMove(root, "XR-002", SectInProgress, "", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(taskFilePath(root, "XR-002"))
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatalf("переход без единого этапа тронул файл задачи:\n%s", after)
	}
}

func TestMoveToCheckOpensOutside(t *testing.T) {
	stageHome(t)
	root := setup(t)
	openStages(t, root, "XR-005", stage.Dev)
	if _, err := cmdMove(root, "XR-005", SectCheck, "", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	rec, err := stage.Load(stage.Path(stage.Home(), stage.MainRoot(root), "XR-005"))
	if err != nil {
		t.Fatal(err)
	}
	live, ok := rec.Live()
	if !ok {
		t.Fatal("перевод в Check не отметил ожидания снаружи")
	}
	if live.Kind != stage.Outside {
		t.Fatalf("вид деятельности после Check %q, жду %q", live.Kind, stage.Outside)
	}
	if len(rec.Stages) != 1 {
		t.Fatalf("ожидание снаружи должно открывать новый пакет, а в нём %d этапов", len(rec.Stages))
	}
}

func TestMoveToBlockedOpensOutsideWithReason(t *testing.T) {
	stageHome(t)
	root := setup(t)
	if _, err := cmdMove(root, "XR-004", SectInProgress, "", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdMove(root, "XR-004", SectBlocked, "ждём железо", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	rec, err := stage.Load(stage.Path(stage.Home(), stage.MainRoot(root), "XR-004"))
	if err != nil {
		t.Fatal(err)
	}
	live, _ := rec.Live()
	if live.Kind != stage.Outside || !strings.Contains(live.Note, "ждём железо") {
		t.Fatalf("блокер не назван ожиданием снаружи с причиной: %+v", live)
	}
}

func TestMoveToBacklogOpensNothing(t *testing.T) {
	stageHome(t)
	root := setup(t)
	if _, err := cmdMove(root, "XR-004", SectInProgress, "", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	rec, err := stage.Load(stage.Path(stage.Home(), stage.MainRoot(root), "XR-004"))
	if err != nil {
		t.Fatal(err)
	}
	if len(rec.Stages) != 0 {
		t.Fatalf("взятие в работу объявило этап раньше, чем за него взялись: %+v", rec.Stages)
	}
}

func TestCloseFlushesStagesBeforeArchive(t *testing.T) {
	stageHome(t)
	root := setup(t)
	if _, err := cmdMove(root, "XR-005", SectCheck, "", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdClose(root, CloseParams{ID: "XR-005", Date: "2026-08-16"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(root + "/docs/tasks/archive/2026/XR-005.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "- Снаружи: проверка после выката,") {
		t.Fatalf("этап ожидания не доехал до архивного файла задачи:\n%s", data)
	}
}
