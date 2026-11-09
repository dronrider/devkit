package main

import (
	"bytes"
	"encoding/json"
	"os"
	"reflect"
	"testing"

	"github.com/dronrider/devkit/internal/chat"
	"github.com/dronrider/devkit/internal/draftproposal"
)

func savedPrePatchAsk(t *testing.T, kind string) (string, preparedChatAnswer, []byte) {
	t.Helper()
	var root string
	var answer preparedChatAnswer
	conclusion := "- Удалить материал. Уже не нужно"
	if kind == "add-adopt" {
		root, answer, _ = legacyAskForAdoption(t, false)
		conclusion = "Предлагаю создать подготовленные задачи."
	} else {
		var view preparedView
		root, view = conclusionFixture(t)
		authored := ""
		if kind == "authored-drop" {
			authored = "Предлагаю удалить лишнее."
			conclusion = authored + "\n\n" + conclusion
		}
		input, _ := json.Marshal(map[string]any{"complete": true, "conclusion": authored, "proposals": []draftproposal.Proposal{{ID: "p1", Revision: 1, Outcome: "drop", Reason: "Уже не нужно"}}})
		raw, err := cmdDraftPreparedSave(root, "XR-008", view.Snapshot, input)
		if err != nil {
			t.Fatal(err)
		}
		if err = json.Unmarshal([]byte(raw), &view); err != nil {
			t.Fatal(err)
		}
		if _, err = cmdPreparedChatAsk(root, "XR-008", nil, askDeps{Main: root}, func(string) string { return "" }); err != nil {
			t.Fatal(err)
		}
		if err = json.Unmarshal(conclusionRequest(view), &answer); err != nil {
			t.Fatal(err)
		}
		answer.Tree = root
	}
	askPath := chat.AskPath(root, chat.TaskName("XR-008"))
	ask, ok := chat.ReadAsk(askPath)
	if !ok {
		t.Fatal("сохранённый Ask отсутствует")
	}
	shown := ask.Questions[0].Prepared
	shown.Sources[0].Conclusion = conclusion
	ask.Questions[0].Text, ask.Questions[0].Options = preparedQuestionShape(root, *shown)
	if err := chat.WriteAsk(root, chat.TaskName("XR-008"), ask); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(askPath)
	if err != nil {
		t.Fatal(err)
	}
	return root, answer, before
}

func TestConclusionSavedPrePatchAskAndReplay(t *testing.T) {
	for _, kind := range []string{"drop", "authored-drop", "add-adopt"} {
		t.Run(kind, func(t *testing.T) {
			root, answer, before := savedPrePatchAsk(t, kind)
			if kind == "add-adopt" {
				if _, err := cmdDraftPreparedAdopt(root, []string{"XR-008"}); err != nil {
					t.Fatal(err)
				}
			}
			input, _ := json.Marshal(answer)
			req, _ := json.Marshal(draftproposal.ConfirmRequest{Snapshots: answer.Snapshots, Decisions: answer.Decisions})
			var receipts [2]struct {
				Key      string                  `json:"key"`
				Repeated bool                    `json:"repeated"`
				Results  []draftproposal.Receipt `json:"results"`
			}
			for i := range receipts {
				raw, err := cmdPreparedChatAnswer(root, "XR-008", input, CommitOpts{})
				if err != nil {
					t.Fatalf("прежнее согласие, вызов %d: %v", i+1, err)
				}
				if err = json.Unmarshal([]byte(raw), &receipts[i]); err != nil {
					t.Fatal(err)
				}
				if i == 0 {
					if _, ok := chat.ReadAsk(chat.AskPath(root, chat.TaskName("XR-008"))); ok {
						t.Fatal("согласие оставило сохранённый Ask")
					}
					if err = os.WriteFile(chat.AskPath(root, chat.TaskName("XR-008")), before, 0644); err != nil {
						t.Fatal(err)
					}
					continue
				}
				after, err := os.ReadFile(chat.AskPath(root, chat.TaskName("XR-008")))
				if err != nil || !bytes.Equal(before, after) {
					t.Fatal("повтор переписал сохранённый Ask")
				}
			}
			if receipts[0].Repeated || !receipts[1].Repeated || receipts[0].Key != draftproposal.Digest(req) || receipts[0].Key != receipts[1].Key || !reflect.DeepEqual(receipts[0].Results, receipts[1].Results) || len(receipts[0].Results) != 1 {
				t.Fatalf("согласие или повтор изменили квитанцию: %+v", receipts)
			}
			_, state, err := readPrepared(root, "XR-008")
			if err != nil || state.State != "history" {
				t.Fatalf("исходник не завершён: %+v %v", state, err)
			}
		})
	}
}

func TestConclusionSavedPrePatchAskRejectsChangedText(t *testing.T) {
	for _, which := range []string{"stored-conclusion", "shown-conclusion"} {
		t.Run(which, func(t *testing.T) {
			root, answer, _ := savedPrePatchAsk(t, "authored-drop")
			if which == "stored-conclusion" {
				_, state, err := readPrepared(root, "XR-008")
				if err != nil {
					t.Fatal(err)
				}
				state.Conclusion = "Предлагаю другое обоснование."
				if err = draftproposal.SavePrepared(root, "XR-008", state); err != nil {
					t.Fatal(err)
				}
			} else {
				ask, ok := chat.ReadAsk(chat.AskPath(root, chat.TaskName("XR-008")))
				if !ok {
					t.Fatal("Ask отсутствует")
				}
				ask.Questions[0].Prepared.Sources[0].Conclusion = "Подменённое заключение"
				if err := chat.WriteAsk(root, chat.TaskName("XR-008"), ask); err != nil {
					t.Fatal(err)
				}
			}
			before, _ := collectDocs(root)
			input, _ := json.Marshal(answer)
			if _, err := cmdPreparedChatAnswer(root, "XR-008", input, CommitOpts{}); err == nil {
				t.Fatal("изменённое заключение применено")
			}
			after, _ := collectDocs(root)
			if !reflect.DeepEqual(before, after) {
				t.Fatal("отказ изменил файлы")
			}
		})
	}
}
