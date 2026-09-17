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

// planChildMark это метка, которую клиент Claude Code с версии 2.1.261 ставит в
// окружение каждого вызова Bash: и у головы разговора, и у субагента (DK-852).
// Своего имени в коде она больше не имеет, а тут стоит замером.
const planChildMark = "CLAUDE_CODE_CHILD_SESSION"

func TestPlanHeadWritesUnderChildMark(t *testing.T) {
	home := t.TempDir()
	// Предмет DK-852: голова разговора со своим ID сессии пишет свой план, хотя
	// метка субагента в окружении стоит. Прежде она получала отказ и обходила
	// его руками, снимая метку через env -u перед каждым вызовом.
	env := planEnv(map[string]string{planEnvSession: "s1", planChildMark: "1"})

	if _, err := cmdPlan(home, "set", []string{"работа"}, "", "", env); err != nil {
		t.Fatalf("голова разговора получила отказ на своём же плане: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".devkit", "plans", "s1.json")); err != nil {
		t.Fatalf("плана сессии по имени s1.json нет: %v", err)
	}
}

func TestPlanSubagentKeepsNeighbourPlan(t *testing.T) {
	home := t.TempDir()
	env := planEnv(map[string]string{planEnvSession: "s1", planChildMark: "1"})

	if _, err := cmdPlan(home, "set", []string{"работа"}, "", "dk613", env); err != nil {
		t.Fatal(err)
	}
	// Имя файла держит маску дашборда: <ID сессии>-sub-<метка>.json.
	if _, err := os.Stat(filepath.Join(home, ".devkit", "plans", "s1-sub-dk613.json")); err != nil {
		t.Fatalf("плана субагента по ожидаемому имени нет: %v", err)
	}
	// Метка из окружения работает наравне с флагом: пачку исполнителей поднимает
	// диспетчер, и метку он может положить парой окружения.
	envLabel := planEnv(map[string]string{planEnvSession: "s1", planEnvLabel: "dk614"})
	if _, err := cmdPlan(home, "set", []string{"работа"}, "", "", envLabel); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".devkit", "plans", "s1-sub-dk614.json")); err != nil {
		t.Fatalf("метка из окружения в имя файла не уехала: %v", err)
	}
	// План соседа по пачке метка бережёт: имена файлов разные, и первый план
	// лежит на месте.
	if _, err := os.Stat(filepath.Join(home, ".devkit", "plans", "s1-sub-dk613.json")); err != nil {
		t.Fatalf("план соседа по пачке пропал: %v", err)
	}
	// Читать чужой план не запрещено: метка называет своего хозяина, а показ
	// берёт план сессии вместе с планами её субагентов.
	if _, err := cmdPlan(home, "show", nil, "", "", env); err != nil {
		t.Fatalf("план внешней сессии не показан: %v", err)
	}
}

