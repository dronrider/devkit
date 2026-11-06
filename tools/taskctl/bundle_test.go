package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/dronrider/devkit/internal/draftproposal"
	"github.com/dronrider/devkit/internal/drafttx"
)

func bundleFixture(t *testing.T) (string, preparedView, draftproposal.ConfirmRequest) {
	t.Helper()
	root := setup(t)
	kit, _ := filepath.Abs("../../kit")
	t.Setenv("DEVKIT_HOME", filepath.Dir(kit))
	if _, err := cmdDraft(root, "исходный материал", "mid", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	raw, err := cmdDraftPrepared(root, "XR-008")
	if err != nil {
		t.Fatal(err)
	}
	var view preparedView
	json.Unmarshal([]byte(raw), &view)
	var proposals []draftproposal.Proposal
	for _, key := range []string{"one", "two"} {
		proposals = append(proposals, draftproposal.Proposal{ID: key, Revision: 1, Outcome: "add", Title: "Задача " + key, Text: fixtureTaskBody, BoardType: "task", Rank: "0+1+1+0+1", Size: "M", Accept: "agent"})
	}
	input, _ := json.Marshal(map[string]any{"proposals": proposals, "complete": true})
	raw, err = cmdDraftPreparedSave(root, "XR-008", view.Snapshot, input)
	if err != nil {
		t.Fatal(err)
	}
	json.Unmarshal([]byte(raw), &view)
	if !view.Ready || view.State.PackRevision < 1 {
		t.Fatalf("not ready: %s", raw)
	}
	request := draftproposal.ConfirmRequest{Snapshots: []draftproposal.Snapshot{{Source: view.ID, Digest: view.Basis, Revision: view.State.PackRevision}}}
	for _, p := range view.State.Proposals {
		request.Decisions = append(request.Decisions, draftproposal.Decision{Source: view.ID, Proposal: p.ID, Revision: p.Revision, Action: "accept"})
	}
	return root, view, request
}

func TestBundleWholeHistoryAndExactReplay(t *testing.T) {
	root, view, request := bundleFixture(t)
	data, _ := json.Marshal(request)
	raw, err := cmdDraftPreparedConfirm(root, data, CommitOpts{})
	if err != nil {
		t.Fatal(err)
	}
	var result confirmationResult
	json.Unmarshal([]byte(raw), &result)
	if len(result.Results) != 2 || result.Repeated {
		t.Fatalf("results: %s", raw)
	}
	_, state, err := readPrepared(root, view.ID)
	if err != nil {
		t.Fatal(err)
	}
	if state.State != "history" || len(state.Receipts) != 3 || state.Receipts[2].Outcome != "bundle" {
		t.Fatalf("history/receipt: %+v", state)
	}
	active, _ := loadDrafts(root)
	if findDraft(active, view.ID) != nil {
		t.Fatal("source remains active")
	}
	all, _ := loadDraftsWithHistory(root, true)
	if findDraft(all, view.ID) == nil {
		t.Fatal("history missing")
	}
	for _, p := range state.Proposals {
		if p.Status != "accepted" || len(p.Result.IDs) != 1 || p.Result.IDs[0] != p.BoardID {
			t.Fatalf("reserved ID lost: %+v", p)
		}
	}
	before, _ := collectDocs(root)
	raw, err = cmdDraftPreparedConfirm(root, data, CommitOpts{})
	if err != nil {
		t.Fatal(err)
	}
	json.Unmarshal([]byte(raw), &result)
	if !result.Repeated || len(result.Results) != 2 {
		t.Fatalf("replay: %s", raw)
	}
	after, _ := collectDocs(root)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("replay changed files")
	}
	if _, err = cmdDraftIdea(root, view.ID, "новая мысль", CommitOpts{}); err == nil || !strings.Contains(err.Error(), "истории") {
		t.Fatalf("history idea: %v", err)
	}
	final, _ := collectDocs(root)
	if !reflect.DeepEqual(after, final) {
		t.Fatal("history idea changed files")
	}
}

