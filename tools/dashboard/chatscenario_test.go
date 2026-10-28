package main

import (
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dronrider/devkit/internal/chat"
	"github.com/dronrider/devkit/internal/taskform"
)

const scenarioPanelSID = "aa144600-1111-4111-8111-111111111111"

type scenarioStand struct {
	e    *testEnv
	c    *http.Client
	path string
	bin  string
}

func newScenarioStand(t *testing.T) *scenarioStand {
	t.Helper()
	e, c := chatEnv(t)
	bin := os.Getenv("TASKCTL_ACCEPT_BIN")
	if bin == "" {
		bin = filepath.Join(t.TempDir(), "taskctl")
		cmd := exec.Command("go", "build", "-o", bin, ".")
		cmd.Dir = "../taskctl"
		cmd.Env = append(os.Environ(), "GOWORK=off")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("сборка taskctl: %v\n%s", err, out)
		}
	}
	old := taskctlBin
	taskctlBin = bin
	t.Cleanup(func() { taskctlBin = old })
	t.Setenv("HOME", e.home)
	t.Setenv("DEVKIT_TMUX", "")
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	t.Setenv("DEVKIT_HEADLESS", "1")
	writeScript(t, e.bin, "tmux", "exit 0")
	if err := os.MkdirAll(filepath.Join(e.proj, "hooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(e.proj, "hooks/notify.py"), []byte("raise SystemExit(0)\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(e.proj, "docs/tasks/drafts/XR-4.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	doc := "# XR-4: выбор\n\n## Пользовательские сценарии\n\n"
	for _, id := range []string{"СЦ-1", "СЦ-2", "СЦ-3"} {
		doc += "### " + id + "\n\n"
		for _, field := range []string{"Ситуация", "Действие", "Результат", "Польза", "Рекомендация", "Ограничение", "Источник"} {
			doc += field + ": пример\n"
		}
		doc += "\n"
	}
	doc += "## Развилки\n\n"
	if err := os.WriteFile(path, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	st := &scenarioStand{e: e, c: c, path: path, bin: bin}
	st.run(t, "--ask", "сценарии", "--scenarios", "--hint", "СЦ-1, СЦ-3: выбор и продолжение", "--option", "СЦ-1 (слово пользователя): выбрать", "--option", "СЦ-2 (вывод из примера): собрать", "--option", "СЦ-3 (предложение агента): **продолжить**", "что включаем?")
	writeSession(t, e.home, e.proj, "", scenarioPanelSID, plainTalk, time.Now())
	writeBinds(t, e.home, "2026-10-28T12:00:00 сессия "+scenarioPanelSID+" задача XR-4 проект demo дерево "+e.proj+" транскрипт "+standTranscript(e.home, "t")+" источник заказ повод startup tmux -\n")
	return st
}

func (st *scenarioStand) run(t *testing.T, args ...string) string {
	t.Helper()
	out, err := exec.Command(st.bin, append([]string{"-C", st.e.proj, "decide", "XR-4"}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("taskctl %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func (st *scenarioStand) ask(t *testing.T) agentAsk {
	return st.publish(t, true)
}

func (st *scenarioStand) publish(t *testing.T, named bool) agentAsk {
	t.Helper()
	args := []string{"-C", st.e.proj, "decide", "XR-4"}
	if named {
		args = append(args, "сценарии")
	}
	cmd := exec.Command(st.bin, append(args, "--chat")...)
	cmd.Env = append(os.Environ(), "DEVKIT_TMUX=chat-scenario-stand", "CLAUDE_CODE_SESSION_ID="+scenarioPanelSID)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("публикация CLI: %v\n%s", err, out)
	}
	r := doReq(t, st.c, "GET", st.e.srv.URL+"/api/projects/demo/chats/"+scenarioPanelSID+"/ask", "")
	var wire struct {
		Ask agentAsk `json:"ask"`
	}
	if err := json.Unmarshal([]byte(body(t, r)), &wire); err != nil {
		t.Fatal(err)
	}
	return wire.Ask
}

func (st *scenarioStand) send(t *testing.T, text string, ask agentAsk, want int) string {
	t.Helper()
	data, _ := json.Marshal(map[string]any{"text": text, "scenario": scenarioAnswer{Task: "XR-4", Tree: ask.Tree, Fork: ask.Fork, Snapshot: ask.Scenario.Snapshot}})
	r := doReq(t, st.c, "POST", st.e.srv.URL+"/api/projects/demo/chats/"+scenarioPanelSID+"/say", string(data))
	out := body(t, r)
	if r.StatusCode != want {
		t.Fatalf("ответ %q: %d, ждали %d\n%s", text, r.StatusCode, want, out)
	}
	return out
}

func TestScenarioPanelCLI(t *testing.T) {
	st := newScenarioStand(t)
	ask := st.ask(t)
	if !ask.Multi || len(ask.Scenario.IDs) != 3 || !strings.Contains(ask.Scenario.Block, "Источник: пример") {
		t.Fatalf("потеря договора Go: %+v", ask)
	}
	askPath := chat.AskPath(st.e.proj, chat.TaskName("XR-4"))
	before, waiting := readFile(t, st.path), readFile(t, askPath)
	for _, text := range []string{"", "сценарии 9"} {
		st.send(t, text, ask, http.StatusBadRequest)
		if readFile(t, st.path) != before || readFile(t, askPath) != waiting {
			t.Fatal("ошибка изменила запись или ожидание")
		}
	}
	raw := "  сценарии СЦ-1, СЦ-3; плюс \"свой, с \\\"кавычкой\\\" и \\\\ путём\"  "
	st.send(t, raw, ask, http.StatusOK)
	f, _ := taskform.FindFork(readFile(t, st.path), "сценарии")
	replies, _ := f.ScenarioReplies()
	if f.Answer != "СЦ-1, СЦ-3, СЦ-4" || len(replies) != 1 || replies[0].Text != raw {
		t.Fatalf("ID/raw: %+v %+v", f, replies)
	}
	if _, ok := chat.ReadAsk(askPath); ok {
		t.Fatal("принятая мутация сохранила ожидание")
	}
	if err := os.WriteFile(askPath, []byte(waiting), 0o644); err != nil {
		t.Fatal(err)
	}
	before = readFile(t, st.path)
	st.send(t, raw, ask, http.StatusOK)
	if readFile(t, st.path) != before || readFile(t, askPath) != waiting {
		t.Fatal("точный повтор изменил запись/ожидание")
	}
	st.send(t, "сценарии 2", ask, http.StatusBadRequest)
	if readFile(t, st.path) != before || readFile(t, askPath) != waiting {
		t.Fatal("устаревание изменило запись/ожидание")
	}
}

func TestScenarioPanelOrdinaryDoesNotChooseScenarios(t *testing.T) {
	st := newScenarioStand(t)
	st.run(t, "--ask", "срок", "--hint", "30 дней: достаточно", "сколько хранить?")
	ask := st.publish(t, false)
	st.send(t, "срок 1", ask, http.StatusOK)
	f, _ := taskform.FindFork(readFile(t, st.path), "сценарии")
	ordinary, _ := taskform.FindFork(readFile(t, st.path), "срок")
	if f.ChoiceEvent != 0 || f.Reconsidered || !f.HoldsStart() || ordinary.Answer != "30 дней: достаточно" {
		t.Fatalf("обычный ответ изменил сценарии: %+v %+v", f, ordinary)
	}
}

func TestScenarioPanelRetryKeepsHandedRoute(t *testing.T) {
	st := newScenarioStand(t)
	ask := st.ask(t)
	writeBinds(t, st.e.home, "2026-10-28T12:00:01 сессия "+scenarioPanelSID+" задача XR-100 проект demo дерево "+st.e.proj+" транскрипт "+standTranscript(st.e.home, "t")+" источник заказ повод startup tmux -\n")
	before := readFile(t, st.path)
	bad := ask
	bad.Tree = t.TempDir()
	st.send(t, "сценарии 3", bad, http.StatusBadRequest)
	if readFile(t, st.path) != before {
		t.Fatal("постороннее дерево изменило запись")
	}
	lock, err := chat.Lock(filepath.Join(chat.Root(st.e.proj), chat.TaskName("XR-4")+chat.LockSuffix))
	if err != nil {
		t.Fatal(err)
	}
	st.send(t, "сценарии 3", ask, http.StatusBadGateway)
	lock.Close()
	before = readFile(t, st.path)
	if _, ok := chat.ReadAsk(chat.AskPath(st.e.proj, chat.TaskName("XR-4"))); ok {
		t.Fatal("CLI не применил ответ до сбоя доставки")
	}
	st.send(t, "сценарии 3", ask, http.StatusOK)
	if readFile(t, st.path) != before {
		t.Fatal("повтор после доставки изменил выбор")
	}
	lines := chatLines(t, st.e.proj, chat.TaskName("XR-4"))
	if len(lines) != 1 || chat.Addressee(lines[0]) != "" || !strings.Contains(chat.Said(lines[0]), "выбрано: СЦ-3") {
		t.Fatalf("розданный маршрут потерян: %q", lines)
	}
}

func TestScenarioPanelReorderAndResume(t *testing.T) {
	st := newScenarioStand(t)
	ask := st.ask(t)
	waiting := readFile(t, chat.AskPath(st.e.proj, chat.TaskName("XR-4")))
	doc := readFile(t, st.path)
	doc = strings.Replace(doc, "вариант: СЦ-1 (слово пользователя): выбрать", "вариант: СЦ-9 (слово пользователя): временно", 1)
	doc = strings.Replace(doc, "вариант: СЦ-2 (вывод из примера): собрать", "вариант: СЦ-1 (слово пользователя): выбрать", 1)
	doc = strings.Replace(doc, "вариант: СЦ-9 (слово пользователя): временно", "вариант: СЦ-2 (вывод из примера): собрать", 1)
	if err := os.WriteFile(st.path, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	out := st.send(t, "сценарии 1", ask, http.StatusBadRequest)
	if !strings.Contains(out, "прежние позиции") || !strings.Contains(out, "нынешние позиции") || readFile(t, st.path) != doc || readFile(t, chat.AskPath(st.e.proj, chat.TaskName("XR-4"))) != waiting {
		t.Fatal("устаревание не наблюдается или изменило файл: " + out)
	}
	fresh := st.ask(t)
	st.send(t, "сценарии 1", fresh, http.StatusOK)
	st.run(t, "--ask", "срок", "--hint", "30 дней: достаточно", "сколько хранить?")
	st.run(t, "срок", "--by", "человек", "30 дней")
	doc = readFile(t, st.path) + "  - требует сверки 2026-10-28: проверить после состава\n"
	if err := os.WriteFile(st.path, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	r := doReq(t, st.c, "GET", st.e.srv.URL+"/api/projects/demo/chats/"+scenarioPanelSID+"/ask", "")
	out = body(t, r)
	for _, word := range []string{"выбрано: СЦ-2", "требует сверки", "30 дней", "проверить после состава"} {
		if !strings.Contains(out, word) {
			t.Fatalf("продолжение без %s: %s", word, out)
		}
	}
	fresh = st.ask(t)
	st.send(t, "ничего", fresh, http.StatusOK)
	f, _ := taskform.FindFork(readFile(t, st.path), "сценарии")
	if !f.Reconsidered || !f.HoldsStart() {
		t.Fatalf("отказ закрыл вопрос: %+v", f)
	}
}

func TestScenarioPanelRemovedModeRefusesSnapshot(t *testing.T) {
	st := newScenarioStand(t)
	ask := st.ask(t)
	doc := strings.Replace(readFile(t, st.path), "  - выбор: сценарии/1\n", "", 1)
	if err := os.WriteFile(st.path, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	waiting := readFile(t, chat.AskPath(st.e.proj, chat.TaskName("XR-4")))
	st.send(t, "сценарии 1", ask, http.StatusBadRequest)
	if readFile(t, st.path) != doc || readFile(t, chat.AskPath(st.e.proj, chat.TaskName("XR-4"))) != waiting {
		t.Fatal("старый снимок применён к обычному вопросу")
	}
}

func TestScenarioPanelWorktreeSource(t *testing.T) {
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
	tree := filepath.Join(filepath.Dir(st.e.proj), "demo-branch")
	git("worktree", "add", "-b", "stand", tree)
	other := filepath.Join(filepath.Dir(st.e.proj), "other")
	mkProject(t, other)
	before := readFile(t, st.path)
	linked := *st.e
	linked.proj = tree
	work := *st
	work.e = &linked
	work.path = filepath.Join(tree, "docs/tasks/drafts/XR-4.md")
	ask := work.ask(t)
	if ask.Tree != tree || ask.Scenario.Tree != tree {
		t.Fatalf("source worktree потерян: %+v", ask)
	}
	// Разговор передан из другого проекта, у него своя задача и своё дерево.
	writeSession(t, st.e.home, other, "", scenarioPanelSID, plainTalk, time.Now())
	writeBinds(t, st.e.home, "2026-10-28T12:00:01 сессия "+scenarioPanelSID+" задача XR-100 проект other дерево "+other+" транскрипт "+standTranscript(st.e.home, "t")+" источник заказ повод startup tmux -\n")
	data, _ := json.Marshal(map[string]any{"text": "сценарии 3", "scenario": scenarioAnswer{Task: "XR-4", Tree: ask.Tree, Fork: ask.Fork, Snapshot: ask.Scenario.Snapshot}})
	post := func() {
		r := doReq(t, st.c, "POST", st.e.srv.URL+"/api/projects/other/chats/"+scenarioPanelSID+"/say", string(data))
		out := body(t, r)
		if r.StatusCode != http.StatusOK {
			t.Fatalf("foreign worktree reply: %d %s", r.StatusCode, out)
		}
	}
	post()
	f, _ := taskform.FindFork(readFile(t, work.path), "сценарии")
	if f.Answer != "СЦ-3" || readFile(t, st.path) != before {
		t.Fatalf("ответ изменил primary вместо worktree: %+v", f)
	}
	after := readFile(t, work.path)
	post()
	if readFile(t, work.path) != after || readFile(t, st.path) != before {
		t.Fatal("retry изменил primary/worktree")
	}
	lines := chatLines(t, tree, chat.TaskName("XR-4"))
	if len(lines) != 1 || chat.Addressee(lines[0]) != "" {
		t.Fatalf("foreign worktree доставка: %q", lines)
	}
	newSID := "aa144601-1111-4111-8111-111111111111"
	writeSession(t, st.e.home, st.e.proj, "branch", newSID, plainTalk, time.Now())
	writeBinds(t, st.e.home, "2026-10-28T12:00:02 сессия "+newSID+" задача XR-4 проект demo дерево "+tree+" транскрипт "+standTranscript(st.e.home, "t")+" источник заказ повод startup tmux -\n")
	r := doReq(t, st.c, "GET", st.e.srv.URL+"/api/projects/demo/chats/"+newSID+"/ask", "")
	if out := body(t, r); !strings.Contains(out, "выбрано: СЦ-3") {
		t.Fatal("продолжение читает primary: " + out)
	}
}

func TestScenarioPanelGoJSONToJS(t *testing.T) {
	for _, mixed := range []bool{false, true} {
		t.Run(map[bool]string{false: "single", true: "mixed"}[mixed], func(t *testing.T) {
			st := newScenarioStand(t)
			if mixed {
				st.run(t, "--ask", "срок", "--hint", "30 дней: достаточно", "--option", "7 дней", "сколько хранить?")
			}
			ask := st.publish(t, !mixed)
			data, _ := json.Marshal(ask)
			wire := filepath.Join(t.TempDir(), "ask.json")
			if err := os.WriteFile(wire, data, 0o644); err != nil {
				t.Fatal(err)
			}
			node, err := exec.LookPath("node")
			if err != nil {
				t.Fatal("node обязателен для проверки договора Go/JS")
			}
			out, err := exec.Command(node, "testdata/poc_scenariopick.mjs", "static/app.js", wire).CombinedOutput()
			if err != nil {
				t.Fatalf("JS по реальному JSON: %v\n%s", err, out)
			}
			var answer struct {
				Text     string         `json:"text"`
				Scenario scenarioAnswer `json:"scenario"`
			}
			if err := json.Unmarshal(out, &answer); err != nil {
				t.Fatalf("ответ JS: %s: %v", out, err)
			}
			if mixed {
				before, wait := readFile(t, st.path), readFile(t, chat.AskPath(st.e.proj, chat.TaskName("XR-4")))
				st.send(t, strings.Replace(answer.Text, "срок 1", "срок 99", 1), ask, http.StatusBadRequest)
				if readFile(t, st.path) != before || readFile(t, chat.AskPath(st.e.proj, chat.TaskName("XR-4"))) != wait {
					t.Fatal("плохой mixed ответ записан частично")
				}
			}
			st.send(t, answer.Text, ask, http.StatusOK)
			f, _ := taskform.FindFork(readFile(t, st.path), "сценарии")
			replies, _ := f.ScenarioReplies()
			if f.Answer != "СЦ-3, СЦ-4" || len(replies) != 1 || replies[0].Text != answer.Text {
				t.Fatalf("JS -> CLI потерял выбор/raw: %+v %+v", f, replies)
			}
			if mixed {
				ordinary, _ := taskform.FindFork(readFile(t, st.path), "срок")
				if ordinary.Answer != "30 дней: достаточно" {
					t.Fatalf("обычный вопрос не решён: %+v", ordinary)
				}
			}
			before := readFile(t, st.path)
			st.send(t, answer.Text, ask, http.StatusOK)
			if readFile(t, st.path) != before {
				t.Fatal("mixed/single retry создал новое событие")
			}
		})
	}
}
