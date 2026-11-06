package draftproposal

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestPreparedConclusionRoundTripAndLegacyBytes(t *testing.T) {
	var state State
	if err := json.Unmarshal([]byte(`{"version":1,"state":"active","conclusion":"Удалить лишнее.\n\nПриписать полезное.","proposals":[{"id":"drop","revision":1,"outcome":"drop","reason":"устарело"}]}`), &state); err != nil {
		t.Fatal(err)
	}
	data, err := RenderPrepared(state)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte(`conclusion = "Удалить лишнее.\n\nПриписать полезное."`)) {
		t.Fatalf("заключение не сохранено: %s", data)
	}
	parsed, err := ParsePrepared(data)
	if err != nil {
		t.Fatal(err)
	}
	jsonData, err := json.Marshal(parsed)
	if err != nil || !bytes.Contains(jsonData, []byte(`"conclusion":"Удалить лишнее.\n\nПриписать полезное."`)) {
		t.Fatalf("заключение потеряно: %s %v", jsonData, err)
	}
	legacy := []byte("```toml\nversion = 1\nstate = \"active\"\nkeys = []\n```\n")
	old, err := ParsePrepared(legacy)
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := RenderPrepared(old)
	if err != nil || !bytes.Equal(rendered, legacy) {
		t.Fatalf("байты старого состояния изменились: %q %v", rendered, err)
	}
}
