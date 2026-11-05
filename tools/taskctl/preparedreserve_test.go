package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/dronrider/devkit/internal/draftproposal"
	"github.com/dronrider/devkit/internal/drafttx"
)

func gitFixture(t *testing.T, root string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=Test", "-c", "user.email=test@example.invalid", "-C", root}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %s %v", args, out, err)
	}
}

// savePreparedDrop записывает в дерево одну новую карточку удаления и отвечает
// номером, который сохранение за ней закрепило.
func savePreparedDrop(t *testing.T, dir string) string {
	t.Helper()
	raw, err := cmdDraftPrepared(dir, "XR-008")
	if err != nil {
		t.Fatal(err)
	}
	var view preparedView
	if err = json.Unmarshal([]byte(raw), &view); err != nil {
		t.Fatal(err)
	}
	input, err := json.Marshal(struct {
		Proposals []draftproposal.Proposal `json:"proposals"`
	}{[]draftproposal.Proposal{{ID: "p1", Revision: 1, Outcome: "drop", Text: "веточный результат", Reason: "лишнее"}}})
	if err != nil {
		t.Fatal(err)
	}
	raw, err = cmdDraftPreparedSave(dir, "XR-008", view.Snapshot, input)
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal([]byte(raw), &view); err != nil {
		t.Fatal(err)
	}
	return view.Assigned["p1"]
}

func boardNumber(t *testing.T, id string) int {
	t.Helper()
	m := idRe.FindStringSubmatch(id)
	if m == nil {
		t.Fatalf("номер %q не вида PREFIX-NNN", id)
	}
	n, err := strconv.Atoi(m[2])
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestPreparedNumbersReservedAcrossWorktrees(t *testing.T) {
	root := setup(t)
	kit, err := filepath.Abs("../../kit")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEVKIT_HOME", filepath.Dir(kit))
	if _, err = cmdDraft(root, "исходник", "mid", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	gitFixture(t, root, "init")
	gitFixture(t, root, "add", "--", "docs")
	gitFixture(t, root, "commit", "-m", "fixture")
	tree := filepath.Join(t.TempDir(), "worktree")
	gitFixture(t, root, "worktree", "add", "-b", "reserve", tree)
	// Связанный worktree не видит незафиксированных prepared-файлов основного
	// дерева: без общей области распределения оба сохранения выдают один номер
	// даже строго по очереди, под общим lock (DK-1564).
	mainID, treeID := savePreparedDrop(t, root), savePreparedDrop(t, tree)
	if mainID == "" || treeID == "" {
		t.Fatalf("номера не выданы: %q %q", mainID, treeID)
	}
	if mainID == treeID {
		t.Fatalf("общая область отдала один номер двум деревьям: %s", mainID)
	}
	if boardNumber(t, treeID) != boardNumber(t, mainID)+1 {
		t.Fatalf("номера пошли не подряд: %s и %s", mainID, treeID)
	}
	// Ответ о свободном номере тоже читает общую область, а не только локальный
	// снимок дерева.
	next, err := cmdID(root)
	if err != nil {
		t.Fatal(err)
	}
	if boardNumber(t, next) <= boardNumber(t, treeID) {
		t.Fatalf("свободный номер %s не учёл закрепления %s и %s", next, mainID, treeID)
	}
}

func TestPreparedNumbersSeeMainBoardBeyondClaims(t *testing.T) {
	root := setup(t)
	kit, err := filepath.Abs("../../kit")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEVKIT_HOME", filepath.Dir(kit))
	if _, err = cmdDraft(root, "исходник", "mid", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	gitFixture(t, root, "init")
	gitFixture(t, root, "add", "--", "docs")
	gitFixture(t, root, "commit", "-m", "fixture")
	tree := filepath.Join(t.TempDir(), "worktree")
	gitFixture(t, root, "worktree", "add", "-b", "stale", tree)
	// Строка XR-009 встала в основную доску прямой записью, мимо механизма
	// выдачи: так выглядят номера, занятые до заведения общей области. Worktree
	// ответвлён раньше и строки не видит, общий ledger пуст.
	board, err := os.ReadFile(boardPath(root))
	if err != nil {
		t.Fatal(err)
	}
	row := "| XR-009 | Вне выдачи | task | P2 | 30 (25+2+1+0+2) | - | [tasks/XR-009.md](tasks/XR-009.md) |\n"
	if err = os.WriteFile(boardPath(root), bytes.Replace(board, []byte("| XR-005 |"), []byte(row+"| XR-005 |"), 1), 0o644); err != nil {
		t.Fatal(err)
	}
	claimed, err := drafttx.ClaimedBoardIDs(tree)
	if err != nil {
		t.Fatal(err)
	}
	if len(claimed) != 0 {
		t.Fatalf("фикстура испорчена, общий ledger не пуст: %v", claimed)
	}
	got := savePreparedDrop(t, tree)
	if got != "XR-010" {
		t.Fatalf("устаревший worktree выдал занятый основной доской номер: %s", got)
	}
}
