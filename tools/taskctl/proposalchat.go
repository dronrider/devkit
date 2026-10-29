package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"github.com/dronrider/devkit/internal/chat"
	"github.com/dronrider/devkit/internal/draftproposal"
	"github.com/dronrider/devkit/internal/drafttx"
	"github.com/dronrider/devkit/internal/stage"
)

type proposalChatAnswer struct {
	Tree      string                   `json:"tree"`
	Snapshots []draftproposal.Snapshot `json:"snapshots"`
	Decisions []draftproposal.Decision `json:"decisions"`
}

func proposalChatQuestion(root, anchor string, also []string) (chat.Question, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return chat.Question{}, err
	}
	root = filepath.Clean(root)
	ids := append([]string{anchor}, also...)
	shown := chat.ProposalQuestion{Version: 1, Tree: root, Anchor: anchor}
	seen := map[string]bool{}
	for _, id := range ids {
		if !idRe.MatchString(id) || seen[id] {
			return chat.Question{}, fmt.Errorf("неверный или повторный ID %s", id)
		}
		seen[id] = true
		raw, err := cmdDraftProposals(root, id)
		if err != nil {
			return chat.Question{}, err
		}
		var view proposalView
		if err := json.Unmarshal([]byte(raw), &view); err != nil {
			return chat.Question{}, err
		}
		s := chat.ProposalSource{Source: id, Basis: view.Basis}
		for _, p := range view.State.Proposals {
			if p.Status == "" {
				s.Proposals = append(s.Proposals, p)
			}
		}
		if len(s.Proposals) == 0 {
			return chat.Question{}, fmt.Errorf("%s: нет нерешённых предложений", id)
		}
		shown.Sources = append(shown.Sources, s)
	}
	if err := shown.Validate(); err != nil {
		return chat.Question{}, err
	}
	return chat.Question{Text: "Какие предложения принять или отклонить?", Multi: true, Proposal: &shown}, nil
}

func cmdProposalChatAsk(root, anchor string, also []string, d askDeps, env func(string) string) (string, error) {
	q, err := proposalChatQuestion(root, anchor, also)
	if err != nil {
		return "", err
	}
	if _, err = runAsk(root, AskParams{ID: anchor, Draft: true, Pack: []chat.Question{q}, Quiet: true}, d, env); err != nil {
		return "", err
	}
	data, err := json.Marshal(q)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func parseProposalChatAnswer(input []byte) (proposalChatAnswer, error) {
	var a proposalChatAnswer
	dec := json.NewDecoder(bytes.NewReader(input))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&a); err != nil {
		return a, err
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return a, fmt.Errorf("лишние данные ответа")
	}
	return a, nil
}

func cmdProposalChatAnswer(root, anchor string, input []byte, opts CommitOpts) (string, error) {
	a, err := parseProposalChatAnswer(input)
	if err != nil {
		return "", err
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return "", err
	}
	root = filepath.Clean(root)
	if !idRe.MatchString(anchor) || a.Tree != root {
		return "", fmt.Errorf("неверное дерево или адрес вопроса")
	}
	req := draftproposal.ConfirmRequest{Snapshots: a.Snapshots, Decisions: a.Decisions}
	ask, ok := chat.ReadAsk(chat.AskPath(stage.MainRoot(root), chat.TaskName(anchor)))
	if ok {
		var shown *chat.ProposalQuestion
		for _, q := range ask.Questions {
			if q.Proposal != nil {
				if shown != nil || q.Scenario != nil {
					return "", fmt.Errorf("неверный вопрос")
				}
				shown = q.Proposal
			}
		}
		if ask.Task != anchor || shown == nil || shown.Anchor != anchor {
			return proposalChatRepeated(root, req)
		}
		if err := chat.CheckProposalAnswer(*shown, root, req); err != nil {
			if repeated, retryErr := proposalChatRepeated(root, req); retryErr == nil {
				return repeated, nil
			}
			return "", err
		}
		// A generic Ask may contain a forged card. Verify the displayed fields
		// against the saved source without replacing the displayed snapshot.
		if repeated, err := proposalChatRepeated(root, req); err == nil {
			return repeated, nil
		}
		for _, source := range shown.Sources {
			raw, err := cmdDraftProposals(root, source.Source)
			if err != nil {
				return "", err
			}
			var view proposalView
			if err = json.Unmarshal([]byte(raw), &view); err != nil {
				return "", err
			}
			var cards []draftproposal.Proposal
			for _, p := range view.State.Proposals {
				if p.Status == "" {
					cards = append(cards, p)
				}
			}
			if view.Basis != source.Basis || !reflect.DeepEqual(cards, source.Proposals) {
				return "", fmt.Errorf("%s: показанные предложения изменились", source.Source)
			}
		}
		data, err := json.Marshal(req)
		if err != nil {
			return "", err
		}
		return cmdDraftProposalsConfirm(root, data, opts)
	}
	return proposalChatRepeated(root, req)
}

