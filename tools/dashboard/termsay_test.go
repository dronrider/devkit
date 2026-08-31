package main

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Терминальная дорога реплики (DK-480). Реплика из панели должна входить в
// живую сессию так же, как набранная в терминале: клавишами в её tmux-сессию,
// а не межсессионным сообщением от соседа. Сосед рамку не снимает, `!`-строки
// у него не исполняются, а подтверждение запертого разрешения от него харнес
// одобрением не считает.

// termSayEnv поднимает разговор с живой tmux-сессией chat-9: реестр называет
// её хозяином эту же сессию, tmux подменён скриптом, пишущим send-keys в файл.
// pane уходит в ответ capture-pane: пустая строка значит, что виджета на
// панели нет.
func termSayEnv(t *testing.T, sid, pane string) (*testEnv, *http.Client, string) {
	t.Helper()
	e, c := chatEnv(t)
	writeSession(t, e.home, e.proj, "", sid, plainTalk, time.Now())
	writeBinds(t, e.home, "2026-08-30T12:00:00 сессия "+sid+
		" задача - проект demo дерево "+e.proj+" транскрипт /tmp/t.jsonl "+
		"источник заказ повод startup tmux chat-9\n")
	sent := filepath.Join(e.home, "sent.log")
	writeScript(t, e.bin, "tmux", `case "$1" in
ls) printf 'chat-9\t1\t1754770421\n';;
capture-pane) printf '%s' `+shQuote(pane)+`;;
send-keys) shift; echo "$@" >> `+sent+`;;
esac
exit 0`)
	return e, c, sent
}

// trustPane это панель клиента с запертым вопросом доверия: так выглядит
// диалог разрешений, на который дашборд отвечает клавишами.
const trustPane = " Quick safety check: доверяешь каталогу?\n\n \u276f 1. Yes, I trust this folder\n" +
	"   2. No, exit\n\n Enter to confirm \u00b7 Esc to cancel\n"

// Живой сокет клиента больше не перехватывает реплику: у сессии с собственной
// tmux она едет клавишами и приходит агенту вводом человека, без рамки
// межсессионного канала. До правки дорога сокета стояла первой, и реплика
// приезжала «сообщением от другой сессии».
func TestChatSayTermRoadBeatsSocket(t *testing.T) {
	sid := "aaaa7777-1111-4111-8111-111111111111"
	e, c, sent := termSayEnv(t, sid, "")
	sock, frames := countingSock(t)
	writePeerSock(t, e.home, sid, os.Getpid(), sock)

	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/say",
		`{"text": "проверь доску"}`)
	said := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("реплика не прошла: %d %s", resp.StatusCode, said)
	}
	if !strings.Contains(said, `"send-keys"`) {
		t.Fatalf("дорога не терминальная: %s", said)
	}
	if keys := readFile(t, sent); !strings.Contains(keys, "проверь доску") {
		t.Fatalf("реплика не подана клавишами: %q", keys)
	}
	for _, f := range frames() {
		if strings.Contains(f, "проверь доску") {
			t.Fatalf("реплика уехала сокетом соседа: %q", f)
		}
	}
}

// Строка с `!` уходит клавишами и исполняется терминалом без витка модели, а
// ручка говорит об этом словами дороги (поле note, панель показывает его
// человеку).
func TestChatSayBangRunsInTerminal(t *testing.T) {
	sid := "bbbb7777-2222-4222-8222-222222222222"
	e, c, sent := termSayEnv(t, sid, "")

	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/say",
		`{"text": "! git status"}`)
	said := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("реплика не прошла: %d %s", resp.StatusCode, said)
	}
	if !strings.Contains(said, `"send-keys"`) || !strings.Contains(said, "без витка модели") {
		t.Fatalf("команда терминала не названа словами дороги: %s", said)
	}
	if keys := readFile(t, sent); !strings.Contains(keys, "! git status") {
		t.Fatalf("команда не подана клавишами: %q", keys)
	}
}

// Там, где терминального входа нет, строка с `!` не исполнится, и молчать об
// этом нельзя: сокетная дорога называет это в ответе ручки.
func TestChatSayBangWithoutTerminalIsNamed(t *testing.T) {
	sid := "cccc7777-3333-4333-8333-333333333333"
	e, c := chatEnv(t)
	writeSession(t, e.home, e.proj, "", sid, plainTalk, time.Now())
	sock, frames := countingSock(t)
	writePeerSock(t, e.home, sid, os.Getpid(), sock)
	// tmux-сессии у разговора нет: окно vscode слышно только сокетом.
	writeScript(t, e.bin, "tmux", `case "$1" in ls) exit 1;; esac
exit 0`)

	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/say",
		`{"text": "! ls"}`)
	said := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("реплика не прошла: %d %s", resp.StatusCode, said)
	}
	if !strings.Contains(said, `"socket"`) {
		t.Fatalf("дорога не сокетная: %s", said)
	}
	if !strings.Contains(said, "терминал её не исполнит") {
		t.Fatalf("бессилие `!` без терминала не названо: %s", said)
	}
	if got := frames(); len(got) != 1 {
		t.Fatalf("реплика не доехала сокетом: %q", got)
	}
}

