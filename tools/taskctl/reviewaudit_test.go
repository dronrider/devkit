package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dronrider/devkit/internal/reviewaudit"
	"github.com/dronrider/devkit/internal/taskhead"
)

// Existing workflow tests supply an assessed report; gate failures below call
// production commands directly so this fixture cannot mask a missing gate.
func completeReviewAudit(t *testing.T, root, id string) string {
	t.Helper()
	dk := t.TempDir()
	t.Setenv("DEVKIT_HOME", dk)
	for p, body := range map[string]string{taskhead.TaskRunRel: "marker", "RULES.architecture.md": "- **ARCH-001.** Сохраняется инвариант.\n", "RULES.style.md": "- **STYLE-001.** Имя соответствует смыслу.\n", "RULES.md": "## Тесты обязательны\n\n- **TEST-001.** Проверяется результат.\n"} {
		full := filepath.Join(dk, p)
		if e := os.MkdirAll(filepath.Dir(full), 0755); e != nil {
			t.Fatal(e)
		}
		if e := os.WriteFile(full, []byte(body), 0644); e != nil {
			t.Fatal(e)
		}
	}
	if _, e := headSha(root); e != nil {
		gitSetup(t, root)
	}
	path, e := reviewAuditPath(root, id)
	if e != nil {
		t.Fatal(e)
	}
	_, stat := os.Stat(path)
	if _, e = reviewaudit.Create(root, path, id, "HEAD", stat == nil); e != nil {
		t.Fatal(e)
	}
	fillReviewAudit(t, path)
	return path
}
func fillReviewAudit(t *testing.T, path string) {
	t.Helper()
	b, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	var r reviewaudit.Report
	if e = json.Unmarshal(b, &r); e != nil {
		t.Fatal(e)
	}
	r.Architecture = reviewaudit.Architecture{Goal: "результат", Decision: "один владелец", Boundaries: "граница хранения", Alternative: "дубликат отклонён", Justification: "сохраняется инвариант", Result: "checked", Evidence: []string{"code.txt:1 операция"}}
	for i := range r.Rules {
		r.Rules[i].Applicability = "applicable"
		r.Rules[i].Result = "checked"
		r.Rules[i].Reason = "Относится к операции"
		r.Rules[i].Evidence = []reviewaudit.Evidence{{Object: "операция", Location: "code.txt:1", Observation: "состояние сохраняется"}}
	}
	b, e = json.MarshalIndent(r, "", "  ")
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(path, b, 0644); e != nil {
		t.Fatal(e)
	}
}

func cleanWithAudit(t *testing.T, root, id, note string, newRound bool, c CommitOpts) (string, error) {
	t.Helper()
	completeReviewAudit(t, root, id)
	return cmdReviewClean(root, id, note, newRound, c)
}

func TestReviewAuditRequiredForClean(t *testing.T) {
	for _, board := range []bool{true, false} {
		name := "notes"
		if board {
			name = "board"
		}
		t.Run(name, func(t *testing.T) {
			root := noteRepo(t)
			id := "MR-42"
			if board {
				root = setup(t)
				gitSetup(t, root)
				id = "XR-005"
			}
			clean := func() error {
				var e error
				if board {
					_, e = cmdReviewClean(root, id, "", false, CommitOpts{})
				} else {
					_, e = cmdNoteClean(root, id, "", CommitOpts{})
				}
				return e
			}
			if e := clean(); e == nil || !strings.Contains(e.Error(), "отчёт") {
				t.Fatalf("missing report accepted: %v", e)
			}
			path := completeReviewAudit(t, root, id)
			b, _ := os.ReadFile(path)
			var r reviewaudit.Report
			json.Unmarshal(b, &r)
			r.Rules = r.Rules[1:]
			b, _ = json.Marshal(r)
			os.WriteFile(path, b, 0644)
			if e := clean(); e == nil {
				t.Fatal("incomplete report accepted")
			}
			completeReviewAudit(t, root, id)
			if e := clean(); e != nil {
				t.Fatalf("complete report rejected: %v", e)
			}
		})
	}
}
func TestReviewAuditCLIWorkflow(t *testing.T) {
	root := setup(t)
	gitSetup(t, root)
	path := completeReviewAudit(t, root, "XR-005")
	if _, e := cmdReviewAudit(root, root, "XR-005", "", false, true); e != nil {
		t.Fatal(e)
	}
	if _, e := cmdReviewAudit(root, root, "XR-005", "", false, false); e == nil {
		t.Fatal("overwrote report")
	}
	out, e := cmdReviewAudit(root, root, "XR-005", "", true, false)
	if e != nil || !strings.Contains(out, path) {
		t.Fatalf("refresh: %s %v", out, e)
	}
	if _, e := cmdReviewAudit(root, root, "XR-005", "", false, true); e == nil {
		t.Fatal("pending refresh accepted")
	}
}