func TestPlanAddress(t *testing.T) {
	home := t.TempDir()

	// Запасной адрес: в контуре второй подписки CLAUDE_CODE_SESSION_ID пуст, и
	// план сессии ведётся именем её tmux-сессии.
	a, err := planResolve(home, "", "", planEnv(map[string]string{planEnvTmux: "chat-2"}))
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, ".devkit", "plans", "chat-2.json"); a.file != want {
		t.Fatalf("запасной адрес %q, ждали %q", a.file, want)
	}

	// Нечем назвать сессию, значит команда отказывает словами, а не пишет план
	// в файл со случайным именем.
	if _, err := planResolve(home, "", "", planEnv(nil)); err == nil {
		t.Fatal("план без ID сессии записан, ждали отказ")
	}

	// Метка не уводит файл за пределы каталога планов.
	if _, err := planResolve(home, "s1", "../../beda", planEnv(nil)); err == nil {
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

// TestPlanSetCarriesStates про перекладку плана посреди захода: снятый шаг
// уходит из набора, а отметки закрытых и идущего остаются на месте (DK-609).
func TestPlanSetCarriesStates(t *testing.T) {
	home := t.TempDir()
	env := planEnv(map[string]string{planEnvSession: "s1"})

	if _, err := cmdPlan(home, "set", []string{"разведка\nправка\nотменённый шаг\nкоммит"}, "", "", env); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdPlan(home, "done", []string{"разведка"}, "", "", env); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdPlan(home, "step", []string{"правка"}, "", "", env); err != nil {
		t.Fatal(err)
	}

	// Работа сменилась: отменённый шаг снят, к хвосту добавился новый этап.
	out, err := cmdPlan(home, "set", []string{"разведка\nправка\nкоммит\nвычитка"}, "", "", env)
	if err != nil {
		t.Fatal(err)
	}
	plan := planFile(t, home, "s1.json")
	want := []planItem{
		{Text: "разведка", State: "completed"},
		{Text: "правка", State: "in_progress"},
		{Text: "коммит", State: "pending"},
		{Text: "вычитка", State: "pending"},
	}
	if len(plan) != len(want) {
		t.Fatalf("пунктов %d, ждали %d: %v", len(plan), len(want), plan)
	}
	for i, it := range plan {
		if it != want[i] {
			t.Fatalf("пункт %d это %v, ждали %v", i+1, it, want[i])
		}
	}
	if !strings.Contains(out, "закрыто 1") || !strings.Contains(out, "идёт 2") {
		t.Fatalf("печать перекладки не назвала закрытого и идущего: %s", out)
	}
}

// TestPlanSetCarryIgnoresCase про то, что пункт, переписанный с другим
// отступом или регистром первой буквы, остаётся тем же пунктом.
func TestPlanSetCarryIgnoresCase(t *testing.T) {
	home := t.TempDir()
	env := planEnv(map[string]string{planEnvSession: "s1"})

	if _, err := cmdPlan(home, "set", []string{"Разведка\nправка"}, "", "", env); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdPlan(home, "done", []string{"Разведка"}, "", "", env); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdPlan(home, "set", []string{"  разведка  \nправка"}, "", "", env); err != nil {
		t.Fatal(err)
	}
	if got := planFile(t, home, "s1.json")[0].State; got != "completed" {
		t.Fatalf("пункт после перекладки в состоянии %q, ждали completed", got)
	}
}

// TestPlanSetCarryOnlyOnce про повторяющийся текст: два одинаковых пункта не
// забирают одну и ту же прежнюю отметку дважды.
func TestPlanSetCarryOnlyOnce(t *testing.T) {
	home := t.TempDir()
	env := planEnv(map[string]string{planEnvSession: "s1"})

	if _, err := cmdPlan(home, "set", []string{"прогон\nправка"}, "", "", env); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdPlan(home, "done", []string{"прогон"}, "", "", env); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdPlan(home, "set", []string{"прогон\nправка\nпрогон"}, "", "", env); err != nil {
		t.Fatal(err)
	}
	plan := planFile(t, home, "s1.json")
	if plan[0].State != "completed" {
		t.Fatalf("первый прогон в состоянии %q, ждали completed", plan[0].State)
	}
	if plan[2].State != "pending" {
		t.Fatalf("второй прогон в состоянии %q, ждали pending", plan[2].State)
	}
}

// TestPlanSetFreshOnEmpty про первый план сессии: переносить нечего, и все
// пункты ложатся ждущими.
func TestPlanSetFreshOnEmpty(t *testing.T) {
	home := t.TempDir()
	env := planEnv(map[string]string{planEnvSession: "s1"})

	if _, err := cmdPlan(home, "set", []string{"разведка\nправка"}, "", "", env); err != nil {
		t.Fatal(err)
	}
	for _, it := range planFile(t, home, "s1.json") {
		if it.State != "pending" {
			t.Fatalf("пункт первого плана в состоянии %q, ждали pending", it.State)
		}
	}
}

// Четыре кейса DK-900 ниже краснеют, если из set убрать ворота planForeign:
// набор без единого совпадения ложится поверх лежащего плана, и отметки
// исполнителя пропадают.

func TestPlanSetRefusesReviewerOverExecutor(t *testing.T) {
	home := t.TempDir()
	// Кейс DK-888: исполнитель ведёт план с меткой задачи, а поднятый им
	// ревьювер берёт ту же метку и кладёт свой план поверх.
	env := planEnv(map[string]string{planEnvSession: "s1"})
	if _, err := cmdPlan(home, "set", []string{"разведка\nправка\nкоммит"}, "", "DK-888", env); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdPlan(home, "done", []string{"разведка"}, "", "DK-888", env); err != nil {
		t.Fatal(err)
	}
	_, err := cmdPlan(home, "set", []string{"чтение диффа\nпрогон тестов\nзамечания"}, "", "DK-888", env)
	if err == nil {
		t.Fatal("план ревьювера с меткой задачи лёг поверх плана исполнителя без отказа")
	}
	// Отказ называет чужой план по имени файла и оба выхода: свою метку с
	// хвостом роли и --force.
	for _, want := range []string{"s1-sub-DK-888.json", "DK-888-review", "--force", `"разведка"`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("в отказе нет %q: %v", want, err)
		}
	}
	got := planFile(t, home, "s1-sub-DK-888.json")
	if len(got) != 3 || got[0].Text != "разведка" || got[0].State != "completed" {
		t.Fatalf("план исполнителя после отказа не цел: %+v", got)
	}
}

