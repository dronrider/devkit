package main

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Стенд выхода клиента посреди подачи реплики (DK-863). Текст едет в панель
// литералом, а перевод строки отдельной посылкой после паузы. Клиент,
// вышедший в этот промежуток, уносит реплику с собой. Обе посылки при этом
// удаются, и прежде панель получала успех дороги send-keys. Хода не было, и
// ответа человек не дождался. Правка, от которой стенд краснеет, это сверка
// живости сессии после подачи и дорога резюма вместо мнимого успеха.
func TestChatSayClientGoneMidSendRidesResume(t *testing.T) {
	e, c := chatEnv(t)
	sid := "bbbb8631-2222-4222-8222-222222222222"
	name := "chat-XR-4-1"
	writeSession(t, e.home, e.proj, "", sid, plainTalk, time.Now().Add(-time.Minute))
	writeBinds(t, e.home, "2026-09-08T00:35:12 сессия "+sid+" задача XR-4 проект demo дерево "+e.proj+
		" транскрипт /tmp/t.jsonl источник заказ повод startup tmux "+name+"\n")
	tmuxLog := filepath.Join(e.home, "tmux.log")
	gone := filepath.Join(e.home, "gone")
	// Клиент выходит на первой же посылке: send-keys удаётся обе, как у живой
	// сессии, а списка машины имя после этого не знает.
	writeScript(t, e.bin, "tmux", `echo "$@" >> "`+tmuxLog+`"
case "$1" in
ls) if [ -f "`+gone+`" ]; then exit 1; fi; echo "`+name+`|1|123";;
send-keys) : > "`+gone+`";;
esac
exit 0`)
	writeScript(t, e.bin, "claude", "exit 0")

	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/say",
		sayBody("по рекомендации", "m-863"))
	said := body(t, resp)
	if resp.StatusCode != 200 {
		t.Fatalf("реплика в выходящую сессию: %d %s", resp.StatusCode, said)
	}
	if strings.Contains(said, "send-keys") {
		t.Fatalf("подача в вышедшую сессию сошла за доставку: %s", said)
	}
	if !strings.Contains(said, "resume") {
		t.Fatalf("реплика не поехала продолжением резюмом: %s", said)
	}
	log := readFile(t, tmuxLog)
	if !strings.Contains(log, "new-session") || !strings.Contains(log, "по рекомендации") {
		t.Fatalf("текста реплики нет во вводной продолжения: %q", log)
	}
	// Молчания тут быть не должно и в ленте: человек читает разговор, а не
	// журнал демона, и причину смены дороги узнаёт там же.
	marks := saidMarks(t, e.home, "sess-"+sid)
	if len(marks) == 0 || !strings.Contains(marks[len(marks)-1], name) {
		t.Fatalf("в ленте нет слов о сессии, вышедшей посреди подачи: %v", marks)
	}
}

// Живая сессия дорогу не меняет. Подача удалась, имя на машине на месте, и
// реплика едет клавишами, как ехала. Стенд держит границу правки. Сверка
// живости не должна уводить резюмом всякую вторую реплику подряд.
func TestChatSayLiveSessionStaysOnKeys(t *testing.T) {
	e, c := chatEnv(t)
	sid := "dddd8632-4444-4444-8444-444444444444"
	name := "chat-XR-4-3"
	writeSession(t, e.home, e.proj, "", sid, plainTalk, time.Now().Add(-time.Minute))
	writeBinds(t, e.home, "2026-09-08T00:40:12 сессия "+sid+" задача XR-4 проект demo дерево "+e.proj+
		" транскрипт /tmp/t.jsonl источник заказ повод startup tmux "+name+"\n")
	tmuxLog := filepath.Join(e.home, "tmux.log")
	writeScript(t, e.bin, "tmux", `echo "$@" >> "`+tmuxLog+`"
case "$1" in ls) echo "`+name+`|1|123";; esac
exit 0`)
	writeScript(t, e.bin, "claude", "exit 0")

	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/say",
		sayBody("что там с задачей", "m-864"))
	said := body(t, resp)
	if resp.StatusCode != 200 || !strings.Contains(said, "send-keys") {
		t.Fatalf("реплика в живую сессию поехала не клавишами: %d %s", resp.StatusCode, said)
	}
	if log := readFile(t, tmuxLog); strings.Contains(log, "new-session") {
		t.Fatalf("живой сессии подняли продолжение резюмом: %q", log)
	}
}
