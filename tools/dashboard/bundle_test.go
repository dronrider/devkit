package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/dronrider/devkit/internal/draftproposal"
)

func TestLegacyBundleReadyWithoutSave(t *testing.T) {
	e, c, _ := tasksEnv(t)
	kit, _ := filepath.Abs("../..")
	t.Setenv("DEVKIT_HOME", kit)
	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/drafts", `{"text":"Старая частично принятая запись","prio":"mid"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatal(body(t, resp))
	}
	resp.Body.Close()
	taskBody, err := os.ReadFile(taskBodyFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	receipt := draftproposal.Receipt{Key: "old", Source: "XR-005", Proposal: "accepted", Revision: 1, Outcome: "attach", IDs: []string{"XR-001"}, Result: "принято прежде"}
	state := draftproposal.State{Version: 1, State: "active", Proposals: []draftproposal.Proposal{
		{ID: "accepted", Revision: 1, Outcome: "attach", Target: "XR-001", Text: "старое", Status: "accepted", Result: &receipt},
		{ID: "remaining", Revision: 1, Outcome: "add", Title: "Оставшаяся задача", Text: string(taskBody), BoardType: "task", Rank: "0+1+1+0+1", Size: "M", Accept: "agent"},
	}, Receipts: []draftproposal.Receipt{receipt}}
	if err := draftproposal.SavePrepared(e.proj, "XR-005", state); err != nil {
		t.Fatal(err)
	}
	base := e.srv.URL + "/api/projects/demo/drafts/XR-005/prepared"
	var view struct {
		ID, Snapshot, Basis string
		Ready               bool
		Reason              string
		State               draftproposal.State
	}
	resp = doReq(t, c, "GET", base, "")
	if err := json.NewDecoder(resp.Body).Decode(&view); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if !view.Ready || view.State.PackRevision != 1 {
		t.Fatalf("legacy requires Save: %+v", view)
	}
	accepted := view.State.Proposals[0]
	pending := view.State.Proposals[1]
	req := draftproposal.ConfirmRequest{Snapshots: []draftproposal.Snapshot{{Source: view.ID, Digest: view.Basis, Revision: view.State.PackRevision}}, Decisions: []draftproposal.Decision{{Source: view.ID, Proposal: pending.ID, Revision: pending.Revision, Action: "accept"}}}
	payload, _ := json.Marshal(req)
	resp = doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/drafts/prepared/confirm", string(payload))
	raw := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("whole confirm: %d %s", resp.StatusCode, raw)
	}
	final, err := draftproposal.ParsePrepared([]byte(readFile(t, draftproposal.PreparedPath(e.proj, view.ID))))
	if err != nil {
		t.Fatal(err)
	}
	if final.State != "history" || !reflect.DeepEqual(final.Proposals[0], accepted) || final.Receipts[0].Key != "old" {
		t.Fatalf("legacy history: %+v", final)
	}
	resp = doReq(t, c, "GET", e.srv.URL+"/api/projects/demo/drafts", "")
	active := body(t, resp)
	if strings.Contains(active, `"id":"XR-005"`) {
		t.Fatalf("source remains active: %s", active)
	}
}

func TestBundleLastElementHTTPFailureLeavesFiles(t *testing.T) {
	e, c, _ := tasksEnv(t)
	kit, _ := filepath.Abs("../..")
	t.Setenv("DEVKIT_HOME", kit)
	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/drafts", `{"text":"Комплект двух задач","prio":"mid"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatal(body(t, resp))
	}
	resp.Body.Close()
	taskBody, err := os.ReadFile(taskBodyFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	record, err := os.ReadFile(filepath.Join(e.proj, "docs", "tasks", "drafts", "XR-005.md"))
	if err != nil {
		t.Fatal(err)
	}
	material, _, err := draftproposal.Material(record)
	if err != nil {
		t.Fatal(err)
	}
	state := draftproposal.State{Version: 1, State: "active", PackRevision: 1, PreparedSource: draftproposal.Digest(material)}
	for _, id := range []string{"first", "last"} {
		state.Proposals = append(state.Proposals, draftproposal.Proposal{ID: id, Revision: 1, Outcome: "add", Title: id, Text: string(taskBody), BoardType: "task", Rank: "0+1+1+0+1", Size: "M", Accept: "agent"})
	}
	state.Proposals[1].Barrier = "неизвестный: пояснение"
	if err := draftproposal.SavePrepared(e.proj, "XR-005", state); err != nil {
		t.Fatal(err)
	}
	var view struct {
		ID, Snapshot, Basis string
		Ready               bool
		State               draftproposal.State
	}
	resp = doReq(t, c, "GET", e.srv.URL+"/api/projects/demo/drafts/XR-005/prepared", "")
	if err := json.NewDecoder(resp.Body).Decode(&view); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	snapshot := func() map[string]string {
		out := map[string]string{}
		err := filepath.Walk(filepath.Join(e.proj, "docs"), func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() {
				return nil
			}
			data, err := os.ReadFile(path)
			if err == nil {
				out[path] = string(data)
			}
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	before := snapshot()
	req := draftproposal.ConfirmRequest{Snapshots: []draftproposal.Snapshot{{Source: view.ID, Digest: view.Basis, Revision: view.State.PackRevision}}}
	for _, p := range view.State.Proposals {
		req.Decisions = append(req.Decisions, draftproposal.Decision{Source: view.ID, Proposal: p.ID, Revision: p.Revision, Action: "accept"})
	}
	partial := req
	partial.Decisions = req.Decisions[:1]
	for _, request := range []draftproposal.ConfirmRequest{partial, req} {
		payload, _ := json.Marshal(request)
		resp = doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/drafts/prepared/confirm", string(payload))
		raw := body(t, resp)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("invalid bundle status %d %s", resp.StatusCode, raw)
		}
		if !reflect.DeepEqual(before, snapshot()) {
			t.Fatal("HTTP failure partially applied bundle/source/receipt")
		}
	}
}
