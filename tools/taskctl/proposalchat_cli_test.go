package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dronrider/devkit/internal/chat"
	"github.com/dronrider/devkit/internal/draftproposal"
)

func TestProposalChatActualCLI(t *testing.T) {
	root := setup(t)
	kit, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEVKIT_HOME", kit)
	for _, id := range []string{"XR-008", "XR-009"} {
		if _, err = cmdDraft(root, "материал "+id, "mid", CommitOpts{}); err != nil {
			t.Fatal(err)
		}
		raw, err := cmdDraftProposals(root, id)
		if err != nil {
			t.Fatal(err)
		}
		var view proposalView
		if err = json.Unmarshal([]byte(raw), &view); err != nil {
			t.Fatal(err)
		}
		if _, err = cmdDraftProposalsSave(root, id, view.Snapshot, []byte(`{"proposals":[{"id":"p1","revision":1,"outcome":"drop","text":"предложение","reason":"лишнее"}]}`)); err != nil {
			t.Fatal(err)
		}
	}
	bin := filepath.Join(t.TempDir(), "taskctl")
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Env = append(os.Environ(), "GOWORK=off")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, out)
	}
	run := func(input []byte, args ...string) (string, error) {
		t.Helper()
		cmd := exec.Command(bin, append([]string{"-C", root}, args...)...)
		cmd.Env = append(os.Environ(), "CLAUDE_CODE_SESSION_ID=cli-stand", "DEVKIT_TMUX=task-stand")
		cmd.Stdin = bytes.NewReader(input)
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	out, err := run(nil, "draft", "proposals", "ask", "XR-008", "--also", "XR-009")
	if err != nil {
		t.Fatalf("ask: %s %v", out, err)
	}
	var q chat.Question
	if err = json.Unmarshal([]byte(out), &q); err != nil || q.Proposal == nil || len(q.Proposal.Sources) != 2 {
		t.Fatalf("question: %s %v", out, err)
	}
	a := proposalChatAnswer{Tree: root, Snapshots: []draftproposal.Snapshot{{Source: "XR-008", Digest: q.Proposal.Sources[0].Basis}}, Decisions: []draftproposal.Decision{{Source: "XR-008", Proposal: "p1", Revision: 1, Action: "reject", Reason: "не нужно"}}}
	input, _ := json.Marshal(a)
	out, err = run(input, "draft", "proposals", "answer", "XR-008")
	if err != nil || !strings.Contains(out, `"repeated":false`) {
		t.Fatalf("answer %s %v", out, err)
	}
	if err = chat.DropAsk(root, chat.TaskName("XR-008")); err != nil {
		t.Fatal(err)
	}
	out, err = run(input, "draft", "proposals", "answer", "--repeat-only", "XR-008")
	if err != nil || !strings.Contains(out, `"repeated":true`) {
		t.Fatalf("replay %s %v", out, err)
	}
	a.Decisions[0].Reason = "новая причина"
	input, _ = json.Marshal(a)
	if out, err = run(input, "draft", "proposals", "answer", "XR-008", "--repeat-only"); err == nil {
		t.Fatalf("new mutation without Ask: %s", out)
	}
}
