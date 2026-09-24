package rehearsal

import (
	"strings"
	"testing"

	"github.com/dronrider/devkit/internal/taskform"
)

// doc собирает файл задачи со сценарием и заданным хвостом.
func doc(tail string) string {
	return "# XR-001\n\n## Сценарий проверки\n\n1. Запустить.\n" + tail
}

func stamp(scenario, sha string) string {
	return "\n## Проверка\n\n" + taskform.RehearsalNote + " 2026-09-24 10:00, свежее дерево " +
		sha + ", сценарий " + taskform.ScenarioPrint(scenario) + ", шагов 1, все зелёные.\n"
}

// TestMarkNeedsStamp: без отметки и без пометки-исключения ворот отказывает,
// называя команду обкатки и тот выход, с которого его позвали.
func TestMarkNeedsStamp(t *testing.T) {
	err := Mark("XR-001", doc(""), "закрытие")
	if err == nil {
		t.Fatal("файл без отметки ворот проходить не должен")
	}
	for _, want := range []string{"taskctl rehearse XR-001", "повторить закрытие"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("в отказе нет %q: %v", want, err)
		}
	}
}

// TestMarkPasses: отметка и пометка-исключение открывают ворот одинаково.
func TestMarkPasses(t *testing.T) {
	d := doc("")
	if err := Mark("XR-001", d+stamp(d, "1a2b3c4d5e6f"), "закрытие"); err != nil {
		t.Fatalf("отметка ворот не открыла: %v", err)
	}
	if err := Mark("XR-001", doc("\n- Исключение: обкатка (шаги гоняет прод)\n"), "закрытие"); err != nil {
		t.Fatalf("пометка-исключение ворот не погасила: %v", err)
	}
}

// TestFreshCatchesSwappedScenario: сценарий, переписанный после обкатки, ворот
// не открывает даже без git: отпечаток в отметке разошёлся с текстом раздела.
func TestFreshCatchesSwappedScenario(t *testing.T) {
	d := doc("")
	swapped := "# XR-001\n\n## Сценарий проверки\n\n1. Запустить другое.\n" + stamp(d, "1a2b3c4d5e6f")
	err := Fresh(t.TempDir(), "XR-001", swapped, "слияние")
	if err == nil {
		t.Fatal("подменённый сценарий ворот не отбил")
	}
	if !strings.Contains(err.Error(), "сценарий менялся после обкатки") {
		t.Fatalf("отказ не называет причину: %v", err)
	}
}

// TestFreshOutsideGit: вне git сверять коммит не с чем, и вороту довольно
// самой отметки: доска живёт и там, где код лежит отдельно от неё.
func TestFreshOutsideGit(t *testing.T) {
	d := doc("")
	if err := Fresh(t.TempDir(), "XR-001", d+stamp(d, "1a2b3c4d5e6f"), "слияние"); err != nil {
		t.Fatalf("вне git отметки должно хватать: %v", err)
	}
}
