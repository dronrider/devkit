package main

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/dronrider/devkit/internal/chat"
)

func TestPreparedConsentCLI(t *testing.T) {
	for _, kind := range []string{"authored-drop", "add-adopt"} {
		t.Run(kind, func(t *testing.T) {
			root, _, _ := savedPrePatchAsk(t, kind)
			path := chat.AskPath(root, chat.TaskName("XR-008"))
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			raw, err := runCLI(t, "-C", root, "draft", "prepared", "check-ask", "XR-008")
			if err != nil {
				t.Fatalf("check-ask: %s %v", raw, err)
			}
			var shown chat.PreparedQuestion
			if json.Unmarshal([]byte(raw), &shown) != nil || len(shown.Sources) != 1 || shown.Sources[0].Revision < 1 {
				t.Fatal(raw)
			}
			after, _ := os.ReadFile(path)
			if string(before) != string(after) {
				t.Fatal("read refreshed the shown Ask")
			}
			recordPath := draftPath(root, "XR-008")
			record, _ := os.ReadFile(recordPath)
			if err := os.WriteFile(recordPath, append(record, []byte("\nИзменённый материал\n")...), 0600); err != nil {
				t.Fatal(err)
			}
			if raw, err := runCLI(t, "-C", root, "draft", "prepared", "check-ask", "XR-008"); err == nil {
				t.Fatalf("stale accepted: %s", raw)
			}
			after, _ = os.ReadFile(path)
			if string(before) != string(after) {
				t.Fatal("stale check changed Ask")
			}
		})
	}
}

func TestPreparedDeliveryModes(t *testing.T) {
	for _, mode := range []string{"panel", "dialog", "text"} {
		t.Run(mode, func(t *testing.T) {
			root, _, _ := savedPrePatchAsk(t, "authored-drop")
			env := func(key string) string {
				if mode == "panel" && key == "DEVKIT_TMUX" {
					return "task-XR-008"
				}
				if mode == "text" && key == "DEVKIT_HEADLESS" {
					return "стенд"
				}
				return ""
			}
			raw, err := cmdPreparedChatAsk(root, "XR-008", nil, askDeps{Main: root}, env)
			if err != nil {
				t.Fatal(err)
			}
			var out struct {
				Delivery string        `json:"delivery"`
				Options  []chat.Option `json:"options"`
			}
			if json.Unmarshal([]byte(raw), &out) != nil || out.Delivery != mode || len(out.Options) != 1 || !out.Options[0].Recommended {
				t.Fatal(raw)
			}
		})
	}
}
