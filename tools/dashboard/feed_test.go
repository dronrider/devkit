package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// fatSubLog кладёт при транскрипте боковой журнал заданного веса: записи в нём
// толстые, как у настоящего субагента с выводом команд, и их в журнале сотни.
func fatSubLog(t *testing.T, path, id string, size int) {
	t.Helper()
	pad := strings.Repeat("вывод команды ", 300)
	var b strings.Builder
	for i := 0; b.Len() < size; i++ {
		at := time.Date(2026, 8, 10, 9, 0, 0, 0, time.UTC).Add(time.Duration(i) * time.Second)
		b.WriteString(sideLine(fmt.Sprintf("шаг %d %s", i, pad), at.Format(time.RFC3339)))
	}
	writeSubLog(t, path, id, "работа "+id, b.String())
}

// Лента разговора не читает боковые журналы целиком ради своего хвоста.
// Прежде каждый заход за лентой сливал все журналы сессии от первой строки, и
// разговор с сотней журналов на сотню мегабайт отвечал секундами, причём
// повторный заход стоил столько же: памяти на разбор не было (жалоба
// пользователя «дашборд стал ужасно тормозить»).
//
// Цена меряется разницей с той же лентой без журналов: у стенда своя цена
// (обход корней, доска, ответ утилит), и мерить надо доплату за журналы, а не
// весь запрос.
func TestFeedTailSkipsWholeJournals(t *testing.T) {
	e := newTestEnv(t)
	forgetChunks()
	forgetDigests()
	writeSession(t, e.home, e.proj, "", "aaa-1", transcriptFixture, time.Now())
	fat := writeSession(t, e.home, e.proj, "", "bbb-2", transcriptFixture, time.Now())
	const (
		logs    = 40
		logSize = 1 << 20
	)
	for i := 0; i < logs; i++ {
		fatSubLog(t, fat, fmt.Sprintf("f%02d", i), logSize)
	}
	c := e.loggedClient(t)
	ask := func(sid string) time.Duration {
		at := time.Now()
		resp := doReq(t, c, "GET", e.srv.URL+"/api/projects/demo/sessions/"+sid+"?n=40", "")
		body(t, resp)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("лента %s: %d", sid, resp.StatusCode)
		}
		return time.Since(at)
	}
	// Первый заход разогревает память процесса на всё, что к ленте не
	// относится: доску, обход корней, шапку сессии.
	ask("aaa-1")
	plain := ask("aaa-1")
	cold := ask("bbb-2")
	warm := ask("bbb-2")
	// Сорок мегабайт журналов разбираются около секунды, хвост в четыре
	// десятка записей стоит десятки миллисекунд. Рубеж поставлен с запасом,
	// чтобы стенд не падал от чужой нагрузки на машине.
	if cold-plain > 400*time.Millisecond {
		t.Fatalf("холодная лента с журналами дороже голой на %v (голая %v, с журналами %v)",
			cold-plain, plain, cold)
	}
	if warm-plain > 150*time.Millisecond {
		t.Fatalf("повторная лента с журналами дороже голой на %v (голая %v, с журналами %v)",
			warm-plain, plain, warm)
	}
}

// Хвост ленты, собранный окном, это тот же хвост, что и у ленты, собранной
// целиком: окно режет цену, а не порядок записей. Слияние тут проверяется на
// журналах вперемешку с транскриптом, потому что ради этого порядка лента и
// сливается по времени.
func TestFeedWindowMatchesWholeFeed(t *testing.T) {
	e := newTestEnv(t)
	forgetChunks()
	at := func(i int) string {
		return time.Date(2026, 8, 10, 10, 0, 0, 0, time.UTC).Add(time.Duration(i) * time.Minute).Format(time.RFC3339)
	}
	var main strings.Builder
	for i := 0; i < 60; i += 2 {
		main.WriteString(fmt.Sprintf(
			`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"ход %d"}]},"timestamp":%q}`,
			i, at(i)) + "\n")
	}
	path := writeSession(t, e.home, e.proj, "", "aaa-1", main.String(), time.Now())
	var side strings.Builder
	for i := 1; i < 60; i += 2 {
		side.WriteString(sideLine(fmt.Sprintf("шаг %d", i), at(i)))
	}
	writeSubLog(t, path, "s1", "разбор", side.String())

	whole := sessionFeedOf(path, 0)
	forgetChunks()
	window := sessionFeedOf(path, 20)
	if !whole.whole || window.whole {
		t.Fatalf("признак целой ленты: целиком=%v окном=%v", whole.whole, window.whole)
	}
	want := whole.items[len(whole.items)-20:]
	if len(window.items) != len(want) {
		t.Fatalf("окно в %d записей вместо %d", len(window.items), len(want))
	}
	for i := range want {
		if window.items[i].Key != want[i].Key || window.items[i].Text != want[i].Text {
			t.Fatalf("запись %d окна: %q/%q, у целой ленты %q/%q", i,
				window.items[i].Key, window.items[i].Text, want[i].Key, want[i].Text)
		}
	}
	// Порядок хвоста чередуется: ход сессии, шаг субагента, ход сессии.
	for i := 1; i < len(window.items); i++ {
		if strings.HasPrefix(window.items[i].Text, "шаг") == strings.HasPrefix(window.items[i-1].Text, "шаг") {
			t.Fatalf("хвост окна собран не по времени: %q за %q",
				window.items[i].Text, window.items[i-1].Text)
		}
	}
}

