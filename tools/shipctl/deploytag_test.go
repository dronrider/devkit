package main

import (
	"path/filepath"
	"strings"
	"testing"
)

// rowInProg5 это третья задача стенда: её выкатывает «машина А» вторым
// заходом, и именно её код попадает в окно выката отставшего клона.
const rowInProg5 = "| XR-005 | Третья мелочь | task | P3 | 8 (0+3+0+5+0) |  |\n"

// boardWith фиксирует состав доски стенда и пушит его: стаб taskctl из setup
// доску не правит, а следующему заходу нужен состав как после живого taskctl.
func boardWith(t *testing.T, root, inProg, check string) {
	t.Helper()
	board := "# Тест: задачи (префикс XR)\n\n" +
		"## In progress\n\n" + section(inProg) + "\n" +
		"## Check\n\n" + section(check) + "\n" +
		"## Backlog\n\n" + section("| XR-002 | Задел | task | P3 | 10 (0+5+0+0+5) |  |\n") + "\n" +
		"## Blocked\n\nНет.\n"
	write(t, root, "docs/TASKS.md", board)
	gitT(t, root, "add", "docs/TASKS.md")
	gitT(t, root, "commit", "-qm", "docs(tasks): доска стенда")
	gitT(t, root, "push", "-q", "origin", "main")
}

