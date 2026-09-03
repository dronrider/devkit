package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const rowInProg4 = "| XR-004 | Третья мелочь | task | P3 | 8 (0+3+0+5+0) |  |\n"

// TestTrainDocsOnlyGoesToCheck: у задачи пачки бывает нет кода (правка одной
// доки). Поезд её не везёт, состав считается по коммитам вне docs/, и до этой
// правки она молча оставалась бы в In progress навсегда: ship переводит в
// Check только поезд. Теперь её переводит сам merge, коммитом доски и без
// выката, а отчёт говорит, что поезд её не везёт.
func TestTrainDocsOnlyGoesToCheck(t *testing.T) {
	root, callLog := setup(t, rowInProg, "")
	gitT(t, root, "checkout", "-qb", "xr-001-docs", "main")
	write(t, root, "docs/lld/train.md", "# LLD\n")
	gitT(t, root, "add", ".")
	gitT(t, root, "commit", "-qm", "docs: XR-001 правка LLD")

	msg, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true", Train: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "поезд задачу не везёт") || !strings.Contains(msg, "в Check, коммит") {
		t.Fatalf("бескодовая задача должна уехать в Check: %q", msg)
	}
	if calls, _ := os.ReadFile(callLog); !strings.Contains(string(calls), "move XR-001 check") {
		t.Fatalf("taskctl move в Check не вызван: %q", calls)
	}
	if log := gitT(t, root, "log", "-1", "--format=%s", "main"); !strings.Contains(log, "XR-001 в Check") {
		t.Fatalf("коммита доски нет: %s", log)
	}
	// Точку выката бескодовое слияние не заводит: везти на прод нечего.
	if _, err := git(root, "rev-parse", "--verify", deployTag); err == nil {
		t.Fatal("бескодовое поездное слияние не должно заводить тег deployed")
	}
	if _, err := cmdShip(root, ShipParams{Deploy: "true"}); err == nil ||
		!strings.Contains(err.Error(), "поезд пуст") {
		t.Fatalf("после бескодового слияния поезд должен остаться пустым: %v", err)
	}
}

// TestTrainCodeMissingFromTrainWarns: код слит, а состав поезда задачу не
// видит. Так бывает, когда верхний коммит ветки распознан как откат: поиск
// коммитов задачи упирается в него и отвечает «кода нет». Задача остаётся в In
// progress, ship её не повезёт и в Check не переведёт, поэтому merge говорит
// про это отдельной строкой, а не молчит.
func TestTrainCodeMissingFromTrainWarns(t *testing.T) {
	root, callLog := setup(t, rowInProg, "")
	gitT(t, root, "checkout", "-qb", "xr-001-fix", "main")
	write(t, root, "a.txt", "новое\n")
	write(t, root, "fix_test.go", "package main\n")
	gitT(t, root, "add", ".")
	gitT(t, root, "commit", "-qm", "fix: XR-001 правка")
	write(t, root, "code.txt", "вернули как было\n")
	gitT(t, root, "add", ".")
	gitT(t, root, "commit", "-qm", "revert: XR-001 откат прошлой правки")

	msg, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true", Train: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "не попала в поезд") {
		t.Fatalf("пропажу задачи из поезда надо называть вслух: %q", msg)
	}
	if calls, _ := os.ReadFile(callLog); strings.Contains(string(calls), "move XR-001") {
		t.Fatalf("такая задача остаётся в In progress: %q", calls)
	}
	// Правка на main лежит, и это ровно то, о чём предупреждение.
	if got, _ := os.ReadFile(filepath.Join(root, "a.txt")); string(got) != "новое\n" {
		t.Fatalf("код не доехал до main: %q", got)
	}
}

// TestTrainEmptyBranchIsNotDocsOnly: у ветки без коммитов слитого диапазона
// нет вовсе, и бескодовой такая задача не считается. Иначе забытая ветка
// уезжала бы в Check как сделанная правка доки.
func TestTrainEmptyBranchIsNotDocsOnly(t *testing.T) {
	root, callLog := setup(t, rowInProg, "")
	if _, err := cmdStart(root, StartParams{ID: "XR-001", Slug: "fix"}); err != nil {
		t.Fatal(err)
	}
	msg, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true", Train: true})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(msg, "поезд задачу не везёт") {
		t.Fatalf("пустая ветка это не бескодовая задача: %q", msg)
	}
	if calls, _ := os.ReadFile(callLog); strings.Contains(string(calls), "move XR-001 check") {
		t.Fatalf("пустая ветка не должна уезжать в Check: %q", calls)
	}
}

