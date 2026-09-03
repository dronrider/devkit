package main

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/dronrider/devkit/internal/chat"
)

// Строка журнала про молчащий вопрос называет дорогу, которая отказала. Дорог
// две, признак ожидания и снимок панели, а строка до этой правки была одна на
// обе и всегда звала чинить разбор. По ней случай 01.09 (клиенты chat-13 и
// chat-14, девять строк за двое суток) не раскладывался: разбор снимка работал,
// а строка на него показывала.

// askRoadEnv поднимает разговор, который по журналу уведомителя стоит на
// вопросе разрешения: без этого признака жалоба в журнал не идёт вовсе.
func askRoadEnv(t *testing.T, sid, task string, now time.Time) (*testEnv, *http.Client, *logCapture) {
	t.Helper()
	e, c := chatEnv(t)
	lc := &logCapture{}
	e.s.logf = lc.log
	e.s.now = func() time.Time { return now }
	writeSession(t, e.home, e.proj, "", sid, plainTalk, now.Add(-30*time.Second))
	writeBinds(t, e.home, fmt.Sprintf("2026-08-25T14:59:00 сессия %s задача %s проект demo "+
		"дерево %s транскрипт /tmp/t.jsonl источник заказ повод startup tmux chat-13\n", sid, task, e.proj))
	writeNotifyLog(t, e.home, []string{permissionNotify(sid)})
	return e, c, lc
}

// askRoadPane подсовывает снимок панели и список tmux одним скриптом.
func askRoadPane(t *testing.T, e *testEnv, pane string) {
	t.Helper()
	writeScript(t, e.bin, "tmux", `case "$1" in
capture-pane) printf '%s' `+shQuote(pane)+`;;
send-keys) :;;
ls) printf 'chat-13\t1\t1786000000\n';;
esac
exit 0`)
}

// askRoadLine достаёт из журнала строку про молчащий вопрос.
func askRoadLine(t *testing.T, lc *logCapture) string {
	t.Helper()
	for _, ln := range lc.lines {
		if strings.Contains(ln, "похоже ждёт ответа") {
			return ln
		}
	}
	t.Fatalf("про молчащий вопрос в журнал не сказано: %v", lc.lines)
	return ""
}

// Признак ожидания лежит живым, но ждёт чужую сессию: отказала дорога признака,
// и строка журнала называет задачу и ту сессию, которую признак ждёт. Прежде
// строка звала чинить разбор снимка, хотя разбирать было нечего.
func TestAskQuietNamesSignRoad(t *testing.T) {
	now := time.Date(2026, 9, 1, 14, 0, 0, 0, time.Local)
	sid := "eeee5555-5555-4555-8555-555555555555"
	e, c, lc := askRoadEnv(t, sid, "XR-1", now)
	alien := "9999aaaa-7777-4777-8777-777777777777"
	writeAskPack(t, e.proj, "XR-9", alien, now.Add(5*time.Minute), chat.Question{Text: "куда катить"})
	askRoadPane(t, e, " Работаю дальше, вопросов нет.\n\n\u276f \n")

	body(t, doReq(t, c, "GET", e.srv.URL+"/api/projects/demo/chats/"+sid+"/ask", ""))
	line := askRoadLine(t, lc)
	// Обе дороги отчитываются в одной строке, и по ней видно, что признак лежал,
	// да не за этим разговором: до правки строка звала чинить один разбор
	// снимка, а про признак молчала вовсе.
	for _, want := range []string{"дорога признака ожидания", "не за этим разговором", "XR-9",
		"9999aaaa", "дорога снимка панели"} {
		if !strings.Contains(line, want) {
			t.Errorf("строка журнала не назвала %q: %s", want, line)
		}
	}
	if strings.Contains(line, "вопросов за разговором нет") {
		t.Errorf("лежащий признак назван отсутствующим: %s", line)
	}
}

