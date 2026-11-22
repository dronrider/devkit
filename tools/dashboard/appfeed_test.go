package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func appFeedJSON(t *testing.T, value any) map[string]any {
	t.Helper()
	b, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	return got
}

func appFeedBody(t *testing.T, value any) string {
	t.Helper()
	b, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestAppFeedAttachments(t *testing.T) {
	for _, count := range []int{1, 2} {
		for _, text := range []string{"Проверь снимок", ""} {
			t.Run(fmt.Sprintf("%d/%s", count, text), func(t *testing.T) {
				e, c := appEnv(t)
				blank := "blank-attachment"
				if err := e.s.chatStoreWrite(blank, chatStore{Blank: true, Project: "demo"}); err != nil {
					t.Fatal(err)
				}
				wire := ""
				paths := []string{}
				for i := 0; i < count; i++ {
					resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+blank+"/shot", `{"kind":"image","data":"iVBORw0KGgo="}`)
					var got struct{ Path string }
					raw := body(t, resp)
					if resp.StatusCode != 200 || json.Unmarshal([]byte(raw), &got) != nil || got.Path == "" {
						t.Fatalf("upload: %d %s", resp.StatusCode, raw)
					}
					paths = append(paths, got.Path)
					wire += fmt.Sprintf("<screenshot file=\"%s\">\nПрочитай снимок\n</screenshot>\n", got.Path)
				}
				wire += text
				wire = strings.TrimSpace(wire)
				start := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats", appFeedBody(t, map[string]any{"text": wire, "harness": "codextest", "model": "модель-codex", "chat": blank}))
				var started struct{ Session string }
				raw := body(t, start)
				if start.StatusCode != 200 || json.Unmarshal([]byte(raw), &started) != nil || started.Session == "" {
					t.Fatalf("start: %d %s", start.StatusCode, raw)
				}
				sid := started.Session
				appWaitFeed(t, e, sid, "ответ на реплику")
				wantTitle := text
				if wantTitle == "" {
					wantTitle = "Снимок экрана"
				}
				for round := 0; round < 2; round++ {
					items := e.s.appFeedRead(sid)
					if len(items) == 0 || items[0].Text != text || items[0].Wire != wire || !reflect.DeepEqual(items[0].Shots, paths) {
						t.Fatalf("вложение: %+v", items)
					}
					feed := doReq(t, c, "GET", e.srv.URL+"/api/projects/demo/sessions/"+sid, "")
					var got struct{ Head struct{ First string } }
					raw = body(t, feed)
					if feed.StatusCode != 200 || json.Unmarshal([]byte(raw), &got) != nil || got.Head.First != wantTitle {
						t.Fatalf("шапка: %d %s", feed.StatusCode, raw)
					}
					rows := e.s.appListRows("demo")
					found := false
					for _, row := range rows {
						if row.ID == sid {
							found = true
							if row.Title != wantTitle {
								t.Fatalf("название: %+v", row)
							}
						}
					}
					if !found {
						t.Fatal("чат исчез из списка")
					}
					for _, path := range paths {
						// shotURL использует владельца исходного пути, включая blank до создания as-чата.
						u := e.srv.URL + "/api/projects/demo/chats/" + filepath.Base(filepath.Dir(path)) + "/shot?name=" + url.QueryEscape(filepath.Base(path))
						r := doReq(t, c, "GET", u, "")
						if r.StatusCode != 200 || body(t, r) != "\x89PNG\r\n\x1a\n" {
							t.Fatal("снимок не открылся")
						}
					}
					if round == 0 {
						e.s.appDrop(sid, "проверка истории")
					}
				}
				// Продолжение записывается и доставляется дословно тем же входом.
				say := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/say", appFeedBody(t, map[string]string{"text": wire + " второй ход"}))
				if say.StatusCode != 200 {
					t.Fatalf("say: %d %s", say.StatusCode, body(t, say))
				}
				body(t, say)
				appWaitFeed(t, e, sid, "ответ на реплику: "+wire+" второй ход")
				items := e.s.appFeedRead(sid)
				var users int
				var delivered bool
				for _, it := range items {
					if it.Role == "user" {
						users++
						if !reflect.DeepEqual(it.Shots, paths) {
							t.Fatalf("продолжение потеряло снимки: %+v", it)
						}
					}
					if it.Role == "assistant" && strings.Contains(it.Text, wire+" второй ход") {
						delivered = true
					}
				}
				if users != 2 || !delivered {
					t.Fatalf("ввод модели/повтор: users=%d delivered=%v", users, delivered)
				}
				r := doReq(t, c, "GET", e.srv.URL+"/api/projects/demo/chats/"+blank+"/shot?name="+url.QueryEscape("../чужой.png"), "")
				if r.StatusCode == 200 {
					t.Fatal("чужой путь открыт")
				}
				body(t, r)
			})
		}
	}
}

