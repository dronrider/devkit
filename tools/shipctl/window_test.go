package main

import (
	"os"
	"strings"
	"testing"
)

// TestMergeKeepsWindowAlive: слияние не сносит директорию, в которой открыто
// окно редактора. Живой случай IRC-097: сессия окна позвала merge, тот по
// дизайну убрал дерево задачи вместе с веткой, рабочий каталог исчез из-под
// живого окна, и окно умерло вместе с сессией и её контекстом (DK-192).
//
// Дерево берётся из аргументов редактора, а не из имени копии: проверяется
// ровно то место, которое shipctl открыл окном, как бы оно ни называлось.
func TestMergeKeepsWindowAlive(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	altHome(t, altEnv("https://endpoint.example/anthropic"))
	log := stubEditor(t)

	if _, err := cmdCode(root, CodeParams{ID: "XR-001"}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(log)
	if err != nil {
		t.Fatal("редактор не звался:", err)
	}
	fields := strings.Fields(strings.TrimSpace(string(raw)))
	tree := fields[len(fields)-1]

	// Правка задачи идёт там же, где открыто окно.
	write(t, tree, "code.txt", "new\n")
	gitT(t, tree, "add", ".")
	gitT(t, tree, "commit", "-qm", "fix: XR-001 правка")
	if _, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true"}); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(tree); err != nil {
		t.Fatalf("директория окна %s исчезла после слияния, окно умирает вместе с сессией: %v", tree, err)
	}
	// Директория не просто лежит: это рабочее дерево git на слитом коммите, из
	// которого сразу берётся следующая задача.
	head := gitT(t, tree, "rev-parse", "HEAD")
	gitT(t, root, "merge-base", "--is-ancestor", head, "main")
	if br := gitT(t, tree, "rev-parse", "--abbrev-ref", "HEAD"); br != "HEAD" {
		t.Fatalf("дерево окна стоит на ветке %q, а ветку задачи после слияния удаляют: она обязана быть отцеплена", br)
	}
	if got, _ := os.ReadFile(tree + "/code.txt"); string(got) != "new\n" {
		t.Fatalf("под окном не слитое состояние: %q", got)
	}
	if br := gitT(t, root, "branch", "--list", "xr-001"); br != "" {
		t.Fatalf("ветка задачи не удалена: %q", br)
	}
}

// TestMergeSyncsWindowTree: копия окна подтягивается на свежий main в конце
// merge, даже когда слитая задача ехала не из копии, а из отдельного дерева
// (случай DK-214, первая находка разбора: копия не трогалась вовсе). Свежий
// main тут не только слитый код, но и коммит доски: подтяжка идёт после него,
// а не сразу за fast-forward, иначе копия отставала бы заново.
func TestMergeSyncsWindowTree(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	wt := startTask(t, root, "XR-001", "code.txt")
	tree := windowTree(root)
	gitT(t, root, "worktree", "add", "--detach", tree, "main")

	msg, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "копия окна "+tree+" подтянута на свежий main") {
		t.Fatalf("в отчёте нет подтяжки копии: %q", msg)
	}
	mainHead := gitT(t, root, "rev-parse", "main")
	if head := gitT(t, tree, "rev-parse", "HEAD"); head != mainHead {
		t.Fatalf("копия %s не на свежем main: копия %s, main %s", tree, head, mainHead)
	}
	if log := gitT(t, tree, "log", "-1", "--format=%s"); !strings.Contains(log, "XR-001 в Check") {
		t.Fatalf("копия должна включать коммит доски, а не только слитый код: %s", log)
	}
	if br := gitT(t, tree, "rev-parse", "--abbrev-ref", "HEAD"); br != "HEAD" {
		t.Fatalf("копия должна остаться detached, стоит на %q", br)
	}
	if _, err := os.Stat(wt); err == nil {
		t.Fatal("worktree слитой задачи должен убираться, как убирался")
	}
}

// TestMergeLeavesBranchedWindowTree: копия, занятая веткой другой задачи, не
// трогается, причина уходит строкой отчёта. Молчание тут неотличимо от
// штатной работы (DoD DK-214), поэтому строка обязана называть причину прямо.
func TestMergeLeavesBranchedWindowTree(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	startTask(t, root, "XR-001", "code.txt")
	tree := windowTree(root)
	gitT(t, root, "worktree", "add", "-b", "xr-099-other", tree, "main")
	preMain := gitT(t, root, "rev-parse", "main")

	msg, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "копия окна "+tree+" стоит на ветке xr-099-other, не подтянута") {
		t.Fatalf("в отчёте нет причины: %q", msg)
	}
	if head := gitT(t, tree, "rev-parse", "HEAD"); head != preMain {
		t.Fatalf("занятая веткой копия не должна двигаться, стоит на %s", head)
	}
	if br := gitT(t, tree, "rev-parse", "--abbrev-ref", "HEAD"); br != "xr-099-other" {
		t.Fatalf("копия должна остаться на своей ветке, стоит на %q", br)
	}
}

