package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAppChatInstalledArtifact(t *testing.T) {
	e, client := appArtifactEnv(t, "mute")
	if err := os.WriteFile(filepath.Join(e.proj, "fake-app-enforce-writer-lock"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	sid := "asartifact-writer"
	stored := chatStore{Project: "demo", Model: "модель-codex", Harness: "codextest", Protocol: "codex", Thread: "th-1", CxEffort: "high", Born: e.s.now().Unix()}
	if err := e.s.chatStoreWrite(sid, stored); err != nil {
		t.Fatal(err)
	}
	if err := e.s.appFeedAppend(sid, []reply{{Role: "user", Text: "первый вопрос"}, {Role: "assistant", Text: "предыдущий ответ"}}); err != nil {
		t.Fatal(err)
	}
	before := readFile(t, filepath.Join(chatStoreDir(e.home), sid+".json"))
	feed := readFile(t, e.s.appFeedFile(sid))
	owner, err := e.s.appServerStart(e.s.chatHarnessOf(stored.Model, stored.Harness), e.proj, binPath(agentctlBin), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(owner.kill)
	if _, err = owner.call("initialize", map[string]any{}); err != nil {
		t.Fatal(err)
	}
	if _, err = owner.call("thread/resume", map[string]any{"threadId": stored.Thread}); err != nil {
		t.Fatalf("fixture owner: %v", err)
	}
	for i := 0; i < 2; i++ {
		r := doReq(t, client, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/say", `{"text":"после внешнего владельца","msg_id":"artifact-writer-retry"}`)
		raw := body(t, r)
		var refused map[string]string
		if r.StatusCode != http.StatusConflict || json.Unmarshal([]byte(raw), &refused) != nil || !strings.Contains(refused["refused"], "другом клиенте Codex") || !strings.Contains(refused["refused"], "повторите отправку") {
			t.Fatalf("artifact writer conflict: %d %s", r.StatusCode, raw)
		}
		if readFile(t, filepath.Join(chatStoreDir(e.home), sid+".json")) != before || readFile(t, e.s.appFeedFile(sid)) != feed {
			t.Fatalf("artifact изменил настройки или историю при конфликте: before=%s after=%s\nfeed before=%s after=%s", before, readFile(t, filepath.Join(chatStoreDir(e.home), sid+".json")), feed, readFile(t, e.s.appFeedFile(sid)))
		}
		if _, err = owner.call("initialize", map[string]any{}); err != nil {
			t.Fatalf("artifact повредил внешнего владельца: %v", err)
		}
	}
	log := readFile(t, filepath.Join(e.proj, "fake-app-log"))
	if strings.Count(log, "start ") != 0 || strings.Count(log, "turn ") != 0 {
		t.Fatalf("artifact конфликт создал новую нить или ход: %s", log)
	}
	owner.kill()
	appMode(t, e, "mute")
	r := doReq(t, client, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/say", `{"text":"после внешнего владельца","msg_id":"artifact-writer-retry"}`)
	if raw := body(t, r); r.StatusCode != http.StatusOK {
		t.Fatalf("artifact retry: %d %s", r.StatusCode, raw)
	}
	r = doReq(t, client, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/say", `{"text":"после внешнего владельца","msg_id":"artifact-writer-retry"}`)
	if raw := body(t, r); r.StatusCode != http.StatusOK || !strings.Contains(raw, `"way":"dup"`) {
		t.Fatalf("artifact duplicate: %d %s", r.StatusCode, raw)
	}
	log = readFile(t, filepath.Join(e.proj, "fake-app-log"))
	if strings.Count(log, "start ") != 0 || strings.Count(log, "turn ") != 1 || strings.Count(log, "resume th-1") != 2 {
		t.Fatalf("artifact потерял нить или повторил ход: %s", log)
	}
	after := e.s.chatStoreRead(sid)
	if after.Thread != stored.Thread || after.Model != stored.Model || after.Harness != stored.Harness || after.CxEffort != stored.CxEffort {
		t.Fatalf("artifact после повтора изменил настройки: %+v", after)
	}
	if got := readFile(t, e.s.appFeedFile(sid)); !strings.HasPrefix(got, feed) || strings.Count(got, `"role":"user"`) != 2 {
		t.Fatalf("artifact потерял историю или реплику: %s", got)
	}
	r = doReq(t, client, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/stop", "{}")
	if raw := body(t, r); r.StatusCode != http.StatusOK {
		t.Fatalf("artifact final stop: %d %s", r.StatusCode, raw)
	}
	t.Log("artifact: HTTP writer conflict, refused, unchanged history/settings, same-thread retry and duplicate refusal passed")
}

func TestAppChatPolicyInstalledArtifact(t *testing.T) {
	e, client, restart := appArtifactRestartable(t, "")
	sid := appStartChat(t, e, client, "первый ход с единой политикой")
	appWaitBusy(t, e, client, sid, false)
	before := e.s.chatStoreRead(sid)
	feed := readFile(t, e.s.appFeedFile(sid))
	restart()
	client = e.loggedClient(t)
	r := doReq(t, client, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/say", `{"text":"продолжение сохранённого разговора"}`)
	if raw := body(t, r); r.StatusCode != http.StatusOK {
		t.Fatalf("resume: %d %s", r.StatusCode, raw)
	}
	appWaitBusy(t, e, client, sid, false)
	after := e.s.chatStoreRead(sid)
	if before.Thread != after.Thread || before.Model != after.Model || before.Harness != after.Harness || !strings.HasPrefix(readFile(t, e.s.appFeedFile(sid)), feed) {
		t.Fatal("после рестарта изменены нить, модель или история")
	}
	policies := 0
	for _, line := range strings.Split(readFile(t, filepath.Join(e.proj, "fake-app-log")), "\n") {
		if !strings.HasPrefix(line, "policy ") {
			continue
		}
		var p map[string]any
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "policy ")), &p); err != nil {
			t.Fatal(err)
		}
		if p["approvalPolicy"] != "on-request" || p["approvalsReviewer"] != "auto_review" || p["sandbox"] != "workspace-write" {
			t.Fatalf("artifact policy: %v", p)
		}
		config := p["config"].(map[string]any)
		for key := range config {
			if !strings.HasPrefix(key, "shell_environment_policy.set.") {
				t.Fatalf("расширена конфигурация: %s", key)
			}
		}
		policies++
	}
	if policies != 2 {
		t.Fatalf("нет start и resume: %d", policies)
	}
	r = doReq(t, client, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/stop", "{}")
	body(t, r)
	appMode(t, e, "approval-denied")
	r = doReq(t, client, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/say", `{"text":"запрос служебного доступа"}`)
	if raw := body(t, r); r.StatusCode != http.StatusOK {
		t.Fatalf("denied request: %d %s", r.StatusCode, raw)
	}
	appWaitFeed(t, e, sid, "automatic approval review rejected")
	appWaitBusy(t, e, client, sid, false)
	denied := false
	for _, item := range e.s.appFeedRead(sid) {
		if strings.Contains(item.Text, "service access denied") && item.Fail {
			denied = true
		}
	}
	if !denied {
		t.Fatal("отказ проверки прав не отмечен ошибкой")
	}
	r = doReq(t, client, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/stop", "{}")
	body(t, r)
	restart()
	client = e.loggedClient(t)
	appMode(t, e, "ignored-policy")
	r = doReq(t, client, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/say", `{"text":"не выполнять с другой политикой"}`)
	if raw := body(t, r); r.StatusCode != http.StatusBadGateway || !strings.Contains(raw, "клиент не подтвердил workspace-write") {
		t.Fatalf("artifact fail-closed: %d %s", r.StatusCode, raw)
	}
	if log := readFile(t, filepath.Join(e.proj, "fake-app-log")); strings.Count(log, "turn ") != 3 {
		t.Fatalf("ход после отказа: %s", log)
	}
	t.Log("artifact: единая политика start/resume, история после рестарта, отказ до хода; песочницу и auto_review проверяет живой сценарий")
}
