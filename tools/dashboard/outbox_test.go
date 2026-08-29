package main

import (
	"encoding/json"
	"fmt"
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
