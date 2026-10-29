package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dronrider/devkit/internal/chat"
	"github.com/dronrider/devkit/internal/draftproposal"
)

func TestProposalAnswerContext(t *testing.T) {
	q := &chat.ProposalQuestion{Version: 1, Tree: "/tmp/tree", Anchor: "XR-1", Sources: []chat.ProposalSource{{Source: "XR-1", Basis: "old", Proposals: []draftproposal.Proposal{{ID: "p", Revision: 1, Outcome: "drop"}}}}}
	if proposalFromAsk(chat.Ask{Task: "XR-1", Questions: []chat.Question{{Text: "q", Proposal: q}}}) != q {
		t.Fatal("shown question lost")
	}
	if proposalFromAsk(chat.Ask{Task: "XR-1", Questions: []chat.Question{{Text: "q", Scenario: &chat.ScenarioQuestion{}}}}) != nil {
		t.Fatal("scenario is not proposal")
	}
	if proposalFromAsk(chat.Ask{Task: "XR-2", Questions: []chat.Question{{Text: "q", Proposal: q}}}) != nil {
		t.Fatal("wrong anchor")
	}
	if _, err := proposalAnswerInput(proposalAnswer{}); err == nil {
		t.Fatal("missing context")
	}
}
func TestProposalHelpersNode(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node недоступен")
	}
	shown := chat.ProposalQuestion{Version: 1, Tree: "/worktree", Anchor: "XR-1", Sources: []chat.ProposalSource{
		{Source: "XR-1", Basis: "old", Proposals: []draftproposal.Proposal{{ID: "a", Revision: 1, Outcome: "add"}, {ID: "b", Revision: 2, Outcome: "add", After: []string{"a"}}}},
		{Source: "XR-2", Basis: "basis2", Proposals: []draftproposal.Proposal{{ID: "c", Revision: 1, Outcome: "drop"}}},
	}}
	data, err := json.Marshal(shown)
	if err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(node, filepath.Join("testdata", "proposal_helpers.mjs"), filepath.Join("static", "app.js"), string(data)).CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	out, err = exec.Command(node, filepath.Join("testdata", "proposal_ui.mjs"), filepath.Join("static", "app.js"), string(data)).CombinedOutput()
	if err != nil {
		t.Fatalf("UI %v: %s", err, out)
	}
}

func TestProposalSayRequiresTypedContext(t *testing.T) {
	st := newScenarioStand(t)
	q := &chat.ProposalQuestion{Version: 1, Tree: st.e.proj, Anchor: "XR-4", Sources: []chat.ProposalSource{{Source: "XR-4", Basis: "basis", Proposals: []draftproposal.Proposal{{ID: "p", Revision: 1, Outcome: "drop", Text: "показано"}}}}}
	if err := chat.WriteAsk(st.e.proj, chat.TaskName("XR-4"), chat.Ask{Task: "XR-4", Session: scenarioPanelSID, Questions: []chat.Question{{Text: "вопрос", Proposal: q}}}); err != nil {
		t.Fatal(err)
	}
	before := readFile(t, st.path)
	for _, context := range []string{"", `,"proposal":null`, `,"scenario":{"task":"XR-4","fork":"x","snapshot":"x"}`} {
		r := doReq(t, st.c, "POST", st.e.srv.URL+"/api/projects/demo/chats/"+scenarioPanelSID+"/say", `{"text":"принять"`+context+`}`)
		if r.StatusCode != 400 {
			t.Fatalf("context %s: %d %s", context, r.StatusCode, body(t, r))
		}
		r.Body.Close()
		if readFile(t, st.path) != before {
			t.Fatal("source changed")
		}
	}
	a := agentAskOf(handedAsk{Tree: st.e.proj, Ask: chat.Ask{Task: "XR-4", Questions: []chat.Question{{Text: "вопрос", Proposal: q}}}})
	if a.Proposal == nil || a.Tree != st.e.proj {
		t.Fatalf("missing question: %+v", a)
	}
}

func TestGroomTypedPromptUsesProjectDefinition(t *testing.T) {
	e, _, _ := tasksEnv(t)
	runTaskctl(t, e.proj, "draft", "синтетическая запись")
	dir := filepath.Join(e.proj, ".devkit", "draft-types")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	def := "name = \"custom\"\ntitle = \"Особый\"\nversion = 1\nprompt = \"Собери особые поля\"\ndefault_type = \"task\"\nrequired = [\"title\"]\n"
	if err := os.WriteFile(filepath.Join(dir, "custom.toml"), []byte(def), 0644); err != nil {
		t.Fatal(err)
	}
	runTaskctl(t, e.proj, "draft", "meta", "XR-005", "--type", "custom")
	prompt, err := groomTypedPrompt(e.proj, "XR-005", "", draftModeRun)
	if err != nil || !strings.Contains(prompt, "Собери особые поля") || !strings.Contains(prompt, "принятые исходы add") {
		t.Fatalf("%s %v", prompt, err)
	}
	if err = os.WriteFile(filepath.Join(dir, "custom.toml"), []byte("broken"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err = groomTypedPrompt(e.proj, "XR-005", "", draftModeRun); err == nil {
		t.Fatal("invalid definition accepted")
	}
}
