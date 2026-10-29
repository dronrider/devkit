package main

import (
	"fmt"

	"github.com/dronrider/devkit/internal/draftmeta"
	"github.com/dronrider/devkit/internal/draftproposal"
	"github.com/dronrider/devkit/internal/drafttypes"
	"github.com/dronrider/devkit/internal/stage"
	"github.com/dronrider/devkit/internal/team"
)

// planProposals executes selected cards only inside the staging tree.
func (s *stagingBoard) planProposals(source string, header draftmeta.Header, def drafttypes.Definition, state draftproposal.State, selected []string) ([]draftproposal.Receipt, error) {
	board, err := LoadBoard(boardPath(s.root))
	if err != nil {
		return nil, err
	}
	archive, err := LoadArchive(archivePath(s.root))
	if err != nil {
		return nil, err
	}
	ordered, err := draftproposal.Order(state, selected, func(id string) bool { return board.find(id) != nil || archive.has(id) })
	if err != nil {
		return nil, err
	}
	members, err := team.Load(stage.Home())
	if err != nil {
		return nil, err
	}
	ids := map[string]string{}
	for _, p := range state.Proposals {
		if p.Status == "accepted" && p.Result != nil && len(p.Result.IDs) > 0 {
			ids[p.ID] = p.Result.IDs[0]
		}
	}
	var results []draftproposal.Receipt
	for _, p := range ordered {
		if p.Outcome == "add" {
			if err = draftproposal.Required(def, p, members); err != nil {
				return nil, err
			}
		}
		if p.Outcome != "add" && len(p.After) > 0 {
			return nil, fmt.Errorf("%s: связь после доступна строке", p.ID)
		}
		res, err := s.stageOutcome(source, header, p)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", p.ID, err)
		}
		if res.ID != "" && p.Outcome == "add" {
			ids[p.ID] = res.ID
		}
		for _, dep := range p.After {
			id := dep
			if mapped, ok := ids[dep]; ok {
				id = mapped
			}
			if _, err = cmdDepAdd(s.root, DepParams{ID: res.ID, DepID: id}); err != nil {
				return nil, fmt.Errorf("%s: %w", p.ID, err)
			}
		}
		receipt := draftproposal.Receipt{Proposal: p.ID, Revision: p.Revision, Outcome: p.Outcome, Result: res.Text, Reason: p.Reason}
		if res.ID != "" {
			receipt.IDs = []string{res.ID}
		}
		results = append(results, receipt)
	}
	return results, nil
}
