package main

import (
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dronrider/devkit/internal/chat"
	"github.com/dronrider/devkit/internal/taskform"
)

func selectionTestBody(t *testing.T, ask agentAsk, text, id string, ids map[string][]string) map[string]any {
	t.Helper()
	data, _ := json.Marshal(ask)
	var shown map[string]any
	if err := json.Unmarshal(data, &shown); err != nil {
		t.Fatal(err)
	}
	steps, _ := shown["steps"].([]any)
	if len(steps) == 0 {
		steps = []any{shown}
	}
	questions := []any{}
	for _, raw := range steps {
		step := raw.(map[string]any)
		fork, _ := step["fork"].(string)
		selected, ok := ids[fork]
		if !ok {
			continue
		}
		snapshot := step["snapshot"]
		if scenario, ok := step["scenario"].(map[string]any); ok {
			snapshot = scenario["snapshot"]
		}
		questions = append(questions, map[string]any{"fork": fork, "snapshot": snapshot, "ids": selected})
	}
	return map[string]any{"text": text, "msg_id": id, "selection": map[string]any{"task": "XR-4", "tree": ask.Tree, "questions": questions}}
}

func (st *scenarioStand) sendSelection(t *testing.T, data map[string]any, want int) string {
	t.Helper()
	encoded, _ := json.Marshal(data)
	r := doReq(t, st.c, "POST", st.e.srv.URL+"/api/projects/demo/chats/"+scenarioPanelSID+"/say", string(encoded))
	out := body(t, r)
	if r.StatusCode != want {
		t.Fatalf("выбор: %d, ждали %d: %s", r.StatusCode, want, out)
	}
	return out
}

func TestSelectionPreservesSupplement(t *testing.T) {
	st := newScenarioStand(t)
	ask := st.ask(t)
	raw := "  Дополню ещё один СЦ:\nЕсли задача была приписана к другой, то и у чата в заголовке задача меняется на актуальную.\n\nплюс, минус; \"свои слова\"  "
	request := selectionTestBody(t, ask, raw, "selection-supplement", map[string][]string{"сценарии": {"СЦ-1", "СЦ-3"}})
	st.sendSelection(t, request, http.StatusOK)
	f, _ := taskform.FindFork(readFile(t, st.path), "сценарии")
	if f.Answer != "СЦ-1, СЦ-3" || f.ChoiceEvent != 1 || len(f.Options) != 3 {
		t.Fatalf("дополнение разобрано как выбор: %+v", f)
	}
	lines := chatLines(t, st.e.proj, chat.TaskName("XR-4"))
	if len(lines) != 1 {
		t.Fatalf("доставка: %q", lines)
	}
	said := chat.Said(lines[0])
	cut := strings.Index(said, "{")
	var delivered struct {
		Text      string `json:"text"`
		Selection struct {
			Questions []struct {
				IDs []string `json:"ids"`
			} `json:"questions"`
		} `json:"selection"`
	}
	if cut < 0 || json.Unmarshal([]byte(said[cut:]), &delivered) != nil || delivered.Text != raw || len(delivered.Selection.Questions) != 1 || strings.Join(delivered.Selection.Questions[0].IDs, ",") != "СЦ-1,СЦ-3" {
		t.Fatalf("потерян текст или вложение: %q", said)
	}
	before := readFile(t, st.path)
	st.sendSelection(t, request, http.StatusOK)
	if readFile(t, st.path) != before || len(chatLines(t, st.e.proj, chat.TaskName("XR-4"))) != 1 {
		t.Fatal("повтор размножил решение или реплику")
	}
}

