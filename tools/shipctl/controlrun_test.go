package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/dronrider/devkit/internal/checkrun"
)

// Контроль задачи по сроку (DK-1470) на входе shipctl: заголовок строки
// разбирается в поля Control и Due, и control-run поднимает инспектора по
// строке в окне срока тем же путём, что check-run по строке Check. Стенд тот
// же, что у страховки тика, живой головы нет, проверяется вызов taskctl run с
// заказом контроля.

// controlRow это XR-001 в работе с контролем: срок просрочен, окно
// контроля открыто, дожим в заказе обязателен.
func controlRow(days string) string {
	return "| XR-001 | Починка бага [срок: " + days + "] [контроль: 30] | bug | P1 | 55 (50+0+0+5+0) | [tasks/XR-001.md](tasks/XR-001.md) |\n"
}

// controlDue даёт просроченный срок. Окно контроля открывается за N дней до
// срока и больше не закрывается, поэтому просрочка делает стенд статичным от
// дня прогона.
func controlDue() string {
	return time.Now().AddDate(0, 0, -1).Format("2006-01-02")
}

// controlStand поднимает стенд контроля: XR-001 в работе с суффиксами срока
// и контроля, права и обвязка выката те же, что у страховки тика.
func controlStand(t *testing.T, row string) (string, string) {
	t.Helper()
	root, callLog := setup(t, row, "")
	writeDeployCfg(t, root, "deploy = true\nautonomous = true\n")
	return root, callLog
}

// Строка работы с контролем в окне срока получает инспектора: заголовок дошёл
// до отбора полями Control и Due, заказ несёт черновик рисков, напоминание и
// дожим просрочки.
func TestControlRunRaisesInspectorByTitle(t *testing.T) {
	root, callLog := controlStand(t, controlRow(controlDue()))

	out, failed, err := cmdControlRun(root, nil, true)
	if err != nil || failed {
		t.Fatalf("подъём контроля: %v %v %s", err, failed, out)
	}
	var reps []checkrun.Report
	if err := json.Unmarshal([]byte(out), &reps); err != nil {
		t.Fatalf("машинный вид не разобрался (%v): %s", err, out)
	}
	if len(reps) != 1 || reps[0].ID != "XR-001" || !reps[0].Raised {
		t.Fatalf("ждал один поднятый контроль: %+v", reps)
	}
	calls := readCalls(t, callLog)
	for _, want := range []string{
		"run XR-001 -C " + root, "--hidden",
		checkrun.ControlOrderHead + "XR-001",
		`taskctl draft --prio high "Риски срыва сроков по XR-001`,
		"taskctl remind XR-001",
		"Потолки DK-400 объезжать нельзя",
		"Срок просрочен",
	} {
		if !strings.Contains(calls, want) {
			t.Errorf("заказ контроля не несёт %q: %q", want, calls)
		}
	}
}

// Контроль вне окна подъёмом не кончается и поломкой не считается: окно
// решает отбор по полям строки, а не сам вызов.
func TestControlRunOutsideWindowStaysQuiet(t *testing.T) {
	far := time.Now().AddDate(1, 0, 0).Format("2006-01-02")
	root, _ := controlStand(t, controlRow(far))

	out, failed, err := cmdControlRun(root, nil, false)
	if err != nil || failed {
		t.Fatalf("закрытое окно не поломка: %v %v %s", err, failed, out)
	}
	if !strings.Contains(out, "окно контроля ещё не настало") {
		t.Fatalf("отчёт не назвал закрытое окно: %s", out)
	}
}