// Подтверждение запертого разрешения из чата отпускает диалог: «да» и «нет»
// уезжают клавишами выбора пункта, а не текстом в модальный виджет.
func TestChatSayFreesLockedPermission(t *testing.T) {
	for _, tc := range []struct {
		said string
		key  string
	}{
		{"да", " 1"},
		{"нет", " 2"},
		{"2", " 2"},
		{"yes, i trust", " 1"},
	} {
		t.Run(tc.said, func(t *testing.T) {
			sid := "dddd7777-4444-4444-8444-444444444444"
			e, c, sent := termSayEnv(t, sid, trustPane)
			writeNotifyLog(t, e.home, []string{permissionNotify(sid)})

			resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/say",
				`{"text": "`+tc.said+`"}`)
			said := body(t, resp)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("ответ на запертый вопрос не прошёл: %d %s", resp.StatusCode, said)
			}
			if !strings.Contains(said, `"answer"`) {
				t.Fatalf("дорога не ответ на вопрос: %s", said)
			}
			keys := readFile(t, sent)
			if !strings.Contains(keys, tc.key) || !strings.Contains(keys, "Enter") {
				t.Fatalf("выбор пункта не подан клавишами: %q", keys)
			}
			// Литеральный ввод (-l) это печать текста в виджет: ответ обязан
			// ехать нажатиями выбора, а не словами.
			if strings.Contains(keys, "-l") {
				t.Fatalf("реплика напечаталась в модальный виджет текстом: %q", keys)
			}
		})
	}
}

// Свободные слова на запертом вопросе в виджет не печатаются: латинская буква
// в них сработала бы горячей клавишей, и реплика нажала бы кнопку за человека.
// С сокетом реплика едет в очередь клиента с пометкой, без сокета остаётся у
// панели с причиной, а не теряется молча.
func TestChatSayFreeWordsSkipLockedDialog(t *testing.T) {
	t.Run("с сокетом", func(t *testing.T) {
		sid := "eeee7777-5555-4555-8555-555555555555"
		e, c, sent := termSayEnv(t, sid, trustPane)
		writeNotifyLog(t, e.home, []string{permissionNotify(sid)})
		sock, frames := countingSock(t)
		writePeerSock(t, e.home, sid, os.Getpid(), sock)

		resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/say",
			`{"text": "why did you stop"}`)
		said := body(t, resp)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("реплика не прошла: %d %s", resp.StatusCode, said)
		}
		if !strings.Contains(said, `"socket"`) || !strings.Contains(said, `"stuck"`) {
			t.Fatalf("очередь за диалогом не названа: %s", said)
		}
		if data, _ := os.ReadFile(sent); strings.Contains(string(data), "why") {
			t.Fatalf("свободные слова напечатались в модальный виджет: %s", data)
		}
		found := false
		for _, f := range frames() {
			if strings.Contains(f, "why did you stop") {
				found = true
			}
		}
		if !found {
			t.Fatalf("реплика не доехала сокетом в очередь клиента: %q", frames())
		}
	})
	t.Run("без сокета", func(t *testing.T) {
		sid := "ffff7777-6666-4666-8666-666666666666"
		e, c, sent := termSayEnv(t, sid, trustPane)
		writeNotifyLog(t, e.home, []string{permissionNotify(sid)})

		resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/say",
			`{"text": "why did you stop"}`)
		said := body(t, resp)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("отказ должен быть удачей с причиной, панель повторит: %d %s", resp.StatusCode, said)
		}
		if !strings.Contains(said, `"held"`) || !strings.Contains(said, "ждёт разрешения") {
			t.Fatalf("недоставленная реплика не названа причиной: %s", said)
		}
		if data, _ := os.ReadFile(sent); strings.Contains(string(data), "why") {
			t.Fatalf("свободные слова напечатались в модальный виджет: %s", data)
		}
	})
}

