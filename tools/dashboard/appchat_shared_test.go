package main

import (
	"encoding/json"
	"reflect"
	"testing"
)

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