// TestTrainScenarioGate: поезд переводит в Check всю пачку разом и уже после
// выката, поэтому ворот сценария отказывает до слияния, если у задачи нет
// раздела «Сценарий проверки». Признак это заголовок вне ограждённых блоков.
func TestTrainScenarioGate(t *testing.T) {
	root, _ := setup(t, rowInProg+rowInProg3, "")

	// У XR-003 файла задачи нет вовсе: сценария нет, ворот отказывает.
	branchFor(t, root, "XR-003", "xr-003-fix", "b.txt")
	_, err := cmdMerge(root, MergeParams{ID: "XR-003", Test: "true", Train: true})
	if err == nil || !strings.Contains(err.Error(), "нет раздела") {
		t.Fatalf("задача без сценария должна отбиваться воротом: %v", err)
	}

	// У XR-001 сценарий в файле задачи есть, и ворот его пропускает.
	branchFor(t, root, "XR-001", "xr-001-fix", "a.txt")
	msg, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true", Train: true})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(msg, "Сценарий проверки") {
		t.Fatalf("при готовом сценарии отказ лишний: %q", msg)
	}
}


// TestTrainDocsOnlyScenarioGate: бескодовая задача без сценария отбивается
// воротом: выката нет, и подтвердить её по сценарию это единственный способ, а
// не повтор проверки прода. Отказ называет того, кто задачу переводит (этот
// merge, без выката), а не ship, который бескодовую задачу не везёт.
func TestTrainDocsOnlyScenarioGate(t *testing.T) {
	root, _ := setup(t, rowInProg+rowInProg3, "")
	gitT(t, root, "checkout", "-qb", "xr-003-docs", "main")
	write(t, root, "docs/lld/train.md", "# LLD\n")
	gitT(t, root, "add", ".")
	gitT(t, root, "commit", "-qm", "docs: XR-003 правка LLD")

	_, err := cmdMerge(root, MergeParams{ID: "XR-003", Test: "true", Train: true})
	if err == nil || !strings.Contains(err.Error(), "бескодовую задачу merge переведёт в Check без выката") {
		t.Fatalf("бескодовая задача без сценария должна отбиваться: %v", err)
	}
	if strings.Contains(err.Error(), "ship переведёт") {
		t.Fatalf("ship бескодовую задачу не везёт, обещать его нельзя: %v", err)
	}
}


// TestTrainScenarioProseIsNotScenario: упоминание сценария в прозе («сценарий
// проверки напишу после выката») разделом не считается, и ворот отбивается:
// признак это заголовок.
func TestTrainScenarioProseIsNotScenario(t *testing.T) {
	root, _ := setup(t, rowInProg+rowInProg3, "")
	branchFor(t, root, "XR-003", "xr-003-fix", "b.txt")
	write(t, root, "docs/tasks/XR-003.md", "# XR-003\n\n## Ход работы\n\nСценарий проверки напишу после выката.\n")
	gitT(t, root, "add", ".")
	gitT(t, root, "commit", "-qm", "docs(tasks): XR-003 ход работы")
	_, err := cmdMerge(root, MergeParams{ID: "XR-003", Test: "true", Train: true})
	if err == nil || !strings.Contains(err.Error(), "нет раздела") {
		t.Fatalf("проза разделом не считается, ворот должен отбиться: %v", err)
	}
}


// TestTrainScenarioWarningReadsBranch: сценарий пишется в дереве задачи, и
// читать его merge обязан там: в основном чекауте на main файла ещё нет.
func TestTrainScenarioWarningReadsBranch(t *testing.T) {
	root, _ := setup(t, rowInProg+rowInProg3, "")
	wt := startTask(t, root, "XR-003", "b.txt")
	write(t, wt, "docs/tasks/XR-003.md", "# XR-003\n\n## Сценарий проверки\n\nАгентский: shipctl status.\n"+fixtureReviewLevel)
	gitT(t, wt, "add", ".")
	gitT(t, wt, "commit", "-qm", "docs(tasks): XR-003 сценарий проверки")
	msg, err := cmdMerge(root, MergeParams{ID: "XR-003", Test: "true", Train: true})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(msg, "нет раздела «Сценарий проверки»") {
		t.Fatalf("сценарий лежит на ветке, подсказка лишняя: %q", msg)
	}
}

