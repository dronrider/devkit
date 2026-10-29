package draftmeta

import (
	"strings"
	"testing"
)

func TestEditorPreservesMetadata(t *testing.T) {
	original := []byte("# запись\nтип: milestone\nисточник: mail\nзамок: locked\n\n## Текст\nзамок: это часть текста\n")
	visible, has, err := Material(original)
	if err != nil || !has || strings.Contains(string(visible), "замок: locked") || !strings.Contains(string(visible), "замок: это часть текста") {
		t.Fatalf("visible %s %v", visible, err)
	}
	restored, err := Restore(append(visible, []byte("правка\n")...), original)
	if err != nil {
		t.Fatal(err)
	}
	h, err := Parse(restored)
	if err != nil || h.Type != "milestone" || h.Source != "mail" || h.Lock != "locked" {
		t.Fatalf("metadata %+v %v", h, err)
	}
}
