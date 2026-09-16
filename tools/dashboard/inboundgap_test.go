package main

import (
	"strings"
	"testing"
)

// Разбор настройки приёма межсессионных реплик (DK-630) отдельным файлом от
// стенда ручки доставки: тест зовёт функцию, которой в базе ещё нет, и
// краснота на старом коде показывается ручкой, а не им.

func TestPeerInboundHeldReadsSettings(t *testing.T) {
	cases := []struct {
		name  string
		body  string
		write bool
		held  bool
	}{
		{name: "ключа нет вовсе", body: `{"model": "opus"}`, write: true, held: true},
		{name: "приём разрешён", body: `{"crossSessionInbound": "accept"}`, write: true},
		{name: "приём закрыт рукой", body: `{"crossSessionInbound": "hold"}`, write: true, held: true},
		// Нечитаемые и битые настройки это разговор доктора: плашка над
		// репликой про них молчит, судить ей не по чему.
		{name: "битый json", body: `{оборвано`, write: true},
		{name: "файла нет", write: false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			home := t.TempDir()
			if c.write {
				writeHarnessSettings(t, home, c.body)
			}
			why := peerInboundHeld(home)
			if c.held && why == "" {
				t.Fatal("закрытый приём реплик не назван")
			}
			if !c.held && why != "" {
				t.Fatalf("плашка встала на исправном: %s", why)
			}
			if c.held && !strings.Contains(why, "doctor --fix") {
				t.Errorf("причина не говорит, чем разложить ключ: %s", why)
			}
		})
	}
}
