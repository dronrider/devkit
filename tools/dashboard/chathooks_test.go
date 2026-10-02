package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Подъём без хуков и мёртвая заготовка (DK-1286). Сессия подписки, в чьих
// настройках нет обвязки devkit, не поднимается молча, а заготовка чата, чья
// tmux-сессия умерла без записи в журнале, не живёт вечно.

const hooksGapText = "обвязки нет: devkitctl doctor --fix (харнес втораяtest, в /x/settings.json нет хуков devkit)"

// hooksGapLayout это раскладка из двух подписок: у домашней хуки на месте, у
// второй agentctl называет находку.
func hooksGapLayout(second string) string {
	gap := ""
	if second != "" {
		b, _ := json.Marshal(second)
		gap = `, "hooks_gap": ` + string(b)
	}
	return `{
  "default": "перваяtest",
  "source": "фикстура",
  "harnesses": [
    {"name": "перваяtest", "enabled": true, "default": true, "bin": "клиент-1",
     "models": [{"tier": "pro", "model": "модель-pro"}]},
    {"name": "втораяtest", "enabled": true, "default": false, "bin": "клиент-2", "env": ["CONFIG_DIR"]` + gap + `,
     "models": [{"tier": "pro", "model": "вторая-pro"}]}
  ]
}`
}

func hooksGapEnv(t *testing.T, second string) (*testEnv, *http.Client, string) {
	t.Helper()
	e, c := chatEnv(t)
	tmuxLog := filepath.Join(e.home, "tmux.log")
	writeScript(t, e.bin, "tmux", `echo "$@" >> "`+tmuxLog+`"
case "$1" in
ls) `+tmuxNoServer+`;;
esac
exit 0`)
	writeScript(t, e.bin, "claude", "exit 0")
	writeAgentctlFake(t, e.bin, hooksGapLayout(second))
	return e, c, tmuxLog
}

// TestChatStartRefusesWithoutHooks: подписка без хуков получает отказ с
// находкой до tmux, а подписка с хуками поднимается.
func TestChatStartRefusesWithoutHooks(t *testing.T) {
	e, c, tmuxLog := hooksGapEnv(t, hooksGapText)

	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats",
		`{"text": "привет", "model": "вторая-pro"}`)
	text := body(t, resp)
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("подъём на подписке без хуков: %d %s, жду 502", resp.StatusCode, text)
	}
	if !strings.Contains(text, "обвязки нет: devkitctl doctor --fix") {
		t.Fatalf("отказ не назвал находку: %s", text)
	}
	if log, err := os.ReadFile(tmuxLog); err == nil && strings.Contains(string(log), "new-session") {
		t.Fatalf("tmux-сессия поднялась, хотя обвязки нет: %s", log)
	}

	resp = doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats",
		`{"text": "привет", "model": "модель-pro"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("подписка с хуками не поднялась: %d %s", resp.StatusCode, body(t, resp))
	}
}

// TestChatStartPassesWhenHooksPresent: находки нет, и вторая подписка
// поднимается как раньше.
func TestChatStartPassesWhenHooksPresent(t *testing.T) {
	e, c, _ := hooksGapEnv(t, "")
	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats",
		`{"text": "привет", "model": "вторая-pro"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("подъём при целой обвязке: %d %s", resp.StatusCode, body(t, resp))
	}
}

// TestBlankSweepByDeadTmux: заготовка с поднятой сессией живёт, пока жив её
// tmux, уходит, когда tmux умер без привязки в журнале, и остаётся, когда опрос
// tmux сорвался (это не «никого нет»).
func TestBlankSweepByDeadTmux(t *testing.T) {
	put := func(t *testing.T, e *testEnv, id string, st chatStore) {
		t.Helper()
		data, err := json.Marshal(st)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(chatStoreDir(e.home), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(blankPath(e.home, id), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now().Unix()
	lifted := chatStore{Blank: true, Born: now, Lifted: now, Project: "demo", Tmux: "chat-7"}

	t.Run("tmux умер", func(t *testing.T) {
		e, c := chatEnv(t)
		writeScript(t, e.bin, "tmux", `case "$1" in ls) `+tmuxNoServer+`;; esac
exit 0`)
		put(t, e, "blank-dead", lifted)
		if blankRow(blankList(t, e, c, "?all=1"), "blank-dead") != nil {
			t.Fatal("заготовка с умершим tmux осталась в списке")
		}
		if _, err := os.Stat(blankPath(e.home, "blank-dead")); !os.IsNotExist(err) {
			t.Fatalf("запись осталась на диске: %v", err)
		}
	})

	t.Run("tmux жив", func(t *testing.T) {
		e, c := chatEnv(t)
		writeScript(t, e.bin, "tmux", `case "$1" in ls) printf 'chat-7\t1\t1754770421\n';; esac
exit 0`)
		put(t, e, "blank-alive", lifted)
		if blankRow(blankList(t, e, c, "?all=1"), "blank-alive") == nil {
			t.Fatal("заготовка с живым tmux стёрта: сессия ещё может назваться")
		}
	})

	t.Run("опрос сорвался", func(t *testing.T) {
		e, c := chatEnv(t)
		writeScript(t, e.bin, "tmux", `case "$1" in ls) exit 1;; esac
exit 0`)
		put(t, e, "blank-unknown", lifted)
		if blankRow(blankList(t, e, c, "?all=1"), "blank-unknown") == nil {
			t.Fatal("заготовка стёрта по сорванному опросу tmux")
		}
	})

	t.Run("есть набранный текст", func(t *testing.T) {
		e, c := chatEnv(t)
		writeScript(t, e.bin, "tmux", `case "$1" in ls) `+tmuxNoServer+`;; esac
exit 0`)
		typed := lifted
		typed.Draft = "недописанное"
		put(t, e, "blank-typed", typed)
		if blankRow(blankList(t, e, c, "?all=1"), "blank-typed") == nil {
			t.Fatal("запись с текстом человека стёрта вслед за tmux")
		}
	})
}
