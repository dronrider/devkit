package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/dronrider/devkit/internal/chat"
	"github.com/dronrider/devkit/internal/draftproposal"
	"github.com/dronrider/devkit/internal/drafttx"
)

func legacyAskForAdoption(t *testing.T, target bool) (string, preparedChatAnswer, []byte) {
	t.Helper()
	return legacyAskForAdoptionKey(t, target, "old-short-key")
}

func legacyAskForAdoptionKey(t *testing.T, target bool, key string) (string, preparedChatAnswer, []byte) {
	t.Helper()
	root := setup(t)
	kit, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEVKIT_HOME", kit)
	if _, err = cmdDraft(root, "legacy shown material", "mid", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	card := draftproposal.Proposal{ID: key, Revision: 1, Outcome: "drop", Text: "Убрать устаревший индикатор", Reason: "лишнее"}
	if target {
		card.Outcome = "attach"
		card.Target = "XR-001"
	}
	state := draftproposal.State{Version: 1, State: "active", Proposals: []draftproposal.Proposal{card}}
	if err = draftproposal.SavePrepared(root, "XR-008", readyLegacyPrepared(t, root, "XR-008", state)); err != nil {
		t.Fatal(err)
	}
	printed, err := cmdPreparedChatAsk(root, "XR-008", nil, askDeps{Main: root}, func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	var q chat.Question
	if err = json.Unmarshal([]byte(printed), &q); err != nil {
		t.Fatal(err)
	}
	source := q.Prepared.Sources[0]
	answer := preparedChatAnswer{Tree: root, Snapshots: []draftproposal.Snapshot{{Source: "XR-008", Revision: source.Revision, Digest: source.Basis}}, Decisions: []draftproposal.Decision{{Source: "XR-008", Proposal: key, Revision: 1, Action: "accept"}}}
	ask, err := os.ReadFile(chat.AskPath(root, chat.TaskName("XR-008")))
	if err != nil {
		t.Fatal(err)
	}
	return root, answer, ask
}

func TestLegacyAskSurvivesNumberAdoptionAndReplay(t *testing.T) {
	for _, key := range []string{"old-short-key", "XR-900"} {
		t.Run(key, func(t *testing.T) {
			testLegacyAskAdoptionReplay(t, key)
		})
	}
}

func testLegacyAskAdoptionReplay(t *testing.T, proposalKey string) {
	t.Helper()
	root, answer, ask := legacyAskForAdoptionKey(t, false, proposalKey)
	if _, err := cmdDraftPreparedAdopt(root, []string{"XR-008"}); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(chat.AskPath(root, chat.TaskName("XR-008")))
	if err != nil || !bytes.Equal(ask, after) {
		t.Fatal("закрепление переписало показанный вопрос")
	}
	_, state, err := readPrepared(root, "XR-008")
	if err != nil {
		t.Fatal(err)
	}
	number := state.Proposals[0].BoardID
	if !idRe.MatchString(number) {
		t.Fatal("номер не закреплён")
	}
	free, err := cmdID(root)
	if err != nil || free == number || !idRe.MatchString(free) {
		t.Fatalf("реестр переходов помешал выдаче номера: %s %v", free, err)
	}
	if _, err = cmdDraftPreparedAdopt(root, []string{"XR-008"}); err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(answer)
	expected, _ := json.Marshal(draftproposal.ConfirmRequest{Snapshots: answer.Snapshots, Decisions: answer.Decisions})
	key := draftproposal.Digest(expected)
	raw, err := cmdPreparedChatAnswer(root, "XR-008", payload, CommitOpts{})
	if err != nil {
		t.Fatalf("старый показанный вопрос после adopt: %v", err)
	}
	var first, second struct {
		Key      string
		Results  []draftproposal.Receipt
		Repeated bool
	}
	if err = json.Unmarshal([]byte(raw), &first); err != nil {
		t.Fatal(err)
	}
	raw, err = cmdPreparedChatAnswer(root, "XR-008", payload, CommitOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal([]byte(raw), &second); err != nil {
		t.Fatal(err)
	}
	if first.Key != key || second.Key != key || !second.Repeated || !reflect.DeepEqual(first.Results, second.Results) || len(first.Results) != 1 {
		t.Fatalf("повтор изменил квитанцию: %+v %+v", first, second)
	}
	rawState, err := os.ReadFile(draftproposal.PreparedPath(root, "XR-008"))
	if err != nil {
		t.Fatal(err)
	}
	saved, err := draftproposal.ParsePrepared(rawState)
	if err != nil || len(saved.Receipts) != 2 {
		t.Fatalf("повтор добавил квитанцию: %v %v", saved, err)
	}
	after, err = os.ReadFile(chat.AskPath(root, chat.TaskName("XR-008")))
	if err != nil || !bytes.Equal(ask, after) {
		t.Fatal("ответ или повтор переписал показанный вопрос")
	}
}

func TestLegacyAdoptionStillRejectsChangedInputs(t *testing.T) {
	for _, key := range []string{"old-short-key", "XR-900"} {
		for _, what := range []string{"material", "definition", "target", "revision", "board_id", "description"} {
			t.Run(key+"/"+what, func(t *testing.T) {
				root, answer, _ := legacyAskForAdoptionKey(t, what == "target", key)
				if _, err := cmdDraftPreparedAdopt(root, []string{"XR-008"}); err != nil {
					t.Fatal(err)
				}
				record, state, err := readPrepared(root, "XR-008")
				if err != nil {
					t.Fatal(err)
				}
				switch what {
				case "material":
					err = os.WriteFile(draftPath(root, "XR-008"), append(record, []byte("\nНовый материал\n")...), 0644)
				case "definition":
					set, e := draftTypeSet(root)
					if e != nil {
						t.Fatal(e)
					}
					data, e := os.ReadFile(set.Types["task"].File)
					if e != nil {
						t.Fatal(e)
					}
					dir := filepath.Join(root, ".devkit", "draft-types")
					if e = os.MkdirAll(dir, 0755); e != nil {
						t.Fatal(e)
					}
					err = os.WriteFile(filepath.Join(dir, "task.toml"), data, 0644)
				case "target":
					path := taskFileAbs(root, "XR-001")
					data, e := os.ReadFile(path)
					if e != nil {
						t.Fatal(e)
					}
					err = os.WriteFile(path, append(data, []byte("\nНовый материал адресата\n")...), 0644)
				default:
					if what == "revision" {
						state.Proposals[0].Revision++
					}
					if what == "board_id" {
						state.Proposals[0].BoardID = "XR-999"
					}
					if what == "description" {
						state.Proposals[0].Text += " новый текст"
					}
					err = draftproposal.SavePrepared(root, "XR-008", state)
				}
				if err != nil {
					t.Fatal(err)
				}
				payload, _ := json.Marshal(answer)
				if out, e := cmdPreparedChatAnswer(root, "XR-008", payload, CommitOpts{}); e == nil {
					t.Fatalf("применено изменённое %s: %s", what, out)
				}
			})
		}
	}
}

func TestPreparedSaveCannotSupplyNumberAliases(t *testing.T) {
	root, _, _ := legacyAskForAdoption(t, false)
	if _, err := cmdDraftPreparedAdopt(root, []string{"XR-008"}); err != nil {
		t.Fatal(err)
	}
	raw, err := cmdDraftPrepared(root, "XR-008")
	if err != nil {
		t.Fatal(err)
	}
	var view preparedView
	json.Unmarshal([]byte(raw), &view)
	dir, err := drafttx.ReservationDir(root)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(dir, "adoptions", "XR-008"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("переходы: %v", entries)
	}
	aliasPath := filepath.Join(dir, "adoptions", "XR-008", entries[0].Name())
	before, err := os.ReadFile(aliasPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"number_adoptions", "number_aliases"} {
		payload := []byte(`{"proposals":[],"` + field + `":["forged"]}`)
		if _, err := cmdDraftPreparedSave(root, "XR-008", view.Snapshot, payload); err == nil {
			t.Fatalf("клиент прислал %s", field)
		}
	}
	after, err := os.ReadFile(aliasPath)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("клиент изменил служебный переход")
	}
}

func TestFullKeyAdoptionRejectsUnrelatedContext(t *testing.T) {
	for _, what := range []string{"without_alias", "shown_board_id"} {
		t.Run(what, func(t *testing.T) {
			root, answer, _ := legacyAskForAdoptionKey(t, false, "XR-900")
			if _, err := cmdDraftPreparedAdopt(root, []string{"XR-008"}); err != nil {
				t.Fatal(err)
			}
			if what == "without_alias" {
				dir, err := drafttx.ReservationDir(root)
				if err != nil {
					t.Fatal(err)
				}
				if err = os.RemoveAll(filepath.Join(dir, "adoptions", "XR-008")); err != nil {
					t.Fatal(err)
				}
			} else {
				ask, ok := chat.ReadAsk(chat.AskPath(root, chat.TaskName("XR-008")))
				if !ok {
					t.Fatal("показанный вопрос потерян")
				}
				ask.Questions[0].Prepared.Sources[0].Proposals[0].BoardID = "XR-999"
				if err := chat.WriteAsk(root, chat.TaskName("XR-008"), ask); err != nil {
					t.Fatal(err)
				}
			}
			payload, err := json.Marshal(answer)
			if err != nil {
				t.Fatal(err)
			}
			if out, err := cmdPreparedChatAnswer(root, "XR-008", payload, CommitOpts{}); err == nil {
				t.Fatalf("принят ответ без исходного служебного контекста %s: %s", what, out)
			}
		})
	}
}
