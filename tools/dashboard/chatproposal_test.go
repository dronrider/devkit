package main

import (
	"encoding/json"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/dronrider/devkit/internal/chat"
	"github.com/dronrider/devkit/internal/draftproposal"
)

func TestProposalAnswerContext(t *testing.T) {
	q := &chat.ProposalQuestion{Version: 1, Tree: "/tmp/tree", Anchor: "XR-1", Sources: []chat.ProposalSource{{Source: "XR-1", Basis: "old", Proposals: []draftproposal.Proposal{{ID: "p", Revision: 1, Outcome: "drop"}}}}}
	if proposalFromAsk(chat.Ask{Task: "XR-1", Questions: []chat.Question{{Text: "q", Proposal: q}}}) != q {
		t.Fatal("shown question lost")
	}
	if proposalFromAsk(chat.Ask{Task: "XR-1", Questions: []chat.Question{{Text: "q", Scenario: &chat.ScenarioQuestion{}}}}) != nil {
		t.Fatal("scenario is not proposal")
	}
	if proposalFromAsk(chat.Ask{Task: "XR-2", Questions: []chat.Question{{Text: "q", Proposal: q}}}) != nil {
		t.Fatal("wrong anchor")
	}
	if _, err := proposalAnswerInput(proposalAnswer{}); err == nil {
		t.Fatal("missing context")
	}
}
func TestProposalHelpersNode(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node недоступен")
	}
	shown := chat.ProposalQuestion{Version: 1, Tree: "/worktree", Anchor: "XR-1", Sources: []chat.ProposalSource{
		{Source: "XR-1", Basis: "old", Proposals: []draftproposal.Proposal{{ID: "a", Revision: 1, Outcome: "add"}, {ID: "b", Revision: 2, Outcome: "add", After: []string{"a"}}}},
		{Source: "XR-2", Basis: "basis2", Proposals: []draftproposal.Proposal{{ID: "c", Revision: 1, Outcome: "drop"}}},
	}}
	data, err := json.Marshal(shown)
	if err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(node, filepath.Join("testdata", "proposal_helpers.mjs"), filepath.Join("static", "app.js"), string(data)).CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
}
