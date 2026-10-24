package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dronrider/devkit/internal/reviewnote"
)

// planBoard кладёт доску со строками: сборка плана читает тип строки и суффикс
// [план: имя] из заголовка.
func planBoard(t *testing.T, root string, rows ...string) {
	t.Helper()
	text := "# Задачи\n\n## In progress\n\n" +
		"| ID | Название | Тип | P | Ранг | Цена | Ссылка |\n" +
		"| --- | --- | --- | --- | --- | --- | --- |\n"
	for _, r := range rows {
		text += "| " + r + " |\n"
	}
	planWriteFile(t, filepath.Join(root, "docs", "TASKS.md"), text)
}

// planReadHead читает файл плана целиком: шапку и пункты.
func planReadHead(t *testing.T, home, name string) planFile {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(home, ".devkit", "plans", name))
	if err != nil {
		t.Fatal(err)
	}
	var p planFile
	if err := json.Unmarshal(data, &p); err != nil {
		t.Fatalf("файл плана не разобран: %v", err)
	}
	return p
}

// TestPlanSetFromType: шаблон выбирается по типу строки, свои пункты ложатся в
// слот, а этапы шаблона стоят порядком файла (LLD DK-972, решения 4 и 5).
func TestPlanSetFromType(t *testing.T) {
	root, _ := planKit(t)
	planBoard(t, root, "XR-001 | Правка сессии | task | P2 | 39 (25+8+1+0+4) | M | -")
	home := filepath.Dir(root)
	env := planEnv(map[string]string{"CLAUDE_CODE_SESSION_ID": "s1"})
	out, err := planRun(home, "set", []string{"снять разметку", "поправить сборку"}, "s1", "XR-001-exec", false, root, "", env)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "шаблон task") {
		t.Fatalf("печать не называет шаблон:\n%s", out)
	}
	p := planReadHead(t, home, "s1-sub-XR-001-exec.json")
	if p.Template != "task" {
		t.Fatalf("шапка шаблона = %q", p.Template)
	}
	if len(p.Items) != 3 {
		t.Fatalf("пунктов %d, жду три: два своих в слоте и тесты: %v", len(p.Items), p.Items)
	}
	if p.Items[0].Text != "снять разметку" || p.Items[0].Stage != "work" || p.Items[0].Trace != "stage:разработка" {
		t.Fatalf("свой пункт не лёг в слот с следом этапа: %+v", p.Items[0])
	}
	if p.Items[1].Text != "поправить сборку" || p.Items[1].Stage != "work" {
		t.Fatalf("второй свой пункт потерялся: %+v", p.Items[1])
	}
	if p.Items[2].Text != "тесты" || p.Items[2].Stage != "tests" || p.Items[2].Trace != "tests" {
		t.Fatalf("этап тестов потерялся: %+v", p.Items[2])
	}
}

// TestPlanSetSlotEmptyAndDup: пустой текст оставляет слот именем этапа, а
// строка, совпавшая с именем этапа, считается этим этапом и не удваивается.
func TestPlanSetSlotEmptyAndDup(t *testing.T) {
	root, kit := planKit(t)
	planWriteFile(t, filepath.Join(kit, "task.toml"), "name = \"task\"\ntitle = \"Задача\"\ntypes = [\"task\"]\n"+
		"[work]\ntitle = \"разработка\"\nby = \"сам\"\ntrace = \"слово\"\nslot = true\n"+
		"[tests]\ntitle = \"тесты\"\nby = \"сам\"\ntrace = \"tests\"\n")
	home := filepath.Dir(root)
	env := planEnv(map[string]string{"CLAUDE_CODE_SESSION_ID": "s2"})
	if _, err := planRun(home, "set", nil, "s2", "XR-001-exec", false, root, "task", env); err != nil {
		t.Fatal(err)
	}
	p := planReadHead(t, home, "s2-sub-XR-001-exec.json")
	if len(p.Items) != 2 || p.Items[0].Text != "разработка" || p.Items[0].Stage != "work" {
		t.Fatalf("пустой слот не остался именем этапа: %+v", p.Items)
	}
	// Совпавший с именем этапа пункт не удваивается: агент печатает план
	// целиком, и «тесты» в его тексте это этап тестов, а не второй пункт слота.
	if _, err := planRun(home, "set", []string{"тесты", "обойти стенки"}, "s2", "XR-001-exec", false, root, "task", env); err != nil {
		t.Fatal(err)
	}
	p = planReadHead(t, home, "s2-sub-XR-001-exec.json")
	if len(p.Items) != 2 {
		t.Fatalf("совпавший пункт удвоился: %+v", p.Items)
	}
	if p.Items[0].Text != "обойти стенки" || p.Items[0].Stage != "work" {
		t.Fatalf("свой пункт не лёг в слот после дубля: %+v", p.Items[0])
	}
	if p.Items[1].Text != "тесты" || p.Items[1].Stage != "tests" {
		t.Fatalf("совпавший пункт не стал этапом тестов: %+v", p.Items[1])
	}
}

