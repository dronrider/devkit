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

// TestExecCeilingEnvOverride: окружение перебивает число лимита для стендов,
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
	if msg != "этап не открыт, лимит не проверить" {
		t.Fatalf("cmdElapsed без записи = %q", msg)
	}
}

// TestCmdElapsedWithinCeiling: этап открыт недавно, до лимита.
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
	want := "этап разработка открыт 46 минут назад (с 2026-08-24T13:55:00), лимит 120 минут: в пределах"
	if msg != want {
		t.Fatalf("cmdElapsed = %q, ждал %q", msg, want)
	}
}

// TestCmdElapsedPastCeiling: этап открыт дольше лимита, команда велит сдать
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
	want := "этап разработка открыт 145 минут назад (с 2026-08-24T09:10:00), лимит 120 минут пройден: сдавай хвост"
	if msg != want {
		t.Fatalf("cmdElapsed = %q, ждал %q", msg, want)
	}
}

// TestCmdElapsedCustomCeiling: лимит из окружения меняет вердикт при той же
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
	if !strings.Contains(msg, "лимит 10 минут пройден: сдавай хвост") {
		t.Fatalf("cmdElapsed с лимитом из окружения = %q", msg)
	}
}

// regcheck:test-begin
//
// TestCmdElapsedCountsFromLiveStage: регрессия DK-874. Команда брала начало
// последней «разработки» в пакете и не смотрела, жив ли этап: открытое следом
// ревью отсчёт не останавливало, и через два часа ревью диспетчер слышал
// «сдавай хвост» про заход, которого нет. Считается живой этап, каким бы он
// ни был.
func TestCmdElapsedCountsFromLiveStage(t *testing.T) {
	root := setup(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	old := timeNow
	defer func() { timeNow = old }()
	now := time.Date(2026, 9, 10, 15, 30, 0, 0, time.Local)
	timeNow = func() time.Time { return now }

	if err := stage.Open(home, root, "XR-005", stage.Dev, "", now.Add(-145*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := stage.Open(home, root, "XR-005", stage.Review, "", now.Add(-10*time.Minute)); err != nil {
		t.Fatal(err)
	}
	msg, err := cmdElapsed(root, "XR-005")
	if err != nil {
		t.Fatalf("cmdElapsed: %v", err)
	}
	want := "этап ревью открыт 10 минут назад (с 2026-09-10T15:20:00), лимит 120 минут: в пределах"
	if msg != want {
		t.Fatalf("cmdElapsed = %q, ждал %q", msg, want)
	}
	// Ожидание снаружи лимитом не меряется: сдавать хвост там некому.
	if err := stage.Open(home, root, "XR-005", stage.Outside, "", now.Add(-300*time.Minute)); err != nil {
		t.Fatal(err)
	}
	msg, err = cmdElapsed(root, "XR-005")
	if err != nil {
		t.Fatalf("cmdElapsed: %v", err)
	}
	want = "этап снаружи открыт 300 минут назад (с 2026-09-10T10:30:00), ожидание, лимит не считается"
	if msg != want {
		t.Fatalf("cmdElapsed у ожидания = %q, ждал %q", msg, want)
	}
}

// regcheck:test-end

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

// TestCmdElapsedClosedStage (DK-911, замечание ревью): этап исполнителя,
// закрытый его концом, лимитом не меряется, команда отвечает как без этапа.
func TestCmdElapsedClosedStage(t *testing.T) {
	root := setup(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	old := timeNow
	defer func() { timeNow = old }()
	now := time.Date(2026, 9, 20, 14, 0, 0, 0, time.Local)
	timeNow = func() time.Time { return now }
	s := stage.Stage{Kind: stage.Dev, Start: now.Add(-5 * time.Hour), End: now.Add(-3 * time.Hour), Note: "субагент opus/high по определению exec-high"}
	if err := stage.Put(home, root, "XR-005", s); err != nil {
		t.Fatal(err)
	}
	msg, err := cmdElapsed(root, "XR-005")
	if err != nil {
		t.Fatalf("cmdElapsed: %v", err)
	}
	want := "этап не открыт, лимит не проверить: этап разработка закрыт писателем 2026-09-20T11:00:00"
	if msg != want {
		t.Fatalf("cmdElapsed по закрытому этапу = %q, ждал %q", msg, want)
	}
}
