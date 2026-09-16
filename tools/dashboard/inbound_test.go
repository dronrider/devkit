package main

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Приём межсессионных реплик на машине (DK-630). Реплика дашборда едет живой
// сессии сокетом, и барьер класса разрешений на входе снимает настройка
// харнеса crossSessionInbound: accept. Без неё получатель придерживает кадр до
// ответа человека в своём окне, а сокет кадр берёт и соединение закрывает: до
// этой сверки такая доставка выглядела удачной обеим сторонам.

// writeHarnessSettings кладёт настройки харнеса в подставной дом стенда.
func writeHarnessSettings(t *testing.T, home, body string) {
	t.Helper()
	dir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Ручка доставки: реплика ушла сокетом, но приём на машине не разложен, и
// отправка кончается видимой причиной, а не молчаливым успехом. С ключом на
// месте плашки нет.
func TestChatSayNamesClosedInbound(t *testing.T) {
	for _, c := range []struct {
		name  string
		body  string
		stuck bool
	}{
		{name: "без ключа", body: `{"model": "opus"}`, stuck: true},
		{name: "с ключом", body: `{"crossSessionInbound": "accept"}`},
	} {
		t.Run(c.name, func(t *testing.T) {
			e, cl := chatEnv(t)
			sid := "cccc6666-6666-4666-8666-666666666666"
			writeSession(t, e.home, e.proj, "", sid, plainTalk, time.Now().Add(-10*time.Minute))
			writePeerSock(t, e.home, sid, os.Getpid(), liveSock(t))
			writeHarnessSettings(t, e.home, c.body)
			// tmux-сессии у разговора нет: дорога остаётся одна, сокет.
			writeScript(t, e.bin, "tmux", `case "$1" in ls) echo "";; esac
exit 0`)
			resp := doReq(t, cl, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/say",
				`{"text": "ау"}`)
			text := body(t, resp)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("реплика сокетом не прошла: %d %s", resp.StatusCode, text)
			}
			if !strings.Contains(text, `"way":"socket"`) && !strings.Contains(text, `"way": "socket"`) {
				t.Fatalf("реплика пошла не сокетом: %s", text)
			}
			held := strings.Contains(text, "приём межсессионных реплик")
			if c.stuck && !held {
				t.Fatalf("закрытый приём не назван в ответе ручки: %s", text)
			}
			if !c.stuck && held {
				t.Fatalf("плашка встала при разложенном ключе: %s", text)
			}
		})
	}
}