// TestPlanSetFromSuffix: суффикс [план: имя] выбирает шаблон явно, минуя тип.
func TestPlanSetFromSuffix(t *testing.T) {
	root, kit := planKit(t)
	planWriteFile(t, filepath.Join(kit, "poc.toml"), "name = \"poc\"\ntitle = \"Режим POC\"\n"+
		"[show]\ntitle = \"показ\"\nby = \"человек\"\ntrace = \"слово\"\n")
	planBoard(t, root, "XR-002 | Показ режима [план: poc] [приёмка: user] | task | P2 | 39 (25+8+1+0+4) | M | -")
	home := filepath.Dir(root)
	env := planEnv(map[string]string{"CLAUDE_CODE_SESSION_ID": "s3"})
	if _, err := planRun(home, "set", nil, "s3", "XR-002-exec", false, root, "", env); err != nil {
		t.Fatal(err)
	}
	p := planReadHead(t, home, "s3-sub-XR-002-exec.json")
	if p.Template != "poc" || len(p.Items) != 1 || p.Items[0].Text != "показ" {
		t.Fatalf("суффикс не выбрал poc: %+v", p)
	}
}

// TestPlanSetFromSuffixMissing: суффикс с именем, которого в наборе нет, это
// ошибка настройки, а не молчаливое «без шаблона».
func TestPlanSetFromSuffixMissing(t *testing.T) {
	root, _ := planKit(t)
	planBoard(t, root, "XR-002 | Показ режима [план: нетакого] | task | P2 | 39 (25+8+1+0+4) | M | -")
	home := filepath.Dir(root)
	env := planEnv(map[string]string{"CLAUDE_CODE_SESSION_ID": "s4"})
	_, err := planRun(home, "set", nil, "s4", "XR-002-exec", false, root, "", env)
	if err == nil || !strings.Contains(err.Error(), "taskctl plan XR-002 --off") {
		t.Fatalf("суффикс с чужим именем прошёл молча: %v", err)
	}
}

// TestPlanSetFlagAndUnknown: флаг работает и без задачи, а неизвестное имя
// флага отказывает списком набора.
func TestPlanSetFlagAndUnknown(t *testing.T) {
	root, _ := planKit(t)
	home := filepath.Dir(root)
	env := planEnv(map[string]string{"CLAUDE_CODE_SESSION_ID": "s5"})
	if _, err := planRun(home, "set", nil, "s5", "", false, root, "task", env); err != nil {
		t.Fatal(err)
	}
	if p := planReadHead(t, home, "s5.json"); p.Template != "task" || len(p.Items) != 2 {
		t.Fatalf("флаг не собрал шаблон: %+v", p)
	}
	_, err := planRun(home, "set", nil, "s5", "", false, root, "нетакого", env)
	if err == nil || !strings.Contains(err.Error(), "есть task") {
		t.Fatalf("неизвестный флаг прошёл молча: %v", err)
	}
}

// TestPlanSetNoTemplate: ни флага, ни задачи, план из одних своих пунктов с
// шапкой «без шаблона».
func TestPlanSetNoTemplate(t *testing.T) {
	root, _ := planKit(t)
	home := filepath.Dir(root)
	env := planEnv(map[string]string{"CLAUDE_CODE_SESSION_ID": "s6"})
	out, err := planRun(home, "set", []string{"разбор", "показ"}, "s6", "", false, root, "", env)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "без шаблона") {
		t.Fatalf("печать не назвала «без шаблона»:\n%s", out)
	}
	p := planReadHead(t, home, "s6.json")
	if p.Template != "без шаблона" || len(p.Items) != 2 || p.Items[0].Stage != "" || p.Items[0].Trace != "" {
		t.Fatalf("план без шаблона собран неверно: %+v", p)
	}
	if _, err := planRun(home, "set", nil, "s6", "", false, root, "", env); err == nil {
		t.Fatal("пустой набор без шаблона прошёл молча")
	}
}

