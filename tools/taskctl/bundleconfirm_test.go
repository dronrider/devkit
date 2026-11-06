package main

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/dronrider/devkit/internal/draftproposal"
)

func TestBundlePartialAndRejectLeaveAllFiles(t *testing.T) {
	for _, action := range []string{"accept", "reject"} {
		t.Run(action, func(t *testing.T) {
			root := setup(t)
			kit, _ := filepath.Abs("../../kit")
			t.Setenv("DEVKIT_HOME", filepath.Dir(kit))
			if _, err := cmdDraft(root, "исходник", "mid", CommitOpts{}); err != nil {
				t.Fatal(err)
			}
			state := draftproposal.State{Version: 1, State: "active", Proposals: []draftproposal.Proposal{
				{ID: "p1", Revision: 1, Outcome: "drop", Text: "одно", Reason: "лишнее"},
				{ID: "p2", Revision: 1, Outcome: "drop", Text: "другое", Reason: "лишнее"},
			}}
			if err := draftproposal.SavePrepared(root, "XR-008", state); err != nil {
				t.Fatal(err)
			}
			raw, err := cmdDraftPrepared(root, "XR-008")
			if err != nil {
				t.Fatal(err)
			}
			var view preparedView
			json.Unmarshal([]byte(raw), &view)
			before, err := collectDocs(root)
			if err != nil {
				t.Fatal(err)
			}
			request, _ := json.Marshal(draftproposal.ConfirmRequest{
				Snapshots: []draftproposal.Snapshot{{Source: "XR-008", Digest: view.Basis}},
				Decisions: []draftproposal.Decision{{Source: "XR-008", Proposal: "p1", Revision: 1, Action: action, Reason: "не нужно"}},
			})
			if _, err = cmdDraftPreparedConfirm(root, request, CommitOpts{}); err == nil {
				t.Fatal("частичное решение применилось")
			}
			after, err := collectDocs(root)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before, after) {
				t.Fatal("отказ изменил файлы")
			}
		})
	}
}

func TestBundleIndividualRejectLeavesAllFiles(t *testing.T) {
	root := setup(t)
	kit, _ := filepath.Abs("../../kit")
	t.Setenv("DEVKIT_HOME", filepath.Dir(kit))
	if _, err := cmdDraft(root, "исходник", "mid", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	state := draftproposal.State{Version: 1, State: "active", Proposals: []draftproposal.Proposal{{ID: "p1", Revision: 1, Outcome: "drop", Reason: "лишнее"}}}
	if err := draftproposal.SavePrepared(root, "XR-008", state); err != nil {
		t.Fatal(err)
	}
	raw, err := cmdDraftPrepared(root, "XR-008")
	if err != nil {
		t.Fatal(err)
	}
	var view preparedView
	json.Unmarshal([]byte(raw), &view)
	before, _ := collectDocs(root)
	if _, err = cmdDraftPreparedReject(root, "XR-008", "p1", view.Snapshot, "не нужно"); err == nil {
		t.Fatal("индивидуальный отказ применился")
	}
	after, _ := collectDocs(root)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("отказ изменил файлы")
	}
}
