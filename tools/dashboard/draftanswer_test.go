package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dronrider/devkit/internal/chat"
	"github.com/dronrider/devkit/internal/draftproposal"
	"github.com/dronrider/devkit/internal/taskform"
)

// Опыт 10 ноября (DK-1679): у двух ответов на вопросы черновиков пропадал
// адресат. Ответ на развилку черновика уезжал из панели во вход задачи
// безадресной строкой, потому что дорога клавишами открывалась только чату с
// собственной привязкой к задаче, а спрашивавший чат жил с задачей «-».
// Подтверждение разбора с формы не снимало живой ask-вопрос груминга, потому
// что захват согласия ждал открытую нативную модалку, а вопрос был спрошен
// taskctl из хода. Оба теста повторяют живые цепочки.

// TestSelectionDraftForkReachesAsker: ответ из панели на развилку черновика
// доезжает окну спрашивавшей сессии клавишами, а не безадресной строкой во
// вход задачи, и признак ожидания снимается ответом.
func TestSelectionDraftForkReachesAsker(t *testing.T) {
	st := newScenarioStand(t)
	asker := "bb260000-2222-4222-8222-222222222222"
	now := time.Now()
	transcript := writeSession(t, st.e.home, st.e.proj, "", asker, plainTalk, now)
	appendBinds(t, st.e.home, fmt.Sprintf("2026-11-10T12:00:00 сессия %s задача - проект demo дерево %s транскрипт %s источник заказ повод startup tmux chat-asker\n", asker, st.e.proj, transcript))
	st.run(t, "--ask", "судьба", "--hint", "чинить сейчас, боль живая", "--option", "отложить", "что делать с багом?")
	keys := filepath.Join(t.TempDir(), "keys")
	writeScript(t, st.e.bin, "tmux", "case \"$1\" in\nls|list-sessions) printf 'chat-asker\\t1\\t1794141800\\n';;\nsend-keys) printf '%s\\n' \"$*\" >> "+shQuote(keys)+";;\nesac\nexit 0")
	q := chat.Question{Text: "«судьба»: что делать с багом?", Options: []chat.Option{{Label: "чинить сейчас, боль живая", Recommended: true}, {Label: "отложить"}}}
	if err := chat.WriteAsk(st.e.proj, chat.TaskName("XR-4"), chat.Ask{Session: asker, Task: "XR-4", Questions: []chat.Question{q}}); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(map[string]any{
		"text":   "",
		"msg_id": "draft-fork-answer",
		"selection": map[string]any{
			"task": "XR-4",
			"tree": st.e.proj,
			"questions": []any{map[string]any{
				"fork":     askForkName(q.Text),
				"snapshot": chat.QuestionSnapshot(q),
				"ids":      []string{"1"},
			}},
		},
	})
	r := doReq(t, st.c, "POST", st.e.srv.URL+"/api/projects/demo/chats/"+asker+"/say", string(data))
	out := body(t, r)
	if r.StatusCode != http.StatusOK {
		t.Fatalf("ответ развилки черновика: %d: %s", r.StatusCode, out)
	}
	sent := readFile(t, keys)
	if !strings.Contains(sent, `"result"`) {
		t.Fatalf("реплика не подана в окно спрашивавшего: %q", sent)
	}
	if lines := chatLines(t, st.e.proj, chat.TaskName("XR-4")); len(lines) != 0 {
		t.Fatalf("реплика ушла во вход задачи безадресной строкой: %q", lines)
	}
	if _, ok := chat.ReadAsk(chat.AskPath(st.e.proj, chat.TaskName("XR-4"))); ok {
		t.Fatal("признак ожидания пережил ответ")
	}
	f, ok := taskform.FindFork(readFile(t, st.path), "судьба")
	if !ok || f.Answer == "" {
		t.Fatalf("развилка черновика не решена: %+v", f)
	}
}

