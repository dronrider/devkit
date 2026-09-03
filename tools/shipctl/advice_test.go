package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestMergeRegcheckGate: слияние bug-задачи отбивается воротом regcheck, если
// в журнале запусков нет зелёного прогона за время жизни ветки; зелёный прогон
// и тип task ворот снимают. Ворот живёт только при наличии журнала .devkit/log:
// каталога .devkit мало (его кладёт обвязка выката и start задолго до прогонов),
// а без самого журнала считать нечего.
func TestMergeRegcheckGate(t *testing.T) {
	// Журнала нет: ворот не работает, слияние проходит.
	root, _ := setup(t, rowInProg, "")
	branchWithFix(t, root)
	if _, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true"}); err != nil {
		t.Fatalf("без .devkit/log ворот regcheck не должен отказывать: %v", err)
	}

	// Журнал есть, зелёного regcheck нет: bug-задача отбивается.
	root, _ = setup(t, rowInProg, "")
	if err := os.Mkdir(filepath.Join(root, ".devkit"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, root, ".devkit/log", "")
	branchWithFix(t, root)
	_, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true"})
	if err == nil || !strings.Contains(err.Error(), "нет зелёного regcheck") {
		t.Fatalf("bug-задача без regcheck должна отбиваться воротом: %v", err)
	}

	// Пометка-исключение в файле задачи гасит ворот: regcheck неприменим, когда
	// правка и тест в одном файле.
	root, _ = setup(t, rowInProg, "")
	if err := os.Mkdir(filepath.Join(root, ".devkit"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, root, ".devkit/log", "")
	write(t, root, "docs/tasks/XR-001.md", "# XR-001: починка бага\n\n## Сценарий проверки\n\nАгентский: `git log -1`.\n\n## Ход работы\n\n- Исключение: regcheck (правка и тест в одном файле)\n"+fixtureReviewLevel)
	gitT(t, root, "add", "docs/tasks/XR-001.md")
	gitT(t, root, "commit", "-qm", "docs(tasks): XR-001 пометка regcheck")
	branchWithFix(t, root)
	if _, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true"}); err != nil {
		t.Fatalf("пометка-исключение должна гасить ворот regcheck: %v", err)
	}

	// Зелёный regcheck за время жизни ветки снимает ворот.
	root, _ = setup(t, rowInProg, "")
	if err := os.Mkdir(filepath.Join(root, ".devkit"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, root, ".devkit/log",
		fmt.Sprintf("%s\tregcheck\trun\t0\n", time.Now().Format("2006-01-02T15:04:05")))
	branchWithFix(t, root)
	if _, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true"}); err != nil {
		t.Fatalf("зелёный regcheck должен снимать ворот: %v", err)
	}

	// Красный запуск ворота не снимает.
	if regcheckLogged(writeLog(t, "2026-01-01T10:00:00\tregcheck\trun\t1\n"), time.Unix(0, 0)) {
		t.Fatal("красный regcheck засчитан за прогон")
	}

	// Для задачи типа task ворота нет и при пустом журнале.
	root, _ = setup(t, rowInProg3, "")
	if err := os.Mkdir(filepath.Join(root, ".devkit"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, root, ".devkit/log", "")
	taskWithScenario(t, root, "XR-003")
	branchFor(t, root, "XR-003", "xr-003-fix", "b.txt")
	if _, err := cmdMerge(root, MergeParams{ID: "XR-003", Test: "true"}); err != nil {
		t.Fatalf("task-задача regcheck не проверяется: %v", err)
	}
}

// TestScenarioGateSkipsFenced: заголовок «Сценарий проверки», вложенный в
// ограждённый блок, это чужой вывод, а не свой раздел. Считая его своим, ворот
// молчал бы ровно там, где задача уезжает в слияние без сценария.
func TestScenarioGateSkipsFenced(t *testing.T) {
	doc := "# XR-001: заголовок\n\n## Ход работы\n\n" +
		"Вывод merge на синтетической доске:\n\n" +
		"```\n## Сценарий проверки\n\nАгентский: `shipctl status`.\n```\n"
	if err := scenarioGate("XR-001", false, doc); err == nil ||
		!strings.Contains(err.Error(), "нет раздела") {
		t.Fatalf("цитата сошла за раздел, ворот потерялся: %v", err)
	}
	// Настоящий раздел ворот снимает, даже когда цитата рядом.
	doc = "# XR-003: заголовок\n\n" +
		"```\n## Сценарий проверки\n\nчужой вывод\n```\n\n" +
		"## Сценарий проверки\n\nАгентский: `shipctl status`.\n"
	if err := scenarioGate("XR-003", false, doc); err != nil {
		t.Fatalf("ворот при настоящем разделе: %v", err)
	}
}

// TestScenarioGateHeadingLevel: уровень заголовка признаком не является, раздел
// пишут и подразделом внутри хода работы, и первым уровнем в старых файлах.
// Признаком остаётся текст заголовка вне ограждённых блоков.
func TestScenarioGateHeadingLevel(t *testing.T) {
	doc := "# XR-001: заголовок\n\n## Ход работы\n\n### Сценарий проверки\n\nАгентский: `shipctl status`.\n"
	if err := scenarioGate("XR-001", false, doc); err != nil {
		t.Fatalf("заголовок третьего уровня не признан разделом: %v", err)
	}
	doc = "# XR-003: заголовок\n\n# Сценарий проверки\n\nАгентский: `shipctl status`.\n"
	if err := scenarioGate("XR-003", false, doc); err != nil {
		t.Fatalf("заголовок первого уровня не признан разделом: %v", err)
	}
}

// TestUnterminatedFenceSpeaksUp: незакрытое ограждение (обычный обрыв при
// вставке вывода) уводит в цитату весь остаток файла вместе с настоящими
// разделами. Молча это терять нельзя: запись «Выкат» после такого блока не
// прочитается, очередь посчитается без неё, а merge допишет свою строку туда,
// откуда её потом никто не увидит.
func TestUnterminatedFenceSpeaksUp(t *testing.T) {
	doc := "# XR-001: задача\n\n## Ход работы\n\nВывод merge:\n\n```\nдоска: XR-001 в Check\n\n" +
		"## Выкат\n\n- 2026-08-01 слито: 1111111\n"
	if at := openFenceLine(doc); at != 7 {
		t.Fatalf("незакрытое ограждение не найдено: строка %d", at)
	}
	if at := openFenceLine("# XR-001\n\n```\nвывод\n```\n\n## Выкат\n"); at != 0 {
		t.Fatalf("закрытый блок принят за оборванный: строка %d", at)
	}

	root, _ := setup(t, rowInProg, "")
	write(t, root, "docs/tasks/XR-001.md", doc)
	gitT(t, root, "add", ".")
	gitT(t, root, "commit", "-qm", "docs(tasks): XR-001 оборванный вывод")
	branchWithFix(t, root)
	_, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true"})
	if err == nil || !strings.Contains(err.Error(), "не закрыт ограждённый блок") {
		t.Fatalf("слияние по оборванному файлу задачи прошло молча: %v", err)
	}
	if !strings.Contains(err.Error(), "строка 7") {
		t.Fatalf("отказ не называет строку ограждения: %v", err)
	}
	st, err := cmdStatus(root)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(st, "docs/tasks/XR-001.md") || !strings.Contains(st, "не закрыт ограждённый блок") {
		t.Fatalf("status молчит про оборванный файл задачи:\n%s", st)
	}
}

// TestUnterminatedFenceInWorktree: обрыв заводится там, где файл задачи и
// пишется, в дереве задачи. Основной чекаут стоит на main и этих правок не
// видит, поэтому и merge, и status обязаны смотреть в дерево ветки: читая
// файл из main, merge пропустил бы оборванный хвост и дописал бы в него
// запись, а status молчал бы ровно в типовом случае In progress.
func TestUnterminatedFenceInWorktree(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	wt := startTask(t, root, "XR-001", "code.txt")
	write(t, wt, "docs/tasks/XR-001.md", "# XR-001: починка бага\n\n## Ход работы\n\nВывод merge:\n\n"+
		"```\nдоска: XR-001 в Check\n\n## Выкат\n\n- 2026-08-01 слито: 1111111\n")
	gitT(t, wt, "add", ".")
	gitT(t, wt, "commit", "-qm", "docs(tasks): XR-001 оборванный вывод")

	head := gitT(t, root, "rev-parse", "main")
	_, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true"})
	if err == nil || !strings.Contains(err.Error(), "не закрыт ограждённый блок") {
		t.Fatalf("merge читает файл задачи в основном чекауте, а не в дереве ветки: %v", err)
	}
	if now := gitT(t, root, "rev-parse", "main"); now != head {
		t.Fatal("отказ по оборванному файлу случился после слияния, а не до него")
	}
	if _, err := os.Stat(wt); err != nil {
		t.Fatal("отказ не должен трогать дерево задачи")
	}

	st, err := cmdStatus(root)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(st, "не закрыт ограждённый блок") || !strings.Contains(st, wt) {
		t.Fatalf("status не смотрит в дерево задачи:\n%s", st)
	}
}

func writeLog(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "log")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

const boardTable7 = "| ID | Задача | Тип | P | R | Цена | Ссылка |\n|--------|--------|-----|---|---|------|--------|\n"

// setBoard7 подменяет доску семиколоночной (с «Ценой»): XR-001 крупная, XR-003
// мелкая, обе в In progress.
func setBoard7(t *testing.T, root string) {
	t.Helper()
	board := "# Тест: задачи (префикс XR)\n\n## In progress\n\n" + boardTable7 +
		"| XR-001 | Крупная правка | bug | P1 | 55 (50+0+0+5+0) | L | [tasks/XR-001.md](tasks/XR-001.md) |\n" +
		"| XR-003 | Вторая мелочь | task | P3 | 8 (0+3+0+5+0) | S | - |\n" +
		"\n## Check\n\nНет.\n\n## Backlog\n\nНет.\n\n## Blocked\n\nНет.\n"
	write(t, root, "docs/TASKS.md", board)
	gitT(t, root, "add", ".")
	gitT(t, root, "commit", "-qm", "board: цена")
}

// TestTrainCriteriaWarnings: мягкие критерии поезда проговариваются в отчёте
// merge --train: цена не S/M, пересечение файлов с задачами поезда, перебор
// размера. Слияние при этом проходит.
func TestTrainCriteriaWarnings(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	setBoard7(t, root)
	taskWithScenario(t, root, "XR-003")

	branchFor(t, root, "XR-001", "xr-001-fix", "a.txt")
	msg, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true", Train: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "цена XR-001 это L") {
		t.Fatalf("нет предупреждения про цену: %q", msg)
	}

	// Вторая задача трогает тот же файл, что уже слитая в поезд XR-001.
	branchFor(t, root, "XR-003", "xr-003-fix", "a.txt")
	msg, err = cmdMerge(root, MergeParams{ID: "XR-003", Test: "true", Train: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "файлы задач поезда") || !strings.Contains(msg, "XR-001: a.txt") {
		t.Fatalf("нет предупреждения про пересечение файлов: %q", msg)
	}
	// Слово ищется вместе с ID: голое «цена» ловится и в «Сценарий».
	if strings.Contains(msg, "цена XR-003") {
		t.Fatalf("цена S не должна давать предупреждения: %q", msg)
	}

	// Пять задач в поезде: предупреждение о размере (юнит, без пяти слияний).
	b, err := loadBoard(root)
	if err != nil {
		t.Fatal(err)
	}
	ws := trainWarnings(root, "main", "HEAD", b, "XR-003", []string{"A-1", "A-2", "A-3", "A-4", "A-5"})
	joined := strings.Join(ws, "\n")
	if !strings.Contains(joined, "больше 3-5 не копят") {
		t.Fatalf("нет предупреждения о размере поезда: %v", ws)
	}
}

// TestNextStep: подсказка следующего шага выбирает ветку по состоянию
// конвейера, и порядок веток это порядок срочности. Юнит на голой логике,
// без репозитория: репозиторий проверяется отдельно, в TestStatusNextStep.
func TestNextStep(t *testing.T) {
	cases := []struct {
		name string
		st   pipelineState
		want string
	}{
		{"пусто", pipelineState{}, "взять задачу с доски"},
		{"работа при autonomous", pipelineState{inProgress: []string{"XR-001"}, autonomous: true},
			"слить самому (shipctl merge <ID>)"},
		{"работа без autonomous", pipelineState{inProgress: []string{"XR-001"}},
			"слияние за пользователем"},
		{"поезд важнее работы", pipelineState{inProgress: []string{"XR-001"}, train: []string{"XR-002"}},
			"выкатить поезд (shipctl ship)"},
		{"Check важнее поезда", pipelineState{train: []string{"XR-002"}, check: []string{"XR-003"}},
			"прогнать сценарий проверки XR-003"},
		{"сломанный прод важнее всего",
			pipelineState{inProgress: []string{"XR-001"}, train: []string{"XR-002"}, check: []string{"XR-003"}, failed: []string{"XR-004"}},
			"чинить прод по XR-004"},
	}
	for _, c := range cases {
		got := nextStep(c.st)
		if !strings.HasPrefix(got, "следующий шаг") {
			t.Errorf("%s: подсказка без общего зачина: %q", c.name, got)
		}
		if !strings.Contains(got, c.want) {
			t.Errorf("%s: жду %q, получил %q", c.name, c.want, got)
		}
	}

	// Ветка autonomous называется вместе со значением флага: без него
	// подсказка не отличает «сливаю сам» от «жду команды».
	on := nextStep(pipelineState{inProgress: []string{"XR-001"}, autonomous: true})
	off := nextStep(pipelineState{inProgress: []string{"XR-001"}})
	if !strings.Contains(on, "autonomous = true") || !strings.Contains(off, "autonomous = false") {
		t.Fatalf("развилка не называет флаг:\n%s\n%s", on, off)
	}
	if !strings.Contains(on, deployConfigPath) || !strings.Contains(off, deployConfigPath) {
		t.Fatalf("развилка не называет файл флага:\n%s\n%s", on, off)
	}
}

// TestStatusNextStep: status печатает следующий шаг последней строкой и берёт
// флаг автономии из живого .devkit/deploy.local, а не из головы. Именно этот
// вывод читают перед решением «сливать или ждать» (DK-205).
func TestStatusNextStep(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	write(t, root, ".devkit/deploy.local", "deploy = echo катим\nautonomous = true\n")
	msg, err := cmdStatus(root)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(msg, "\n")
	last := lines[len(lines)-1]
	if !strings.HasPrefix(last, "следующий шаг") {
		t.Fatalf("последняя строка status не про следующий шаг:\n%s", msg)
	}
	if !strings.Contains(last, "shipctl merge <ID>") || !strings.Contains(last, "autonomous = true") {
		t.Fatalf("при autonomous = true не назван merge своими силами: %q", last)
	}
	if !strings.Contains(last, "XR-001") {
		t.Fatalf("подсказка не называет задачу в работе: %q", last)
	}

	// Снятый флаг разворачивает развилку на пользователя.
	write(t, root, ".devkit/deploy.local", "deploy = echo катим\nautonomous = false\n")
	msg, err = cmdStatus(root)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "слияние за пользователем") || !strings.Contains(msg, "autonomous = false") {
		t.Fatalf("при autonomous = false развилка не за пользователем:\n%s", msg)
	}
}

