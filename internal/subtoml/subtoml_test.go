package subtoml

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestFixtures: те же фикстуры, что у профилей харнесов
// (kit/harness/testdata), их читает и питоновская реализация
// (tools/devkitctl/harness_test.py). После переезда парсера из agentctl в
// internal фикстуры остались на месте, и сверка тут доказывает, что переезд
// ничего не сдвинул: отказ слово в слово тот же, канонический дамп тот же.
func TestFixtures(t *testing.T) {
	dir := filepath.Join("..", "..", "kit", "harness", "testdata")
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, e := range ents {
		name := e.Name()
		if !strings.HasPrefix(name, "parse-") || !strings.HasSuffix(name, ".toml") {
			continue
		}
		seen++
		t.Run(name, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil {
				t.Fatal(err)
			}
			want, err := os.ReadFile(filepath.Join(dir, strings.TrimSuffix(name, ".toml")+".expected"))
			if err != nil {
				t.Fatal(err)
			}
			var got string
			d, perr := Parse(name, string(data))
			if perr != nil {
				got = "error: " + perr.Error() + "\n"
			} else {
				got = d.Dump()
			}
			if got != string(want) {
				t.Fatalf("разбор разошёлся с фикстурой\nжду:\n%s\nвижу:\n%s", want, got)
			}
		})
	}
	if seen < 10 {
		t.Fatalf("фикстур найдено %d, набор потерялся", seen)
	}
}

// TestOrderKeepsSections: порядок секций это контракт шаблона плана, по нему
// идут этапы. Карта в Go не упорядочена, и без Order порядок ехал бы от прогона
// к прогону разный.
func TestOrderKeepsSections(t *testing.T) {
	d, err := Parse("plan.toml", "name = \"x\"\n[work]\ntitle = \"a\"\n[tests]\ntitle = \"b\"\n[review]\ntitle = \"c\"\n")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"", "work", "tests", "review"}
	if len(d.Order) != len(want) {
		t.Fatalf("секций %d, жду %d: %v", len(d.Order), len(want), d.Order)
	}
	for i := range want {
		if d.Order[i] != want[i] {
			t.Fatalf("порядок секций %v, жду %v", d.Order, want)
		}
	}
	if d.Table("tests").Str("title") != "b" {
		t.Fatalf("значение секции [tests] прочитано как %q", d.Table("tests").Str("title"))
	}
}
