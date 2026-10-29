package draftproposal

import (
	"testing"

	"github.com/dronrider/devkit/internal/drafttypes"
	"github.com/dronrider/devkit/internal/team"
)

func TestAgreementHumanAndMilestoneDue(t *testing.T) {
	people := []team.Participant{{Handle: "human", Kind: team.Human, Roles: []string{team.RoleExec}}, {Handle: "agent", Kind: team.Agent, Roles: []string{team.RoleExec}}, {Handle: "reviewer", Kind: team.Human, Roles: []string{team.RoleReview}}}
	def := drafttypes.Definition{Name: "agreement"}
	for _, id := range []string{"", "agent", "reviewer", "missing"} {
		if err := Required(def, Proposal{ID: "p", Assignee: id}, people); err == nil {
			t.Errorf("accepted %q", id)
		}
	}
	if err := Required(def, Proposal{ID: "p", Assignee: "human"}, people); err != nil {
		t.Fatal(err)
	}
	milestone := drafttypes.Definition{Name: "milestone"}
	if err := Required(milestone, Proposal{ID: "p"}, people); err == nil {
		t.Fatal("no due accepted")
	}
	if err := Required(milestone, Proposal{ID: "p", Due: "2026-11-01"}, people); err != nil {
		t.Fatal(err)
	}
}
