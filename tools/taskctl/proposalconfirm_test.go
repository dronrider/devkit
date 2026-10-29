package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/dronrider/devkit/internal/draftproposal"
	"github.com/dronrider/devkit/internal/drafttx"
)

func TestPrepareConfirmationAllOutcomesAndReplay(t *testing.T) {
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
	cards := []draftproposal.Proposal{
		{ID: "p1", Revision: 1, Outcome: "add", Title: "Результат", Text: fixtureTaskBody, BoardType: "task", Rank: "0+1+1+0+1", Size: "M", Accept: "agent"},
		{ID: "p2", Revision: 1, Outcome: "attach", Target: "XR-001", Text: "приписка"},
		{ID: "p3", Revision: 1, Outcome: "defer", Text: "вернуться позже", Reason: "ждём ответа"},
		{ID: "p4", Revision: 1, Outcome: "drop", Text: "устарело", Reason: "не нужно"},
	}
	input, err := json.Marshal(struct {
		Proposals []draftproposal.Proposal `json:"proposals"`
	}{cards})
	if err != nil {
		t.Fatal(err)
	}
	raw, err = cmdDraftProposalsSave(root, "XR-008", view.Snapshot, input)
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal([]byte(raw), &view); err != nil {
		t.Fatal(err)
	}
	req := draftproposal.ConfirmRequest{Snapshots: []draftproposal.Snapshot{{Source: "XR-008", Digest: view.Basis}}}
	for _, p := range cards {
		req.Decisions = append(req.Decisions, draftproposal.Decision{Source: "XR-008", Proposal: p.ID, Revision: 1, Action: "accept"})
	}
	l, err := drafttx.Acquire(root)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := prepareConfirmation(root, req)
	if err != nil {
		l.Close()
		t.Fatal(err)
	}
	if len(plan.Results) != 4 || len(plan.Changes) < 3 || plan.Changes[len(plan.Changes)-1].Path != draftRel("XR-008") {
		l.Close()
		t.Fatalf("plan %+v", plan)
	}
	before, err := os.ReadFile(draftPath(root, "XR-008"))
	if err != nil {
		t.Fatal(err)
	}
	if len(before) == 0 {
		t.Fatal("source missing")
	}
	if err = l.Apply(root, plan.Changes); err != nil {
		l.Close()
		t.Fatal(err)
	}
	l.Close()
	l, err = drafttx.Acquire(root)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := prepareConfirmation(root, req)
	l.Close()
	if err != nil || !replay.Repeated || len(replay.Results) != 4 {
		t.Fatalf("replay %+v: %v", replay, err)
	}
	got, err := os.ReadFile(draftPath(root, "XR-008"))
	if err != nil {
		t.Fatal(err)
	}
	state, err := draftproposal.Parse(got)
	if err != nil || state.State != "history" {
		t.Fatalf("state %+v %v", state, err)
	}
	changed := req
	changed.Snapshots = append([]draftproposal.Snapshot(nil), req.Snapshots...)
	changed.Snapshots[0].Digest = "other"
	l, err = drafttx.Acquire(root)
	if err != nil {
		t.Fatal(err)
	}
	_, err = prepareConfirmation(root, changed)
	l.Close()
	if err == nil {
		t.Fatal("stale accepted")
	}
}

func TestConcurrentPreparedConfirmationReplays(t *testing.T) {
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
	json.Unmarshal([]byte(raw), &view)
	p := draftproposal.Proposal{ID: "p1", Revision: 1, Outcome: "add", Title: "Результат", Text: fixtureTaskBody, BoardType: "task", Rank: "0+1+1+0+1", Size: "M", Accept: "agent"}
	input, err := json.Marshal(struct {
		Proposals []draftproposal.Proposal `json:"proposals"`
	}{[]draftproposal.Proposal{p}})
	if err != nil {
		t.Fatal(err)
	}
	raw, err = cmdDraftProposalsSave(root, "XR-008", view.Snapshot, input)
	if err != nil {
		t.Fatal(err)
	}
	json.Unmarshal([]byte(raw), &view)
	req := draftproposal.ConfirmRequest{Snapshots: []draftproposal.Snapshot{{Source: "XR-008", Digest: view.Basis}}, Decisions: []draftproposal.Decision{{Source: "XR-008", Proposal: "p1", Revision: 1, Action: "accept"}}}
	type answer struct {
		plan confirmationPlan
		err  error
	}
	answers := make(chan answer, 2)
	for i := 0; i < 2; i++ {
		go func() {
			lock, err := drafttx.Acquire(root)
			if err != nil {
				answers <- answer{err: err}
				return
			}
			defer lock.Close()
			plan, err := prepareConfirmation(root, req)
			if err == nil && !plan.Repeated {
				err = lock.Apply(root, plan.Changes)
			}
			answers <- answer{plan: plan, err: err}
		}()
	}
	a, b := <-answers, <-answers
	if a.err != nil || b.err != nil {
		t.Fatalf("errors %v %v", a.err, b.err)
	}
	if a.plan.Repeated == b.plan.Repeated || len(a.plan.Results) != 1 || len(b.plan.Results) != 1 || a.plan.Results[0].IDs[0] != b.plan.Results[0].IDs[0] {
		t.Fatalf("results %+v %+v", a.plan, b.plan)
	}
}