// TestTrainPipelineThreeTasks: боевой прогон пачки на временном репозитории.
// Три задачи стартуют в своих деревьях, сливаются в порядке, отличном от
// порядка старта (готовыми они приезжают вперемешку), и уезжают одним
// выкатом. Проверяется, что предусловия не сработали ложно, состав поезда
// полный и все три ушли в Check.
func TestTrainPipelineThreeTasks(t *testing.T) {
	root, callLog := setup(t, rowInProg+rowInProg3+rowInProg4, "")
	taskWithScenario(t, root, "XR-003")
	taskWithScenario(t, root, "XR-004")
	ids := []string{"XR-001", "XR-003", "XR-004"}
	wts := map[string]string{}
	for i, id := range ids {
		wts[id] = startTask(t, root, id, string(rune('a'+i))+".txt")
	}
	for _, id := range ids {
		if br := gitT(t, wts[id], "rev-parse", "--abbrev-ref", "HEAD"); br != strings.ToLower(id)+"-fix" {
			t.Fatalf("%s: в дереве стоит %q", id, br)
		}
	}
	// Порядок слияния свой: XR-004 приехала раньше остальных.
	for _, id := range []string{"XR-004", "XR-001", "XR-003"} {
		msg, err := cmdMerge(root, MergeParams{ID: id, Test: "true", Train: true})
		if err != nil {
			t.Fatalf("поездное слияние %s не прошло: %v", id, err)
		}
		if !strings.Contains(msg, "в поезде: ") {
			t.Fatalf("%s: в отчёте нет состава поезда: %q", id, msg)
		}
	}
	st, err := cmdStatus(root)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(st, "поезд: XR-001, XR-003, XR-004 слиты") || strings.Contains(st, "аномалия") {
		t.Fatalf("состав поезда перед выкатом:\n%s", st)
	}
	msg, err := cmdShip(root, ShipParams{Deploy: "touch shipped.marker"})
	if err != nil {
		t.Fatalf("выкат поезда не прошёл: %v", err)
	}
	if !strings.Contains(msg, "поезд выкачен (XR-001, XR-003, XR-004)") {
		t.Fatalf("выкат поезда: %q", msg)
	}
	calls, _ := os.ReadFile(callLog)
	for _, id := range ids {
		if !strings.Contains(string(calls), "move "+id+" check") {
			t.Fatalf("%s не переведена в Check: %q", id, calls)
		}
		if _, err := os.Stat(wts[id]); err == nil {
			t.Fatalf("дерево задачи %s не убрано", id)
		}
	}
	if gitT(t, root, "rev-parse", deployTag) != gitT(t, root, "rev-parse", "main^") {
		t.Fatal("тег deployed не сдвинут на выкаченный main")
	}
}

// TestTrainNeighbourMentionIsNotOwnership: упомянуть соседнюю задачу в subject
// законно, и владельцем коммита такое упоминание её не делает. По прежнему
// правилу «ID словом где угодно» коммит записывался в код соседки, та приходила
// осиротевшей и наглухо отбивала слияние чужой задачи.
func TestTrainNeighbourMentionIsNotOwnership(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	gitT(t, root, "checkout", "-qb", "xr-001-fix", "main")
	write(t, root, "a.txt", "новое\n")
	write(t, root, "fix_test.go", "package main\n")
	gitT(t, root, "add", ".")
	gitT(t, root, "commit", "-qm", "fix: XR-001 правка, пробел DoD цели XR-002 снят")

	msg, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true", Train: true})
	if err != nil {
		t.Fatalf("упоминание соседки в subject отбило слияние: %v", err)
	}
	if strings.Contains(msg, "XR-002") {
		t.Fatalf("соседка попала в отчёт слияния: %q", msg)
	}
	st, err := cmdStatus(root)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(st, "аномалия") || strings.Contains(st, "XR-002") {
		t.Fatalf("соседка из Backlog пришла осиротевшей:\n%s", st)
	}
	if _, err := cmdShip(root, ShipParams{Deploy: "true"}); err != nil {
		t.Fatalf("выкат поезда с упоминанием соседки не прошёл: %v", err)
	}
}