// Ключ записи держится за смещение своей строки в файле, и дописанный журнал
// его не двигает: по ключу режется страница истории, и уехавший ключ
// показывал бы соседние страницы внахлёст.
func TestFeedKeysHoldWhenJournalGrows(t *testing.T) {
	e := newTestEnv(t)
	forgetChunks()
	at := "2026-08-10T10:00:05.000Z"
	path := writeSession(t, e.home, e.proj, "", "aaa-1", transcriptFixture, time.Now())
	log := writeSubLog(t, path, "s1", "разбор", sideLine("первый шаг", at))
	keys := func() map[string]string {
		forgetChunks()
		out := map[string]string{}
		for _, it := range sessionFeedOf(path, 0).items {
			out[it.Text] = it.Key
		}
		return out
	}
	before := keys()
	if before["первый шаг"] == "" {
		t.Fatalf("записи журнала нет в ленте: %v", before)
	}
	appendLine(t, log, sideLine("второй шаг", "2026-08-10T10:00:06.000Z"))
	appendLine(t, path, `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"после"}]},"timestamp":"2026-08-10T10:00:07.000Z"}`+"\n")
	after := keys()
	for text, key := range before {
		if after[text] != key {
			t.Fatalf("ключ записи %q уехал: было %q, стало %q", text, key, after[text])
		}
	}
	if after["второй шаг"] == "" || after["после"] == "" {
		t.Fatalf("дописанного нет в ленте: %v", after)
	}
}

// Страница истории просит окно шире, пока курсор в него не попадёт: у
// разговора с толстыми журналами хвостовое окно курсор давней страницы не
// накрывает, и без дочитывания «раньше» отдавало бы тот же хвост по кругу.
func TestFeedPagesBackThroughWindow(t *testing.T) {
	e := newTestEnv(t)
	forgetChunks()
	at := func(i int) string {
		return time.Date(2026, 8, 10, 10, 0, 0, 0, time.UTC).Add(time.Duration(i) * time.Minute).Format(time.RFC3339)
	}
	var main strings.Builder
	pad := strings.Repeat("длинный вывод ", 400)
	for i := 0; i < 400; i++ {
		main.WriteString(fmt.Sprintf(
			`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"ход %d %s"}]},"timestamp":%q}`,
			i, pad, at(i)) + "\n")
	}
	writeSession(t, e.home, e.proj, "", "aaa-1", main.String(), time.Now())
	c := e.loggedClient(t)
	page := func(query string) []reply {
		resp := doReq(t, c, "GET", e.srv.URL+"/api/projects/demo/sessions/aaa-1?"+query, "")
		var got struct {
			Items []reply `json:"items"`
		}
		if err := json.Unmarshal([]byte(body(t, resp)), &got); err != nil {
			t.Fatal(err)
		}
		return got.Items
	}
	tail := page("n=20")
	if len(tail) != 20 || !strings.HasPrefix(tail[19].Text, "ход 399 ") {
		t.Fatalf("хвост ленты: %d записей, последняя %.20q", len(tail), tail[len(tail)-1].Text)
	}
	// Курсор первой страницы лежит далеко от хвоста: окно под него дочитывается.
	older := page("n=20&before=" + tail[0].Key)
	if len(older) != 20 || !strings.HasPrefix(older[19].Text, "ход 379 ") {
		t.Fatalf("страница раньше хвоста: %d записей, последняя %.20q", len(older), older[len(older)-1].Text)
	}
}
