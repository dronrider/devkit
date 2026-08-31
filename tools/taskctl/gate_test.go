package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dronrider/devkit/internal/stage"
)

// dropScenario оставляет задаче файл без раздела сценария: так выглядит файл,
// заведённый в начале работы и не дописанный к переводу в Check.
func dropScenario(t *testing.T, root, id string) {
	t.Helper()
	p := filepath.Join(root, "docs", "tasks", id+".md")
	if err := os.WriteFile(p, []byte("# "+id+"\n\n## Ход работы\n\n- начали\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// branchWithCommit заводит ветку с одним коммитом и возвращает дерево на main.
func branchWithCommit(t *testing.T, root, branch string) {
	t.Helper()
	gitOut(t, root, "checkout", "-q", "-b", branch)
	if err := os.WriteFile(filepath.Join(root, "work.txt"), []byte(branch+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitOut(t, root, "add", "work.txt")
	gitOut(t, root, "commit", "-q", "-m", "feat: работа ветки")
	gitOut(t, root, "checkout", "-q", "main")
}

func sect(t *testing.T, root, id string) string {
	t.Helper()
	b, err := LoadBoard(boardPath(root))
	if err != nil {
		t.Fatal(err)
	}
	r := b.find(id)
	if r == nil {
		t.Fatalf("%s нет на доске", id)
	}
	return r.Sect
}

func TestMoveToCheckNeedsScenarioSection(t *testing.T) {
	root := setup(t)
	dropScenario(t, root, "XR-005")
	_, err := cmdMove(root, "XR-005", SectCheck, "", CommitOpts{})
	if err == nil {
		t.Fatal("move в Check без раздела сценария должен падать")
	}
	if !strings.Contains(err.Error(), "Сценарий проверки") {
		t.Fatalf("отказ не называет причину: %v", err)
	}
	if s := sect(t, root, "XR-005"); s != SectInProgress {
		t.Fatalf("строка уехала при отказе: %s", s)
	}
}

func TestMoveToCheckNeedsTaskFile(t *testing.T) {
	root := setup(t)
	// Строка старого запаса: без файла задачи изменяющий move отказывает
	// раньше ворот Check и называет taskctl file (DK-394).
	dropTaskFile(t, root, "XR-004")
	_, err := cmdMove(root, "XR-004", SectInProgress, "", CommitOpts{})
	if err == nil {
		t.Fatal("move строке без файла задачи должен падать")
	}
	if !strings.Contains(err.Error(), "taskctl file XR-004") {
		t.Fatalf("отказ не называет следующий шаг: %v", err)
	}
}

func TestMoveToCheckPassesWithScenario(t *testing.T) {
	root := setup(t)
	if _, err := cmdMove(root, "XR-005", SectCheck, "", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	if s := sect(t, root, "XR-005"); s != SectCheck {
		t.Fatalf("строка не дошла до Check: %s", s)
	}
}

func TestMoveToCheckAcceptsAgentScenario(t *testing.T) {
	root := setup(t)
	p := filepath.Join(root, "docs", "tasks", "XR-005.md")
	if err := os.WriteFile(p, []byte("# XR-005\n\n## Сценарий проверки (агентский)\n\n1. Прогнать.\n"+fixtureVerification), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdMove(root, "XR-005", SectCheck, "", CommitOpts{}); err != nil {
		t.Fatalf("агентский сценарий тоже сценарий: %v", err)
	}
}

// Заголовок внутри ограждённого блока это чужой вывод, вложенный в файл, а не
// сценарий этой задачи.
func TestMoveToCheckIgnoresFencedScenario(t *testing.T) {
	root := setup(t)
	p := filepath.Join(root, "docs", "tasks", "XR-005.md")
	body := "# XR-005\n\n## Ход работы\n\n```\n## Сценарий проверки\n\n1. Чужой вывод.\n```\n"
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdMove(root, "XR-005", SectCheck, "", CommitOpts{}); err == nil {
		t.Fatal("сценарий в ограждённом блоке не считается разделом файла")
	}
}

// LLD-задача описывает проверку в дизайне, и строка ведёт туда: файла задачи у
// неё может не быть, и ворота такую строку пропускают.
func TestMoveToCheckSkipsRowLinkedToLLD(t *testing.T) {
	root := setup(t)
	if _, err := cmdMove(root, "XR-001", SectInProgress, "", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdSet(root, SetParams{ID: "XR-001", Link: "[lld/search.md](lld/search.md)"}); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdMove(root, "XR-001", SectCheck, "", CommitOpts{}); err != nil {
		t.Fatalf("строка со ссылкой на LLD должна проходить: %v", err)
	}
}

func TestMoveToCheckRefusesUnmergedBranch(t *testing.T) {
	root := setup(t)
	gitSetup(t, root)
	branchWithCommit(t, root, "xr-005-gate")
	_, err := cmdMove(root, "XR-005", SectCheck, "", CommitOpts{})
	if err == nil {
		t.Fatal("move в Check при неслитой ветке должен падать")
	}
	for _, want := range []string{"xr-005-gate", "shipctl merge XR-005"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("в отказе нет %q: %v", want, err)
		}
	}
	if s := sect(t, root, "XR-005"); s != SectInProgress {
		t.Fatalf("строка уехала при отказе: %s", s)
	}
}

func TestMoveToCheckPassesMergedBranch(t *testing.T) {
	root := setup(t)
	gitSetup(t, root)
	branchWithCommit(t, root, "xr-005-gate")
	gitOut(t, root, "merge", "-q", "--ff-only", "xr-005-gate")
	if _, err := cmdMove(root, "XR-005", SectCheck, "", CommitOpts{}); err != nil {
		t.Fatalf("слитая ветка воротам не помеха: %v", err)
	}
}

// Чужая ветка с похожим началом имени не считается веткой задачи: опознание
// то же, что у shipctl, по ID до дефиса.
func TestMoveToCheckIgnoresForeignBranch(t *testing.T) {
	root := setup(t)
	gitSetup(t, root)
	branchWithCommit(t, root, "xr-0051-other")
	if _, err := cmdMove(root, "XR-005", SectCheck, "", CommitOpts{}); err != nil {
		t.Fatalf("чужая ветка не должна держать строку: %v", err)
	}
}

func TestLintFindsUnmergedCheckBranch(t *testing.T) {
	root := setup(t)
	gitSetup(t, root)
	if _, err := cmdMove(root, "XR-005", SectCheck, "", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	branchWithCommit(t, root, "xr-005-gate")
	finds, err := cmdLint(root)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(finds, "\n")
	for _, want := range []string{"XR-005 в Check", "xr-005-gate", "shipctl merge XR-005"} {
		if !strings.Contains(got, want) {
			t.Fatalf("в находках нет %q:\n%s", want, got)
		}
	}
	gitOut(t, root, "merge", "-q", "--ff-only", "xr-005-gate")
	finds, err = cmdLint(root)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(finds, "\n"); strings.Contains(got, "xr-005-gate") {
		t.Fatalf("слитая ветка не находка:\n%s", got)
	}
}

// dropVerification оставляет задаче файл без раздела «Проверка»: так выглядит
// агентская задача, закрытая до того, как вывод прогона вложен.
func dropVerification(t *testing.T, root, id string) {
	t.Helper()
	p := filepath.Join(root, "docs", "tasks", id+".md")
	if err := os.WriteFile(p, []byte("# "+id+"\n"+fixtureScenario), 0o644); err != nil {
		t.Fatal(err)
	}
}

// emptyVerification заводит файл с пустым разделом «Проверка»: заголовок стоит,
// а тела нет, и ворота обязаны считать это пустым закрытием.
func emptyVerification(t *testing.T, root, id string) {
	t.Helper()
	p := filepath.Join(root, "docs", "tasks", id+".md")
	if err := os.WriteFile(p, []byte("# "+id+"\n"+fixtureScenario+"\n## Проверка\n\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestCloseAgentGateRejectsWithoutVerification: агентская задача без раздела
// «Проверка» воротами close не закрывается (LLD DK-292, решение 4). На старом
// коде ворот не было, и пустое закрытие проходило.
func TestCloseAgentGateRejectsWithoutVerification(t *testing.T) {
	root := setup(t)
	dropVerification(t, root, "XR-005")
	_, err := cmdClose(root, CloseParams{ID: "XR-005", Date: "2026-07-08"})
	if err == nil {
		t.Fatal("close агентской задачи без «Проверка» должен падать")
	}
	if !strings.Contains(err.Error(), "Проверка") {
		t.Fatalf("отказ не называет раздел «Проверка»: %v", err)
	}
	// Строка осталась на доске, в архив не уехала.
	b, _ := LoadBoard(boardPath(root))
	if b.find("XR-005") == nil {
		t.Fatal("строка уехала в архив при отказе ворот")
	}
}

// TestCloseAgentGateRejectsEmptyVerification: пустой раздел «Проверка»
// считается отсутствующим, ворота требуют непустого тела с реальным выводом.
func TestCloseAgentGateRejectsEmptyVerification(t *testing.T) {
	root := setup(t)
	emptyVerification(t, root, "XR-005")
	if _, err := cmdClose(root, CloseParams{ID: "XR-005", Date: "2026-07-08"}); err == nil {
		t.Fatal("close агентской задачи с пустым разделом «Проверка» должен падать")
	}
}

// TestCloseAgentGatePassesWithVerification: непустой раздел «Проверка»
// пропускает агентскую задачу в архив.
func TestCloseAgentGatePassesWithVerification(t *testing.T) {
	root := setup(t)
	// XR-005 в фикстуре уже с непустой «Проверкой» (fixtureVerification).
	if _, err := cmdClose(root, CloseParams{ID: "XR-005", Date: "2026-07-08"}); err != nil {
		t.Fatalf("close агентской задачи с непустой «Проверкой» должен пройти: %v", err)
	}
	arch, _ := LoadArchive(archivePath(root))
	if !arch.has("XR-005") {
		t.Fatal("задача не уехала в архив")
	}
}

// TestCloseAgentGateIgnoresFencedVerification: заголовок «## Проверка» внутри
// ограждённого блока это чужой вывод, вложенный в файл, а не раздел этой задачи,
// и ворота его не считают.
func TestCloseAgentGateIgnoresFencedVerification(t *testing.T) {
	root := setup(t)
	p := filepath.Join(root, "docs", "tasks", "XR-005.md")
	body := "# XR-005\n" + fixtureScenario +
		"\n```\n## Проверка\n\n- чужой вывод.\n```\n"
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdClose(root, CloseParams{ID: "XR-005", Date: "2026-07-08"}); err == nil {
		t.Fatal("«Проверка» в ограждённом блоке не считается разделом файла")
	}
}

// TestCloseNonAgentSkipsVerificationGate: не агентский вид воротами close не
// трогается, его перебор обходов держат ворота move check. user-задача без
// раздела «Проверка» закрывается, как и раньше.
func TestCloseNonAgentSkipsVerificationGate(t *testing.T) {
	root := setup(t)
	if _, err := cmdAdd(root, AddParams{ID: "XR-100", Title: "Пользовательская", Type: "task", Rank: "0+1+1+0+1", Accept: "user", Barrier: "событие"}); err != nil {
		t.Fatal(err)
	}
	acceptanceBody := "# XR-100: Пользовательская\n" +
		"\n## Приёмка\n\n- вид: user\n- барьер «событие»: причина\n" +
		"  - событие в логе: годится\n" +
		"  - пустой лог: годится\n" +
		"  - сторонний источник: годится\n"
	if err := os.WriteFile(filepath.Join(root, "docs", "tasks", "XR-100.md"), []byte(acceptanceBody), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdClose(root, CloseParams{ID: "XR-100", Date: "2026-07-08"}); err != nil {
		t.Fatalf("не агентский вид закрывается без «Проверки»: %v", err)
	}
}

// commitFile кладёт файл в main отдельным коммитом с заданной темой: так
// выглядит коммит задачи, по теме которого подсказка и ищет промпты.
func commitFile(t *testing.T, root, rel, subject string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("текст "+rel+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitOut(t, root, "add", rel)
	gitOut(t, root, "commit", "-q", "-m", subject)
}

// Задача правила скилл и файл правил: перевод в Check проходит, а в выводе
// стоит строка про стенд. Подсказка ищет по коммитам с ID в теме, поэтому
// работает и на слитой задаче, у которой диффа ветки против main уже нет.
func TestMoveToCheckHintsPromptDiff(t *testing.T) {
	root := setup(t)
	gitSetup(t, root)
	commitFile(t, root, "kit/skills/board-draft/SKILL.md", "feat(skills): XR-005 черновик доски")
	commitFile(t, root, "RULES.md", "docs: XR-005 правило выноса")
	out, err := cmdMove(root, "XR-005", SectCheck, "", CommitOpts{})
	if err != nil {
		t.Fatalf("подсказка не должна отказывать: %v", err)
	}
	for _, want := range []string{"prompt-test", "«Проверка»", "kit/skills/board-draft/SKILL.md", "RULES.md"} {
		if !strings.Contains(out, want) {
			t.Fatalf("в выводе нет %q:\n%s", want, out)
		}
	}
	if s := sect(t, root, "XR-005"); s != SectCheck {
		t.Fatalf("строка не доехала до Check: %s", s)
	}
}

// Задача правила только код и свою доку: подсказке взяться неоткуда, и вывод
// про стенд молчит.
func TestMoveToCheckSilentWithoutPromptDiff(t *testing.T) {
	root := setup(t)
	gitSetup(t, root)
	commitFile(t, root, "tools/taskctl/ops.go", "feat(taskctl): XR-005 подсказка ворот")
	commitFile(t, root, "tools/taskctl/README.md", "docs(taskctl): XR-005 строка про подсказку")
	out, err := cmdMove(root, "XR-005", SectCheck, "", CommitOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "prompt-test") {
		t.Fatalf("подсказка без промптов в коммитах:\n%s", out)
	}
}

// Чужая задача с похожим номером не считается своей: тема коммита XR-0051
// подсказку XR-005 не поднимает.
func TestMoveToCheckIgnoresLongerTaskID(t *testing.T) {
	root := setup(t)
	gitSetup(t, root)
	commitFile(t, root, "kit/skills/board-draft/SKILL.md", "feat(skills): XR-0051 чужой скилл")
	out, err := cmdMove(root, "XR-005", SectCheck, "", CommitOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "prompt-test") {
		t.Fatalf("подсказку подняла чужая задача:\n%s", out)
	}
}

func TestPromptPath(t *testing.T) {
	yes := []string{"kit/skills/prompt-test/SKILL.md", "kit/agents/exec-high.md",
		"RULES.md", "RULES.board.core.md", "TASKFORM.md", "RANKING.md", "ACCEPTANCE.md"}
	no := []string{"tools/taskctl/gate.go", "docs/TASKS.md", "docs/tasks/XR-005.md",
		"README.md", "docs/RULES.md", "kit/harness/claude-code.toml"}
	for _, p := range yes {
		if !promptPath(p) {
			t.Fatalf("%s это промпт, а не опознан", p)
		}
	}
	for _, p := range no {
		if promptPath(p) {
			t.Fatalf("%s промптом не считается", p)
		}
	}
}

// Прогон сценария чужими руками (DK-642): closeVerifyGate сверяет прогонявшего
// с исполнителем последнего этапа «разработка», и совпадение имён закрытия не
// даёт. Задача без записи прогона проходит молча, на этом держится
// автозакрытие тиком (DK-516).

// stagedDoc кладёт файл задачи с разделом «Ход работы» из данных строк, дальше
// сценарий и непустая «Проверка», чтобы ворота агентского вида не мешали.
func stagedDoc(t *testing.T, root, id string, lines ...string) {
	t.Helper()
	doc := "# " + id + "\n\n## Ход работы\n\n" + strings.Join(lines, "\n") + "\n" +
		fixtureScenario + fixtureVerification
	if err := os.WriteFile(filepath.Join(root, "docs", "tasks", id+".md"), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
}

func devStageLine(model string) string {
	return "- Разработка: субагент " + model + "/high по вердикту pick, 2026-08-30 10:00-11:00."
}

func verifyStageLine(model string) string {
	return "- Проверка: сценарий прогнал " + model + ", 2026-08-31 12:00."
}

func TestCloseRefusesAuthorVerifyRun(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root := setup(t)
	stagedDoc(t, root, "XR-005", devStageLine("opus"), verifyStageLine("opus"))
	_, err := cmdClose(root, CloseParams{ID: "XR-005", Date: "2026-07-08"})
	if err == nil {
		t.Fatal("закрытие с прогоном под исполнителем разработки прошло")
	}
	for _, want := range []string{"opus", "не автор"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("в отказе нет %q: %v", want, err)
		}
	}
}

func TestClosePassesForeignVerifyRun(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root := setup(t)
	stagedDoc(t, root, "XR-005", devStageLine("opus"), verifyStageLine("sonnet"))
	if _, err := cmdClose(root, CloseParams{ID: "XR-005", Date: "2026-07-08"}); err != nil {
		t.Fatalf("закрытие с чужим прогоном: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(root, "docs", "tasks", "archive", "2026", "XR-005.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "сценарий прогнал sonnet") {
		t.Fatal("строка прогона не доехала до архивного файла задачи")
	}
}

func TestCloseVerifyGateSilentWithoutRecord(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root := setup(t)
	stagedDoc(t, root, "XR-005", devStageLine("opus"))
	if err := closeVerifyGate(root, "XR-005"); err != nil {
		t.Fatalf("задача без записи прогона обязана проходить: %v", err)
	}
}

func TestCloseVerifyGateReadsPendingStage(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root := setup(t)
	stagedDoc(t, root, "XR-005", devStageLine("opus"))
	if err := stage.Open(stage.Home(), root, "XR-005", stage.Verify, stage.VerifyNote("opus"), time.Now()); err != nil {
		t.Fatal(err)
	}
	err := closeVerifyGate(root, "XR-005")
	if err == nil || !strings.Contains(err.Error(), "opus") {
		t.Fatalf("незакрытый пакет с прогоном автора не остановил ворота: %v", err)
	}
	// Чужое имя в том же пакете свежее записи автора: закрытие проходит.
	if err := stage.Open(stage.Home(), root, "XR-005", stage.Verify, stage.VerifyNote("sonnet"), time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := closeVerifyGate(root, "XR-005"); err != nil {
		t.Fatalf("свежий чужой прогон обязан открывать ворота: %v", err)
	}
}

func TestCloseVerifyGateCaseInsensitive(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root := setup(t)
	stagedDoc(t, root, "XR-005", devStageLine("opus"), verifyStageLine("Opus"))
	err := closeVerifyGate(root, "XR-005")
	if err == nil || !strings.Contains(err.Error(), "Opus") {
		t.Fatalf("«Opus» против «opus» прошёл за чужой прогон: %v", err)
	}
}

// TestMoveToCheckNeedsRehearsal: агентскую задачу без отметки обкатки в Check
// не пускают, отказ называет команду, и строка остаётся на месте.
func TestMoveToCheckNeedsRehearsal(t *testing.T) {
	root := setup(t)
	writeTask(t, root, "XR-005", "# XR-005\n"+fixtureScenario)
	_, err := cmdMove(root, "XR-005", SectCheck, "", CommitOpts{})
	if err == nil {
		t.Fatal("move в Check без обкатки должен падать")
	}
	if !strings.Contains(err.Error(), "taskctl rehearse XR-005") {
		t.Fatalf("отказ не называет команду обкатки: %v", err)
	}
	if s := sect(t, root, "XR-005"); s != SectInProgress {
		t.Fatalf("строка уехала при отказе: %s", s)
	}
}

// TestMoveToCheckPassesAfterRehearsal: отметка от rehearse открывает ворота.
func TestMoveToCheckPassesAfterRehearsal(t *testing.T) {
	root := setup(t)
	writeTask(t, root, "XR-005", "# XR-005\n"+fixtureScenario+"\n## Проверка\n\n"+fixtureRehearsal)
	if _, err := cmdMove(root, "XR-005", SectCheck, "", CommitOpts{}); err != nil {
		t.Fatalf("обкатанный сценарий ворота держать не должны: %v", err)
	}
}

// TestMoveToCheckRehearsalException: пометка-исключение гасит ворот тем же
// порядком, что у ворот слияния.
func TestMoveToCheckRehearsalException(t *testing.T) {
	root := setup(t)
	writeTask(t, root, "XR-005", "# XR-005\n"+fixtureScenario+
		"\n## Ход работы\n\n- Исключение: обкатка (шаги проверяются на проде)\n")
	if _, err := cmdMove(root, "XR-005", SectCheck, "", CommitOpts{}); err != nil {
		t.Fatalf("пометка-исключение ворот не погасила: %v", err)
	}
}

// TestMoveToCheckQuotedRehearsalDoesNotPass: отметка, процитированная в
// ограждённом блоке вместе с чужим выводом, ворота не открывает.
func TestMoveToCheckQuotedRehearsalDoesNotPass(t *testing.T) {
	root := setup(t)
	writeTask(t, root, "XR-005", "# XR-005\n"+fixtureScenario+
		"\n## Проверка\n\n```console\n$ taskctl show XR-002\n"+fixtureRehearsal+"```\n")
	if _, err := cmdMove(root, "XR-005", SectCheck, "", CommitOpts{}); err == nil {
		t.Fatal("процитированная отметка открыла ворота")
	}
}

// TestMoveToCheckSkipsRehearsalForUserKind: у не агентского вида часть шагов
// держит человек, машинной отметки с него не спрашивают.
func TestMoveToCheckSkipsRehearsalForUserKind(t *testing.T) {
	root := setup(t)
	if _, err := cmdSet(root, SetParams{ID: "XR-005", Accept: "user"}); err != nil {
		t.Fatal(err)
	}
	writeTask(t, root, "XR-005", "# XR-005\n"+fixtureScenario+
		"\n## Приёмка\n\n- барьер «глаза»:\n  - слепок не годится: правится вёрстка\n  - разметка не годится: смотрят на глаз\n  - метрика не годится: судит человек\n")
	if _, err := cmdMove(root, "XR-005", SectCheck, "", CommitOpts{}); err != nil {
		t.Fatalf("вид user отметку обкатки спрашивать не должен: %v", err)
	}
}