func TestSelectionFreeTextKeepsQuestion(t *testing.T) {
	st := newScenarioStand(t)
	st.ask(t)
	waiting := chat.AskPath(st.e.proj, chat.TaskName("XR-4"))
	before, ask := readFile(t, st.path), readFile(t, waiting)
	raw := "Дополню ещё один СЦ:\nЕсли задача была приписана к другой, то и у чата в заголовке задача меняется на актуальную.\n\n"
	st.sendSelection(t, map[string]any{"text": raw, "msg_id": "comment"}, http.StatusOK)
	if readFile(t, st.path) != before || readFile(t, waiting) != ask {
		t.Fatal("комментарий закрыл состав или ожидание")
	}
	lines := chatLines(t, st.e.proj, chat.TaskName("XR-4"))
	var message struct {
		Text string `json:"text"`
	}
	if len(lines) != 1 {
		t.Fatalf("доставка: %q", lines)
	}
	said := strings.TrimPrefix(chat.Said(lines[0]), "Ответ с вложением панели: ")
	if len(lines) != 1 || json.Unmarshal([]byte(said), &message) != nil || message.Text != raw {
		t.Fatalf("слова изменены или превращены в служебный JSON: %q", lines)
	}
}

func TestSelectionPackAndValidation(t *testing.T) {
	st := newScenarioStand(t)
	st.run(t, "--ask", "срок", "--hint", "30 дней: достаточно", "--option", "7 дней", "сколько хранить?")
	st.run(t, "--ask", "вывод", "--hint", "полный: нужен", "--option", "краткий", "какой вывод?")
	ask := st.publish(t, false)
	wanted := map[string][]string{"сценарии": {"СЦ-3"}, "срок": {"2"}, "вывод": {"1"}}
	request := selectionTestBody(t, ask, "точные слова", "pack", wanted)
	before, waiting := readFile(t, st.path), readFile(t, chat.AskPath(st.e.proj, chat.TaskName("XR-4")))
	for _, field := range []string{"tree", "task", "fork", "snapshot", "ids", "duplicate"} {
		t.Run(field, func(t *testing.T) {
			data := selectionTestBody(t, ask, "точные слова", "bad-"+field, wanted)
			a := data["selection"].(map[string]any)
			qs := a["questions"].([]any)
			q := qs[len(qs)-1].(map[string]any)
			switch field {
			case "tree":
				a["tree"] = t.TempDir()
			case "task":
				a["task"] = "XR-100"
			case "fork":
				q["fork"] = "чужой"
			case "snapshot":
				q["snapshot"] = "q1-чужой"
			case "ids":
				q["ids"] = []string{"999"}
			case "duplicate":
				a["questions"] = append(qs, qs[0])
			}
			st.sendSelection(t, data, http.StatusBadRequest)
			if readFile(t, st.path) != before || readFile(t, chat.AskPath(st.e.proj, chat.TaskName("XR-4"))) != waiting {
				t.Fatal("неверный выбор записан частично")
			}
		})
	}
	st.sendSelection(t, request, http.StatusOK)
	for name, want := range map[string]string{"сценарии": "СЦ-3", "срок": "7 дней", "вывод": "полный: нужен"} {
		f, _ := taskform.FindFork(readFile(t, st.path), name)
		if f.Answer != want {
			t.Fatalf("%s: %q, ждали %q", name, f.Answer, want)
		}
	}
}

func TestSelectionEmptyRefusalAndRetryDelivery(t *testing.T) {
	st := newScenarioStand(t)
	ask := st.ask(t)
	carrier := chat.Path(st.e.proj, chat.TaskName("XR-4"))
	if err := os.Mkdir(carrier, 0700); err != nil {
		t.Fatal(err)
	}
	request := selectionTestBody(t, ask, "", "refusal", map[string][]string{"сценарии": {}})
	st.sendSelection(t, request, http.StatusBadGateway)
	f, _ := taskform.FindFork(readFile(t, st.path), "сценарии")
	if !f.Reconsidered || !f.HoldsStart() || f.ChoiceEvent != 1 {
		t.Fatalf("отказ потерян: %+v", f)
	}
	before := readFile(t, st.path)
	if err := os.Remove(carrier); err != nil {
		t.Fatal(err)
	}
	st.sendSelection(t, request, http.StatusOK)
	if readFile(t, st.path) != before || len(chatLines(t, st.e.proj, chat.TaskName("XR-4"))) != 1 {
		t.Fatal("повтор после принятия изменил выбор или потерял доставку")
	}
	fresh := st.ask(t)
	encoded, _ := json.Marshal(fresh)
	if !strings.Contains(string(encoded), `"explicit":true`) || len(fresh.Scenario.Current) != 0 {
		t.Fatalf("отказ не отличим от нового выбора: %s", encoded)
	}
	request = selectionTestBody(t, fresh, "", "choose-after-refusal", map[string][]string{"сценарии": {"СЦ-2"}})
	st.sendSelection(t, request, http.StatusOK)
	if len(chatLines(t, st.e.proj, chat.TaskName("XR-4"))) != 2 {
		t.Fatal("одинаковый пустой текст потерял разные вложения")
	}
}