func TestAppFeedEvents(t *testing.T) {
	e, _ := appEnv(t)
	data, err := os.ReadFile("testdata/appfeed_items.json")
	if err != nil {
		t.Fatal(err)
	}
	var events []json.RawMessage
	if err := json.Unmarshal(data, &events); err != nil {
		t.Fatal(err)
	}
	sid := "as-feed-events"
	// Элементы сняты thread/read 2026-11-18 с исходной нити из DK-1732.
	// Оболочка уведомления содержит те же поля threadId/turnId/item.
	for _, event := range events {
		e.s.appNote(sid, "item/completed", event)
		e.s.appNote(sid, "item/completed", event)
	}
	got := e.s.appFeedRead(sid)
	if len(got) != len(events) {
		t.Fatalf("повторные события: %d вместо %d", len(got), len(events))
	}
	for i, it := range got {
		saved := appFeedJSON(t, it)
		app, ok := saved["app"].(map[string]any)
		if !ok {
			t.Fatalf("событие потеряло метаданные: %+v", saved)
		}
		original := map[string]any{}
		json.Unmarshal(events[i], &original)
		if !reflect.DeepEqual(app["item"], original["item"]) || app["threadId"] != original["threadId"] || app["turnId"] != original["turnId"] {
			t.Fatalf("потерян контракт: %+v", app)
		}
		if it.Tool == "collabAgentToolCall" || it.Tool == "subAgentActivity" {
			t.Fatalf("техническая карточка: %+v", it)
		}
	}
	delegated := json.RawMessage(`{"threadId":"parent","turnId":"t","item":{"type":"collabAgentToolCall","id":"spawn-rich","tool":"spawnAgent","status":"completed","senderThreadId":"parent","receiverThreadIds":["child"],"prompt":"Проверь тесты","model":"test-model","reasoningEffort":"high","agentsStates":{"child":{"status":"completed","message":"Проверено"}}}}`)
	e.s.appNote(sid, "item/completed", delegated)
	delegatedItems := e.s.appFeedRead(sid)
	delegatedReply := appFeedJSON(t, delegatedItems[len(delegatedItems)-1])["app"].(map[string]any)
	var delegatedOriginal map[string]any
	if err := json.Unmarshal(delegated, &delegatedOriginal); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(delegatedReply["item"], delegatedOriginal["item"]) {
		t.Fatal("поручение или ответ субагента потеряны")
	}

	// Неполный протокол не получает фиктивных code/duration/state.
	raw := json.RawMessage(`{"threadId":"child","turnId":"t","item":{"type":"commandExecution","id":"exec-missing","command":"pwd"}}`)
	e.s.appMu.Lock()
	e.s.appChats[sid] = &appChat{thread: "parent"}
	e.s.appMu.Unlock()
	e.s.appNote(sid, "item/started", raw)
	it := e.s.appFeedRead(sid)
	last := it[len(it)-1]
	if last.Sub != "child" {
		t.Fatalf("владение нитью потерялось: %+v", last)
	}
	app := appFeedJSON(t, last)["app"].(map[string]any)
	item := app["item"].(map[string]any)
	if _, ok := item["exitCode"]; ok {
		t.Fatal("выдуман код")
	}
	if _, ok := item["durationMs"]; ok {
		t.Fatal("выдумана длительность")
	}
	completed := json.RawMessage(`{"threadId":"child","turnId":"t","completedAtMs":17,"item":{"type":"commandExecution","id":"exec-missing","command":"pwd","status":"failed","exitCode":2,"durationMs":0,"aggregatedOutput":"полный вывод\nошибка\n"}}`)
	e.s.appNote(sid, "item/completed", completed)
	it = e.s.appFeedRead(sid)
	last = it[len(it)-1]
	if !last.Fail || last.Text != "полный вывод\nошибка\n" || last.Sub != "child" {
		t.Fatalf("завершение команды: %+v", last)
	}
	// Завершение дочернего хода не освобождает родительский.
	e.s.appChats[sid].busy = true
	e.s.appNote(sid, "turn/completed", json.RawMessage(`{"threadId":"child","turn":{"status":"completed"}}`))
	if !e.s.appChats[sid].busyNow() {
		t.Fatal("чужой ход освободил родительский")
	}
}

