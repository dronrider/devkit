package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPushAuditRequiresCompletedCurrentReport(t *testing.T) {
	for _, mode := range []string{"missing", "stale", "complete"} {
		t.Run(mode, func(t *testing.T) {
			root, _ := setup(t, rowInProg, "")
			addRemote(t, root)
			gitT(t, root, "checkout", "-qb", "xr-001")
			codeCommit(t, root, "XR-001", "feature.txt")
			if mode != "missing" {
				completeReviewAudit(t, root, "XR-001", true)
			}
			if mode == "stale" {
				codeCommit(t, root, "XR-001", "later.txt")
			}
			err := checkOnly(t, root)
			if mode == "complete" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), "отчёт ревью") {
				t.Fatalf("%s report accepted or wrong refusal: %v", mode, err)
			}
		})
	}
}

func TestPushAuditUsesPushedVersion(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	addRemote(t, root)
	gitT(t, root, "checkout", "-qb", "xr-001")
	codeCommit(t, root, "XR-001", "feature.txt")
	pushed := gitT(t, root, "rev-parse", "HEAD")
	reviewNote(t, root, "Ярлык: pushed-review\nУровень 2 до a1b2c3d: проверено")
	codeCommit(t, root, "XR-001", "later.txt")
	completeReviewAudit(t, root, "pushed-review", false)
	base := gitT(t, root, "rev-parse", "origin/main")
	if err := reviewTraceGate(root, base, pushed); err == nil || !strings.Contains(err.Error(), "изменились после отчёта") {
		t.Fatalf("report for HEAD certified another pushed commit: %v", err)
	}
}

func TestPushAuditAcceptsReviewedCommitWithDifferentWorkspace(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	addRemote(t, root)
	gitT(t, root, "checkout", "-qb", "xr-001")
	codeCommit(t, root, "XR-001", "feature.txt")
	completeReviewAudit(t, root, "pushed-review", false)
	reviewNote(t, root, "Ярлык: pushed-review\nУровень 2 до a1b2c3d: проверено")
	pushed := gitT(t, root, "rev-parse", "HEAD")
	codeCommit(t, root, "XR-001", "later.txt")
	write(t, root, "unfinished.txt", "another session\n")
	base := gitT(t, root, "rev-parse", "origin/main")
	if err := reviewTraceGate(root, base, pushed); err != nil {
		t.Fatalf("reviewed pushed commit refused because of later workspace: %v", err)
	}
}

func TestPushAuditBoardDoesNotBorrowCurrentTask(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	addRemote(t, root)
	gitT(t, root, "checkout", "-qb", "xr-001")
	codeCommit(t, root, "XR-001", "feature.txt")
	completeReviewAudit(t, root, "XR-001", true)
	gitT(t, root, "checkout", "-qb", "xr-002")
	write(t, root, "docs/tasks/XR-002.md", "# XR-002\n\n## DoD\n\nДругое требование к тому же коду.\n")
	gitT(t, root, "add", "docs/tasks/XR-002.md")
	gitT(t, root, "commit", "-qm", "docs: XR-002 requirements")
	pushed := gitT(t, root, "rev-parse", "HEAD")
	gitT(t, root, "checkout", "xr-001")
	base := gitT(t, root, "rev-parse", "origin/main")
	if err := reviewTraceGate(root, base, pushed); err == nil || !strings.Contains(err.Error(), "текущей ветке") {
		t.Fatalf("another task certified by current branch report: %v", err)
	}
}

func TestPushAuditNoteNeedsLabelAndReport(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	addRemote(t, root)
	codeCommit(t, root, "XR-001", "feature.txt")
	reviewNote(t, root, "Уровень 2 до a1b2c3d: проверено")
	if err := checkOnly(t, root); err == nil || !strings.Contains(err.Error(), "ярлыка") {
		t.Fatalf("unlabelled note accepted: %v", err)
	}
	reviewNote(t, root, "Ярлык: external-review\nУровень 2 до a1b2c3d: проверено")
	if err := checkOnly(t, root); err == nil || !strings.Contains(err.Error(), "отчёт ревью") {
		t.Fatalf("missing labelled audit accepted: %v", err)
	}
	completeReviewAudit(t, root, "external-review", false)
	if err := checkOnly(t, root); err != nil {
		t.Fatal(err)
	}
	codeCommit(t, root, "XR-001", "later.txt")
	reviewNote(t, root, "Ярлык: external-review\nУровень 2 до a1b2c3d: проверено")
	if err := checkOnly(t, root); err == nil {
		t.Fatal("stale labelled audit accepted")
	}
}

func TestMergeAuditRefusesBeforeTestsAndMain(t *testing.T) {
	for _, mode := range []string{"missing", "stale", "rebase"} {
		t.Run(mode, func(t *testing.T) {
			root, _ := setup(t, rowInProg, "")
			branchWithFix(t, root)
			if mode == "missing" {
				gitT(t, root, "rm", "docs/tasks/XR-001.review-audit.json")
				gitT(t, root, "commit", "--amend", "--no-edit", "-q")
			} else if mode == "stale" {
				codeCommit(t, root, "XR-001", "later.txt")
			} else {
				gitT(t, root, "checkout", "main")
				codeCommit(t, root, "XR-001", "other.txt")
				gitT(t, root, "checkout", "xr-001-fix")
			}
			main := gitT(t, root, "rev-parse", "main")
			marker := filepath.Join(t.TempDir(), "tests-ran")
			_, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "touch " + marker, Train: true})
			if err == nil || !strings.Contains(err.Error(), "отчёт ревью") {
				t.Fatalf("%s report accepted or wrong refusal: %v", mode, err)
			}
			if mode == "rebase" && !strings.Contains(err.Error(), "после ребейза") {
				t.Fatalf("expected post-rebase refusal: %v", err)
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatal("tests ran before audit validation")
			}
			if got := gitT(t, root, "rev-parse", "main"); got != main {
				t.Fatal("main moved without valid audit")
			}
		})
	}
}
