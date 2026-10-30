package main

import (
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/dronrider/devkit/internal/chat"
)

func TestScenarioSayRequiresShownContext(t *testing.T) {
	st := newScenarioStand(t)
	st.ask(t)
	path := chat.AskPath(st.e.proj, chat.TaskName("XR-4"))
	doc, waiting := readFile(t, st.path), readFile(t, path)
	for _, one := range []struct{ name, context string }{
		{"empty", `,"scenario":{}`},
		{"no-snapshot", `,"scenario":{"task":"XR-4","fork":"сценарии"}`},
		{"no-task", `,"scenario":{"fork":"сценарии","snapshot":"sc1-missing"}`},
		{"no-fork", `,"scenario":{"task":"XR-4","snapshot":"sc1-missing"}`},
	} {
		t.Run(one.name, func(t *testing.T) {
			if err := os.WriteFile(path, []byte(waiting), 0o644); err != nil {
				t.Fatal(err)
			}
			os.Remove(chat.Path(st.e.proj, chat.TaskName("XR-4")))
			r := doReq(t, st.c, "POST", st.e.srv.URL+"/api/projects/demo/chats/"+scenarioPanelSID+"/say",
				`{"text":"сценарии 3"`+one.context+`}`)
			out := body(t, r)
			if r.StatusCode != http.StatusBadRequest || !strings.Contains(out, "снимок") {
				t.Errorf("неполный контекст не дал наблюдаемого отказа: %d %s", r.StatusCode, out)
			}
			if readFile(t, st.path) != doc {
				t.Error("ответ без показанного снимка изменил файл выбора")
			}
			if got, err := os.ReadFile(path); err != nil || string(got) != waiting {
				t.Errorf("ответ без показанного снимка изменил ожидание: %v %q", err, got)
			}
			if lines := chatLines(t, st.e.proj, chat.TaskName("XR-4")); len(lines) != 0 {
				t.Errorf("непринятый ответ доставлен агенту: %q", lines)
			}
		})
	}
}

func TestScenarioSayKeepsOrdinaryQuestions(t *testing.T) {
	for _, one := range []struct {
		name     string
		question chat.Question
		queued   bool
	}{
		{"single", chat.Question{Text: "«срок»: сколько хранить?", Options: []chat.Option{{Label: "30 дней"}, {Label: "7 дней"}}}, false},
		{"free", chat.Question{Text: "чем держать очередь?"}, false},
		{"legacy", chat.Question{Text: "«кейсы»: какой состав?", Options: []chat.Option{{Label: "узкий состав, 1 кейс: (1) выбрать"}}}, false},
		{"single-with-queued-scenarios", chat.Question{Text: "«срок»: сколько хранить?", Options: []chat.Option{{Label: "30 дней"}}}, true},
	} {
		t.Run(one.name, func(t *testing.T) {
			e, c, frames := askSayEnvNoTerm(t, scenarioPanelSID)
			if one.queued {
				if err := chat.WriteAsk(e.proj, chat.TaskName("XR-100"), chat.Ask{Session: scenarioPanelSID, Task: "XR-100", Questions: []chat.Question{{Text: "«сценарии»: что включаем?", Multi: true, Scenario: &chat.ScenarioQuestion{Snapshot: "sc1-missing", Fork: "сценарии"}}}}); err != nil {
					t.Fatal(err)
				}
			}
			if err := chat.WriteAsk(e.proj, chat.TaskName("XR-4"), chat.Ask{Session: scenarioPanelSID, Task: "XR-4", Questions: []chat.Question{one.question}}); err != nil {
				t.Fatal(err)
			}
			r := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+scenarioPanelSID+"/say", `{"text":"срок 1, свои слова"}`)
			out := body(t, r)
			if r.StatusCode != http.StatusOK || !strings.Contains(out, `"way":"ask"`) {
				t.Fatalf("прежний ответ не доставлен: %d %s", r.StatusCode, out)
			}
			lines := chatLines(t, e.proj, chat.TaskName("XR-4"))
			if len(lines) != 1 || chat.Said(lines[0]) != "срок 1, свои слова" || len(frames()) != 0 {
				t.Fatalf("прежняя доставка изменилась: %q", lines)
			}
			if _, ok := chat.ReadAsk(chat.AskPath(e.proj, chat.TaskName("XR-4"))); ok {
				t.Fatal("обычный ответ оставил прежнее ожидание")
			}
		})
	}
}
