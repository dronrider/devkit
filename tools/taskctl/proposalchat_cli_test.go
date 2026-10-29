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
		cards := []draftproposal.Proposal{{ID: "p1", Revision: 1, Outcome: "drop", Text: "предложение", Reason: "лишнее"}}
		if id == "XR-008" {
			cards = append(cards, draftproposal.Proposal{ID: "p2", Revision: 1, Outcome: "add", Title: "Результат", Text: fixtureTaskBody, BoardType: "task", Rank: "0+1+1+0+1", Size: "M", Accept: "agent", After: []string{"p1"}})
		}
		save, _ := json.Marshal(struct {
			Proposals []draftproposal.Proposal `json:"proposals"`
		}{cards})
		if _, err = cmdDraftProposalsSave(root, id, view.Snapshot, save); err != nil {
			t.Fatal(err)
		}
	}
	bin := os.Getenv("TASKCTL_ACCEPT_BIN")
	if bin == "" {
		bin = filepath.Join(t.TempDir(), "taskctl")
		build := exec.Command("go", "build", "-o", bin, ".")
		build.Env = append(os.Environ(), "GOWORK=off")
		if out, err := build.CombinedOutput(); err != nil {
			t.Fatalf("build: %v %s", err, out)
		}
	}
	run := func(input []byte, args ...string) (string, error) {
		t.Helper()
		cmd := exec.Command(bin, append([]string{"-C", root}, args...)...)
		cmd.Env = append(os.Environ(), "CLAUDE_CODE_SESSION_ID=cli-stand", "DEVKIT_TMUX=task-stand")
		cmd.Stdin = bytes.NewReader(input)
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	second := draftPath(root, "XR-009")
	public, err := os.ReadFile(second)
	if err != nil {
		t.Fatal(err)
	}
	protected := strings.Replace(string(public), "\n", "\nзамок: locked\n", 1)
	if err = os.WriteFile(second, []byte(protected), 0600); err != nil {
		t.Fatal(err)
	}
	if leaked, err := run(nil, "draft", "proposals", "ask", "XR-008", "--also", "XR-009"); err == nil || strings.Contains(leaked, "предложение") {
		t.Fatalf("locked shown source reached agent: %s %v", leaked, err)
	}
	if _, ok := chat.ReadAsk(chat.AskPath(root, chat.TaskName("XR-008"))); ok {
		t.Fatal("private question was published")
	}
	if err = os.WriteFile(second, public, 0600); err != nil {
		t.Fatal(err)
	}
	out, err := run(nil, "draft", "proposals", "ask", "XR-008", "--also", "XR-009")
	if err != nil {
		t.Fatalf("ask: %s %v", out, err)
	}
	var q chat.Question
	if err = json.Unmarshal([]byte(out), &q); err != nil || q.Proposal == nil || len(q.Proposal.Sources) != 2 {
		t.Fatalf("question: %s %v", out, err)
	}
	t.Setenv("DEVKIT_GROOM_SOURCE", "XR-008")
	t.Setenv("DEVKIT_GROOM_MODE", "run")
	if denied, err := run(nil, "run", "XR-001"); err == nil || !strings.Contains(denied, "только принятым результатам add") {
		t.Fatalf("run before confirmation: %s %v", denied, err)
	}
	t.Setenv("DEVKIT_GROOM_SOURCE", "")
	t.Setenv("DEVKIT_GROOM_MODE", "")
	a := proposalChatAnswer{Tree: root, Snapshots: []draftproposal.Snapshot{{Source: "XR-008", Digest: q.Proposal.Sources[0].Basis}}, Decisions: []draftproposal.Decision{{Source: "XR-008", Proposal: "p1", Revision: 1, Action: "reject", Reason: "не нужно"}}}
	bad := a
	bad.Decisions = []draftproposal.Decision{{Source: "XR-008", Proposal: "p2", Revision: 1, Action: "accept"}}
	badInput, _ := json.Marshal(bad)
	before, err := os.ReadFile(draftPath(root, "XR-008"))
	if err != nil {
		t.Fatal(err)
	}
	if got, err := run(badInput, "draft", "proposals", "answer", "XR-008"); err == nil {
		t.Fatalf("hidden predecessor accepted: %s", got)
	}
	after, err := os.ReadFile(draftPath(root, "XR-008"))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("hidden predecessor changed source")
	}
	bad = a
	bad.Tree = t.TempDir()
	badInput, _ = json.Marshal(bad)
	if got, err := run(badInput, "draft", "proposals", "answer", "XR-008"); err == nil {
		t.Fatalf("wrong tree accepted: %s", got)
	}
	after, err = os.ReadFile(draftPath(root, "XR-008"))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("wrong tree changed source")
	}
	input, _ := json.Marshal(a)
	jq := exec.Command("jq", `{tree: .proposal.tree, snapshots: [{source: .proposal.sources[0].source, digest: .proposal.sources[0].basis}], decisions: [{source: .proposal.sources[0].source, proposal: .proposal.sources[0].proposals[0].id, revision: .proposal.sources[0].proposals[0].revision, action: "reject", reason: "не нужно"}]}`)
	jq.Stdin = strings.NewReader(out)
	docInput, err := jq.Output()
	if err != nil {
		t.Fatalf("README jq: %v", err)
	}
	var fromDocs proposalChatAnswer
	if err := json.Unmarshal(docInput, &fromDocs); err != nil || fromDocs.Tree != a.Tree || len(fromDocs.Decisions) != 1 || fromDocs.Decisions[0] != a.Decisions[0] || len(fromDocs.Snapshots) != 1 || fromDocs.Snapshots[0] != a.Snapshots[0] {
		t.Fatalf("README answer: %s %v", docInput, err)
	}
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
