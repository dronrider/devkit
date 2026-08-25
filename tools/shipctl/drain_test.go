package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestShipDrainEmptyTrainNoop: разлив на пустом поезде выходит нулём и
// молчит явным текстом, а не отбивается ошибкой, как обычный ship (LLD
// DK-306, решение 2).
func TestShipDrainEmptyTrainNoop(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	msg, err := cmdShip(root, ShipParams{Drain: true})
	if err != nil {
		t.Fatalf("разлив на пустом поезде должен молчать нулём: %v", err)
	}
	if !strings.Contains(msg, "разлив не нужен") || !strings.Contains(msg, "поезд пуст") {
		t.Fatalf("сообщение о пустом поезде: %q", msg)
	}
}

// TestShipDrainQueueBusyNoop: та же тишина на занятой очереди.
func TestShipDrainQueueBusyNoop(t *testing.T) {
	root, _ := setup(t, rowInProg, rowCheck)
	codeCommit(t, root, "XR-009", "nine.txt")
	msg, err := cmdShip(root, ShipParams{Drain: true})
	if err != nil {
		t.Fatalf("разлив на занятой очереди должен молчать нулём: %v", err)
	}
	if !strings.Contains(msg, "разлив не нужен") || !strings.Contains(msg, "очередь занята: XR-009") {
		t.Fatalf("сообщение о занятой очереди: %q", msg)
	}
}

// TestShipDrainBrokenProdNoop: и на сломанном проде, третьем чистом отказе.
func TestShipDrainBrokenProdNoop(t *testing.T) {
	root, _ := setup(t, rowInProg+rowFailed, "")
	msg, err := cmdShip(root, ShipParams{Drain: true})
	if err != nil {
		t.Fatalf("разлив на сломанном проде должен молчать нулём: %v", err)
	}
	if !strings.Contains(msg, "разлив не нужен") || !strings.Contains(msg, "провал проверки за XR-003") {
		t.Fatalf("сообщение о сломанном проде: %q", msg)
	}
}

// TestShipDrainBusyLockNoop: тик сторожка, попавший в окно чужого merge,
// ship или разлива от close, получает от acquireLock отказ занятости.
// Решение 2 LLD DK-306 зовёт такой исход штатной состыковкой («первый
// выкатывает поезд, второй заходит в пустой поезд и отступает»), поэтому
// под --drain он выходит нулём с маркером «разлив не нужен», а не валит
// тик строкой «разлив упал» в журнале сторожка.
func TestShipDrainBusyLockNoop(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	devkitDir(t, root)
	unlock, err := acquireLock(root)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()

	msg, err := cmdShip(root, ShipParams{Drain: true})
	if err != nil {
		t.Fatalf("разлив при занятом конвейере должен отступать нулём: %v", err)
	}
	if !strings.Contains(msg, "разлив не нужен") || !strings.Contains(msg, "конвейер занят") {
		t.Fatalf("сообщение о занятом конвейере: %q", msg)
	}
}

// TestShipDrainLockAnomalyStaysError: занятость под --drain глушится, а
// аномальные отказы замка (не открылся, не устоялся за N попыток) нет: это
// поломка окружения, и она попадает в журнал сторожка провалом, а не тихой
// состыковкой. Замок, который не открывается, заводится каталогом на его
// пути: open с O_RDWR на каталог отказывает.
func TestShipDrainLockAnomalyStaysError(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	devkitDir(t, root)
	if err := os.Mkdir(filepath.Join(root, lockPath), 0o755); err != nil {
		t.Fatal(err)
	}

	_, err := cmdShip(root, ShipParams{Drain: true})
	if err == nil || !strings.Contains(err.Error(), "замок") {
		t.Fatalf("аномальный отказ замка под --drain должен остаться ошибкой: %v", err)
	}
}

// TestShipDrainNonEmptyTrainShipsNormally: непустой поезд при свободной
// очереди и целом проде --drain не отличает от обычного ship: один деплой,
// перевод в Check, сдвиг тега.
func TestShipDrainNonEmptyTrainShipsNormally(t *testing.T) {
	root, callLog := setup(t, rowInProg, "")
	branchFor(t, root, "XR-001", "xr-001-fix", "a.txt")
	if _, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true", Train: true}); err != nil {
		t.Fatal(err)
	}
	msg, err := cmdShip(root, ShipParams{Deploy: "true", Drain: true})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(msg, "разлив не нужен") {
		t.Fatalf("непустой поезд не должен выглядеть no-op: %q", msg)
	}
	if !strings.Contains(msg, "поезд выкачен (XR-001)") {
		t.Fatalf("непустой поезд под --drain должен выкатываться как обычно: %q", msg)
	}
	calls, _ := os.ReadFile(callLog)
	if !strings.Contains(string(calls), "move XR-001 check") {
		t.Fatalf("ship --drain должен перевести задачу в Check: %q", calls)
	}
}

// TestShipDrainDeployFailureMarksFailure: провал деплоя это не «нечего
// разливать», --drain его не глушит, но ставит признак провала на первую
// задачу состава тем же путём, что taskctl fail, и notify зовёт через него, а
// не своим вызовом notify() в cmdShip: иначе одно и то же событие дало бы два
// уведомления.
func TestShipDrainDeployFailureMarksFailure(t *testing.T) {
	root, callLog := setup(t, rowInProg, "")
	branchFor(t, root, "XR-001", "xr-001-fix", "a.txt")
	if _, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true", Train: true}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEVKIT_NOTIFY_OFF", "")
	notifyCalls := writeNotifyStub(t, root)

	_, err := cmdShip(root, ShipParams{Deploy: "false", Drain: true})
	if err == nil || !strings.Contains(err.Error(), "выкат поезда упал") {
		t.Fatalf("провал деплоя под --drain должен остаться ошибкой: %v", err)
	}
	calls, _ := os.ReadFile(callLog)
	if !strings.Contains(string(calls), "fail XR-001 --reason упал") {
		t.Fatalf("признак провала не поставлен через taskctl fail: %q", calls)
	}
	if got, _ := os.ReadFile(notifyCalls); len(strings.TrimSpace(string(got))) != 0 {
		t.Fatalf("cmdShip не должен звать notify() сам под --drain, это дублировало бы уведомление taskctl fail: %q", got)
	}
}

// TestShipWithoutDrainDeployFailureNoFailureMark: обычный ship (без --drain)
// поведения не меняет, признак не ставит и продолжает звать notify сам.
func TestShipWithoutDrainDeployFailureNoFailureMark(t *testing.T) {
	root, callLog := setup(t, rowInProg, "")
	branchFor(t, root, "XR-001", "xr-001-fix", "a.txt")
	if _, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true", Train: true}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEVKIT_NOTIFY_OFF", "")
	notifyCalls := writeNotifyStub(t, root)

	_, err := cmdShip(root, ShipParams{Deploy: "false"})
	if err == nil || !strings.Contains(err.Error(), "выкат поезда упал") {
		t.Fatalf("ждал отказ по упавшему выкату: %v", err)
	}
	calls, _ := os.ReadFile(callLog)
	if strings.Contains(string(calls), "fail XR-001") {
		t.Fatalf("обычный ship не должен ставить признак провала: %q", calls)
	}
	if got, _ := os.ReadFile(notifyCalls); len(strings.TrimSpace(string(got))) == 0 {
		t.Fatal("обычный ship должен звать notify сам")
	}
}