// TestMergeNextStep: после слияния и выката отчёт называет один следующий
// шаг по виду приёмки, а не обе ветки сразу. XR-001 без суффикса вида это
// агентская задача, и совет просит прогнать сценарий и закрыть, не называя
// пользовательскую ветку. На старом коде (обе ветки сразу) совет содержал
// слово «пользовательский», и проверка его отсутствия падает.
func TestMergeNextStep(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	branchWithFix(t, root)
	msg, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true"})
	if err != nil {
		t.Fatal(err)
	}
	last := msg[strings.LastIndex(msg, "\n")+1:]
	if !strings.HasPrefix(last, "следующий шаг: прогнать сценарий проверки XR-001") {
		t.Fatalf("после merge не назван сценарий проверки: %q", last)
	}
	if !strings.Contains(last, "закрыть задачу") {
		t.Fatalf("после merge агентской задачи не названо закрытие: %q", last)
	}
	if strings.Contains(last, "пользовательск") {
		t.Fatalf("агентский совет не должен называть пользовательскую ветку: %q", last)
	}
}

// TestNextAfterMerge: совет после выката читает вид из строки доски и печатает
// один шаг на вид. Агентский прогоняет сценарий и закрывает, смешанный
// прогоняет агентскую часть, пользовательский ждёт человека.
func TestNextAfterMerge(t *testing.T) {
	cases := []struct {
		name  string
		title string
		want  string
		bad   string
	}{
		{"агентский", "Починка бага", "прогнать сценарий проверки", "пользовательск"},
		{"смешанный", "Правка [приёмка: mixed]", "прогнать агентскую часть", ""},
		{"пользовательский", "Правка [приёмка: user]", "ждёт пользователя", "закрыть задачу"},
	}
	for _, c := range cases {
		b := &board{sects: map[string][]row{"check": {{ID: "XR-001", Title: c.title}}}}
		got := nextAfterMerge(b, []string{"XR-001"})
		if !strings.HasPrefix(got, "следующий шаг") {
			t.Errorf("%s: совет без общего зачина: %q", c.name, got)
		}
		if !strings.Contains(got, c.want) {
			t.Errorf("%s: жду %q в совете, получил %q", c.name, c.want, got)
		}
		if c.bad != "" && strings.Contains(got, c.bad) {
			t.Errorf("%s: совет не должен содержать %q: %q", c.name, c.bad, got)
		}
	}
}

