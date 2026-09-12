package main

import (
	"os"
	"strings"
	"testing"
)

// TestChainLevels: сценарий решения 4 LLD DK-933 (просьба «после DK-A сделай
// DK-B и DK-C, потом DK-D») на фикстуре доски. --dry-run печатает план тем же
// текстом и не трогает файл доски, а реальный вызов кладёт рёбра и взвод
// одним проходом.
func TestChainLevels(t *testing.T) {
	root := setup(t)

	before, err := os.ReadFile(boardPath(root))
	if err != nil {
		t.Fatal(err)
	}
	out, err := cmdChain(root, ChainParams{
		After:  []string{"XR-005"},
		Levels: [][]string{{"XR-001", "XR-003"}, {"XR-004"}},
		DryRun: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := "уровень 1: XR-001 [после XR-005] [взвод]\n" +
		"уровень 1: XR-003 [после XR-005] [взвод]\n" +
		"уровень 2: XR-004 [после XR-001, XR-003] [взвод]"
	if out != want {
		t.Fatalf("dry-run план: %q, ожидал %q", out, want)
	}
	after, err := os.ReadFile(boardPath(root))
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("dry-run тронул доску:\n%s", after)
	}

	out, err = cmdChain(root, ChainParams{
		After:  []string{"XR-005"},
		Levels: [][]string{{"XR-001", "XR-003"}, {"XR-004"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "уровень 1: XR-001 [после XR-005] [взвод]") ||
		!strings.Contains(out, "уровень 1: XR-003 [после XR-005] [взвод]") ||
		!strings.Contains(out, "уровень 2: XR-004 [после XR-001, XR-003] [взвод]") {
		t.Fatalf("сообщение chain: %q", out)
	}
	if got := backlogTitle(t, root, "XR-001"); got != "Средняя [после XR-005] [взвод]" {
		t.Fatalf("заголовок XR-001: %q", got)
	}
	if got := backlogTitle(t, root, "XR-003"); got != "Та же R, больший ID [после XR-005] [взвод]" {
		t.Fatalf("заголовок XR-003: %q", got)
	}
	if got := backlogTitle(t, root, "XR-004"); got != "Хвост [после XR-001, XR-003] [взвод]" {
		t.Fatalf("заголовок XR-004: %q", got)
	}
}

// TestChainFirstLevelWithoutAfter: без --after первый уровень получает только
// взвод, без ребра, и стартует ближайшим обходом.
func TestChainFirstLevelWithoutAfter(t *testing.T) {
	root := setup(t)
	if _, err := cmdChain(root, ChainParams{Levels: [][]string{{"XR-002"}}}); err != nil {
		t.Fatal(err)
	}
	if got := backlogTitle(t, root, "XR-002"); got != "Верхняя [взвод]" {
		t.Fatalf("заголовок XR-002: %q", got)
	}
}

// TestChainCycleRefusesWithoutWrite: цепочка, замыкающая цикл (то же ребро,
// что цикл у dep add), отказывает раньше первой записи на доску.
func TestChainCycleRefusesWithoutWrite(t *testing.T) {
	root := setup(t)
	before, err := os.ReadFile(boardPath(root))
	if err != nil {
		t.Fatal(err)
	}
	_, err = cmdChain(root, ChainParams{
		After:  []string{"XR-001"},
		Levels: [][]string{{"XR-003"}, {"XR-001"}},
	})
	if err == nil || !strings.Contains(err.Error(), "цикл") {
		t.Fatalf("цикл должен отбиваться: %v", err)
	}
	after, err := os.ReadFile(boardPath(root))
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("отказ цикла тронул доску:\n%s", after)
	}
}

// TestChainArmRefusalRefusesWithoutWrite: строка, которой arm отказывает
// (тут: цена XL), даёт отказ всей цепочки без записи на доску, даже если
// другие строки уровня были бы готовы.
func TestChainArmRefusalRefusesWithoutWrite(t *testing.T) {
	root := setup(t)
	if _, err := cmdSet(root, SetParams{ID: "XR-004", Cost: "XL"}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(boardPath(root))
	if err != nil {
		t.Fatal(err)
	}
	_, err = cmdChain(root, ChainParams{Levels: [][]string{{"XR-002", "XR-004"}}})
	if err == nil || !strings.Contains(err.Error(), "XL") {
		t.Fatalf("отказ arm ценой XL должен всплыть: %v", err)
	}
	after, err := os.ReadFile(boardPath(root))
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("отказ arm тронул доску:\n%s", after)
	}
}

func TestSplitChainIDs(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"DK-B DK-C", []string{"DK-B", "DK-C"}},
		{"DK-B, DK-C", []string{"DK-B", "DK-C"}},
		{"DK-B,DK-C", []string{"DK-B", "DK-C"}},
		{"DK-A", []string{"DK-A"}},
	}
	for _, c := range cases {
		got := splitChainIDs(c.in)
		if strings.Join(got, "|") != strings.Join(c.want, "|") {
			t.Fatalf("splitChainIDs(%q) = %v, ожидал %v", c.in, got, c.want)
		}
	}
}
