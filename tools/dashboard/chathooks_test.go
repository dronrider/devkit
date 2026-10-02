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
     "models": [{"tier": "pro", "model": "вторая-pro"}, {"tier": "base", "model": "opus"}]}
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

// storeModel кладёт настройки диалога с моделью второй подписки: по ней ручки
// реплики и продолжения выбирают, чьи хуки судить.
func storeModel(t *testing.T, e *testEnv, key, model string) {
	t.Helper()
	data, err := json.Marshal(chatStore{Model: model})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(chatStoreDir(e.home), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(chatStoreDir(e.home), key+".json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

type raiseEnv func(t *testing.T) (*testEnv, *http.Client, string)

// TestEveryRaiseRefusesWithoutHooks: барьер стоит на каждой дороге подъёма, а
// не на одной ручке. Убери вызов проверки с любой из них, и подъём на подписке
// без хуков опять пройдёт молча.
func TestEveryRaiseRefusesWithoutHooks(t *testing.T) {
	const sid = "aaaa1111-2222-4222-8222-333333333333"
	const bare = "bbbb1111-2222-4222-8222-333333333333"
	cases := []struct {
		name string
		path string
		body string
		env  raiseEnv
	}{
		{"реплика резюмом", "/chats/" + sid + "/say", `{"text": "привет"}`, func(t *testing.T) (*testEnv, *http.Client, string) {
			e, c, log := hooksGapEnv(t, hooksGapText)
			endedChat(t, e, sid, "-", "chat-7")
			storeModel(t, e, sid, "вторая-pro")
			return e, c, log
		}},
		{"реплика в чат без сессии", "/chats/" + bare + "/say", `{"text": "привет"}`, func(t *testing.T) (*testEnv, *http.Client, string) {
			e, c, log := hooksGapEnv(t, hooksGapText)
			storeModel(t, e, bare, "вторая-pro")
			return e, c, log
		}},
		{"продолжить задачу резюмом", "/tasks/XR-4/continue", ``, func(t *testing.T) (*testEnv, *http.Client, string) {
			e, c, log := hooksGapEnv(t, hooksGapText)
			endedChat(t, e, sid, "XR-4", "chat-7")
			storeModel(t, e, sid, "вторая-pro")
			return e, c, log
		}},
		{"продолжить задачу с чистого листа", "/tasks/XR-4/continue", ``, func(t *testing.T) (*testEnv, *http.Client, string) {
			return hooksGapEnv(t, hooksGapText)
		}},
		{"запуск задачи", "/runs", `{"id": "XR-002", "harness": "втораяtest"}`, func(t *testing.T) (*testEnv, *http.Client, string) {
			e, c, log := runsEnv(t, "")
			writeAgentctlFake(t, e.bin, hooksGapLayout(hooksGapText))
			return e, c, log
		}},
		{"груминг черновика", "/drafts/XR-005/groom", `{"harness": "втораяtest"}`, func(t *testing.T) (*testEnv, *http.Client, string) {
			e, c, _ := tasksEnv(t)
			log := filepath.Join(e.home, "tmux.log")
			writeTmuxFake(t, e.bin, log, "")
			writeScript(t, e.bin, "claude", "exit 0")
			writeAgentctlFake(t, e.bin, hooksGapLayout(hooksGapText))
			doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/drafts",
				`{"title": "дашборд не показывает накопитель черновиков", "prio": "mid"}`).Body.Close()
			return e, c, log
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e, c, tmuxLog := tc.env(t)
			resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo"+tc.path, tc.body)
			text := body(t, resp)
			if resp.StatusCode != http.StatusBadGateway {
				t.Fatalf("подъём на подписке без хуков: %d %s, жду 502", resp.StatusCode, text)
			}
			if !strings.Contains(text, "обвязки нет") {
				t.Fatalf("отказ не назвал находку: %s", text)
			}
			if log, err := os.ReadFile(tmuxLog); err == nil && strings.Contains(string(log), "new-session") {
				t.Fatalf("tmux-сессия поднялась, хотя обвязки нет: %s", log)
			}
		})
	}
}
