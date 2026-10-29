package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dronrider/devkit/internal/chat"
	"github.com/dronrider/devkit/internal/draftmeta"
	"github.com/dronrider/devkit/internal/draftproposal"
)

func TestProposalDeliveryReservationFencesPrivateAttach(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "docs", "tasks", "XR-1.md")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	public := []byte("# XR-1: запись\n")
	if err := os.WriteFile(path, public, 0600); err != nil {
		t.Fatal(err)
	}
	snapshots := []draftproposal.Snapshot{{Source: "XR-1", Digest: "shown"}}
	release, err := proposalDeliveryReservation(root, snapshots)
	if err != nil {
		t.Fatal(err)
	}
	if l, err := draftmeta.CloudExclusive(root, "XR-1"); err == nil {
		l.Close()
		release()
		t.Fatal("attach passed during delivery")
	}
	release()
	l, err := draftmeta.CloudExclusive(root, "XR-1")
	if err != nil {
		t.Fatal(err)
	}
	private, err := draftmeta.ProtectRecord(public)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, private, 0600); err != nil {
		t.Fatal(err)
	}
	l.Close()
	if release, err = proposalDeliveryReservation(root, snapshots); err == nil {
		release()
		t.Fatal("private reply allowed")
	}
}

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

func TestProposalCommentThenActualApply(t *testing.T) {
	st := newScenarioStand(t)
	putDoc(t, st.e.proj, "docs/TASKS-archive.md", "# Архив\n\n| ID | Задача | Тип | P | Закрыто | Ссылка |\n|--------|--------|-----|---|---------|--------|\n")
	kit, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEVKIT_HOME", kit)
	cli := func(input string, args ...string) string {
		t.Helper()
		cmd := exec.Command(st.bin, append([]string{"-C", st.e.proj}, args...)...)
		cmd.Env = append(os.Environ(), "CLAUDE_CODE_SESSION_ID="+scenarioPanelSID, "DEVKIT_TMUX=task-test")
		cmd.Stdin = strings.NewReader(input)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%v: %s %v", args, out, err)
		}
		return string(out)
	}
	var view struct {
		Snapshot string `json:"snapshot"`
	}
	if err = json.Unmarshal([]byte(cli("", "draft", "proposals", "XR-4")), &view); err != nil {
		t.Fatal(err)
	}
	cli(`{"proposals":[{"id":"p1","revision":1,"outcome":"drop","text":"первое","reason":"лишнее"}]}`, "draft", "proposals", "save", "XR-4", "--base", view.Snapshot)
	var q chat.Question
	raw := cli("", "draft", "proposals", "ask", "XR-4")
	if err = json.Unmarshal([]byte(raw), &q); err != nil || q.Proposal == nil {
		t.Fatalf("ask %s %v", raw, err)
	}
	answer := proposalAnswer{Mode: "comment", Anchor: "XR-4", Tree: st.e.proj, Snapshots: []draftproposal.Snapshot{{Source: "XR-4", Digest: q.Proposal.Sources[0].Basis}}}
	before := readFile(t, st.path)
	askPath := chat.AskPath(st.e.proj, chat.TaskName("XR-4"))
	askBefore := readFile(t, askPath)
	req, _ := json.Marshal(map[string]any{"text": "почему этот исход?", "msg_id": "comment-one", "proposal": answer})
	resp := doReq(t, st.c, "POST", st.e.srv.URL+"/api/projects/demo/chats/"+scenarioPanelSID+"/say", string(req))
	msg := body(t, resp)
	if resp.StatusCode != 200 {
		t.Fatalf("comment %d %s", resp.StatusCode, msg)
	}
	if readFile(t, st.path) != before || readFile(t, askPath) != askBefore {
		t.Fatal("comment changed source or question")
	}
	lines := chat.ReadLines(chat.Path(st.e.proj, chat.TaskName("XR-4")))
	if len(lines) != 1 || !strings.Contains(lines[0], "почему этот исход?") {
		t.Fatalf("delivery %v", lines)
	}
	answer.Mode = "apply"
	answer.Decisions = []draftproposal.Decision{{Source: "XR-4", Proposal: "p1", Revision: 1, Action: "reject", Reason: "не нужно"}}
	for _, wrong := range []proposalAnswer{
		{Mode: "apply", Anchor: answer.Anchor, Tree: answer.Tree, Snapshots: []draftproposal.Snapshot{{Source: "XR-4", Digest: "stale"}}, Decisions: answer.Decisions},
		{Mode: "apply", Anchor: answer.Anchor, Tree: answer.Tree, Snapshots: answer.Snapshots, Decisions: []draftproposal.Decision{{Source: "XR-4", Proposal: "unseen", Revision: 1, Action: "accept"}}},
	} {
		bad, _ := json.Marshal(map[string]any{"text": "ошибка", "proposal": wrong})
		resp := doReq(t, st.c, "POST", st.e.srv.URL+"/api/projects/demo/chats/"+scenarioPanelSID+"/say", string(bad))
		if resp.StatusCode < 400 {
			t.Fatalf("invalid context accepted: %s", body(t, resp))
		}
		resp.Body.Close()
		if readFile(t, st.path) != before {
			t.Fatal("invalid context mutated source")
		}
	}
	req, _ = json.Marshal(map[string]any{"text": "отклонить", "msg_id": "apply-one", "proposal": answer})
	carrier := chat.Path(st.e.proj, chat.TaskName("XR-4"))
	if err := os.Remove(carrier); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(carrier, 0700); err != nil {
		t.Fatal(err)
	}
	resp = doReq(t, st.c, "POST", st.e.srv.URL+"/api/projects/demo/chats/"+scenarioPanelSID+"/say", string(req))
	msg = body(t, resp)
	if resp.StatusCode != 502 {
		t.Fatalf("apply with failed delivery %d %s", resp.StatusCode, msg)
	}
	applied := readFile(t, st.path)
	if err := os.Remove(carrier); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(askPath); err != nil {
		t.Fatal(err)
	}
	resp = doReq(t, st.c, "POST", st.e.srv.URL+"/api/projects/demo/chats/"+scenarioPanelSID+"/say", string(req))
	msg = body(t, resp)
	if resp.StatusCode != 200 || readFile(t, st.path) != applied {
		t.Fatalf("receipt-only retry %d %s", resp.StatusCode, msg)
	}
	lines = chat.ReadLines(carrier)
	if len(lines) != 1 || !strings.Contains(lines[0], `"repeated":true`) {
		t.Fatalf("retry delivery %v", lines)
	}
	replacement := chat.Ask{Task: "XR-4", Session: scenarioPanelSID, Questions: []chat.Question{{Text: "новый вопрос"}}}
	if err := chat.WriteAsk(st.e.proj, chat.TaskName("XR-4"), replacement); err != nil {
		t.Fatal(err)
	}
	req, _ = json.Marshal(map[string]any{"text": "отклонить", "msg_id": "apply-retry-after-replace", "proposal": answer})
	resp = doReq(t, st.c, "POST", st.e.srv.URL+"/api/projects/demo/chats/"+scenarioPanelSID+"/say", string(req))
	msg = body(t, resp)
	if resp.StatusCode != 200 || readFile(t, st.path) != applied {
		t.Fatalf("replacement retry %d %s", resp.StatusCode, msg)
	}
	newAsk, ok := chat.ReadAsk(askPath)
	if !ok || len(newAsk.Questions) != 1 || newAsk.Questions[0].Text != "новый вопрос" {
		t.Fatal("replacement Ask removed")
	}
	var result struct {
		State draftproposal.State `json:"state"`
	}
	raw = cli("", "draft", "proposals", "XR-4")
	if err = json.Unmarshal([]byte(raw), &result); err != nil || result.State.Proposals[0].Status != "rejected" {
		t.Fatalf("state %s %v", raw, err)
	}
}