func TestPartialConfirmationCannotRepeatInDifferentBatch(t *testing.T) {
	root := setup(t)
	kit, err := filepath.Abs("../../kit")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEVKIT_HOME", filepath.Dir(kit))
	if _, err = cmdDraft(root, "материал", "mid", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	raw, err := cmdDraftProposals(root, "XR-008")
	if err != nil {
		t.Fatal(err)
	}
	var view proposalView
	json.Unmarshal([]byte(raw), &view)
	input := []byte(`{"proposals":[{"id":"p1","revision":1,"outcome":"drop","text":"убрать","reason":"устарело"},{"id":"p2","revision":1,"outcome":"drop","text":"другое","reason":"лишнее"}]}`)
	raw, err = cmdDraftProposalsSave(root, "XR-008", view.Snapshot, input)
	if err != nil {
		t.Fatal(err)
	}
	json.Unmarshal([]byte(raw), &view)
	first := draftproposal.ConfirmRequest{Snapshots: []draftproposal.Snapshot{{Source: "XR-008", Digest: view.Basis}}, Decisions: []draftproposal.Decision{{Source: "XR-008", Proposal: "p1", Revision: 1, Action: "accept"}}}
	l, err := drafttx.Acquire(root)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := prepareConfirmation(root, first)
	if err == nil {
		err = l.Apply(root, plan.Changes)
	}
	l.Close()
	if err != nil {
		t.Fatal(err)
	}
	raw, err = cmdDraftProposals(root, "XR-008")
	if err != nil {
		t.Fatal(err)
	}
	json.Unmarshal([]byte(raw), &view)
	if view.State.State != "active" || view.State.Proposals[0].Status != "accepted" || view.State.Proposals[1].Status != "" {
		t.Fatalf("partial: %s", raw)
	}
	different := draftproposal.ConfirmRequest{Snapshots: []draftproposal.Snapshot{{Source: "XR-008", Digest: view.Basis}}, Decisions: []draftproposal.Decision{{Source: "XR-008", Proposal: "p1", Revision: 1, Action: "accept"}, {Source: "XR-008", Proposal: "p2", Revision: 1, Action: "accept"}}}
	l, err = drafttx.Acquire(root)
	if err != nil {
		t.Fatal(err)
	}
	_, err = prepareConfirmation(root, different)
	l.Close()
	if err == nil {
		t.Fatal("accepted card ran in a different batch")
	}
}

func TestCLIConfirmPublicationFailureAndRetry(t *testing.T) {
	root := setup(t)
	gitSetup(t, root)
	kit, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEVKIT_HOME", kit)
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
	payload, _ := json.Marshal(struct {
		Proposals []draftproposal.Proposal `json:"proposals"`
	}{[]draftproposal.Proposal{{ID: "p1", Revision: 1, Outcome: "drop", Text: "материал", Reason: "устарело"}}})
	raw, err = cmdDraftProposalsSave(root, "XR-008", view.Snapshot, payload)
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal([]byte(raw), &view); err != nil {
		t.Fatal(err)
	}
	req, _ := json.Marshal(draftproposal.ConfirmRequest{Snapshots: []draftproposal.Snapshot{{Source: "XR-008", Digest: view.Basis}}, Decisions: []draftproposal.Decision{{Source: "XR-008", Proposal: "p1", Revision: 1, Action: "accept"}}})
	writeHook(t, root, "pre-commit", "#!/bin/sh\nexit 1\n")
	run := func(args ...string) (string, error) {
		t.Helper()
		cmd := exec.Command("go", append([]string{"run", ".", "-C", root, "draft", "proposals", "confirm"}, args...)...)
		cmd.Stdin = bytes.NewReader(req)
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	out, err := run("-m", "docs(tasks): XR подтверждение")
	if err == nil || !strings.Contains(out, "применено, публикация не завершена") {
		t.Fatalf("publication: %v %s", err, out)
	}
	out, err = run()
	if err != nil || !strings.Contains(out, `"repeated":true`) {
		t.Fatalf("retry: %v %s", err, out)
	}
	data, err := os.ReadFile(draftPath(root, "XR-008"))
	if err != nil {
		t.Fatal(err)
	}
	state, err := draftproposal.Parse(data)
	if err != nil || state.State != "history" || len(state.Receipts) != 1 {
		t.Fatalf("state %+v %v", state, err)
	}
}

func TestConfirmLockedAttachRejectsCloudWithoutWrites(t *testing.T) {
	root := setup(t)
	kit, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEVKIT_HOME", kit)
	if _, err = cmdDraft(root, "частный источник", "mid", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	if _, err = cmdDraftMeta(root, "XR-008", "", "", "locked", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	raw, err := cmdDraftProposals(root, "XR-008")
	if err != nil {
		t.Fatal(err)
	}
	var v proposalView
	if err = json.Unmarshal([]byte(raw), &v); err != nil {
		t.Fatal(err)
	}
	card := draftproposal.Proposal{ID: "p1", Revision: 1, Outcome: "attach", Target: "XR-001", Text: "частный фрагмент"}
	in, _ := json.Marshal(struct {
		Proposals []draftproposal.Proposal `json:"proposals"`
	}{[]draftproposal.Proposal{card}})
	raw, err = cmdDraftProposalsSave(root, "XR-008", v.Snapshot, in)
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal([]byte(raw), &v); err != nil {
		t.Fatal(err)
	}
	request, _ := json.Marshal(draftproposal.ConfirmRequest{Snapshots: []draftproposal.Snapshot{{Source: "XR-008", Digest: v.Basis}}, Decisions: []draftproposal.Decision{{Source: "XR-008", Proposal: "p1", Revision: 1, Action: "accept"}}})
	source, target := draftPath(root, "XR-008"), taskFileAbs(root, "XR-001")
	a, _ := os.ReadFile(source)
	b, _ := os.ReadFile(target)
	cloud, err := testCloudShared(root, "XR-001")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = cmdDraftProposalsConfirm(root, request, CommitOpts{}); err == nil {
		cloud.Close()
		t.Fatal("cloud active")
	}
	aa, _ := os.ReadFile(source)
	bb, _ := os.ReadFile(target)
	if !bytes.Equal(a, aa) || !bytes.Equal(b, bb) {
		cloud.Close()
		t.Fatal("partial write")
	}
	cloud.Close()
	if _, err = cmdDraftProposalsConfirm(root, request, CommitOpts{}); err != nil {
		t.Fatal(err)
	}
}

func TestProposalHistoryNewAnalysisAndOldRetry(t *testing.T) {
	root := setup(t)
	kit, _ := filepath.Abs("../..")
	t.Setenv("DEVKIT_HOME", kit)
	if _, err := cmdDraft(root, "исходник", "mid", CommitOpts{}); err != nil {
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
	payload, _ := json.Marshal(map[string]any{"proposals": []draftproposal.Proposal{{ID: "p1", Revision: 1, Outcome: "add", Title: "Первый результат", Text: fixtureTaskBody, BoardType: "task", Rank: "0+1+1+0+1", Size: "M", Accept: "agent"}}})
	raw, err = cmdDraftProposalsSave(root, "XR-008", view.Snapshot, payload)
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal([]byte(raw), &view); err != nil {
		t.Fatal(err)
	}
	req := draftproposal.ConfirmRequest{Snapshots: []draftproposal.Snapshot{{Source: "XR-008", Digest: view.Basis}}, Decisions: []draftproposal.Decision{{Source: "XR-008", Proposal: "p1", Revision: 1, Action: "accept"}}}
	input, _ := json.Marshal(req)
	first, err := cmdDraftProposalsConfirm(root, input, CommitOpts{})
	if err != nil {
		t.Fatal(err)
	}
	raw, err = cmdDraftProposals(root, "XR-008")
	if err != nil {
		t.Fatal(err)
	}
	json.Unmarshal([]byte(raw), &view)
	if view.State.State != "history" || len(view.State.Receipts) != 1 || len(view.State.Receipts[0].IDs) == 0 {
		t.Fatalf("state: %+v", view.State)
	}
	old := view.State
	raw, err = cmdDraftProposalsSave(root, "XR-008", view.Snapshot, []byte(`{"proposals":[{"id":"p2","revision":1,"outcome":"drop","text":"второй","reason":"устарело"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	json.Unmarshal([]byte(raw), &view)
	if view.State.State != "active" || len(view.State.Proposals) != 2 || !reflect.DeepEqual(view.State.Proposals[0], old.Proposals[0]) || !reflect.DeepEqual(view.State.Receipts, old.Receipts) {
		t.Fatalf("new state: %+v", view.State)
	}
	before, _ := os.ReadFile(draftPath(root, "XR-008"))
	repeat, err := cmdDraftProposalsConfirm(root, input, CommitOpts{})
	if err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(draftPath(root, "XR-008"))
	var a, b confirmationResult
	json.Unmarshal([]byte(first), &a)
	json.Unmarshal([]byte(repeat), &b)
	if !b.Repeated || !reflect.DeepEqual(a.Results, b.Results) || !bytes.Equal(before, after) {
		t.Fatalf("retry: %s", repeat)
	}
	next, _ := json.Marshal(draftproposal.ConfirmRequest{Snapshots: []draftproposal.Snapshot{{Source: "XR-008", Digest: view.Basis}}, Decisions: []draftproposal.Decision{{Source: "XR-008", Proposal: "p2", Revision: 1, Action: "accept"}}})
	if _, err = cmdDraftProposalsConfirm(root, next, CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	raw, err = cmdDraftProposals(root, "XR-008")
	if err != nil {
		t.Fatal(err)
	}
	json.Unmarshal([]byte(raw), &view)
	if view.State.State != "history" || len(view.State.Receipts) != 2 {
		t.Fatalf("final: %+v", view.State)
	}
}

func TestProposalConfirmLinkedWorktreeRejectsBoardMutation(t *testing.T) {
	root := setup(t)
	kit, _ := filepath.Abs("../..")
	t.Setenv("DEVKIT_HOME", kit)
	if _, err := cmdDraft(root, "исходник", "mid", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root, "-c", "user.name=Test", "-c", "user.email=test@example.invalid"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git: %s %v", out, err)
		}
	}
	git("init")
	git("add", "docs")
	git("commit", "-m", "fixture")
	tree := filepath.Join(t.TempDir(), "worktree")
	git("worktree", "add", "-b", "test", tree)
	raw, err := cmdDraftProposals(tree, "XR-008")
	if err != nil {
		t.Fatal(err)
	}
	var view proposalView
	json.Unmarshal([]byte(raw), &view)
	payload, _ := json.Marshal(map[string]any{"proposals": []draftproposal.Proposal{{ID: "p1", Revision: 1, Outcome: "add", Title: "Результат", Text: fixtureTaskBody, BoardType: "task", Rank: "0+1+1+0+1", Size: "M", Accept: "agent"}}})
	raw, err = cmdDraftProposalsSave(tree, "XR-008", view.Snapshot, payload)
	if err != nil {
		t.Fatal(err)
	}
	json.Unmarshal([]byte(raw), &view)
	input, _ := json.Marshal(draftproposal.ConfirmRequest{Snapshots: []draftproposal.Snapshot{{Source: "XR-008", Digest: view.Basis}}, Decisions: []draftproposal.Decision{{Source: "XR-008", Proposal: "p1", Revision: 1, Action: "accept"}}})
	source, _ := os.ReadFile(draftPath(tree, "XR-008"))
	board, _ := os.ReadFile(boardPath(tree))
	mainBoard, _ := os.ReadFile(boardPath(root))
	if _, err = cmdDraftProposalsConfirm(tree, input, CommitOpts{}); err == nil || !strings.Contains(err.Error(), "линкованный worktree") {
		t.Fatalf("expected refusal: %v", err)
	}
	sourceAfter, _ := os.ReadFile(draftPath(tree, "XR-008"))
	boardAfter, _ := os.ReadFile(boardPath(tree))
	mainAfter, _ := os.ReadFile(boardPath(root))
	if !bytes.Equal(source, sourceAfter) || !bytes.Equal(board, boardAfter) || !bytes.Equal(mainBoard, mainAfter) {
		t.Fatal("refusal changed files")
	}
}
