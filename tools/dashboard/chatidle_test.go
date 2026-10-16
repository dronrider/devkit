package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Срок жизни живого чата (DK-828). Три случая из DoD: разговор, простоявший
// дольше порога, снят дашбордом сам и об этом сказано в ленте и журнале;
// окно с идущим ходом живо; окно, занятое чужим разговором, живо.

// idleEnv поднимает разговор sid с окном chat-5 (фикстура tmux) и репликой
// пятичасовой давности: разговор жив, а хода в нём нет давным-давно.
func idleEnv(t *testing.T, sid string) *testEnv {
	t.Helper()
	e, _ := chatEnv(t)
	writeSession(t, e.home, e.proj, "", sid, saidLine("давний разговор", time.Now().Add(-5*time.Hour)), time.Now())
	writeScript(t, e.bin, "tmux", `case "$1" in
ls) printf 'chat-5\t1\t1754770421\n';;
esac
exit 0`)
	writeBinds(t, e.home, "2026-08-20T12:00:00 сессия "+sid+
		" задача - проект demo дерево "+e.proj+" транскрипт "+standTranscript(e.home, "t")+" "+
		"источник заказ повод startup tmux chat-5\n")
	return e
}

// idleKill подменяет chatKill собирающим именем и возвращает споткнутый журнал.
func idleKill(t *testing.T) (*string, *string) {
	t.Helper()
	killed := ""
	was := chatKill
	chatKill = func(name string) error {
		killed = name
		return nil
	}
	t.Cleanup(func() { chatKill = was })
	log := ""
	return &killed, &log
}

// Разговор, простоявший дольше порога, снят сам, и снаружи это видно в двух
// местах: строка в ленте разговора и запись в журнале с окном и сроком простоя.
func TestChatIdleKillsAStaleChat(t *testing.T) {
	sid := "aaaa8281-1111-4111-8111-111111111111"
	e := idleEnv(t, sid)
	e.cfg.ChatIdle = time.Hour
	killed, log := idleKill(t)
	e.s.logf = func(format string, args ...any) { *log += fmt.Sprintf(format, args...) + "\n" }

	e.s.chatIdleTick()
	if *killed != "chat-5" {
		t.Fatalf("разговор, простоявший дольше порога, не снят: снято %q", *killed)
	}
	mark := readFile(t, filepath.Join(e.home, "said", "sess-"+sid+".jsonl"))
	if !strings.Contains(mark, "простоял") || !strings.Contains(mark, "chat-5") {
		t.Fatalf("в ленте разговора нет строки о снятии за простой: %s", mark)
	}
	if !strings.Contains(*log, "снят за простой") || !strings.Contains(*log, "окно chat-5") {
		t.Fatalf("журнал не назвал снятое окно и срок простоя: %s", *log)
	}
}

// Окно с идущим ходом живо, даже если последняя реплика старше порога: ход
// меряется занятостью сессии, а не свежестью реплики.
func TestChatIdleKeepsARunningTurnAlive(t *testing.T) {
	sid := "bbbb8282-2222-4222-8222-222222222222"
	e := idleEnv(t, sid)
	e.cfg.ChatIdle = time.Hour
	writePeerPID(t, e.home, os.Getpid(), sid, "chat-5:@5.%5", "busy", time.Now().UnixMilli())
	killed, _ := idleKill(t)

	e.s.chatIdleTick()
	if *killed != "" {
		t.Fatalf("окно с идущим ходом снято: %q", *killed)
	}
}

// Окно, занятое чужим живым разговором, живо: слово живого клиента в склейке
// списка старше записи реестра, она выставляет соседа из имени (DK-673), и
// обход застаёт разговор без окна, снятию нечего взять.
func TestChatIdleKeepsAHeldWindowAlive(t *testing.T) {
	sid := "cccc8283-3333-4333-8333-333333333333"
	alien := "dddd8284-4444-4444-8444-444444444444"
	e := idleEnv(t, sid)
	e.cfg.ChatIdle = time.Hour
	writePeerWindow(t, e.home, alien, "chat-5")
	killed, _ := idleKill(t)

	e.s.chatIdleTick()
	if *killed != "" {
		t.Fatalf("чужой живой разговор в окне снят: %q", *killed)
	}
}

// Окно, поднятое дашбордом заново, до записи хука старта ничьё (DK-851): за
// именем уже другой разговор, и старый жилец его не снимает. Устаревшее имя
// снимает сам список (DK-859), поэтому обход застаёт разговор без окна.
func TestChatIdleKeepsAReraisedWindowAlive(t *testing.T) {
	sid := "eeee8285-5555-4555-8555-555555555555"
	e := idleEnv(t, sid)
	e.cfg.ChatIdle = time.Hour
	e.s.chatRaised("chat-5", "", "", "demo")
	killed, _ := idleKill(t)

	e.s.chatIdleTick()
	if *killed != "" {
		t.Fatalf("переподнятое окно снято за прежним жильцом: %q", *killed)
	}
}

// Разговор, ждущий ответа человека, окно не теряет: вопрос доезжает только в
// поднятую сессию, и убивать её под живым вопросом значит терять ответ.
func TestChatIdleKeepsAChatWithALiveAsk(t *testing.T) {
	sid := "ffff8286-6666-4666-8666-666666666666"
	e := idleEnv(t, sid)
	e.cfg.ChatIdle = time.Hour
	writeAskPack(t, e.proj, "XR-4", sid, time.Now().Add(time.Hour))
	killed, _ := idleKill(t)

	e.s.chatIdleTick()
	if *killed != "" {
		t.Fatalf("разговор с живым вопросом снят: %q", *killed)
	}
}
