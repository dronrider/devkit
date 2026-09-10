package main

import (
	"testing"
	"strings"
)

// TestDepSidesCaching проверяет, что depSides кешируется и не пересчитывается
// при повторных вызовах на одной и той же доске.
func TestDepSidesCaching(t *testing.T) {
	boardTxt := `## In progress (2)
| ID | Заголовок | Тип | P | R | Цена |
|-|-|-|-|-|-|
| OB-1 | задача 1 | task | P1 | 10 (5+2+1+0+2) | S |
| OB-2 | задача 2 [после OB-1] | task | P1 | 10 (5+2+1+0+2) | S |

## Check (0)
Нет.

## Backlog (0)
Нет.

## Blocked (0)
Нет.
`

	b, err := parseLines("test.md", strings.Split(boardTxt, "\n"))
	if err != nil {
		t.Fatalf("parseLines failed: %v", err)
	}

	// Первый вызов должен пересчитать кеш
	sides1 := depSides(b)
	if b.depSidesCache == nil {
		t.Error("depSidesCache should be non-nil after first call")
	}

	// Второй вызов должен вернуть кешированный результат (указатель на тот же объект)
	sides2 := depSides(b)
	if len(sides1) != len(sides2) {
		t.Error("depSides should return the same data on second call")
	}

	// Проверим, что зависимости правильно вычислены
	if len(sides1) == 0 {
		t.Error("depSides should return non-empty map")
	}

	// Проверим OB-2: он должен зависеть от OB-1
	if s := sides1["OB-2"]; s == nil || len(s.after) != 1 || s.after[0] != "OB-1" {
		t.Errorf("OB-2 should depend on OB-1, got %+v", s)
	}

	// Проверим OB-1: он должен держать OB-2
	if s := sides1["OB-1"]; s == nil || len(s.blocks) != 1 || s.blocks[0] != "OB-2" {
		t.Errorf("OB-1 should block OB-2, got %+v", s)
	}

	// Инвалидируем кеш и проверим, что он пересчитывается
	b.invalidateDepSidesCache()
	if b.depSidesCache != nil {
		t.Error("depSidesCache should be nil after invalidation")
	}

	sides3 := depSides(b)
	if b.depSidesCache == nil {
		t.Error("depSidesCache should be non-nil after calling depSides after invalidation")
	}

	// Результаты должны быть одинаковыми
	s1 := sides1["OB-2"]
	s3 := sides3["OB-2"]
	if s1.after[0] != s3.after[0] {
		t.Error("depSides results should be the same after invalidation and recalculation")
	}
}