// TestPlanSetRepeatCarriesStages: повторный set собирает этапы заново и
// переносит состояния: обязательный пункт из плана не выкинуть.
func TestPlanSetRepeatCarriesStages(t *testing.T) {
	root, kit := planKit(t)
	planWriteFile(t, filepath.Join(kit, "task.toml"), "name = \"task\"\ntitle = \"Задача\"\ntypes = [\"task\"]\n"+
		"[work]\ntitle = \"разработка\"\nby = \"сам\"\ntrace = \"слово\"\nslot = true\n"+
		"[docs]\ntitle = \"дока\"\nby = \"сам\"\ntrace = \"слово\"\n")
	home := filepath.Dir(root)
	env := planEnv(map[string]string{"CLAUDE_CODE_SESSION_ID": "s7"})
	if _, err := planRun(home, "set", nil, "s7", "", false, root, "task", env); err != nil {
		t.Fatal(err)
	}
	if _, err := planRun(home, "done", []string{"разработка"}, "s7", "", false, root, "", env); err != nil {
		t.Fatal(err)
	}
	out, err := planRun(home, "set", []string{"своя правка"}, "s7", "", false, root, "task", env)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "закрыто 0") || !strings.Contains(out, "своя правка") {
		t.Fatalf("повторный set сломал план:\n%s", out)
	}
	p := planReadHead(t, home, "s7.json")
	if len(p.Items) != 2 || p.Items[0].Text != "своя правка" || p.Items[0].Stage != "work" {
		t.Fatalf("слот не пересобрался: %+v", p.Items)
	}
	if p.Items[1].Text != "дока" || p.Items[1].Trace != "слово" {
		t.Fatalf("обязательный этап потерялся: %+v", p.Items[1])
	}
}

// TestPlanDoneTraceRefuses: done на этапе со следом без следа отказывает, и
// пункт остаётся незакрытым.
func TestPlanDoneTraceRefuses(t *testing.T) {
	root, kit := planKit(t)
	planWriteFile(t, filepath.Join(kit, "task.toml"), "name = \"task\"\ntitle = \"Задача\"\ntypes = [\"task\"]\n"+
		"[work]\ntitle = \"разработка\"\nby = \"сам\"\ntrace = \"слово\"\nslot = true\n"+
		"[review]\ntitle = \"ревью\"\nby = \"сам\"\ntrace = \"review\"\n")
	planBoard(t, root, "XR-003 | Правка с ревью | task | P2 | 39 (25+8+1+0+4) | M | -")
	if err := os.MkdirAll(filepath.Join(root, "docs", "tasks"), 0o755); err != nil {
		t.Fatal(err)
	}
	planWriteFile(t, filepath.Join(root, "docs", "tasks", "XR-003.md"), "# XR-003\n\n## Ход работы\n")
	home := filepath.Dir(root)
	env := planEnv(map[string]string{"CLAUDE_CODE_SESSION_ID": "s8"})
	if _, err := planRun(home, "set", nil, "s8", "XR-003-exec", false, root, "task", env); err != nil {
		t.Fatal(err)
	}
	// У файла задачи нет раздела «Ревью», и след review отказывает словами,
	// как отказывали бы ворота слияния.
	_, err := planRun(home, "done", []string{"ревью"}, "s8", "XR-003-exec", false, root, "", env)
	if err == nil || !strings.Contains(err.Error(), "не закрыт") {
		t.Fatalf("done без следа прошёл молча: %v", err)
	}
	p := planReadHead(t, home, "s8-sub-XR-003-exec.json")
	for _, it := range p.Items {
		if it.Text == "ревью" && it.State == "completed" {
			t.Fatal("отказавший пункт закрылся")
		}
	}
}

// TestPlanDoneByWord: этап на слове агента закрывается с печатью об этом, а
// след, живущий в файле задачи, у сессии без задачи закрывается словом.
func TestPlanDoneByWord(t *testing.T) {
	root, kit := planKit(t)
	planWriteFile(t, filepath.Join(kit, "task.toml"), "name = \"task\"\ntitle = \"Задача\"\ntypes = [\"task\"]\n"+
		"[work]\ntitle = \"разработка\"\nby = \"сам\"\ntrace = \"stage:разработка\"\nslot = true\n"+
		"[docs]\ntitle = \"дока\"\nby = \"сам\"\ntrace = \"слово\"\n")
	home := filepath.Dir(root)
	env := planEnv(map[string]string{"CLAUDE_CODE_SESSION_ID": "s9"})
	if _, err := planRun(home, "set", nil, "s9", "", false, root, "task", env); err != nil {
		t.Fatal(err)
	}
	out, err := planRun(home, "done", []string{"дока"}, "s9", "", false, root, "", env)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "на слове агента") {
		t.Fatalf("печать не назвала слово:\n%s", out)
	}
	out, err = planRun(home, "done", []string{"разработка"}, "s9", "", false, root, "", env)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "след недоступен без задачи, закрыто словом") {
		t.Fatalf("печать не назвала закрытие словом:\n%s", out)
	}
}

