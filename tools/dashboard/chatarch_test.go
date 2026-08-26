package main

import (
	"net/http"
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
		" задача - проект demo дерево "+e.proj+" транскрипт /tmp/t.jsonl "+
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
