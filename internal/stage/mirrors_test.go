package stage

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// Словарь этапов живёт здесь, а три места повторяют его словами, потому что
// импортировать go-пакет не могут: хук спавна пишет запись сам (hooks/stagerun.py),
// сторож прозы узнаёт строку этапа по ярлыку (hooks/check-prose.py, STAGE_LABELS),
// перечень машинных строк вычитки называет те же ярлыки читателю
// (kit/skills/proofread/machine-lines.md). Тест держит зеркала в согласии со
// словарём: новое слово, не доехавшее до сторожа, тот считал бы прозой, а
// вычитка переписала бы машинную строку (DK-911, замечание ревью).

var quoted = regexp.MustCompile(`"([^"]+)"`)

func repoFile(t *testing.T, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("зеркало словаря не прочлось: %v", err)
	}
	return string(data)
}

func sorted(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// capital строит ярлык так же, как Lines: слово словаря с заглавной буквы.
// Своей рукой, а не через Label, чтобы тест собирался и на коде до него и
// краснел по расхождению перечней, а не по сборке.
func capital(kind string) string {
	r := []rune(kind)
	return strings.ToUpper(string(r[0])) + string(r[1:])
}

// labelsWanted это ярлыки всех слов словаря плюс два слова прежнего: старые
// пакеты «Хода работы» их ещё несут, и сторож обязан узнавать и их.
func labelsWanted() map[string]bool {
	want := map[string]bool{}
	for _, k := range append(append([]string{}, Kinds...), Outside, Ask) {
		want[capital(k)] = true
	}
	return want
}

func TestDictionaryMirrorsMatchGo(t *testing.T) {
	t.Run("сторож прозы", func(t *testing.T) {
		src := repoFile(t, "hooks/check-prose.py")
		m := regexp.MustCompile(`(?s)STAGE_LABELS = \((.*?)\)`).FindStringSubmatch(src)
		if m == nil {
			t.Fatal("в hooks/check-prose.py не нашёлся кортеж STAGE_LABELS")
		}
		got := map[string]bool{}
		for _, q := range quoted.FindAllStringSubmatch(m[1], -1) {
			got[q[1]] = true
		}
		if want := labelsWanted(); strings.Join(sorted(got), ",") != strings.Join(sorted(want), ",") {
			t.Fatalf("STAGE_LABELS сторожа прозы разошёлся со словарём:\n сторож  %v\n словарь %v", sorted(got), sorted(want))
		}
	})
	t.Run("перечень машинных строк", func(t *testing.T) {
		src := repoFile(t, "kit/skills/proofread/machine-lines.md")
		for label := range labelsWanted() {
			if !strings.Contains(src, "«- "+label+":»") {
				t.Errorf("machine-lines.md не называет строку «- %s:»", label)
			}
		}
	})
	t.Run("писатель хука", func(t *testing.T) {
		src := repoFile(t, "hooks/stagerun.py")
		got := map[string]bool{}
		for _, ln := range strings.Split(src, "\n") {
			m := regexp.MustCompile(`^[A-Z_]+ = "([^"]+)"$`).FindStringSubmatch(ln)
			if m != nil && regexp.MustCompile(`\p{Cyrillic}`).MatchString(m[1]) {
				got[m[1]] = true
			}
		}
		want := map[string]bool{}
		for _, k := range Kinds {
			want[k] = true
		}
		if strings.Join(sorted(got), ",") != strings.Join(sorted(want), ",") {
			t.Fatalf("словарь stagerun.py разошёлся с go:\n хук     %v\n словарь %v", sorted(got), sorted(want))
		}
	})
}
