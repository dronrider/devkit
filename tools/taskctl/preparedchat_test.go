package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dronrider/devkit/internal/chat"
	"github.com/dronrider/devkit/internal/draftproposal"
)

func TestPreparedChatReceiptRecoveryCannotApply(t *testing.T) {
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
	answer := preparedChatAnswer{Tree: root, Snapshots: req.Snapshots, Decisions: req.Decisions}
	input, _ := json.Marshal(answer)
	out, err := cmdPreparedChatAnswer(root, "XR-001", input, CommitOpts{})
	if err != nil || !strings.Contains(out, `"repeated":true`) {
		t.Fatalf("%s %v", out, err)
	}
	if out, err = cmdPreparedChatReplay(root, "XR-001", input); err != nil || !strings.Contains(out, `"repeated":true`) {
		t.Fatalf("receipt-only replay %s %v", out, err)
	}
	answer.Decisions[0].Reason = "другая"
	input, _ = json.Marshal(answer)
	if _, err = cmdPreparedChatAnswer(root, "XR-001", input, CommitOpts{}); err == nil {
		t.Fatal("new request accepted without Ask")
	}
	if _, err = cmdPreparedChatReplay(root, "XR-001", input); err == nil {
		t.Fatal("receipt-only accepted new request")
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(data) {
		t.Fatal("source changed", err)
	}
}

func TestPreparedChatShownOnly(t *testing.T) {
	q := chat.PreparedQuestion{Version: 1, Tree: "/tmp/project", Anchor: "XR-1", Sources: []chat.PreparedSource{{Source: "XR-1", Basis: "basis", Revision: 1, Proposals: []draftproposal.Proposal{{ID: "one", Revision: 3, Outcome: "drop"}}}}}
	req := draftproposal.ConfirmRequest{Snapshots: []draftproposal.Snapshot{{Source: "XR-1", Revision: 1, Digest: "basis"}}, Decisions: []draftproposal.Decision{{Source: "XR-1", Proposal: "one", Revision: 3, Action: "accept"}}}
	if err := chat.CheckPreparedAnswer(q, q.Tree, req); err != nil {
		t.Fatal(err)
	}
	req.Decisions[0].Proposal = "unseen"
	if chat.CheckPreparedAnswer(q, q.Tree, req) == nil {
		t.Fatal("unseen accepted")
	}
}

func TestPreparedChatApplyAndReplay(t *testing.T) {
	root := setup(t)
	kit, err := filepath.Abs("../../kit")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEVKIT_HOME", filepath.Dir(kit))
	if _, err = cmdDraft(root, "исходник", "mid", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	raw, err := cmdDraftPrepared(root, "XR-008")
	if err != nil {
		t.Fatal(err)
	}
	var view preparedView
	if err = json.Unmarshal([]byte(raw), &view); err != nil {
		t.Fatal(err)
	}
	raw, err = cmdDraftPreparedSave(root, "XR-008", view.Snapshot, []byte(`{"complete":true,"proposals":[{"id":"p1","revision":1,"outcome":"drop","text":"одно","reason":"лишнее"},{"id":"p2","revision":1,"outcome":"drop","text":"другое","reason":"лишнее"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal([]byte(raw), &view); err != nil {
		t.Fatal(err)
	}
	question, err := preparedChatQuestion(root, "XR-008", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(question.Text, "Подготовил задачи, согласуй их описание.") ||
		!strings.Contains(question.Text, "#"+filepath.Base(root)+"/draft/XR-008/prepared/"+assignedKey(view, "p1")) ||
		len(question.Options) != 1 || question.Options[0].Label != "Подтвердить разбор XR-008" ||
		question.Prepared.Sources[0].Revision != view.State.PackRevision || question.Prepared.Sources[0].Revision < 1 {
		t.Fatalf("вопрос без ссылок: %s %+v", question.Text, question.Options)
	}
	printed, err := cmdPreparedChatAsk(root, "XR-008", nil, askDeps{Main: root}, func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	var published chat.Question
	if err = json.Unmarshal([]byte(printed), &published); err != nil || published.Prepared == nil {
		t.Fatalf("printed question: %s %v", printed, err)
	}
	if strings.Contains(printed, "одно") || strings.Contains(printed, "другое") {
		t.Fatalf("тела задач уехали в вопрос: %s", printed)
	}
	ask, ok := chat.ReadAsk(chat.AskPath(root, chat.TaskName("XR-008")))
	if !ok || ask.Questions[0].Prepared == nil {
		t.Fatal(err)
	}
	answer := preparedChatAnswer{Tree: root, Snapshots: []draftproposal.Snapshot{{Source: "XR-008", Revision: view.State.PackRevision, Digest: view.Basis}}, Decisions: []draftproposal.Decision{{Source: "XR-008", Proposal: assignedKey(view, "p1"), Revision: 1, Action: "accept"}, {Source: "XR-008", Proposal: assignedKey(view, "p2"), Revision: 1, Action: "accept"}}}
	input, _ := json.Marshal(answer)
	forged := ask
	copyQ := *question.Prepared
	copyQ.Sources = append([]chat.PreparedSource(nil), copyQ.Sources...)
	copyQ.Sources[0].Proposals = append([]draftproposal.Proposal(nil), copyQ.Sources[0].Proposals...)
	copyQ.Sources[0].Proposals[0].Reason = "подмена"
	forged.Questions = []chat.Question{{Text: question.Text, Multi: true, Prepared: &copyQ}}
	if err = chat.WriteAsk(root, chat.TaskName("XR-008"), forged); err != nil {
		t.Fatal(err)
	}
	if _, err = cmdPreparedChatAnswer(root, "XR-008", input, CommitOpts{}); err == nil {
		t.Fatal("forged display accepted")
	}
	if err = chat.WriteAsk(root, chat.TaskName("XR-008"), ask); err != nil {
		t.Fatal(err)
	}
	out, err := cmdPreparedChatAnswer(root, "XR-008", input, CommitOpts{})
	if err != nil || !strings.Contains(out, `"repeated":false`) {
		t.Fatalf("apply %s %v", out, err)
	}
	out, err = cmdPreparedChatAnswer(root, "XR-008", input, CommitOpts{})
	if err != nil || !strings.Contains(out, `"repeated":true`) {
		t.Fatalf("replay with ask %s %v", out, err)
	}
	if err = chat.DropAsk(root, chat.TaskName("XR-008")); err != nil {
		t.Fatal(err)
	}
	out, err = cmdPreparedChatAnswer(root, "XR-008", input, CommitOpts{})
	if err != nil || !strings.Contains(out, `"repeated":true`) {
		t.Fatalf("replay without ask %s %v", out, err)
	}
	if err = chat.WriteAsk(root, chat.TaskName("XR-008"), chat.Ask{Task: "XR-008", Questions: []chat.Question{{Text: "другой вопрос"}}}); err != nil {
		t.Fatal(err)
	}
	out, err = cmdPreparedChatAnswer(root, "XR-008", input, CommitOpts{})
	if err != nil || !strings.Contains(out, `"repeated":true`) {
		t.Fatalf("replay after new question %s %v", out, err)
	}
	raw, err = cmdDraftPrepared(root, "XR-008")
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal([]byte(raw), &view); err != nil {
		t.Fatal(err)
	}
	if view.State.State != "history" || view.State.Proposals[0].Status != "accepted" || view.State.Proposals[1].Status != "accepted" || len(view.State.Receipts) != 3 {
		t.Fatalf("state %+v", view.State)
	}
}