func TestAppFeedLegacy(t *testing.T) {
	data := []byte("{\"role\":\"user\",\"text\":\"<screenshot file=\\\"/tmp/blank/a.png\\\">\\nread\\n</screenshot>\\nслова\"}\n{\"role\":\"tool\",\"tool\":\"userMessage\"}\n{\"role\":\"tool\",\"tool\":\"collabAgentToolCall\"}\n{\"role\":\"tool\",\"tool\":\"subAgentActivity\"}\n{\"role\":\"tool\",\"tool\":\"Bash\",\"note\":\"pwd\",\"text\":\"/tmp\\n\"}\n")
	got := appFeedParse(data)
	if len(got) != 2 || got[0].Text != "слова" || len(got[0].Shots) != 1 || !strings.Contains(got[0].Wire, "<screenshot") || got[1].Key != "app:4" {
		t.Fatalf("старая история: %+v", got)
	}
}

func appIndexReply(t *testing.T, id, text string) reply {
	t.Helper()
	raw := appFeedBody(t, map[string]any{"role": "tool", "tool": "Bash", "text": text, "app": map[string]any{"method": "item/completed", "threadId": "parent", "turnId": "turn", "item": map[string]any{"id": id, "type": "commandExecution", "command": "pwd", "aggregatedOutput": text}}})
	var it reply
	if err := json.Unmarshal([]byte(raw), &it); err != nil {
		t.Fatal(err)
	}
	return it
}

func appIndexLine(t *testing.T, it reply) []byte {
	t.Helper()
	return []byte(appFeedBody(t, it) + "\n")
}

func TestAppFeedConcurrentWritersKeepOneEvent(t *testing.T) {
	e, _ := appEnv(t)
	const sid = "as-concurrent-feed"
	const writers = 8
	events := []reply{appIndexReply(t, "shared", "одна реплика")}
	start := make(chan struct{})
	replies := make(chan error, writers)
	for range writers {
		go func() {
			<-start
			replies <- e.s.appFeedAppend(sid, events)
		}()
	}
	close(start)
	for range writers {
		if err := <-replies; err != nil {
			t.Fatal(err)
		}
	}
	if feed := e.s.appFeedRead(sid); len(feed) != 1 || feed[0].Text != "одна реплика" {
		t.Fatalf("лента после параллельной записи: %+v", feed)
	}
}

