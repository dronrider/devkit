package draftproposal

import (
	"bytes"
	"testing"
)

func TestBundleFieldsOptionalAndRoundTrip(t *testing.T) {
	legacy := []byte("```toml\nversion = 1\nstate = \"active\"\nkeys = []\n```\n")
	state, err := ParsePrepared(legacy)
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := RenderPrepared(state)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(legacy, rendered) {
		t.Fatalf("legacy changed: %s", rendered)
	}
	state.PackRevision = 3
	state.PreparedSource = "source-digest"
	rendered, err = RenderPrepared(state)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParsePrepared(rendered)
	if err != nil || parsed.PackRevision != 3 || parsed.PreparedSource != "source-digest" {
		t.Fatalf("round trip: %+v %v", parsed, err)
	}
}
