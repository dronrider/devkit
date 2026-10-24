package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// planKit пишет проектный слой шаблонов: команда сверяет имя суффикса с
// набором, и без него тест зависел бы от машины.
func planKit(t *testing.T, root string) {
	t.Helper()
	dir := filepath.Join(root, ".devkit", "plans")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "poc.toml"),
		[]byte("name = \"poc\"\ntitle = \"Режим POC\"\n[show]\ntitle = \"показ\"\nby = \"человек\"\ntrace = \"слово\"\n"),
		0o644); err != nil {
		t.Fatal(err)
	}
}

// TestPlanSetAndOff: суффикс ставится между взводом и приёмкой, повтор тем же
// именем узнаёт себя, снятие убирает, повторное снятие отказывает.
func TestPlanSetAndOff(t *testing.T) {
	root := setup(t)
	planKit(t, root)
	out, err := cmdPlan(root, "XR-004", PlanParams{Name: "poc"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "из шаблона poc") {
		t.Fatalf("печать не назвала шаблон: %q", out)
	}
	b, err := LoadBoard(boardPath(root))
	if err != nil {
		t.Fatal(err)
	}
	if got := b.find("XR-004").Title; got != "Хвост [план: poc]" {
		t.Fatalf("суффикс сел криво: %q", got)
	}
	again, err := cmdPlan(root, "XR-004", PlanParams{Name: "poc"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(again, "уже собирает") {
		t.Fatalf("повтор не назвал себя: %q", again)
	}
	if _, err := cmdPlan(root, "XR-004", PlanParams{Off: true}); err != nil {
		t.Fatal(err)
	}
	b, err = LoadBoard(boardPath(root))
	if err != nil {
		t.Fatal(err)
	}
	if got := b.find("XR-004").Title; got != "Хвост" {
		t.Fatalf("снятие оставило хвост: %q", got)
	}
	if _, err := cmdPlan(root, "XR-004", PlanParams{Off: true}); err == nil {
		t.Fatal("снятие без суффикса прошло молча")
	}
}

// TestPlanSetKeepsOtherTails: суффикс стоит после долга и взвода, до приёмки,
// и соседние команды разбор не ломают.
func TestPlanSetKeepsOtherTails(t *testing.T) {
	root := setup(t)
	planKit(t, root)
	if _, err := cmdDebt(root, "XR-004", DebtParams{}); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdPlan(root, "XR-004", PlanParams{Name: "poc"}); err != nil {
		t.Fatal(err)
	}
	b, err := LoadBoard(boardPath(root))
	if err != nil {
		t.Fatal(err)
	}
	if got := b.find("XR-004").Title; got != "Хвост [долг] [план: poc]" {
		t.Fatalf("суффикс встал не между долгом и приёмкой: %q", got)
	}
}

// TestPlanUnknownName: имя сверяется с набором в момент правки строки.
func TestPlanUnknownName(t *testing.T) {
	root := setup(t)
	planKit(t, root)
	_, err := cmdPlan(root, "XR-004", PlanParams{Name: "нетакого"})
	if err == nil || !strings.Contains(err.Error(), "есть poc") {
		t.Fatalf("чужое имя прошло молча: %v", err)
	}
	if _, err := cmdPlan(root, "XR-004", PlanParams{}); err == nil {
		t.Fatal("пустое имя прошло молча")
	}
}

// TestLintPlanNames: суффикс с именем, которого в наборе нет, видит lint.
func TestLintPlanNames(t *testing.T) {
	root := setup(t)
	planKit(t, root)
	b, err := LoadBoard(boardPath(root))
	if err != nil {
		t.Fatal(err)
	}
	row := b.find("XR-004")
	row.Title = "Хвост [план: нетакого]"
	b.updateLine(row.LineIdx, formatRow(row))
	finds := lintPlanNames(root, b, "docs/TASKS.md")
	if len(finds) != 1 || !strings.Contains(finds[0], "нетакого") {
		t.Fatalf("находки про чужое имя нет: %v", finds)
	}
	row.Title = "Хвост [план: poc]"
	b.updateLine(row.LineIdx, formatRow(row))
	if finds := lintPlanNames(root, b, "docs/TASKS.md"); len(finds) != 0 {
		t.Fatalf("живое имя дало находки: %v", finds)
	}
}
