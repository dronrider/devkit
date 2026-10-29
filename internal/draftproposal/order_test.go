package draftproposal

import "testing"

func TestOrderRequiresVisiblePredecessorAndRejectsCycles(t *testing.T) {
	state := State{Version: 1, State: "active", Proposals: []Proposal{{ID: "a", Revision: 1, Outcome: "add"}, {ID: "b", Revision: 1, Outcome: "add", After: []string{"a", "XR-1"}}}}
	exists := func(id string) bool { return id == "XR-1" }
	if _, err := Order(state, []string{"b"}, exists); err == nil {
		t.Fatal("unselected predecessor")
	}
	order, err := Order(state, []string{"b", "a"}, exists)
	if err != nil || len(order) != 2 || order[0].ID != "a" {
		t.Fatalf("order %+v: %v", order, err)
	}
	state.Proposals[0].After = []string{"b"}
	if _, err = Order(state, []string{"a", "b"}, exists); err == nil {
		t.Fatal("cycle")
	}
	state.Proposals[0].After = nil
	state.Proposals[1].After = []string{"missing"}
	if _, err = Order(state, []string{"b"}, exists); err == nil {
		t.Fatal("unknown dependency")
	}
	state.Proposals[1].After = []string{"a"}
	state.Proposals[0].Status = "accepted"
	state.Proposals[0].Result = &Receipt{IDs: []string{"XR-2"}}
	if _, err = Order(state, []string{"b"}, exists); err != nil {
		t.Fatal(err)
	}
}
