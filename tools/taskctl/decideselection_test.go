package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/dronrider/devkit/internal/chat"
	"github.com/dronrider/devkit/internal/taskform"
)

func TestSelectionAtomicSnapshotAndReceipt(t *testing.T) {
	root := setup(t)
	addScenarioTestCards(t, root, "XR-005", "СЦ-1", "СЦ-2")
	decideRun(t, root, DecideParams{ID: "XR-005", Ask: "сценарии", Scenarios: true, Hint: "СЦ-1: нужен", Opts: []string{"СЦ-1 (слово пользователя): выбрать", "СЦ-2 (предложение агента): продолжить"}, Text: "что включаем?"})
	askForks(t, root, "XR-005", [][]string{{"срок", "30 дней: достаточно", "сколько хранить?", "7 дней"}})
	chatRun(t, root, DecideParams{ID: "XR-005", Chat: true}, &askDeps{}, botEnv)
	questions, err := scenarioChatQuestions(root, "XR-005", taskform.ParseForks(readTask(t, root, "XR-005")))
	if err != nil {
		t.Fatal(err)
	}
	selection := chat.Selection{Task: "XR-005", Questions: []chat.SelectedQuestion{
		{Fork: "сценарии", Snapshot: questions[0].Scenario.Snapshot, IDs: []string{"СЦ-2"}},
		{Fork: "срок", Snapshot: chat.QuestionSnapshot(questions[1]), IDs: []string{"99"}},
	}}
	input := func(reply string) DecideParams {
		data, _ := json.Marshal(selection)
		return DecideParams{ID: "XR-005", Selection: string(data), SelectionReply: reply, Now: decideDay}
	}
	before := readTask(t, root, "XR-005")
	wait := askInMain(t, root, "XR-005")
	if _, err := cmdDecide(root, input("one")); err == nil || readTask(t, root, "XR-005") != before || !askStands(wait) {
		t.Fatal("ошибка второй развилки изменила первую")
	}
	selection.Questions[1].IDs = []string{"2"}
	result := decideRun(t, root, input("one"))
	if !strings.Contains(result, "выбрано: СЦ-2") || !strings.Contains(result, "7 дней") || askStands(wait) {
		t.Fatalf("пачка не применена: %s", result)
	}
	before = readTask(t, root, "XR-005")
	if !strings.Contains(before, `вложение ответа: {"reply":"one"`) {
		t.Fatal("нет долговечной квитанции сообщения")
	}
	// A changed question cannot revoke an already accepted message receipt.
	decideRun(t, root, DecideParams{ID: "XR-005", Name: "сценарии", Opts: []string{"СЦ-3 (предложение агента): позже"}})
	before = readTask(t, root, "XR-005")
	retry := input("one")
	retry.SelectionRepeatOnly = true
	if got := decideRun(t, root, retry); got != result || readTask(t, root, "XR-005") != before {
		t.Fatal("точный повтор изменил новый вопрос или результат")
	}
	if _, err := cmdDecide(root, input("two")); err == nil || readTask(t, root, "XR-005") != before {
		t.Fatal("новое сообщение применило старый снимок")
	}
	selection.Questions[0].IDs = []string{"СЦ-1"}
	if _, err := cmdDecide(root, input("one")); err == nil || readTask(t, root, "XR-005") != before {
		t.Fatal("тот же ключ принял другой выбор")
	}
}

func TestSelectionExplicitEmptyIDs(t *testing.T) {
	root := setup(t)
	addScenarioTestCards(t, root, "XR-005", "СЦ-1")
	decideRun(t, root, DecideParams{ID: "XR-005", Ask: "сценарии", Scenarios: true, Hint: "СЦ-1: нужен", Opts: []string{"СЦ-1 (слово пользователя): выбрать"}, Text: "что включаем?"})
	chatRun(t, root, DecideParams{ID: "XR-005", Chat: true}, &askDeps{}, botEnv)
	qs, err := scenarioChatQuestions(root, "XR-005", taskform.ParseForks(readTask(t, root, "XR-005")))
	if err != nil {
		t.Fatal(err)
	}
	selection := chat.Selection{Task: "XR-005", Questions: []chat.SelectedQuestion{{Fork: "сценарии", Snapshot: qs[0].Scenario.Snapshot}}}
	before := readTask(t, root, "XR-005")
	data, _ := json.Marshal(selection)
	if _, err := cmdDecide(root, DecideParams{ID: "XR-005", Selection: string(data), SelectionReply: "missing", Now: decideDay}); err == nil || readTask(t, root, "XR-005") != before {
		t.Fatal("отсутствующий список стал отказом")
	}
	selection.Questions[0].IDs = []string{}
	data, _ = json.Marshal(selection)
	decideRun(t, root, DecideParams{ID: "XR-005", Selection: string(data), SelectionReply: "empty", Now: decideDay})
	f, _ := taskform.FindFork(readTask(t, root, "XR-005"), "сценарии")
	if !f.Reconsidered || !f.HoldsStart() || f.ChoiceEvent != 1 {
		t.Fatalf("отказ не сохранён: %+v", f)
	}
	qs, err = scenarioChatQuestions(root, "XR-005", []taskform.Fork{f})
	if err != nil || !qs[0].Scenario.Explicit || len(qs[0].Scenario.Current) != 0 {
		t.Fatalf("отказ не отличим от нового списка: %+v %v", qs, err)
	}
}