func TestPlanSetRefusesUnlabelledOverHead(t *testing.T) {
	home := t.TempDir()
	// Кейс DK-936: субагент без метки пишет по унаследованному ID сессии и
	// стирает план головы захода, где первый пункт уже идёт.
	env := planEnv(map[string]string{planEnvSession: "s1"})
	if _, err := cmdPlan(home, "set", []string{"груминг\nнарезка\nотчёт"}, "", "", env); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdPlan(home, "step", nil, "", "", env); err != nil {
		t.Fatal(err)
	}
	_, err := cmdPlan(home, "set", []string{"вычитка\nправка формы"}, "", "", env)
	if err == nil {
		t.Fatal("безымянный план субагента лёг поверх плана головы без отказа")
	}
	if !strings.Contains(err.Error(), "s1.json") || !strings.Contains(err.Error(), "--label") {
		t.Errorf("отказ не называет файл головы и выход через метку: %v", err)
	}
	got := planFile(t, home, "s1.json")
	if len(got) != 3 || got[0].State != "in_progress" {
		t.Fatalf("план головы после отказа не цел: %+v", got)
	}
}

func TestPlanDoneWithoutLabelHitsHeadPlan(t *testing.T) {
	home := t.TempDir()
	// Кейс DK-467: plan done без метки закрывает пункт в плане диспетчера.
	// Ворота set этого не видят, у done нового набора нет, и адресность даёт
	// только своя метка захода: с ней закрытие уходит в свой файл, а план
	// головы остаётся как был.
	env := planEnv(map[string]string{planEnvSession: "s1"})
	if _, err := cmdPlan(home, "set", []string{"спавн исполнителей\nслияние"}, "", "", env); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdPlan(home, "step", nil, "", "", env); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdPlan(home, "set", []string{"правка\nтесты"}, "", "DK-467-exec", env); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdPlan(home, "step", nil, "", "DK-467-exec", env); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdPlan(home, "done", nil, "", "DK-467-exec", env); err != nil {
		t.Fatal(err)
	}
	head := planFile(t, home, "s1.json")
	if head[0].State != "in_progress" {
		t.Fatalf("закрытие с меткой задело план головы: %+v", head)
	}
	own := planFile(t, home, "s1-sub-DK-467-exec.json")
	if own[0].State != "completed" {
		t.Fatalf("закрытие с меткой не дошло до своего плана: %+v", own)
	}
	// Без метки закрытие уходит в план головы: это предел механики, и
	// закрывает его правило метки в определениях агентов, а не команда.
	if _, err := cmdPlan(home, "done", nil, "", "", env); err != nil {
		t.Fatal(err)
	}
	if head = planFile(t, home, "s1.json"); head[0].State != "completed" {
		t.Fatalf("done без метки не дошёл до плана головы: %+v", head)
	}
}

func TestPlanSetNestedProofreadTakesOwnLabel(t *testing.T) {
	home := t.TempDir()
	// Кейс DK-1012: вычитка внутри исполнителя берёт метку задачи и получает
	// отказ, а со своим хвостом ложится рядом, и show печатает оба плана.
	env := planEnv(map[string]string{planEnvSession: "s1"})
	if _, err := cmdPlan(home, "set", []string{"разведка\nправка\nкоммит"}, "", "DK-974", env); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdPlan(home, "done", []string{"разведка"}, "", "DK-974", env); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdPlan(home, "set", []string{"чтение текста\nвычитка"}, "", "DK-974", env); err == nil {
		t.Fatal("план вычитки с меткой задачи лёг поверх плана исполнителя без отказа")
	}
	if _, err := cmdPlan(home, "set", []string{"чтение текста\nвычитка"}, "", "DK-974-proofread", env); err != nil {
		t.Fatalf("план вычитки под своей меткой не лёг: %v", err)
	}
	out, err := cmdPlan(home, "show", nil, "", "", env)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"план субагента DK-974: пунктов 3, закрыто 1", "план субагента DK-974-proofread: пунктов 2"} {
		if !strings.Contains(out, want) {
			t.Errorf("в показе нет %q:\n%s", want, out)
		}
	}
}

func TestPlanSetOverFinishedPlan(t *testing.T) {
	home := t.TempDir()
	// Закрытый целиком план чужим не считается: в контуре второй подписки
	// адрес плана это имя tmux-сессии (DK-269), и новая сессия под тем же
	// именем кладёт свой план на место доделанного без --force.
	env := planEnv(map[string]string{planEnvTmux: "dk"})
	if _, err := cmdPlan(home, "set", []string{"разведка\nправка"}, "", "", env); err != nil {
		t.Fatal(err)
	}
	for _, it := range []string{"1", "2"} {
		if _, err := cmdPlan(home, "done", []string{it}, "", "", env); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := cmdPlan(home, "set", []string{"груминг"}, "", "", env); err != nil {
		t.Fatalf("новый план поверх доделанного получил отказ: %v", err)
	}
	// Перекладка своими словами с одним совпавшим пунктом воротами не ловится.
	if _, err := cmdPlan(home, "set", []string{"груминг\nотчёт"}, "", "", env); err != nil {
		t.Fatalf("перекладка своего плана получила отказ: %v", err)
	}
}