// TestPreparedFormConfirmReachesAsker: подтверждение разбора с формы черновика
// снимает живой ask-вопрос груминга и доносит исход окну спрашивавшей сессии
// той же строкой, какой отвечает чат. Случай DK-1672: вопрос был спрошен
// taskctl из хода, нативной модалки у сессии не было, и прежний захват
// согласия промахивался, оставляя чат ждать человека после применённого
// разбора.
func TestPreparedFormConfirmReachesAsker(t *testing.T) {
	st := newScenarioStand(t)
	kit, _ := filepath.Abs("../..")
	t.Setenv("DEVKIT_HOME", kit)
	putDoc(t, st.e.proj, "docs/TASKS-archive.md", "# Архив\n\n| ID | Задача | Тип | P | Закрыто | Ссылка |\n|---|---|---|---|---|---|\n")
	groom := "cc270000-3333-4333-8333-333333333333"
	now := time.Now()
	transcript := writeSession(t, st.e.home, st.e.proj, "", groom, plainTalk, now)
	appendBinds(t, st.e.home, fmt.Sprintf("2026-11-10T12:00:00 сессия %s задача XR-4 проект demo дерево %s транскрипт %s источник заказ повод startup tmux task-groom\n", groom, st.e.proj, transcript))
	keys := filepath.Join(t.TempDir(), "keys")
	writeScript(t, st.e.bin, "tmux", "case \"$1\" in\nls|list-sessions) printf 'task-groom\\t1\\t1794141800\\n';;\nsend-keys) printf '%s\\n' \"$*\" >> "+shQuote(keys)+";;\nesac\nexit 0")
	cli := func(input string, args ...string) string {
		t.Helper()
		cmd := exec.Command(st.bin, append([]string{"-C", st.e.proj}, args...)...)
		cmd.Env = append(os.Environ(), "CLAUDE_CODE_SESSION_ID="+groom, "DEVKIT_TMUX=task-groom")
		cmd.Stdin = strings.NewReader(input)
		raw, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%v: %s %v", args, raw, err)
		}
		return string(raw)
	}
	material := "## Что происходит\n\nПроверка записи.\n\n## Чего хотим\n\nЗапись проверяется.\n\n## DoD\n\n- Видно результат.\n\n## Ранг\n\n`0+1+1+0+1 = 3`, P3.\n\n- Серьёзность 0: работа продолжается.\n- Ценность 1: один сценарий.\n- Неопределённость 1: место известно.\n- Поправка на баг 0: новая работа.\n- Рычаг 1: убирает повтор.\n"
	for _, id := range []string{"XR-4", "XR-5"} {
		if id == "XR-5" {
			putDoc(t, st.e.proj, "docs/tasks/drafts/XR-5.md", "# XR-5: соседняя запись\n")
		}
		var view struct {
			Snapshot string `json:"snapshot"`
		}
		if json.Unmarshal([]byte(cli("", "draft", "prepared", id)), &view) != nil {
			t.Fatal("view")
		}
		proposals := []draftproposal.Proposal{{ID: "drop", Revision: 1, Outcome: "drop", Reason: "устарело"}}
		if id == "XR-4" {
			for _, key := range []string{"one", "two"} {
				proposals = append(proposals, draftproposal.Proposal{ID: key, Revision: 1, Outcome: "add", Title: "Задача " + key, Text: material, BoardType: "task", Rank: "0+1+1+0+1", Size: "S", Accept: "agent"})
			}
		}
		payload, _ := json.Marshal(map[string]any{"complete": true, "conclusion": "Предлагаю удалить лишнее и создать задачи.", "proposals": proposals})
		cli(string(payload), "draft", "prepared", "save", id, "--base", view.Snapshot)
	}
	printed := cli("", "draft", "prepared", "ask", "XR-4", "--also", "XR-5")
	var question chat.Question
	if err := json.Unmarshal([]byte(printed), &question); err != nil {
		t.Fatal(err)
	}
	var snapshots []draftproposal.Snapshot
	var decisions []draftproposal.Decision
	for _, source := range question.Prepared.Sources {
		snapshots = append(snapshots, draftproposal.Snapshot{Source: source.Source, Digest: source.Basis, Revision: source.Revision})
		for _, p := range source.Proposals {
			decisions = append(decisions, draftproposal.Decision{Source: source.Source, Proposal: p.ID, Revision: p.Revision, Action: "accept"})
		}
	}
	stale, _ := json.Marshal(draftproposal.ConfirmRequest{Snapshots: snapshots, Decisions: decisions})
	r := doReq(t, st.c, "POST", st.e.srv.URL+"/api/projects/demo/drafts/prepared/confirm", string(stale))
	out := body(t, r)
	if r.StatusCode != http.StatusOK {
		t.Fatalf("подтверждение с формы: %d: %s", r.StatusCode, out)
	}
	if _, ok := chat.ReadAsk(chat.AskPath(st.e.proj, chat.TaskName("XR-4"))); ok {
		t.Fatal("вопрос груминга пережил подтверждение с формы")
	}
	sent := readFile(t, keys)
	if !strings.Contains(sent, "Ответ панели обработан taskctl") {
		t.Fatalf("исход подтверждения не дошёл окну спрашивавшего: %q", sent)
	}
	var result struct {
		Results []draftproposal.Receipt `json:"results"`
	}
	if json.Unmarshal([]byte(out), &result) != nil || len(result.Results) != 4 {
		t.Fatalf("подтверждение применилось не полностью: %s", out)
	}
}