// TestRevertSkipsNeighbourMention: цена той же ошибки на откате выше, чем на
// слиянии. Коммит соседки, упомянувшей задачу в тексте subject, в состав отката
// не берётся: revert вернул бы чужую правку.
func TestRevertSkipsNeighbourMention(t *testing.T) {
	root, _ := setup(t, "", rowInProg) // XR-001 уже в Check
	write(t, root, "code.txt", "broken\n")
	gitT(t, root, "add", ".")
	gitT(t, root, "commit", "-qm", "feat: XR-001 фича")
	own := gitT(t, root, "rev-parse", "HEAD")
	write(t, root, "neighbour.txt", "чужая правка\n")
	gitT(t, root, "add", ".")
	gitT(t, root, "commit", "-qm", "fix: XR-002 своя правка, пробел DoD цели XR-001 снят")

	shas, err := taskCommits(root, "main", "XR-001")
	if err != nil {
		t.Fatal(err)
	}
	if len(shas) != 1 || shas[0] != own {
		t.Fatalf("в откат XR-001 попал чужой коммит: %v, свой %s", shas, own)
	}
	if _, err := cmdRevert(root, RevertParams{ID: "XR-001", Test: "true"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "neighbour.txt")); err != nil {
		t.Fatal("откат XR-001 снёс правку соседки")
	}
	if got, _ := os.ReadFile(filepath.Join(root, "code.txt")); string(got) != "old\n" {
		t.Fatalf("свой код не откатился: %q", got)
	}
}

// stubTaskctl подменяет стаб taskctl в PATH: body встаёт перед строкой,
// которая имитирует правку доски, и решает, каким вызовам отказать. Так
// в стенде проигрывается отказ ворот перевода, живущих в самом taskctl.
func stubTaskctl(t *testing.T, callLog, body string) {
	t.Helper()
	bin := filepath.Dir(callLog)
	write(t, bin, "taskctl", "#!/bin/sh\necho \"$@\" >> \""+callLog+"\"\n"+body+
		"printf '<!-- move -->\\n' >> \"$2/docs/TASKS.md\"\n")
	if err := os.Chmod(filepath.Join(bin, "taskctl"), 0o755); err != nil {
		t.Fatal(err)
	}
}

// realMoves считает настоящие переводы в Check в логе стаба: сухой прогон
// ворот идёт той же командой с --dry-run, и по одному вхождению «move ID
// check» эти два вызова не различить.
func realMoves(t *testing.T, callLog, id string) int {
	t.Helper()
	calls, _ := os.ReadFile(callLog)
	n := 0
	for _, ln := range strings.Split(string(calls), "\n") {
		if strings.HasSuffix(strings.TrimSpace(ln), "move "+id+" check") {
			n++
		}
	}
	return n
}

// TestTrainGateBeforeDeploy воспроизводит разлив 2026-09-03: в составе поезда
// строка со сценарием и строка без него, и перевод второй отбивают ворота.
// Пока ворота спрашивались уже после деплоя, выкат проходил, точка выката
// уезжала вперёд, обе строки оставались в In progress, а повторный ship
// отвечал «поезд пуст». Теперь состав проверяется до выката: отказ называет
// строку, деплой не запускается, точка стоит на месте.
func TestTrainGateBeforeDeploy(t *testing.T) {
	root, callLog := setup(t, rowInProg+rowInProg3, "")
	write(t, root, "docs/tasks/XR-003.md", "# XR-003: заголовок\n"+fixtureReviewLevel)
	gitT(t, root, "add", "docs/tasks/XR-003.md")
	gitT(t, root, "commit", "-qm", "docs(tasks): XR-003 файл задачи")
	stubTaskctl(t, callLog, `case "$*" in
  *XR-003*check*) echo "XR-003: в docs/tasks/XR-003.md нет раздела «Сценарий проверки», без него в Check нельзя" >&2; exit 1;;
  *--dry-run*) exit 0;;
esac
`)
	branchFor(t, root, "XR-001", "xr-001-fix", "a.txt")
	if _, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true", Train: true}); err != nil {
		t.Fatal(err)
	}
	// Правка XR-003 приезжает на main мимо shipctl, как приезжает срочная
	// починка: ворот слияния её не видел, а состав поезда её берёт.
	gitT(t, root, "checkout", "-q", "main")
	write(t, root, "b.txt", "XR-003\n")
	gitT(t, root, "add", "b.txt")
	gitT(t, root, "commit", "-qm", "fix: XR-003 правка")
	tagBefore := gitT(t, root, "rev-parse", deployTag)

	_, err := cmdShip(root, ShipParams{Deploy: "touch shipped.marker"})
	if err == nil || !strings.Contains(err.Error(), "XR-003") ||
		!strings.Contains(err.Error(), "ворота перевода") {
		t.Fatalf("состав с непроходной строкой должен отбиваться до выката: %v", err)
	}
	if _, e := os.Stat(filepath.Join(root, "shipped.marker")); e == nil {
		t.Fatal("выкат запустился на непроходном составе")
	}
	if now := gitT(t, root, "rev-parse", deployTag); now != tagBefore {
		t.Fatalf("точка выката сдвинулась при отказе: было %s, стало %s", tagBefore, now)
	}
	if n := realMoves(t, callLog, "XR-001"); n != 0 {
		t.Fatalf("строки состава переведены при отказе, настоящих move по XR-001: %d", n)
	}
}

