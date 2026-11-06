package main

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/dronrider/devkit/internal/draftproposal"
	"github.com/dronrider/devkit/internal/drafttx"
)

func TestBundleHistoryLateSaveCannotReopen(t *testing.T) {
	root, view, request := bundleFixture(t)
	confirmation, _ := json.Marshal(request)
	if _, err := cmdDraftPreparedConfirm(root, confirmation, CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	raw, err := cmdDraftPrepared(root, view.ID)
	if err != nil {
		t.Fatal(err)
	}
	var history preparedView
	json.Unmarshal([]byte(raw), &history)
	before, err := collectDocs(root)
	if err != nil {
		t.Fatal(err)
	}
	claims, err := drafttx.ClaimedBoardIDs(root)
	if err != nil {
		t.Fatal(err)
	}
	next := draftproposal.Proposal{ID: "late-result", Revision: 1, Outcome: "drop", Text: "опоздавший разбор", Reason: "новое решение"}
	input, _ := json.Marshal(map[string]any{"proposals": []draftproposal.Proposal{next}, "complete": true})
	if _, err := cmdDraftPreparedSave(root, view.ID, history.Snapshot, input); err == nil || !strings.Contains(err.Error(), "истории") {
		t.Fatalf("history save did not refuse: %v", err)
	}
	after, err := collectDocs(root)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("late save reopened source or changed receipts")
	}
	currentClaims, err := drafttx.ClaimedBoardIDs(root)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(claims, currentClaims) {
		t.Fatal("history save reserved a number")
	}
	replay, err := cmdDraftPreparedConfirm(root, confirmation, CommitOpts{})
	if err != nil {
		t.Fatal(err)
	}
	var result confirmationResult
	json.Unmarshal([]byte(replay), &result)
	if !result.Repeated || len(result.Results) != 2 {
		t.Fatalf("history receipt replay lost: %s", replay)
	}
}