// TestNextAfterMergeTrain: поезд из задач разных видов группируется по виду,
// каждый вид звучит своей строкой в порядке agent/mixed/user, а одноранговые
// ID склеиваются через запятую. Контракт группировки описан в докстринге
// nextAfterMerge и README, но без этого утверждения регрессия ловится только
// глазами: TestNextAfterMerge гоняет каждый вид по одной задаче.
func TestNextAfterMergeTrain(t *testing.T) {
	b := &board{sects: map[string][]row{"check": {
		{ID: "XR-1", Title: "Агентская раз"},
		{ID: "XR-2", Title: "Агентская два"},
		{ID: "XR-3", Title: "Смешанная [приёмка: mixed]"},
		{ID: "XR-4", Title: "Пользовательская [приёмка: user]"},
	}}}
	got := nextAfterMerge(b, []string{"XR-1", "XR-2", "XR-3", "XR-4"})
	lines := strings.Split(got, "\n")
	if len(lines) != 3 {
		t.Fatalf("жду 3 строки совета (по виду), получил %d: %q", len(lines), got)
	}
	if !strings.Contains(lines[0], "XR-1, XR-2") || !strings.Contains(lines[0], "прогнать сценарий проверки") {
		t.Errorf("первая строка agent склеивает агентские ID: %q", lines[0])
	}
	if !strings.Contains(lines[1], "XR-3") || !strings.Contains(lines[1], "агентскую часть") {
		t.Errorf("вторая строка mixed: %q", lines[1])
	}
	if !strings.Contains(lines[2], "XR-4") || !strings.Contains(lines[2], "ждёт пользователя") {
		t.Errorf("третья строка user: %q", lines[2])
	}
}
