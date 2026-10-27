package taskform

import (
	"strings"
	"testing"
)

func scenarioFixture(t *testing.T) (string, Fork) {
	t.Helper()
	doc, err := AddForkSpec("## Развилки\n\n", Fork{Name: "сценарии", Question: "что включаем?", Who: ForkHuman, Mode: ScenarioMode, NextID: 4, Hint: "СЦ-1, СЦ-3: нужны выбор и продолжение", Options: []string{
		"СЦ-1 (слово пользователя): выбрать", "СЦ-2 (вывод из примера): собрать", "СЦ-3 (предложение агента): продолжить",
	}})
	if err != nil {
		t.Fatal(err)
	}
	f, _ := FindFork(doc, "сценарии")
	return doc, f
}

func TestScenariosFormatAndHistory(t *testing.T) {
	doc, f := scenarioFixture(t)
	if len(f.Choices()) != 3 || f.Choices()[0].Recommended {
		t.Fatal("рекомендация стала вариантом")
	}
	if finds := ForkFinds(doc); len(finds) > 0 {
		t.Fatal(finds)
	}
	for _, replace := range []struct{ from, to string }{
		{"СЦ-2 (вывод", "СЦ-1 (вывод"}, {"следующий ID: СЦ-4", "следующий ID: СЦ-3"},
		{"СЦ-2 (вывод из примера)", "СЦ-02 (вывод из примера)"}, {"СЦ-1, СЦ-3:", "СЦ-1, СЦ-9:"},
	} {
		if finds := ForkFinds(strings.Replace(doc, replace.from, replace.to, 1)); len(finds) == 0 {
			t.Errorf("не замечено: %+v", replace)
		}
	}
	doc, _ = AppendToFork(doc, f.Name, ForkDecisionLine(ByHuman, "2026-10-27", "СЦ-1"))
	doc, _ = AppendToFork(doc, f.Name, "  - требует сверки 2026-10-26: изменились условия")
	f, _ = FindFork(doc, f.Name)
	if f.Decided() || !f.HoldsStart() || f.State() != StateReview || f.Answer != "СЦ-1" {
		t.Fatalf("сверка: %+v", f)
	}
	doc, _ = AppendToFork(doc, f.Name, ForkDecisionLine(ByHuman, "2026-10-25", "СЦ-1"))
	f, _ = FindFork(doc, f.Name)
	if !f.Decided() || f.Review != "" || f.ChoiceEvent != 2 {
		t.Fatalf("подтверждение по порядку строк: %+v", f)
	}
	doc, _ = AppendToFork(doc, f.Name, "  - пересмотр человеком 2026-10-27: отказ от всех; требуется пересмотр границ")
	f, _ = FindFork(doc, f.Name)
	badHighWater := strings.Replace(doc, "следующий ID: СЦ-4", "следующий ID: СЦ-1", 1)
	if len(ForkFinds(badHighWater)) == 0 {
		t.Fatal("уменьшение следующего ID не замечено")
	}
	if f.Decided() || !f.HoldsStart() || f.ChoiceEvent != 3 {
		t.Fatalf("пересмотр: %+v", f)
	}
}

func TestScenarioSnapshotIntegrity(t *testing.T) {
	doc, f := scenarioFixture(t)
	snapshot, err := NewScenarioSnapshot("XR-1", f)
	if err != nil {
		t.Fatal(err)
	}
	key, raw := snapshot.Encoded()
	doc, _ = AppendToFork(doc, f.Name, "  - снимок: "+key+" "+raw)
	f, _ = FindFork(doc, f.Name)
	if _, err := ReadScenarioSnapshot(f, "XR-1", key); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadScenarioSnapshot(f, "XR-2", key); err == nil {
		t.Fatal("чужая запись принята")
	}
	if _, err := ReadScenarioSnapshot(f, "XR-1", key+"a"); err == nil {
		t.Fatal("неизвестный ключ принят")
	}
	damaged := strings.Replace(doc, `"question":"что включаем?"`, `"question":"подмена"`, 1)
	f, _ = FindFork(damaged, f.Name)
	if _, err := ReadScenarioSnapshot(f, "XR-1", key); err == nil {
		t.Fatal("повреждённый снимок принят")
	}
}
