package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dronrider/devkit/internal/taskform"
)

// armStand готовит стенд взвода: временный репозиторий с доской фикстуры, где
// признак «слита» считается по настоящему git, и ворота ёмкости с известным
// потолком пачки. Живой `agentctl budget` тут не спрашивается: потолок зависел
// бы от остатка подписки машины, на которой идёт прогон.
func armStand(t *testing.T, limit int) string {
	t.Helper()
	root := setup(t)
	if err := os.MkdirAll(filepath.Join(root, ".devkit"), 0o755); err != nil {
		t.Fatal(err)
	}
	wakeGit(t, root, "init", "-q", "-b", "main")
	wakeGit(t, root, "add", ".")
	wakeGit(t, root, "commit", "-qm", "seed")
	old := batchCeiling
	batchCeiling = func(string) (int, string) { return limit, "стенд" }
	t.Cleanup(func() { batchCeiling = old })
	return root
}

// mergeWork кладёт в main коммит кода задачи: по нему признак «слита» снимает
// ребро. Коммит одной доски или файла задачи работой не считается.
func mergeWork(t *testing.T, root, id string) {
	t.Helper()
	file := filepath.Join(root, strings.ToLower(id)+".go")
	if err := os.WriteFile(file, []byte("package x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	wakeGit(t, root, "add", filepath.Base(file))
	wakeGit(t, root, "commit", "-qm", "feat(x): "+id+" правка")
}

// refreshRehearse переписывает отметку обкатки под нынешний HEAD: ворота
// `move check` сверяют её с коммитом, а стенд взвода двигает main своими
// слияниями, и отметка фикстуры к этому моменту протухает.
func refreshRehearse(t *testing.T, root, id string) {
	t.Helper()
	out, err := exec.Command("git", "-C", root, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	sha := strings.TrimSpace(string(out))[:12]
	doc := "# " + id + "\n" + fixtureScenario
	body := doc + "\n## Проверка\n\n- Обкатка: 2026-01-01 10:00, свежее дерево " + sha +
		", сценарий " + taskform.ScenarioPrint(doc) + ", шагов 1, все зелёные.\n- прогон пройден, вывод вложен.\n"
	if err := os.WriteFile(taskFilePath(root, id), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func armTitle(t *testing.T, root, id string) string {
	t.Helper()
	b, err := LoadBoard(boardPath(root))
	if err != nil {
		t.Fatal(err)
	}
	row := b.find(id)
	if row == nil {
		t.Fatalf("%s нет на доске", id)
	}
	return row.Title
}

// DoD: взвод ставится суффиксом на своё место (за «[после ...]» и до
// «[приёмка: ...]»), снимается флагом --off, и второй взвод подряд отбивается.
func TestArmPutsSuffixInItsPlace(t *testing.T) {
	root := armStand(t, 3)
	if _, err := cmdDepAdd(root, DepParams{ID: "XR-003", DepID: "XR-001"}); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdSet(root, SetParams{ID: "XR-003", Accept: acceptUser}); err != nil {
		t.Fatal(err)
	}
	msg, err := cmdArm(root, "XR-003", false, CommitOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "взвод стоит, ждёт") {
		t.Fatalf("arm не назвал, чего строка ждёт: %s", msg)
	}
	want := "[после XR-001] [взвод] [приёмка: user]"
	if got := armTitle(t, root, "XR-003"); !strings.Contains(got, want) {
		t.Fatalf("суффикс встал не на своё место: %q, жду %q", got, want)
	}
	if _, err := cmdArm(root, "XR-003", false, CommitOpts{}); err == nil {
		t.Fatal("второй взвод подряд должен падать")
	}
	if _, err := cmdArm(root, "XR-003", true, CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	if got := armTitle(t, root, "XR-003"); strings.Contains(got, "[взвод]") {
		t.Fatalf("--off не снял взвод: %q", got)
	}
	if _, err := cmdArm(root, "XR-003", true, CommitOpts{}); err == nil {
		t.Fatal("снятие взвода с невзведённой строки должно падать")
	}
}

// DoD решения 1: взвод не получают строка вне Backlog, строка с открытой
// человеческой развилкой, грумминговый вердикт (неопределённость 4-5 либо цена
// XL) и строка из состава незакрытой цели.
func TestArmRefusesWhatExecutorCannotTake(t *testing.T) {
	root := armStand(t, 3)
	if _, err := cmdArm(root, "XR-005", false, CommitOpts{}); err == nil ||
		!strings.Contains(err.Error(), "взводится только строка Backlog") {
		t.Fatalf("строка In progress взведена: %v", err)
	}
	if _, err := cmdSet(root, SetParams{ID: "XR-001", Rank: "25+2+4+0+2"}); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdArm(root, "XR-001", false, CommitOpts{}); err == nil ||
		!strings.Contains(err.Error(), "неопределённостью 4") {
		t.Fatalf("строка с неопределённостью 4 взведена: %v", err)
	}
	if _, err := cmdSet(root, SetParams{ID: "XR-002", Cost: "XL"}); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdArm(root, "XR-002", false, CommitOpts{}); err == nil ||
		!strings.Contains(err.Error(), "ценой XL") {
		t.Fatalf("строка ценой XL взведена: %v", err)
	}
	fork := "# XR-003\n\n## Развилки\n\n- «формат»: json или таблица?\n  - решает: человек\n"
	if err := os.WriteFile(taskFilePath(root, "XR-003"), []byte(fork), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdArm(root, "XR-003", false, CommitOpts{}); err == nil ||
		!strings.Contains(err.Error(), "открытых развилок 1") {
		t.Fatalf("строка с человеческой развилкой взведена: %v", err)
	}
	// Строка незакрытой цели: цель стоит на доске, и её состав поднимает
	// цикл цели, а не обход.
	goalFile := filepath.Join(root, "docs", "tasks", "XR-002.md")
	if err := os.WriteFile(goalFile, []byte("# XR-002: Цель\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(taskFilePath(root, "XR-004"),
		[]byte("# XR-004\n\nЦель: [tasks/XR-002.md](tasks/XR-002.md)\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdArm(root, "XR-004", false, CommitOpts{}); err == nil ||
		!strings.Contains(err.Error(), "из состава незакрытой цели XR-002") {
		t.Fatalf("строка цели взведена: %v", err)
	}
}

// DoD: взведённая строка стартует сама после слияния кода всех предпосылок, а
// строка уровня N ждёт все строки уровня N-1. Цепочка из трёх строк на двух
// уровнях идёт на временном репозитории, уровни ставятся рёбрами и взводом.
func TestArmedChainStartsByMerges(t *testing.T) {
	root := armStand(t, 3)
	calls := recordRaise(t)
	// Уровень 1 это XR-001 и XR-002, уровень 2 это XR-003, за ним XR-004.
	for _, dep := range []string{"XR-001", "XR-002"} {
		if _, err := cmdDepAdd(root, DepParams{ID: "XR-003", DepID: dep}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := cmdDepAdd(root, DepParams{ID: "XR-004", DepID: "XR-003"}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"XR-003", "XR-004"} {
		if _, err := cmdArm(root, id, false, CommitOpts{}); err != nil {
			t.Fatal(err)
		}
	}
	out, failed, err := cmdWake(root, nil, wakeOpts{})
	if err != nil || failed {
		t.Fatalf("обход упал: %v %v\n%s", err, failed, out)
	}
	if len(*calls) != 0 {
		t.Fatalf("строка поднята до слияния предпосылок: %+v\n%s", *calls, out)
	}
	if !strings.Contains(out, "ждут событием 2, событие случилось у 0") {
		t.Fatalf("обход не назвал взведённых: %s", out)
	}
	mergeWork(t, root, "XR-001")
	if _, _, err := cmdWake(root, nil, wakeOpts{}); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 0 {
		t.Fatalf("XR-003 стартовала до слияния всего уровня: %+v", *calls)
	}
	mergeWork(t, root, "XR-002")
	out, failed, err = cmdWake(root, nil, wakeOpts{})
	if err != nil || failed {
		t.Fatalf("обход упал: %v %v\n%s", err, failed, out)
	}
	if len(*calls) != 1 || (*calls)[0] != (raiseCall{ID: "XR-003", Hidden: true}) {
		t.Fatalf("жду скрытый подъём XR-003, а было %+v\n%s", *calls, out)
	}
	if got := sectOf(t, root, "XR-003"); got != SectInProgress {
		t.Fatalf("XR-003 в %s после подъёма", got)
	}
	if got := armTitle(t, root, "XR-003"); strings.Contains(got, "[взвод]") {
		t.Fatalf("старт не снял взвод: %q", got)
	}
	log, _ := os.ReadFile(filepath.Join(root, ".devkit", "log"))
	if !strings.Contains(string(log), "\ttaskctl\twake XR-003 взвод\t0\n") {
		t.Fatalf("подъёма по взводу нет в .devkit/log:\n%s", log)
	}
	// Второй уровень: XR-004 ждала XR-003 и стартует по её слиянию.
	mergeWork(t, root, "XR-003")
	if _, _, err := cmdWake(root, nil, wakeOpts{}); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 2 || (*calls)[1].ID != "XR-004" {
		t.Fatalf("второй уровень не стартовал: %+v", *calls)
	}
}

// DoD: откат и провал предпосылки держат взведённую строку до нового слияния, а
// возврат предпосылки из Check с замечаниями её не держит (решение 3).
func TestArmedRowWaitsRevertAndFail(t *testing.T) {
	root := armStand(t, 3)
	calls := recordRaise(t)
	if _, err := cmdDepAdd(root, DepParams{ID: "XR-003", DepID: "XR-005"}); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdArm(root, "XR-003", false, CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	mergeWork(t, root, "XR-005")
	// Провал предпосылки возвращает ребро: строка ждёт нового слияния.
	if _, err := cmdFail(root, FailParams{ID: "XR-005", Reason: "прод отдаёт 500"}); err != nil {
		t.Fatal(err)
	}
	out, _, err := cmdWake(root, nil, wakeOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 0 {
		t.Fatalf("провал предпосылки не удержал взведённую: %+v\n%s", *calls, out)
	}
	// Откат предпосылки держит её так же.
	if _, err := cmdFail(root, FailParams{ID: "XR-005", Clear: true}); err != nil {
		t.Fatal(err)
	}
	wakeGit(t, root, "revert", "--no-edit", "HEAD")
	if _, _, err := cmdWake(root, nil, wakeOpts{}); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 0 {
		t.Fatalf("откат предпосылки не удержал взведённую: %+v", *calls)
	}
	// Новое слияние починки снимает ребро, и взвод стреляет: согласие человека
	// дано строке, и возврат предпосылки его не отменяет.
	mergeWork(t, root, "XR-005")
	if _, _, err := cmdWake(root, nil, wakeOpts{}); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 1 || (*calls)[0].ID != "XR-003" {
		t.Fatalf("после починки взвод не выстрелил: %+v", *calls)
	}
}

// DoD решения 3: возврат предпосылки из Check с замечаниями взведённую строку
// не держит. Код предпосылки остаётся в main, ребро остаётся снятым.
func TestArmedRowStartsWhilePrereqReturnedFromCheck(t *testing.T) {
	root := armStand(t, 3)
	calls := recordRaise(t)
	if _, err := cmdDepAdd(root, DepParams{ID: "XR-003", DepID: "XR-005"}); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdArm(root, "XR-003", false, CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	mergeWork(t, root, "XR-005")
	refreshRehearse(t, root, "XR-005")
	if _, err := cmdMove(root, "XR-005", SectCheck, "", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	// Проверяющий вернул предпосылку с замечаниями: прод жив, основание цело.
	if _, err := cmdMove(root, "XR-005", SectInProgress, "", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := cmdWake(root, nil, wakeOpts{}); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 1 || (*calls)[0].ID != "XR-003" {
		t.Fatalf("возврат предпосылки из Check удержал взведённую: %+v", *calls)
	}
}

// DoD: взведённая строка отличается в `taskctl list` от заглохшей, то есть от
// освобождённой строки без взвода, которую поднять некому.
func TestListTellsArmedFromIdle(t *testing.T) {
	root := armStand(t, 3)
	for _, id := range []string{"XR-003", "XR-004"} {
		if _, err := cmdDepAdd(root, DepParams{ID: id, DepID: "XR-005"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := cmdArm(root, "XR-003", false, CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	out, err := cmdList(root, SectBacklog)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "взвод: ждёт, XR-005 не слита") {
		t.Fatalf("list не назвал, чего ждёт взведённая:\n%s", out)
	}
	mergeWork(t, root, "XR-005")
	out, err = cmdList(root, SectBacklog)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "взвод: готова") {
		t.Fatalf("list не назвал готовую взведённую:\n%s", out)
	}
	if !strings.Contains(out, "свободна, старт рукой") {
		t.Fatalf("list не отличил заглохшую строку от взведённой:\n%s", out)
	}
	show, err := cmdShow(root, "XR-003")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(show, "взвод: готова") {
		t.Fatalf("show не назвал состояние взвода:\n%s", show)
	}
}

// DoD решения 5: ворота ёмкости оставляют готовую строку стоять, причина с
// временем ложится файлом .devkit/arm/<ID> и печатается в list, а зов человеку
// уходит один раз на каждую новую причину.
func TestArmCapacityRefusalCallsOnce(t *testing.T) {
	root := armStand(t, 0)
	callLog := writeNotifyStub(t, root, 0)
	calls := recordRaise(t)
	if _, err := cmdArm(root, "XR-003", false, CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	out, _, err := cmdWake(root, nil, wakeOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 0 {
		t.Fatalf("ворота ёмкости пропустили подъём: %+v\n%s", *calls, out)
	}
	if !strings.Contains(out, "нет квоты на пачку") {
		t.Fatalf("обход не назвал причину отказа:\n%s", out)
	}
	ref, ok := readArmRefusal(root, "XR-003")
	if !ok || ref.Why != "нет квоты на пачку" {
		t.Fatalf("файла отказа нет или он не о том: %+v %v", ref, ok)
	}
	list, err := cmdList(root, SectBacklog)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(list, "взвод: готова, отказ") || !strings.Contains(list, "нет квоты на пачку") {
		t.Fatalf("list не назвал отказ ворот:\n%s", list)
	}
	// Вторым проходом причина та же: зов не повторяется, и отчёт молчит.
	out, _, err = cmdWake(root, nil, wakeOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "нет квоты на пачку") {
		t.Fatalf("повтор той же причины заговорил в отчёте:\n%s", out)
	}
	said := notifyCalls(t, callLog)
	if len(said) != 1 {
		t.Fatalf("жду один зов на причину, а было %d:\n%v", len(said), said)
	}
	// Новая причина зовёт снова.
	t.Setenv(slotTreesEnv, "0")
	batchCeiling = func(string) (int, string) { return 3, "стенд" }
	out, _, err = cmdWake(root, nil, wakeOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "потолок деревьев") {
		t.Fatalf("новая причина не названа:\n%s", out)
	}
	if said := notifyCalls(t, callLog); len(said) != 2 {
		t.Fatalf("новая причина не позвала человека:\n%v", said)
	}
}

// notifyCalls читает журнал стаба уведомителя строками.
func notifyCalls(t *testing.T, callLog string) []string {
	t.Helper()
	data, err := os.ReadFile(callLog)
	if err != nil {
		return nil
	}
	var out []string
	for _, ln := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if strings.TrimSpace(ln) != "" {
			out = append(out, ln)
		}
	}
	return out
}

// DoD: выход из Backlog снимает взвод вместе с файлом отказа, а вернувшаяся в
// Backlog строка по старому согласию сама не стартует.
func TestMoveOutOfBacklogDisarms(t *testing.T) {
	root := armStand(t, 0)
	if _, err := cmdArm(root, "XR-003", false, CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := cmdWake(root, nil, wakeOpts{}); err != nil {
		t.Fatal(err)
	}
	if _, ok := readArmRefusal(root, "XR-003"); !ok {
		t.Fatal("файла отказа нет, стенд не о том")
	}
	msg, err := cmdMove(root, "XR-003", SectInProgress, "", CommitOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "взвод снят") {
		t.Fatalf("move не сказал о снятии взвода: %s", msg)
	}
	if got := armTitle(t, root, "XR-003"); strings.Contains(got, "[взвод]") {
		t.Fatalf("взвод пережил старт: %q", got)
	}
	if _, ok := readArmRefusal(root, "XR-003"); ok {
		t.Fatal("файл отказа пережил старт строки")
	}
}

// DoD: lint находит взвод вне Backlog и на строке незакрытой цели, а хвост
// обхода такую строку пропускает.
func TestLintFindsArmWhereItDoesNotBelong(t *testing.T) {
	root := armStand(t, 3)
	calls := recordRaise(t)
	replaceInBoard(t, root, "| XR-005 | Задача в работе", "| XR-005 | Задача в работе [взвод]")
	if err := os.WriteFile(filepath.Join(root, "docs", "tasks", "XR-002.md"),
		[]byte("# XR-002: Цель\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(taskFilePath(root, "XR-004"),
		[]byte("# XR-004\n\nЦель: [tasks/XR-002.md](tasks/XR-002.md)\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	replaceInBoard(t, root, "| XR-004 | Хвост", "| XR-004 | Хвост [взвод]")
	b, err := LoadBoard(boardPath(root))
	if err != nil {
		t.Fatal(err)
	}
	finds := lintArmed(root, b, "docs/TASKS.md")
	if len(finds) != 2 {
		t.Fatalf("жду две находки, а было %d:\n%s", len(finds), strings.Join(finds, "\n"))
	}
	if !strings.Contains(finds[0], "взвод на строке в In progress") {
		t.Fatalf("находки про взвод вне Backlog нет: %s", finds[0])
	}
	if !strings.Contains(finds[1], "незакрытой цели XR-002") {
		t.Fatalf("находки про строку цели нет: %s", finds[1])
	}
	out, _, err := cmdWake(root, nil, wakeOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 0 {
		t.Fatalf("обход поднял строку, которой взвод не положен: %+v\n%s", *calls, out)
	}
}

// TestArmGatesSkipDeadWindows: регрессия DK-967. Ворота ёмкости считали
// работой всякое окно tmux с именем task-<ID>, а такие окна остаются от
// брошенных заходов и живут на машине неделями. Взведённая строка не
// стартовала: живых заходов было два, а счётчик показывал девять, и подъёму
// отказывали словами «потолок пачки 3 исчерпан, живых работ 9». Работу даёт
// живой заход: клиент на переднем плане окна и взятая строка на доске.
func TestArmGatesSkipDeadWindows(t *testing.T) {
	root := armStand(t, 3)
	fakeTmux(t, strings.Join([]string{
		"task-XR-005\\t1\\t100", // живой заход по строке из In progress
		"task-XR-001\\t1\\t200", // окно от брошенного захода, внутри оболочка
		"task-XR-002\\t1\\t300", // клиент жив, а строка лежит в Backlog
		"task-XR-004\\t1\\t400", // то же самое, второе такое окно
	}, "\\n"), strings.Join([]string{
		"task-XR-005|claude|0",
		"task-XR-001|zsh|0",
		"task-XR-002|claude|0",
		"task-XR-004|claude|0",
	}, "\\n"))
	b, err := LoadBoard(boardPath(root))
	if err != nil {
		t.Fatal(err)
	}
	g := newArmGates(root, b)
	if why := g.pass("XR-003"); why != "" {
		t.Fatalf("ворота не пустили взведённую строку: %s", why)
	}
	if why := g.pass("XR-005"); why != "дерево занято живой работой" {
		t.Fatalf("живая работа перестала занимать дерево: %q", why)
	}
}
