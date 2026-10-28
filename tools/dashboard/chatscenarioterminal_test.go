package main

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dronrider/devkit/internal/chat"
)

func TestScenarioSayOrdinaryTermKeepsQueuedScenario(t *testing.T) {
	st := newScenarioStand(t)
	e := st.e
	queuedDoc := filepath.Join(filepath.Dir(st.path), "XR-100.md")
	if err := os.WriteFile(queuedDoc, []byte(strings.ReplaceAll(readFile(t, st.path), "XR-4", "XR-100")), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(st.bin, "-C", e.proj, "decide", "XR-100", "сценарии", "--chat")
	cmd.Env = append(os.Environ(), "DEVKIT_TMUX=chat-scenario-stand", "CLAUDE_CODE_SESSION_ID="+scenarioPanelSID)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("публикация сценарного вопроса CLI: %v\n%s", err, out)
	}
	queuedPath := chat.AskPath(e.proj, chat.TaskName("XR-100"))
	ask, ok := chat.ReadAsk(queuedPath)
	if !ok || len(ask.Questions) != 1 || ask.Questions[0].Scenario == nil || !strings.HasPrefix(ask.Questions[0].Scenario.Snapshot, "sc1-") {
		t.Fatalf("CLI не опубликовал сценарный снимок: %+v", ask)
	}
	writeBinds(t, e.home, fmt.Sprintf("2026-10-28T12:00:00 сессия %s задача XR-4 проект demo дерево %s транскрипт %s источник заказ повод startup tmux task-XR-4\n", scenarioPanelSID, e.proj, standTranscript(e.home, "t")))
	sent := filepath.Join(e.home, "sent.log")
	writeScript(t, e.bin, "tmux", `case "$1" in
ls) printf 'task-XR-4\t1\t1754770421\n';;
capture-pane) printf '';;
send-keys) shift; echo "$@" >> `+sent+`;;
esac
exit 0`)
	ordinary := chat.Ask{Session: scenarioPanelSID, Task: "XR-4", Questions: []chat.Question{{Text: "«срок»: сколько хранить?", Options: []chat.Option{{Label: "30 дней"}}}}}
	if err := chat.WriteAsk(e.proj, chat.TaskName("XR-4"), ordinary); err != nil {
		t.Fatal(err)
	}
	waiting, doc, ownDoc := readFile(t, queuedPath), readFile(t, queuedDoc), readFile(t, st.path)
	r := doReq(t, st.c, "POST", e.srv.URL+"/api/projects/demo/chats/"+scenarioPanelSID+"/say", `{"text":"срок 1"}`)
	out := body(t, r)
	if r.StatusCode != http.StatusOK || !strings.Contains(out, `"way":"send-keys"`) {
		t.Errorf("обычный ответ не доставлен терминалу: %d %s", r.StatusCode, out)
	}
	if got := readFile(t, sent); got != "-t =task-XR-4: -l срок 1\n-t =task-XR-4: Enter\n" {
		t.Errorf("ответ не дошёл один раз в выбранный терминал: %q", got)
	}
	if _, ok := chat.ReadAsk(chat.AskPath(e.proj, chat.TaskName("XR-4"))); ok {
		t.Error("терминальный ответ сохранил собственное ожидание")
	}
	if got, err := os.ReadFile(queuedPath); err != nil || string(got) != waiting {
		t.Errorf("обычный терминальный ответ изменил соседнее сценарное ожидание: %v %q", err, got)
	}
	if readFile(t, queuedDoc) != doc || readFile(t, st.path) != ownDoc {
		t.Error("обычная доставка изменила файл решения")
	}
	for _, name := range []string{"XR-4", "XR-100"} {
		if lines := chatLines(t, e.proj, chat.TaskName(name)); len(lines) != 0 {
			t.Errorf("терминальный ответ дополнительно положен во вход %s: %q", name, lines)
		}
	}
}
