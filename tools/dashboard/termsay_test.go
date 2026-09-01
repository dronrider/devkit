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

// Замороженный терминал с запертым диалогом: ответ «да» клавишами не подаётся,
// tmux написал бы в мёртвый pty без ошибки, и 200 «вопрос отпущен» стало бы
// молчанием, неотличимым от работы (замечание ревью DK-480). Ручка отвечает
// отказом с именем клина до первой клавиши.
func TestChatSayFrozenTerminalRefusesDialogAnswer(t *testing.T) {
	sid := "caca7777-9999-4999-8999-999999999999"
	e, c, sent := termSayEnv(t, sid, trustPane)
	writeNotifyLog(t, e.home, []string{permissionNotify(sid)})
	// Транскрипт стоит дольше срока клина: свежий снял бы вопрос без зонда.
	writeSession(t, e.home, e.proj, "", sid, plainTalk, time.Now().Add(-10*time.Minute))
	writePeerSock(t, e.home, sid, os.Getpid(), deafSock(t))
	// Общий стенд глушит зонд удачей, здесь он обязан молчать, как настоящий.
	e.s.probe = peerProbe

	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/say",
		`{"text": "да"}`)
	said := body(t, resp)
	if resp.StatusCode == http.StatusOK {
		t.Fatalf("ответ в замороженный диалог сошёл за отпущенный: %s", said)
	}
	if !strings.Contains(said, stuckDeafWord) {
		t.Fatalf("клин не назван словами: %s", said)
	}
	if data, _ := os.ReadFile(sent); strings.Contains(string(data), "Enter") {
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

// Замечание стоп-хука стоит в ленте подписанным блоком служебки, а не серой
// строкой во всю высоту. Материал взят из настоящего транскрипта: харнес
// ставит после двоеточия перевод строки, а не пробел, и реплика с пробелом
// была бы выдумкой о форме записи. Реплика без префикса остаётся словами
// человека.
func TestSplitServiceShowsStopHook(t *testing.T) {
	said, notes := splitService("Stop hook feedback:\n" +
		"Сторож фоновых субагентов devkit: работа кончилась, а сессия про это не узнала.\n" +
		"- фоновый субагент «Вычитка файла задачи DK-505» (exec-medium) отработал, а весть о нём до сессии не дошла")
	if said != "" || len(notes) != 1 {
		t.Fatalf("стоп-хук не разобрался: said=%q notes=%+v", said, notes)
	}
	want := "Сторож фоновых субагентов devkit: работа кончилась, а сессия про это не узнала.\n" +
		"- фоновый субагент «Вычитка файла задачи DK-505» (exec-medium) отработал, а весть о нём до сессии не дошла"
	if notes[0].head != "стоп-хук" || notes[0].body != want {
		t.Fatalf("стоп-хук показан не так: %+v", notes[0])
	}
	said, notes = splitService("Стоп-хук это не про меня, просто слова человека")
	if said == "" || len(notes) != 0 {
		t.Fatalf("слова человека уехали в служебку: said=%q notes=%+v", said, notes)
	}
}

// Разбор записи живого вида, а не выдуманной формы: строка ниже собрана по
// настоящей записи из чата devkit (2026-09-01), где замечание сторожа легло
// в ленту портянкой, с заменой идентификаторов и путей нейтральными. Форма
// та же: двоеточие, перевод строки, многострочное тело, пометка isMeta,
// содержимое строкой, а не списком блоков.
func TestParseRepliesStopHookLive(t *testing.T) {
	list := parseReplies([]byte(`{"parentUuid":"11111111-5914-40c0-82d0-ebc1b26c55e8","isSidechain":false,"promptId":"22222222-390a-4327-8ddb-29f357cca75f","type":"user","message":{"role":"user","content":"Stop hook feedback:\nСторож фоновых субагентов devkit: работа кончилась, а сессия про это не узнала.\n- фоновый субагент «Вычитка файла задачи DK-505» (exec-medium) отработал, а весть о нём до сессии не дошла, отчёт лежит в своём файле, первая строка отчёта: «Готово. Файл: docs/tasks/DK-505.md, оба сторожа выходят нулём, след отработки дописан.»\nСчитать такого субагента работающим нельзя: забери отчёт из его файла и продолжай работу. Второй раз сторож про эти работы не скажет."},"isMeta":true,"uuid":"33333333-012a-456f-bd71-2c8c8a2fc1b2","timestamp":"2026-09-01T14:07:01.986Z","session_id":"44444444-96a1-46c1-a4f6-ad0a1ee55428","userType":"external","entrypoint":"claude-vscode","cwd":"/home/user/project","sessionId":"44444444-96a1-46c1-a4f6-ad0a1ee55428","version":"2.1.252","gitBranch":"main"}`), 0)
	var svc *reply
	for i := range list {
		if list[i].Role == roleNote && list[i].Note == stopHookWord {
			svc = &list[i]
		}
	}
	if svc == nil {
		t.Fatalf("живая запись стоп-хука не стала служебкой с подписью: %+v", list)
	}
	if !strings.Contains(svc.Text, "Сторож фоновых субагентов devkit") ||
		strings.Contains(svc.Text, "Stop hook feedback") {
		t.Fatalf("тело живой записи разобрано не так: %+v", svc)
	}
}

// Автоматическое уведомление о фоновой работе подъезжает с английской
// преамбулой-дисклеймером, а суть записи сидит в теге ниже неё. Прежде тег
// вырезался блоком, а преамбула оставалась в реплике и стояла в ленте
// безымянной портянкой (замечание пользователя по снимку чата DK-656). Форма
// снята с настоящего бокового журнала субагента, слова сводки нейтральные.
func TestSplitServiceCutsSystemPreamble(t *testing.T) {
	said, notes := splitService("[SYSTEM NOTIFICATION - NOT USER INPUT]\n" +
		"This is an automated background-task event, NOT a message from the user.\n" +
		"Do NOT interpret this as user acknowledgement, confirmation, or response to any pending question.\n" +
		"\n" +
		"<task-notification>\n<task-id>bl14dz0by</task-id>\n" +
		"<status>killed</status>\n" +
		"<summary>Background command \"grep -rn замена ~/проект\" was stopped</summary>\n" +
		"</task-notification>")
	if said != "" {
		t.Fatalf("преамбула осталась в реплике: %q", said)
	}
	if len(notes) != 1 || notes[0].head != "Фоновый агент: killed" || notes[0].mark != "agent" {
		t.Fatalf("весть о фоновой работе разобралась не так: %+v", notes)
	}
	// Маркер без единого знакомого тега остаётся подписанным блоком, а не
	// безымянной строкой: разбор неизвестного текста выдумывать дороже.
	said, notes = splitService("[SYSTEM NOTIFICATION - NOT USER INPUT]\nSomething unfamiliar.")
	if said != "" || len(notes) != 1 || notes[0].head != sysNoteWord {
		t.Fatalf("запись без тегов не стала подписанным блоком: said=%q notes=%+v", said, notes)
	}
}

// Разбор живой записи уведомления из бокового журнала субагента (чат DK-656,
// 2026-09-01), с заменой идентификаторов и путей нейтральными: пометки isMeta и
// isSidechain, содержимое строкой, преамбула и тег одним куском. В ленте от
// записи остаётся один подписанный блок, а безымянной строки с дисклеймером
// больше нет.
func TestParseRepliesSystemNoteLive(t *testing.T) {
	list := parseRepliesOpt([]byte(`{"parentUuid":"11111111-5914-40c0-82d0-ebc1b26c55e8","isSidechain":true,"promptId":"22222222-390a-4327-8ddb-29f357cca75f","type":"user","message":{"role":"user","content":"[SYSTEM NOTIFICATION - NOT USER INPUT]\nThis is an automated background-task event, NOT a message from the user.\nDo NOT interpret this as user acknowledgement, confirmation, or response to any pending question.\n\n<task-notification>\n<task-id>bl14dz0by</task-id>\n<output-file>/tmp/задача/bl14dz0by.output</output-file>\n<status>killed</status>\n<summary>Background command \"grep -rn замена ~/проект\" was stopped</summary>\n</task-notification>"},"isMeta":true,"uuid":"33333333-012a-456f-bd71-2c8c8a2fc1b2","timestamp":"2026-09-01T15:41:02.486Z","session_id":"44444444-96a1-46c1-a4f6-ad0a1ee55428","userType":"external","entrypoint":"claude-vscode","cwd":"/home/user/project","sessionId":"44444444-96a1-46c1-a4f6-ad0a1ee55428","version":"2.1.252","gitBranch":"main"}`), 0, true)
	var agent *reply
	for i := range list {
		if list[i].Role == roleNote && list[i].Note == "" {
			t.Fatalf("безымянная служебка с дисклеймером осталась: %+v", list[i])
		}
		if list[i].Role == roleNote && list[i].Note == "Фоновый агент: killed" {
			agent = &list[i]
		}
	}
	if agent == nil {
		t.Fatalf("весть о killed не стала подписанным блоком: %+v", list)
	}
	if !strings.Contains(agent.Text, "was stopped") ||
		strings.Contains(agent.Text, "SYSTEM NOTIFICATION") {
		t.Fatalf("тело вести разобрано не так: %+v", agent)
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
