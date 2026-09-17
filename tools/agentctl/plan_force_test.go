package main

import "testing"

func TestPlanSetForceReplacesForeign(t *testing.T) {
	home := t.TempDir()
	// Второй выход из отказа: --force кладёт набор поверх, состояния совпавших
	// пунктов при этом переносятся как обычно.
	env := planEnv(map[string]string{planEnvSession: "s1"})
	if _, err := cmdPlan(home, "set", []string{"разведка\nправка"}, "", "", env); err != nil {
		t.Fatal(err)
	}
	if _, err := planCmd(home, "set", []string{"вычитка"}, "", "", true, env); err != nil {
		t.Fatalf("set с --force получил отказ: %v", err)
	}
	got := planFile(t, home, "s1.json")
	if len(got) != 1 || got[0].Text != "вычитка" {
		t.Fatalf("--force не положил план: %+v", got)
	}
}
