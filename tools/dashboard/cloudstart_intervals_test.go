package main

import (
	"os"
	"strings"
	"testing"
)

// Рабочие интервалы guardian (DK-1691): без подмены окружением скрипт
// получает продовые умолчания тик одна секунда и пауза удержания одна
// минута, а заданные значения доезжают в скрипт целиком. Сами переходы
// состояний проверяет TestCloudStartMissingTmuxParksWithOwner на сжатых
// интервалах, здесь защищены именно умолчания продовой паузы.
func TestGuardianScriptKeepsWorkIntervals(t *testing.T) {
	os.Unsetenv("DEVKIT_CLOUDSTART_TICK")
	os.Unsetenv("DEVKIT_CLOUDSTART_HOLD")
	script := guardianScript(cloudStartInterval("DEVKIT_CLOUDSTART_TICK", "1"), cloudStartInterval("DEVKIT_CLOUDSTART_HOLD", "60"))
	for _, want := range []string{"/bin/sleep 60\n", "/bin/sleep 1\n"} {
		if !strings.Contains(script, want) {
			t.Fatalf("рабочий интервал %q пропал из скрипта guardian", strings.TrimSpace(want))
		}
	}
	t.Setenv("DEVKIT_CLOUDSTART_TICK", "0.05")
	t.Setenv("DEVKIT_CLOUDSTART_HOLD", "0.2")
	script = guardianScript(cloudStartInterval("DEVKIT_CLOUDSTART_TICK", "1"), cloudStartInterval("DEVKIT_CLOUDSTART_HOLD", "60"))
	if !strings.Contains(script, "/bin/sleep 0.05\n") || !strings.Contains(script, "/bin/sleep 0.2\n") {
		t.Fatal("управляемые интервалы не доехали в скрипт guardian")
	}
}