func prepareApprovalAudit(t *testing.T, root string) string {
	t.Helper()
	if _, e := headSha(root); e != nil {
		gitSetup(t, root)
	}
	sha := gitOut(t, root, "rev-parse", "HEAD")
	d := draftOf(t, root)
	d.Sha = sha
	if e := d.save(); e != nil {
		t.Fatal(e)
	}
	completeReviewAudit(t, root, "XR-005")
	return sha
}
func TestApproveMRRequiresReportAndCurrentRemoteVersion(t *testing.T) {
	root, _ := pollEnv(t)
	writeReviewConf(t, root, "publish = auto\n")
	d := draftOf(t, root)
	d.Blocks[0].State = reviewStateDropped
	d.save()
	gitSetup(t, root)
	calls := stubPoll(t, mrOpen, threadQuiet)
	if _, e := cmdReviewApproveMR(root, "XR-005", false, CommitOpts{}); e == nil {
		t.Fatal("approved without report")
	}
	if len(*calls) != 0 {
		t.Fatal("network used before completeness gate")
	}
	prepareApprovalAudit(t, root)
	if _, e := cmdReviewApproveMR(root, "XR-005", false, CommitOpts{}); e == nil || !strings.Contains(e.Error(), "вершина MR") {
		t.Fatalf("stale remote: %v", e)
	}
	if len(*calls) != 1 || strings.Contains((*calls)[0], "/approve") {
		t.Fatalf("stale MR approved: %v", *calls)
	}
}

func TestReviewAuditCorporateCodeRoot(t *testing.T) {
	board := setup(t)
	code := noteRepo(t)
	completeReviewAudit(t, board, "XR-005") // supplies standards and a deliberately wrong board snapshot
	corpGitT(t, code, "config", "devkit.local", board)
	path, _ := reviewAuditPath(board, "XR-005")
	if _, e := cmdReviewCleanFrom(board, code, "XR-005", "", false, CommitOpts{}); e == nil {
		t.Fatal("board snapshot accepted for corporate code")
	}
	if _, e := cmdReviewAudit(board, code, "XR-005", "HEAD", true, false); e != nil {
		t.Fatal(e)
	}
	fillReviewAudit(t, path)
	b, _ := os.ReadFile(path)
	var report reviewaudit.Report
	json.Unmarshal(b, &report)
	if report.HeadSHA != noteGit(t, code, "rev-parse", "HEAD") {
		t.Fatalf("snapshot uses board HEAD: %+v", report)
	}
	if _, e := cmdReviewCleanFrom(board, code, "XR-005", "проверено", false, CommitOpts{}); e != nil {
		t.Fatal(e)
	}
	if got := readTaskFile(t, board, "XR-005"); !strings.Contains(got, noteGit(t, code, "rev-parse", "--short=7", "HEAD")) {
		t.Fatalf("verdict SHA is not code HEAD: %s", got)
	}
}
func TestReviewAuditCleanCommitIncludesReport(t *testing.T) {
	root := setup(t)
	completeReviewAudit(t, root, "XR-005")
	if _, e := cmdReviewClean(root, "XR-005", "", false, CommitOpts{Msg: "docs(tasks): XR-005 assessed"}); e != nil {
		t.Fatal(e)
	}
	names := gitOut(t, root, "show", "--pretty=", "--name-only", "HEAD")
	if !strings.Contains(names, "docs/tasks/XR-005.review-audit.json") {
		t.Fatalf("report omitted from commit: %s", names)
	}
	if e := checkReviewAudit(root, root, "XR-005"); e != nil {
		t.Fatalf("report stale after clean commit: %v", e)
	}
}