func TestSelectionStaleQuestionDoesNotApply(t *testing.T) {
	st := newScenarioStand(t)
	ask := st.ask(t)
	doc := strings.Replace(readFile(t, st.path), "вариант: СЦ-2 (вывод из примера): собрать", "вариант: СЦ-2 (вывод из примера): иначе собрать", 1)
	if err := os.WriteFile(st.path, []byte(doc), 0644); err != nil {
		t.Fatal(err)
	}
	request := selectionTestBody(t, ask, "не менять слова", "stale", map[string][]string{"сценарии": {"СЦ-2"}})
	st.sendSelection(t, request, http.StatusBadRequest)
	if readFile(t, st.path) != doc {
		t.Fatal("устаревший выбор изменил запись")
	}
}

func TestSelectionHistoryFields(t *testing.T) {
	st := newScenarioStand(t)
	ask := st.ask(t)
	raw := "  точные слова\n\n"
	request := selectionTestBody(t, ask, raw, "history", map[string][]string{"сценарии": {"СЦ-2"}})
	st.sendSelection(t, request, http.StatusOK)
	data, _ := os.ReadFile(saidFile(st.e.home, saidSessionKey(scenarioPanelSID)))
	if len(data) == 0 {
		t.Fatal("нет долговечного следа отправки")
	}
	loaded := saidLoad(st.e.home, saidSessionKey(scenarioPanelSID))
	if len(loaded) != 1 {
		t.Fatalf("история: %+v", loaded)
	}
	encoded, _ := json.Marshal(loaded[0])
	var item map[string]any
	if err := json.Unmarshal(encoded, &item); err != nil {
		t.Fatal(err)
	}
	if item["text"] != raw || item["time"] == "" || item["selection"] == nil || item["wire"] == nil {
		t.Fatalf("история смешала слова и вложение: %s", encoded)
	}
	transcript, _ := json.Marshal(map[string]any{"type": "user", "timestamp": "2026-10-30T12:00:00Z", "message": map[string]any{"role": "user", "content": item["wire"]}})
	replies := parseReplies(append(transcript, '\n'), 0)
	if len(replies) != 1 {
		t.Fatalf("транскрипт: %+v", replies)
	}
	projected, _ := json.Marshal(replies[0])
	var echo map[string]any
	json.Unmarshal(projected, &echo)
	if echo["text"] != raw || echo["selection"] == nil {
		t.Fatalf("эхо потеряло поля: %s", projected)
	}
	if merged := saidMerge(replies, loaded); len(merged) != 1 {
		t.Fatalf("история задублировала эхо: %+v", merged)
	}
}

func TestSelectionReceiptBelongsToMessage(t *testing.T) {
	st := newScenarioStand(t)
	ask := st.ask(t)
	carrier := chat.Path(st.e.proj, chat.TaskName("XR-4"))
	if err := os.Mkdir(carrier, 0700); err != nil {
		t.Fatal(err)
	}
	request := selectionTestBody(t, ask, "  ещё слова\n", "receipt-original", map[string][]string{"сценарии": {"СЦ-1"}})
	st.sendSelection(t, request, http.StatusBadGateway)
	st.run(t, "сценарии", "--option", "СЦ-4 (предложение агента): новый вариант после приёма")
	changed := readFile(t, st.path)
	if err := os.Remove(carrier); err != nil {
		t.Fatal(err)
	}
	// The old receipt is a delivery retry, even after the next question changed.
	st.sendSelection(t, request, http.StatusOK)
	if readFile(t, st.path) != changed {
		t.Fatal("повтор переписал новое состояние вопроса")
	}
	// A new message using the accepted snapshot has no authority to apply it again.
	stale := selectionTestBody(t, ask, "  ещё слова\n", "receipt-new-message", map[string][]string{"сценарии": {"СЦ-1"}})
	st.sendSelection(t, stale, http.StatusBadRequest)
	if readFile(t, st.path) != changed || len(chatLines(t, st.e.proj, chat.TaskName("XR-4"))) != 1 {
		t.Fatal("старый снимок получил новое применение или доставку")
	}
}

