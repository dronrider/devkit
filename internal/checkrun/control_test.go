package checkrun

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Строка стенда с контролем: срок ещё не пришёл, окно открыто за N дней до
// него. Дата стенда стоит в будущем от дат, на которых гонялись сами тесты,
// чтобы окно не зависело от дня прогона.
var (
	controlNow = time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	dueNear    = "2026-11-05"
	dueFar     = "2026-12-31"
)

// TestControlNeed: отбор решает по секции, суффиксу и окну, пустой ответ значит
// «подъём нужен». Контроль живёт на строках работы: Check уже проверяется
// своим прогоном, Blocked держит собственную механику пробуждения. Отбор
// читает поля Control и Due строки, а не заголовок (DK-1470), у shipctl и
// дашборда заголовки разбираются по-разному. Поле Control это -1 у строки без
// контроля, заголовки в тесте остаются для узнаваемости строк стенда.
func TestControlNeed(t *testing.T) {
	inWork := Row{ID: "XR-3", Sect: "in-progress", Section: "In progress", Control: -1}
	for _, tc := range []struct {
		name string
		row  Row
		want string
	}{
		{"нужен", Row{ID: "XR-3", Sect: "in-progress", Section: "In progress",
			Title: "Работа [срок: " + dueNear + "] [контроль: 3]", Control: 3, Due: dueNear}, ""},
		{"нужен и в backlog", Row{ID: "XR-3", Sect: "backlog", Section: "Backlog",
			Title: "Работа [срок: " + dueNear + "] [контроль: 3]", Control: 3, Due: dueNear}, ""},
		{"проверяется своим прогоном", Row{ID: "XR-3", Sect: "check", Section: "Check",
			Title: "Работа [срок: " + dueNear + "] [контроль: 3]", Control: 3, Due: dueNear}, "строка не в работе"},
		{"без контроля", inWork, "строка без контроля"},
		{"контроль без срока", Row{ID: "XR-3", Sect: "in-progress", Section: "In progress",
			Title: "Работа [контроль: 3]", Control: 3}, "контроль без срока"},
		{"окно не настало", Row{ID: "XR-3", Sect: "in-progress", Section: "In progress",
			Title: "Работа [срок: " + dueFar + "] [контроль: 3]", Control: 3, Due: dueFar}, "окно контроля ещё не настало"},
		{"просрочка в окне", Row{ID: "XR-3", Sect: "in-progress", Section: "In progress",
			Title: "Работа [срок: 2026-10-30] [контроль: 3]", Control: 3, Due: "2026-10-30"}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := ControlNeed(controlNow, tc.row)
			if (tc.want == "") != (got == "") || !strings.Contains(got, tc.want) {
				t.Fatalf("ждал %q, пришло %q", tc.want, got)
			}
		})
	}
}

