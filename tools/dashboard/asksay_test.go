package main

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dronrider/devkit/internal/chat"
)

// Ответ человека сессии, которая стоит на вопросе инструмента ожидания. Ждёт
// его процесс taskctl ask внутри хода Bash, а сокет клиента слышит только сам
// клиент: реплика, ушедшая сокетом, легла бы в очередь следующего хода, и
// ожидание добрало бы свой срок с готовым ответом на руках. Поэтому при живом
// признаке ожидания ручка чата кладёт реплику во вход разговора (LLD DK-430,
// решение 2: вход принадлежит разговору, реплика сессии идёт с адресатом).

// writeAskSign кладёт признак ожидания разговора так же, как его пишет
// taskctl ask: срок первой строкой, ниже ждущая сессия, задача и вопросы.
func writeAskSign(t *testing.T, tree, name, sid, task string, until time.Time) {
	t.Helper()
	err := chat.WriteAsk(tree, name, chat.Ask{Until: until, Session: sid, Task: task,
		Questions: []chat.Question{{Text: "режем строку или поднимаем цену"}}})
	if err != nil {
		t.Fatal(err)
	}
}

// askSayEnv поднимает сессию, ведущую задачу XR-4, с живым сокетом клиента.
func askSayEnv(t *testing.T, sid string) (*testEnv, *http.Client, func() []string) {
	t.Helper()
	e, c := chatEnv(t)
	writeSession(t, e.home, e.proj, "", sid, plainTalk, time.Now())
	writeBinds(t, e.home, fmt.Sprintf("2026-08-28T12:00:00 сессия %s задача XR-4 проект demo "+
		"дерево %s транскрипт /tmp/t.jsonl источник заказ повод startup tmux task-XR-4\n", sid, e.proj))
	sock, frames := countingSock(t)
	writePeerSock(t, e.home, sid, os.Getpid(), sock)
	writeScript(t, e.bin, "tmux", `case "$1" in ls) printf 'task-XR-4\t1\t1754770421\n';; esac
exit 0`)
	return e, c, frames
}

// chatLines читает вход разговора дерева: доставленная строка из него уходит,
// поэтому лежащая строка тут и есть неразобранная реплика.
func chatLines(t *testing.T, tree, name string) []string {
	t.Helper()
	return chat.ReadLines(filepath.Join(tree, ".devkit", "chat", name+".in"))
}

// Сессия стоит на вопросе: реплика идёт во вход разговора с адресатом, а сокет
// клиента её не получает вовсе.
func TestChatSayGoesToInputWhileAskWaits(t *testing.T) {
	sid := "dddd4444-4444-4444-8444-444444444444"
	e, c, frames := askSayEnv(t, sid)
	writeAskSign(t, e.proj, "task-XR-4", sid, "XR-4", time.Now().Add(5*time.Minute))

	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/say",
		sayBody("режем, две половины по шву", "m-1"))
	said := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("ответ ждущей сессии отбит: %d %s", resp.StatusCode, said)
	}
	if got := frames(); len(got) != 0 {
		t.Fatalf("реплика ушла в сокет клиента мимо ждущего инструмента: %q", got)
	}
	lines := chatLines(t, e.proj, "task-XR-4")
	if len(lines) != 1 {
		t.Fatalf("во входе разговора строк %d, ждали одну: %q", len(lines), lines)
	}
	if chat.Addressee(lines[0]) != sid {
		t.Fatalf("реплика легла без адресата, её заберёт чужой ход: %q", lines[0])
	}
	if chat.Said(lines[0]) != "режем, две половины по шву" {
		t.Fatalf("текст реплики потерялся: %q", lines[0])
	}
	if !strings.Contains(said, `"ask"`) {
		t.Fatalf("ручка не назвала дорогу ожиданием: %s", said)
	}
}

// Признак протух: ждущего за ним нет, и реплика идёт обычной дорогой, клавишами
// в живую tmux-сессию разговора (DK-480). Без срока всякий брошенный признак
// уводил бы разговор во вход, где реплику никто не читает до следующего хода.
func TestChatSayIgnoresStaleAsk(t *testing.T) {
	sid := "eeee5555-5555-4555-8555-555555555555"
	e, c, frames := askSayEnv(t, sid)
	writeAskSign(t, e.proj, "task-XR-4", sid, "XR-4", time.Now().Add(-time.Minute))

	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/say",
		sayBody("ответ на протухший вопрос", "m-2"))
	said := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("реплика отбита: %d %s", resp.StatusCode, said)
	}
	if !strings.Contains(said, `"send-keys"`) {
		t.Fatalf("реплика не поехала терминальной дорогой: %s", said)
	}
	if got := frames(); len(got) != 0 {
		t.Fatalf("реплика ушла в сокет мимо терминала: %q", got)
	}
	if lines := chatLines(t, e.proj, "task-XR-4"); len(lines) != 0 {
		t.Fatalf("реплика легла во вход, где её никто не ждёт: %q", lines)
	}
}

// Вопрос задала другая сессия того же разговора: реплика этой сессии едет
// своей дорогой, а не во вход. Иначе ответ одному собеседнику забрал бы ждущий
// сосед, и оба разговора получили бы не своё.
func TestChatSayIgnoresAskOfOtherSession(t *testing.T) {
	sid := "ffff6666-6666-4666-8666-666666666666"
	e, c, frames := askSayEnv(t, sid)
	writeAskSign(t, e.proj, "task-XR-4", "9999aaaa-7777-4777-8777-777777777777", "XR-4",
		time.Now().Add(5*time.Minute))

	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/say",
		sayBody("это ответ не тому, кто ждёт", "m-3"))
	said := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("реплика отбита: %d %s", resp.StatusCode, said)
	}
	if !strings.Contains(said, `"send-keys"`) {
		t.Fatalf("реплика не поехала терминальной дорогой: %s", said)
	}
	if got := frames(); len(got) != 0 {
		t.Fatalf("реплика ушла в сокет мимо терминала: %q", got)
	}
	if lines := chatLines(t, e.proj, "task-XR-4"); len(lines) != 0 {
		t.Fatalf("реплика легла во вход чужого ожидания: %q", lines)
	}
}
