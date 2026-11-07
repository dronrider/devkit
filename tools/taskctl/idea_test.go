package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/dronrider/devkit/internal/draftproposal"
	"strings"
	"testing"
)

func TestDraftIdeaWritesDatedItem(t *testing.T) {
	root := setup(t)
	id := newDraft(t, root, "материал записи")

	msg, err := cmdDraftIdea(root, id, "кнопка была бы удобнее слева", CommitOpts{})
	if err != nil || !strings.Contains(msg, "«Идеи»") {
		t.Fatalf("сообщение команды: %q %v", msg, err)
	}
	body := draftBody(t, root, id)
	want := fmt.Sprintf("## Идеи\n\n- %s: кнопка была бы удобнее слева\n", today())
	if !strings.Contains(body, want) {
		t.Fatalf("файл черновика:\n%s\nжду пункт:\n%s", body, want)
	}
	if !strings.Contains(body, "материал записи") {
		t.Fatalf("мысль съела материал записи:\n%s", body)
	}

	if _, err = cmdDraftIdea(root, id, "вторая мысль про размер", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	body = draftBody(t, root, id)
	first := strings.Index(body, "кнопка была бы удобнее слева")
	second := strings.Index(body, "вторая мысль про размер")
	if strings.Count(body, draftIdeasHeading) != 1 || first < 0 || second < 0 || second < first {
		t.Fatalf("вторая мысль легла мимо раздела:\n%s", body)
	}

	if _, err = cmdDraftIdea(root, id, "   ", CommitOpts{}); err == nil {
		t.Fatal("пустая мысль должна отказывать")
	}
	if _, err = cmdDraftIdea(root, "XR-404", "мысль", CommitOpts{}); err == nil {
		t.Fatal("мысль по чужому ID должна отказывать")
	}
}

// TestDraftIdeaStandsBeforeGrooming: раздел идей встаёт перед разделом
// «Грумминг», а не после него, чтобы журнал откладывания оставался хвостом.
func TestDraftIdeaStandsBeforeGrooming(t *testing.T) {
	root := setup(t)
	id := newDraft(t, root, "запись с пометкой")
	if _, err := cmdDraftDefer(root, id, "ждём релиза смежника", "", false, CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	writeSavedDraftIdea(t, root, id, "вернуть старый фильтр")
	body := draftBody(t, root, id)
	ideas := strings.Index(body, draftIdeasHeading)
	groom := strings.Index(body, draftGroomHeading)
	if ideas < 0 || groom < 0 || ideas > groom {
		t.Fatalf("раздел идей лёг не перед грумингом:\n%s", body)
	}
}

func writeSavedDraftIdea(t *testing.T, root, id, text string) {
	t.Helper()
	doc, err := loadDraftText(draftPath(root, id))
	if err != nil {
		t.Fatal(err)
	}
	doc.addIdea("- " + today() + ": " + text)
	if err := doc.save(); err != nil {
		t.Fatal(err)
	}
}

func TestDraftIdeaOnlyBeforeGroomResult(t *testing.T) {
	for _, tc := range []struct {
		name    string
		state   draftproposal.State
		legacy  string
		allowed bool
	}{
		{name: "raw", allowed: true},
		{name: "incomplete", state: draftproposal.State{Version: 1, State: "active", PackRevision: 1}, allowed: true},
		{name: "first-error", state: draftproposal.State{Version: 1, State: "active", Analysis: "error"}, allowed: true},
		{name: "empty", state: draftproposal.State{Version: 1, State: "active", Analysis: "empty"}},
		{name: "conclusion", state: draftproposal.State{Version: 1, State: "active", Conclusion: "Сохранённый результат"}},
		{name: "proposals", state: draftproposal.State{Version: 1, State: "active", Proposals: []draftproposal.Proposal{{ID: "p", Revision: 1, Outcome: "drop", Reason: "лишнее"}}}},
		{name: "retry-error", state: draftproposal.State{Version: 1, State: "active", Analysis: "error", Conclusion: "Сохранённый результат"}},
		{name: "legacy", legacy: "\n## Грумминг\n\nРазобрано прежде.\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := setup(t)
			id := newDraft(t, root, "Материал записи")
			if _, err := cmdDraftIdea(root, id, "Ранее записанное замечание", CommitOpts{}); err != nil {
				t.Fatal(err)
			}
			if tc.state.Version != 0 {
				if err := draftproposal.SavePrepared(root, id, tc.state); err != nil {
					t.Fatal(err)
				}
			}
			if tc.legacy != "" {
				if err := os.WriteFile(draftPath(root, id), []byte(draftBody(t, root, id)+tc.legacy), 0644); err != nil {
					t.Fatal(err)
				}
			}
			before := draftBody(t, root, id)
			msg, err := cmdDraftIdea(root, id, "Замечание второго пользователя", CommitOpts{})
			if tc.allowed {
				if err != nil || !strings.Contains(draftBody(t, root, id), "Замечание второго пользователя") {
					t.Fatalf("append: %s %v", msg, err)
				}
			} else {
				if err == nil || !strings.Contains(err.Error(), "Чате разбора") {
					t.Fatalf("late thought accepted: %s %v", msg, err)
				}
				if draftBody(t, root, id) != before {
					t.Fatal("rejected thought changed source")
				}
			}
			if !strings.Contains(draftBody(t, root, id), "Ранее записанное замечание") {
				t.Fatal("previous thought lost")
			}
		})
	}
}

func TestDraftIdeaBeforeGroomActualCLI(t *testing.T) {
	bin := os.Getenv("TASKCTL_ACCEPT_BIN")
	if bin == "" {
		bin = buildTaskctl(t)
	}
	if !filepath.IsAbs(bin) {
		t.Fatal("TASKCTL_ACCEPT_BIN должен быть абсолютным путём")
	}
	root := setup(t)
	id := newDraft(t, root, "Исходник разных пользователей")
	run := func(text string) (string, error) {
		cmd := exec.Command(bin, "-C", root, "draft", "idea", id, text)
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	for _, text := range []string{"Первое замечание", "Второе замечание"} {
		if out, err := run(text); err != nil {
			t.Fatalf("raw append: %s %v", out, err)
		}
	}
	if err := draftproposal.SavePrepared(root, id, draftproposal.State{Version: 1, State: "active", Conclusion: "Сохранённый результат"}); err != nil {
		t.Fatal(err)
	}
	before := draftBody(t, root, id)
	out, err := run("Позднее замечание")
	if err == nil || !strings.Contains(out, "Чате разбора") {
		t.Fatalf("late append: %s %v", out, err)
	}
	if draftBody(t, root, id) != before {
		t.Fatal("CLI rejection changed source")
	}
}
