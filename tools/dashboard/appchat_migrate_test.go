package main

import (
	"bufio"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAppSayMigratesLegacyOnlyOnManualMessage(t *testing.T) {
	e, client := appEnv(t)
	dir, err := os.MkdirTemp("/tmp", "dk-shared-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	socket := filepath.Join(dir, "sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		// Сокет существует, но общий сервер обрывает рукопожатие.
		conn, err := listener.Accept()
		if err == nil {
			conn.Close()
		}
	}()
	t.Setenv(appSharedSocketEnv, filepath.Join(dir, "absent.sock"))
	sid := "as-legacy"
	st := chatStore{Project: "demo", Model: "модель-codex", Harness: "codextest", Protocol: "codex", Thread: "th-1"}
	if err := e.s.chatStoreWrite(sid, st); err != nil {
		t.Fatal(err)
	}
	res := doReq(t, client, "GET", e.srv.URL+"/api/projects/demo/chats/"+sid+"/status", "")
	body(t, res)
	if got := e.s.chatStoreRead(sid).SharedSocket; got != "" {
		t.Fatalf("GET migrated chat: %q", got)
	}
	say := func() (*http.Response, string) {
		res := doReq(t, client, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/say", `{"text":"продолжить","manual":true}`)
		return res, body(t, res)
	}
	res, data := say()
	if res.StatusCode != http.StatusBadGateway || !strings.Contains(data, "общий сервер Codex недоступен") {
		t.Fatalf("ответ при отсутствии сокета: %d %s", res.StatusCode, data)
	}
	if got := e.s.chatStoreRead(sid).SharedSocket; got != "" {
		t.Fatalf("записан отсутствующий сокет: %q", got)
	}
	t.Setenv(appSharedSocketEnv, socket)
	res, data = say()
	if res.StatusCode != http.StatusBadGateway || !strings.Contains(data, "общий сервер Codex недоступен") {
		t.Fatalf("response %d %s", res.StatusCode, data)
	}
	got := e.s.chatStoreRead(sid)
	if got.SharedSocket != socket || got.Thread != st.Thread {
		t.Fatalf("binding: %+v", got)
	}
	if _, err := os.Stat(filepath.Join(e.proj, "fake-app-log")); !os.IsNotExist(err) {
		t.Fatalf("legacy client started: %v", err)
	}
}

func TestAppSharedStreamReplacesUnconfirmedEcho(t *testing.T) {
	e, client := appEnv(t)
	const sid = "as-legacy-stream"
	st := chatStore{Project: "demo", Protocol: "codex", Thread: "th-1"}
	if err := e.s.chatStoreWrite(sid, st); err != nil {
		t.Fatal(err)
	}
	if err := e.s.appFeedAppend(sid, []reply{{Role: "user", Text: "старый текст", Wire: "старый текст"}}); err != nil {
		t.Fatal(err)
	}
	resp := doReq(t, client, "GET", e.srv.URL+"/api/projects/demo/sessions/"+sid+"?stream=1", "")
	defer resp.Body.Close()
	scan := bufio.NewScanner(resp.Body)
	next := func() (string, reply) {
		t.Helper()
		var kind string
		for scan.Scan() {
			line := scan.Text()
			if value, ok := strings.CutPrefix(line, "event: "); ok {
				kind = value
			}
			if value, ok := strings.CutPrefix(line, "data: "); ok {
				var item reply
				if err := json.Unmarshal([]byte(value), &item); err != nil {
					t.Fatal(err)
				}
				return kind, item
			}
		}
		t.Fatalf("поток закрылся: %v", scan.Err())
		return "", reply{}
	}
	_, old := next()
	index := appLegacyEchoes(e.s.appFeedRead(sid), st.Thread)
	if err := e.s.appFeedAppend(sid, index.unconfirmed(st.Thread)); err != nil {
		t.Fatal(err)
	}
	event := &appFeedEvent{Method: "item/completed", ThreadID: st.Thread, TurnID: "th-turn",
		Item: map[string]any{"id": "native-message", "type": "userMessage"}}
	if err := e.s.appFeedAppend(sid, []reply{{Role: "user", Text: old.Text, Wire: old.Wire, App: event}}); err != nil {
		t.Fatal(err)
	}
	kind, replacement := next()
	if kind != "replace" || replacement.Key != old.Key || replacement.Role != "note" || replacement.Note != appLegacyUnconfirmed {
		t.Fatalf("замена старого эха: %q %+v", kind, replacement)
	}
	kind, current := next()
	if kind != "" || current.Key == old.Key || current.Role != "user" || current.Text != old.Text {
		t.Fatalf("подтверждённая реплика: %q %+v", kind, current)
	}
}