func cmdProposalChatReplay(root, anchor string, input []byte) (string, error) {
	a, err := parseProposalChatAnswer(input)
	if err != nil {
		return "", err
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return "", err
	}
	root = filepath.Clean(root)
	if !idRe.MatchString(anchor) || a.Tree != root {
		return "", fmt.Errorf("неверное дерево или адрес вопроса")
	}
	return proposalChatRepeated(root, draftproposal.ConfirmRequest{Snapshots: a.Snapshots, Decisions: a.Decisions})
}

// A missing Ask permits receipt recovery only; it never invokes the writer.
func proposalChatRepeated(root string, req draftproposal.ConfirmRequest) (string, error) {
	if len(req.Decisions) == 0 || len(req.Snapshots) == 0 {
		return "", fmt.Errorf("показанный вопрос не найден")
	}
	sort.Slice(req.Snapshots, func(i, j int) bool { return req.Snapshots[i].Source < req.Snapshots[j].Source })
	sort.Slice(req.Decisions, func(i, j int) bool {
		a, b := req.Decisions[i], req.Decisions[j]
		if a.Source != b.Source {
			return a.Source < b.Source
		}
		return a.Proposal < b.Proposal
	})
	snaps := map[string]bool{}
	used := map[string]bool{}
	seen := map[string]bool{}
	for _, s := range req.Snapshots {
		if !idRe.MatchString(s.Source) || s.Digest == "" || snaps[s.Source] {
			return "", fmt.Errorf("неверный снимок")
		}
		snaps[s.Source] = true
	}
	for _, d := range req.Decisions {
		key := d.Source + "\x00" + d.Proposal
		if !snaps[d.Source] || d.Proposal == "" || d.Revision < 1 || seen[key] || (d.Action != "accept" && d.Action != "reject") || (d.Action == "reject" && strings.TrimSpace(d.Reason) == "") {
			return "", fmt.Errorf("неверное решение")
		}
		seen[key] = true
		used[d.Source] = true
	}
	if len(used) != len(snaps) {
		return "", fmt.Errorf("лишний снимок")
	}
	b, err := json.Marshal(req)
	if err != nil {
		return "", err
	}
	key := draftproposal.Digest(b)
	lock, err := drafttx.Acquire(root)
	if err != nil {
		return "", err
	}
	defer lock.Close()
	var results []draftproposal.Receipt
	for _, d := range req.Decisions {
		data, err := os.ReadFile(draftPath(root, d.Source))
		if err != nil {
			return "", err
		}
		state, err := draftproposal.Parse(data)
		if err != nil {
			return "", err
		}
		found := false
		for _, receipt := range state.Receipts {
			if receipt.Key == key && receipt.Source == d.Source && receipt.Proposal == d.Proposal && receipt.Revision == d.Revision {
				results = append(results, receipt)
				found = true
				break
			}
		}
		if !found {
			return "", fmt.Errorf("показанный вопрос не найден, повтор не подтверждён")
		}
	}
	sortReceipts(results)
	return marshal(confirmationResult{Key: key, Results: results, Repeated: true})
}
