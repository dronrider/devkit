package chat

import "testing"

func TestSelectionMessagePreservesTextAndEmptyChoice(t *testing.T) {
	text := "  Дополню ещё один СЦ:\nплюс, минус; \"слова\"\n\n"
	original := SelectionMessage{Version: 1, Text: text, Selection: &Selection{Task: "XR-1", Tree: "/project", Questions: []SelectedQuestion{{Fork: "сценарии", Snapshot: "shown", IDs: []string{}}}}}
	wire := original.Wire()
	message, ok := ReadSelectionMessage(wire)
	if !ok || message.Text != text || message.Selection == nil || message.Selection.Questions[0].IDs == nil || len(message.Selection.Questions[0].IDs) != 0 {
		t.Fatalf("потерян текст или явный отказ: %+v", message)
	}
	if _, ok := ReadSelectionMessage("обычные слова " + wire); ok {
		t.Fatal("обычный текст принят за сообщение панели")
	}
	if _, ok := ReadSelectionMessage(SelectionWire + `{"version":2,"text":"другой формат"}`); ok {
		t.Fatal("неизвестная версия принята")
	}
	comment, ok := ReadSelectionMessage((SelectionMessage{Version: 1, Text: text}).Wire())
	if !ok || comment.Text != text || comment.Selection != nil {
		t.Fatal("комментарий получил неявный выбор")
	}
}

func TestQuestionSnapshotTracksShownOptions(t *testing.T) {
	q := Question{Text: "«срок»: сколько хранить?", Options: []Option{{Label: "30 дней", Recommended: true}, {Label: "7 дней"}}}
	key := QuestionSnapshot(q)
	q.Options[0].Label = "60 дней"
	if QuestionSnapshot(q) == key {
		t.Fatal("изменённый вариант сохранил старый снимок")
	}
}
