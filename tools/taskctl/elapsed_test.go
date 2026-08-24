package main

import (
	"strings"
	"testing"
	"time"

	"github.com/dronrider/devkit/internal/stage"
)

func TestPluralMinutes(t *testing.T) {
	cases := map[int]string{
		0: "минут", 1: "минута", 2: "минуты", 4: "минуты", 5: "минут",
		11: "минут", 12: "минут", 14: "минут", 21: "минута", 22: "минуты", 25: "минут",
		101: "минута", 102: "минуты", 111: "минут",
	}
	for n, want := range cases {
		if got := pluralMinutes(n); got != want {
			t.Errorf("pluralMinutes(%d) = %q, ожидал %q", n, got, want)
		}
	}
}

// TestExecCeilingEnvOverride: окружение перебивает число потолка для стендов,
// а битое значение не роняет команду и возвращает умолчание (LLD DK-503,
// «оба числа печатаются в отказах и перебиваются окружением для стендов»).
func TestExecCeilingEnvOverride(t *testing.T) {
	if c := execCeiling(); c != execCeilingDefault {
		t.Fatalf("execCeiling() без окружения = %d, ждал %d", c, execCeilingDefault)
	}
	t.Setenv(execCeilingEnv, "5")
	if c := execCeiling(); c != 5 {
		t.Fatalf("execCeiling() с окружением = %d, ждал 5", c)
	}
	t.Setenv(execCeilingEnv, "не число")
	if c := execCeiling(); c != execCeilingDefault {
		t.Fatalf("execCeiling() с битым окружением = %d, ждал возврат к умолчанию %d", c, execCeilingDefault)
	}
}

// TestCmdElapsedNoOpenStage: запись .run пуста (диспетчер ни разу не звал
// pick --record), и команда честно говорит об этом, не падая.
func TestCmdElapsedNoOpenStage(t *testing.T) {
	root := setup(t)
	t.Setenv("HOME", t.TempDir())
	msg, err := cmdElapsed(root, "XR-005")
	if err != nil {
		t.Fatalf("cmdElapsed без записи: %v", err)
	}
	if msg != "этап не открыт, потолок не проверить" {
		t.Fatalf("cmdElapsed без записи = %q", msg)
	}
}

// TestCmdElapsedWithinCeiling: этап открыт недавно, до потолка.
func TestCmdElapsedWithinCeiling(t *testing.T) {
	root := setup(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	old := timeNow
	defer func() { timeNow = old }()
	now := time.Date(2026, 8, 24, 14, 41, 0, 0, time.Local)
	timeNow = func() time.Time { return now }

	if err := stage.Open(home, root, "XR-005", stage.Dev, "", now.Add(-46*time.Minute)); err != nil {
		t.Fatal(err)
	}
	msg, err := cmdElapsed(root, "XR-005")
	if err != nil {
		t.Fatalf("cmdElapsed: %v", err)
	}
	want := "разработка открыта 46 минут назад (с 2026-08-24T13:55:00), потолок 120 минут: в пределах"
	if msg != want {
		t.Fatalf("cmdElapsed = %q, ждал %q", msg, want)
	}
}

// TestCmdElapsedPastCeiling: этап открыт дольше потолка, команда велит сдать
// хвост, а не молчит про превышение.
func TestCmdElapsedPastCeiling(t *testing.T) {
	root := setup(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	old := timeNow
	defer func() { timeNow = old }()
	now := time.Date(2026, 8, 24, 11, 35, 0, 0, time.Local)
	timeNow = func() time.Time { return now }

	if err := stage.Open(home, root, "XR-005", stage.Dev, "", now.Add(-145*time.Minute)); err != nil {
		t.Fatal(err)
	}
	msg, err := cmdElapsed(root, "XR-005")
	if err != nil {
		t.Fatalf("cmdElapsed: %v", err)
	}
	want := "разработка открыта 145 минут назад (с 2026-08-24T09:10:00), потолок 120 минут пройден: сдавай хвост"
	if msg != want {
		t.Fatalf("cmdElapsed = %q, ждал %q", msg, want)
	}
}

// TestCmdElapsedCustomCeiling: потолок из окружения меняет вердикт при той же
// длительности этапа.
func TestCmdElapsedCustomCeiling(t *testing.T) {
	root := setup(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(execCeilingEnv, "10")
	old := timeNow
	defer func() { timeNow = old }()
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.Local)
	timeNow = func() time.Time { return now }

	if err := stage.Open(home, root, "XR-005", stage.Dev, "", now.Add(-15*time.Minute)); err != nil {
		t.Fatal(err)
	}
	msg, err := cmdElapsed(root, "XR-005")
	if err != nil {
		t.Fatalf("cmdElapsed: %v", err)
	}
	if !strings.Contains(msg, "потолок 10 минут пройден: сдавай хвост") {
		t.Fatalf("cmdElapsed с потолком из окружения = %q", msg)
	}
}

// TestElapsedNeedsID: подкоманда без ID отказывает, а не молчит про «этап не
// открыт» пустой строке.
func TestElapsedNeedsID(t *testing.T) {
	root := setup(t)
	out, err := runCLI(t, "-C", root, "elapsed")
	if err == nil {
		t.Fatalf("elapsed без ID принят молча:\n%s", out)
	}
	if !strings.Contains(out, "elapsed <ID>") {
		t.Fatalf("вместо подсказки по разбору: %s", out)
	}
}

// TestElapsedRefusesExtraPositional: лишний аргумент после ID это чаще всего
// потерянные кавычки, тот же случай, что и у move (TestExtraPositionalRefused).
func TestElapsedRefusesExtraPositional(t *testing.T) {
	root := setup(t)
	out, err := runCLI(t, "-C", root, "elapsed", "XR-005", "лишнее")
	if err == nil {
		t.Fatalf("лишний аргумент принят молча:\n%s", out)
	}
	if !strings.Contains(out, "лишний аргумент \"лишнее\"") {
		t.Fatalf("вместо отказа: %s", out)
	}
}
