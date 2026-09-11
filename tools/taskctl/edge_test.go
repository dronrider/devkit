package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Стенд ребра «после» (решение 2 LLD DK-933): доска фикстуры в git на main, а
// работа предпосылки ложится коммитами прямо в main, как после shipctl merge.
func edgeRepo(t *testing.T) string {
	t.Helper()
	root := setup(t)
	wakeGit(t, root, "init", "-q", "-b", "main")
	wakeGit(t, root, "add", ".")
	wakeGit(t, root, "commit", "-qm", "seed")
	return root
}

func edgeCommit(t *testing.T, root, subj string, files map[string]string) {
	t.Helper()
	for rel, body := range files {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		wakeGit(t, root, "add", rel)
	}
	wakeGit(t, root, "commit", "-qm", subj)
}

func mustMove(t *testing.T, root, id, target string) {
	t.Helper()
	if _, err := cmdMove(root, id, target, "", CommitOpts{}); err != nil {
		t.Fatalf("move %s %s: %v", id, target, err)
	}
}

func mustDep(t *testing.T, root, id, dep string) {
	t.Helper()
	if _, err := cmdDepAdd(root, DepParams{ID: id, DepID: dep}); err != nil {
		t.Fatalf("dep add %s %s: %v", id, dep, err)
	}
}

func startRefused(t *testing.T, root, id, want string) {
	t.Helper()
	_, err := cmdMove(root, id, SectInProgress, "", CommitOpts{})
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("старт %s: ждал отказ со словами %q, а пришло %v", id, want, err)
	}
	if got := sectOf(t, root, id); got != SectBacklog {
		t.Fatalf("отбитый старт сдвинул %s в %s", id, got)
	}
}

// DoD: слияние X пускает Y в работу, откат X и провал X снова держат Y, вид
// user держит Y до закрытия X. Правка X только в доске и файле задачи ребро не
// снимает.
func TestEdgeLifecycleOnBoard(t *testing.T) {
	root := edgeRepo(t)
	mustDep(t, root, "XR-004", "XR-001")
	startRefused(t, root, "XR-004", "XR-001 не слита")
	edgeCommit(t, root, "docs(tasks): XR-001 в работу", map[string]string{
		"docs/tasks/XR-001.md": "# XR-001\n" + fixtureScenario + fixtureVerification + "ход работы\n",
	})
	startRefused(t, root, "XR-004", "XR-001 не слита")
	edgeCommit(t, root, "feat(x): XR-001 правка", map[string]string{"x/code.go": "package x\n"})
	mustMove(t, root, "XR-004", SectInProgress)
	mustMove(t, root, "XR-004", SectBacklog)

	edgeCommit(t, root, "revert: XR-001 откат", map[string]string{"x/code.go": ""})
	startRefused(t, root, "XR-004", "XR-001 откачена")

	edgeCommit(t, root, "fix(x): XR-001 снова", map[string]string{"x/code.go": "package x\n"})
	mustMove(t, root, "XR-001", SectInProgress)
	if _, err := cmdFail(root, FailParams{ID: "XR-001", Reason: "прод отдаёт 500"}); err != nil {
		t.Fatal(err)
	}
	startRefused(t, root, "XR-004", "XR-001 провалена: прод отдаёт 500")
	if _, err := cmdFail(root, FailParams{ID: "XR-001", Clear: true}); err != nil {
		t.Fatal(err)
	}
	mustMove(t, root, "XR-004", SectInProgress)
	mustMove(t, root, "XR-004", SectBacklog)

	if _, err := cmdSet(root, SetParams{ID: "XR-001", Accept: acceptUser}); err != nil {
		t.Fatal(err)
	}
	startRefused(t, root, "XR-004", "XR-001 с приёмкой user")
	// Закрытие руками: ворота close тут не предмет, предмет архив.
	archiveByHand(t, root, "XR-001")
	mustMove(t, root, "XR-004", SectInProgress)
}

// DoD: правка в docs/lld снимает ребро, зависимой от LLD нужен документ в main.
func TestEdgeLiftedByLLDOnBoard(t *testing.T) {
	root := edgeRepo(t)
	mustDep(t, root, "XR-004", "XR-003")
	startRefused(t, root, "XR-004", "XR-003 не слита")
	edgeCommit(t, root, "docs(lld): XR-003 документ", map[string]string{"docs/lld/XR-003.md": "# LLD\n"})
	mustMove(t, root, "XR-004", SectInProgress)
}

