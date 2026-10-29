package taskhead

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dronrider/devkit/internal/draftproposal"
)

func TestGroomRunOnlyAcceptedAddReceipt(t *testing.T) {
	s := newStand(t, true, true)
	t.Setenv("DEVKIT_GROOM_SOURCE", "DK-8")
	t.Setenv("DEVKIT_GROOM_MODE", "run")
	path := filepath.Join(s.root, "docs", "tasks", "drafts", "DK-8.md")
	put(t, path, "# DK-8: источник\n", 0644)
	state := draftproposal.State{Version: 1, State: "active", Proposals: []draftproposal.Proposal{{ID: "p1", Revision: 1, Outcome: "add"}}}
	write := func() {
		t.Helper()
		data, err := draftproposal.Replace([]byte("# DK-8: источник\n"), state)
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(path, data, 0644); err != nil {
			t.Fatal(err)
		}
	}
	write()
	if _, err := Raise(s.req()); err == nil {
		t.Fatal("pending add launched")
	}
	if len(s.lines("runner.log")) != 0 {
		t.Fatal("runner started before confirmation")
	}
	receipt := draftproposal.Receipt{Key: "receipt", Source: "DK-8", Proposal: "p1", Revision: 1, Outcome: "add", IDs: []string{"DK-1"}, Result: "создано"}
	state.Proposals[0].Status, state.Proposals[0].Result = "accepted", &receipt
	state.Receipts = []draftproposal.Receipt{receipt}
	write()
	q := s.req()
	q.ID = "DK-2"
	if _, err := Raise(q); err == nil {
		t.Fatal("unshown result allowed")
	}
	t.Setenv("DEVKIT_GROOM_MODE", "groom")
	if _, err := Raise(s.req()); err == nil {
		t.Fatal("groom-only mode launched")
	}
	t.Setenv("DEVKIT_GROOM_MODE", "run")
	for _, outcome := range []string{"attach", "defer", "drop"} {
		state.Proposals[0].Outcome, state.Proposals[0].Result.Outcome, state.Receipts[0].Outcome = outcome, outcome, outcome
		write()
		if _, err := Raise(s.req()); err == nil {
			t.Fatalf("%s launched", outcome)
		}
	}
	state.Proposals[0].Outcome, state.Proposals[0].Result.Outcome, state.Receipts[0].Outcome = "add", "add", "add"
	write()
	res, err := Raise(s.req())
	if err != nil || res.Code != CodeRaised {
		t.Fatalf("accepted add not raised: %+v %v", res, err)
	}
}
