package main

import (
	"strings"
	"testing"

	"github.com/dronrider/devkit/internal/chat"
	"github.com/dronrider/devkit/internal/draftproposal"
)

func TestChatStorePreservesProposalDeliveryContext(t *testing.T) {
	e := newTestEnv(t)
	const sid = "aa144600-1111-4111-8111-111111111111"
	stale := e.s.chatStoreRead(sid)
	first := proposalDeliveryContext{Key: "key", Tree: e.proj, Anchor: "XR-4", Sources: []string{"XR-4", "XR-5"}}
	if err := e.s.chatStoreWrite(sid, chatStore{ProposalContexts: []proposalDeliveryContext{first}}); err != nil {
		t.Fatal(err)
	}
	stale.Seen = 1
	if err := e.s.chatStoreWrite(sid, stale); err != nil {
		t.Fatal(err)
	}
	got := e.s.chatStoreRead(sid)
	if len(got.ProposalContexts) != 1 || len(got.ProposalContexts[0].Sources) != 2 {
		t.Fatalf("lost delivery context: %+v", got)
	}
	other := proposalDeliveryContext{Key: "key", Tree: e.proj, Anchor: "XR-4", Sources: []string{"XR-6"}}
	if err := e.s.chatStoreWrite(sid, chatStore{ProposalContexts: []proposalDeliveryContext{other}}); err != nil {
		t.Fatal(err)
	}
	got = e.s.chatStoreRead(sid)
	if len(got.ProposalContexts) != 1 || len(got.ProposalContexts[0].Sources) != 3 {
		t.Fatalf("shown sources lost: %+v", got)
	}
}

func TestGroomContextSurvivesResumeWithoutLeakingToAnotherTask(t *testing.T) {
	e := newTestEnv(t)
	const sid = "aa144600-1111-4111-8111-111111111111"
	writeBinds(t, e.home, "2026-10-30T12:00:00 сессия "+sid+" задача XR-4 проект demo дерево "+e.proj+" транскрипт "+standTranscript(e.home, "t")+" источник заказ повод startup tmux draft-XR-4\n")
	if err := e.s.chatStoreWrite("tmux-draft-XR-4", chatStore{GroomSource: "XR-4", GroomMode: "run", GroomTree: e.proj}); err != nil {
		t.Fatal(err)
	}
	if err := e.s.preserveGroomContext(sid, "XR-4"); err != nil {
		t.Fatal(err)
	}
	if err := e.s.chatStoreWrite("tmux-draft-XR-4", chatStore{Model: "new occupant"}); err != nil {
		t.Fatal(err)
	}
	q := chat.ProposalQuestion{Version: 1, Tree: e.proj, Anchor: "XR-4", Sources: []chat.ProposalSource{{Source: "XR-4", Basis: "basis", Proposals: []draftproposal.Proposal{{ID: "p", Revision: 1, Outcome: "drop", Text: "карточка"}}}}}
	if err := e.s.rememberProposalShown(sid, q); err != nil {
		t.Fatal(err)
	}
	env := e.s.launchEnv("XR-4", "chat-resume", sid)
	if !strings.Contains(env, "DEVKIT_GROOM_SOURCE='XR-4'") || !strings.Contains(env, "DEVKIT_GROOM_MODE='run'") {
		t.Fatalf("resume lost gate: %s", env)
	}
	if env = e.s.launchEnv("XR-100", "chat-other", sid); strings.Contains(env, "DEVKIT_GROOM_SOURCE='XR-4'") {
		t.Fatalf("gate crossed task binding: %s", env)
	}
}