func TestAppFeedIndexReadsOnlyNewBytes(t *testing.T) {
	e, _ := appEnv(t)
	sid := "as-index-cost"
	file := e.s.appFeedFile(sid)
	if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
		t.Fatal(err)
	}
	seed := []byte{}
	for i := 0; i < 16; i++ {
		seed = append(seed, appIndexLine(t, appIndexReply(t, fmt.Sprintf("seed-%d", i), strings.Repeat("x", 64<<10)))...)
	}
	if err := os.WriteFile(file, seed, 0644); err != nil {
		t.Fatal(err)
	}
	_, before := feedIOStat()
	for i := 0; i < 32; i++ {
		it := appIndexReply(t, fmt.Sprintf("new-%d", i), strings.Repeat("y", 8<<10))
		if err := e.s.appFeedAppend(sid, []reply{it, it}); err != nil {
			t.Fatal(err)
		}
	}
	_, after := feedIOStat()
	if read := after - before; read != int64(len(seed)) {
		t.Fatalf("индекс перечитал историю: %d байт, начальная лента %d", read, len(seed))
	}
	// Другой писатель добавляет событие под тем же файловым соглашением.
	external := appIndexReply(t, "external", "другой писатель")
	extra := appIndexLine(t, external)
	f, err := os.OpenFile(file, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(extra); err != nil {
		t.Fatal(err)
	}
	f.Close()
	_, before = feedIOStat()
	if err := e.s.appFeedAppend(sid, []reply{external, appIndexReply(t, "after-external", "готово")}); err != nil {
		t.Fatal(err)
	}
	_, after = feedIOStat()
	if read := after - before; read != int64(len(extra)) {
		t.Fatalf("внешнее добавление перечитало %d байт вместо хвоста %d", read, len(extra))
	}
	if got := len(e.s.appFeedRead(sid)); got != 50 {
		t.Fatalf("дедуп при добавлении: %d записей вместо 50", got)
	}
	// Новый процесс восстанавливает индекс один раз и узнаёт прежние identity.
	restarted := &server{cfg: e.s.cfg}
	info, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	_, before = feedIOStat()
	if err := restarted.appFeedAppend(sid, []reply{external}); err != nil {
		t.Fatal(err)
	}
	_, after = feedIOStat()
	if read := after - before; read != info.Size() {
		t.Fatalf("перезапуск: %d байт вместо %d", read, info.Size())
	}
	_, before = feedIOStat()
	if err := restarted.appFeedAppend(sid, []reply{external}); err != nil {
		t.Fatal(err)
	}
	_, after = feedIOStat()
	if after != before {
		t.Fatal("после восстановления индекс снова прочёл файл")
	}
	if got := len(e.s.appFeedRead(sid)); got != 50 {
		t.Fatalf("повтор после перезапуска: %d", got)
	}
}

func TestAppFeedIndexResetsChangedFile(t *testing.T) {
	for _, change := range []string{"truncate", "replace", "same-size"} {
		t.Run(change, func(t *testing.T) {
			e, _ := appEnv(t)
			sid := "as-index-reset"
			file := e.s.appFeedFile(sid)
			old := appIndexReply(t, "old", strings.Repeat("o", 1024))
			if err := e.s.appFeedAppend(sid, []reply{old}); err != nil {
				t.Fatal(err)
			}
			fresh := appIndexReply(t, "new", "новая лента")
			switch change {
			case "truncate":
				if err := os.WriteFile(file, appIndexLine(t, fresh), 0644); err != nil {
					t.Fatal(err)
				}
			case "replace":
				other := file + ".new"
				if err := os.WriteFile(other, appIndexLine(t, fresh), 0644); err != nil {
					t.Fatal(err)
				}
				if err := os.Rename(other, file); err != nil {
					t.Fatal(err)
				}
			case "same-size":
				fresh = appIndexReply(t, "new", strings.Repeat("o", 1024))
				if err := os.WriteFile(file, appIndexLine(t, fresh), 0644); err != nil {
					t.Fatal(err)
				}
				// Метка меняется явно, без ожидания разрешения файловой системы.
				at := time.Unix(1700000000, 0)
				if err := os.Chtimes(file, at, at); err != nil {
					t.Fatal(err)
				}
			}
			wantRead := len(appIndexLine(t, fresh))
			_, before := feedIOStat()
			if err := e.s.appFeedAppend(sid, []reply{fresh, old}); err != nil {
				t.Fatal(err)
			}
			_, after := feedIOStat()
			if read := after - before; read != int64(wantRead) {
				t.Fatalf("смена файла: %d байт вместо %d", read, wantRead)
			}
			got := e.s.appFeedRead(sid)
			if len(got) != 2 || got[0].Text != fresh.Text || got[1].Text != old.Text {
				t.Fatalf("старый индекс пережил смену файла: %+v", got)
			}
		})
	}
}
