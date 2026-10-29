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
	"github.com/dronrider/devkit/internal/stage"
)

func TestProposalWorktreeHandoffRealCLI(t *testing.T) {
	st := newScenarioStand(t)
	putDoc(t, st.e.proj, "docs/TASKS-archive.md", "# Архив\n\n| ID | Задача | Тип | P | Закрыто | Ссылка |\n|--------|--------|-----|---|---------|--------|\n")
	kit, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEVKIT_HOME", kit)
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-c", "user.name=Stand", "-c", "user.email=stand@example.invalid", "-C", st.e.proj}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s %v", args, out, err)
		}
	}
	git("init")
	git("add", "--", "docs")
	git("commit", "-m", "synthetic fixture")
	tree := filepath.Join(filepath.Dir(st.e.proj), "demo-branch")
	git("worktree", "add", "-b", "stand", tree)
	mainInfo, mainErr := os.Stat(stage.MainRoot(tree))
	projInfo, projErr := os.Stat(st.e.proj)
	if mainErr != nil || projErr != nil || !os.SameFile(mainInfo, projInfo) {
		t.Fatalf("wrong main root: %s", stage.MainRoot(tree))
	}
	source := filepath.Join(tree, "docs", "tasks", "drafts", "XR-4.md")
	mainBefore := readFile(t, st.path)
	cli := func(input string, args ...string) string {
		t.Helper()
		cmd := exec.Command(st.bin, append([]string{"-C", tree}, args...)...)
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
	if err := json.Unmarshal([]byte(cli("", "draft", "proposals", "XR-4")), &view); err != nil {
		t.Fatal(err)
	}
	cli(`{"proposals":[{"id":"p1","revision":1,"outcome":"drop","text":"веточный результат","reason":"устарело"}]}`, "draft", "proposals", "save", "XR-4", "--base", view.Snapshot)
	var question chat.Question
	if err := json.Unmarshal([]byte(cli("", "draft", "proposals", "ask", "XR-4")), &question); err != nil || question.Proposal == nil {
		t.Fatalf("question: %+v %v", question, err)
	}
	writeBinds(t, st.e.home, "2026-10-30T12:00:00 сессия "+scenarioPanelSID+" задача XR-4 проект demo дерево "+tree+" транскрипт "+standTranscript(st.e.home, "t")+" источник заказ повод startup tmux -\n")
	answer := proposalAnswer{Anchor: "XR-4", Tree: tree, Snapshots: []draftproposal.Snapshot{{Source: "XR-4", Digest: question.Proposal.Sources[0].Basis}}, Decisions: []draftproposal.Decision{{Source: "XR-4", Proposal: "p1", Revision: 1, Action: "reject", Reason: "не нужно"}}}
	data, _ := json.Marshal(map[string]any{"text": "отклонить веточный результат", "msg_id": "tree-answer", "proposal": answer})
	resp := doReq(t, st.c, "POST", st.e.srv.URL+"/api/projects/demo/chats/"+scenarioPanelSID+"/say", string(data))
	got := body(t, resp)
	if resp.StatusCode != 200 {
		t.Fatalf("worktree answer: %d %s", resp.StatusCode, got)
	}
	state, err := draftproposal.Parse([]byte(readFile(t, source)))
	if err != nil || state.Proposals[0].Status != "rejected" || readFile(t, st.path) != mainBefore {
		t.Fatalf("wrong source: %+v %v", state, err)
	}
	applied := readFile(t, source)
	carrier := chat.Path(st.e.proj, chat.TaskName("XR-4"))
	lines := chat.ReadLines(carrier)
	if len(lines) != 1 || !strings.Contains(lines[0], "веточный результат") {
		t.Fatalf("main carrier: %v", lines)
	}
	other := filepath.Join(filepath.Dir(st.e.proj), "other")
	mkProject(t, other)
	putDoc(t, other, "docs/tasks/XR-4.md", "# XR-4: другое дерево\n")
	writeBinds(t, st.e.home, "2026-10-30T12:01:00 сессия "+scenarioPanelSID+" задача XR-4 проект other дерево "+other+" транскрипт "+standTranscript(st.e.home, "t")+" источник заказ повод startup tmux -\n")
	data, _ = json.Marshal(map[string]any{"text": "повтор после смены дерева", "msg_id": "tree-retry", "proposal": answer})
	resp = doReq(t, st.c, "POST", st.e.srv.URL+"/api/projects/demo/chats/"+scenarioPanelSID+"/say", string(data))
	got = body(t, resp)
	if resp.StatusCode != 200 || readFile(t, source) != applied || readFile(t, st.path) != mainBefore {
		t.Fatalf("worktree retry: %d %s", resp.StatusCode, got)
	}
	lines = chat.ReadLines(carrier)
	if len(lines) < 2 || !strings.Contains(lines[len(lines)-1], "повтор после смены дерева") {
		t.Fatalf("old carrier: %v", lines)
	}
	if got := chat.ReadLines(chat.Path(other, chat.TaskName("XR-4"))); len(got) != 0 {
		t.Fatalf("redirected to other project: %v", got)
	}
}
