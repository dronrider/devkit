package main

import (
	"encoding/json"
	"errors"
	"io"
	"net"
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
	h := &Harness{Name: "test-profile", Protocol: "codex", Bin: "codex"}
	if got, err := appSharedSocketPath(h, chatStore{}); err != nil || got != "" {
		t.Fatalf("профиль без объявленного аккаунта получил общий сокет: %q, ошибка %v", got, err)
	}
	h.Home = filepath.Join(home, ".codex")
	if got, err := appSharedSocketPath(h, chatStore{}); err != nil || got != socket {
		t.Fatalf("адрес недоступного общего сервера: %q, жду %q, ошибка %v", got, socket, err)
	}
	if got, err := appSharedSocketPath(h, chatStore{TaskRun: true}); err != nil || got != "" {
		t.Fatalf("конвейер подключился бы к общему серверу: %q, ошибка %v", got, err)
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

func TestAppSharedNewChatRejectsRelativeSocket(t *testing.T) {
	e, client := appEnv(t)
	t.Setenv(appSharedSocketEnv, "relative.sock")
	res := doReq(t, client, "POST", e.srv.URL+"/api/projects/demo/chats",
		`{"text":"проверка","model":"модель-codex","harness":"codextest"}`)
	data := body(t, res)
	if res.StatusCode != http.StatusBadGateway || !strings.Contains(data, appSharedSocketEnv) ||
		!strings.Contains(data, "абсолютный путь") || !strings.Contains(data, `"refused"`) {
		t.Fatalf("создание чата с относительным путём: %d %s", res.StatusCode, data)
	}
	if _, err := os.Stat(filepath.Join(e.proj, "fake-app-log")); !os.IsNotExist(err) {
		t.Fatalf("запущен отдельный клиент: %v", err)
	}
}

func TestAppSharedDirectStartReusesAcceptedSession(t *testing.T) {
	e, client := appEnv(t)
	t.Setenv(appSharedSocketEnv, filepath.Join(t.TempDir(), "absent.sock"))
	const request = `{"text":"проверка","model":"модель-codex","harness":"codextest","msg_id":"first-request"}`
	const claim = "first-request"
	url := e.srv.URL + "/api/projects/demo/chats"
	res := doReq(t, client, "POST", url, request)
	if data := body(t, res); res.StatusCode != http.StatusBadGateway {
		t.Fatalf("первая попытка без подключения: %d %s", res.StatusCode, data)
	}
	sid := appSharedStartID("demo", claim)
	st := e.s.chatStoreRead(sid)
	if st.Project != "demo" || st.SharedFirst != claim || st.Thread != "" {
		t.Fatalf("первая попытка не закрепила запись: %+v", st)
	}
	// Подключение восстановилось. Сервер уже принял ход, а браузер
	// повторяет запрос, поскольку не получил ответ.
	c := &appChat{sid: sid, shared: true, thread: "shared-thread", proc: &appServer{}}
	if err := e.s.appSharedHold(c, claim); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(e.s.appSharedReceipt(c, claim), []byte("accepted\n"), 0600); err != nil {
		t.Fatal(err)
	}
	e.s.appMu.Lock()
	e.s.appChats[sid] = c
	e.s.appMu.Unlock()
	t.Cleanup(func() {
		e.s.appMu.Lock()
		delete(e.s.appChats, sid)
		e.s.appMu.Unlock()
	})
	res = doReq(t, client, "POST", url, request)
	data := body(t, res)
	if res.StatusCode != http.StatusOK || !strings.Contains(data, sid) {
		t.Fatalf("повтор первого запроса: %d %s", res.StatusCode, data)
	}
	if c.busyNow() {
		t.Fatal("повтор принятого запроса открыл ход")
	}
	if got := e.s.chatStoreRead(sid).Thread; got != c.thread {
		t.Fatalf("в хранилище нет ID восстановленной нити: %q", got)
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

func TestAppSharedMigrateIdleAndBusy(t *testing.T) {
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
	t.Setenv(appSharedSocketEnv, socket)
	s := newServer(&Config{Home: t.TempDir()}, nil, nil)
	h := &Harness{Name: "codex", Protocol: "codex"}
	st := chatStore{Thread: "old-thread", Project: "demo"}
	if err := s.chatStoreWrite("as-old", st); err != nil {
		t.Fatal(err)
	}
	in, out := io.Pipe()
	defer in.Close()
	dead := make(chan struct{})
	close(dead)
	c := &appChat{sid: "as-old", proc: &appServer{in: out, dead: dead}, busy: true}
	s.appChats[c.sid] = c
	if err := s.appSharedMigrate(c.sid, h, &st); !errors.Is(err, errAppTurnBusy) {
		t.Fatalf("busy: %v", err)
	}
	if s.chatStoreRead(c.sid).SharedSocket != "" || s.appChats[c.sid] != c {
		t.Fatal("active client was changed")
	}
	c.busy = false
	if err := s.appSharedMigrate(c.sid, h, &st); err != nil {
		t.Fatal(err)
	}
	got := s.chatStoreRead(c.sid)
	if got.SharedSocket != socket || got.Thread != "old-thread" || s.appChats[c.sid] != nil {
		t.Fatalf("binding: %+v", got)
	}
	if !c.busyNow() {
		t.Fatal("removed client can accept another turn")
	}
}

func TestAppSharedMigrateSkipsExcludedProfiles(t *testing.T) {
	t.Setenv(appSharedSocketEnv, filepath.Join(t.TempDir(), "codex.sock"))
	s := newServer(&Config{Home: t.TempDir()}, nil, nil)
	for _, tc := range []struct {
		h  *Harness
		st chatStore
	}{
		{&Harness{Protocol: "codex"}, chatStore{Thread: "t", TaskRun: true}},
		{&Harness{Protocol: "codex"}, chatStore{Thread: "t", GroomSource: "DK-1"}},
		{&Harness{Protocol: "other"}, chatStore{Thread: "t"}},
	} {
		if err := s.appSharedMigrate("as-excluded", tc.h, &tc.st); err != nil || tc.st.SharedSocket != "" {
			t.Fatalf("excluded: %+v %v", tc, err)
		}
	}
}
