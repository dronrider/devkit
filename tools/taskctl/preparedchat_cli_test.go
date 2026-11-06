package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/dronrider/devkit/internal/chat"
	"github.com/dronrider/devkit/internal/draftproposal"
)

func TestPreparedChatActualCLI(t *testing.T) {
	root := setup(t)
	kit, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEVKIT_HOME", kit)
	issued := map[string]string{}
	revisions := map[string]int{}
	var first, second string
	for i, title := range []string{"материал один", "материал два"} {
		msg, err := cmdDraft(root, title, "mid", CommitOpts{})
		if err != nil {
			t.Fatal(err)
		}
		id := draftIDFromMsg(msg)
		if i == 0 {
			first = id
		} else {
			second = id
		}
		raw, err := cmdDraftPrepared(root, id)
		if err != nil {
			t.Fatal(err)
		}
		var view preparedView
		if err = json.Unmarshal([]byte(raw), &view); err != nil {
			t.Fatal(err)
		}
		cards := []draftproposal.Proposal{{ID: "p1", Revision: 1, Outcome: "drop", Text: "частная мысль", Reason: "лишнее"}}
		if i == 0 {
			cards[0] = draftproposal.Proposal{ID: "p1", Revision: 1, Outcome: "add", Title: "Первый результат", Text: fixtureTaskBody, BoardType: "task", Rank: "0+1+1+0+1", Size: "M", Accept: "agent"}
			cards = append(cards, draftproposal.Proposal{ID: "p2", Revision: 1, Outcome: "add", Title: "Результат", Text: fixtureTaskBody, BoardType: "task", Rank: "0+1+1+0+1", Size: "M", Accept: "agent", After: []string{"p1"}})
		}
		save, _ := json.Marshal(struct {
			Proposals []draftproposal.Proposal `json:"proposals"`
			Complete  bool                     `json:"complete"`
		}{cards, true})
		raw, err = cmdDraftPreparedSave(root, id, view.Snapshot, save)
		if err != nil {
			t.Fatal(err)
		}
		if err = json.Unmarshal([]byte(raw), &view); err != nil {
			t.Fatal(err)
		}
		revisions[id] = view.State.PackRevision
		for sent, full := range view.Assigned {
			issued[id+"/"+sent] = full
		}
	}
	bin := os.Getenv("TASKCTL_ACCEPT_BIN")
	if bin == "" {
		bin = filepath.Join(t.TempDir(), "taskctl")
		build := exec.Command("go", "build", "-o", bin, ".")
		build.Env = append(os.Environ(), "GOWORK=off")
		if out, err := build.CombinedOutput(); err != nil {
			t.Fatalf("build: %v %s", err, out)
		}
	}
	run := func(input []byte, args ...string) (string, error) {
		t.Helper()
		cmd := exec.Command(bin, append([]string{"-C", root}, args...)...)
		cmd.Env = append(os.Environ(), "CLAUDE_CODE_SESSION_ID=cli-stand", "DEVKIT_TMUX=task-stand")
		cmd.Stdin = bytes.NewReader(input)
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	secondPath := draftPath(root, second)
	public, err := os.ReadFile(secondPath)
	if err != nil {
		t.Fatal(err)
	}
	protected := strings.Replace(string(public), "\n", "\nзамок: locked\n", 1)
	if err = os.WriteFile(secondPath, []byte(protected), 0600); err != nil {
		t.Fatal(err)
	}
	if leaked, err := run(nil, "draft", "prepared", "ask", first, "--also", second); err == nil || strings.Contains(leaked, second+"/prepared") {
		t.Fatalf("locked shown source reached agent: %s %v", leaked, err)
	}
	if _, ok := chat.ReadAsk(chat.AskPath(root, chat.TaskName(first))); ok {
		t.Fatal("private question was published")
	}
	if err = os.WriteFile(secondPath, public, 0600); err != nil {
		t.Fatal(err)
	}
	out, err := run(nil, "draft", "prepared", "ask", first, "--also", second)
	if err != nil {
		t.Fatalf("ask: %s %v", out, err)
	}
	var q chat.Question
	if err = json.Unmarshal([]byte(out), &q); err != nil || q.Prepared == nil || len(q.Prepared.Sources) != 2 {
		t.Fatalf("question: %s %v", out, err)
	}
	if len(q.Options) != len(q.Prepared.Sources) {
		t.Fatalf("options are not one per source: %+v", q.Options)
	}
	for i, source := range q.Prepared.Sources {
		if q.Options[i].Label != "Подтвердить разбор "+source.Source || source.Revision < 1 || source.Revision != revisions[source.Source] {
			t.Fatalf("generated source confirmation: %+v %+v", source, q.Options[i])
		}
	}
	if !strings.Contains(q.Text, "Подготовил задачи, согласуй их описание.") ||
		!strings.Contains(q.Text, "#"+filepath.Base(root)+"/draft/"+first+"/prepared/"+issued[first+"/p2"]) ||
		!strings.Contains(q.Text, "#"+filepath.Base(root)+"/draft/"+second+"/prepared/"+issued[second+"/p1"]) {
		t.Fatalf("вопрос без ссылок на задачи: %s", q.Text)
	}
	if strings.Contains(out, "Тестовый запуск обнаружил пропуск") || strings.Contains(out, "частная мысль") {
		t.Fatalf("тела задач уехали в вопрос: %s", out)
	}
	dropOut, err := run(nil, "draft", "prepared", "ask", second)
	if err != nil {
		t.Fatalf("ask без add: %s %v", dropOut, err)
	}
	var dropOnly chat.Question
	if err = json.Unmarshal([]byte(dropOut), &dropOnly); err != nil || dropOnly.Prepared == nil {
		t.Fatalf("question без add: %s %v", dropOut, err)
	}
	if !strings.Contains(dropOnly.Text, "Подготовил задачи, согласуй их описание.") || !strings.Contains(dropOnly.Text, "/draft/"+second+"/prepared/"+issued[second+"/p1"]) {
		t.Fatalf("вопрос без задачи add собран не тем текстом: %s", dropOnly.Text)
	}
	t.Setenv("DEVKIT_GROOM_SOURCE", first)
	t.Setenv("DEVKIT_GROOM_MODE", "run")
	if denied, err := run(nil, "run", "XR-001"); err == nil || !strings.Contains(denied, "только подтверждённым результатам add") {
		t.Fatalf("run before confirmation: %s %v", denied, err)
	}
	t.Setenv("DEVKIT_GROOM_SOURCE", "")
	t.Setenv("DEVKIT_GROOM_MODE", "")
	a := preparedChatAnswer{Tree: root}
	for _, source := range q.Prepared.Sources {
		a.Snapshots = append(a.Snapshots, draftproposal.Snapshot{Source: source.Source, Revision: source.Revision, Digest: source.Basis})
		for _, p := range source.Proposals {
			a.Decisions = append(a.Decisions, draftproposal.Decision{Source: source.Source, Proposal: p.ID, Revision: p.Revision, Action: "accept"})
		}
	}
	firstOnly := draftproposal.ConfirmRequest{Snapshots: a.Snapshots[:1], Decisions: a.Decisions[:len(q.Prepared.Sources[0].Proposals)]}
	if err := chat.CheckPreparedAnswer(*q.Prepared, root, firstOnly); err != nil {
		t.Fatalf("whole first source: %v", err)
	}
	bad := a
	bad.Snapshots = a.Snapshots[:1]
	bad.Decisions = []draftproposal.Decision{{Source: first, Proposal: issued["XR-008/p2"], Revision: 1, Action: "accept"}}
	badInput, _ := json.Marshal(bad)
	before, err := os.ReadFile(draftPath(root, first))
	if err != nil {
		t.Fatal(err)
	}
	if got, err := run(badInput, "draft", "prepared", "answer", first); err == nil {
		t.Fatalf("partial source accepted: %s", got)
	}
	after, err := os.ReadFile(draftPath(root, first))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("partial source changed source")
	}
	bad = a
	bad.Tree = t.TempDir()
	badInput, _ = json.Marshal(bad)
	if got, err := run(badInput, "draft", "prepared", "answer", first); err == nil {
		t.Fatalf("wrong tree accepted: %s", got)
	}
	after, err = os.ReadFile(draftPath(root, first))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("wrong tree changed source")
	}
	input, _ := json.Marshal(a)
	readme, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatal(err)
	}
	var jqFilter string
	for _, line := range strings.Split(string(readme), "\n") {
		if !strings.HasPrefix(line, "jq '") || !strings.Contains(line, "/tmp/prepared-question.json") {
			continue
		}
		jqFilter, _, _ = strings.Cut(strings.TrimPrefix(line, "jq '"), "' /tmp/prepared-question.json")
		break
	}
	if jqFilter == "" {
		t.Fatal("README has no prepared answer jq example")
	}
	jq := exec.Command("jq", jqFilter)
	jq.Stdin = strings.NewReader(out)
	docInput, err := jq.Output()
	if err != nil {
		t.Fatalf("README jq: %v", err)
	}
	var fromDocs preparedChatAnswer
	if err := json.Unmarshal(docInput, &fromDocs); err != nil || fromDocs.Tree != a.Tree || !reflect.DeepEqual(fromDocs.Decisions, a.Decisions) || !reflect.DeepEqual(fromDocs.Snapshots, a.Snapshots) {
		t.Fatalf("README answer: %s %v", docInput, err)
	}
	out, err = run(input, "draft", "prepared", "answer", first)
	if err != nil || !strings.Contains(out, `"repeated":false`) {
		t.Fatalf("answer %s %v", out, err)
	}
	for _, source := range q.Prepared.Sources {
		current, err := run(nil, "draft", "prepared", source.Source)
		if err != nil {
			t.Fatal(current, err)
		}
		var view preparedView
		if err = json.Unmarshal([]byte(current), &view); err != nil || view.State.State != "history" || len(view.State.Receipts) != len(source.Proposals)+1 {
			t.Fatalf("whole generated ask did not enter history: %s %v", current, err)
		}
		for _, p := range view.State.Proposals {
			if p.Status != "accepted" || p.Result == nil {
				t.Fatalf("unconfirmed task: %+v", p)
			}
		}
	}
	if err = chat.DropAsk(root, chat.TaskName(first)); err != nil {
		t.Fatal(err)
	}
	out, err = run(input, "draft", "prepared", "answer", "--repeat-only", first)
	if err != nil || !strings.Contains(out, `"repeated":true`) {
		t.Fatalf("replay %s %v", out, err)
	}
	a.Decisions[0].Reason = "новая причина"
	input, _ = json.Marshal(a)
	if out, err = run(input, "draft", "prepared", "answer", first, "--repeat-only"); err == nil {
		t.Fatalf("new mutation without Ask: %s", out)
	}
}