// planGitRepo превращает проект planKit в git-репозиторий: коммит доски в
// main, затем ветка dk-x с коммитом правки кода без теста. Беззадачная сессия
// работает в таком дереве, и её следы читаются по нему.
func planGitRepo(t *testing.T, root string) {
	t.Helper()
	run := func(args ...string) {
		t.Helper()
		args = append([]string{"-C", root}, args...)
		cmd := exec.Command("git", args...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	run("config", "user.name", "t")
	run("config", "user.email", "t@t")
	run("add", "docs/TASKS.md")
	run("commit", "-q", "-m", "доска")
	run("checkout", "-q", "-b", "dk-x")
	planWriteFile(t, filepath.Join(root, "code.go"), "package x\n")
	run("add", "code.go")
	run("commit", "-q", "-m", "правка без теста")
}

// TestPlanDoneTasklessTree: у сессии без задачи следы читаются по дереву
// (решение 6): tests отказывает ветке без тестовых файлов, review проверяется
// git-заметкой на HEAD, а не разделом несуществующего файла задачи.
func TestPlanDoneTasklessTree(t *testing.T) {
	root, kit := planKit(t)
	planWriteFile(t, filepath.Join(kit, "task.toml"), "name = \"task\"\ntitle = \"Задача\"\n"+
		"[tests]\ntitle = \"тесты\"\nby = \"сам\"\ntrace = \"tests\"\n"+
		"[review]\ntitle = \"ревью\"\nby = \"сам\"\ntrace = \"review\"\n")
	planGitRepo(t, root)
	home := filepath.Dir(root)
	env := planEnv(map[string]string{"CLAUDE_CODE_SESSION_ID": "s10"})
	if _, err := planRun(home, "set", nil, "s10", "", false, root, "task", env); err != nil {
		t.Fatal(err)
	}
	// Ветка без тестовых файлов: молчаливое закрытие было бы пропуском
	// обязательных тестов у любой беззадачной сессии.
	_, err := planRun(home, "done", []string{"тесты"}, "s10", "", false, root, "", env)
	if err == nil || !strings.Contains(err.Error(), "нет тестовых файлов") {
		t.Fatalf("tests закрылся молча: %v", err)
	}
	// Ревью без заметки отказывает словами про заметку, пустого пути
	// docs/tasks/.md в отказе быть не должно.
	_, err = planRun(home, "done", []string{"ревью"}, "s10", "", false, root, "", env)
	if err == nil || !strings.Contains(err.Error(), "заметки ревью на HEAD нет") {
		t.Fatalf("review без заметки прошёл или отказал не тем: %v", err)
	}
	if strings.Contains(err.Error(), "docs/tasks/.md") {
		t.Fatalf("отказ зовёт в файл задачи без задачи: %v", err)
	}
	if err := reviewnote.Write(root, "HEAD",
		reviewnote.LevelLine(2, "a1b2c3d", "тронут agentctl")); err != nil {
		t.Fatal(err)
	}
	if _, err := planRun(home, "done", []string{"ревью"}, "s10", "", false, root, "", env); err != nil {
		t.Fatalf("review с заметкой не прошёл: %v", err)
	}
}

// TestPlanSuffix: суффикс читается при любом наборе хвостов, стоящих позже.
func TestPlanSuffix(t *testing.T) {
	cases := map[string]string{
		"Просто":                                          "",
		"Режим [план: poc]":                               "poc",
		"Режим [план: poc] [приёмка: user]":               "poc",
		"Ждём [план: bug] [приёмка: mixed] [провал: 500]": "bug",
		"Стоп [план: task] [блок: ждём железо]":           "task",
	}
	for title, want := range cases {
		if got := planSuffix(title); got != want {
			t.Fatalf("planSuffix(%q) = %q, жду %q", title, got, want)
		}
	}
}

// TestPlanAssemblePathsTail: этап с условием по путям стоит в плане с хвостом
// «при правке <маска>» (решение 8).
func TestPlanAssemblePathsTail(t *testing.T) {
	root, kit := planKit(t)
	planWriteFile(t, filepath.Join(kit, "task.toml"), "name = \"task\"\ntitle = \"Задача\"\ntypes = [\"task\"]\n"+
		"[work]\ntitle = \"разработка\"\nby = \"сам\"\ntrace = \"слово\"\nslot = true\n"+
		"[proof]\ntitle = \"вычитка\"\nby = \"субагент\"\ntrace = \"stage:вычитка\"\npaths = [\"**/*.md\"]\n")
	set, err := planLoadSet(root)
	if err != nil {
		t.Fatal(err)
	}
	tpl, ok := set.Get("task")
	if !ok {
		t.Fatal("шаблон task не собрался")
	}
	items := planAssemble(tpl, nil)
	if len(items) != 2 || items[1].Text != "вычитка при правке **/*.md" {
		t.Fatalf("этап с маской стоит без хвоста: %+v", items)
	}
}