func TestApproveScriptPinsReviewedCommit(t *testing.T) {
	sha := strings.Repeat("a", 40)
	for _, kind := range []string{"gitlab", "github", "gitea"} {
		t.Run(kind, func(t *testing.T) {
			script, e := approveScript(mrTarget{Kind: kind, Host: "https://example.com", Project: "team/project", Number: "42", TokenEnv: "TEST_TOKEN"}, sha)
			if e != nil {
				t.Fatal(e)
			}
			field := "commit_id"
			if kind == "gitlab" {
				field = "sha"
			}
			if !strings.Contains(script, field) || !strings.Contains(script, sha) {
				t.Fatalf("approval is not bound: %s", script)
			}
			if kind != "github" && !strings.Contains(script, "curl --fail ") {
				t.Fatalf("HTTP conflict silently succeeds: %s", script)
			}
		})
	}
	if _, e := approveScript(mrTarget{Kind: "github"}, "HEAD"); e == nil {
		t.Fatal("mutable commit accepted")
	}
}
func TestApproveMRRemoteConflictDoesNotRecordApproval(t *testing.T) {
	root, _ := pollEnv(t)
	writeReviewConf(t, root, "publish = auto\n")
	d := draftOf(t, root)
	d.Blocks[0].State = reviewStateDropped
	d.save()
	sha := prepareApprovalAudit(t, root)
	old := runPublish
	t.Cleanup(func() { runPublish = old })
	runPublish = func(script string) ([]byte, error) {
		if strings.Contains(script, "/approve") {
			return nil, os.ErrPermission
		}
		return []byte(`{"sha":"` + sha + `"}`), nil
	}
	if _, e := cmdReviewApproveMR(root, "XR-005", false, CommitOpts{}); e == nil {
		t.Fatal("conflicting API approval accepted")
	}
	if draftOf(t, root).Mark == reviewMarkApproved {
		t.Fatal("failed approval marked done")
	}
}

func TestNoteCleanMultipleLabels(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	root := noteRepo(t)
	for _, id := range []string{"MR-42", "MR-43"} {
		completeReviewAudit(t, root, id)
		if _, e := cmdNoteClean(root, id, "проверено", CommitOpts{}); e != nil {
			t.Fatalf("%s: %v", id, e)
		}
	}
}

func TestReviewCleanNewRoundRechecksChangedRulesAndTask(t *testing.T) {
	for _, change := range []string{"rules", "task"} {
		t.Run(change, func(t *testing.T) {
			root := setup(t)
			path := completeReviewAudit(t, root, "XR-005")
			if _, e := cmdReviewClean(root, "XR-005", "first", false, CommitOpts{}); e != nil {
				t.Fatal(e)
			}
			if change == "rules" {
				corpWrite(t, filepath.Join(os.Getenv("DEVKIT_HOME"), "RULES.style.md"), "- **STYLE-001.** Updated naming rule.\n")
			} else {
				corpWrite(t, taskFileAbs(root, "XR-005"), readTaskFile(t, root, "XR-005")+"\n## New requirement\nChanged behavior.\n")
			}
			if _, e := cmdReviewClean(root, "XR-005", "second", true, CommitOpts{}); e == nil {
				t.Fatal("new-round bypassed stale audit")
			}
			if _, e := cmdReviewAudit(root, root, "XR-005", "", true, false); e != nil {
				t.Fatal(e)
			}
			fillReviewAudit(t, path)
			if _, e := cmdReviewClean(root, "XR-005", "second", true, CommitOpts{}); e != nil {
				t.Fatalf("refreshed audit cannot finish: %v", e)
			}
		})
	}
}
