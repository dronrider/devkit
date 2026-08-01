package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Пункт 20 скелета DK-033: появление профилей харнеса не двигает вердикт pick
// ничем. Снимок вердиктов по фикстурной доске снят с кода до правки и лежит
// в testdata; тест пересчитывает его и сверяет побайтно. Условия зафиксированы
// целиком, иначе сверять было бы нечего: часы стоят, снимка квоты нет,
// машинного конфига харнесов нет, путь домашней директории в тексте заменён на
// тильду.
func pickVerdicts(t *testing.T) string {
	t.Helper()
	root := writeBoard(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	fixNow(t, time.Date(2026, 8, 1, 12, 0, 0, 0, time.Local))
	var b strings.Builder
	for _, id := range []string{"T-001", "T-002", "T-003", "T-004", "T-005", "T-006"} {
		for _, role := range []string{roleExec, roleReview} {
			out, err := cmdPick(root, id, false, role)
			if err != nil {
				t.Fatalf("pick %s --role %s: %v", id, role, err)
			}
			b.WriteString("### " + id + " " + role + "\n" + strings.ReplaceAll(out, home, "~") + "\n")
		}
	}
	return b.String()
}

func TestPickVerdictsUnchanged(t *testing.T) {
	golden := filepath.Join("testdata", "pick-verdicts.txt")
	if os.Getenv("DEVKIT_WRITE_GOLDEN") != "" {
		if err := os.WriteFile(golden, []byte(pickVerdicts(t)), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if got := pickVerdicts(t); got != string(want) {
		t.Fatalf("вердикты разошлись со снимком %s\nбыло:\n%s\nстало:\n%s", golden, want, got)
	}
}
