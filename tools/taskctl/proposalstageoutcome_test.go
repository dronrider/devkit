package main

import (
	"os"
	"strings"
	"testing"

	"github.com/dronrider/devkit/internal/draftmeta"
	"github.com/dronrider/devkit/internal/draftproposal"
)

func TestStagingFourOutcomesDoNotConsumeSource(t *testing.T) {
	root := setup(t)
	if _, err := cmdDraft(root, "исходный материал", "mid", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(draftPath(root, "XR-008"))
	if err != nil {
		t.Fatal(err)
	}
	s, err := newStagingBoard(root)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	h := draftmeta.Header{Type: "task", Source: "meeting", Lock: "locked", Priority: "mid"}
	cases := []draftproposal.Proposal{
		{ID: "p1", Outcome: "add", Title: "Новая задача", Text: fixtureTaskBody, BoardType: "task", Rank: "0+1+1+0+1", Size: "M", Accept: "agent"},
		{ID: "p2", Outcome: "attach", Target: "XR-001", Text: "Материал для приписки"},
		{ID: "p3", Outcome: "defer", Text: "Материал на потом", Reason: "ждём ответа"},
		{ID: "p4", Outcome: "drop", Text: "Протухший материал", Reason: "уже не нужно"},
	}
	var ids []string
	for _, p := range cases {
		r, err := s.stageOutcome("XR-008", h, p)
		if err != nil {
			t.Fatalf("%s: %v", p.ID, err)
		}
		ids = append(ids, r.ID)
	}
	if ids[0] == "" || ids[2] == "" || ids[0] == ids[2] {
		t.Fatalf("IDs: %v", ids)
	}
	for _, id := range []string{ids[0], "XR-001"} {
		if err := draftmeta.GuardRecord(s.root, id); err == nil {
			t.Errorf("%s is not protected", id)
		}
	}
	fragment, err := os.ReadFile(draftPath(s.root, ids[2]))
	if err != nil {
		t.Fatal(err)
	}
	for _, word := range []string{"источник: meeting", "замок: locked", "/docs/tasks/drafts/XR-008.md", "ждём ответа"} {
		if !strings.Contains(string(fragment), word) {
			t.Errorf("нет %s: %s", word, fragment)
		}
	}
	for _, id := range []string{ids[0], "XR-001"} {
		data, err := os.ReadFile(taskFileAbs(s.root, id))
		if err != nil || !strings.Contains(string(data), "](/docs/tasks/drafts/XR-008.md)") {
			t.Fatalf("%s: нет перехода к исходнику: %v\n%s", id, err, data)
		}
	}
	staged, err := os.ReadFile(draftPath(s.root, "XR-008"))
	if err != nil || string(staged) != string(before) {
		t.Fatalf("source consumed or changed: %v", err)
	}
	live, err := os.ReadFile(draftPath(root, "XR-008"))
	if err != nil || string(live) != string(before) {
		t.Fatalf("live changed: %v", err)
	}
}
