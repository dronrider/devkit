package main

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestProposalsSaveAndStale(t *testing.T) {
	root := setup(t)
	kit, err := filepath.Abs("../../kit")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEVKIT_HOME", filepath.Dir(kit))
	if _, err := cmdDraft(root, "исходный материал", "mid", CommitOpts{}); err != nil {
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
	input := []byte(`{"proposals":[{"id":"p1","revision":1,"outcome":"add","text":"первая"},{"id":"p2","revision":1,"outcome":"drop","reason":"лишнее"}],"analysis":"готово"}`)
	saved, err := cmdDraftProposalsSave(root, "XR-008", view.Snapshot, input)
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal([]byte(saved), &view); err != nil || len(view.State.Proposals) != 2 {
		t.Fatalf("%s: %v", saved, err)
	}
	if _, err = cmdDraftProposalsSave(root, "XR-008", "stale", input); err == nil || !strings.Contains(err.Error(), "изменилась") {
		t.Fatalf("stale: %v", err)
	}
	edit := []byte(`{"proposals":[{"id":"p1","revision":2,"outcome":"add","text":"правка"}]}`)
	saved, err = cmdDraftProposalsSave(root, "XR-008", view.Snapshot, edit)
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal([]byte(saved), &view); err != nil || len(view.State.Proposals) != 2 || view.State.Proposals[0].Text != "правка" {
		t.Fatalf("%s: %v", saved, err)
	}
	if _, err = cmdDraftProposalsSave(root, "XR-008", view.Snapshot, []byte(`{"proposals":[{"id":"p3","revision":1,"outcome":"drop","status":"accepted"}]}`)); err == nil {
		t.Fatal("forged decision")
	}
}

func TestProposalRejectPartialAndHistory(t *testing.T) {
	root := setup(t)
	kit, err := filepath.Abs("../../kit")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEVKIT_HOME", filepath.Dir(kit))
	if _, err := cmdDraft(root, "исходный материал", "mid", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	raw, err := cmdDraftProposals(root, "XR-008")
	if err != nil {
		t.Fatal(err)
	}
	var view proposalView
	json.Unmarshal([]byte(raw), &view)
	raw, err = cmdDraftProposalsSave(root, "XR-008", view.Snapshot, []byte(`{"proposals":[{"id":"p1","revision":1,"outcome":"drop"},{"id":"p2","revision":1,"outcome":"drop"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	json.Unmarshal([]byte(raw), &view)
	raw, err = cmdDraftProposalReject(root, "XR-008", "p1", view.Snapshot, "не нужно")
	if err != nil {
		t.Fatal(err)
	}
	json.Unmarshal([]byte(raw), &view)
	if view.State.State != "active" || view.State.Proposals[0].Result.Reason != "не нужно" {
		t.Fatalf("partial: %s", raw)
	}
	raw, err = cmdDraftProposalReject(root, "XR-008", "p2", view.Snapshot, "лишнее")
	if err != nil {
		t.Fatal(err)
	}
	json.Unmarshal([]byte(raw), &view)
	if view.State.State != "history" {
		t.Fatalf("history: %s", raw)
	}
	raw, err = cmdDraftProposals(root, "XR-008")
	if err != nil || !strings.Contains(raw, "не нужно") {
		t.Fatalf("history read: %s %v", raw, err)
	}
}
