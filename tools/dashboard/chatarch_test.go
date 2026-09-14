package main

import (
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// Архив это уборка разговора рукой: после разбора десятка черновиков десяток
// отработавших чатов стоит в списке и мозолит глаза, а окно по свежести их не
// прячет, они свежие (требование пользователя). Признак живёт памятью диалога
// на сервере, поэтому переживает перезапуск дашборда и виден с любой вкладки.

// chatOne достаёт строку списка по ID.
func chatOne(list []chatEntry, sid string) *chatEntry {
	for i := range list {
		if list[i].ID == sid {
			return &list[i]
		}
	}
	return nil
}

// Признак архива едет в строке списка, ложится на диск и снимается той же
// ручкой: дорога назад из архива обязана быть, иначе уборка это удаление.
func TestChatArchiveMarksAndReturns(t *testing.T) {
	e, c := chatEnv(t)
	sid := "aaaa1111-1111-4111-8111-111111111111"
	writeSession(t, e.home, e.proj, "", sid, saidLine("разобранный черновик", time.Now().Add(-time.Hour)), time.Now())

	list, _ := chatsWindow(t, e, c, "")
	if got := chatOne(list, sid); got == nil || got.Archived {
		t.Fatalf("свежий разговор пришёл уже убранным: %+v", got)
	}

	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/archive", `{"archived": true}`)
	text := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("уборка в архив: %d %s", resp.StatusCode, text)
	}
	if !strings.Contains(text, `"archived":true`) {
		t.Fatalf("ручка не подтвердила уборку: %s", text)
	}

	list, _ = chatsWindow(t, e, c, "")
	got := chatOne(list, sid)
	if got == nil {
		t.Fatalf("убранный разговор пропал из ответа ручки: список решает панель, а не сервер: %+v", list)
	}
	if !got.Archived {
		t.Fatalf("признак архива не доехал до строки списка: %+v", got)
	}

	resp = doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/archive", `{"archived": false}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("возврат из архива: %d %s", resp.StatusCode, body(t, resp))
	}
	list, _ = chatsWindow(t, e, c, "")
	if got := chatOne(list, sid); got == nil || got.Archived {
		t.Fatalf("разговор не вернулся из архива: %+v", got)
	}
}

// Живую сессию убранного разговора снимает сам сервер: убирают отработавший
// чат, и оставлять за ним живой клиент значит держать процесс, за которым
// больше не следят.
func TestChatArchiveDropsLiveSession(t *testing.T) {
	e, c := chatEnv(t)
	sid := "bbbb2222-2222-4222-8222-222222222222"
	writeSession(t, e.home, e.proj, "", sid, saidLine("отработавший разбор", time.Now().Add(-time.Hour)), time.Now())
	writeScript(t, e.bin, "tmux", `case "$1" in
ls) printf 'chat-1\t1\t1754770421\n';;
esac
exit 0`)
	writeBinds(t, e.home, "2026-08-20T12:00:00 сессия "+sid+
		" задача - проект demo дерево "+e.proj+" транскрипт "+standTranscript(e.home, "t")+" "+
		"источник заказ повод startup tmux chat-1\n")

	killed := ""
	was := chatKill
	chatKill = func(name string) error {
		killed = name
		return nil
	}
	defer func() { chatKill = was }()

	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/archive", `{"archived": true}`)
	text := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("уборка живого разговора: %d %s", resp.StatusCode, text)
	}
	if killed != "chat-1" {
		t.Fatalf("сессия убранного разговора не снята: снято %q", killed)
	}
	if !strings.Contains(text, "сессия снята") {
		t.Fatalf("про снятую сессию в ответе не сказано: %s", text)
	}

	// Возврат из архива сессий не трогает: поднимать снятое некому и незачем,
	// следующая реплика поднимет разговор резюмом.
	killed = ""
	doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/archive", `{"archived": false}`)
	if killed != "" {
		t.Fatalf("возврат из архива полез снимать сессию: %q", killed)
	}
}

// Уборка мимо живой сессии это не отказ: разговор кончился сам, снимать нечего,
// и признак всё равно ложится.
func TestChatArchiveWithoutSession(t *testing.T) {
	e, c := chatEnv(t)
	sid := "cccc3333-3333-4333-8333-333333333333"
	writeSession(t, e.home, e.proj, "", sid, saidLine("кончившийся разговор", time.Now().Add(-time.Hour)), time.Now())
	killed := ""
	was := chatKill
	chatKill = func(name string) error {
		killed = name
		return nil
	}
	defer func() { chatKill = was }()

	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/archive", `{"archived": true}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("уборка разговора без сессии: %d %s", resp.StatusCode, body(t, resp))
	}
	if killed != "" {
		t.Fatalf("уборка полезла снимать несуществующую сессию: %q", killed)
	}
	list, _ := chatsWindow(t, e, c, "")
	if got := chatOne(list, sid); got == nil || !got.Archived {
		t.Fatalf("признак архива не лёг разговору без сессии: %+v", got)
	}
}

