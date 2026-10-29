package main

import (
	"encoding/json"
	"fmt"

	"github.com/dronrider/devkit/internal/chat"
	"github.com/dronrider/devkit/internal/draftproposal"
)

type proposalAnswer struct {
	Anchor    string                   `json:"anchor"`
	Tree      string                   `json:"tree"`
	Snapshots []draftproposal.Snapshot `json:"snapshots"`
	Decisions []draftproposal.Decision `json:"decisions"`
}

func proposalAnswerInput(a proposalAnswer) ([]byte, error) {
	if a.Anchor == "" || a.Tree == "" || len(a.Snapshots) == 0 || len(a.Decisions) == 0 {
		return nil, fmt.Errorf("нужен контекст показанных предложений")
	}
	return json.Marshal(struct {
		Tree      string                   `json:"tree"`
		Snapshots []draftproposal.Snapshot `json:"snapshots"`
		Decisions []draftproposal.Decision `json:"decisions"`
	}{a.Tree, a.Snapshots, a.Decisions})
}

func proposalFromAsk(a chat.Ask) *chat.ProposalQuestion {
	var found *chat.ProposalQuestion
	for _, q := range a.Questions {
		if q.Proposal == nil {
			continue
		}
		if found != nil || q.Scenario != nil || q.Proposal.Validate() != nil || q.Proposal.Anchor != a.Task {
			return nil
		}
		found = q.Proposal
	}
	return found
}
