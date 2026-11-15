package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"reflect"
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

// Клиент пишет в транскрипт реплику, пришедшую скобочной вставкой, обёрткой
// pasted_content, а журнал хранит слова человека без неё. Обёртка это не слова
// человека: пузырь показывает их без тегов, и эхо ту же реплику вытесняет, а
// не даёт второй пузырь.
func TestFeedUnwrapsPastedEcho(t *testing.T) {
	e, c := chatEnv(t)
	wrapped := `{"type":"user","message":{"role":"user","content":"\n\n<pasted_content id=\"552a\">\nработа идёт\n</pasted_content id=\"552a\">\n"},"timestamp":"2026-08-17T10:00:01.000Z","gitBranch":"main"}` + "\n"
	writeSession(t, e.home, e.proj, "", "aaaa-1111", wrapped, time.Now())
	if err := e.s.saidPut(saidSessionKey("aaaa-1111"),
		saidRec{Time: "2026-08-17T10:00:05Z", Text: "работа идёт"}); err != nil {
		t.Fatal(err)
	}
	got := userTexts(saidFeed(t, c, e, "aaaa-1111"))
	if len(got) != 1 || got[0] != "работа идёт" {
		t.Fatalf("обёртка вставки дала дубль или осталась в пузыре: %q", got)
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

// Журнал задачи, уехавшей с доски, в чужую ленту больше не подмешивается:
// критерий теперь живая строка доски, а не разряд привязки (DK-1199). Заказ
// дашборда остаётся ведущим XR-9 и после того, как строка ушла с доски
// (talkSrc переживает отвязку), но раз строки больше нет, читать там нечего.
func TestFeedHidesTaskJournalWhenRowLeftBoard(t *testing.T) {
	e, c := chatEnv(t)
	writeSession(t, e.home, e.proj, "", "aaaa-1111", plainTalk, time.Now())
	writeBinds(t, e.home, bindRecord(e.home, "2026-08-18T12:00:00", "aaaa-1111", "XR-9", bindOrder))
	if err := e.s.saidPut(saidTaskKey("XR-9"),
		saidRec{Time: "2026-08-17T10:00:05Z", Text: "ответ на вопрос"}); err != nil {
		t.Fatal(err)
	}
	got := userTexts(saidFeed(t, c, e, "aaaa-1111"))
	for _, text := range got {
		if text == "ответ на вопрос" {
			t.Fatalf("журнал задачи вне доски попал в чужую ленту: %q", got)
		}
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
// из транскрипта: в пузыре слова человека, выделение, картинки и фрагменты при
// них. Снимков и фрагментов в одной реплике бывает по несколько.
func TestSaidOfCutsPrefixes(t *testing.T) {
	wire := "<screenshot file=\"/tmp/a.png\">\nвставлен снимок экрана\n</screenshot>\n" +
		"<screenshot file=\"/tmp/b.png\">\nвставлен снимок экрана\n</screenshot>\n" +
		"<fragment file=\"/tmp/c.txt\">\nвставлен фрагмент 4 КБ\n</fragment>\n" +
		"<selection file=\"постановка\">\nкусок текста\n</selection>\nчто это значит"
	got := saidOf(wire, "socket")
	if got.Text != "что это значит" || got.Sel != "кусок текста" ||
		got.SelFile != "постановка" {
		t.Fatalf("разбор отправленного: %+v", got)
	}
	if len(got.Shots) != 2 || got.Shots[0] != "/tmp/a.png" || got.Shots[1] != "/tmp/b.png" {
		t.Fatalf("снимки отправленного: %+v", got.Shots)
	}
	if len(got.Frags) != 1 || got.Frags[0] != "/tmp/c.txt" {
		t.Fatalf("фрагменты отправленного: %+v", got.Frags)
	}
}

// Смешанный порядок вложений снимается целиком: панель кладёт префиксы в
// порядке вставки, и фрагмент перед снимком это обычная реплика. Съём двумя
// последовательными проходами оставлял обёртку снимка в тексте, и она уезжала
// в журнал и пузырь после перезагрузки.
func TestCutWrapsMixedOrder(t *testing.T) {
	wire := "<fragment file=\"/tmp/c.txt\">\nвставлен фрагмент 4 КБ\n</fragment>\n" +
		"<screenshot file=\"/tmp/a.png\">\nвставлен снимок экрана\n</screenshot>\n" +
		"<fragment file=\"/tmp/d.txt\">\nвставлен ещё фрагмент\n</fragment>\n" +
		"<screenshot file=\"/tmp/b.png\">\nвставлен второй снимок\n</screenshot>\nчто это значит"
	w := cutWraps(wire)
	if w.rest != "что это значит" {
		t.Fatalf("смешанный порядок оставил обёртку в тексте: %q", w.rest)
	}
	if len(w.frags) != 2 || w.frags[0] != "/tmp/c.txt" || w.frags[1] != "/tmp/d.txt" {
		t.Fatalf("фрагменты смешанного порядка: %+v", w.frags)
	}
	if len(w.shots) != 2 || w.shots[0] != "/tmp/a.png" || w.shots[1] != "/tmp/b.png" {
		t.Fatalf("снимки смешанного порядка: %+v", w.shots)
	}
}

// Старая строка журнала несла один снимок полем shot: записи в файле перезаписи
// не подлежат, и старое поле читается наравне со списком.
func TestSaidRepliesMergeLegacyShot(t *testing.T) {
	lines := []string{
		`{"time":"2026-10-17T10:00:01Z","text":"один снимок","shot":"/tmp/old.png"}`,
		`{"time":"2026-10-17T10:00:02Z","text":"два снимка","shots":["/tmp/a.png","/tmp/b.png"],"frags":["/tmp/c.txt"]}`,
	}
	got, _ := saidReplies(lines, saidSrc+"sess-x", 0)
	if len(got) != 2 {
		t.Fatalf("строки журнала не разобрались: %+v", got)
	}
	if len(got[0].Shots) != 1 || got[0].Shots[0] != "/tmp/old.png" {
		t.Fatalf("старое поле снимка потерялось: %+v", got[0].Shots)
	}
	if len(got[1].Shots) != 2 || len(got[1].Frags) != 1 {
		t.Fatalf("списки вложений не доехали: %+v", got[1])
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

// Смена модели диалога оставляет след в самой ленте: разделитель, который
// живёт журналом разговора и потому переживает и перерисовку панели, и
// перезагрузку страницы. Раньше смена не оставляла ничего, и человек,
// вернувшийся к разговору, читал подряд ответы двух разных моделей.
func TestModelChangeMarksFeed(t *testing.T) {
	e, c := chatEnv(t)
	sid := "aaaa-1111"
	writeSession(t, e.home, e.proj, "", sid, plainTalk, time.Now())
	at := e.srv.URL + "/api/projects/demo/chats/" + sid + "/model"
	if resp := doReq(t, c, "POST", at, `{"model":"fable"}`); resp.StatusCode != http.StatusOK {
		t.Fatalf("смена модели: %d %s", resp.StatusCode, body(t, resp))
	}
	items := saidFeed(t, c, e, sid)
	var marks []string
	for _, it := range items {
		if it.Role == roleMark {
			marks = append(marks, it.Text)
		}
	}
	if len(marks) != 1 || marks[0] != "модель изменена: opus -> fable" {
		t.Fatalf("разделитель смены модели не встал в ленту: %q (лента %+v)", marks, items)
	}
	// Пометка это не реплика человека: пузырём она не рисуется и в вводную
	// резюма не подкладывается.
	if said := userTexts(items); len(said) != 1 || said[0] != "работа идёт" {
		t.Errorf("пометка уехала пузырём человека: %q", said)
	}
	if lost := e.s.lostSaid(sid, time.Time{}); len(lost) != 0 {
		t.Errorf("пометка попала в недоставленные реплики резюма: %q", lost)
	}
	// Повторный выбор той же модели ничего не меняет и второго разделителя не
	// заводит: рубеж это смена, а не нажатие.
	if resp := doReq(t, c, "POST", at, `{"model":"fable"}`); resp.StatusCode != http.StatusOK {
		t.Fatalf("повтор смены: %d", resp.StatusCode)
	}
	again := 0
	for _, it := range saidFeed(t, c, e, sid) {
		if it.Role == roleMark {
			again++
		}
	}
	if again != 1 {
		t.Errorf("выбор той же модели завёл второй разделитель: %d", again)
	}
}

// Номер записи журнала один и тот же, каким бы путём запись ни приехала в
// ленту: обычным ответом или дочитыванием в потоке. Пока поток считал номера
// по выжившим после слияния записям, дописанная реплика приезжала под чужим
// ключом, лента видела в ней вторую запись и показывала реплику дважды
// (жалоба пользователя про дубль, уходивший с обновлением чата).
func TestStreamSaidKeysMatchFeed(t *testing.T) {
	e, c := chatEnv(t)
	sid := "aaaa-1111"
	writeSession(t, e.home, e.proj, "", sid, plainTalk, time.Now())
	key := saidSessionKey(sid)
	// Первая запись журнала уже отражена транскриптом: слияние её выбрасывает,
	// и счёт выживших расходится с номерами строк файла.
	if err := e.s.saidPut(key, saidRec{Time: "2026-08-17T10:00:01Z", Text: "работа идёт", Way: "socket"}); err != nil {
		t.Fatal(err)
	}
	resp := doReq(t, c, "GET", e.srv.URL+"/api/projects/demo/sessions/"+sid+"?stream=1", "")
	defer resp.Body.Close()
	if err := e.s.saidPut(key, saidRec{Time: "2026-08-17T10:00:09Z", Text: "с ноутбука"}); err != nil {
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
	// Ключ той же записи в обычном ответе ленты: по нему лента и отсеивает
	// повторы, и разойтись эти два ключа не имеют права.
	want := ""
	for _, it := range saidFeed(t, c, e, sid) {
		if it.Text == "с ноутбука" {
			want = it.Key
		}
	}
	if want != saidSrc+key+":1" {
		t.Fatalf("ключ записи в обычной ленте: %q", want)
	}
	if !strings.Contains(seen, `"key":"`+want+`"`) {
		t.Fatalf("поток назвал ту же запись другим ключом: %q (ждали %s)", seen, want)
	}
}

// Лента истории идёт разговором: реплика человека из журнала стоит между теми
// записями транскрипта, между которыми она сказана. Лента собирается хвостом
// файла, а журнал разговора лежит целиком, и слияние сваливало всё, что старше
// окна, одной кучей перед первой записью окна. Человек, листавший историю
// вверх, получал целую страницу своих реплик подряд, за неделю разом:
// «сгруппировал все сообщения, и теперь мои я вижу одной пачкой все свои
// сообщения, полистав вверх чат».
func TestFeedHistoryKeepsSaidInOrder(t *testing.T) {
	e, c := chatEnv(t)
	forgetChunks()
	base := time.Date(2026, 8, 10, 10, 0, 0, 0, time.UTC)
	at := func(i int) string { return base.Add(time.Duration(i) * time.Minute).Format(time.RFC3339) }
	// Записи толстые нарочно: окно ленты набирается кусками файла, и на тонком
	// транскрипте окно накрыло бы разговор целиком, а стенду нужен хвост.
	pad := strings.Repeat("длинный вывод ", 400)
	var main strings.Builder
	for i := 0; i < 400; i++ {
		main.WriteString(fmt.Sprintf(
			`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"ход %d %s"}]},"timestamp":%q}`,
			i, pad, at(i)) + "\n")
	}
	writeSession(t, e.home, e.proj, "", "aaaa-2222", main.String(), time.Now())
	// Реплики человека раскиданы по всему разговору, по одной на каждый
	// десяток ходов, и эха у них в транскрипте нет.
	for i := 5; i < 400; i += 10 {
		if err := e.s.saidPut(saidSessionKey("aaaa-2222"),
			saidRec{Time: at(i), Text: fmt.Sprintf("сказано %d", i)}); err != nil {
			t.Fatal(err)
		}
	}
	page := func(query string) []reply {
		resp := doReq(t, c, "GET", e.srv.URL+"/api/projects/demo/sessions/aaaa-2222?"+query, "")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("лента %s: %d", query, resp.StatusCode)
		}
		var got struct {
			Items []reply `json:"items"`
		}
		if err := json.Unmarshal([]byte(body(t, resp)), &got); err != nil {
			t.Fatal(err)
		}
		return got.Items
	}
	// Лента собирается так же, как её собирает панель: хвост, дальше страницы
	// истории приписками сверху по ключу первой показанной записи.
	talk := page("n=20")
	if len(talk) == 0 {
		t.Fatal("хвост ленты пуст")
	}
	for i := 0; i < 30 && talk[0].Key != ""; i++ {
		older := page("n=20&before=" + talk[0].Key)
		if len(older) == 0 {
			break
		}
		talk = append(append([]reply{}, older...), talk...)
	}
	// Каждая реплика человека стоит между своими соседями по времени: слева
	// ход того же времени, справа следующий.
	seen := 0
	for i, it := range talk {
		if it.Role != "user" {
			continue
		}
		seen++
		var idx int
		if _, err := fmt.Sscanf(it.Text, "сказано %d", &idx); err != nil {
			t.Fatalf("чужая реплика в ленте: %q", it.Text)
		}
		before, after := "", ""
		if i > 0 {
			before = talk[i-1].Text
		}
		if i+1 < len(talk) {
			after = talk[i+1].Text
		}
		if !strings.HasPrefix(before, fmt.Sprintf("ход %d ", idx)) ||
			!strings.HasPrefix(after, fmt.Sprintf("ход %d ", idx+1)) {
			t.Fatalf("реплика %q встала не на своё место: перед ней %.12q, за ней %.12q",
				it.Text, before, after)
		}
	}
	if seen < 10 {
		t.Fatalf("реплик человека в ленте %d, история их не отдала", seen)
	}
}

func taskSaidBindTime(at string) string {
	parsed, _ := time.Parse(time.RFC3339, at)
	return bindTimeSince(parsed.Unix())
}

// Снятие границы feedFrom на полном транскрипте возвращало весь task-журнал.
func TestTaskSaidConversationBoundary(t *testing.T) {
	for _, mode := range []string{"since", "born", "old-window", "continuation", "resume", "legacy"} {
		t.Run(mode, func(t *testing.T) {
			e, c := chatEnv(t)
			const sid = "aaaa-1717"
			const born = "2026-08-17T10:00:01Z"
			transcript := passTalk("начало", born)
			if mode == "legacy" {
				transcript = `{"type":"user","message":{"role":"user","content":"начало"},"gitBranch":"main"}` + "\n"
			}
			if mode == "since" {
				transcript = `{"type":"system","timestamp":"2026-08-16T08:00:00Z"}` + "\n" + transcript
			}
			if mode == "born" {
				transcript = `{"type":"system","timestamp":"2026-08-17T10:00:00Z"}` + "\n" + transcript
			}
			writeSession(t, e.home, e.proj, "-xr-4", sid, transcript, time.Now())
			writeBinds(t, e.home, passBind(e.home, taskSaidBindTime("2026-08-17T10:00:01Z"), sid, "XR-4", e.proj, "chat-XR-4-2"))
			lower := "2026-08-17T10:00:00Z"
			switch mode {
			case "since":
				at, _ := time.Parse(time.RFC3339, lower)
				writeChatStore(t, e.home, "tmux-chat-XR-4-2", chatStore{Since: at.Unix(), From: "blank-1717"})
			case "resume":
				lower = born
				at, _ := time.Parse(time.RFC3339, "2026-08-17T10:00:02Z")
				writeChatStore(t, e.home, "tmux-chat-XR-4-2", chatStore{Since: at.Unix(), From: sid})
				writeBinds(t, e.home, passBind(e.home, taskSaidBindTime("2026-08-17T10:00:03Z"), sid, "XR-4", e.proj, "chat-XR-4-2"))
			case "old-window":
				at, _ := time.Parse(time.RFC3339, "2026-08-18T10:00:00Z")
				writeChatStore(t, e.home, "tmux-chat-XR-4-2", chatStore{Since: at.Unix()})
				lower = born
			case "continuation":
				lower = "2026-08-17T09:00:00Z"
				writeSession(t, e.home, e.proj, "-xr-4", "bbbb-1717", passTalk("прошлый заход", lower), time.Now())
				writeBinds(t, e.home,
					passBind(e.home, taskSaidBindTime("2026-08-17T09:00:00Z"), "bbbb-1717", "XR-4", e.proj, "chat-XR-4-2"),
					passBind(e.home, taskSaidBindTime("2026-08-17T10:00:01Z"), sid, "XR-4", e.proj, "chat-XR-4-2"))
				if err := e.s.saidPut(saidSessionKey("bbbb-1717"), saidRec{Time: "2026-08-17T08:30:00Z", Text: "свой прошлый"}); err != nil {
					t.Fatal(err)
				}
			case "born":
				lower = "2026-08-17T10:00:00Z"
			}
			for _, rec := range []saidRec{
				{Time: "2026-08-16T10:00:00Z", Text: "чужое старое"},
				{Time: lower, Text: "на границе"},
				{Time: "2026-08-17T09:30:00Z", Text: "ответ до продолжателя"},
				{Time: "2026-08-17T10:01:00Z", Text: "ответ парковке"},
			} {
				if err := e.s.saidPut(saidTaskKey("XR-4"), rec); err != nil {
					t.Fatal(err)
				}
			}
			if err := e.s.saidPut(saidSessionKey(sid), saidRec{Time: "2026-08-15T00:00:00Z", Text: "свой до начала"}); err != nil {
				t.Fatal(err)
			}
			read := func(query string) struct {
				Items []reply
				Start bool
				Total int
			} {
				resp := doReq(t, c, "GET", e.srv.URL+"/api/projects/demo/sessions/"+sid+query, "")
				var got struct {
					Items []reply
					Start bool
					Total int
				}
				if err := json.Unmarshal([]byte(body(t, resp)), &got); err != nil {
					t.Fatal(err)
				}
				return got
			}
			all := read("?n=100")
			if !all.Start {
				t.Fatalf("начало не достигнуто: %+v", all)
			}
			texts := userTexts(all.Items)
			joined := strings.Join(texts, "|")
			if !strings.Contains(joined, "свой до начала") || !strings.Contains(joined, "на границе") || !strings.Contains(joined, "ответ парковке") {
				t.Fatalf("потеря допустимых реплик: %q", texts)
			}
			if mode == "legacy" && !strings.Contains(joined, "чужое старое") {
				t.Fatalf("legacy без временных меток потерял журнал: %q", texts)
			}
			if mode != "legacy" && strings.Contains(joined, "чужое старое") {
				t.Fatalf("старый task-журнал попал в новый разговор: %q", texts)
			}
			if mode == "continuation" && (!strings.Contains(joined, "ответ до продолжателя") || !strings.Contains(joined, "свой прошлый") || !strings.Contains(joined, "прошлый заход")) {
				t.Fatalf("потерян прошлый заход: %q", texts)
			}
			var pages []reply
			query := "?n=2"
			for i := 0; i < 20; i++ {
				page := read(query)
				pages = append(page.Items, pages...)
				if page.Start || len(page.Items) == 0 {
					break
				}
				query = "?n=2&before=" + url.QueryEscape(page.Items[0].Key)
			}
			if !reflect.DeepEqual(userTexts(pages), texts) {
				t.Fatalf("страницы отличаются от полной ленты: %q / %q", userTexts(pages), texts)
			}
			if all.Total != len(all.Items) {
				t.Fatalf("total=%d items=%d", all.Total, len(all.Items))
			}
		})
	}
}

func TestTaskSaidStreamBoundary(t *testing.T) {
	e, c := chatEnv(t)
	const sid = "aaaa-1717"
	writeSession(t, e.home, e.proj, "-xr-4", sid, passTalk("начало", "2026-08-17T10:00:01Z"), time.Now())
	writeBinds(t, e.home, passBind(e.home, taskSaidBindTime("2026-08-17T10:00:01Z"), sid, "XR-4", e.proj, "chat-XR-4-2"))
	since, _ := time.Parse(time.RFC3339, "2026-08-17T10:00:00Z")
	writeChatStore(t, e.home, "tmux-chat-XR-4-2", chatStore{Since: since.Unix()})
	put := func(key, at, text string) {
		t.Helper()
		if err := e.s.saidPut(key, saidRec{Time: at, Text: text}); err != nil {
			t.Fatal(err)
		}
	}
	put(saidTaskKey("XR-4"), "2026-08-16T00:00:00Z", "чужое в снимке")
	put(saidTaskKey("XR-4"), "2026-08-17T10:00:02Z", "снимок готов")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", e.srv.URL+"/api/projects/demo/sessions/"+sid+"?stream=1", nil)
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	scan := bufio.NewScanner(resp.Body)
	var got []reply
	until := func(text string) {
		t.Helper()
		for scan.Scan() {
			line := scan.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			var item reply
			if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &item) != nil {
				continue
			}
			got = append(got, item)
			if item.Text == text {
				return
			}
		}
		t.Fatalf("нет %q: %v, %q", text, scan.Err(), userTexts(got))
	}
	until("снимок готов")
	put(saidTaskKey("XR-4"), "2026-08-16T00:00:00Z", "чужое в дострении")
	put(saidSessionKey(sid), "2026-08-15T00:00:00Z", "свой до начала")
	put(saidTaskKey("XR-4"), "2026-08-17T10:00:03Z", "дострение готово")
	until("дострение готово")
	texts := strings.Join(userTexts(got), "|")
	if strings.Contains(texts, "чужое") || !strings.Contains(texts, "свой до начала") {
		t.Fatalf("неверная граница потока: %q", texts)
	}
	var taskKeys []string
	for _, item := range got {
		if strings.HasPrefix(item.Key, "said-task-") {
			taskKeys = append(taskKeys, item.Key)
		}
	}
	if !reflect.DeepEqual(taskKeys, []string{"said-task-XR-4:1", "said-task-XR-4:3"}) {
		t.Fatalf("поток сменил ключи: %q", taskKeys)
	}
}
