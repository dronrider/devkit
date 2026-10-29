package main

import (
	"os"
	"strings"
	"testing"

	"github.com/dronrider/devkit/internal/draftmeta"
	"github.com/dronrider/devkit/internal/draftproposal"
	"github.com/dronrider/devkit/internal/drafttypes"
)

func TestProposalPlanDependenciesAndNoSourceConsumption(t *testing.T) {
	root := setup(t)
	if _, err := cmdDraft(root, "исходник", "mid", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	source, err := os.ReadFile(draftPath(root, "XR-008"))
	if err != nil {
		t.Fatal(err)
	}
	first := draftproposal.Proposal{ID: "p1", Revision: 1, Outcome: "add", Title: "Первое", Text: fixtureTaskBody, BoardType: "task", Rank: "0+1+1+0+1", Size: "M", Accept: "agent"}
	second := first
	second.ID = "p2"
	second.Title = "Второе"
	second.After = []string{"p1"}
	state := draftproposal.State{Version: 1, State: "active", Proposals: []draftproposal.Proposal{first, second}}
	header := draftmeta.Header{Type: "task", Source: "thought", Lock: "unlocked", Priority: "mid"}
	def := drafttypes.Definition{Name: "task", Required: []string{"title", "body", "type", "rank", "size", "accept"}}
	s, err := newStagingBoard(root)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err = s.planProposals("XR-008", header, def, state, []string{"p2"}); err == nil {
		t.Fatal("unselected prerequisite accepted")
	}
	receipts, err := s.planProposals("XR-008", header, def, state, []string{"p2", "p1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(receipts) != 2 || receipts[0].Proposal != "p1" || receipts[1].Proposal != "p2" {
		t.Fatalf("receipts %+v", receipts)
	}
	board, err := os.ReadFile(boardPath(s.root))
	if err != nil || !strings.Contains(string(board), "[после "+receipts[0].IDs[0]+"]") {
		t.Fatalf("dependency absent %s %v", board, err)
	}
	after, err := os.ReadFile(draftPath(s.root, "XR-008"))
	if err != nil || string(after) != string(source) {
		t.Fatalf("source changed: %v", err)
	}
}
