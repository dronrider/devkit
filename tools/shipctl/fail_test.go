package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Строки задач с непогашенным провалом проверки: пометку ставит taskctl fail,
// shipctl читает её из той же доски.
const (
	rowFailed     = "| XR-003 | Вторая мелочь [провал: прод отдаёт 500] | task | P3 | 8 (0+3+0+5+0) |  |\n"
	rowSelfFailed = "| XR-001 | Починка бага [провал: прод отдаёт 500] | bug | P1 | 55 (50+0+0+5+0) | [tasks/XR-001.md](tasks/XR-001.md) |\n"
)

// TestMergeRefusesWhileProdBroken: пока за чужой задачей числится провал,
// прод сломан, и очередь стоит целиком.
func TestMergeRefusesWhileProdBroken(t *testing.T) {
	root, _ := setup(t, rowInProg+rowFailed, "")
	branchWithFix(t, root)
	_, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true"})
	if err == nil || !strings.Contains(err.Error(), "провал проверки за XR-003") {
		t.Fatalf("ожидал отказ по сломанному проду, получил: %v", err)
	}
	if !strings.Contains(err.Error(), "shipctl revert XR-003") {
		t.Fatalf("в отказе нет способа починить прод: %v", err)
	}
	if log := gitT(t, root, "log", "main", "--format=%s"); strings.Contains(log, "fix: XR-001 правка") {
		t.Fatalf("отбитый merge всё же слил ветку:\n%s", log)
	}
}

// TestMergeFixesBrokenProd: форвард-фикс самой проваленной задачи проходит,
// иначе чинить прод было бы нечем, и перевод в Check гасит признак.
func TestMergeFixesBrokenProd(t *testing.T) {
	root, callLog := setup(t, rowSelfFailed, "")
	branchWithFix(t, root)
	msg, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true"})
	if err != nil {
		t.Fatalf("форвард-фикс проваленной задачи должен сливаться: %v", err)
	}
	if !strings.Contains(msg, "признак провала") {
		t.Fatalf("merge молчит про снятый признак: %q", msg)
	}
	calls, _ := os.ReadFile(callLog)
	if !strings.Contains(string(calls), "move XR-001 check") {
		t.Fatalf("taskctl move не вызван: %q", calls)
	}
}

// TestMergeTrainRefusedWhileProdBroken: поезд это накопление, а сломанный
// прод чинят выкатом, поэтому --train отбивается и для своей задачи.
func TestMergeTrainRefusedWhileProdBroken(t *testing.T) {
	root, _ := setup(t, rowSelfFailed, "")
	branchWithFix(t, root)
	_, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true", Train: true})
	if err == nil || !strings.Contains(err.Error(), "поезд при сломанном проде не копится") {
		t.Fatalf("ожидал отказ поездного слияния, получил: %v", err)
	}
}

// TestShipRefusesWhileProdBroken: провал проверяется раньше состава поезда,
// иначе отказ говорил бы про пустой поезд вместо сломанного прода.
func TestShipRefusesWhileProdBroken(t *testing.T) {
	root, _ := setup(t, rowInProg+rowFailed, "")
	_, err := cmdShip(root, ShipParams{Deploy: "true"})
	if err == nil || !strings.Contains(err.Error(), "провал проверки за XR-003") {
		t.Fatalf("ожидал отказ ship по сломанному проду, получил: %v", err)
	}
}

// TestRevertClearsFailMark: откат это второй способ починить прод, и признак
// он гасит сам. Задача при этом уже в In progress, поэтому move не зовётся.
func TestRevertClearsFailMark(t *testing.T) {
	root, callLog := setup(t, rowSelfFailed, "")
	codeCommit(t, root, "XR-001", "one.txt")
	msg, err := cmdRevert(root, RevertParams{ID: "XR-001", Test: "true"})
	if err != nil {
		t.Fatal(err)
	}
	calls, _ := os.ReadFile(callLog)
	if !strings.Contains(string(calls), "fail XR-001 --clear") {
		t.Fatalf("признак провала не снят: %q", calls)
	}
	if strings.Contains(string(calls), "move XR-001") {
		t.Fatalf("откат двигал задачу, которая и так в In progress: %q", calls)
	}
	if !strings.Contains(msg, "признак провала снят откатом") {
		t.Fatalf("откат молчит про снятый признак: %q", msg)
	}
	if log := gitT(t, root, "log", "--format=%s"); !strings.Contains(log, "признак провала снят откатом") {
		t.Fatalf("коммита доски о погашении нет:\n%s", log)
	}
}

