package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dronrider/devkit/internal/taskform"
)

// rehearseAt это фиксированное время записи: отметка обкатки уезжает в файл
// задачи, и тесту нужна не сегодняшняя дата, а та же самая на каждом прогоне.
var rehearseAt = time.Date(2026, 8, 31, 12, 0, 0, 0, time.Local)

// setupRehearse готовит git-дерево доски с задачей, у которой сценарий несёт
// команды ограждённым блоком. steps идут телом блока.
func setupRehearse(t *testing.T, steps string) string {
	t.Helper()
	root := setup(t)
	writeTask(t, root, "XR-005", "# XR-005\n\n## Сценарий проверки\n\nАгентский.\n\n```sh\n"+steps+"\n```\n")
	gitSetup(t, root)
	return root
}

func writeTask(t *testing.T, root, id, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, "docs", "tasks", id+".md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func rehearseDoc(t *testing.T, root, id string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, "docs", "tasks", id+".md"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestRehearseRunsStepsInFreshTree: шаги гонятся не в рабочем чекауте, а в
// свежем дереве на HEAD и с временным HOME. Незакоммиченный след работы
// исполнителя в прогон не едет, вывод шага целиком ложится в «Проверку», и
// рядом встаёт отметка, которую спрашивают ворота move check.
func TestRehearseRunsStepsInFreshTree(t *testing.T) {
	rec := t.TempDir()
	root := setupRehearse(t, "pwd > "+filepath.Join(rec, "pwd")+
		"\necho \"$HOME\" > "+filepath.Join(rec, "home")+
		"\ntest ! -e leftover.txt && echo сценарий-сошёлся")
	// След работы исполнителя: в прогретом чекауте шаг об него споткнётся.
	if err := os.WriteFile(filepath.Join(root, "leftover.txt"), []byte("артефакт\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	msg, err := cmdRehearse(root, "XR-005", RehearseParams{Now: rehearseAt})
	if err != nil {
		t.Fatalf("обкатка должна пройти в свежем дереве: %v", err)
	}
	if !strings.Contains(msg, "обкатка зелёная") || !strings.Contains(msg, "шагов 3") {
		t.Fatalf("отчёт не называет исход и число шагов: %q", msg)
	}
	pwd := strings.TrimSpace(readFileT(t, filepath.Join(rec, "pwd")))
	if pwd == root || strings.HasPrefix(pwd+"/", root+"/") {
		t.Fatalf("шаг гнался в рабочем чекауте %s, а не в свежем дереве", pwd)
	}
	home := strings.TrimSpace(readFileT(t, filepath.Join(rec, "home")))
	if home == "" || home == os.Getenv("HOME") {
		t.Fatalf("дом прогона совпал с домом сессии: %q", home)
	}
	doc := rehearseDoc(t, root, "XR-005")
	if !taskform.Rehearsed(doc) {
		t.Fatalf("отметки обкатки в файле задачи нет:\n%s", doc)
	}
	if !strings.Contains(doc, "сценарий-сошёлся") {
		t.Fatalf("реальный вывод шага в «Проверку» не лёг:\n%s", doc)
	}
	if !strings.Contains(doc, "## Проверка") {
		t.Fatalf("раздел «Проверка» не заведён:\n%s", doc)
	}
	if wl := gitOut(t, root, "worktree", "list"); strings.Count(wl, "\n") != 0 {
		t.Fatalf("временное дерево не убрано:\n%s", wl)
	}
}

// TestRehearseRedStepLeavesNoMark: упавший шаг отметки не ставит, зато его
// вывод в файле лежит, и отказ называет число красных шагов.
func TestRehearseRedStepLeavesNoMark(t *testing.T) {
	root := setupRehearse(t, "echo первый-шаг\necho вышло-боком >&2; exit 3")
	_, err := cmdRehearse(root, "XR-005", RehearseParams{Now: rehearseAt})
	if err == nil {
		t.Fatal("красная обкатка должна падать")
	}
	if !strings.Contains(err.Error(), "упало 1 из 2") {
		t.Fatalf("отказ не называет счёт красных шагов: %v", err)
	}
	doc := rehearseDoc(t, root, "XR-005")
	if taskform.Rehearsed(doc) {
		t.Fatalf("красная обкатка поставила отметку ворот:\n%s", doc)
	}
	if !strings.Contains(doc, "вышло-боком") {
		t.Fatalf("вывод красного шага не записан:\n%s", doc)
	}
}

// TestRehearseStepsFromFlag: ключ --step отменяет чтение сценария, шаги идут в
// порядке ключей.
func TestRehearseStepsFromFlag(t *testing.T) {
	root := setupRehearse(t, "exit 1")
	msg, err := cmdRehearse(root, "XR-005", RehearseParams{
		Steps: []string{"echo раз", "echo два"}, Now: rehearseAt})
	if err != nil {
		t.Fatalf("шаги из ключа не прогнались: %v", err)
	}
	if !strings.Contains(msg, "шагов 2") {
		t.Fatalf("прогнаны не названные шаги: %q", msg)
	}
	doc := rehearseDoc(t, root, "XR-005")
	if strings.Index(doc, "echo раз") > strings.Index(doc, "echo два") {
		t.Fatalf("порядок шагов перепутан:\n%s", doc)
	}
}

// TestRehearseWithoutCommandBlock: сценарий без ограждённого блока обкатать
// нечем, и отказ называет оба выхода (блок в файле либо ключ --step).
func TestRehearseWithoutCommandBlock(t *testing.T) {
	root := setup(t)
	writeTask(t, root, "XR-005", "# XR-005\n\n## Сценарий проверки\n\n1. Позвать `taskctl show XR-005`.\n")
	gitSetup(t, root)
	_, err := cmdRehearse(root, "XR-005", RehearseParams{Now: rehearseAt})
	if err == nil {
		t.Fatal("сценарий без блока команд должен отказывать")
	}
	if !strings.Contains(err.Error(), "--step") {
		t.Fatalf("отказ не называет выход: %v", err)
	}
}

// TestScenarioStepsIgnoresProse: команды берутся только из ограждённых блоков.
// Обратные кавычки в прозе это имена и пути, гонять их шагами нельзя.
func TestScenarioStepsIgnoresProse(t *testing.T) {
	text := "\nАгентский. Позвать `rm -rf /`, смотреть в docs/tasks/XR-005.md.\n\n" +
		"```console\n# комментарий\n$ taskctl show XR-005\n\ntaskctl list\n```\n"
	got := scenarioSteps(text)
	want := []string{"taskctl show XR-005", "taskctl list"}
	if len(got) != len(want) {
		t.Fatalf("шаги разобраны неверно: %q", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("шаг %d: %q, ждали %q", i+1, got[i], want[i])
		}
	}
}

// TestRehearseTimeoutKillsStep: вставший шаг убивается по пределу, а не держит
// заход исполнителя до упора.
func TestRehearseTimeoutKillsStep(t *testing.T) {
	root := setupRehearse(t, "sleep 30")
	start := time.Now()
	_, err := cmdRehearse(root, "XR-005", RehearseParams{Timeout: 300 * time.Millisecond, Now: rehearseAt})
	if err == nil {
		t.Fatal("вставший шаг должен валить обкатку")
	}
	if time.Since(start) > 20*time.Second {
		t.Fatal("шаг не убит по пределу")
	}
	if doc := rehearseDoc(t, root, "XR-005"); !strings.Contains(doc, "убит") {
		t.Fatalf("в файле задачи не сказано, что шаг убит:\n%s", doc)
	}
}

func readFileT(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestScenarioStepsTakesOnlyShellBlocks: блок чужого языка это иллюстрация
// (пример конфига, кусок вывода), командой она не считается. Прогон такого
// блока стрелял бы наугад от имени автора сценария.
func TestScenarioStepsTakesOnlyShellBlocks(t *testing.T) {
	text := "\nАгентский.\n\n```json\n{\"rm\": \"-rf /\"}\n```\n\n```\necho безъязыкий\n```\n" +
		"\n```console\ntaskctl show XR-005\n```\n\n```bash\ntaskctl list\n```\n"
	got := scenarioSteps(text)
	want := []string{"taskctl show XR-005", "taskctl list"}
	if len(got) != len(want) {
		t.Fatalf("взяты не те блоки: %q", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("шаг %d: %q, ждали %q", i+1, got[i], want[i])
		}
	}
}

// TestRehearseWithoutShellBlockRefuses: сценарий с одной иллюстрацией и без
// shell-блока обкатывать нечем, и отказ называет выход.
func TestRehearseWithoutShellBlockRefuses(t *testing.T) {
	root := setup(t)
	writeTask(t, root, "XR-005", "# XR-005\n\n## Сценарий проверки\n\n```json\n{\"шаг\": 1}\n```\n")
	gitSetup(t, root)
	_, err := cmdRehearse(root, "XR-005", RehearseParams{Now: rehearseAt})
	if err == nil {
		t.Fatal("сценарий без shell-блока должен отказывать")
	}
	if !strings.Contains(err.Error(), "--step") {
		t.Fatalf("отказ не называет выход: %v", err)
	}
}

// TestRehearseReplacesOwnRecord: повторный прогон не копит блоки, а заменяет
// свою прошлую запись. Отметка в файле остаётся одна, и она про нынешний
// коммит; вложенное руками остаётся на месте.
func TestRehearseReplacesOwnRecord(t *testing.T) {
	root := setupRehearse(t, "echo первый-прогон")
	writeTask(t, root, "XR-005", rehearseDoc(t, root, "XR-005")+"\n## Проверка\n\n- вывод smoke вложен руками.\n")
	gitOut(t, root, "add", ".")
	gitOut(t, root, "commit", "-q", "-m", "правка сценария")
	if _, err := cmdRehearse(root, "XR-005", RehearseParams{Now: rehearseAt}); err != nil {
		t.Fatal(err)
	}
	writeTask(t, root, "XR-005", strings.Replace(rehearseDoc(t, root, "XR-005"),
		"echo первый-прогон", "echo второй-прогон", 1))
	if _, err := cmdRehearse(root, "XR-005", RehearseParams{Now: rehearseAt}); err != nil {
		t.Fatal(err)
	}
	doc := rehearseDoc(t, root, "XR-005")
	if n := strings.Count(doc, taskform.RehearsalNote); n != 1 {
		t.Fatalf("отметок в файле %d, а должна остаться одна:\n%s", n, doc)
	}
	if strings.Contains(doc, "первый-прогон\n```") {
		t.Fatalf("вывод прошлого прогона не унесён:\n%s", doc)
	}
	if !strings.Contains(doc, "второй-прогон") {
		t.Fatalf("вывод нынешнего прогона не записан:\n%s", doc)
	}
	if !strings.Contains(doc, "- вывод smoke вложен руками.") {
		t.Fatalf("вложенное руками унесено вместе с записью:\n%s", doc)
	}
}

// TestRehearseMarkCarriesHeadSha: отметка называет коммит, на котором шёл
// прогон, и это HEAD ветки. По нему ворота отличают свежую обкатку от старой.
func TestRehearseMarkCarriesHeadSha(t *testing.T) {
	root := setupRehearse(t, "echo проба")
	if _, err := cmdRehearse(root, "XR-005", RehearseParams{Now: rehearseAt}); err != nil {
		t.Fatal(err)
	}
	mark, ok := taskform.RehearsalSha(rehearseDoc(t, root, "XR-005"))
	if !ok {
		t.Fatal("отметка без коммита")
	}
	if head := gitOut(t, root, "rev-parse", "HEAD"); !strings.HasPrefix(head, mark) {
		t.Fatalf("коммит отметки %s не от HEAD %s", mark, head)
	}
}

// TestRehearseStepSeesTreeUtility: шаг зовёт утилиту проверяемого дерева по
// имени и получает ту, что собрана из ветки. Из PATH прогона каталоги дома
// убраны, установленных на машине утилит devkit там нет, и до DK-684 такой шаг
// падал с command not found, а сценарий вместо проверки собирал бинарь сам.
func TestRehearseStepSeesTreeUtility(t *testing.T) {
	root := setup(t)
	writeTool(t, root, "xrhello", "go.mod", "module xrhello\n\ngo 1.26\n")
	writeTool(t, root, "xrhello", "main.go",
		"package main\n\nimport \"fmt\"\n\nfunc main() { fmt.Println(\"утилита ветки\") }\n")
	writeTool(t, root, "xrpy", "xrpy.py", "print(\"питон ветки\")\n")
	writeTask(t, root, "XR-005", "# XR-005\n\n## Сценарий проверки\n\nАгентский.\n\n```sh\nxrhello\nxrpy\n```\n")
	gitSetup(t, root)
	msg, err := cmdRehearse(root, "XR-005", RehearseParams{Now: rehearseAt})
	if err != nil {
		t.Fatalf("шаг с утилитой дерева должен проходить обкатку: %v\n%s", err, rehearseDoc(t, root, "XR-005"))
	}
	doc := rehearseDoc(t, root, "XR-005")
	for _, want := range []string{"утилита ветки", "питон ветки", "утилит дерева 2"} {
		if !strings.Contains(doc, want) {
			t.Fatalf("в записи обкатки нет %q:\n%s", want, doc)
		}
	}
	if !strings.Contains(msg, "обкатка зелёная") {
		t.Fatalf("отчёт не называет исход: %q", msg)
	}
}

// TestRehearseNamesMissingCommand: красный шаг по нехватке команды называет её
// саму и места, где прогон искал. В чистом окружении это первое, обо что
// спотыкается сценарий, и голый «exit status 127» разбирать нечем.
func TestRehearseNamesMissingCommand(t *testing.T) {
	root := setupRehearse(t, "xrmissingtool --help")
	if _, err := cmdRehearse(root, "XR-005", RehearseParams{Now: rehearseAt}); err == nil {
		t.Fatal("шаг с несуществующей командой должен краснеть")
	}
	doc := rehearseDoc(t, root, "XR-005")
	for _, want := range []string{"команды `xrmissingtool` в прогоне нет", "искали в PATH прогона", "утилит дерево не несёт"} {
		if !strings.Contains(doc, want) {
			t.Fatalf("разбор отказа не называет %q:\n%s", want, doc)
		}
	}
}

func writeTool(t *testing.T, root, tool, name, body string) {
	t.Helper()
	dir := filepath.Join(root, "tools", tool)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