// DoD: возврат Y из Check при откаченной X проходит, и lint не требует вернуть
// начатую Y в Backlog. Выход из Blocked рёбер тоже не проверяет. Ворота dep add
// на начатой строке пропускают снятое ребро и отбивают неснятое, а lint у
// начатой строки находит ребро, которое ворота старта не пропустили бы ни разу.
func TestEdgeStartedRow(t *testing.T) {
	root := edgeRepo(t)
	mustDep(t, root, "XR-004", "XR-001")
	edgeCommit(t, root, "feat(x): XR-001 правка", map[string]string{"x/code.go": "package x\n"})
	edgeCommit(t, root, "feat(y): XR-002 правка", map[string]string{"y/code.go": "package y\n"})
	mustMove(t, root, "XR-004", SectInProgress)
	mustDep(t, root, "XR-004", "XR-002")
	if _, err := cmdDepAdd(root, DepParams{ID: "XR-004", DepID: "XR-003"}); err == nil ||
		!strings.Contains(err.Error(), "неснятое ребро на XR-003") {
		t.Fatalf("неснятое ребро дописано начатой строке: %v", err)
	}
	// Отметка обкатки фикстуры стоит на чужом коммите, а HEAD стенда свой:
	// ворота обкатки тут не предмет, их гасит штатная пометка.
	if err := os.WriteFile(filepath.Join(root, "docs", "tasks", "XR-004.md"), []byte("# XR-004\n"+fixtureScenario+fixtureVerification+
		"\n- Исключение: обкатка (стенд ребра, HEAD не совпадает с отметкой фикстуры)\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustMove(t, root, "XR-004", SectCheck)

	edgeCommit(t, root, "revert: XR-001 откат", map[string]string{"x/code.go": ""})
	mustMove(t, root, "XR-004", SectInProgress)
	if _, err := cmdMove(root, "XR-004", SectBlocked, "слияние: XR-001", CommitOpts{}); err != nil {
		t.Fatalf("парковка до нового слияния предпосылки: %v", err)
	}
	mustMove(t, root, "XR-004", SectInProgress)
	finds, err := cmdLint(root)
	if err != nil {
		t.Fatal(err)
	}
	if joined := strings.Join(finds, "\n"); strings.Contains(joined, "ребром на XR-001") {
		t.Fatalf("lint требует вернуть начатую строку при откаченной предпосылке:\n%s", joined)
	}

	if _, err := cmdSet(root, SetParams{ID: "XR-001", Accept: acceptUser}); err != nil {
		t.Fatal(err)
	}
	finds, err = cmdLint(root)
	if err != nil {
		t.Fatal(err)
	}
	if joined := strings.Join(finds, "\n"); !strings.Contains(joined, "XR-004 в In progress с неснятым ребром на XR-001 (XR-001 с приёмкой user") {
		t.Fatalf("lint пропустил начатую строку за предпосылкой вида user:\n%s", joined)
	}
}

func rowJSON(t *testing.T, root, id string) jsonRow {
	t.Helper()
	out, err := cmdListJSON(root, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, sec := range decodeBoard(t, out).Sections {
		for _, r := range sec.Rows {
			if r.ID == id {
				return r
			}
		}
	}
	t.Fatalf("%s нет в list --json", id)
	return jsonRow{}
}

func depJSON(t *testing.T, root, id string) jsonDep {
	t.Helper()
	out, err := cmdDepListJSON(root, id)
	if err != nil {
		t.Fatal(err)
	}
	var d jsonDep
	if err := json.Unmarshal([]byte(out), &d); err != nil {
		t.Fatalf("dep list --json не разобрался: %v\n%s", err, out)
	}
	return d
}

// DoD: dep list --json и JSON строки отдают неснятые рёбра. По снятому ребру
// held_by пуст, а after остаётся: маркер снимает только close, и держать строку
// по нему экран больше не должен.
func TestEdgeJSON(t *testing.T) {
	root := edgeRepo(t)
	mustDep(t, root, "XR-004", "XR-001")
	if r := rowJSON(t, root, "XR-004"); strings.Join(r.HeldBy, ",") != "XR-001" {
		t.Fatalf("held_by до слияния: %+v", r)
	}
	if d := depJSON(t, root, "XR-004"); len(d.Edges) != 1 || d.Edges[0].State != "ждёт" || d.Edges[0].ID != "XR-001" {
		t.Fatalf("состояние ребра до слияния: %+v", d)
	}
	edgeCommit(t, root, "feat(x): XR-001 правка", map[string]string{"x/code.go": "package x\n"})
	if r := rowJSON(t, root, "XR-004"); len(r.HeldBy) != 0 || strings.Join(r.After, ",") != "XR-001" {
		t.Fatalf("после слияния held_by %v, after %v", r.HeldBy, r.After)
	}
	if d := depJSON(t, root, "XR-004"); d.Edges[0].State != "слита" || !strings.Contains(d.Edges[0].Why, "XR-001 слита") {
		t.Fatalf("состояние слитого ребра: %+v", d.Edges)
	}
	out, err := cmdShowJSON(root, "XR-004")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "held_by") {
		t.Fatalf("show --json держит строку по снятому ребру: %s", out)
	}
	archiveByHand(t, root, "XR-001")
	whole, err := cmdDepListJSON(root, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(whole, `"state":"закрыта"`) {
		t.Fatalf("dep list --json по доске не называет закрытое ребро: %s", whole)
	}
}