func TestBundleLastElementFailureIsAtomic(t *testing.T) {
	root, view, _ := bundleFixture(t)
	state := view.State
	state.Proposals[1].Barrier = "неизвестный: пояснение"
	if err := draftproposal.SavePrepared(root, view.ID, state); err != nil {
		t.Fatal(err)
	}
	record, _, _ := readPrepared(root, view.ID)
	changed, err := preparedViewOf(root, view.ID, record, state)
	if err != nil {
		t.Fatal(err)
	}
	request := draftproposal.ConfirmRequest{Snapshots: []draftproposal.Snapshot{{Source: view.ID, Digest: changed.Basis, Revision: state.PackRevision}}}
	for _, p := range state.Proposals {
		request.Decisions = append(request.Decisions, draftproposal.Decision{Source: view.ID, Proposal: p.ID, Revision: p.Revision, Action: "accept"})
	}
	before, _ := collectDocs(root)
	data, _ := json.Marshal(request)
	if _, err := cmdDraftPreparedConfirm(root, data, CommitOpts{}); err == nil {
		t.Fatal("invalid last element accepted")
	}
	after, _ := collectDocs(root)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("last element failure changed files")
	}
}

func TestBundleReadinessAndStaleRevision(t *testing.T) {
	root, view, request := bundleFixture(t)
	before, _ := collectDocs(root)
	partial := request
	partial.Decisions = append([]draftproposal.Decision(nil), request.Decisions[:1]...)
	data, _ := json.Marshal(partial)
	if _, err := cmdDraftPreparedConfirm(root, data, CommitOpts{}); err == nil {
		t.Fatal("ready subset accepted")
	}
	rejected := request
	rejected.Decisions = append([]draftproposal.Decision(nil), request.Decisions...)
	rejected.Decisions[1].Action = "reject"
	rejected.Decisions[1].Reason = "нет"
	data, _ = json.Marshal(rejected)
	if _, err := cmdDraftPreparedConfirm(root, data, CommitOpts{}); err == nil {
		t.Fatal("ready reject accepted")
	}
	after, _ := collectDocs(root)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("invalid decisions changed files")
	}
	p := view.State.Proposals[0]
	p.Revision++
	p.Title = "Исправлено"
	input, _ := json.Marshal(map[string]any{"proposals": []draftproposal.Proposal{p}, "complete": true})
	raw, err := cmdDraftPreparedSave(root, view.ID, view.Snapshot, input)
	if err != nil {
		t.Fatal(err)
	}
	var fresh preparedView
	json.Unmarshal([]byte(raw), &fresh)
	if !fresh.Ready || fresh.State.PackRevision <= view.State.PackRevision {
		t.Fatalf("manual save readiness: %s", raw)
	}
	data, _ = json.Marshal(request)
	if _, err = cmdDraftPreparedConfirm(root, data, CommitOpts{}); err == nil {
		t.Fatal("stale request accepted")
	}
	if _, err = cmdDraftIdea(root, view.ID, "ещё не разобрано", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	record, state, _ := readPrepared(root, view.ID)
	if reason := preparedNotReady(record, state); !strings.Contains(reason, "мысли") {
		t.Fatalf("new idea readiness: %s", reason)
	}
	input, _ = json.Marshal(map[string]any{"complete": true, "proposals": []any{}})
	current, _ := preparedViewOf(root, view.ID, record, state)
	if _, err = cmdDraftPreparedSave(root, view.ID, current.Snapshot, input); err == nil {
		t.Fatal("unparsed idea completed")
	}
}

