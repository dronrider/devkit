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

func TestSelectionReplyKeepsOtherAttachments(t *testing.T) {
	text := "<screenshot file=\"/tmp/choice.png\">\nвставлен снимок экрана\n</screenshot>\n<fragment file=\"/tmp/choice.txt\">\nвставлен фрагмент 5 КБ\n</fragment>\nпояснение"
	wire := (chat.SelectionMessage{Version: 1, Text: text, Selection: &chat.Selection{Task: "XR-4", Questions: []chat.SelectedQuestion{{Fork: "сценарии", Snapshot: "sc1-test", IDs: []string{"СЦ-1"}}}}}).Wire()
	item := reply{Role: "user", Text: wire}
	projectPanelReply(&item)
	if item.Text != "пояснение" || item.Wire != wire || item.Selection == nil || len(item.Shots) != 1 || item.Shots[0] != "/tmp/choice.png" || len(item.Frags) != 1 || item.Frags[0] != "/tmp/choice.txt" {
		t.Fatalf("выбор потерял прочие вложения: %+v", item)
	}
}

func TestSelectionWorktreeDocument(t *testing.T) {
	for _, mode := range []string{"source", "legacy", "failed-context", "delivered-context", "legacy-success", "legacy-archive-success"} {
		t.Run(mode, func(t *testing.T) {
			st := newScenarioStand(t)
			git := func(args ...string) {
				t.Helper()
				cmd := exec.Command("git", append([]string{"-c", "core.hooksPath=/dev/null", "-c", "user.name=stand", "-c", "user.email=stand@example.invalid", "-C", st.e.proj}, args...)...)
				cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null")
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("git %v: %v\n%s", args, err, out)
				}
			}
			git("init")
			git("add", "docs")
			git("commit", "-m", "stand")
			tree := filepath.Join(filepath.Dir(st.e.proj), "selection-branch")
			git("worktree", "add", "-b", "selection", tree)
			primary := readFile(t, st.path)
			linked := *st.e
			linked.proj = tree
			work := *st
			work.e = &linked
			work.path = filepath.Join(tree, "docs/tasks/drafts/XR-4.md")
			work.run(t, "--ask", "срок", "--hint", "месяц: хватает", "--option", "месяц", "--option", "год", "когда?")
			cmd := exec.Command(st.bin, "-C", tree, "decide", "XR-4", "срок", "--chat")
			cmd.Env = append(os.Environ(), "DEVKIT_TMUX=chat-scenario-stand", "CLAUDE_CODE_SESSION_ID="+scenarioPanelSID)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("publish: %v %s", err, out)
			}
			name := chat.TaskName("XR-4")
			askPath := chat.AskPath(st.e.proj, name)
			raw := readFile(t, askPath)
			if mode == "source" && !strings.Contains(raw, "дерево "+tree+"\n") {
				t.Fatalf("source lost: %s", raw)
			}
			if mode != "source" {
				raw = strings.ReplaceAll(raw, "дерево "+tree+"\n", "")
				if err := os.WriteFile(askPath, []byte(raw), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			writeBinds(t, st.e.home, "2026-11-15T12:00:01 сессия "+scenarioPanelSID+" задача XR-4 проект demo дерево "+tree+" транскрипт "+standTranscript(st.e.home, "t")+" источник заказ повод startup tmux -\n")
			hs := st.e.s.handedAsks(scenarioPanelSID, st.e.s.binds(), st.e.s.now())
			if len(hs) != 1 {
				t.Fatalf("asks: %+v", hs)
			}
			shown := agentAskOf(hs[0])
			if shown.Tree != tree {
				t.Fatalf("wrong document: %+v", shown)
			}
			alien := filepath.Join(filepath.Dir(st.e.proj), "alien")
			mkProject(t, alien)
			forged := shown
			forged.Tree = alien
			st.sendSelection(t, selectionTestBody(t, forged, "", "foreign-project", map[string][]string{"срок": {"1"}}), http.StatusBadRequest)
			request := selectionTestBody(t, shown, "точные слова", "worktree-selection", map[string][]string{"срок": {"1"}})
			if mode == "failed-context" || mode == "delivered-context" || mode == "legacy-success" || mode == "legacy-archive-success" {
				data, _ := json.Marshal(request["selection"])
				var previous chat.Selection
				if err := json.Unmarshal(data, &previous); err != nil {
					t.Fatal(err)
				}
				previous.Tree = st.e.proj
				key, err := st.e.s.selectionTextKey(scenarioPanelSID, "worktree-selection", "точные слова", previous)
				if err != nil {
					t.Fatal(err)
				}
				store := st.e.s.chatStoreRead(scenarioPanelSID)
				stored := selectionDeliveryContext{Reply: "worktree-selection", TextKey: key, Selection: previous, Name: name, Session: scenarioPanelSID}
				if mode == "delivered-context" {
					if err := json.Unmarshal([]byte(`{"delivered":true}`), &stored); err != nil {
						t.Fatal(err)
					}
				}
				store.SelectionContexts = append(store.SelectionContexts, stored)
				if err := st.e.s.chatStoreWrite(scenarioPanelSID, store); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "legacy-success" || mode == "legacy-archive-success" {
				receipt := `{"reply":"worktree-selection","selection":{},"result":"решено раньше"}`
				saved, err := taskform.AppendToFork(primary, "сценарии", "  - вложение ответа: "+receipt)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(st.path, []byte(saved), 0o644); err != nil {
					t.Fatal(err)
				}
				if mode == "legacy-archive-success" {
					putDoc(t, st.e.proj, "docs/tasks/archive/2026/XR-4.md", saved)
					if err := os.Remove(st.path); err != nil {
						t.Fatal(err)
					}
				}
			}
			if mode == "delivered-context" || mode == "legacy-success" || mode == "legacy-archive-success" {
				before := readFile(t, work.path)
				st.sendSelection(t, request, http.StatusBadRequest)
				if readFile(t, work.path) != before || len(chatLines(t, st.e.proj, name)) != 0 {
					t.Fatal("successful context was retargeted")
				}
				return
			}
			bad := selectionTestBody(t, shown, "точные слова", "worktree-selection", map[string][]string{"срок": {"2"}})
			if mode == "failed-context" {
				st.sendSelection(t, bad, http.StatusBadRequest)
			}
			wrong := shown
			wrong.Snapshot = "q1-wrong"
			st.sendSelection(t, selectionTestBody(t, wrong, "", "wrong-snapshot", map[string][]string{"срок": {"1"}}), http.StatusBadRequest)
			wrong = shown
			wrong.Fork = "чужой вопрос"
			st.sendSelection(t, selectionTestBody(t, wrong, "", "wrong-fork", map[string][]string{"чужой вопрос": {"1"}}), http.StatusBadRequest)
			st.sendSelection(t, request, http.StatusOK)
			f, found := taskform.FindFork(readFile(t, work.path), "срок")
			if !found || f.Answer != "месяц: хватает" || readFile(t, st.path) != primary {
				t.Fatalf("wrong document changed: %+v", f)
			}
			after := readFile(t, work.path)
			st.sendSelection(t, request, http.StatusOK)
			st.e.s.mu.Lock()
			st.e.s.says = nil
			st.e.s.mu.Unlock()
			repeated := st.sendSelection(t, request, http.StatusOK)
			if !strings.Contains(repeated, `"way":"dup"`) {
				t.Fatalf("successful response delivered again: %s", repeated)
			}
			if readFile(t, work.path) != after || len(chatLines(t, st.e.proj, name)) != 1 {
				t.Fatal("duplicate application or delivery")
			}
			if _, err := os.Stat(chat.Path(tree, name)); !os.IsNotExist(err) {
				t.Fatalf("transport moved into document tree: %v", err)
			}
		})
	}
}

func TestSelectionDeliveredMergeMonotonic(t *testing.T) {
	var delivered, pending selectionDeliveryContext
	raw := `{"reply":"saved","textKey":"same","name":"task-XR-4","selection":{"task":"XR-4","tree":"/tmp/document"},"transport":"/tmp/main","delivered":true}`
	if err := json.Unmarshal([]byte(raw), &delivered); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(strings.Replace(raw, `"delivered":true`, `"delivered":false`, 1)), &pending); err != nil {
		t.Fatal(err)
	}
	for _, groups := range [][]selectionDeliveryContext{{delivered, pending}, {pending, delivered}} {
		merged := mergeSelectionContexts(groups[:1], groups[1:])
		data, _ := json.Marshal(merged)
		if len(merged) != 1 || !strings.Contains(string(data), `"delivered":true`) {
			t.Fatalf("delivery marker regressed: %s", data)
		}
	}
}

func TestSelectionContextMigrationMerge(t *testing.T) {
	var legacy, current selectionDeliveryContext
	if err := json.Unmarshal([]byte(`{"reply":"saved","textKey":"same","name":"task-XR-4","session":"source","selection":{"task":"XR-4","tree":"/tmp/main"}}`), &legacy); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(`{"reply":"saved","textKey":"same","name":"task-XR-4","session":"source","selection":{"task":"XR-4","tree":"/tmp/document"},"transport":"/tmp/main","delivered":true}`), &current); err != nil {
		t.Fatal(err)
	}
	for _, groups := range [][]selectionDeliveryContext{{legacy, current}, {current, legacy}} {
		merged := mergeSelectionContexts(groups[:1], groups[1:])
		data, _ := json.Marshal(merged)
		if len(merged) != 1 || merged[0].Selection.Tree != "/tmp/document" || !strings.Contains(string(data), `"delivered":true`) {
			t.Fatalf("stale legacy context returned: %s", data)
		}
	}
}
