package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dronrider/devkit/internal/loadfail"
)

// Вторая строка In progress с рангом ниже первой: очередь обязана лить их по
// ранговому порядку, а не по тому, как строки легли в файл доски.
const rowInProgLow = "| XR-007 | Мелкая правка | task | P2 | 20 (15+5+0+0+0) | [tasks/XR-007.md](tasks/XR-007.md) |\n"

// queueSetup собирает доску с двумя строками In progress и ветками под обе.
// Строка низкого ранга стоит в файле первой: порядок очереди должен держаться
// на числе, а не на порядке строк.
func queueSetup(t *testing.T) (root string) {
	t.Helper()
	root, _ = setup(t, rowInProgLow+rowInProg, "")
	devkitDir(t, root)
	write(t, root, "docs/tasks/XR-007.md",
		"# XR-007: мелкая правка\n\n## Сценарий проверки\n\nАгентский: `git log -1`, ждём коммит правки.\n"+
			fixtureRehearsal+fixtureReviewLevel+"\n- вердикт: без замечаний\n")
	gitT(t, root, "add", ".")
	gitT(t, root, "commit", "-qm", "docs(tasks): XR-007 файл задачи")
	queueBranch(t, root, "XR-001", "xr-001-fix", "code.txt")
	queueBranch(t, root, "XR-007", "xr-007-small", "small.txt")
	return root
}

// queueBranch выкладывает ветку задачи в своё дерево рядом с проектом, тем
// же путём, каким это делает shipctl start: очередь работает из основного
// чекаута на main и ветку ищет в дереве задачи.
func queueBranch(t *testing.T, root, id, branch, file string) {
	t.Helper()
	wt := filepath.Join(filepath.Dir(root), filepath.Base(root)+"-"+strings.ToLower(id))
	gitT(t, root, "worktree", "add", "-q", "-b", branch, wt, "main")
	// Убирать дерево может и само слияние, поэтому отказ уборки тут не
	// провал теста: важно не оставить дерево живым после прогона.
	t.Cleanup(func() { git(root, "worktree", "remove", "--force", wt) })
	write(t, wt, file, "new\n")
	write(t, wt, strings.TrimSuffix(file, ".txt")+"_test.go", "package main\n")
	gitT(t, wt, "add", ".")
	gitT(t, wt, "commit", "-qm", "fix: "+id+" правка")
}

// TestQueueOrderFollowsRank: состав очереди производный от доски, и голова у
// него это строка с наибольшим рангом.
func TestQueueOrderFollowsRank(t *testing.T) {
	root := queueSetup(t)
	msg, err := cmdQueue(root, QueueParams{})
	if err != nil {
		t.Fatal(err)
	}
	first := strings.Index(msg, "XR-001")
	second := strings.Index(msg, "XR-007")
	if first < 0 || second < 0 {
		t.Fatalf("в очереди обе ветки, а напечатано: %q", msg)
	}
	if first > second {
		t.Errorf("очередь идёт не по рангу: %q", msg)
	}
	if !strings.Contains(msg, "1. XR-001") {
		t.Errorf("позиция головы не напечатана: %q", msg)
	}
}