func TestBundleActualPlanRecoversAfterEveryFile(t *testing.T) {
	root, view, request := bundleFixture(t)
	lock, err := drafttx.Acquire(root)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := prepareConfirmation(root, request)
	lock.Close()
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Changes) < 4 {
		t.Fatalf("plan too small: %d", len(plan.Changes))
	}
	for cut := 0; cut <= len(plan.Changes); cut++ {
		t.Run(fmt.Sprint(cut), func(t *testing.T) {
			clone := t.TempDir()
			images, err := collectDocs(root)
			if err != nil {
				t.Fatal(err)
			}
			for rel, img := range images {
				path := filepath.Join(clone, rel)
				os.MkdirAll(filepath.Dir(path), 0755)
				if err := os.WriteFile(path, img.Data, os.FileMode(img.Mode)); err != nil {
					t.Fatal(err)
				}
			}
			clone, _ = filepath.EvalSymlinks(clone)
			l, err := drafttx.Acquire(clone)
			if err != nil {
				t.Fatal(err)
			}
			dir := filepath.Join(l.ServiceDir(), fmt.Sprintf("%x", sha256.Sum256([]byte(clone))))
			pending, _ := json.Marshal(map[string]any{"version": 1, "root": clone, "changes": plan.Changes})
			if err := os.WriteFile(filepath.Join(dir, "pending.json"), pending, 0600); err != nil {
				t.Fatal(err)
			}
			for _, change := range plan.Changes[:cut] {
				path := filepath.Join(clone, change.Path)
				if change.After.Exists {
					os.MkdirAll(filepath.Dir(path), 0755)
					if err := os.WriteFile(path, change.After.Data, os.FileMode(change.After.Mode)); err != nil {
						t.Fatal(err)
					}
				} else {
					os.Remove(path)
				}
			}
			l.Close()
			recovered, err := drafttx.Acquire(clone)
			if err != nil {
				t.Fatal(err)
			}
			recovered.Close()
			for _, change := range plan.Changes {
				actual, err := drafttx.Read(filepath.Join(clone, change.Path))
				if err != nil || !reflect.DeepEqual(actual, change.After) {
					t.Fatalf("incomplete after %d: %s %v", cut, change.Path, err)
				}
			}
			_, state, err := readPrepared(clone, view.ID)
			if err != nil || state.State != "history" || len(state.Receipts) != 3 {
				t.Fatalf("state after recovery: %+v %v", state, err)
			}
		})
	}
}

func TestBundleConcurrentConfirmReplays(t *testing.T) {
	root, _, request := bundleFixture(t)
	data, _ := json.Marshal(request)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var results []confirmationResult
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			raw, err := cmdDraftPreparedConfirm(root, data, CommitOpts{})
			if err != nil {
				t.Error(err)
				return
			}
			var result confirmationResult
			json.Unmarshal([]byte(raw), &result)
			mu.Lock()
			results = append(results, result)
			mu.Unlock()
		}()
	}
	wg.Wait()
	if len(results) != 2 || results[0].Repeated == results[1].Repeated {
		t.Fatalf("parallel replies: %+v", results)
	}
}

func TestBundleReplacePendingKeepsReservationsWithoutLedger(t *testing.T) {
	root, view, stale := bundleFixture(t)
	kept := view.State.Proposals[0]
	kept.Revision++
	kept.Title = "Осталась одна"
	removed := view.State.Proposals[1].BoardID
	input, _ := json.Marshal(map[string]any{"proposals": []draftproposal.Proposal{kept}, "replace_pending": true, "complete": true})
	raw, err := cmdDraftPreparedSave(root, view.ID, view.Snapshot, input)
	if err != nil {
		t.Fatal(err)
	}
	var changed preparedView
	json.Unmarshal([]byte(raw), &changed)
	if !changed.Ready || len(changed.State.Proposals) != 1 || !reflect.DeepEqual(changed.State.RetiredIDs, []string{removed}) {
		t.Fatalf("replacement: %s", raw)
	}
	old, _ := json.Marshal(stale)
	if _, err := cmdDraftPreparedConfirm(root, old, CommitOpts{}); err == nil {
		t.Fatal("old composition approved")
	}
	clone := t.TempDir()
	images, _ := collectDocs(root)
	for rel, img := range images {
		path := filepath.Join(clone, rel)
		os.MkdirAll(filepath.Dir(path), 0755)
		if err := os.WriteFile(path, img.Data, os.FileMode(img.Mode)); err != nil {
			t.Fatal(err)
		}
	}
	reserved, err := draftproposal.ReservedIDs(clone)
	if err != nil || !reserved[removed] {
		t.Fatalf("reservation lost in copy: %v %v", reserved, err)
	}
	next, err := cmdID(clone)
	if err != nil || next == removed {
		t.Fatalf("retired ID reused: %s %v", next, err)
	}
}

