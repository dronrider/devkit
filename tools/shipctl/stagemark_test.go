package main

import (
	"strings"
	"testing"

	"github.com/dronrider/devkit/internal/stage"
)

// Этапы слияния, выката и ожидания очереди ставит сам shipctl (DK-911), без
// команды диспетчера. Стенд читает запись ~/.devkit/runs во временном доме:
// taskctl тут стаб, пакет в файл задачи он не уносит, и запись остаётся на
// месте вместе с концами этапов.

func stagesOf(t *testing.T, root, id string) []stage.Stage {
	t.Helper()
	rec, err := stage.Load(stage.Path(stage.Home(), stage.MainRoot(root), id))
	if err != nil {
		t.Fatal(err)
	}
	return rec.Stages
}

func TestMergeWritesMergeStageAndClosesIt(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	branchWithFix(t, root)
	if _, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true"}); err != nil {
		t.Fatal(err)
	}
	stages := stagesOf(t, root, "XR-001")
	if len(stages) != 1 || stages[0].Kind != stage.Merge {
		t.Fatalf("после merge жду один этап слияния, вижу %+v", stages)
	}
	if !stages[0].Ended() {
		t.Fatalf("слияние кончилось, а этап открыт: %+v", stages[0])
	}
	if !strings.Contains(stages[0].Note, "shipctl merge") {
		t.Fatalf("запись не называет писателя: %q", stages[0].Note)
	}
}

func TestBusyLockWritesQueueWaitOnce(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	devkitDir(t, root)
	branchWithFix(t, root)
	unlock, err := acquireLock(root, "merge XR-009")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true"}); err == nil {
			t.Fatal("merge при взятом замке прошёл")
		}
	}
	stages := stagesOf(t, root, "XR-001")
	if len(stages) != 1 || stages[0].Kind != stage.WaitQueue || stages[0].Ended() {
		t.Fatalf("два отказа по замку должны дать одно живое ожидание очереди, вижу %+v", stages)
	}
	if !strings.Contains(stages[0].Note, "shipctl merge") {
		t.Fatalf("ожидание не называет, кто ждал: %q", stages[0].Note)
	}
	unlock()
	if _, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true"}); err != nil {
		t.Fatalf("после снятия замка merge должен проходить: %v", err)
	}
	stages = stagesOf(t, root, "XR-001")
	if len(stages) != 2 || stages[1].Kind != stage.Merge || !stages[1].Ended() {
		t.Fatalf("слияние после ожидания не легло закрытым этапом: %+v", stages)
	}
}

func TestShipWritesDeployStageForTrain(t *testing.T) {
	root, _ := setup(t, rowInProg+rowInProg3, "")
	taskWithScenario(t, root, "XR-003")
	branchFor(t, root, "XR-001", "xr-001-fix", "a.txt")
	if _, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true", Train: true}); err != nil {
		t.Fatal(err)
	}
	branchFor(t, root, "XR-003", "xr-003-fix", "b.txt")
	if _, err := cmdMerge(root, MergeParams{ID: "XR-003", Test: "true", Train: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdShip(root, ShipParams{Deploy: "true"}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"XR-001", "XR-003"} {
		stages := stagesOf(t, root, id)
		if len(stages) != 2 || stages[0].Kind != stage.Merge || stages[1].Kind != stage.Deploy {
			t.Fatalf("у %s жду слияние и выкат, вижу %+v", id, stages)
		}
		if !stages[1].Ended() || !strings.Contains(stages[1].Note, "XR-001, XR-003") {
			t.Fatalf("выкат %s не закрыт либо не называет состав: %+v", id, stages[1])
		}
	}
}

func TestShipBusyLockWritesQueueWaitForTrain(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	devkitDir(t, root)
	branchWithFix(t, root)
	if _, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true", Train: true}); err != nil {
		t.Fatal(err)
	}
	unlock, err := acquireLock(root, "ship")
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	if _, err := cmdShip(root, ShipParams{Deploy: "true", Drain: true}); err != nil {
		t.Fatalf("занятый замок под --drain это тихий no-op: %v", err)
	}
	stages := stagesOf(t, root, "XR-001")
	if n := len(stages); n != 2 || stages[1].Kind != stage.WaitQueue || stages[1].Ended() {
		t.Fatalf("ship по занятому замку не отметил ожидание очереди составу: %+v", stages)
	}
}
