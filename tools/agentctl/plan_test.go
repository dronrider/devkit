package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// planEnv собирает подставное окружение: команда спрашивает переменные через
// переданную функцию, и живой os.Getenv в тестах не участвует.
func planEnv(pairs map[string]string) func(string) string {
	return func(name string) string { return pairs[name] }
}

// planFile читает файл плана из подставного дома.
func planFile(t *testing.T, home, name string) []planItem {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(home, ".devkit", "plans", name))
	if err != nil {
		t.Fatal(err)
	}
	var out []planItem
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("файл плана не разобран: %v", err)
	}
	return out
}

func TestPlanSetAndStep(t *testing.T) {
	home := t.TempDir()
	env := planEnv(map[string]string{planEnvSession: "s1"})

	if _, err := cmdPlan(home, "set", []string{"дерево\nскилл\nтесты"}, "", "", env); err != nil {
		t.Fatal(err)
	}
	plan := planFile(t, home, "s1.json")
	if len(plan) != 3 {
		t.Fatalf("пунктов %d, ждали три: %v", len(plan), plan)
	}
	for _, it := range plan {
		if it.State != "pending" {
			t.Fatalf("свежий пункт в состоянии %q, ждали pending", it.State)
		}
	}

	if _, err := cmdPlan(home, "step", nil, "", "", env); err != nil {
		t.Fatal(err)
	}
	if got := planFile(t, home, "s1.json")[0].State; got != "in_progress" {
		t.Fatalf("первый пункт в состоянии %q, ждали in_progress", got)
	}

	// Начатый пункт закрывает прежний идущий: двух идущих в кольце дашборда не
	// различить.
	if _, err := cmdPlan(home, "step", []string{"скилл"}, "", "", env); err != nil {
		t.Fatal(err)
	}
	plan = planFile(t, home, "s1.json")
	if plan[0].State != "completed" || plan[1].State != "in_progress" {
		t.Fatalf("после шага состояния %q и %q, ждали completed и in_progress", plan[0].State, plan[1].State)
	}

	if _, err := cmdPlan(home, "done", nil, "", "", env); err != nil {
		t.Fatal(err)
	}
	if got := planFile(t, home, "s1.json")[1].State; got != "completed" {
		t.Fatalf("закрытый пункт в состоянии %q, ждали completed", got)
	}
}

func TestPlanShow(t *testing.T) {
	home := t.TempDir()
	env := planEnv(map[string]string{planEnvSession: "s1"})

	out, err := cmdPlan(home, "show", nil, "", "", env)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "плана нет") {
		t.Fatalf("пустой план показан как %q, ждали слова про отсутствие плана", out)
	}

	if _, err := cmdPlan(home, "set", []string{"дерево", "тесты"}, "", "", env); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdPlan(home, "step", []string{"2"}, "", "", env); err != nil {
		t.Fatal(err)
	}
	// План субагента лежит рядом и печатается своей строкой: делегированная
	// работа видна в терминале так же, как в кольце дашборда.
	if _, err := cmdPlan(home, "set", []string{"ревью"}, "", "dk613", env); err != nil {
		t.Fatal(err)
	}
	out, err = cmdPlan(home, "show", nil, "", "", env)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"план сессии s1", "идёт 2", "[ ] дерево", "[>] тесты",
		"план субагента dk613", "[ ] ревью"} {
		if !strings.Contains(out, want) {
			t.Fatalf("в показе плана нет %q:\n%s", want, out)
		}
	}
}

func TestPlanSubagentNeedsLabel(t *testing.T) {
	home := t.TempDir()
	env := planEnv(map[string]string{planEnvSession: "s1", planEnvChild: "1"})

	_, err := cmdPlan(home, "set", []string{"работа"}, "", "", env)
	if err == nil {
		t.Fatal("субагент без метки положил план поверх плана внешней сессии")
	}
	if !strings.Contains(err.Error(), "--label") {
		t.Fatalf("отказ не зовёт флаг метки: %v", err)
	}

	if _, err := cmdPlan(home, "set", []string{"работа"}, "", "dk613", env); err != nil {
		t.Fatal(err)
	}
	// Имя файла держит маску дашборда: <ID сессии>-sub-<метка>.json.
	if _, err := os.Stat(filepath.Join(home, ".devkit", "plans", "s1-sub-dk613.json")); err != nil {
		t.Fatalf("плана субагента по ожидаемому имени нет: %v", err)
	}
	// Метка из окружения работает наравне с флагом: пачку исполнителей поднимает
	// диспетчер, и метку он может положить парой окружения.
	envLabel := planEnv(map[string]string{planEnvSession: "s1", planEnvChild: "1", planEnvLabel: "dk614"})
	if _, err := cmdPlan(home, "set", []string{"работа"}, "", "", envLabel); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".devkit", "plans", "s1-sub-dk614.json")); err != nil {
		t.Fatalf("метка из окружения в имя файла не уехала: %v", err)
	}
	// Читать чужой план субагенту не запрещено: метки требует запись, а не показ.
	if _, err := cmdPlan(home, "show", nil, "", "", env); err != nil {
		t.Fatalf("субагент без метки не смог посмотреть план внешней сессии: %v", err)
	}
}

func TestPlanAddress(t *testing.T) {
	home := t.TempDir()

	// Запасной адрес: в контуре второй подписки CLAUDE_CODE_SESSION_ID пуст, и
	// план сессии ведётся именем её tmux-сессии.
	a, err := planResolve(home, "", "", planEnv(map[string]string{planEnvTmux: "chat-2"}), true)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, ".devkit", "plans", "chat-2.json"); a.file != want {
		t.Fatalf("запасной адрес %q, ждали %q", a.file, want)
	}

	// Нечем назвать сессию, значит команда отказывает словами, а не пишет план
	// в файл со случайным именем.
	if _, err := planResolve(home, "", "", planEnv(nil), true); err == nil {
		t.Fatal("план без ID сессии записан, ждали отказ")
	}

	// Метка не уводит файл за пределы каталога планов.
	if _, err := planResolve(home, "s1", "../../beda", planEnv(nil), true); err == nil {
		t.Fatal("метка с путём принята, ждали отказ")
	}
}

func TestPlanPickAmbiguous(t *testing.T) {
	plan := []planItem{{Text: "дока agentctl"}, {Text: "дока дашборда"}}
	if _, err := planPick(plan, "дока"); err == nil {
		t.Fatal("двусмысленный кусок текста выбрал пункт молча, ждали отказ")
	}
	if n, err := planPick(plan, "дашборд"); err != nil || n != 1 {
		t.Fatalf("выбор по тексту дал %d, %v; ждали второй пункт", n, err)
	}
	if _, err := planPick(plan, "9"); err == nil {
		t.Fatal("номер за пределами плана принят, ждали отказ")
	}
}

func TestPlanKeepsUnknownOp(t *testing.T) {
	home := t.TempDir()
	env := planEnv(map[string]string{planEnvSession: "s1"})
	if _, err := cmdPlan(home, "поехали", nil, "", "", env); err == nil {
		t.Fatal("неизвестное действие принято молча")
	}
}
