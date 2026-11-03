package main

import (
	"fmt"
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
	if _, err := cmdDraftIdea(root, id, "вернуть старый фильтр", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	body := draftBody(t, root, id)
	ideas := strings.Index(body, draftIdeasHeading)
	groom := strings.Index(body, draftGroomHeading)
	if ideas < 0 || groom < 0 || ideas > groom {
		t.Fatalf("раздел идей лёг не перед грумингом:\n%s", body)
	}
}