// staleClone собирает инцидент DK-1251 на стенде из двух машин. Машина A
// выкатывает XR-003 поездом и XR-005 одиночным слиянием, тег deployed уходит в
// origin за каждым выкатом. Клон B снимается после первого выката и потом
// подтягивает один main (fetch и fast-forward, как freshMain и git pull):
// существующий локальный тег при этом не двигается, и код второй машины
// попадает в окно deployed..main чужой задачей в Check.
func staleClone(t *testing.T) (a, b string) {
	t.Helper()
	a, _ = setup(t, rowInProg+rowInProg3+rowInProg5, "")
	taskWithScenario(t, a, "XR-003")
	taskWithScenario(t, a, "XR-005")
	bare := addRemote(t, a)

	// Первый выкат на A: поезд заводит тег, ship катит его и двигает тег на
	// main, XR-003 уходит в Check.
	branchFor(t, a, "XR-003", "xr-003-fix", "c.txt")
	if _, err := cmdMerge(a, MergeParams{ID: "XR-003", Test: "true", Train: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdShip(a, ShipParams{Deploy: "true", Push: true}); err != nil {
		t.Fatal(err)
	}
	boardWith(t, a, rowInProg+rowInProg5, rowInProg3)
	// Smoke по первому выкату освобождает очередь: без отметки второй выкат
	// на A уходил бы в поезд вместо деплоя.
	if _, err := cmdSmoke(a, SmokeParams{ID: "XR-003", Push: true}); err != nil {
		t.Fatal(err)
	}

	// Клон снимается после первого выката: его локальный тег стоит на первой
	// точке и при последующих fetch уже не двигается.
	parent := t.TempDir()
	gitT(t, parent, "clone", "-q", bare, "cloneB")
	b = filepath.Join(parent, "cloneB")
	gitT(t, b, "config", "user.email", "test@test")
	gitT(t, b, "config", "user.name", "test")

	// Второй выкат на A: XR-005 уезжает на прод одиночным слиянием, тег в
	// origin двигается на вторую точку.
	branchFor(t, a, "XR-005", "xr-005-fix", "d.txt")
	if _, err := cmdMerge(a, MergeParams{ID: "XR-005", Test: "true", Deploy: "true", Push: true}); err != nil {
		t.Fatal(err)
	}
	boardWith(t, a, rowInProg, rowInProg3+rowInProg5)

	// B подтягивает main, как это делает freshMain: локальный тег остаётся на
	// первой точке, и выкаченный XR-005 попадает в окно deployed..main.
	gitT(t, b, "fetch", "origin", "main")
	gitT(t, b, "merge", "--ff-only", "origin/main")
	return a, b
}

// TestMergeSurvivesForeignDeploy: слияние на второй машине проходит, когда
// чужой выкат уже в origin. До DK-1251 код чужой задачи в Check попадал по
// отставшему тегу в окно выката, объявлялся strays и ронял слияние; теперь
// тег подтягивается до расчёта состава, а чужой Check без smoke переводит
// слияние в поездной режим, но не останавливает его.
func TestMergeSurvivesForeignDeploy(t *testing.T) {
	_, b := staleClone(t)

	branchFor(t, b, "XR-001", "xr-001-fix", "b.txt")
	msg, err := cmdMerge(b, MergeParams{ID: "XR-001", Test: "true"})
	if err != nil {
		t.Fatalf("слияние на отставшем клоне отказало: %v\n%s", err, msg)
	}
	log := gitT(t, b, "log", "main", "--format=%s")
	if !strings.Contains(log, "fix: XR-001 правка") {
		t.Fatalf("правка XR-001 не доехала до main отставшего клона:\n%s", log)
	}
}

// TestStatusSyncsDeployTag: status на отставшем клоне сверяет тег с origin и
// не рисует аномалию окна на ровном месте (кейс 2 постановки).
func TestStatusSyncsDeployTag(t *testing.T) {
	_, b := staleClone(t)

	msg, err := cmdStatus(b)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(msg, "аномалия") {
		t.Fatalf("status увидел аномалию окна выката на отставшем клоне:\n%s", msg)
	}
	remote := strings.Fields(gitT(t, b, "ls-remote", "origin", "refs/tags/"+deployTag))
	if len(remote) == 0 {
		t.Fatal("на origin нет тега, стенд собран неверно")
	}
	if local := gitT(t, b, "rev-parse", deployTag); local != remote[0] {
		t.Fatalf("тег не подтянут с origin: локально %s, на origin %s", local, remote[0])
	}
}

// TestStatusUnreachableOriginKeepsLocalWindow: при недоступном origin окно
// выката остаётся по локальному тегу, а пометка называет причину, иначе
// «всё сверено» на вид неотличимо от правды.
func TestStatusUnreachableOriginKeepsLocalWindow(t *testing.T) {
	_, b := staleClone(t)
	gitT(t, b, "remote", "set-url", "origin", filepath.Join(t.TempDir(), "origin-нет"))

	msg, err := cmdStatus(b)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "не сверен с origin") {
		t.Fatalf("status промолчал про несверенный тег:\n%s", msg)
	}
	if !strings.Contains(msg, "аномалия") {
		t.Fatalf("окно обязано считаться по локальному тегу и показать аномалию:\n%s", msg)
	}
}

// TestSmokeOnStaleCloneSeesDeploy: smoke отмечается задаче с непроверенным
// выкатом, а выкат виден по тегу. На отставшем клоне тег не находил выкат у
// задачи в Check, отметка не ставилась вовсе (кейс 3 постановки), из-за чего
// очередь не освобождалась и закрытие задачи вставало.
func TestSmokeOnStaleCloneSeesDeploy(t *testing.T) {
	_, b := staleClone(t)

	msg, err := cmdSmoke(b, SmokeParams{ID: "XR-005"})
	if err != nil {
		t.Fatalf("smoke на отставшем клоне отказал: %v\n%s", err, msg)
	}
	if !strings.Contains(msg, "smoke за XR-005 отмечен") {
		t.Fatalf("отметка smoke не поставлена: %q", msg)
	}
}

// TestStatusDeployTagDirection: направление сверки тега в status. Тег
// двигается только вперёд за origin: свой непушенный выкат не откатывается
// назад (кейс 4 постановки), тега нет на origin значит локальное состояние
// остаётся, а при недоступном origin пометка называет причину. Случай «тега
// нет вообще» не поглощается: тег не заводится из ничего (открытая строка
// DK-077).
func TestStatusDeployTagDirection(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	addRemote(t, root)
	write(t, root, "code.txt", "second\n")
	gitT(t, root, "add", ".")
	gitT(t, root, "commit", "-qm", "feat: XR-001 вторая правка")
	gitT(t, root, "push", "-q", "origin", "main")
	c1 := gitT(t, root, "rev-parse", "HEAD~1")
	c2 := gitT(t, root, "rev-parse", "HEAD")

	// Ни локального тега, ни тега на origin: status молчит и тег не заводит.
	msg, err := cmdStatus(root)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(msg, "не сверен") || hasTag(root) {
		t.Fatalf("тег заведён из ничего или сверен с пустым origin: %q", msg)
	}

	// Origin впереди: локальный тег уезжает за ним.
	gitT(t, root, "tag", "--no-sign", deployTag, c1)
	gitT(t, root, "push", "-qf", "origin", deployTag)
	gitT(t, root, "push", "-qf", "origin", c2+":refs/tags/"+deployTag)
	if _, err := cmdStatus(root); err != nil {
		t.Fatal(err)
	}
	if got := gitT(t, root, "rev-parse", deployTag); got != c2 {
		t.Fatalf("тег не догнал origin: %s, ждал %s", got, c2)
	}

	// Свой непушенный выкат: origin позади, локальная точка не откатывается.
	gitT(t, root, "push", "-qf", "origin", c1+":refs/tags/"+deployTag)
	if _, err := cmdStatus(root); err != nil {
		t.Fatal(err)
	}
	if got := gitT(t, root, "rev-parse", deployTag); got != c2 {
		t.Fatalf("тег откатился назад за origin: %s", got)
	}

	// Локального тега нет, на origin есть: тег заводится с origin.
	gitT(t, root, "tag", "-d", deployTag)
	gitT(t, root, "push", "-qf", "origin", c2+":refs/tags/"+deployTag)
	if _, err := cmdStatus(root); err != nil {
		t.Fatal(err)
	}
	if got := gitT(t, root, "rev-parse", deployTag); got != c2 {
		t.Fatalf("тег не заведён с origin: %s", got)
	}

	// Тега на origin нет, локальный есть: состояние остаётся, ошибки нет.
	gitT(t, root, "push", "-q", "origin", ":refs/tags/"+deployTag)
	msg, err = cmdStatus(root)
	if err != nil {
		t.Fatal(err)
	}
	if got := gitT(t, root, "rev-parse", deployTag); got != c2 {
		t.Fatalf("локальный тег потерян при пустом origin: %s", got)
	}
	if strings.Contains(msg, "не сверен") {
		t.Fatalf("пустой origin выдан за недоступный: %q", msg)
	}

	// Недоступный origin: окно остаётся по локальному тегу, пометка видна.
	gitT(t, root, "remote", "set-url", "origin", filepath.Join(t.TempDir(), "origin-нет"))
	msg, err = cmdStatus(root)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "не сверен с origin") {
		t.Fatalf("status промолчал про несверенный тег:\n%s", msg)
	}

	// Без настроенного origin сверять не с чем, тихо.
	gitT(t, root, "remote", "remove", "origin")
	msg, err = cmdStatus(root)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(msg, "не сверен") {
		t.Fatalf("без origin сверки не было, а пометка есть: %q", msg)
	}
}
