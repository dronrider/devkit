package taskform

import (
	"reflect"
	"testing"
)

func TestScenarioAnswerSelection(t *testing.T) {
	_, f := scenarioFixture(t)
	snapshot, _ := NewScenarioSnapshot("XR-1", f)
	for _, c := range []struct {
		text  string
		ids   []string
		added string
	}{
		{"сценарии 1, 3, СЦ-1", []string{"СЦ-1", "СЦ-3"}, ""},
		{"минус СЦ-3; плюс 2", []string{"СЦ-1", "СЦ-2"}, ""},
		{"сценарии 2; минус 2; плюс СЦ-3", []string{"СЦ-3"}, ""},
		{`плюс "увидеть причину отказа, затем исправить ввод"`, []string{"СЦ-1", "СЦ-3", "СЦ-4"}, "увидеть причину отказа, затем исправить ввод"},
		{`сценарии 1; плюс "путь \\ и слово \"да\""`, []string{"СЦ-1", "СЦ-4"}, `путь \ и слово "да"`},
		{"минус 1; минус 3", []string{}, ""},
		{"минус 1, 3", []string{}, ""},
		{"ничего", []string{}, ""},
	} {
		t.Run(c.text, func(t *testing.T) {
			got, err := ParseScenarioAnswer(f, snapshot, c.text)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got.IDs, c.ids) {
				t.Fatalf("выбрано %v, нужно %v", got.IDs, c.ids)
			}
			if c.added != "" && (len(got.Added) != 1 || got.Added[0].Title != c.added || got.NextID != 5) {
				t.Fatalf("добавление %+v", got)
			}
		})
	}
	for _, text := range []string{"", " ", "сценарии 1, 99, плюс новый", "СЦ-9", "плюс 9999999999999999999999999999999", `плюс "незакрыто`} {
		if _, err := ParseScenarioAnswer(f, snapshot, text); err == nil {
			t.Errorf("принят плохой ответ %q", text)
		}
	}
	f.Reconsidered = true
	f.ChoiceEvent = 1
	snapshot.Current = []string{}
	snapshot.ChoiceEvent = 1
	if _, err := ParseScenarioAnswer(f, snapshot, "плюс 2"); err == nil {
		t.Fatal("рекомендация подставлена после отказа")
	}
	if got, err := ParseScenarioAnswer(f, snapshot, "по рекомендации; плюс 2"); err != nil || len(got.IDs) != 3 {
		t.Fatalf("явная основа: %+v %v", got, err)
	}
	got, err := ParseScenarioAnswer(f, snapshot, "сценарии 1, обсудим, по рекомендации")
	if err != nil || len(got.Unparsed) != 1 || len(got.Added) != 0 {
		t.Fatalf("свободный остаток: %+v %v", got, err)
	}
}

func TestScenarioTitleOnlyAllowsQuoteAndSlashEscapes(t *testing.T) {
	_, f := scenarioFixture(t)
	snapshot, _ := NewScenarioSnapshot("XR-1", f)
	for _, text := range []string{`плюс "проверить путь C:\tmp"`, `плюс "буква \u0041"`, `плюс "буква \x41"`} {
		if _, err := ParseScenarioAnswer(f, snapshot, text); err == nil {
			t.Fatalf("неразрешённый escape принят: %s", text)
		}
	}
	got, err := ParseScenarioAnswer(f, snapshot, `плюс "проверить C:\\tmp и \"кавычку\""`)
	if err != nil || got.Added[0].Title != `проверить C:\tmp и "кавычку"` {
		t.Fatalf("разрешённые escape: %+v %v", got, err)
	}
}
