package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"reflect"
	"strings"

	"github.com/dronrider/devkit/internal/draftproposal"
	"github.com/dronrider/devkit/internal/drafttx"
)

type proposalView struct {
	ID       string              `json:"id"`
	Snapshot string              `json:"snapshot"`
	Basis    string              `json:"basis"`
	State    draftproposal.State `json:"state"`
}

func cmdDraftProposals(root, id string) (string, error) {
	l, err := drafttx.Acquire(root)
	if err != nil {
		return "", err
	}
	defer l.Close()
	drafts, err := loadDraftsWithHistory(root, true)
	if err != nil {
		return "", err
	}
	d := findDraft(drafts, id)
	if d == nil {
		return "", fmt.Errorf("%s: черновик не найден", id)
	}
	data, err := os.ReadFile(d.Path)
	if err != nil {
		return "", err
	}
	state, err := draftproposal.Parse(data)
	if err != nil {
		return "", err
	}
	basis, err := proposalBasis(root, data, state)
	if err != nil {
		return "", err
	}
	return marshal(proposalView{ID: id, Snapshot: draftproposal.Digest(data), Basis: basis, State: state})
}

func cmdDraftProposalsSave(root, id, base string, input []byte) (string, error) {
	l, err := drafttx.Acquire(root)
	if err != nil {
		return "", err
	}
	defer l.Close()
	d, err := findDraftFor(root, id)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(d.Path)
	if err != nil {
		return "", err
	}
	if base == "" || base != draftproposal.Digest(data) {
		return "", fmt.Errorf("%s: запись изменилась, откройте её снова", id)
	}
	old, err := draftproposal.Parse(data)
	if err != nil {
		return "", err
	}
	var incoming struct {
		Proposals []draftproposal.Proposal `json:"proposals"`
		Analysis  *string                  `json:"analysis"`
	}
	dec := json.NewDecoder(strings.NewReader(string(input)))
	dec.DisallowUnknownFields()
	if err = dec.Decode(&incoming); err != nil {
		return "", err
	}
	var extra any
	if err = dec.Decode(&extra); err != io.EOF {
		return "", fmt.Errorf("лишние данные после JSON")
	}
	// A new analysis adds cards; existing cards are preserved. Undecided cards
	// can be edited only by supplying their next revision, decided cards cannot.
	index := map[string]int{}
	for i, p := range old.Proposals {
		index[p.ID] = i
	}
	seen := map[string]bool{}
	for _, p := range incoming.Proposals {
		if seen[p.ID] {
			return "", fmt.Errorf("повтор предложения %s", p.ID)
		}
		seen[p.ID] = true
		if p.Status != "" || p.Result != nil {
			return "", fmt.Errorf("%s: решение нельзя задать при записи разбора", p.ID)
		}
		if i, ok := index[p.ID]; ok {
			previous := old.Proposals[i]
			if previous.Status != "" {
				return "", fmt.Errorf("%s: решённое предложение изменить нельзя", p.ID)
			}
			if p.Revision != previous.Revision+1 || reflect.DeepEqual(p, previous) {
				return "", fmt.Errorf("%s: нужна следующая редакция", p.ID)
			}
			old.Proposals[i] = p
		} else {
			if p.Revision != 1 {
				return "", fmt.Errorf("%s: первая редакция должна быть 1", p.ID)
			}
			index[p.ID] = len(old.Proposals)
			old.Proposals = append(old.Proposals, p)
		}
	}
	if incoming.Analysis != nil {
		old.Analysis = *incoming.Analysis
	}
	out, err := draftproposal.Replace(data, old)
	if err != nil {
		return "", err
	}
	before, err := drafttx.Read(d.Path)
	if err != nil {
		return "", err
	}
	if !bytes.Equal(before.Data, data) {
		return "", fmt.Errorf("%s: запись изменилась, откройте её снова", id)
	}
	basis, err := proposalBasis(root, out, old)
	if err != nil {
		return "", err
	}
	if err = l.Apply(root, []drafttx.Change{{Path: draftRel(id), Before: before, After: drafttx.Image{Exists: true, Data: out, Mode: before.Mode}}}); err != nil {
		return "", err
	}
	return marshal(proposalView{ID: id, Snapshot: draftproposal.Digest(out), Basis: basis, State: old})
}

func cmdDraftProposalReject(root, id, proposal, base, reason string) (string, error) {
	if strings.TrimSpace(reason) == "" {
		return "", fmt.Errorf("укажите причину отказа")
	}
	l, err := drafttx.Acquire(root)
	if err != nil {
		return "", err
	}
	defer l.Close()
	d, err := findDraftFor(root, id)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(d.Path)
	if err != nil {
		return "", err
	}
	if base == "" || base != draftproposal.Digest(data) {
		return "", fmt.Errorf("%s: запись изменилась, откройте её снова", id)
	}
	state, err := draftproposal.Parse(data)
	if err != nil {
		return "", err
	}
	found := false
	allDone := len(state.Proposals) > 0
	for i := range state.Proposals {
		p := &state.Proposals[i]
		if p.ID == proposal {
			if p.Status != "" {
				return "", fmt.Errorf("%s: решение уже принято", p.ID)
			}
			p.Status = "rejected"
			p.Result = &draftproposal.Receipt{Proposal: p.ID, Revision: p.Revision, Outcome: p.Outcome, Result: "отклонено", Reason: strings.TrimSpace(reason)}
			found = true
		}
		if p.Status == "" {
			allDone = false
		}
	}
	if !found {
		return "", fmt.Errorf("предложение %s не найдено", proposal)
	}
	if allDone {
		state.State = "history"
	}
	out, err := draftproposal.Replace(data, state)
	if err != nil {
		return "", err
	}
	before, err := drafttx.Read(d.Path)
	if err != nil {
		return "", err
	}
	if !bytes.Equal(before.Data, data) {
		return "", fmt.Errorf("%s: запись изменилась", id)
	}
	basis, err := proposalBasis(root, out, state)
	if err != nil {
		return "", err
	}
	if err = l.Apply(root, []drafttx.Change{{Path: draftRel(id), Before: before, After: drafttx.Image{Exists: true, Data: out, Mode: before.Mode}}}); err != nil {
		return "", err
	}
	return marshal(proposalView{ID: id, Snapshot: draftproposal.Digest(out), Basis: basis, State: state})
}