func TestBundleLegacyRemainingKeepsAcceptedReceipt(t *testing.T) {
	root, view, _ := bundleFixture(t)
	state := view.State
	accepted := &state.Proposals[0]
	accepted.Status = "accepted"
	receipt := draftproposal.Receipt{Key: "legacy", Source: view.ID, Proposal: accepted.ID, Revision: accepted.Revision, Outcome: "add", IDs: []string{"XR-001"}, Result: "прежде заведено"}
	accepted.Result = &receipt
	state.Receipts = []draftproposal.Receipt{receipt}
	state.PreparedSource = ""
	state.PackRevision = 0
	if err := draftproposal.SavePrepared(root, view.ID, state); err != nil {
		t.Fatal(err)
	}
	_, baseline, err := readPrepared(root, view.ID)
	if err != nil {
		t.Fatal(err)
	}
	expectedAccepted := baseline.Proposals[0]
	record, _, _ := readPrepared(root, view.ID)
	legacy, _ := preparedViewOf(root, view.ID, record, state)
	if !legacy.Ready || legacy.State.PackRevision != 1 {
		t.Fatalf("legacy remaining requires Save: %+v", legacy)
	}
	input := []byte(`{"proposals":[],"complete":true}`)
	raw, err := cmdDraftPreparedSave(root, view.ID, legacy.Snapshot, input)
	if err != nil {
		t.Fatal(err)
	}
	var fresh preparedView
	json.Unmarshal([]byte(raw), &fresh)
	if !fresh.Ready || !reflect.DeepEqual(fresh.State.Proposals[0], expectedAccepted) {
		t.Fatalf("accepted changed: %s", raw)
	}
	pending := fresh.State.Proposals[1]
	request := draftproposal.ConfirmRequest{Snapshots: []draftproposal.Snapshot{{Source: view.ID, Digest: fresh.Basis, Revision: fresh.State.PackRevision}}, Decisions: []draftproposal.Decision{{Source: view.ID, Proposal: pending.ID, Revision: pending.Revision, Action: "accept"}}}
	data, _ := json.Marshal(request)
	raw, err = cmdDraftPreparedConfirm(root, data, CommitOpts{})
	if err != nil {
		t.Fatal(err)
	}
	_, after, _ := readPrepared(root, view.ID)
	if after.State != "history" || !reflect.DeepEqual(after.Proposals[0], expectedAccepted) || after.Receipts[0].Key != "legacy" {
		t.Fatalf("legacy changed: %+v", after)
	}
	var result confirmationResult
	json.Unmarshal([]byte(raw), &result)
	if len(result.Results) != 1 {
		t.Fatalf("accepted was reapplied: %s", raw)
	}
}

