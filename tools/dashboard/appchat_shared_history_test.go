package main

import (
	"bufio"
	"encoding/json"
	"io"
	"reflect"
	"testing"
)

func TestAppSharedImageOnlyUserMessage(t *testing.T) {
	item := map[string]any{"content": []any{map[string]any{"type": "localImage", "path": "/sample/image.png"}}}
	replies := appSharedUserReplies(item, "")
	if len(replies) != 1 || replies[0].Role != "user" || replies[0].Text != "[изображение]" {
		t.Fatalf("реплика с изображением: %+v", replies)
	}
}

func TestAppSharedLegacyEchoRemainsUnconfirmed(t *testing.T) {
	s := newServer(&Config{Home: t.TempDir()}, nil, nil)
	const sid = "as-echo"
	old := reply{Role: "user", Text: "same", Wire: "same"}
	if err := s.appFeedAppend(sid, []reply{old}); err != nil {
		t.Fatal(err)
	}
	index := appLegacyEchoes(s.appFeedRead(sid), "thread-1")
	item := old
	item.App = &appFeedEvent{ThreadID: "thread-1", TurnID: "unrecorded", Item: map[string]any{"id": "canonical"}}
	index.reconcile(&item)
	if item.Replaces != "" {
		t.Fatal("попытка отправки подменила подтверждённую реплику")
	}
	items := append([]reply{item}, index.unconfirmed("thread-1")...)
	for range 2 {
		if err := s.appFeedAppend(sid, items); err != nil {
			t.Fatal(err)
		}
	}
	feed := s.appFeedRead(sid)
	if len(feed) != 2 || feed[0].Role != "note" || feed[0].Note != appLegacyUnconfirmed ||
		feed[0].Text != old.Text || feed[0].Key != "app:0" ||
		feed[1].Role != "user" || feed[1].App.Item["id"] != "canonical" {
		t.Fatalf("пометка старой попытки: %+v", feed)
	}
}

func TestAppSharedBackfillPagesAndDedup(t *testing.T) {
	const first = `{"data":[{"id":"turn-1","status":"completed","startedAt":10,"items":[
		{"type":"userMessage","id":"user-1","startedAtMs":11000,"content":[{"type":"text","text":"первая"},{"type":"localImage"},{"type":"text","text":"строка"}]},
		{"type":"agentMessage","id":"agent-1","completedAtMs":12000,"text":"первый ответ"}]}],"nextCursor":"page-2"}`
	const second = `{"data":[{"id":"turn-2","status":"inProgress","startedAt":20,"items":[
		{"type":"userMessage","id":"user-2","clientId":"native-msg","content":[{"type":"text","text":"из Codex"}]},
		{"type":"commandExecution","id":"command-1","status":"inProgress","command":"ls"}]}],"nextCursor":null}`
	in, out := io.Pipe()
	defer in.Close()
	defer out.Close()
	proc := &appServer{in: out, pending: make(map[int64]chan *appMsg), dead: make(chan struct{})}
	var cursors []string
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		scan := bufio.NewScanner(in)
		for scan.Scan() {
			var req struct {
				ID     int64  `json:"id"`
				Method string `json:"method"`
				Params struct {
					ThreadID string `json:"threadId"`
					Cursor   string `json:"cursor"`
					Sort     string `json:"sortDirection"`
					View     string `json:"itemsView"`
				} `json:"params"`
			}
			json.Unmarshal(scan.Bytes(), &req)
			if req.Method != "thread/turns/list" || req.Params.ThreadID != "thread-1" ||
				req.Params.Sort != "asc" || req.Params.View != "full" {
				proc.resolve(req.ID, &appMsg{Error: &appError{Message: "неверный запрос истории"}})
				continue
			}
			switch req.Params.Cursor {
			case "":
				proc.resolve(req.ID, &appMsg{Result: json.RawMessage(first)})
			case "page-2":
				proc.resolve(req.ID, &appMsg{Result: json.RawMessage(second)})
			default:
				proc.resolve(req.ID, &appMsg{Error: &appError{Message: "неверный указатель страницы"}})
				continue
			}
			cursors = append(cursors, req.Params.Cursor)
		}
	}()
	chat := &appChat{sid: "as-history", thread: "thread-1", proc: proc}
	home := t.TempDir()
	s := newServer(&Config{Home: home}, nil, nil)
	previous := []reply{
		{Role: "user", Text: "первая\nстрока", Wire: "первая\nстрока"},
		{Role: "assistant", Text: "первый ответ", App: &appFeedEvent{
			Method: "item/completed", ThreadID: chat.thread, TurnID: "turn-1",
			Item: map[string]any{"id": "agent-1", "type": "agentMessage"},
		}},
	}
	if err := s.appFeedAppend(chat.sid, previous); err != nil {
		t.Fatal(err)
	}
	if err := s.appSharedBackfill(chat); err != nil {
		t.Fatal(err)
	}
	// После перезапуска индекс восстанавливается из того же файла ленты.
	s = newServer(&Config{Home: home}, nil, nil)
	if err := s.appSharedBackfill(chat); err != nil {
		t.Fatal(err)
	}
	out.Close()
	<-finished
	if want := []string{"", "page-2", "", "page-2"}; !reflect.DeepEqual(cursors, want) {
		t.Fatalf("указатели страниц: %v, жду %v", cursors, want)
	}
	items := s.appFeedRead(chat.sid)
	if len(items) != 3 {
		t.Fatalf("в ленте %d записей, жду 3: %+v", len(items), items)
	}
	if items[0].Role != "user" || items[0].Text != "первая\nстрока\n[изображение]" || items[0].Wire != "первая\nстрока" ||
		items[0].Key != "app:0" || items[0].App.identity() != appEventIdentity("item/completed", chat.thread, "turn-1", "user-1") {
		t.Fatalf("первая реплика: %+v", items[0])
	}
	if items[1].Role != "assistant" || items[1].Text != "первый ответ" {
		t.Fatalf("первый ответ: %+v", items[1])
	}
	if items[2].Role != "user" || items[2].Text != "из Codex" ||
		items[2].Time != "1970-01-01T00:00:20Z" || items[2].App.Item["clientId"] != "native-msg" {
		t.Fatalf("реплика с пропущенного хода: %+v", items[2])
	}
}
