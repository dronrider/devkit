package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Подъём прогона сценария после выката (DK-718). Стенд тот же, что у остальных
// проверок конвейера: синтетическая доска, поддельная команда выката и
// фикстуры утилит в PATH. Настоящего дашборда тут нет, проверяется, кто и с
// чем его зовёт.

// autonomousCfg это обвязка автономного выката: команда выката поддельная,
// маркер её запуска ложится в корень.
func autonomousCfg(root string) string {
	return "deploy = touch " + filepath.Join(root, "deployed.marker") + "\nautonomous = true\n"
}

// Автономное слияние доводит задачу до прода и тут же поднимает прогон
// сценария: своего окна у такого выката нет, и без подъёма строка стояла бы в
// Check, держа очередь непроверенного выката.
func TestMergeAutonomousRaisesCheckRun(t *testing.T) {
	root, callLog := setup(t, rowInProg, "")
	writeDeployCfg(t, root, autonomousCfg(root))
	addRemote(t, root)
	branchWithFix(t, root)

	msg, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "deployed.marker")); err != nil {
		t.Fatalf("выкат не запускался: %q", msg)
	}
	calls := readCalls(t, callLog)
	if !strings.Contains(calls, "dashboard check -C "+root+" XR-001") {
		t.Fatalf("подъём прогона позван не так: %q", calls)
	}
	if !strings.Contains(msg, "прогон сценария поднят") {
		t.Fatalf("отчёт молчит про подъём прогона: %q", msg)
	}
}

// Выкат за пользователем прогон не поднимает: человек в окне, и проверяющего
// он поднимает сам (граница задачи DK-718).
func TestMergeWithoutAutonomousRaisesNothing(t *testing.T) {
	root, callLog := setup(t, rowInProg, "")
	writeDeployCfg(t, root, "deploy = true\nautonomous = false\n")
	branchWithFix(t, root)

	if _, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true"}); err != nil {
		t.Fatal(err)
	}
	if calls := readCalls(t, callLog); strings.Contains(calls, "dashboard") {
		t.Fatalf("при autonomous = false подъёма быть не должно: %q", calls)
	}
}

// Разлив поезда тиком сторожка это второй вход: тик зовёт `ship --drain`, и
// прогон поднимается на весь состав, а не на головную задачу.
func TestShipDrainRaisesCheckRunForTrain(t *testing.T) {
	root, callLog := setup(t, rowInProg+rowInProg3, "")
	writeDeployCfg(t, root, autonomousCfg(root))
	addRemote(t, root)
	taskWithScenario(t, root, "XR-003")
	branchFor(t, root, "XR-001", "xr-001-fix", "a.txt")
	if _, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true", Train: true}); err != nil {
		t.Fatal(err)
	}
	branchFor(t, root, "XR-003", "xr-003-fix", "b.txt")
	if _, err := cmdMerge(root, MergeParams{ID: "XR-003", Test: "true", Train: true}); err != nil {
		t.Fatal(err)
	}

	msg, err := cmdShip(root, ShipParams{Drain: true})
	if err != nil {
		t.Fatal(err)
	}
	calls := readCalls(t, callLog)
	if !strings.Contains(calls, "dashboard check -C "+root+" XR-001 XR-003") {
		t.Fatalf("подъём прогона по составу позван не так: %q", calls)
	}
	if !strings.Contains(msg, "прогон сценария поднят") {
		t.Fatalf("отчёт разлива молчит про подъём: %q", msg)
	}
}

// Ручной ship без флагов прогон не поднимает даже на автономном проекте:
// границу задачи держит признак вызова, а не флаг конфига. Разлив зовёт тик, у
// него окна нет; голый ship зовёт тот, кто сидит в окне и проверяющего
// поднимает сам. Оставшееся без отметки доберёт следующий тик.
func TestShipByHandRaisesNothing(t *testing.T) {
	root, callLog := setup(t, rowInProg, "")
	writeDeployCfg(t, root, autonomousCfg(root))
	addRemote(t, root)
	branchFor(t, root, "XR-001", "xr-001-fix", "a.txt")
	if _, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true", Train: true}); err != nil {
		t.Fatal(err)
	}
	msg, err := cmdShip(root, ShipParams{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "deployed.marker")); err != nil {
		t.Fatalf("выкат не запускался: %q", msg)
	}
	if calls := readCalls(t, callLog); strings.Contains(calls, "dashboard") {
		t.Fatalf("ручной ship прогон не поднимает: %q", calls)
	}
	if strings.Contains(msg, "прогон сценария поднят") {
		t.Fatalf("отчёт ручного ship обещает подъём: %q", msg)
	}
}

// Ручной выкат поезда командой человека прогон не поднимает: явный --deploy это
// указание прямо сейчас, и окно у него есть.
func TestShipManualDeployRaisesNothing(t *testing.T) {
	root, callLog := setup(t, rowInProg, "")
	branchFor(t, root, "XR-001", "xr-001-fix", "a.txt")
	if _, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true", Train: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdShip(root, ShipParams{Deploy: "true"}); err != nil {
		t.Fatal(err)
	}
	if calls := readCalls(t, callLog); strings.Contains(calls, "dashboard") {
		t.Fatalf("при явном --deploy подъёма быть не должно: %q", calls)
	}
}

// Ветка, не тронувшая ничего вне docs/, уходит в Check без выката, и прогон ей
// нужен тот же: сценарий у неё свой, а отметки smoke на ней нет.
func TestMergeTrainDocsOnlyRaisesCheckRun(t *testing.T) {
	root, callLog := setup(t, rowInProg, "")
	writeDeployCfg(t, root, autonomousCfg(root))
	addRemote(t, root)
	gitT(t, root, "checkout", "-qb", "xr-001-docs", "main")
	write(t, root, "docs/lld/note.md", "# LLD\n")
	// Добавляется только docs/: обвязка выката лежит в рабочем дереве
	// незакоммиченной, и попади она в коммит, ветка перестала бы быть
	// бескодовой.
	gitT(t, root, "add", "docs")
	gitT(t, root, "commit", "-qm", "docs: XR-001 правка LLD")

	if _, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true", Train: true}); err != nil {
		t.Fatal(err)
	}
	if calls := readCalls(t, callLog); !strings.Contains(calls, "dashboard check -C "+root+" XR-001") {
		t.Fatalf("бескодовой задаче прогон нужен тот же: %q", calls)
	}
}

// Молчания нет ни в одном исходе: не нашлась утилита подъёма, значит об этом
// сказано строкой отчёта вместе с тем, что делать руками.
func TestCheckRunNoteWithoutBinary(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	note := checkRunNote(t.TempDir(), []string{"XR-001"})
	for _, want := range []string{checkRunBin, "shipctl smoke XR-001"} {
		if !strings.Contains(note, want) {
			t.Fatalf("приписка о ненайденной утилите не называет %q: %q", want, note)
		}
	}
}

// Поднимать нечего значит и говорить нечего: пустой состав не зовёт утилиту
// вовсе.
func TestCheckRunNoteWithoutTasks(t *testing.T) {
	if note := checkRunNote(t.TempDir(), nil); note != "" {
		t.Fatalf("на пустом составе приписки быть не должно: %q", note)
	}
}

func readCalls(t *testing.T, callLog string) string {
	t.Helper()
	data, err := os.ReadFile(callLog)
	if err != nil {
		return ""
	}
	return string(data)
}
