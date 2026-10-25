package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dronrider/devkit/internal/stage"
)

// devStage кладёт задаче незакрытый этап разработки названной моделью: по нему
// расчёт проверяющего узнаёт автора правки.
func devStage(t *testing.T, root, id, model string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, "docs", "tasks"), 0o755); err != nil {
		t.Fatal(err)
	}
	doc := "# " + id + "\n\n## Ход работы\n\n"
	if err := os.WriteFile(filepath.Join(root, "docs", "tasks", id+".md"), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	note := "субагент " + model + "/high по вердикту pick"
	if err := stage.Open(stage.Home(), stage.MainRoot(root), id, stage.Dev, note, time.Now()); err != nil {
		t.Fatal(err)
	}
}

// TestCmdCheckStepsOverDevModel: сценарий прогоняет не автор правки, и на
// задаче ценой M вердикт роли ревью приходит тем же ярусом base, каким шла
// разработка. Расчёт проверяющего обязан поднять ступень, иначе поднятый по
// нему субагент отработает впустую, а taskctl close его прогон отобьёт
// (DK-1116).
func TestCmdCheckStepsOverDevModel(t *testing.T) {
	isolateQuota(t)
	root := writeBoard(t)
	devStage(t, root, "T-007", "sonnet")
	out, err := cmdCheck(root, "T-007")
	if err != nil {
		t.Fatalf("check T-007: %v", err)
	}
	for _, want := range []string{"model: opus", "tier: pro", "ярус base поднят ступенью до pro", "разработку вёл sonnet"} {
		if !strings.Contains(out, want) {
			t.Fatalf("в расчёте нет %q:\n%s", want, out)
		}
	}
}

// Разработку вела другая модель, значит ступень не нужна: проверяющий идёт
// ярусом вердикта роли ревью.
func TestCmdCheckKeepsTierOnForeignDevModel(t *testing.T) {
	isolateQuota(t)
	root := writeBoard(t)
	devStage(t, root, "T-007", "opus")
	out, err := cmdCheck(root, "T-007")
	if err != nil {
		t.Fatalf("check T-007: %v", err)
	}
	if !strings.Contains(out, "model: sonnet") || !strings.Contains(out, "tier: base") {
		t.Fatalf("ярус вердикта не удержан:\n%s", out)
	}
	if strings.Contains(out, "ступенью") {
		t.Fatalf("ступень поднята без совпадения моделей:\n%s", out)
	}
}

// Исполнителя разработки записи не назвали: сверять не с кем, ступени нет, и
// это сказано словами.
func TestCmdCheckWithoutDevRecord(t *testing.T) {
	isolateQuota(t)
	root := writeBoard(t)
	out, err := cmdCheck(root, "T-007")
	if err != nil {
		t.Fatalf("check T-007: %v", err)
	}
	if !strings.Contains(out, "model: sonnet") || !strings.Contains(out, "исполнителя разработки записи не назвали") {
		t.Fatalf("молчание записей не названо:\n%s", out)
	}
}

// Лестница исчерпана: разработку вела модель верхнего яруса, поднимать другой
// некем, и команда отказывает вместо того, чтобы назвать автора правки.
func TestCmdCheckRefusesOnExhaustedLadder(t *testing.T) {
	isolateQuota(t)
	root := writeBoard(t)
	devStage(t, root, "T-007", "fable")
	doc := "# T-007\n\nМодель: max\n\n## Ход работы\n\n"
	if err := os.WriteFile(filepath.Join(root, "docs", "tasks", "T-007.md"), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := cmdCheck(root, "T-007")
	if err == nil {
		t.Fatal("расчёт на исчерпанной лестнице обязан отказывать")
	}
	for _, want := range []string{"прогонять некем", "fable", "ступени выше"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("в отказе нет %q: %v", want, err)
		}
	}
}

func TestCmdCheckChangesEnabledHarnessForIndependence(t *testing.T) {
	setupLadder(t, `default = "claude-code"
enabled = ["glm-code", "claude-code"]
auto = ["glm-code"]
[glm-code]
mini = "glm-5.3"
base = "glm-5.3"
pro = "glm-5.3"
max = "glm-5.3"
[claude-code]
mini = "z-ai/glm-5.3"
base = "z-ai/glm-5.3"
pro = "sonnet"
max = "opus"
`)
	t.Setenv("DEVKIT_HARNESS", "glm-code")
	root := writeBoard(t)
	devStage(t, root, "T-007", "glm-5.3")
	out, err := cmdCheck(root, "T-007")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"model: sonnet", "tier: pro", "harness: claude-code", "подписка claude-code"} {
		if !strings.Contains(out, want) {
			t.Fatalf("нет %s: %s", want, out)
		}
	}
}

// copyProfile кладёт настоящий профиль харнеса в стендовую директорию
// DEVKIT_HOME: тесты допуска берут настоящие профили, а не сочинённые.
func copyProfile(t *testing.T, name string) {
	t.Helper()
	src, err := os.ReadFile(filepath.Join(repoRoot(t), profileDirGroup, profileDirName, name))
	if err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(os.Getenv("DEVKIT_HOME"), profileDirGroup, profileDirName, name)
	if err := os.WriteFile(dst, src, 0o644); err != nil {
		t.Fatal(err)
	}
}

// policyMachine это раскладка на три подписки: две лестницы claude-code и
// glm-code плюс routerai с независимыми моделями. Ключ auto решает, кому из
// вторичных разрешены автоматические назначения.
const policyMachine = `default = "claude-code"
enabled = ["glm-code", "claude-code", "routerai"]
%s[glm-code]
mini = "glm-5.3"
base = "glm-5.3"
pro = "glm-5.3"
max = "glm-5.3"
[claude-code]
mini = "haiku"
base = "sonnet"
pro = "opus"
max = "fable"
[routerai]
mini = "route-mini"
base = "route-base"
pro = "route-pro"
max = "route-max"
`

