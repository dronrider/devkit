package main

import (
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/dronrider/devkit/internal/mainchat"
	"github.com/dronrider/devkit/internal/sessions"
)

func TestTaskChatEqualTimeHandoverKeepsLatestCarrier(t *testing.T) {
	e, c := chatEnv(t)
	stamp := time.Date(2026, 11, 4, 23, 14, 14, 0, time.UTC)
	const key = "tmux-task-XR-4"
	journal := mainchat.Line(stamp, mainchat.Bind{Record: "XR-4", Chat: key,
		Project: "demo", Source: mainchat.ByWork})
	writeMainChat(t, e.home, journal)
	old := writeSession(t, e.home, e.proj, "", "zz-old", plainTalk, time.Now())
	fresh := writeSession(t, e.home, e.proj, "", "aa-new", plainTalk, time.Now())
	birth := func(sid, transcript string) string {
		return sessions.Line(stamp, sid, sessionBind{Task: "XR-4", Source: bindOrder,
			Project: "demo", Tmux: "task-XR-4", Transcript: transcript}, "startup")
	}
	started := birth("zz-old", old) + birth("aa-new", "")
	writeBindsAt(t, e.home, started)
	endpoint := e.srv.URL + "/api/projects/demo/tasks/XR-4/chat"
	code, before := taskChatGet(t, c, endpoint)
	if code != http.StatusOK || before.Chat != "zz-old" {
		t.Fatalf("заказ без транскрипта скрыл прежнюю ленту: %d %+v", code, before)
	}
	writeBindsAt(t, e.home, started+birth("aa-new", fresh))
	for n := 0; n < 64; n++ {
		code, got := taskChatGet(t, c, endpoint)
		if code != http.StatusOK || got.Chat != "aa-new" || got.Window != "task-XR-4" {
			t.Fatalf("чтение %d после передачи: %d %+v", n, code, got)
		}
	}
	for _, path := range []string{old, fresh} {
		if data, err := os.ReadFile(path); err != nil || string(data) != plainTalk {
			t.Fatalf("изменился исходный транскрипт %s: %v", path, err)
		}
	}
	if data := readFile(t, mainchat.Path(e.home)); data != journal {
		t.Fatalf("разрешение носителя изменило назначения: %s", data)
	}
}

func TestChatMainRowFollowsResumedCarrierSID(t *testing.T) {
	e, c := chatEnv(t)
	stamp := time.Date(2026, 11, 4, 23, 14, 14, 0, time.UTC)
	const sid = "resumed-carrier"
	path := writeSession(t, e.home, e.proj, "", sid, plainTalk, time.Now())
	writeMainChat(t, e.home, mainchat.Line(stamp, mainchat.Bind{Record: "XR-4",
		Chat: "tmux-task-XR-4", Project: "demo", Source: mainchat.ByWork}))
	birth := func(window string, at time.Time) string {
		return sessions.Line(at, sid, sessionBind{Task: "XR-4", Source: bindOrder,
			Project: "demo", Tmux: window, Transcript: path}, "startup")
	}
	writeBindsAt(t, e.home, birth("task-XR-4", stamp)+birth("chat-XR-4-1", stamp.Add(time.Second)))
	code, handle := taskChatGet(t, c, e.srv.URL+"/api/projects/demo/tasks/XR-4/chat")
	if code != http.StatusOK || handle.Chat != sid {
		t.Fatalf("после resume адрес потерял SID: %d %+v", code, handle)
	}
	for _, keep := range []string{"", "&keep=" + sid} {
		rows := chatRows(t, c, e.srv.URL+"/api/projects/demo/chats?all=1&days=0"+keep)
		mainRows, pinned := 0, 0
		for _, row := range rows {
			if rowStr(row, "id") == "XR-4" && rowBool(row, "main") {
				mainRows++
			}
			if rowStr(row, "id") == sid {
				pinned++
				if rowStr(row, "carrier") != "XR-4" {
					t.Fatalf("носитель вернулся отдельным обсуждением: %v", row)
				}
			}
		}
		if mainRows != 1 || (keep != "" && pinned != 1) || (keep == "" && pinned != 0) {
			t.Fatalf("после resume main=%d, pinned=%d, keep=%q: %v", mainRows, pinned, keep, rows)
		}
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != plainTalk {
		t.Fatalf("resume изменил историю: %v", err)
	}
}
