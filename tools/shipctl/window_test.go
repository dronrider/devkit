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