// teamOf пишет команду машины с инспектором на присланный дом.
func teamOf(t *testing.T, home, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(home, ".devkit"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".devkit", "team.toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestInspectorChoice: инспектор это агентский участник роли inspect, человек с
// той же ролью сессией не поднимается. Инспектор, совпавший моделью с
// исполнителем разработки, сменяется лестницей: он смотрит работу со стороны.
func TestInspectorChoice(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if ch := inspectorChoice("sonnet", true); ch.Model != "" {
		t.Fatalf("без команды выбрало %+v", ch)
	}
	teamOf(t, os.Getenv("HOME"), `
[rider]
kind = "human"
name = "Всадник"
roles = ["inspect"]

[insp]
kind = "agent"
name = "Инспектор"
model = "flash"
tier = "base"
roles = ["inspect"]
`)
	if ch := inspectorChoice("sonnet", true); ch.Model != "flash" || ch.Tier != "base" {
		t.Fatalf("инспектор не выбран: %+v", ch)
	}
	if ch := inspectorChoice("flash", true); ch.Model != "" {
		t.Fatalf("инспектор-исполнитель не сменён лестницей: %+v", ch)
	}
	if ch := inspectorChoice("flash", false); ch.Model != "flash" {
		t.Fatalf("без известного исполнителя инспектор обязан подняться: %+v", ch)
	}
}

// TestControlOrder: заказ называет черновик рисков, напоминание и потолки
// DK-400 словами, по которым скиллы доски разводят работу сессии без человека.
func TestControlOrder(t *testing.T) {
	order := ControlOrder("XR-3", "sonnet", 0)
	for _, want := range []string{
		ControlOrderHead + "XR-3",
		`taskctl draft --prio high "Риски срыва сроков по XR-3`,
		"taskctl remind XR-3",
		"Потолки DK-400 объезжать нельзя",
		"не больше двух на цель",
		"дерево задачи не заводи",
		"Разработку вёл sonnet",
	} {
		if !strings.Contains(order, want) {
			t.Errorf("заказ не несёт %q: %s", want, order)
		}
	}
	if strings.Contains(order, "просрочен") {
		t.Errorf("непросроченный заказ несёт дожим: %s", order)
	}
	if order = ControlOrder("XR-3", "", 2); !strings.Contains(order, "просрочен на 2 дней") {
		t.Errorf("заказ дожима без просрочки: %s", order)
	}
}

// TestControlRun: контроль поднимает инспектора по строке в окне, молчит без
// строк контроля и отказывает словами отказа, а не пустотой.
func TestControlRun(t *testing.T) {
	root := project(t, "", nil)
	t.Setenv("HOME", t.TempDir())
	rows := map[string]Row{
		"XR-3": {ID: "XR-3", Sect: "in-progress", Section: "In progress",
			Title: "Работа [срок: " + dueNear + "] [контроль: 3]", Control: 3, Due: dueNear},
		"XR-4": {ID: "XR-4", Sect: "in-progress", Section: "In progress", Control: -1},
	}

	f := &fake{tier: "base", ladder: ladder}
	reps := ControlRun(f, root, rows, nil, controlNow)
	if len(reps) != 1 || !reps[0].Raised || reps[0].ID != "XR-3" {
		t.Fatalf("ждал один поднятый контроль: %+v", reps)
	}
	if len(f.planned) != 1 || f.planned[0].Choice.Model != "sonnet" || f.planned[0].Dev != "" {
		t.Fatalf("заказ ушел не лестнице без команды: %+v", f.planned)
	}
	if !strings.Contains(f.planned[0].Order, ControlOrderHead+"XR-3") {
		t.Fatalf("носителю ушёл не заказ контроля: %s", f.planned[0].Order)
	}

	f = &fake{}
	reps = ControlRun(f, root, map[string]Row{"XR-4": rows["XR-4"]}, nil, controlNow)
	if len(reps) != 1 || !strings.Contains(reps[0].Line, "строк с контролем нет") {
		t.Fatalf("пустой отбор: %+v", reps)
	}

	reps = ControlRun(&fake{tier: "base", ladder: ladder}, root, rows, []string{"XR-9"}, controlNow)
	if !Failed(reps) || !strings.Contains(reps[0].Line, "строки нет") {
		t.Fatalf("строки нет: %+v", reps)
	}
	reps = ControlRun(&fake{ready: "замок конвейера занят", failed: true, tier: "base", ladder: ladder},
		root, rows, []string{"XR-3"}, controlNow)
	if !Failed(reps) || !strings.Contains(reps[0].Line, "замок конвейера занят") {
		t.Fatalf("отказ предполёта: %+v", reps)
	}
}

// TestControlRunPrefersInspector: инспектор команды сильнее лестницы, но
// независимость остаётся: инспектор-исполнитель сменяется ступенью выше.
func TestControlRunPrefersInspector(t *testing.T) {
	root := project(t, "", map[string]string{"XR-3": doc + devSonnet})
	t.Setenv("HOME", t.TempDir())
	teamOf(t, os.Getenv("HOME"), `
[insp]
kind = "agent"
name = "Инспектор"
model = "flash"
tier = "base"
roles = ["inspect"]
`)
	rows := map[string]Row{
		"XR-3": {ID: "XR-3", Sect: "in-progress", Section: "In progress",
			Title: "Работа [срок: " + dueNear + "] [контроль: 3]", Control: 3, Due: dueNear},
	}
	f := &fake{tier: "base", ladder: ladder}
	reps := ControlRun(f, root, rows, nil, controlNow)
	if !reps[0].Raised {
		t.Fatalf("контроль не поднят: %+v", reps)
	}
	if f.planned[0].Choice.Model != "flash" || !strings.Contains(f.planned[0].TierWhy, "инспектор контроля из команды") {
		t.Fatalf("заказ ушёл не инспектору: %+v", f.planned[0])
	}
	// Разработку вёл sonnet, инспектор flash: совпадения нет, инспектор остаётся.
	if !strings.Contains(reps[0].Line, "инспектор") && !strings.Contains(f.planned[0].Order, ControlOrderHead) {
		t.Fatalf("отчёт не называет инспектора: %s", reps[0].Line)
	}
}