// Та же строка задачи, но на блокере: провал и блокировка уживаются в
// заголовке, между ними задачу могли отложить до ответа хостера.
const rowBlockedFailed = "| XR-001 | Починка бага [провал: прод отдаёт 500] [блок: ждём хостера] | bug | P1 | 55 (50+0+0+5+0) | [tasks/XR-001.md](tasks/XR-001.md) |\n"

// TestRevertClearsFailMarkFromBlocked: откат чинит прод независимо от того, в
// какой секции лежит строка, поэтому и признак он гасит независимо от неё.
// Раньше задачу с блокера revert только возвращал в In progress, пометка
// оставалась, и очередь выката стояла с починенным продом.
func TestRevertClearsFailMarkFromBlocked(t *testing.T) {
	root, callLog := setup(t, "", "")
	data, _ := os.ReadFile(filepath.Join(root, "docs", "TASKS.md"))
	write(t, root, "docs/TASKS.md", strings.Replace(string(data),
		"## Blocked\n\nНет.\n", "## Blocked\n\n"+section(rowBlockedFailed), 1))
	gitT(t, root, "add", ".")
	gitT(t, root, "commit", "-qm", "docs(tasks): XR-001 на блокере")
	codeCommit(t, root, "XR-001", "one.txt")

	msg, err := cmdRevert(root, RevertParams{ID: "XR-001", Test: "true"})
	if err != nil {
		t.Fatal(err)
	}
	calls, _ := os.ReadFile(callLog)
	if !strings.Contains(string(calls), "fail XR-001 --clear") {
		t.Fatalf("признак провала не снят у задачи с блокера: %q", calls)
	}
	if !strings.Contains(string(calls), "move XR-001 in-progress") {
		t.Fatalf("задача с блокера не вернулась в работу: %q", calls)
	}
	if !strings.Contains(msg, "признак провала снят откатом") {
		t.Fatalf("откат молчит про снятый признак: %q", msg)
	}
	log := gitT(t, root, "log", "-1", "--format=%s")
	if !strings.Contains(log, "обратно в In progress") || !strings.Contains(log, "признак провала снят откатом") {
		t.Fatalf("обе правки доски должны уехать одним коммитом: %q", log)
	}
}

// TestRevertWithoutFailMark: обычный откат задачи из In progress признака не
// касается и лишнего коммита доски не делает.
func TestRevertWithoutFailMark(t *testing.T) {
	root, callLog := setup(t, rowInProg, "")
	codeCommit(t, root, "XR-001", "one.txt")
	before := gitT(t, root, "rev-parse", "HEAD")
	if _, err := cmdRevert(root, RevertParams{ID: "XR-001", Test: "true"}); err != nil {
		t.Fatal(err)
	}
	if calls, _ := os.ReadFile(callLog); strings.Contains(string(calls), "fail XR-001") {
		t.Fatalf("taskctl fail позван на задаче без провала: %q", calls)
	}
	if n := gitT(t, root, "rev-list", "--count", before+"..HEAD"); n != "1" {
		t.Fatalf("после отката без провала коммитов сверху %s, ждал один (сам откат)", n)
	}
}

// TestStatusNamesBrokenProd: status называет сломанный прод отдельной строкой
// и вердикта про свободную очередь рядом с ней не печатает.
func TestStatusNamesBrokenProd(t *testing.T) {
	root, _ := setup(t, rowSelfFailed, "")
	codeCommit(t, root, "XR-001", "one.txt")
	st, err := cmdStatus(root)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(st, "провал проверки за XR-001") || !strings.Contains(st, "merge и ship отказывают") {
		t.Fatalf("status молчит про сломанный прод:\n%s", st)
	}
	if strings.Contains(st, "очередь свободна") {
		t.Fatalf("вердикт про свободную очередь противоречит сломанному проду:\n%s", st)
	}
}

// TestStatusNamesReturnedDeploy: задача ушла из Check с приёмкой замечаний,
// её выкат остался на проде. Очередь такая задача не держит, но выкат за ней
// числится, и status его называет.
func TestStatusNamesReturnedDeploy(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	codeCommit(t, root, "XR-001", "one.txt")
	st, err := cmdStatus(root)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(st, "выкат на проде за ушедшими из Check: XR-001") {
		t.Fatalf("status молчит про выкат вернувшейся в работу задачи:\n%s", st)
	}
	if !strings.Contains(st, "очередь свободна") {
		t.Fatalf("приёмка с замечаниями не должна держать очередь:\n%s", st)
	}
}

