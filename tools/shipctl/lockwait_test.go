package main

import (
	"strings"
	"testing"
	"time"
)

// TestLockWaitWaitsOutHolder: занятый замок ждётся, и держатель называется
// сразу, до отчёта команды. Держателя снимает отдельная горутина через
// половину шага проверки, поэтому дожидается ожидание с первой же попытки.
func TestLockWaitWaitsOutHolder(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	devkitDir(t, root)
	unlock, err := acquireLock(root, "merge XR-009")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(lockPoll / 2)
		unlock()
	}()
	var said []string
	started := time.Now()
	got, err := acquireLockWait(root, "merge XR-001", time.Minute, func(s string) { said = append(said, s) })
	if err != nil {
		t.Fatalf("ожидание не дождалось свободного замка: %v", err)
	}
	got()
	if time.Since(started) < lockPoll/2 {
		t.Error("замок взят раньше, чем держатель его отпустил")
	}
	if len(said) == 0 || !strings.Contains(said[0], "merge XR-009") {
		t.Fatalf("держатель обязан быть назван первой же строкой: %v", said)
	}
	if !strings.Contains(said[0], "жду до") {
		t.Errorf("срок ожидания не назван: %q", said[0])
	}
}

// TestLockWaitZeroRefusesAtOnce: нулевой срок это прежний мгновенный отказ,
// его ключом просит скрипт, которому важнее не встать в ожидание.
func TestLockWaitZeroRefusesAtOnce(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	devkitDir(t, root)
	unlock, err := acquireLock(root, "merge XR-009")
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	started := time.Now()
	said := 0
	if _, err := acquireLockWait(root, "merge XR-001", 0, func(string) { said++ }); err == nil {
		t.Fatal("нулевой срок обязан отказывать сразу")
	} else if !strings.Contains(err.Error(), "конвейер занят") {
		t.Fatalf("отказ занятости: %v", err)
	}
	if time.Since(started) > lockPoll {
		t.Error("мгновенный отказ ушёл в ожидание")
	}
	if said != 0 {
		t.Error("мгновенному отказу печатать ход нечего")
	}
}

// TestLockWaitGivesUpOnDeadline: срок вышел, а держатель всё работает, значит
// это не чужое слияние, а висящий процесс, и отказ говорит про разбор, а не
// про новый повтор.
func TestLockWaitGivesUpOnDeadline(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	devkitDir(t, root)
	unlock, err := acquireLock(root, "merge XR-009")
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	_, err = acquireLockWait(root, "merge XR-001", time.Nanosecond, func(string) {})
	if err == nil {
		t.Fatal("вышедший срок обязан отказывать")
	}
	if !strings.Contains(err.Error(), "не дождался") || !strings.Contains(err.Error(), "конвейер занят") {
		t.Fatalf("отказ по сроку: %v", err)
	}
}

// TestLockBusyNowLeavesNoTrace: проба занятости ничего на диске не меняет, и
// файла держателя после неё не остаётся: иначе следующий отказ называл бы
// держателем пробу.
func TestLockBusyNowLeavesNoTrace(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	devkitDir(t, root)
	if lockBusyNow(root) {
		t.Error("на свободном конвейере проба говорит о занятости")
	}
	unlock, err := acquireLock(root, "merge XR-009")
	if err != nil {
		t.Fatal(err)
	}
	if !lockBusyNow(root) {
		t.Error("под взятым замком проба занятости не видит")
	}
	unlock()
	if !strings.Contains(lockHolderNow(root), "неназвавшийся") {
		t.Errorf("после снятия замка держателя быть не должно: %q", lockHolderNow(root))
	}
}

// TestMergeWaitsLockMarksStage: слияние, ткнувшееся в занятый замок, отмечает
// ожидание очереди этапом до самого ожидания. Без этого минуты под занятым
// замком снаружи неотличимы от зависшей команды.
func TestMergeWaitsLockMarksStage(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	devkitDir(t, root)
	branchWithFix(t, root)
	unlock, err := acquireLock(root, "merge XR-009")
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	if _, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true", LockWait: time.Nanosecond}); err == nil {
		t.Fatal("занятый замок с вышедшим сроком обязан отказывать")
	}
	stages := stagesOf(t, root, "XR-001")
	if len(stages) == 0 || !strings.Contains(stages[0].Note, "ждёт замок конвейера") {
		t.Errorf("этап ожидания не записан: %+v", stages)
	}
}
