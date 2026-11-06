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

func TestBundleHistorySourcePutRefusesOldAndFreshForm(t *testing.T) {
	for _, carrier := range []string{"prepared", "legacy"} {
		t.Run(carrier, func(t *testing.T) {
			e, c, _ := tasksEnv(t)
			kit, _ := filepath.Abs("../..")
			t.Setenv("DEVKIT_HOME", kit)
			resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/drafts", `{"text":"Исходник закрытого разбора","prio":"mid"}`)
			if resp.StatusCode != http.StatusOK {
				t.Fatal(body(t, resp))
			}
			resp.Body.Close()
			base := e.srv.URL + "/api/projects/demo/drafts/XR-005"
			var oldForm struct{ Text, Hash string }
			resp = doReq(t, c, "GET", base, "")
			if err := json.NewDecoder(resp.Body).Decode(&oldForm); err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			source := filepath.Join(e.proj, "docs", "tasks", "drafts", "XR-005.md")
			record, err := os.ReadFile(source)
			if err != nil {
				t.Fatal(err)
			}
			material, _, err := draftproposal.Material(record)
			if err != nil {
				t.Fatal(err)
			}
			state := draftproposal.State{Version: 1, State: "active", PackRevision: 1, PreparedSource: draftproposal.Digest(material), Proposals: []draftproposal.Proposal{{ID: "close", Revision: 1, Outcome: "drop", Text: "фрагмент", Reason: "не требуется"}}}
			if err := draftproposal.SavePrepared(e.proj, "XR-005", state); err != nil {
				t.Fatal(err)
			}
			var view struct {
				ID, Basis string
				State     draftproposal.State
			}
			resp = doReq(t, c, "GET", base+"/prepared", "")
			if err := json.NewDecoder(resp.Body).Decode(&view); err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			p := view.State.Proposals[0]
			request := draftproposal.ConfirmRequest{Snapshots: []draftproposal.Snapshot{{Source: view.ID, Digest: view.Basis, Revision: view.State.PackRevision}}, Decisions: []draftproposal.Decision{{Source: view.ID, Proposal: p.ID, Revision: p.Revision, Action: "accept"}}}
			confirmation, _ := json.Marshal(request)
			resp = doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/drafts/prepared/confirm", string(confirmation))
			raw := body(t, resp)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("confirm %d %s", resp.StatusCode, raw)
			}
			if carrier == "legacy" {
				saved, err := os.ReadFile(draftproposal.PreparedPath(e.proj, view.ID))
				if err != nil {
					t.Fatal(err)
				}
				history, err := draftproposal.ParsePrepared(saved)
				if err != nil {
					t.Fatal(err)
				}
				record, err = os.ReadFile(source)
				if err != nil {
					t.Fatal(err)
				}
				embedded, err := draftproposal.Replace(record, history)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(source, embedded, 0644); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(draftproposal.PreparedPath(e.proj, view.ID)); err != nil {
					t.Fatal(err)
				}
			}
			var fresh struct{ Text, Hash string }
			resp = doReq(t, c, "GET", base, "")
			if err := json.NewDecoder(resp.Body).Decode(&fresh); err != nil {
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
			for _, hash := range []string{oldForm.Hash, fresh.Hash} {
				input, _ := json.Marshal(map[string]string{"base": hash, "text": fresh.Text + "\nпоздняя правка другой вкладки\n"})
				resp = doReq(t, c, "PUT", base, string(input))
				raw = body(t, resp)
				if resp.StatusCode != http.StatusConflict || !strings.Contains(raw, "истории") {
					t.Fatalf("history PUT status%d %s", resp.StatusCode, raw)
				}
				if !reflect.DeepEqual(before, snapshot()) {
					t.Fatal("late history PUT changed docs or receipts")
				}
			}
			resp = doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/drafts/prepared/confirm", string(confirmation))
			raw = body(t, resp)
			if resp.StatusCode != http.StatusOK || !strings.Contains(raw, `"repeated":true`) {
				t.Fatalf("history replay %d %s", resp.StatusCode, raw)
			}
			if !reflect.DeepEqual(before, snapshot()) {
				t.Fatal("history replay changed files")
			}
		})
	}
}