// Панель знает про архив: три положения кнопки, отсев списка и ручка уборки
// живут в статике, и без них уборка снаружи не видна.
func TestStaticPanelKnowsArchive(t *testing.T) {
	app := readFile(t, "static/app.js")
	for _, want := range []string{"devkit.chat.arch", "chatArchShown", "/archive"} {
		if !strings.Contains(app, want) {
			t.Errorf("в static/app.js нет опоры архива %q", want)
		}
	}
	if !strings.Contains(readFile(t, "static/index.html"), `data-ico="i-box"`) {
		t.Error("значка архива нет в наборе: кнопке нечем рисоваться")
	}
}

// Запись реестра у живого чата бывает уже вырезана, и окно про него знает
// только живой клиент. Ручка брала имя одной записью реестра, отвечала «убран в
// архив» и оставляла процесс жить невидимым: 29 чатов за один заход, 27 окон
// снято руками (DK-825). Имя берётся той же свёрткой, что и в списке.
func TestChatArchiveDropsWindowKnownOnlyToPeer(t *testing.T) {
	e, c := chatEnv(t)
	sid := "dddd8250-2222-4222-8222-222222222222"
	writeSession(t, e.home, e.proj, "", sid, saidLine("живой разговор без записи", time.Now().Add(-time.Hour)), time.Now())
	writeScript(t, e.bin, "tmux", `case "$1" in
ls) printf 'chat-5\t1\t1754770421\n';;
esac
exit 0`)
	writePeerTmux(t, e.home, sid, "chat-5:@5.%5", "idle")

	killed := ""
	was := chatKill
	chatKill = func(name string) error {
		killed = name
		return nil
	}
	defer func() { chatKill = was }()

	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/archive", `{"archived": true}`)
	text := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("уборка чата без записи в реестре: %d %s", resp.StatusCode, text)
	}
	if killed != "chat-5" {
		t.Fatalf("окно, известное только клиенту, не снято: снято %q", killed)
	}
	if !strings.Contains(text, "сессия снята") || !strings.Contains(text, `"tmux":"chat-5"`) {
		t.Fatalf("ответ не назвал снятую сессию: %s", text)
	}
}

// Когда окна не нашлось ни в реестре, ни у живого клиента, ручка говорит это
// словами: «убран в архив» без оговорки читалось как снятие, которого не было.
func TestChatArchiveSaysWindowNotFound(t *testing.T) {
	e, c := chatEnv(t)
	sid := "dddd8251-2222-4222-8222-222222222222"
	writeSession(t, e.home, e.proj, "", sid, saidLine("разговор без окна", time.Now().Add(-time.Hour)), time.Now())
	was := chatKill
	chatKill = func(name string) error { return nil }
	defer func() { chatKill = was }()

	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/archive", `{"archived": true}`)
	text := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("уборка чата без окна: %d %s", resp.StatusCode, text)
	}
	if !strings.Contains(text, "окно не найдено") || !strings.Contains(text, "остался") {
		t.Fatalf("ответ не сказал, что окна нет и процесс остался: %s", text)
	}
}

// Имя убираемого чата бывает занято свежей записью реестра, за которой живого
// клиента уже нет. Это кончившийся разговор, а не сосед за работой, и окно
// снимается, как снималось до свёртки: держателем окна считается живой
// клиент, а не запись (замечание ревью DK-825).
func TestChatArchiveDropsWindowOfDeadClaimant(t *testing.T) {
	e, c := chatEnv(t)
	sid := "dddd8252-2222-4222-8222-222222222222"
	other := "dddd8253-2222-4222-8222-222222222222"
	writeSession(t, e.home, e.proj, "", sid, saidLine("старый жилец окна", time.Now().Add(-2*time.Hour)), time.Now())
	writeScript(t, e.bin, "tmux", `case "$1" in
ls) printf 'chat-6\t1\t1754770421\n';;
esac
exit 0`)
	line := func(when, id string) string {
		return when + " сессия " + id + " задача - проект demo дерево " + e.proj +
			" транскрипт " + standTranscript(e.home, "t") + " источник заказ повод startup tmux chat-6\n"
	}
	writeBinds(t, e.home, line("2026-08-20T12:00:00", sid), line("2026-08-20T13:00:00", other))

	killed := ""
	was := chatKill
	chatKill = func(name string) error {
		killed = name
		return nil
	}
	defer func() { chatKill = was }()

	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/archive", `{"archived": true}`)
	text := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("уборка чата с занятым именем: %d %s", resp.StatusCode, text)
	}
	if killed != "chat-6" {
		t.Fatalf("окно за мёртвой записью соседа не снято: снято %q, ответ %s", killed, text)
	}
	if !strings.Contains(text, "сессия снята") {
		t.Fatalf("ответ не назвал снятую сессию: %s", text)
	}
}

