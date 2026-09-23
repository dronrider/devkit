package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
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
// Цена меряется следом ленты на диске: сколько файлов она открыла и сколько
// байтов прочла (feedIOStat). Стенное время тут не годится вовсе. Прогон
// слияния поднимает восемь воркеров разом, разбор сорока мегабайт под такой
// нагрузкой идёт вдвое дольше, и рубеж в миллисекундах отбивал чужие ветки на
// ровном месте (DK-746). Прочитанные байты от нагрузки не зависят: хвост
// читает у журнала кусок с конца, а разбор целиком читает весь файл, и разница
// между ними больше чем на порядок.
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
	ask := func(sid string) (opens, read int64) {
		opens0, read0 := feedIOStat()
		resp := doReq(t, c, "GET", e.srv.URL+"/api/projects/demo/sessions/"+sid+"?n=40", "")
		body(t, resp)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("лента %s: %d", sid, resp.StatusCode)
		}
		opens1, read1 := feedIOStat()
		return opens1 - opens0, read1 - read0
	}
	// Первый заход разогревает память процесса на всё, что к ленте не
	// относится: доску, обход корней, шапку сессии.
	ask("aaa-1")
	coldOpens, coldRead := ask("bbb-2")
	warmOpens, warmRead := ask("bbb-2")
	const whole = logs * logSize
	// Холодный заход берёт у каждого журнала кусок с конца, и на сорок
	// мегабайт журналов приходится пара мегабайт чтения. Рубеж стоит вдесятеро
	// ниже полного веса: лента, читающая журналы целиком, переваливает за него
	// с первого же захода, а честному хвосту до него далеко при любом числе
	// дочитываний.
	if coldRead > whole/10 {
		t.Fatalf("холодная лента прочла %d байт из %d за %d файлов, а должна брать хвосты",
			coldRead, whole, coldOpens)
	}
	// Повторный заход держится памятью на разобранные куски и на диск не ходит
	// вовсе: отпечатки файлов те же.
	if warmOpens != 0 || warmRead != 0 {
		t.Fatalf("повторная лента открыла %d файлов и прочла %d байт, а должна отвечать из памяти",
			warmOpens, warmRead)
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

// fixture читает транскрипт из testdata: без него тесту склейки не на чем
// стоять, и пустой файл тут это провал стенда, а не пустая лента.
func fixture(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// fixtureKeys считает по самому файлу, под каким ключом каждая его запись
// обязана встать в ленту: источник, смещение строки в байтах и номер блока
// внутри строки. Ключ тут пересчитывается своим счётом, а не берётся у разбора,
// иначе подмена ключа сошлась бы сама с собой.
func fixtureKeys(t *testing.T, data, src string) map[string]string {
	t.Helper()
	out := map[string]string{}
	off := 0
	for _, line := range strings.SplitAfter(data, "\n") {
		if strings.TrimSpace(line) == "" {
			off += len(line)
			continue
		}
		var rec struct {
			Message struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("транскрипт из testdata не разобрался: %v", err)
		}
		var text string
		if json.Unmarshal(rec.Message.Content, &text) != nil {
			var blocks []struct {
				Text string `json:"text"`
			}
			if err := json.Unmarshal(rec.Message.Content, &blocks); err != nil || len(blocks) == 0 {
				t.Fatalf("в записи транскрипта нет текста: %s", line)
			}
			text = blocks[0].Text
		}
		out[text] = fmt.Sprintf("%s:%d.0", src, off)
		off += len(line)
	}
	return out
}

// Склейка захода в ленту. Разбор записи накопителя идёт сессией, которая зовёт
// субагентов, и разговор человека с грумером лежит не в одном файле: реплики
// сессии в её транскрипте, работа субагента в своём боковом журнале. Прежде
// склейку заходов черновику собирала бы своя серверная ручка (LLD DK-354,
// решение 2), а собрала её общая лента, и проверяется тут именно она: файлы
// разговора сливаются по времени, а ключ записи держится за смещение её строки
// в своём файле. Ключ несущий: по нему режется страница истории и по нему
// приходит дописанное стримом, поэтому счёт от номера записи в разборе вместо
// смещения ленту молча разъезжает.
func TestFeedGluesDraftRunFiles(t *testing.T) {
	e := newTestEnv(t)
	forgetChunks()
	run, side := fixture(t, "testdata/feed_draft_run.jsonl"), fixture(t, "testdata/feed_draft_sub.jsonl")
	path := writeSession(t, e.home, e.proj, "", "aaa-1", run, time.Now())
	log := writeSubLog(t, path, "s1", "поиск дублей", side)

	items := sessionFeedOf(path, 0).items
	var texts []string
	for _, it := range items {
		texts = append(texts, it.Text)
	}
	want := []string{
		"Проведи груминг DK-900",
		"поднимаю разбор записи",
		"смотрю накопитель",
		"дублей у записи нет",
		"спрашиваю человека, режем или ждём",
		"завёл строку DK-900",
	}
	if strings.Join(texts, "|") != strings.Join(want, "|") {
		t.Fatalf("лента захода собрана не по времени:\n%s\nждали:\n%s",
			strings.Join(texts, "\n"), strings.Join(want, "\n"))
	}
	// Заказ субагенту в ленту не едет: он уже стоит карточкой вызова в
	// транскрипте, и вторым разом читался бы репликой человека.
	for _, text := range texts {
		if text == "поищи дубли по доске" {
			t.Errorf("заказ субагенту встал в ленту второй копией: %v", texts)
		}
	}

	keys := fixtureKeys(t, run, mainSrc)
	for text, key := range fixtureKeys(t, side, srcName(log)) {
		keys[text] = key
	}
	for _, it := range items {
		if keys[it.Text] == "" {
			continue
		}
		if it.Key != keys[it.Text] {
			t.Errorf("ключ записи %q: %q, а по смещению её строки в файле %q",
				it.Text, it.Key, keys[it.Text])
		}
	}
}