func TestBundleRawEmptyAndFatesBeforeComplete(t *testing.T) {
	root := setup(t)
	kit, _ := filepath.Abs("../../kit")
	t.Setenv("DEVKIT_HOME", filepath.Dir(kit))
	if _, err := cmdDraft(root, "сырой материал", "mid", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	raw, _ := cmdDraftPrepared(root, "XR-008")
	var view preparedView
	json.Unmarshal([]byte(raw), &view)
	if view.Ready {
		t.Fatal("raw draft ready")
	}
	before, _ := collectDocs(root)
	if _, err := cmdDraftPreparedSave(root, view.ID, view.Snapshot, []byte(`{"proposals":[],"complete":true}`)); err == nil {
		t.Fatal("empty result ready")
	}
	after, _ := collectDocs(root)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("empty complete changed files")
	}
	root, view, _ = bundleFixture(t)
	if _, err := cmdDraftIdea(root, view.ID, "новая мысль", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	text, err := os.ReadFile(draftPath(root, view.ID))
	if err != nil {
		t.Fatal(err)
	}
	text = []byte(strings.ReplaceAll(string(text), "новая мысль", "новая мысль -> влита в материал записи"))
	if err := os.WriteFile(draftPath(root, view.ID), text, 0644); err != nil {
		t.Fatal(err)
	}
	_, state, _ := readPrepared(root, view.ID)
	current, _ := preparedViewOf(root, view.ID, text, state)
	raw, err = cmdDraftPreparedSave(root, view.ID, current.Snapshot, []byte(`{"proposals":[],"complete":true}`))
	if err != nil {
		t.Fatal(err)
	}
	json.Unmarshal([]byte(raw), &current)
	if !current.Ready {
		t.Fatalf("fates before complete not ready: %s", raw)
	}
	if err := os.WriteFile(draftPath(root, view.ID), append(text, []byte("\nДополнение материала\n")...), 0644); err != nil {
		t.Fatal(err)
	}
	raw, _ = cmdDraftPrepared(root, view.ID)
	json.Unmarshal([]byte(raw), &current)
	if current.Ready {
		t.Fatal("changed material ready")
	}
}

func TestBundleSaveConfirmRace(t *testing.T) {
	for range 4 {
		root, view, request := bundleFixture(t)
		confirmation, _ := json.Marshal(request)
		p := view.State.Proposals[0]
		p.Revision++
		p.Title = "Свежая правка"
		save, _ := json.Marshal(map[string]any{"proposals": []draftproposal.Proposal{p}, "complete": true})
		start := make(chan struct{})
		saved := make(chan error, 1)
		confirmed := make(chan error, 1)
		go func() { <-start; _, err := cmdDraftPreparedSave(root, view.ID, view.Snapshot, save); saved <- err }()
		go func() { <-start; _, err := cmdDraftPreparedConfirm(root, confirmation, CommitOpts{}); confirmed <- err }()
		close(start)
		saveErr, confirmErr := <-saved, <-confirmed
		if (saveErr == nil) == (confirmErr == nil) {
			t.Fatalf("race outcomes save=%v confirm=%v", saveErr, confirmErr)
		}
		_, state, err := readPrepared(root, view.ID)
		if err != nil {
			t.Fatal(err)
		}
		if saveErr == nil && state.State != "active" {
			t.Fatal("save lost to stale confirm")
		}
		if confirmErr == nil && state.State != "history" {
			t.Fatal("confirm left active source")
		}
	}
}

func TestBundleIdeaConfirmActualCLIRace(t *testing.T) {
	bin := os.Getenv("TASKCTL_ACCEPT_BIN")
	if bin == "" {
		bin = filepath.Join(t.TempDir(), "taskctl")
		build := exec.Command("go", "build", "-o", bin, ".")
		build.Env = append(os.Environ(), "GOWORK=off")
		if out, err := build.CombinedOutput(); err != nil {
			t.Fatalf("build: %v %s", err, out)
		}
	}
	for range 3 {
		root, view, request := bundleFixture(t)
		data, _ := json.Marshal(request)
		idea := exec.Command(bin, "-C", root, "draft", "idea", view.ID, "мысль из другой вкладки")
		confirm := exec.Command(bin, "-C", root, "draft", "prepared", "confirm")
		confirm.Stdin = bytes.NewReader(data)
		start := make(chan struct{})
		type reply struct {
			out []byte
			err error
		}
		ideas, confirmations := make(chan reply, 1), make(chan reply, 1)
		go func() { <-start; out, err := idea.CombinedOutput(); ideas <- reply{out, err} }()
		go func() { <-start; out, err := confirm.CombinedOutput(); confirmations <- reply{out, err} }()
		close(start)
		i, c := <-ideas, <-confirmations
		if (i.err == nil) == (c.err == nil) {
			t.Fatalf("race idea=%s %v confirm=%s %v", i.out, i.err, c.out, c.err)
		}
		record, state, err := readPrepared(root, view.ID)
		if err != nil {
			t.Fatal(err)
		}
		if i.err == nil && (state.State != "active" || !strings.Contains(string(record), "мысль из другой вкладки")) {
			t.Fatal("successful idea hidden or lost")
		}
		if c.err == nil && (state.State != "history" || !strings.Contains(string(i.out), "истории")) {
			t.Fatal("history idea did not fail explicitly")
		}
	}
}

func TestBundleRetiresLegacyFullKeyWithoutBoardID(t *testing.T) {
	root, view, _ := bundleFixture(t)
	state := view.State
	retired := state.Proposals[1].ID
	state.Proposals[1].BoardID = ""
	if err := draftproposal.SavePrepared(root, view.ID, state); err != nil {
		t.Fatal(err)
	}
	raw, err := cmdDraftPrepared(root, view.ID)
	if err != nil {
		t.Fatal(err)
	}
	json.Unmarshal([]byte(raw), &view)
	kept := view.State.Proposals[0]
	kept.Revision++
	input, _ := json.Marshal(map[string]any{"proposals": []draftproposal.Proposal{kept}, "replace_pending": true, "complete": true})
	raw, err = cmdDraftPreparedSave(root, view.ID, view.Snapshot, input)
	if err != nil {
		t.Fatal(err)
	}
	json.Unmarshal([]byte(raw), &view)
	if !reflect.DeepEqual(view.State.RetiredIDs, []string{retired}) {
		t.Fatalf("legacy full key reservation lost: %s", raw)
	}
}
