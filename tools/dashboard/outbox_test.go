package main

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// saidFeed берёт ленту сессии ручкой разговора.
func saidFeed(t *testing.T, c *http.Client, e *testEnv, sid string) []reply {
	t.Helper()
	resp := doReq(t, c, "GET", e.srv.URL+"/api/projects/demo/sessions/"+sid, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("лента %s: %d", sid, resp.StatusCode)
	}
	var got struct {
		Items []reply `json:"items"`
	}
	if err := json.Unmarshal([]byte(body(t, resp)), &got); err != nil {
		t.Fatal(err)
	}
	return got.Items
}

func userTexts(items []reply) []string {
	var out []string
	for _, it := range items {
		if it.Role == "user" {
			out = append(out, it.Text)
		}
	}
	return out
}

// Отправленная реплика видна на любом открытом экране, а не только у
// отправителя: дашборд ведёт свой журнал отправленного, и лента подмешивает его
// к транскрипту. Пузырь отправителя тут ни при чём, ответ ручки читает второе
// устройство.
func TestFeedShowsSaidJournal(t *testing.T) {
	e, c := chatEnv(t)
	writeSession(t, e.home, e.proj, "", "aaaa-1111", plainTalk, time.Now())
	if err := e.s.saidPut(saidSessionKey("aaaa-1111"),
		saidRec{Time: "2026-08-17T10:00:05Z", Text: "с телефона", Way: "socket"}); err != nil {
		t.Fatal(err)
	}
	got := userTexts(saidFeed(t, c, e, "aaaa-1111"))
	if len(got) != 2 || got[0] != "работа идёт" || got[1] != "с телефона" {
		t.Fatalf("лента без реплики с другого устройства: %q", got)
	}
}

// Эхо из транскрипта ту же реплику вытесняет: агент её прочитал, и показывать
// её дважды нечестно.
func TestFeedDropsEchoedSaid(t *testing.T) {
	e, c := chatEnv(t)
	writeSession(t, e.home, e.proj, "", "aaaa-1111", plainTalk, time.Now())
	if err := e.s.saidPut(saidSessionKey("aaaa-1111"),
		saidRec{Time: "2026-08-17T10:00:05Z", Text: "работа идёт"}); err != nil {
		t.Fatal(err)
	}
	got := userTexts(saidFeed(t, c, e, "aaaa-1111"))
	if len(got) != 1 {
		t.Fatalf("реплика показана дважды: %q", got)
	}
}

// Ответ задаче уходит безадресной строкой во вход и в транскрипт не попадает
// вовсе. Журнал разговора задачи показывает его ленте той сессии, что задачу
// ведёт.
func TestTaskMessageSeenInFeed(t *testing.T) {
	e, c := chatEnv(t)
	writeSession(t, e.home, e.proj, "-xr-4", "aaaa-1111", plainTalk, time.Now())
	sideTree(t, e.proj, "xr-4")

	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/tasks/XR-4/message",
		`{"text": "ответ на вопрос"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("ответ задаче: %d %s", resp.StatusCode, body(t, resp))
	}
	raw := readFile(t, filepath.Join(e.home, "said", "task-XR-4.jsonl"))
	if !strings.Contains(raw, `"ответ на вопрос"`) {
		t.Fatalf("журнал разговора задачи пуст: %q", raw)
	}
	got := userTexts(saidFeed(t, c, e, "aaaa-1111"))
	if len(got) != 2 || got[1] != "ответ на вопрос" {
		t.Fatalf("лента сессии задачи не показала ответ: %q", got)
	}
}

// Реплика ручкой сессии тоже оседает в журнале: подхват доставит её вставкой
// хода, а в транскрипте она не появится, и второе устройство её иначе не
// увидит.
func TestSessionMessageSeenInFeed(t *testing.T) {
	e, c := chatEnv(t)
	writeSession(t, e.home, e.proj, "-xr-4", "aaaa-1111", plainTalk, time.Now())
	sideTree(t, e.proj, "xr-4")

	resp := postSessionMessage(t, c, e, "aaaa-1111", "правь ленту")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("реплика сессии: %d %s", resp.StatusCode, body(t, resp))
	}
	got := userTexts(saidFeed(t, c, e, "aaaa-1111"))
	if len(got) != 2 || got[1] != "правь ленту" {
		t.Fatalf("лента не показала свою же реплику: %q", got)
	}
}

// Приложенное к реплике едет в журнал отдельными полями, как его читает лента
// из транскрипта: в пузыре слова человека, выделение и картинка при них.
func TestSaidOfCutsPrefixes(t *testing.T) {
	wire := "<screenshot file=\"/tmp/a.png\">\nвставлен снимок экрана\n</screenshot>\n" +
		"<selection file=\"постановка\">\nкусок текста\n</selection>\nчто это значит"
	got := saidOf(wire, "socket")
	if got.Text != "что это значит" || got.Sel != "кусок текста" ||
		got.SelFile != "постановка" || got.Shot != "/tmp/a.png" {
		t.Fatalf("разбор отправленного: %+v", got)
	}
}

// Ключ записи журнала устойчив и продолжает счёт строк файла: по нему лента
// отсеивает повтор, пришедший стримом следом за первым куском.
func TestSaidRepliesKeepKeys(t *testing.T) {
	lines := []string{
		`{"time":"2026-08-17T10:00:05Z","text":"раз"}`,
		"битая строка",
		`{"time":"2026-08-17T10:00:06Z","text":"два"}`,
	}
	got, next := saidReplies(lines, saidSrc+"sess-a", 3)
	if len(got) != 2 || got[0].Key != "said-sess-a:3" || got[1].Key != "said-sess-a:5" {
		t.Fatalf("ключи записей журнала: %+v", got)
	}
	if next != 6 {
		t.Fatalf("следующий номер строки %d, ждал 6", next)
	}
}

// Стрим разносит отправленное всем подписчикам: реплика, легшая в журнал при
// открытом потоке, приходит его событием, а не остаётся местным пузырём.
func TestStreamSendsSaid(t *testing.T) {
	e, c := chatEnv(t)
	writeSession(t, e.home, e.proj, "", "aaaa-1111", plainTalk, time.Now())
	resp := doReq(t, c, "GET", e.srv.URL+"/api/projects/demo/sessions/aaaa-1111?stream=1", "")
	defer resp.Body.Close()
	if err := e.s.saidPut(saidSessionKey("aaaa-1111"),
		saidRec{Time: "2026-08-17T10:00:09Z", Text: "с ноутбука"}); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 4096)
	deadline := time.Now().Add(5 * time.Second)
	seen := ""
	for time.Now().Before(deadline) && !strings.Contains(seen, "с ноутбука") {
		n, err := resp.Body.Read(buf)
		seen += string(buf[:n])
		if err != nil {
			break
		}
	}
	if !strings.Contains(seen, "с ноутбука") {
		t.Fatalf("поток не разнёс отправленное: %q", seen)
	}
}
