package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// muteShipctlInTests ставит в PATH молчаливый стаб shipctl: close с DK-312
// зовёт разлив после каждой задачи, и без стаба сотня close-тестов звала бы
// настоящий shipctl с машины разработчика, медленно и с выводом в отчёты.
// Тесты самого разлива кладут свой говорящий стаб поверх или вычищают PATH
// целиком через pathWithoutShipctl.
func muteShipctlInTests() {
	bin, err := os.MkdirTemp("", "taskctl-ship-stub")
	if err != nil {
		return
	}
	stub := "#!/bin/sh\nexit 0\n"
	if err := os.WriteFile(filepath.Join(bin, "shipctl"), []byte(stub), 0o755); err != nil {
		return
	}
	os.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// pathWithoutShipctl вычищает из PATH каталоги, где лежит shipctl: на машине
// разработчика утилита стоит глобально, и голый prepend стаба её не спрятал бы.
// Приём тот же, что у pathWithout в tools/shipctl (corp_pipeline_test.go).
func pathWithoutShipctl(path string) string {
	var kept []string
	for _, dir := range strings.Split(path, string(os.PathListSeparator)) {
		if dir == "" {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, "shipctl")); err == nil {
			continue
		}
		kept = append(kept, dir)
	}
	return strings.Join(kept, string(os.PathListSeparator))
}

// writeShipStub кладёт в PATH подставной shipctl с телом body: тело целиком
// за тестом, обычно оно пишет вызов и то, что стаб увидел на доске, в лог.
func writeShipStub(t *testing.T, body string) {
	t.Helper()
	bin := t.TempDir()
	stub := "#!/bin/sh\n" + body
	if err := os.WriteFile(filepath.Join(bin, "shipctl"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// TestCloseCallsShipDrainAfterArchive: close зовёт shipctl ship --drain в том
// же корне, и зовёт после архива, когда строки задачи на доске уже нет (LLD
// DK-306, решение 4: close коммитит и пушит доску, потом вызывает ship).
func TestCloseCallsShipDrainAfterArchive(t *testing.T) {
	root := setup(t)
	dir := t.TempDir()
	log := filepath.Join(dir, "calls.log")
	writeShipStub(t,
		"echo \"$@\" >> "+log+
			"\nif grep -q '^| XR-005 |' \"$2/docs/TASKS.md\"; then echo 'строка ещё на доске' >> "+log+
			"; else echo 'строки нет на доске' >> "+log+"; fi\n")
	if _, err := cmdClose(root, CloseParams{ID: "XR-005", Date: "2026-08-26"}); err != nil {
		t.Fatal(err)
	}
	calls, err := os.ReadFile(log)
	if err != nil {
		t.Fatalf("разлив не позван: %v", err)
	}
	got := string(calls)
	if !strings.Contains(got, "-C "+root+" ship --drain") {
		t.Fatalf("close должен звать shipctl -C <корень> ship --drain: %q", got)
	}
	if !strings.Contains(got, "строки нет на доске") || strings.Contains(got, "строка ещё на доске") {
		t.Fatalf("разлив должен зваться после архива, со строки уже снятой: %q", got)
	}
}

// TestCloseDrainFailureWarns: провал разлива не роняет close, а дописывается
// в отчёт предупреждением (LLD DK-306, решение 4: вызов best-effort).
func TestCloseDrainFailureWarns(t *testing.T) {
	root := setup(t)
	writeShipStub(t, "echo поезд застрял >&2\nexit 1\n")
	msg, err := cmdClose(root, CloseParams{ID: "XR-005", Date: "2026-08-26"})
	if err != nil {
		t.Fatalf("провал разлива не должен ронять close: %v", err)
	}
	if !strings.Contains(msg, "предупреждение: разлив упал") || !strings.Contains(msg, "поезд застрял") {
		t.Fatalf("провал разлива должен идти предупреждением с выводом ship: %q", msg)
	}
}

// TestCloseDrainSilentSuccess: молчаливый разлив ничего не дописывает в отчёт,
// формат сообщений close без выката остаётся прежним.
func TestCloseDrainSilentSuccess(t *testing.T) {
	root := setup(t)
	writeShipStub(t, "exit 0\n")
	msg, err := cmdClose(root, CloseParams{ID: "XR-005", Date: "2026-08-26"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(msg, "разлив") {
		t.Fatalf("молчаливый разлив не должен менять отчёт close: %q", msg)
	}
}

// TestCloseDrainNoShipctl: без shipctl в PATH close всё равно успешен, а в
// отчёте предупреждение с способом поставить утилиту.
func TestCloseDrainNoShipctl(t *testing.T) {
	root := setup(t)
	t.Setenv("PATH", pathWithoutShipctl(os.Getenv("PATH")))
	msg, err := cmdClose(root, CloseParams{ID: "XR-005", Date: "2026-08-26"})
	if err != nil {
		t.Fatalf("отсутствие shipctl не должно ронять close: %v", err)
	}
	if !strings.Contains(msg, "разлив не позван: shipctl не найден в PATH") {
		t.Fatalf("отсутствие shipctl должно идти предупреждением: %q", msg)
	}
}
