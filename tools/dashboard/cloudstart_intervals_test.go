package main

import (
	"strings"
	"testing"
)

// Рабочие интервалы guardian (DK-1691): без подмены окружением скрипт
// получает продовые умолчания тик одна секунда и пауза удержания одна
// минута, а заданные значения доезжают в скрипт целиком. Мусорное
// значение отвергается на старте с именем переменной, а не роняет
// /bin/sleep в каждой итерации. Сами переходы состояний проверяет
// TestCloudStartMissingTmuxParksWithOwner на сжатых интервалах, здесь
// защищены именно умолчания продовой паузы.
func TestGuardianScriptKeepsWorkIntervals(t *testing.T) {
	tick, err := cloudStartInterval("DEVKIT_CLOUDSTART_TICK", "1")
	if err != nil {
		t.Fatalf("умолчание тика: %v", err)
	}
	hold, err := cloudStartInterval("DEVKIT_CLOUDSTART_HOLD", "60")
	if err != nil {
		t.Fatalf("умолчание паузы: %v", err)
	}
	script := guardianScript(tick, hold)
	for _, want := range []string{"/bin/sleep 60\n", "/bin/sleep 1\n"} {
		if !strings.Contains(script, want) {
			t.Fatalf("рабочий интервал %q пропал из скрипта guardian", strings.TrimSpace(want))
		}
	}
	t.Setenv("DEVKIT_CLOUDSTART_TICK", "0.05")
	t.Setenv("DEVKIT_CLOUDSTART_HOLD", "0.2")
	tick, err = cloudStartInterval("DEVKIT_CLOUDSTART_TICK", "1")
	if err != nil {
		t.Fatalf("подмена тика: %v", err)
	}
	hold, err = cloudStartInterval("DEVKIT_CLOUDSTART_HOLD", "60")
	if err != nil {
		t.Fatalf("подмена паузы: %v", err)
	}
	script = guardianScript(tick, hold)
	if !strings.Contains(script, "/bin/sleep 0.05\n") || !strings.Contains(script, "/bin/sleep 0.2\n") {
		t.Fatal("управляемые интервалы не доехали в скрипт guardian")
	}
}

func TestCloudStartIntervalRejectsGarbage(t *testing.T) {
	t.Setenv("DEVKIT_CLOUDSTART_TICK", "0,05")
	if _, err := cloudStartInterval("DEVKIT_CLOUDSTART_TICK", "1"); err == nil || !strings.Contains(err.Error(), "DEVKIT_CLOUDSTART_TICK") {
		t.Fatalf("мусорное значение тика отвергнуто без имени переменной: %v", err)
	}
	t.Setenv("DEVKIT_CLOUDSTART_HOLD", "минута")
	if _, err := cloudStartInterval("DEVKIT_CLOUDSTART_HOLD", "60"); err == nil || !strings.Contains(err.Error(), "DEVKIT_CLOUDSTART_HOLD") {
		t.Fatalf("мусорное значение паузы отвергнуто без имени переменной: %v", err)
	}
}