// Замороженный терминал глотает send-keys без эха: при молчащем событийном
// цикле (зонд сокета не дождался закрытия) клавиши не подаются вовсе, а ручка
// отвечает отказом с именем клина.
func TestChatSayFrozenTerminalRefusesKeys(t *testing.T) {
	sid := "abab7777-7777-4777-8777-777777777777"
	e, c, sent := termSayEnv(t, sid, "")
	// Транскрипт стоит дольше срока клина: свежий снял бы вопрос без зонда.
	writeSession(t, e.home, e.proj, "", sid, plainTalk, time.Now().Add(-10*time.Minute))
	writePeerSock(t, e.home, sid, os.Getpid(), deafSock(t))
	// Общий стенд глушит зонд удачей, здесь он обязан молчать, как настоящий.
	e.s.probe = peerProbe

	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/say",
		`{"text": "ау"}`)
	said := body(t, resp)
	if resp.StatusCode == http.StatusOK {
		t.Fatalf("реплика в замороженный терминал сошла за доставленную: %s", said)
	}
	if !strings.Contains(said, stuckDeafWord) {
		t.Fatalf("клин не назван словами: %s", said)
	}
	if data, _ := os.ReadFile(sent); strings.Contains(string(data), "ау") {
		t.Fatalf("клавиши ушли в замороженный терминал: %s", data)
	}
}

// Дорога в терминал есть только у человека с дашборда: запрос без входа и
// запрос с чужим Origin отбиваются до того, как хоть одна клавиша уедет в
// tmux. Агент с машины куки входа не имеет, и писать в канал ему нечем.
func TestChatSayAgentWriteRejected(t *testing.T) {
	sid := "baba7777-8888-4888-8888-888888888888"
	e, c, sent := termSayEnv(t, sid, "")
	at := e.srv.URL + "/api/projects/demo/chats/" + sid + "/say"

	// Без куки входа: так выглядела бы запись агента или скрипта с машины.
	raw := &http.Client{}
	resp := doReq(t, raw, "POST", at, `{"text": "! rm -rf /"}`)
	said := body(t, resp)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("запись без входа не отбита: %d %s", resp.StatusCode, said)
	}

	// С кукой, но с чужим Origin: браузерная страница другого сайта.
	req, err := http.NewRequest("POST", at, strings.NewReader(`{"text": "! whoami"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Origin", "http://evil.example")
	got, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	said = body(t, got)
	if got.StatusCode != http.StatusForbidden {
		t.Fatalf("запись с чужим Origin не отбита: %d %s", got.StatusCode, said)
	}
	if data, _ := os.ReadFile(sent); strings.Contains(string(data), "rm -rf") ||
		strings.Contains(string(data), "whoami") {
		t.Fatalf("отбитая запись доехала до терминала: %s", data)
	}
}

// askOptionOf превращает свободную реплику в выбор пункта строго: номер, слова
// согласия и отказа, однозначное начало варианта. Всё прочее это отказ, а не
// догадка за человека.
func TestAskOptionOf(t *testing.T) {
	ask := tmuxAsk{Options: []tmuxPick{
		{Text: "Yes, I trust this folder"},
		{Text: "No, exit"},
		{Text: "Type something", Kind: pickFree},
	}}
	for _, tc := range []struct {
		said string
		want int
	}{
		{"да", 1}, {"Да.", 1}, {"yes", 1}, {"разрешаю", 1},
		{"нет", 2}, {"no", 2}, {"отмена", 2},
		{"1", 1}, {"2", 2},
		{"yes, i trust", 1},
		{"почему встал", 0},
		{"9", 0},
		{"3", 0},
		{"type something", 0},
		{"", 0},
	} {
		if got := askOptionOf(ask, tc.said); got != tc.want {
			t.Errorf("askOptionOf(%q) = %d, жду %d", tc.said, got, tc.want)
		}
	}
}

// Лента показывает терминальную команду и её вывод служебными строками, а не
// сырыми тегами в пузыре человека: команда с `!` видна вместе с результатом.
func TestSplitServiceShowsBashRun(t *testing.T) {
	said, notes := splitService("<bash-input>git status</bash-input>")
	if said != "" || len(notes) != 1 || notes[0].head != "! git status" {
		t.Fatalf("команда терминала не разобралась: said=%q notes=%+v", said, notes)
	}
	said, notes = splitService("<bash-stdout>On branch main</bash-stdout><bash-stderr></bash-stderr>")
	if said != "" || len(notes) != 1 {
		t.Fatalf("вывод терминала не разобрался: said=%q notes=%+v", said, notes)
	}
	if notes[0].head != "вывод терминала" || notes[0].body != "On branch main" {
		t.Fatalf("вывод показан не так: %+v", notes[0])
	}
}

// Панель показывает слова дороги: ответ ручки с полем note встаёт тостом, и
// человек видит, что строка с `!` исполнилась терминалом или уехала мимо него.
func TestStaticPanelShowsSayNote(t *testing.T) {
	app := readFile(t, filepath.Join("static", "app.js"))
	if !strings.Contains(app, "if (r.body.note) sayResult(r.body.note);") {
		t.Fatal("в static/app.js нет показа слов дороги (r.body.note)")
	}
}
