package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dronrider/devkit/internal/draftproposal"
)

func TestProposalBasisChangesWithTargetAndType(t *testing.T) {
	root := setup(t)
	kit, err := filepath.Abs("../../kit")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEVKIT_HOME", filepath.Dir(kit))
	home := t.TempDir()
	t.Setenv("HOME", home)
	source := []byte("# XR-8: исходник\n")
	state := draftproposal.State{Version: 1, State: "active", Proposals: []draftproposal.Proposal{{ID: "p1", Revision: 1, Outcome: "attach", Target: "XR-001"}}}
	initial, err := proposalBasis(root, source, state)
	if err != nil {
		t.Fatal(err)
	}
	path := taskFileAbs(root, "XR-001")
	if err = os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, []byte("target edit"), 0644); err != nil {
		t.Fatal(err)
	}
	changed, err := proposalBasis(root, source, state)
	if err != nil || initial == changed {
		t.Fatalf("target: %s %s %v", initial, changed, err)
	}
	project := filepath.Join(root, ".devkit", "draft-types", "task.toml")
	if err = os.MkdirAll(filepath.Dir(project), 0755); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(kit, "draft-types", "task.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(project, data, 0644); err != nil {
		t.Fatal(err)
	}
	replaced, err := proposalBasis(root, source, state)
	if err != nil || replaced == changed {
		t.Fatalf("type: %s %s %v", changed, replaced, err)
	}
	teamPath := filepath.Join(home, ".devkit", "team.toml")
	if err := os.MkdirAll(filepath.Dir(teamPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(teamPath, []byte("# команда\n"), 0600); err != nil {
		t.Fatal(err)
	}
	changedTeam, err := proposalBasis(root, source, state)
	if err != nil || changedTeam == replaced {
		t.Fatalf("team: %s %s %v", replaced, changedTeam, err)
	}

}
