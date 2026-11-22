package reviewaudit

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestRuntimeReportIntegrationUsesCommittedAudit(t *testing.T) {
	root, path := fixture(t)
	ordinary := completed(t, root, path)
	code := gitT(t, root, "rev-parse", "HEAD")
	live := ordinary
	live.Rules = append(append([]Rule{}, ordinary.Rules...), Rule{
		ID: namingRule, Evidence: []Evidence{{
			ObjectComparison: &NameObjectComparison{Proof: &NameObjectProof{
				Runtime: &NameRuntimeProof{Runner: "tester", Payload: "revision-bound payload"},
			}},
		}},
	})
	save(t, path, live)
	if err := ValidateAt(root, path, "XR-1", code); err == nil || !strings.Contains(err.Error(), "выпускаемом коммите") {
		t.Fatalf("accepted the uncommitted runtime report: %v", err)
	}
	gitT(t, root, "add", path)
	gitT(t, root, "commit", "-qm", "add runtime report")
	if _, err := runtimeReportAtCommit(root, root, path, "XR-1", live); err != nil {
		t.Fatalf("rejected the committed report: %v", err)
	}
	save(t, path, ordinary)
	if err := ValidateAt(root, path, "XR-1", "HEAD"); err == nil || !strings.Contains(err.Error(), "рабочий отчёт отличается") {
		t.Fatalf("used a working report in place of a committed runtime report: %v", err)
	}
	other := filepath.Join(root, "another-audit.json")
	save(t, other, live)
	if _, err := runtimeReportAtCommit(root, root, other, "XR-1", live); err == nil {
		t.Fatal("accepted a receipt from a different report path")
	}
}

func TestRuntimeProofTemplateIsLimitedToWrites(t *testing.T) {
	for _, tc := range []struct {
		check, result string
		wantRuntime   bool
	}{
		{"writes", "checked", true},
		{"result", "checked", false},
		{"meaning", "checked", false},
		{"writes", "pending", false},
	} {
		entries, err := EvidenceTemplates("", "STYLE-001", tc.check, "code.go:1#writeClock", tc.result, "runtime")
		if err != nil || len(entries) != 1 {
			t.Fatalf("template %s/%s: %+v, %v", tc.check, tc.result, entries, err)
		}
		pair := entries[0].ObjectComparison
		hasRuntime := pair != nil && pair.Proof != nil && pair.Proof.Runtime != nil
		if hasRuntime != tc.wantRuntime {
			t.Errorf("template %s/%s: runtime = %t, want %t", tc.check, tc.result, hasRuntime, tc.wantRuntime)
		}
	}
}
