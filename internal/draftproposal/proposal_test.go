package draftproposal

import (
	"bytes"
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
