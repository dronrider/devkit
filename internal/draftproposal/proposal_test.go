package draftproposal

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestRoundTripAndHistory(t *testing.T) {
	source := []byte("# XR-8: идея\n\n## Текст\nматериал\n\n## Другое\nтекст\n")
	s, err := Parse(source)
	if err != nil || s.State != "active" || len(s.Proposals) != 0 {
		t.Fatalf("empty: %+v %v", s, err)
	}
	s.Proposals = []Proposal{{ID: "p1", Revision: 1, Outcome: "add", Text: "первая"}, {ID: "p2", Revision: 2, Outcome: "drop", Reason: "причина", Status: "rejected"}}
	out, err := Replace(source, s)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Parse(out)
	if err != nil || len(got.Proposals) != 2 || got.Proposals[1].Status != "rejected" {
		t.Fatalf("roundtrip %+v %v", got, err)
	}
	got.Proposals[0].Status = "accepted"
	got.Proposals[0].Result = &Receipt{Proposal: "p1", Revision: 1, Outcome: "add", IDs: []string{"XR-9"}, Result: "готово"}
	got.State = "history"
	out, err = Replace(out, got)
	if err != nil {
		t.Fatal(err)
	}
	got, err = Parse(out)
	if err != nil || got.State != "history" || got.Proposals[0].Result.IDs[0] != "XR-9" {
		t.Fatalf("history %+v %v", got, err)
	}
	if !bytes.Contains(out, []byte("материал")) || !bytes.Contains(out, []byte("## Другое\nтекст")) || strings.Count(string(out), heading) != 1 {
		t.Fatalf("source changed: %s", out)
	}
}
func TestInvalidStateAndDigest(t *testing.T) {
	if _, err := Parse([]byte("# запись\n\n" + heading + "\n\n```json\n{bad}\n```\n")); err == nil {
		t.Fatal("invalid JSON accepted")
	}
	s := State{Version: 1, State: "active", Proposals: []Proposal{{ID: "p", Revision: 1, Outcome: "add"}, {ID: "p", Revision: 1, Outcome: "drop"}}}
	if _, err := Replace([]byte("# запись"), s); err == nil {
		t.Fatal("duplicate accepted")
	}
	if Digest([]byte("ab"), []byte("c")) == Digest([]byte("a"), []byte("bc")) {
		t.Fatal("ambiguous digest")
	}
}

func TestDecisionAndReceiptKeepSourceAndReason(t *testing.T) {
	original := ConfirmRequest{Snapshots: []Snapshot{{Source: "XR-1", Digest: "snapshot"}}, Decisions: []Decision{{Source: "XR-1", Proposal: "p1", Revision: 2, Action: "reject", Reason: "лишнее"}}}
	raw, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	var got ConfirmRequest
	if err = json.Unmarshal(raw, &got); err != nil || got.Decisions[0].Reason != "лишнее" || got.Decisions[0].Source != "XR-1" {
		t.Fatalf("request %s %v", raw, err)
	}
	s := State{Version: 1, State: "history", Proposals: []Proposal{{ID: "p1", Revision: 2, Outcome: "drop", Status: "rejected", Result: &Receipt{Source: "XR-1", Proposal: "p1", Revision: 2, Reason: "лишнее"}}}}
	data, err := Replace([]byte("# запись\n"), s)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := Parse(data)
	if err != nil || parsed.Proposals[0].Result.Source != "XR-1" || parsed.Proposals[0].Result.Reason != "лишнее" {
		t.Fatalf("receipt %+v %v", parsed, err)
	}
}

func TestMaterialHidesStateAndRestoresIt(t *testing.T) {
	state := State{Version: 1, State: "active", Proposals: []Proposal{{ID: "p1", Revision: 1, Outcome: "drop", Reason: "лишнее", Rationale: "дубликат"}}}
	original, err := Replace([]byte("# запись\n\n## Текст\nданные\n"), state)
	if err != nil {
		t.Fatal(err)
	}
	material, found, err := Material(original)
	if err != nil || !found || bytes.Contains(material, []byte("```json")) {
		t.Fatalf("material %s %v", material, err)
	}
	edited := append(material, []byte("\nновая строка\n")...)
	restored, err := Replace(edited, state)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := Parse(restored)
	if err != nil || len(parsed.Proposals) != 1 || parsed.Proposals[0].Reason != "лишнее" || parsed.Proposals[0].Rationale != "дубликат" || !bytes.Contains(restored, []byte("новая строка")) {
		t.Fatalf("restored %s %v", restored, err)
	}
}

func TestQuotedProposalHeadingIsNotState(t *testing.T) {
	data := []byte("# запись\n\n## Текст\n\n```md\n## Предложения разбора\nпример\n```\n")
	state, err := Parse(data)
	if err != nil || state.State != "active" || len(state.Proposals) != 0 {
		t.Fatalf("quoted heading: %+v %v", state, err)
	}
	_, found, err := Material(data)
	if err != nil || found {
		t.Fatalf("quoted section: %v %v", found, err)
	}
}
