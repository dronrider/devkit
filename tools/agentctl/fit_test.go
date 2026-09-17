package main

import (
	"strings"
	"testing"
	"time"
)

// TestFitTightBucket: сам порог сверки, без обхода через файлы. Бюджет больше
// остатка и бюджет ровно в четыре пятых остатка это tight, четверть остатка
// проходит молча.
func TestFitTightBucket(t *testing.T) {
	cases := []struct {
		name  string
		limit int
		left  int
		tight bool
	}{
		{"бюджет больше остатка", 40, 30, true},
		{"бюджет ровно в остаток", 40, 40, true},
		{"четыре пятых остатка это уже tight", 40, 50, true},
		{"чуть ниже четырёх пятых", 39, 50, false},
		{"четверть остатка", 10, 40, false},
		{"остатка не осталось вовсе", 1, 0, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := fitTightBucket(c.limit, c.left); got != c.tight {
				t.Fatalf("бюджет %d при остатке %d: жду tight=%v, получила %v", c.limit, c.left, c.tight, got)
			}
		})
	}
}

// TestBucketLeft: остаток считается целыми пунктами и вниз не уходит.
// Панель процентов больше сотни не показывает, но доля в снимке приходит
// числом, и перерасход должен читаться нулевым остатком, а не отрицательным:
// бюджет тогда не влезает ни при каком лимите.
func TestBucketLeft(t *testing.T) {
	cases := []struct {
		name string
		used float64
		left int
	}{
		{"половина окна", 0.5, 50},
		{"округление до пункта", 0.515, 48},
		{"бакет выбран целиком", 1, 0},
		{"перерасход это нулевой остаток", 1.2, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := bucketLeft(bucket{Name: "week_all", Used: c.used}); got != c.left {
				t.Fatalf("при доле %.3f жду остаток %d, получила %d", c.used, c.left, got)
			}
		})
	}
}

// TestCmdFitExhaustedBucket: бакет выбран целиком, до сброса не осталось
// ничего. Любой бюджет тогда съедает весь остаток, и сверка отвечает tight с
// нулём в строке.
func TestCmdFitExhaustedBucket(t *testing.T) {
	quota := isolateQuota(t)
	root := writeBoard(t)
	goal := goalFile(t, root, "T-100", goalText("бюджет: week_all <= 1\n", ""))
	writeSnapshot(t, quota, testNow.Add(-freshAge), bucketAt("week_all", 100, halfWindow))
	out, err := cmdFit(root, goal, testNow)
	if err != nil {
		t.Fatalf("fit по выбранному бакету: %v", err)
	}
	if !strings.HasPrefix(out, "fit: tight\n") {
		t.Fatalf("выбранный бакет прошёл за запас:\n%s", out)
	}
	if !strings.Contains(out, "бюджет 1 пп из остатка 0 пп") {
		t.Fatalf("нулевой остаток в строке не назван:\n%s", out)
	}
}

// TestCmdFitWarnsOnTightBudget: главный случай задачи. Бюджет week_all <= 40
// при остатке 49 пп формально влезает, но выбирает бакет почти целиком, и
// постановка говорит об этом с датой сброса.
func TestCmdFitWarnsOnTightBudget(t *testing.T) {
	quota := isolateQuota(t)
	root := writeBoard(t)
	goal := goalFile(t, root, "T-100", goalText("бюджет: week_all <= 40\n", ""))
	reset := testNow.Add(halfWindow)
	writeSnapshot(t, quota, testNow.Add(-freshAge), bucketAt("week_all", 51, halfWindow))
	out, err := cmdFit(root, goal, testNow)
	if err != nil {
		t.Fatalf("fit: %v", err)
	}
	if !strings.HasPrefix(out, "fit: tight\n") {
		t.Fatalf("тесный бюджет прошёл без предупреждения:\n%s", out)
	}
	for _, part := range []string{"бюджет 40 пп из остатка 49 пп", "сброс " + at(reset), "съедает почти весь остаток"} {
		if !strings.Contains(out, part) {
			t.Fatalf("в выводе нет %q:\n%s", part, out)
		}
	}
}

// TestCmdFitQuietOnRoomyBudget: остатка хватает с запасом, и сверка молчит.
func TestCmdFitQuietOnRoomyBudget(t *testing.T) {
	quota := isolateQuota(t)
	root := writeBoard(t)
	goal := goalFile(t, root, "T-100", goalText("бюджет: week_all <= 10\nбюджет: week_max <= 5\n", ""))
	writeSnapshot(t, quota, testNow.Add(-freshAge),
		bucketAt("week_all", 20, halfWindow), bucketAt("week_max", 10, halfWindow))
	out, err := cmdFit(root, goal, testNow)
	if err != nil {
		t.Fatalf("fit: %v", err)
	}
	if !strings.HasPrefix(out, "fit: ok\n") {
		t.Fatalf("запаса хватает, а сверка ругается:\n%s", out)
	}
	if strings.Contains(out, "съедает почти весь остаток") {
		t.Fatalf("предупреждение на запасе:\n%s", out)
	}
	if !strings.Contains(out, "week_max: бюджет 5 пп из остатка 90 пп") {
		t.Fatalf("второй бакет не посчитан:\n%s", out)
	}
}