// TestQueueDrainPoursOneBranch: разлив льёт одну ветку за заход, голову
// очереди, и остальные оставляет следующему тику.
func TestQueueDrainPoursOneBranch(t *testing.T) {
	root := queueSetup(t)
	msg, err := cmdQueue(root, QueueParams{Drain: true, Test: "true"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "XR-001 слита очередью") {
		t.Fatalf("разлив обязан слить голову очереди: %q", msg)
	}
	if strings.Contains(msg, "XR-007 слита") {
		t.Errorf("за один заход льётся одна ветка: %q", msg)
	}
	log := gitT(t, root, "log", "--format=%s")
	if !strings.Contains(log, "fix: XR-001 правка") {
		t.Fatalf("коммиты XR-001 не в main:\n%s", log)
	}
	if strings.Contains(log, "fix: XR-007 правка") {
		t.Errorf("вторая ветка уехала тем же заходом:\n%s", log)
	}
}

// TestQueueDrainStopsOnBrokenProd: сломанный прод держит очередь целиком, и
// разлив молчит своим маркером, как ship --drain.
func TestQueueDrainStopsOnBrokenProd(t *testing.T) {
	root, _ := setup(t, rowInProg+rowFailed, "")
	devkitDir(t, root)
	branchWithFix(t, root)
	gitT(t, root, "checkout", "-q", "main")
	msg, err := cmdQueue(root, QueueParams{Drain: true, Test: "true"})
	if err != nil {
		t.Fatalf("сломанный прод останавливает разлив молча: %v", err)
	}
	if !strings.Contains(msg, noQueue) || !strings.Contains(msg, "провал проверки за XR-003") {
		t.Fatalf("причина остановки: %q", msg)
	}
	if strings.Contains(gitT(t, root, "log", "--format=%s"), "fix: XR-001 правка") {
		t.Error("при сломанном проде ветка уехала в main")
	}
}

// TestQueueDrainEmptyNoop: разливать нечего значит нулевой выход со своим
// маркером, по нему тик отличает пустой заход от значимого.
func TestQueueDrainEmptyNoop(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	devkitDir(t, root)
	msg, err := cmdQueue(root, QueueParams{Drain: true, Test: "true"})
	if err != nil {
		t.Fatalf("пустая очередь молчит нулём: %v", err)
	}
	if !strings.Contains(msg, noQueue) {
		t.Fatalf("маркер пустого захода: %q", msg)
	}
}

// loadRedTest это команда тестов, красная нагрузочным падением компонента
// вне диффа задачи: строки итога печатаются в том же виде, в каком их даёт
// раннер, и тест сам называет себя замером стенного времени.
const loadRedTest = `sh -c 'printf "%s\n" "dashboard (tools/dashboard) 74.1s FAIL" "FAIL dashboard" "--- FAIL: TestDashboardSmokeHiddenTabQuiet (74.12s)" "    quietsmoke_test.go:390: ` + loadfail.Mark + `" "Ran 1 of 1 components in 1m14s"; exit 1'`

// ownRedTest это та же краснота, но своя: компонент задет диффом задачи, а
// признака нагрузки в блоке нет.
const ownRedTest = `sh -c 'printf "%s\n" "code (code.txt) 0.1s FAIL" "FAIL code" "--- FAIL: TestCodeKeepsOldValue (0.01s)" "    code_test.go:3: жду old, пришло new" "Ran 1 of 1 components in 0m01s"; exit 1'`

// TestQueueRetriesOnLoadRed: ветка, отбитая нагрузочным падением вне диффа,
// встаёт в хвост очереди сама, с повтором в наклейке.
func TestQueueRetriesOnLoadRed(t *testing.T) {
	root := queueSetup(t)
	msg, err := cmdQueue(root, QueueParams{Drain: true, Test: loadRedTest})
	if err != nil {
		t.Fatalf("нагрузочная краснота это повтор, а не отказ разлива: %v", err)
	}
	if !strings.Contains(msg, "XR-001 встала в хвост очереди слияний, повтор 1 из 3") {
		t.Fatalf("отчёт повтора: %q", msg)
	}
	if !strings.Contains(msg, "нагрузочное падение") || !strings.Contains(msg, "HiddenTabQuiet") {
		t.Errorf("причина повтора обязана назвать тест: %q", msg)
	}
	st := loadQueue(root)
	if m := st.Tasks["XR-001"]; m == nil || m.Tries != 1 || m.Held {
		t.Fatalf("наклейка после первого отказа: %+v", st.Tasks["XR-001"])
	}
	// Разлив не встал: следующая по рангу ветка получила свой заход тем же
	// проходом, и её отбило то же нагрузочное падение.
	if !strings.Contains(msg, "XR-007 встала в хвост") {
		t.Errorf("проход обязан идти дальше по составу: %q", msg)
	}
}

// TestQueueLeavesOnRetryLimit: три повтора подряд это уже не нагрузка, и на
// потолке строка уходит из очереди.
func TestQueueLeavesOnRetryLimit(t *testing.T) {
	root := queueSetup(t)
	for i := 1; i <= queueRetryLimit; i++ {
		msg, err := cmdQueue(root, QueueParams{Drain: true, Test: loadRedTest})
		if err != nil {
			t.Fatalf("заход %d: %v", i, err)
		}
		if i < queueRetryLimit && !strings.Contains(msg, "повтор") {
			t.Fatalf("заход %d обязан считать повтор: %q", i, msg)
		}
		if i == queueRetryLimit && !strings.Contains(msg, "XR-001 ушла из очереди слияний") {
			t.Fatalf("на потолке строка уходит из очереди: %q", msg)
		}
	}
	st := loadQueue(root)
	if m := st.Tasks["XR-001"]; m == nil || !m.Held || m.Tries != queueRetryLimit {
		t.Fatalf("наклейка на потолке: %+v", st.Tasks["XR-001"])
	}
	// Снятая строка в составе не участвует и позиции не занимает: голова
	// очереди достаётся следующей по рангу.
	if _, err := cmdQueue(root, QueueParams{Free: "XR-007"}); err != nil {
		t.Fatal(err)
	}
	msg, err := cmdQueue(root, QueueParams{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "снята XR-001") || !strings.Contains(msg, "1. XR-007") {
		t.Fatalf("печать очереди со снятой строкой: %q", msg)
	}
}

// TestQueueDropsOnOwnRed: своя краснота повтора не получает. Строка уходит из
// очереди сразу, с записью в файл задачи на её же ветке, и проход встаёт: в
// main поехало бы то, что тесты только что назвали сломанным.
func TestQueueDropsOnOwnRed(t *testing.T) {
	root := queueSetup(t)
	msg, err := cmdQueue(root, QueueParams{Drain: true, Test: ownRedTest})
	if err != nil {
		t.Fatalf("своя краснота выводит строку из очереди, а не валит разлив: %v", err)
	}
	if !strings.Contains(msg, "XR-001 снята с очереди слияний") || !strings.Contains(msg, "краснота в диффе задачи") {
		t.Fatalf("отчёт снятия: %q", msg)
	}
	st := loadQueue(root)
	if m := st.Tasks["XR-001"]; m == nil || !m.Held || m.Tries != 0 {
		t.Fatalf("своя краснота повторов не считает: %+v", st.Tasks["XR-001"])
	}
	// Запись едет коммитом ветки: дерево задачи остаётся чистым, иначе
	// следующее слияние отбилось бы незакоммиченным.
	wt, err := taskWorktree(root, "XR-001")
	if err != nil || wt == nil {
		t.Fatalf("дерево задачи не нашлось: %v", err)
	}
	doc, err := os.ReadFile(taskFilePath(wt.Path, "XR-001"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(doc), "снята с очереди слияний") {
		t.Errorf("записи в файле задачи нет:\n%s", doc)
	}
	if st := gitT(t, wt.Path, "status", "--porcelain"); st != "" {
		t.Errorf("дерево задачи осталось грязным:\n%s", st)
	}
}

// TestQueueHoldAndFree: снятие руками просит причину, а возврат обнуляет
// повторы: препятствие разобрано, и прошлые отказы к делу не относятся.
func TestQueueHoldAndFree(t *testing.T) {
	root := queueSetup(t)
	if _, err := cmdQueue(root, QueueParams{Hold: "XR-001"}); err == nil {
		t.Error("снятие без причины должно отказывать")
	}
	if _, err := cmdQueue(root, QueueParams{Hold: "XR-001", Reason: "жду ответа смежника"}); err != nil {
		t.Fatal(err)
	}
	if m := loadQueue(root).Tasks["XR-001"]; m == nil || !m.Held {
		t.Fatalf("наклейка после снятия: %+v", m)
	}
	msg, err := cmdQueue(root, QueueParams{Drain: true, Test: "true"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(msg, "XR-001 слита") {
		t.Errorf("снятая строка уехала разливом: %q", msg)
	}
	if _, err := cmdQueue(root, QueueParams{Free: "XR-001"}); err != nil {
		t.Fatal(err)
	}
	if m := loadQueue(root).Tasks["XR-001"]; m == nil || m.Held || m.Tries != 0 {
		t.Fatalf("наклейка после возврата: %+v", m)
	}
}

// TestQueueMarkGoesAfterMerge: слитая строка наклейку не тащит. Круг
// доработки после возврата из Check это второй заход той же задачи, и
// повторы прошлого круга ему не наследуются.
func TestQueueMarkGoesAfterMerge(t *testing.T) {
	root := queueSetup(t)
	if _, err := cmdQueue(root, QueueParams{Drain: true, Test: loadRedTest}); err != nil {
		t.Fatal(err)
	}
	if m := loadQueue(root).Tasks["XR-001"]; m == nil || m.Tries != 1 {
		t.Fatalf("повтор не посчитан: %+v", m)
	}
	if _, err := cmdQueue(root, QueueParams{Drain: true, Test: "true"}); err != nil {
		t.Fatal(err)
	}
	if m := loadQueue(root).Tasks["XR-001"]; m != nil {
		t.Errorf("наклейка слитой задачи осталась: %+v", m)
	}
}

// TestStatusPrintsMergeQueue: очередь видна снаружи, позицией и повторами, а
// не только в журнале.
func TestStatusPrintsMergeQueue(t *testing.T) {
	root := queueSetup(t)
	if _, err := cmdQueue(root, QueueParams{Drain: true, Test: loadRedTest}); err != nil {
		t.Fatal(err)
	}
	msg, err := cmdStatus(root)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "очередь слияний") {
		t.Fatalf("status молчит про очередь слияний:\n%s", msg)
	}
	if !strings.Contains(msg, "повторов 1 из 3") {
		t.Errorf("число повторов не напечатано:\n%s", msg)
	}
}
