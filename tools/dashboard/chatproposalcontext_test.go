package main

import (
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dronrider/devkit/internal/chat"
	"github.com/dronrider/devkit/internal/draftproposal"
)

func TestProposalShownPreservesConcurrentModelChoice(t *testing.T) {
	e, c := appEnv(t)
	catalog := strings.Replace(appHarnessJSON, `"protocol": "codex", "models":`,
		`"chat": ["gpt-6.2-sol"], "protocol": "codex", "models":`, 1)
	writeScript(t, e.bin, "agentctl", `printf '%s' '`+catalog+`'`)
	e.s.harnesses()
	const sid = "as-concurrent-choice"
	before := chatStore{Model: "gpt-6.1-sol", Harness: "codextest", Protocol: "codex", Project: "demo", Thread: "saved-context",
		ProposalContexts: []proposalDeliveryContext{{Key: "receipt", Tree: e.proj, Anchor: "XR-3", Sources: []string{"XR-3"}}}}
	if err := e.s.chatStoreWrite(sid, before); err != nil {
		t.Fatal(err)
	}
	q := chat.PreparedQuestion{Version: 1, Tree: e.proj, Anchor: "XR-4", Sources: []chat.PreparedSource{{Source: "XR-4", Basis: "basis", Proposals: []draftproposal.Proposal{{ID: "p", Revision: 1, Outcome: "drop"}}}}}
	entered, release, finished := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	originalHome := realHomeFn
	var held atomic.Bool
	realHomeFn = func() string {
		if held.CompareAndSwap(false, true) {
			close(entered)
			<-release
		}
		return e.home
	}
	released := false
	defer func() {
		if !released {
			close(release)
			<-finished
		}
		realHomeFn = originalHome
	}()
	go func() { finished <- e.s.rememberProposalShown(sid, q) }()
	select {
	case <-entered:
	case err := <-finished:
		released = true
		t.Fatalf("writer не дошёл до барьера: %v", err)
	case <-time.After(8 * time.Second):
		t.Fatal("writer не начал читать происхождение вопроса")
	}
	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/model", `{"model":"gpt-6.2-sol","harness":"codextest"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("выбор модели: %d %s", resp.StatusCode, body(t, resp))
	}
	resp.Body.Close()
	close(release)
	released = true
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	after := e.s.chatStoreRead(sid)
	if after.Model != "gpt-6.2-sol" || after.Harness != before.Harness || after.Thread != before.Thread || len(after.ProposalContexts) != 2 {
		t.Fatalf("контекст вопроса откатил модель или потерял квитанцию: %+v", after)
	}
}

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
	// Тела задач в показанном вопросе нет (DK-1469, решение 4): вопрос несёт
	// ссылки, а описание читается на экране задачи.
	q := chat.PreparedQuestion{Version: 1, Tree: e.proj, Anchor: "XR-4", Sources: []chat.PreparedSource{{Source: "XR-4", Basis: "basis", Proposals: []draftproposal.Proposal{{ID: "p", Revision: 1, Outcome: "drop"}}}}}
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