// TestCmdFitWithoutSnapshot: снимка на машине нет. Сверка не падает и не врёт
// зелёным ответом: остаток неизвестен, и ответ это unknown с зовом снять
// снимок.
func TestCmdFitWithoutSnapshot(t *testing.T) {
	isolateQuota(t)
	root := writeBoard(t)
	goal := goalFile(t, root, "T-100", goalText("бюджет: week_all <= 40\n", ""))
	out, err := cmdFit(root, goal, testNow)
	if err != nil {
		t.Fatalf("fit без снимка: %v", err)
	}
	if !strings.HasPrefix(out, "fit: unknown\n") {
		t.Fatalf("пустой снимок сошёл за проверенный запас:\n%s", out)
	}
	for _, part := range []string{"снимка квоты нет", "agentctl quota refresh"} {
		if !strings.Contains(out, part) {
			t.Fatalf("в выводе нет %q:\n%s", part, out)
		}
	}
}

// TestCmdFitStaleSnapshot: протухший снимок сверку не роняет, числа считаются
// по нему, но строка зовёт переснять.
func TestCmdFitStaleSnapshot(t *testing.T) {
	quota := isolateQuota(t)
	root := writeBoard(t)
	goal := goalFile(t, root, "T-100", goalText("бюджет: week_all <= 40\n", ""))
	writeSnapshot(t, quota, testNow.Add(-staleAge), bucketAt("week_all", 51, halfWindow))
	out, err := cmdFit(root, goal, testNow)
	if err != nil {
		t.Fatalf("fit по протухшему снимку: %v", err)
	}
	if !strings.HasPrefix(out, "fit: tight\n") {
		t.Fatalf("вывод:\n%s", out)
	}
	if !strings.Contains(out, "остаток мог уйти") {
		t.Fatalf("про возраст снимка не сказано:\n%s", out)
	}
}

// TestCmdFitExpiredWindow: дата сброса позади, остаток в снимке от старого
// окна. Сравнивать с ним бюджет нечестно, и сверка честно отвечает unknown.
func TestCmdFitExpiredWindow(t *testing.T) {
	quota := isolateQuota(t)
	root := writeBoard(t)
	goal := goalFile(t, root, "T-100", goalText("бюджет: week_all <= 40\n", ""))
	writeSnapshot(t, quota, testNow.Add(-freshAge), bucketAt("week_all", 90, -time.Hour))
	out, err := cmdFit(root, goal, testNow)
	if err != nil {
		t.Fatalf("fit по старому окну: %v", err)
	}
	if !strings.HasPrefix(out, "fit: unknown\n") {
		t.Fatalf("остаток старого окна сошёл за нынешний:\n%s", out)
	}
	if !strings.Contains(out, "дата сброса") {
		t.Fatalf("про смену окна не сказано:\n%s", out)
	}
}

// TestCmdFitMissingBucket: бакет бюджета в снимке не показался. Молчать про
// это нельзя, пропущенный бакет неотличим от сверенного.
func TestCmdFitMissingBucket(t *testing.T) {
	quota := isolateQuota(t)
	root := writeBoard(t)
	goal := goalFile(t, root, "T-100", goalText("бюджет: week_all <= 10\nбюджет: week_max <= 5\n", ""))
	writeSnapshot(t, quota, testNow.Add(-freshAge), bucketAt("week_all", 20, halfWindow))
	out, err := cmdFit(root, goal, testNow)
	if err != nil {
		t.Fatalf("fit: %v", err)
	}
	if !strings.Contains(out, "бакета week_max в снимке нет") {
		t.Fatalf("пропущенный бакет прошёл молча:\n%s", out)
	}
}

// TestCmdFitWithoutLimits: раздела «Бюджет» в файле цели нет вовсе. Сверке
// нечего считать, и это отказ, а не зелёный ответ.
func TestCmdFitWithoutLimits(t *testing.T) {
	isolateQuota(t)
	root := writeBoard(t)
	goal := goalFile(t, root, "T-100", goalText("", ""))
	if _, err := cmdFit(root, goal, testNow); err == nil ||
		!strings.Contains(err.Error(), "сверять с остатком нечего") {
		t.Fatalf("жду отказ по пустому бюджету, получила %v", err)
	}
}
