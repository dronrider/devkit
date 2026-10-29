package chat

import (
	"encoding/json"
	"github.com/dronrider/devkit/internal/draftproposal"
	"testing"
)

func TestProposalQuestionRoundTripAndAnswer(t *testing.T) {
	q := ProposalQuestion{Version: 1, Tree: "/tmp/tree", Anchor: "XR-1", Sources: []ProposalSource{{Source: "XR-1", Basis: "shown", Proposals: []draftproposal.Proposal{{ID: "a", Revision: 1, Outcome: "add", Text: "future"}, {ID: "b", Revision: 2, Outcome: "add", After: []string{"a"}}}}}}
	a := Ask{Task: "XR-1", Questions: []Question{{Text: "выбор", Proposal: &q}}}
	parsed, ok := ParseAsk(a.Text())
	if !ok || parsed.Questions[0].Proposal.Sources[0].Proposals[1].After[0] != "a" {
		t.Fatal(parsed)
	}
	req := draftproposal.ConfirmRequest{Snapshots: []draftproposal.Snapshot{{Source: "XR-1", Digest: "shown"}}, Decisions: []draftproposal.Decision{{Source: "XR-1", Proposal: "b", Revision: 2, Action: "accept"}}}
	if err := CheckProposalAnswer(q, "/tmp/tree", req); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name   string
		change func(*draftproposal.ConfirmRequest)
		tree   string
	}{
		{"tree", func(*draftproposal.ConfirmRequest) {}, "/tmp/other"},
		{"basis", func(r *draftproposal.ConfirmRequest) { r.Snapshots[0].Digest = "fresh" }, "/tmp/tree"},
		{"hidden", func(r *draftproposal.ConfirmRequest) { r.Decisions[0].Proposal = "new" }, "/tmp/tree"},
		{"revision", func(r *draftproposal.ConfirmRequest) { r.Decisions[0].Revision++ }, "/tmp/tree"},
		{"reason", func(r *draftproposal.ConfirmRequest) { r.Decisions[0].Action = "reject" }, "/tmp/tree"},
		{"duplicate", func(r *draftproposal.ConfirmRequest) { r.Decisions = append(r.Decisions, r.Decisions[0]) }, "/tmp/tree"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b, _ := json.Marshal(req)
			var copy draftproposal.ConfirmRequest
			json.Unmarshal(b, &copy)
			tc.change(&copy)
			if CheckProposalAnswer(q, tc.tree, copy) == nil {
				t.Fatal("accepted")
			}
		})
	}
	qdata, _ := json.Marshal(q)
	mixed := `{"questions":[{"text":"выбор","proposal":` + string(qdata) + `,"scenario":{"snapshot":"x"}}]}`
	if _, err := ParsePack([]byte(mixed)); err == nil {
		t.Fatal("mixed accepted")
	}
}

func TestDropAskIfKeepsReplacement(t *testing.T) {
	root := t.TempDir()
	first := Ask{Task: "XR-1", Questions: []Question{{Text: "первый"}}}
	next := Ask{Task: "XR-1", Questions: []Question{{Text: "второй"}}}
	if err := WriteAsk(root, TaskName("XR-1"), first); err != nil {
		t.Fatal(err)
	}
	if err := WriteAsk(root, TaskName("XR-1"), next); err != nil {
		t.Fatal(err)
	}
	removed, err := DropAskIf(root, TaskName("XR-1"), first)
	if err != nil || removed {
		t.Fatalf("replacement removed: %v %v", removed, err)
	}
	got, ok := ReadAsk(AskPath(root, TaskName("XR-1")))
	if !ok || got.Questions[0].Text != "второй" {
		t.Fatal(got)
	}
	removed, err = DropAskIf(root, TaskName("XR-1"), next)
	if err != nil || !removed {
		t.Fatal(removed, err)
	}
}