// TestMergeLeavesDirtyWindowTree: копия с незакоммиченной правкой не
// трогается, причина уходит строкой отчёта, а сама правка не теряется.
func TestMergeLeavesDirtyWindowTree(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	startTask(t, root, "XR-001", "code.txt")
	tree := windowTree(root)
	gitT(t, root, "worktree", "add", "--detach", tree, "main")
	preHead := gitT(t, tree, "rev-parse", "HEAD")
	write(t, tree, "code.txt", "черновик копии\n")

	msg, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "копия окна "+tree+" не подтянута на main: незакоммиченные правки") {
		t.Fatalf("в отчёте нет причины: %q", msg)
	}
	if head := gitT(t, tree, "rev-parse", "HEAD"); head != preHead {
		t.Fatalf("грязная копия не должна двигаться, стоит на %s", head)
	}
	if got, _ := os.ReadFile(tree + "/code.txt"); string(got) != "черновик копии\n" {
		t.Fatalf("незакоммиченная правка в копии потеряна: %q", got)
	}
}

// TestMergeSilentWithoutWindowTree: без второй подписки копии окна нет вовсе,
// и merge про неё молчит -- строка о подтяжке тут была бы поломкой, а не
// подсказкой (проекту без копии подтягивать нечего).
func TestMergeSilentWithoutWindowTree(t *testing.T) {
	root, callLog := setup(t, rowInProg, "")
	branchWithFix(t, root)
	msg, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(msg, "копия окна") {
		t.Fatalf("без копии окна merge не должен её упоминать: %q", msg)
	}
	if calls, _ := os.ReadFile(callLog); !strings.Contains(string(calls), "move XR-001 check") {
		t.Fatalf("сам merge отработать обязан как обычно: %q", calls)
	}
}

// TestTrainDocsOnlySyncsWindowTree: бескодовую задачу поездного merge
// переводит в Check сам, без ship, и подтяжка копии обязана сработать в этой
// же ветке кода, а не только на общем хвосте с deploy-веткой cmdMerge.
func TestTrainDocsOnlySyncsWindowTree(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	tree := windowTree(root)
	gitT(t, root, "worktree", "add", "--detach", tree, "main")
	gitT(t, root, "checkout", "-qb", "xr-001-docs", "main")
	write(t, root, "docs/lld/train.md", "# LLD\n")
	gitT(t, root, "add", ".")
	gitT(t, root, "commit", "-qm", "docs: XR-001 правка LLD")

	msg, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true", Train: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "копия окна "+tree+" подтянута на свежий main") {
		t.Fatalf("бескодовое поездное слияние должно подтянуть копию: %q", msg)
	}
	mainHead := gitT(t, root, "rev-parse", "main")
	if head := gitT(t, tree, "rev-parse", "HEAD"); head != mainHead {
		t.Fatalf("копия отстаёт: копия %s, main %s", head, mainHead)
	}
}

// TestShipSyncsWindowTree: обычный (не бескодовый) поезд переводит задачи в
// Check и двигает доску только в cmdShip, а не в cmdMerge, поэтому подтяжка
// копии обязана сработать именно там -- иначе копия осталась бы на коммите
// fast-forward, а main уехал бы вперёд выкатом и переводом поезда в Check.
func TestShipSyncsWindowTree(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	branchFor(t, root, "XR-001", "xr-001-fix", "a.txt")
	if _, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true", Train: true}); err != nil {
		t.Fatal(err)
	}
	tree := windowTree(root)
	gitT(t, root, "worktree", "add", "--detach", tree, "main")

	msg, err := cmdShip(root, ShipParams{Deploy: "true"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "копия окна "+tree+" подтянута на свежий main") {
		t.Fatalf("выкат поезда должен подтянуть копию: %q", msg)
	}
	mainHead := gitT(t, root, "rev-parse", "main")
	if head := gitT(t, tree, "rev-parse", "HEAD"); head != mainHead {
		t.Fatalf("копия отстаёт после ship: копия %s, main %s", head, mainHead)
	}
	if log := gitT(t, tree, "log", "-1", "--format=%s"); !strings.Contains(log, "в Check поездом") {
		t.Fatalf("копия должна включать коммит доски поезда: %s", log)
	}
}
