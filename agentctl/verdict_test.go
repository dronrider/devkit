package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// Пункт 20 скелета DK-033: перевод внутренних таблиц на ярусы не двигает
// вердикт pick ничем. Снимок вердиктов по фикстурной доске снят с кода до
// появления профилей харнеса и лежит в testdata; тест пересчитывает его и
// сверяет побайтно. Условия зафиксированы целиком, иначе сверять было бы
// нечего: часы стоят, снимка квоты нет, машинного конфига харнесов нет,
// профили берутся из репозитория, путь домашней директории в тексте заменён на
// тильду.
func pickVerdicts(t *testing.T) string {
	t.Helper()
	root := writeBoard(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("DEVKIT_HARNESS", "")
	t.Setenv("DEVKIT_HOME", repoRoot(t))
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

var tierWord = regexp.MustCompile(`\b(mini|base|pro|max)\b`)

var modelOfTier = map[string]string{
	tierMini: "haiku", tierBase: "sonnet", tierPro: "opus", tierMax: "fable",
}

// asModelVerdicts переводит сегодняшний вывод обратно на язык имён моделей:
// строка tier снимается, слова ярусов в человеческой строке разворачиваются по
// таблице псевдонимов. Иначе сверять новый вывод было бы не с чем: снимок с
// новой строкой сам по себе ничего не доказывает, он снят с уже правленого
// кода. Так снимок остаётся тем же файлом, что и до DK-037, а тест доказывает
// ровно обещанное дизайном: кроме переименования ступеней и третьей строки в
// вердиктах не двинулось ничего.
func asModelVerdicts(s string) string {
	var out []string
	for _, ln := range strings.Split(s, "\n") {
		if strings.HasPrefix(ln, "tier: ") {
			continue
		}
		out = append(out, tierWord.ReplaceAllStringFunc(ln, func(w string) string { return modelOfTier[w] }))
	}
	return strings.Join(out, "\n")
}

func TestPickVerdictsUnchanged(t *testing.T) {
	golden := filepath.Join("testdata", "pick-verdicts.txt")
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if got := asModelVerdicts(pickVerdicts(t)); got != string(want) {
		t.Fatalf("вердикты разошлись со снимком %s\nбыло:\n%s\nстало:\n%s", golden, want, got)
	}
}

// TestPickVerdictsTiers держит сегодняшний вывод целиком: строку tier, порядок
// машинных строк и ярусные формулировки причин. Снимок из теста выше их не
// видит по построению, он смотрит на вывод через перевод в старые имена.
func TestPickVerdictsTiers(t *testing.T) {
	golden := filepath.Join("testdata", "pick-verdicts-tiers.txt")
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
