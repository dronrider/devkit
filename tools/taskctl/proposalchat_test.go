package main

import (
	"encoding/json"
	"github.com/dronrider/devkit/internal/chat"
	"github.com/dronrider/devkit/internal/draftproposal"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProposalChatReceiptRecoveryCannotApply(t *testing.T) {
	root := t.TempDir()
	path := draftPath(root, "XR-001")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	req := draftproposal.ConfirmRequest{Snapshots: []draftproposal.Snapshot{{Source: "XR-001", Digest: "shown"}}, Decisions: []draftproposal.Decision{{Source: "XR-001", Proposal: "p1", Revision: 1, Action: "reject", Reason: "не нужно"}}}
	b, _ := json.Marshal(req)
	key := draftproposal.Digest(b)
	receipt := draftproposal.Receipt{Key: key, Source: "XR-001", Proposal: "p1", Revision: 1, Outcome: "drop", Result: "отклонено", Reason: "не нужно"}
	state := draftproposal.State{Version: 1, State: "history", Proposals: []draftproposal.Proposal{{ID: "p1", Revision: 1, Outcome: "drop", Status: "rejected", Result: &receipt}}, Receipts: []draftproposal.Receipt{receipt}}
	data, err := draftproposal.Replace([]byte("# source\n"), state)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
	answer := proposalChatAnswer{Tree: root, Snapshots: req.Snapshots, Decisions: req.Decisions}
	input, _ := json.Marshal(answer)
	out, err := cmdProposalChatAnswer(root, "XR-001", input, CommitOpts{})
	if err != nil || !strings.Contains(out, `"repeated":true`) {
		t.Fatalf("%s %v", out, err)
	}
	answer.Decisions[0].Reason = "другая"
	input, _ = json.Marshal(answer)
	if _, err = cmdProposalChatAnswer(root, "XR-001", input, CommitOpts{}); err == nil {
		t.Fatal("new request accepted without Ask")
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(data) {
		t.Fatal("source changed", err)
	}
}

func TestProposalChatShownOnly(t *testing.T) {
	q := chat.ProposalQuestion{Version: 1, Tree: "/tmp/project", Anchor: "XR-1", Sources: []chat.ProposalSource{{Source: "XR-1", Basis: "basis", Proposals: []draftproposal.Proposal{{ID: "one", Revision: 3, Outcome: "drop"}}}}}
	req := draftproposal.ConfirmRequest{Snapshots: []draftproposal.Snapshot{{Source: "XR-1", Digest: "basis"}}, Decisions: []draftproposal.Decision{{Source: "XR-1", Proposal: "one", Revision: 3, Action: "reject", Reason: "лишнее"}}}
	if err := chat.CheckProposalAnswer(q, q.Tree, req); err != nil {
		t.Fatal(err)
	}
	req.Decisions[0].Proposal = "unseen"
	if chat.CheckProposalAnswer(q, q.Tree, req) == nil {
		t.Fatal("unseen accepted")
	}
}

func TestProposalChatApplyAndReplay(t *testing.T) {
	root := setup(t)
	kit, err := filepath.Abs("../../kit")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEVKIT_HOME", filepath.Dir(kit))
	if _, err = cmdDraft(root, "исходник", "mid", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	raw, err := cmdDraftProposals(root, "XR-008")
	if err != nil {
		t.Fatal(err)
	}
	var view proposalView
	if err = json.Unmarshal([]byte(raw), &view); err != nil {
		t.Fatal(err)
	}
	raw, err = cmdDraftProposalsSave(root, "XR-008", view.Snapshot, []byte(`{"proposals":[{"id":"p1","revision":1,"outcome":"drop","text":"одно","reason":"лишнее"},{"id":"p2","revision":1,"outcome":"drop","text":"другое","reason":"лишнее"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal([]byte(raw), &view); err != nil {
		t.Fatal(err)
	}
	question, err := proposalChatQuestion(root, "XR-008", nil)
	if err != nil {
		t.Fatal(err)
	}
	printed, err := cmdProposalChatAsk(root, "XR-008", nil, askDeps{Main: root}, func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	var published chat.Question
	if err = json.Unmarshal([]byte(printed), &published); err != nil || published.Proposal == nil {
		t.Fatalf("printed question: %s %v", printed, err)
	}
	ask, ok := chat.ReadAsk(chat.AskPath(root, chat.TaskName("XR-008")))
	if !ok || ask.Questions[0].Proposal == nil {
		t.Fatal(err)
	}
	answer := proposalChatAnswer{Tree: root, Snapshots: []draftproposal.Snapshot{{Source: "XR-008", Digest: view.Basis}}, Decisions: []draftproposal.Decision{{Source: "XR-008", Proposal: "p1", Revision: 1, Action: "reject", Reason: "не подходит"}}}
	input, _ := json.Marshal(answer)
	forged := ask
	copyQ := *question.Proposal
	copyQ.Sources = append([]chat.ProposalSource(nil), copyQ.Sources...)
	copyQ.Sources[0].Proposals = append([]draftproposal.Proposal(nil), copyQ.Sources[0].Proposals...)
	copyQ.Sources[0].Proposals[0].Text = "подмена"
	forged.Questions = []chat.Question{{Text: question.Text, Proposal: &copyQ}}
	if err = chat.WriteAsk(root, chat.TaskName("XR-008"), forged); err != nil {
		t.Fatal(err)
	}
	if _, err = cmdProposalChatAnswer(root, "XR-008", input, CommitOpts{}); err == nil {
		t.Fatal("forged display accepted")
	}
	if err = chat.WriteAsk(root, chat.TaskName("XR-008"), ask); err != nil {
		t.Fatal(err)
	}
	out, err := cmdProposalChatAnswer(root, "XR-008", input, CommitOpts{})
	if err != nil || !strings.Contains(out, `"repeated":false`) {
		t.Fatalf("apply %s %v", out, err)
	}
	out, err = cmdProposalChatAnswer(root, "XR-008", input, CommitOpts{})
	if err != nil || !strings.Contains(out, `"repeated":true`) {
		t.Fatalf("replay with ask %s %v", out, err)
	}
	if err = chat.DropAsk(root, chat.TaskName("XR-008")); err != nil {
		t.Fatal(err)
	}
	out, err = cmdProposalChatAnswer(root, "XR-008", input, CommitOpts{})
	if err != nil || !strings.Contains(out, `"repeated":true`) {
		t.Fatalf("replay without ask %s %v", out, err)
	}
	if err = chat.WriteAsk(root, chat.TaskName("XR-008"), chat.Ask{Task: "XR-008", Questions: []chat.Question{{Text: "другой вопрос"}}}); err != nil {
		t.Fatal(err)
	}
	out, err = cmdProposalChatAnswer(root, "XR-008", input, CommitOpts{})
	if err != nil || !strings.Contains(out, `"repeated":true`) {
		t.Fatalf("replay after new question %s %v", out, err)
	}
	raw, err = cmdDraftProposals(root, "XR-008")
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal([]byte(raw), &view); err != nil {
		t.Fatal(err)
	}
	if view.State.Proposals[0].Status != "rejected" || view.State.Proposals[1].Status != "" || len(view.State.Receipts) != 1 {
		t.Fatalf("state %+v", view.State)
	}
}