func TestSelectionQueueReceiver(t *testing.T) {
	for _, selected := range []bool{false, true} {
		t.Run(map[bool]string{false: "comment", true: "selection"}[selected], func(t *testing.T) {
			st := newScenarioStand(t)
			ask := st.ask(t)
			raw := "  Дополню ещё один СЦ:\nЕсли задача была приписана к другой, то и у чата в заголовке задача меняется на актуальную.\n\n"
			request := map[string]any{"text": raw, "msg_id": "real-hook"}
			if selected {
				request = selectionTestBody(t, ask, raw, "real-hook", map[string][]string{"сценарии": {"СЦ-2"}})
			}
			st.sendSelection(t, request, http.StatusOK)
			hook, err := filepath.Abs("../../hooks/chat-in.py")
			if err != nil {
				t.Fatal(err)
			}
			script := `import importlib.util,json,os,sys,time,types
sys.path.insert(0,os.path.dirname(sys.argv[1]))
spec=importlib.util.spec_from_file_location("chat_in",sys.argv[1]); mod=importlib.util.module_from_spec(spec); spec.loader.exec_module(mod)
args=(os.path.join(sys.argv[2],".devkit","chat"),"task-XR-4",".in",types.SimpleNamespace(session=sys.argv[3]),time.time(),sys.argv[2])
first=mod.serve_chat(*args); second=mod.serve_chat(*args)
print(json.dumps({"first":first,"second":second},ensure_ascii=False))`
			cmd := exec.Command("python3", "-c", script, hook, st.e.proj, scenarioPanelSID)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("получатель: %v\n%s", err, out)
			}
			var received struct {
				First  string  `json:"first"`
				Second *string `json:"second"`
			}
			if json.Unmarshal(out, &received) != nil || received.Second != nil {
				t.Fatalf("повторная доставка получателю: %s", out)
			}
			found := false
			for _, line := range strings.Split(received.First, "\n") {
				const prefix = "- Ответ с вложением панели: "
				if !strings.HasPrefix(line, prefix) {
					continue
				}
				var value struct {
					Text      string `json:"text"`
					Selection *struct {
						Questions []struct {
							IDs []string `json:"ids"`
						} `json:"questions"`
					} `json:"selection"`
				}
				if err := json.Unmarshal([]byte(strings.TrimPrefix(line, prefix)), &value); err != nil {
					t.Fatal(err)
				}
				if value.Text != raw || (value.Selection != nil) != selected {
					t.Fatalf("получатель потерял слова/выбор: %s", line)
				}
				if selected && strings.Join(value.Selection.Questions[0].IDs, ",") != "СЦ-2" {
					t.Fatal("получателю достался другой выбор")
				}
				found = true
			}
			if !found {
				t.Fatalf("сообщение не пришло получателю: %s", out)
			}
		})
	}
}