// TestStatusSilentOnFreshTask: у задачи первого захода выкатов ещё нет, и
// строки про них в status быть не должно.
func TestStatusSilentOnFreshTask(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	st, err := cmdStatus(root)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(st, "выкат на проде за ушедшими из Check") {
		t.Fatalf("status придумал выкат задаче без слитого кода:\n%s", st)
	}
}

// TestStatusCheckStaysInQueue: задача, которая из Check ещё не уходила, это
// занятая очередь, а не вернувшийся в работу выкат.
func TestStatusCheckStaysInQueue(t *testing.T) {
	root, _ := setup(t, "", rowInProg)
	codeCommit(t, root, "XR-001", "one.txt")
	st, err := cmdStatus(root)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(st, "очередь занята: XR-001") {
		t.Fatalf("выкаченная задача в Check должна держать очередь:\n%s", st)
	}
	if strings.Contains(st, "выкат на проде за ушедшими из Check") {
		t.Fatalf("задача из Check попала в строку про ушедших:\n%s", st)
	}
}

// TestReturnedIgnoresTrain: слитая в поезд задача до прода не доехала, её код
// лежит выше точки выката, и выкатом за ней он не считается.
func TestReturnedIgnoresTrain(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	branchFor(t, root, "XR-001", "xr-001-fix", "a.txt")
	if _, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true", Train: true}); err != nil {
		t.Fatal(err)
	}
	st, err := cmdStatus(root)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(st, "выкат на проде за ушедшими из Check") {
		t.Fatalf("невыкаченный поезд принят за выкат:\n%s", st)
	}
	if !strings.Contains(st, "поезд: XR-001") {
		t.Fatalf("поезд пропал из status:\n%s", st)
	}
}

// TestFailedChecksReadsBoard: разбор пометки идёт по той же строке доски, что
// пишет taskctl, включая соседство с маркером зависимости и блокировкой.
func TestFailedChecksReadsBoard(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	data, _ := os.ReadFile(filepath.Join(root, "docs", "TASKS.md"))
	write(t, root, "docs/TASKS.md", strings.Replace(string(data), "| Починка бага |",
		"| Починка бага [после XR-002] [провал: прод отдаёт 500] [блок: ждём хостера] |", 1))
	b, err := loadBoard(root)
	if err != nil {
		t.Fatal(err)
	}
	if got := failedOf(b, "XR-001"); got != "прод отдаёт 500" {
		t.Fatalf("причина провала разобрана как %q", got)
	}
	if got := failedOf(b, "XR-002"); got != "" {
		t.Fatalf("провал приписан чужой задаче: %q", got)
	}
}

// TestStatusNamesStuckMove: перевод отбили уже после выката, и строка стоит в
// In progress с пометкой в разделе «Выкат». Код её на проде, Check у неё нет,
// и между заходами ship узнать об этом больше неоткуда, поэтому status
// называет строку с причиной отказа и следующим шагом (DK-781). Приёмкой с
// замечаниями такая строка не считается: там правку вернули в работу
// осознанно, а тут перевод недоведён.
func TestStatusNamesStuckMove(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	codeCommit(t, root, "XR-001", "one.txt")
	if err := recordMovePending(root, "XR-001", "taskctl move: XR-001: у барьера «доступ» обходов 4, а перебор в «Приёмка» имеет строк 0"); err != nil {
		t.Fatal(err)
	}
	st, err := cmdStatus(root)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(st, "выкачено, перевод в Check отбит: XR-001") ||
		!strings.Contains(st, "довести: shipctl ship") {
		t.Fatalf("status молчит про недоведённый перевод:\n%s", st)
	}
	if !strings.Contains(st, "у барьера «доступ»") {
		t.Fatalf("причина отказа в статусе не названа:\n%s", st)
	}
	if strings.Contains(st, "выкат на проде за ушедшими из Check") {
		t.Fatalf("недоведённый перевод это не приёмка с замечаниями:\n%s", st)
	}
	// Доведённый перевод пометку гасит, и строка из статуса уходит.
	if err := recordMoveDone(root, "XR-001"); err != nil {
		t.Fatal(err)
	}
	st, err = cmdStatus(root)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(st, "перевод в Check отбит") {
		t.Fatalf("погашенная пометка осталась в статусе:\n%s", st)
	}
}