// Окно, поднятое дашбордом заново, до записи хука старта ничьё (DK-851): оно
// живое и ведёт новый разговор, и уборка прежнего жильца его не снимает, а
// ответ говорит про это словами.
func TestChatArchiveKeepsReraisedWindow(t *testing.T) {
	e, c := chatEnv(t)
	sid := "dddd8254-2222-4222-8222-222222222222"
	writeSession(t, e.home, e.proj, "", sid, saidLine("прежний жилец окна", time.Now().Add(-2*time.Hour)), time.Now())
	writeScript(t, e.bin, "tmux", `case "$1" in
ls) printf 'chat-7\t1\t1754770421\n';;
esac
exit 0`)
	writeBinds(t, e.home, "2026-08-20T12:00:00 сессия "+sid+" задача - проект demo дерево "+e.proj+
		" транскрипт "+standTranscript(e.home, "t")+" источник заказ повод startup tmux chat-7\n")
	e.s.chatRaised("chat-7", "", "", "demo")

	killed := ""
	was := chatKill
	chatKill = func(name string) error {
		killed = name
		return nil
	}
	defer func() { chatKill = was }()

	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/archive", `{"archived": true}`)
	text := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("уборка прежнего жильца окна: %d %s", resp.StatusCode, text)
	}
	if killed != "" {
		t.Fatalf("переподнятое окно снято за прежним жильцом: %q", killed)
	}
	if !strings.Contains(text, "поднято заново") {
		t.Fatalf("ответ не сказал, что окно поднято заново: %s", text)
	}
}

// Запись реестра бывает чужой, а своё окно живой клиент называет сам. Снимается
// своё окно, что бы ни говорила запись: разбор чужого имени идёт только у чата
// без своего окна, иначе ручка отвечала «сессия соседа осталась жить» и
// оставляла процесс убираемого чата (замечание ревью, второй круг DK-825).
func TestChatArchiveDropsOwnWindowDespiteForeignBind(t *testing.T) {
	e, c := chatEnv(t)
	sid := "dddd8255-2222-4222-8222-222222222222"
	other := "dddd8256-2222-4222-8222-222222222222"
	writeSession(t, e.home, e.proj, "", sid, saidLine("чат с чужой записью", time.Now().Add(-time.Hour)), time.Now())
	writeScript(t, e.bin, "tmux", `case "$1" in
ls) printf 'chat-8\t1\t1754770421\nchat-9\t1\t1754770422\n';;
esac
exit 0`)
	line := func(when, id string) string {
		return when + " сессия " + id + " задача - проект demo дерево " + e.proj +
			" транскрипт " + standTranscript(e.home, "t") + " источник заказ повод startup tmux chat-8\n"
	}
	writeBinds(t, e.home, line("2026-08-20T12:00:00", sid), line("2026-08-20T13:00:00", other))
	writePeerPID(t, e.home, os.Getppid(), other, "chat-8:@8.%8", "idle", time.Now().Unix())
	writePeerPID(t, e.home, os.Getpid(), sid, "chat-9:@9.%9", "idle", time.Now().Unix())

	killed := ""
	was := chatKill
	chatKill = func(name string) error {
		killed = name
		return nil
	}
	defer func() { chatKill = was }()

	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/archive", `{"archived": true}`)
	text := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("уборка чата с чужой записью: %d %s", resp.StatusCode, text)
	}
	if killed != "chat-9" {
		t.Fatalf("своё окно не снято: снято %q, ответ %s", killed, text)
	}
	if !strings.Contains(text, `"tmux":"chat-9"`) || !strings.Contains(text, "сессия снята") {
		t.Fatalf("ответ не назвал своё окно: %s", text)
	}
}