func TestSelectionRetryAfterRebind(t *testing.T) {
	for _, archived := range []bool{false, true} {
		t.Run(map[bool]string{false: "open", true: "archived"}[archived], func(t *testing.T) {
			st := newScenarioStand(t)
			ask := st.ask(t)
			waiting := readFile(t, chat.AskPath(st.e.proj, chat.TaskName("XR-4")))
			carrier := chat.Path(st.e.proj, chat.TaskName("XR-4"))
			if err := os.Mkdir(carrier, 0700); err != nil {
				t.Fatal(err)
			}
			raw := "  дополнение после выбора\n\n"
			request := selectionTestBody(t, ask, raw, "accepted-rebind", map[string][]string{"сценарии": {"СЦ-1"}})
			st.sendSelection(t, request, http.StatusBadGateway)
			original := st.path
			if archived {
				if err := os.Remove(filepath.Join(st.e.proj, "docs/tasks/XR-4.md")); err != nil {
					t.Fatal(err)
				}
				original = filepath.Join(st.e.proj, "docs/tasks/archive/2026/XR-4.md")
				if err := os.MkdirAll(filepath.Dir(original), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.Rename(st.path, original); err != nil {
					t.Fatal(err)
				}
			}
			before, board := readFile(t, original), readFile(t, filepath.Join(st.e.proj, "docs/TASKS.md"))
			if err := os.Remove(carrier); err != nil {
				t.Fatal(err)
			}
			other := t.TempDir()
			putDoc(t, other, "docs/TASKS.md", "# другая доска\n")
			putDoc(t, other, "docs/tasks/XR-4.md", "# XR-4: другая запись\nзащита: locked\n")
			protected := readFile(t, filepath.Join(other, "docs/tasks/XR-4.md"))
			writeBinds(t, st.e.home, "2026-10-30T12:01:00 сессия "+scenarioPanelSID+" задача XR-4 проект other дерево "+other+" транскрипт "+standTranscript(st.e.home, "t")+" источник заказ повод startup tmux -\n")
			st.sendSelection(t, request, http.StatusOK)
			st.sendSelection(t, request, http.StatusOK)
			lines := chatLines(t, st.e.proj, chat.TaskName("XR-4"))
			if len(lines) != 1 || chat.Addressee(lines[0]) != "" || len(chatLines(t, other, chat.TaskName("XR-4"))) != 0 {
				t.Fatalf("повтор сменил адрес или размножил доставку: %q", lines)
			}
			var message struct {
				Text      string `json:"text"`
				Selection struct {
					Task string `json:"task"`
					Tree string `json:"tree"`
				} `json:"selection"`
			}
			if json.Unmarshal([]byte(strings.TrimPrefix(chat.Said(lines[0]), "Ответ с вложением панели: ")), &message) != nil || message.Text != raw || message.Selection.Tree != st.e.proj || message.Selection.Task != "XR-4" {
				t.Fatalf("повтор потерял исходный ответ: %q", lines)
			}
			if readFile(t, original) != before || readFile(t, filepath.Join(st.e.proj, "docs/TASKS.md")) != board || readFile(t, filepath.Join(other, "docs/tasks/XR-4.md")) != protected {
				t.Fatal("доставка изменила исходную или чужую запись/доску")
			}
			if archived {
				changedText := selectionTestBody(t, ask, raw+"другие слова", "accepted-rebind", map[string][]string{"сценарии": {"СЦ-1"}})
				st.sendSelection(t, changedText, http.StatusBadRequest)
				changedIDs := selectionTestBody(t, ask, raw, "accepted-rebind", map[string][]string{"сценарии": {"СЦ-2"}})
				st.sendSelection(t, changedIDs, http.StatusBadRequest)
			}
			if archived {
				// An old shown Ask cannot authorize a new mutation in the archive.
				putDoc(t, st.e.proj, ".devkit/chat/task-XR-4.ask", waiting)
				fresh := selectionTestBody(t, ask, raw, "new-after-close", map[string][]string{"сценарии": {"СЦ-1"}})
				st.sendSelection(t, fresh, http.StatusBadRequest)
				if readFile(t, original) != before || len(chatLines(t, st.e.proj, chat.TaskName("XR-4"))) != 1 {
					t.Fatal("новый ответ изменил закрытую запись")
				}
			}
		})
	}
}

func TestSelectionOriginalProtectionStillApplies(t *testing.T) {
	st := newScenarioStand(t)
	ask := st.ask(t)
	doc := strings.Replace(readFile(t, st.path), "\n", "\nзамок: locked\n", 1)
	if err := os.WriteFile(st.path, []byte(doc), 0644); err != nil {
		t.Fatal(err)
	}
	request := selectionTestBody(t, ask, "новые слова", "protected-original", map[string][]string{"сценарии": {"СЦ-2"}})
	st.sendSelection(t, request, http.StatusConflict)
	if readFile(t, st.path) != doc || len(chatLines(t, st.e.proj, chat.TaskName("XR-4"))) != 0 {
		t.Fatal("защищённый исходный адрес изменён")
	}
}