// Признак лежит с вышедшим сроком: ждущего за ним нет, и строка говорит именно
// это, а не «вопросов нет вовсе». Разница видна по журналу без второго захода.
func TestAskQuietNamesStaleSign(t *testing.T) {
	now := time.Date(2026, 9, 1, 14, 0, 0, 0, time.Local)
	sid := "eeee5555-5555-4555-8555-555555555555"
	e, c, lc := askRoadEnv(t, sid, "XR-1", now)
	writeAskPack(t, e.proj, "XR-9", sid, now.Add(-time.Minute), chat.Question{Text: "куда катить"})
	askRoadPane(t, e, " Работаю дальше, вопросов нет.\n\n\u276f \n")

	body(t, doReq(t, c, "GET", e.srv.URL+"/api/projects/demo/chats/"+sid+"/ask", ""))
	line := askRoadLine(t, lc)
	for _, want := range []string{"лежит просроченный", "XR-9", "13:59:00"} {
		if !strings.Contains(line, want) {
			t.Errorf("строка журнала не назвала %q: %s", want, line)
		}
	}
}

// Вопрос со снимка разобрался, но отбит рубежом эха: это вывод агента, а не
// виджет. Дорога снимка отработала, чинить в ней нечего, и строка журнала
// больше не зовёт её чинить.
func TestAskQuietNamesEchoRoad(t *testing.T) {
	now := time.Date(2026, 9, 3, 19, 21, 0, 0, time.Local)
	sid := "eeee5555-5555-4555-8555-555555555555"
	e, c, lc := askRoadEnv(t, sid, "XR-1", now)
	// Агент напечатал в своё окно снимок соседней панели, и разбор поднял его
	// как виджет. Те же слова стоят в ленте разговора ходом агента (живой
	// случай 03.09, chat-33: capture-pane соседнего чата).
	said := `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text",` +
		`"text":"1. Askpass через дашборд\n2. Правка правил подъёма"}]},` +
		`"timestamp":"2026-09-03T19:20:00.000Z"}` + "\n"
	writeSession(t, e.home, e.proj, "", sid, plainTalk+said, now.Add(-30*time.Second))
	askRoadPane(t, e, " Как чинить sudo из чата дашборда?\n\n \u276f 1. Askpass через дашборд\n"+
		"   2. Правка правил подъёма\n\n Enter to confirm \u00b7 Esc to cancel\n")

	text := body(t, doReq(t, c, "GET", e.srv.URL+"/api/projects/demo/chats/"+sid+"/ask", ""))
	if strings.Contains(text, `"ask"`) {
		t.Fatalf("эхо вывода агента приехало виджетом: %s", text)
	}
	line := askRoadLine(t, lc)
	if !strings.Contains(line, "повторяет ленту разговора") {
		t.Errorf("строка журнала не назвала рубеж эха: %s", line)
	}
	if strings.Contains(line, "разбор надо чинить") {
		t.Errorf("сработавший рубеж эха выдан за поломку разбора: %s", line)
	}
}

// Живой случай 01.09 по chat-13 и chat-14: человек ответил виджету кнопкой
// панели, виджет с панели ушёл, а признак стояния на вопросе в журнале
// уведомителя держится до конца хода. Жалоба уезжала в журнал секундой позже
// каждого удачного ответа и звала чинить работающий разбор.
func TestAskQuietAfterPanelAnswer(t *testing.T) {
	now := time.Date(2026, 9, 1, 13, 54, 40, 0, time.Local)
	sid := "eeee5555-5555-4555-8555-555555555555"
	e, c, lc := askRoadEnv(t, sid, "XR-1", now)
	askRoadPane(t, e, " Чинить сейчас?\n\n \u276f 1. Починить сейчас\n   2. Отложить\n\n"+
		" Enter to confirm \u00b7 Esc to cancel\n")

	at := e.srv.URL + "/api/projects/demo/chats/" + sid + "/ask"
	resp := doReq(t, c, "POST", at, `{"option": 1}`)
	if text := body(t, resp); resp.StatusCode != http.StatusOK {
		t.Fatalf("ответ на вопрос: %d %s", resp.StatusCode, text)
	}
	// Виджет отвечен и с панели ушёл, а панель переспрашивает через секунду.
	e.s.now = func() time.Time { return now.Add(time.Second) }
	askRoadPane(t, e, " Работаю дальше.\n\n\u276f \n")
	body(t, doReq(t, c, "GET", at, ""))

	line := askRoadLine(t, lc)
	if !strings.Contains(line, "панель ответила виджету") {
		t.Errorf("строка журнала не назвала свежий ответ панели: %s", line)
	}
	if strings.Contains(line, "разбор надо чинить") {
		t.Errorf("жалоба на разбор ушла следом за удачным ответом: %s", line)
	}
}

