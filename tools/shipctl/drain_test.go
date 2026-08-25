package main

import (
	"os"
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