// TestTrainRepeatFinishesRefusedMove: перевод отбит уже после выката (ворота
// прошли, а move отказал на своей стороне). Такая строка стоит на проде без
// Check, и ship обязан довести её перевод повтором: код всего состава уже
// выкачен, поезд по коммитам пуст, и «поезд пуст» тут не ответ. Перевод
// остальных строк отказ по одной не роняет.
func TestTrainRepeatFinishesRefusedMove(t *testing.T) {
	root, callLog := setup(t, rowInProg+rowInProg3, "")
	taskWithScenario(t, root, "XR-003")
	gate := filepath.Join(t.TempDir(), "refuse")
	write(t, filepath.Dir(gate), "refuse", "держим отказ\n")
	stubTaskctl(t, callLog, `case "$*" in
  *--dry-run*) exit 0;;
  *"move XR-003 check"*)
    if [ -f "`+gate+`" ]; then echo "XR-003: перевод в Check отбит воротами" >&2; exit 1; fi;;
esac
`)
	branchFor(t, root, "XR-001", "xr-001-fix", "a.txt")
	if _, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true", Train: true}); err != nil {
		t.Fatal(err)
	}
	branchFor(t, root, "XR-003", "xr-003-fix", "b.txt")
	if _, err := cmdMerge(root, MergeParams{ID: "XR-003", Test: "true", Train: true}); err != nil {
		t.Fatal(err)
	}

	msg, err := cmdShip(root, ShipParams{Deploy: "touch shipped.marker"})
	if err != nil {
		t.Fatalf("отказ перевода по одной строке не должен ронять весь ship: %v", err)
	}
	if _, e := os.Stat(filepath.Join(root, "shipped.marker")); e != nil {
		t.Fatal("выкат не отработал")
	}
	if !strings.Contains(msg, "в In progress осталось: XR-003") {
		t.Fatalf("оставшаяся в In progress строка не названа: %q", msg)
	}
	if n := realMoves(t, callLog, "XR-001"); n != 1 {
		t.Fatalf("перевод XR-001 не доведён, настоящих move: %d", n)
	}
	doc, _ := os.ReadFile(filepath.Join(root, "docs", "tasks", "XR-003.md"))
	if !strings.Contains(string(doc), "перевод в Check отбит") {
		t.Fatalf("следа выката без перевода нет в файле задачи:\n%s", doc)
	}
	if st := gitT(t, root, "status", "--porcelain", "--untracked-files=no"); st != "" {
		t.Fatalf("пометка осталась незакоммиченной:\n%s", st)
	}

	// Причину отказа починили, и повторный ship доводит перевод хвоста.
	if err := os.Remove(gate); err != nil {
		t.Fatal(err)
	}
	msg, err = cmdShip(root, ShipParams{Deploy: "touch second.marker"})
	if err != nil {
		t.Fatalf("повторный ship должен довести перевод: %v", err)
	}
	if strings.Contains(msg, "поезд пуст") {
		t.Fatalf("выкаченная строка без Check это не пустой поезд: %q", msg)
	}
	if !strings.Contains(msg, "хвост прошлого выката доведён: XR-003") {
		t.Fatalf("доводка перевода не названа: %q", msg)
	}
	if n := realMoves(t, callLog, "XR-003"); n != 2 {
		t.Fatalf("повторный перевод XR-003 не позван, настоящих move: %d", n)
	}
	if _, e := os.Stat(filepath.Join(root, "second.marker")); e == nil {
		t.Fatal("доводка перевода покатила повторный выкат")
	}
	doc, _ = os.ReadFile(filepath.Join(root, "docs", "tasks", "XR-003.md"))
	if !strings.Contains(string(doc), "перевод в Check доведён") {
		t.Fatalf("пометка не погашена после доводки:\n%s", doc)
	}
}