// Признак ожидания без сессии: ход спросил человека, а назваться сессией ему
// было нечем (ни ключа, ни окружения, ни записи реестра). Такой вопрос ждёт
// безадресную реплику, и место ему в разговоре своей задачи. Прежде скан
// пропускал его первой же проверкой, и вопрос не доезжал до панели ни текстом,
// ни вариантами.
func TestChatAskSignWithoutSessionReachesTaskChat(t *testing.T) {
	now := time.Date(2026, 9, 1, 14, 0, 0, 0, time.Local)
	sid := "eeee5555-5555-4555-8555-555555555555"
	e, c, _ := askRoadEnv(t, sid, "XR-4", now)
	askRoadPane(t, e, " Работаю дальше, вопросов нет.\n\n\u276f \n")
	err := chat.WriteAsk(e.proj, chat.TaskName("XR-4"), chat.Ask{
		Until: now.Add(5 * time.Minute), Task: "XR-4",
		Questions: []chat.Question{{Text: "куда катить", Options: []chat.Option{
			{Label: "в прод", Note: "сразу", Recommended: true}, {Label: "в стенд"}}}},
	})
	if err != nil {
		t.Fatal(err)
	}

	ask := askOf(t, e, c, sid)
	if ask.Kind != askKindAgent || ask.Task != "XR-4" {
		t.Fatalf("вопрос безадресного признака не собрался виджетом: %+v", ask)
	}
	if ask.Text != "куда катить" {
		t.Errorf("текст вопроса не доехал: %q", ask.Text)
	}
	if len(ask.Options) != 3 || ask.Options[0].Text != "в прод" || ask.Options[1].Text != "в стенд" {
		t.Fatalf("варианты не доехали: %+v", ask.Options)
	}

	// Ответ человека ложится во вход разговора безадресной строкой: её и ждёт
	// признак без сессии.
	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/say",
		sayBody("в прод", "m-9"))
	said := body(t, resp)
	if resp.StatusCode != http.StatusOK || !strings.Contains(said, `"way":"ask"`) {
		t.Fatalf("ответ не поехал дорогой ожидания: %d %s", resp.StatusCode, said)
	}
	lines := chatLines(t, e.proj, chat.TaskName("XR-4"))
	if len(lines) != 1 || chat.Said(lines[0]) != "в прод" {
		t.Fatalf("ответ не лёг во вход разговора задачи: %q", lines)
	}
	if a := chat.Addressee(lines[0]); a != "" {
		t.Errorf("ответ безадресному признаку лёг с адресатом %q: ждущий его не заберёт", a)
	}
}

// Разговор без своей задачи безадресный признак себе не берёт: иначе всякий
// открытый чат показывал бы чужой вопрос и отвечал бы за чужую задачу.
func TestChatAskSignWithoutSessionStaysInItsTask(t *testing.T) {
	now := time.Date(2026, 9, 1, 14, 0, 0, 0, time.Local)
	sid := "eeee5555-5555-4555-8555-555555555555"
	e, c, _ := askRoadEnv(t, sid, "-", now)
	askRoadPane(t, e, " Работаю дальше, вопросов нет.\n\n\u276f \n")
	err := chat.WriteAsk(e.proj, chat.TaskName("XR-4"), chat.Ask{
		Until: now.Add(5 * time.Minute), Task: "XR-4",
		Questions: []chat.Question{{Text: "куда катить"}},
	})
	if err != nil {
		t.Fatal(err)
	}

	text := body(t, doReq(t, c, "GET", e.srv.URL+"/api/projects/demo/chats/"+sid+"/ask", ""))
	if strings.Contains(text, `"ask"`) {
		t.Fatalf("чужой безадресный вопрос приехал в разговор без задачи: %s", text)
	}
}
