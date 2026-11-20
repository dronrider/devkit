package main

import (
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
	t.Setenv(appSharedSocketEnv, socket)
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
	res = doReq(t, client, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/say", `{"text":"продолжить"}`)
	data := body(t, res)
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
