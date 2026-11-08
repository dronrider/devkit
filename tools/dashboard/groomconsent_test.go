package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dronrider/devkit/internal/chat"
	"github.com/dronrider/devkit/internal/draftproposal"
)

func TestGroomConsentWaiting(t *testing.T) {
	for _, selection := range []string{"all", "one-source"} {
		t.Run(selection, func(t *testing.T) { groomConsentWaiting(t, selection) })
	}
}

func groomConsentWaiting(t *testing.T, selection string) {
	st := newScenarioStand(t)
	kit, _ := filepath.Abs("../..")
	t.Setenv("DEVKIT_HOME", kit)
	putDoc(t, st.e.proj, "docs/TASKS-archive.md", "# Архив\n\n| ID | Задача | Тип | P | Закрыто | Ссылка |\n|---|---|---|---|---|---|\n")
	cli := func(input string, args ...string) string {
		t.Helper()
		cmd := exec.Command(st.bin, append([]string{"-C", st.e.proj}, args...)...)
		cmd.Env = append(os.Environ(), "CLAUDE_CODE_SESSION_ID="+scenarioPanelSID, "DEVKIT_TMUX=task-XR-4")
		cmd.Stdin = strings.NewReader(input)
		raw, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%v: %s %v", args, raw, err)
		}
		return string(raw)
	}
	material := "## Что происходит\n\nПроверка записи.\n\n## Чего хотим\n\nЗапись проверяется.\n\n## DoD\n\n- Видно результат.\n\n## Ранг\n\n`0+1+1+0+1 = 3`, P3.\n\n- Серьёзность 0: работа продолжается.\n- Ценность 1: один сценарий.\n- Неопределённость 1: место известно.\n- Поправка на баг 0: новая работа.\n- Рычаг 1: убирает повтор.\n"
	for _, id := range []string{"XR-4", "XR-5"} {
		if id == "XR-5" {
			putDoc(t, st.e.proj, "docs/tasks/drafts/XR-5.md", "# XR-5: соседняя запись\n")
		}
		var view struct {
			Snapshot string `json:"snapshot"`
		}
		if json.Unmarshal([]byte(cli("", "draft", "prepared", id)), &view) != nil {
			t.Fatal("view")
		}
		proposals := []draftproposal.Proposal{{ID: "drop", Revision: 1, Outcome: "drop", Reason: "устарело"}}
		if id == "XR-4" {
			for _, key := range []string{"one", "two"} {
				proposals = append(proposals, draftproposal.Proposal{ID: key, Revision: 1, Outcome: "add", Title: "Задача " + key, Text: material, BoardType: "task", Rank: "0+1+1+0+1", Size: "S", Accept: "agent"})
			}
		}
		payload, _ := json.Marshal(map[string]any{"complete": true, "conclusion": "Предлагаю удалить лишнее и создать задачи.", "proposals": proposals})
		cli(string(payload), "draft", "prepared", "save", id, "--base", view.Snapshot)
	}
	printed := cli("", "draft", "prepared", "ask", "XR-4", "--also", "XR-5")
	var question chat.Question
	if err := json.Unmarshal([]byte(printed), &question); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	event := func(id, name string, input any) string {
		raw, _ := json.Marshal(map[string]any{"type": "assistant", "timestamp": now.Format(time.RFC3339Nano), "message": map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "tool_use", "id": id, "name": name, "input": input}}}})
		return string(raw) + "\n"
	}
	nativeInput := map[string]any{"questions": []any{
		map[string]any{"header": "XR-4", "question": "Применить разбор XR-4?", "multiSelect": false, "options": []any{map[string]string{"label": "Подтвердить (Recommended)"}, map[string]string{"label": "Отклонить"}}},
		map[string]any{"header": "XR-5", "question": "Применить разбор XR-5?", "multiSelect": false, "options": []any{map[string]string{"label": "Подтвердить (Recommended)"}, map[string]string{"label": "Отклонить"}}},
	}}
	native := event("native-consent", "AskUserQuestion", nativeInput)
	transcript := writeSession(t, st.e.home, st.e.proj, "", scenarioPanelSID, plainTalk+native, now)
	writeBinds(t, st.e.home, fmt.Sprintf("2026-11-08T12:00:00 сессия %s задача XR-4 проект demo дерево %s транскрипт %s источник заказ повод startup tmux task-XR-4\n", scenarioPanelSID, st.e.proj, transcript))
	writePeerAged(t, st.e.home, scenarioPanelSID, "task-XR-4", "waiting", now.UnixMilli())
	keys := filepath.Join(t.TempDir(), "keys")
	writeScript(t, st.e.bin, "tmux", `case "$1" in
ls|list-sessions) printf 'task-XR-4\t1\t1794141800\n';;
send-keys) printf '%s\n' "$*" >> `+shQuote(keys)+`;;
has-session) exit 0;;
esac`)
	initialMaterial := readFile(t, st.path)
	initialAsk, _ := chat.ReadAsk(chat.AskPath(st.e.proj, chat.TaskName("XR-4")))
	mux := http.NewServeMux()
	mux.HandleFunc("/__consent-fixture", func(w http.ResponseWriter, r *http.Request) {
		askPath := chat.AskPath(st.e.proj, chat.TaskName("XR-4"))
		switch r.URL.Query().Get("mode") {
		case "writer":
			if err := os.Remove(askPath); err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			if err := os.WriteFile(st.path, []byte(initialMaterial+"\nПоправка во время записи\n"), 0600); err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			writeSession(t, st.e.home, st.e.proj, "", scenarioPanelSID, plainTalk+native+event("writer", "Bash", map[string]string{"command": "save"}), now)
			writePeerAged(t, st.e.home, scenarioPanelSID, "task-XR-4", "busy", now.UnixMilli())
		case "restore":
			if err := os.WriteFile(st.path, []byte(initialMaterial), 0600); err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			writeSession(t, st.e.home, st.e.proj, "", scenarioPanelSID, plainTalk+native, now)
			writePeerAged(t, st.e.home, scenarioPanelSID, "task-XR-4", "waiting", now.UnixMilli())
			if err := chat.WriteAsk(st.e.proj, chat.TaskName("XR-4"), initialAsk); err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			if err := os.Chtimes(askPath, now.Add(-time.Second), now.Add(-time.Second)); err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
		default:
			http.Error(w, "mode", 400)
			return
		}
		w.WriteHeader(200)
	})
	mux.Handle("/", st.e.s.handler())
	browserServer := httptest.NewServer(mux)
	t.Cleanup(browserServer.Close)
	chrome := findChrome()
	if chrome == "" {
		t.Fatal("Chrome is required for consent symptom")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	browser := func(mode string) {
		t.Helper()
		cmd := exec.Command(node, "testdata/groom_consent_browser.mjs", browserServer.URL, chrome, st.e.cfg.Token, scenarioPanelSID, mode, selection)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("browser %s: %s %v", mode, out, err)
		} else {
			t.Log(string(out))
		}
	}
	browser("surface")
	var delivery struct {
		Delivery string `json:"delivery"`
	}
	json.Unmarshal([]byte(printed), &delivery)
	if delivery.Delivery != "panel" {
		t.Fatalf("panel delivery: %s", printed)
	}
	for _, option := range question.Options {
		if !option.Recommended {
			t.Fatal("source recommendation missing")
		}
	}
	status := func(want bool) {
		t.Helper()
		raw := body(t, doReq(t, st.c, "GET", st.e.srv.URL+"/api/projects/demo/chats/"+scenarioPanelSID+"/status", ""))
		var got struct {
			Busy    bool `json:"busy"`
			Consent bool `json:"consent"`
		}
		if json.Unmarshal([]byte(raw), &got) != nil || got.Consent != want || (want && got.Busy) {
			t.Fatalf("consent=%v: %s", want, raw)
		}
	}
	status(true)
	var project struct {
		Projects []struct {
			Works []map[string]any `json:"works"`
		} `json:"projects"`
	}
	raw := body(t, doReq(t, st.c, "GET", st.e.srv.URL+"/api/projects", ""))
	json.Unmarshal([]byte(raw), &project)
	found := false
	for _, p := range project.Projects {
		for _, w := range p.Works {
			if w["id"] == "XR-4" && w["consent"] == true {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("waiting work lacks consent: %s", raw)
	}
	// A still-open Bash and a real busy peer must keep the writing guard.
	writeSession(t, st.e.home, st.e.proj, "", scenarioPanelSID, plainTalk+native+event("writer", "Bash", map[string]string{"command": "save"}), now)
	status(false)
	browser("writer")
	writeSession(t, st.e.home, st.e.proj, "", scenarioPanelSID, plainTalk+native, now)
	writePeerAged(t, st.e.home, scenarioPanelSID, "task-XR-4", "busy", now.UnixMilli())
	status(false)
	writePeerAged(t, st.e.home, scenarioPanelSID, "task-XR-4", "waiting", now.UnixMilli())
	child := filepath.Join(filepath.Dir(transcript), strings.TrimSuffix(filepath.Base(transcript), ".jsonl"), "subagents", "agent-other.jsonl")
	if err := os.MkdirAll(filepath.Dir(child), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(child, []byte(event("other-tool", "Bash", map[string]string{"command": "save"})), 0600); err != nil {
		t.Fatal(err)
	}
	status(false)
	if err := os.Remove(child); err != nil {
		t.Fatal(err)
	}
	// A native question about another matter is not a duplicate consent.
	unrelated := event("generic-question", "AskUserQuestion", map[string]any{"questions": []any{map[string]any{"header": "XR-4", "question": "Какой срок?", "options": []any{map[string]string{"label": "30 дней"}, map[string]string{"label": "7 дней"}}}}})
	writeSession(t, st.e.home, st.e.proj, "", scenarioPanelSID, plainTalk+unrelated, now)
	status(false)
	writeSession(t, st.e.home, st.e.proj, "", scenarioPanelSID, plainTalk+native, now)
	// The old shown snapshot cannot be used after a material edit.
	materialBefore := readFile(t, st.path)
	putDoc(t, st.e.proj, "docs/tasks/drafts/XR-4.md", materialBefore+"\nНовая мысль\n")
	status(false)
	var stale struct {
		State draftproposal.State `json:"state"`
	}
	json.Unmarshal([]byte(cli("", "draft", "prepared", "XR-4")), &stale)
	var snapshots []draftproposal.Snapshot
	var decisions []draftproposal.Decision
	for _, source := range question.Prepared.Sources {
		snapshots = append(snapshots, draftproposal.Snapshot{Source: source.Source, Digest: source.Basis, Revision: source.Revision})
		for _, p := range source.Proposals {
			decisions = append(decisions, draftproposal.Decision{Source: source.Source, Proposal: p.ID, Revision: p.Revision, Action: "accept"})
		}
	}
	request, _ := json.Marshal(map[string]any{"text": "", "proposal": map[string]any{"anchor": "XR-4", "tree": st.e.proj, "snapshots": snapshots, "decisions": decisions}})
	refused := doReq(t, st.c, "POST", st.e.srv.URL+"/api/projects/demo/chats/"+scenarioPanelSID+"/say", string(request))
	if refused.StatusCode == 200 {
		t.Fatal("stale canonical answer applied")
	}
	refused.Body.Close()
	if got := readFile(t, st.path); got != materialBefore+"\nНовая мысль\n" {
		t.Fatal("stale changed material")
	}
	putDoc(t, st.e.proj, "docs/tasks/drafts/XR-4.md", materialBefore)
	status(true)
	// The legacy field name keeps the same displayed question and snapshots.
	ask, _ := chat.ReadAsk(chat.AskPath(st.e.proj, chat.TaskName("XR-4")))
	ask.Questions[0].Proposal = ask.Questions[0].Prepared
	ask.Questions[0].Prepared = nil
	if err := chat.WriteAsk(st.e.proj, chat.TaskName("XR-4"), ask); err != nil {
		t.Fatal(err)
	}
	askPath := chat.AskPath(st.e.proj, chat.TaskName("XR-4"))
	if err := os.Chtimes(askPath, now.Add(-time.Second), now.Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	status(true)
	browser("consent")
	// The canonical click is recorded; the old native tool remains unanswered.
	if readFile(t, transcript) != plainTalk+native {
		t.Fatal("native transcript was rewritten")
	}
	if data, err := os.ReadFile(keys); err == nil && len(data) > 0 {
		t.Fatalf("literal keys sent into native modal: %s", data)
	}
	status(true)
	var remaining struct {
		State draftproposal.State `json:"state"`
	}
	if json.Unmarshal([]byte(cli("", "draft", "prepared", "XR-5")), &remaining) != nil {
		t.Fatal("remaining view")
	}
	if selection == "one-source" && (remaining.State.State != "active" || remaining.State.Proposals[0].Status != "") {
		t.Fatal("unchecked source was applied")
	}
}