// setupPolicy ставит раскладку с названным ключом auto и докладывает профиль
// routerai: без него включённая подписка молча выпала бы из раскладки, и тест
// проверял бы не запрет, а отсутствие подписки.
func setupPolicy(t *testing.T, auto string) {
	t.Helper()
	setupLadder(t, fmt.Sprintf(policyMachine, auto))
	copyProfile(t, "routerai.toml")
}

// TestTaskHarnessPolicy это допуск подписок к автоматическим назначениям
// (DK-1453). Включённая routerai без ключа auto остаётся подпиской ручных
// чатов: независимый расчёт её не поднимает, даже когда она активная, а
// исчерпанная лестница допущенных подписок отказывает словами вместо обхода
// запрета. Явное разрешение возвращает подписку в отбор.
func TestTaskHarnessPolicy(t *testing.T) {
	t.Run("запрещённая не поднимается ни активной, ни запасной", func(t *testing.T) {
		setupPolicy(t, "")
		t.Setenv("DEVKIT_HARNESS", "routerai")
		root := writeBoard(t)
		devStage(t, root, "T-007", "sonnet")
		out, err := cmdCheck(root, "T-007")
		if err != nil {
			t.Fatal(err)
		}
		// Ярус вердикта base у claude-code это sonnet, он же вёл разработку:
		// ступень до pro, а routerai в отборе нет вовсе.
		for _, want := range []string{"model: opus", "tier: pro", "harness: claude-code"} {
			if !strings.Contains(out, want) {
				t.Fatalf("нет %s: %s", want, out)
			}
		}
		if strings.Contains(out, "harness: routerai") || strings.Contains(out, "via: routerai") {
			t.Fatalf("запрещённая подписка прошла в отбор: %s", out)
		}
	})

	t.Run("после явного разрешения участвует", func(t *testing.T) {
		setupPolicy(t, "auto = [\"routerai\"]\n")
		t.Setenv("DEVKIT_HARNESS", "routerai")
		root := writeBoard(t)
		devStage(t, root, "T-007", "sonnet")
		out, err := cmdCheck(root, "T-007")
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{"model: route-base", "tier: base", "harness: routerai"} {
			if !strings.Contains(out, want) {
				t.Fatalf("нет %s: %s", want, out)
			}
		}
	})

	t.Run("исчерпанные допущенные дают отказ, а не обход запрета", func(t *testing.T) {
		setupPolicy(t, "")
		root := writeBoard(t)
		devStage(t, root, "T-007", "fable")
		doc := "# T-007\n\nМодель: max\n\n## Ход работы\n\n"
		if err := os.WriteFile(filepath.Join(root, "docs", "tasks", "T-007.md"), []byte(doc), 0o644); err != nil {
			t.Fatal(err)
		}
		_, err := cmdCheck(root, "T-007")
		if err == nil {
			t.Fatal("исчерпанная лестница обязана отказывать")
		}
		if !strings.Contains(err.Error(), "прогонять некем") || strings.Contains(err.Error(), "routerai") {
			t.Fatalf("отказ обошёлся запрещённой подпиской: %v", err)
		}
	})

	t.Run("ступени запрещённого via без модели отдают отказ", func(t *testing.T) {
		setupLadder(t, `default = "claude-code"
enabled = ["claude-code", "routerai"]
[claude-code]
mini = "routerai:route-mini"
base = "routerai:route-base"
pro = "routerai:route-pro"
max = "routerai:route-max"
[routerai]
mini = "route-mini"
base = "route-base"
pro = "route-pro"
max = "route-max"
`)
		copyProfile(t, "routerai.toml")
		root := writeBoard(t)
		// Записи разработки нет, яруса не разворачивает ни одна допущенная
		// ступень: отказ обязан назвать допуск, а не прочерк с чужой подпиской.
		_, err := cmdCheck(root, "T-007")
		if err == nil {
			t.Fatal("ступени запрещённого via обязаны отказывать")
		}
		for _, want := range []string{"прогонять некем", "автоматическим назначениям"} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("в отказе нет %q: %v", want, err)
			}
		}
	})

	t.Run("машинный вид печатает допуск, ручной выбор не трогая", func(t *testing.T) {
		setupPolicy(t, "auto = [\"routerai\"]\n")
		out, err := cmdHarnessJSON(".")
		if err != nil {
			t.Fatal(err)
		}
		var view struct {
			Harnesses []struct {
				Name    string `json:"name"`
				Enabled bool   `json:"enabled"`
				Default bool   `json:"default"`
				Auto    bool   `json:"auto"`
			} `json:"harnesses"`
		}
		if err := json.Unmarshal([]byte(out), &view); err != nil {
			t.Fatal(err)
		}
		enabled, admitted := map[string]bool{}, map[string]bool{}
		for _, h := range view.Harnesses {
			enabled[h.Name], admitted[h.Name] = h.Enabled, h.Auto
		}
		for _, name := range []string{"claude-code", "glm-code", "routerai"} {
			if !enabled[name] {
				t.Fatalf("%s выпал из включённых: ручной выбор допуск не трогает", name)
			}
		}
		if !admitted["claude-code"] || !admitted["routerai"] || admitted["glm-code"] {
			t.Fatalf("допуск разошёлся с ключом auto: %v", admitted)
		}
	})
}
