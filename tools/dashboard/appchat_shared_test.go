package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestAppSharedSocketDoesNotFallBackWhenDaemonIsAbsent(t *testing.T) {
	t.Setenv(appSharedSocketEnv, "")
	t.Setenv("CODEX_HOME", "")
	home := t.TempDir()
	previous := realHomeFn
	realHomeFn = func() string { return home }
	t.Cleanup(func() { realHomeFn = previous })
	socket := filepath.Join(home, ".codex", "app-server-control", "app-server-control.sock")
	h := &Harness{Name: "codex", Protocol: "codex"}
	if got := appNewSharedSocket(h, chatStore{}); got != socket {
		t.Fatalf("адрес недоступного общего сервера: %q, жду %q", got, socket)
	}
	if got := appNewSharedSocket(h, chatStore{TaskRun: true}); got != "" {
		t.Fatalf("конвейер подключился бы к общему серверу: %q", got)
	}
}

func TestAppSharedNewChatReportsUnavailableDaemon(t *testing.T) {
	e, client := appEnv(t)
	t.Setenv(appSharedSocketEnv, filepath.Join(t.TempDir(), "absent.sock"))
	res := doReq(t, client, "POST", e.srv.URL+"/api/projects/demo/chats",
		`{"text":"проверка","model":"модель-codex","harness":"codextest"}`)
	data := body(t, res)
	if res.StatusCode != http.StatusBadGateway || !strings.Contains(data, "общий сервер Codex недоступен") ||
		!strings.Contains(data, `"refused"`) {
		t.Fatalf("создание чата без сокета: %d %s", res.StatusCode, data)
	}
	if _, err := os.Stat(filepath.Join(e.proj, "fake-app-log")); !os.IsNotExist(err) {
		t.Fatalf("вместо общего сервера запущен отдельный процесс: %v", err)
	}
}

func TestAppSharedNotificationsOnlyResumeTheirThread(t *testing.T) {
	var buffer appSharedNotifications
	buffer.add("turn/started", json.RawMessage(`{"threadId":"other","order":1}`))
	buffer.add("turn/started", json.RawMessage(`{"threadId":"current","order":2}`))
	buffer.add("item/completed", json.RawMessage(`{"threadId":"current","order":3}`))
	var seen []int
	err := buffer.flush("current", func(_ string, params json.RawMessage) {
		var note struct{ Order int }
		if err := json.Unmarshal(params, &note); err != nil {
			t.Fatal(err)
		}
		seen = append(seen, note.Order)
	})
	if err != nil {
		t.Fatal(err)
	}
	buffer.add("turn/completed", json.RawMessage(`{"threadId":"other","order":4}`))
	buffer.add("turn/completed", json.RawMessage(`{"threadId":"current","order":5}`))
	if want := []int{2, 3, 5}; !reflect.DeepEqual(seen, want) {
		t.Fatalf("очередь событий: %v, жду %v", seen, want)
	}
}

func TestAppSharedUnknownReceiptWaitsForMatchingUserItem(t *testing.T) {
	cfg := &Config{Home: t.TempDir()}
	s := newServer(cfg, nil, nil)
	c := &appChat{sid: "as-receipt", thread: "current", shared: true}
	if err := s.appSharedHold(c, "request"); err != nil {
		t.Fatal(err)
	}
	s = newServer(cfg, nil, nil)
	if err := s.appTurn(c, "same text", true, "request"); err != errAppSharedUnknown {
		t.Fatalf("повтор неподтверждённого хода: %v", err)
	}
	appendUser := func(thread, item, request string) {
		t.Helper()
		err := s.appFeedAppend(c.sid, []reply{{
			Role: "user", Text: "same text",
			App: &appFeedEvent{
				Method: "item/completed", ThreadID: thread, TurnID: "turn",
				Item: map[string]any{"id": item, "clientId": request},
			},
		}})
		if err != nil {
			t.Fatal(err)
		}
	}
	appendUser("other", "item-1", "request")
	appendUser("current", "item-2", "desktop-request")
	if err := s.appTurn(c, "same text", true, "request"); err != errAppSharedUnknown {
		t.Fatalf("чужая реплика совпала с квитанцией: %v", err)
	}
	appendUser("current", "item-3", "request")
	if err := s.appTurn(c, "same text", true, "request"); err != nil {
		t.Fatalf("собственная реплика не подтвердила доставку: %v", err)
	}
}
