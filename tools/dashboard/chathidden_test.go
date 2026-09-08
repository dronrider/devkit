package main

import (
	"testing"
	"time"
)

// Список чатов панели держит только разговоры человека (DK-847): запись,
// поднятая конвейером без нажатия, несёт признак hidden в памяти диалога
// (chats.go, поле Hidden), и обычный список её не отдаёт вовсе. Поиск
// достаёт её явным запросом &hidden=1, и найденная строка несёт клеймо в
// самом ответе, чтобы панель не спутала её с разговором человека.

// TestChatListHidesHiddenSession: запись с hidden не идёт ни в проектный, ни
// в общий список машины, обычным окном или без него.
func TestChatListHidesHiddenSession(t *testing.T) {
	e, c := chatEnv(t)
	sid := "cccc3333-3333-4333-8333-333333333333"
	writeSession(t, e.home, e.proj, "", sid, saidLine("прогон сценария проверки", time.Now()), time.Now())
	if err := e.s.chatStoreWrite(sid, chatStore{Hidden: true}); err != nil {
		t.Fatal(err)
	}

	list, _ := chatsWindow(t, e, c, "&days=0")
	if got := chatOne(list, sid); got != nil {
		t.Fatalf("запись с hidden попала в общий список: %+v", got)
	}
}

// TestChatSearchFindsHiddenSession: параметр hidden=1 отдаёт и спрятанные
// записи, а найденная строка сама несёт признак, панель отличит её от
// разговора человека.
func TestChatSearchFindsHiddenSession(t *testing.T) {
	e, c := chatEnv(t)
	sid := "dddd4444-4444-4444-8444-444444444444"
	writeSession(t, e.home, e.proj, "", sid, saidLine("прогон сценария проверки", time.Now()), time.Now())
	if err := e.s.chatStoreWrite(sid, chatStore{Hidden: true}); err != nil {
		t.Fatal(err)
	}

	// Без hidden=1 запрос поиска (days=0) её по-прежнему не видит.
	list, _ := chatsWindow(t, e, c, "&days=0")
	if got := chatOne(list, sid); got != nil {
		t.Fatalf("запись с hidden попала в выдачу без &hidden=1: %+v", got)
	}

	list, _ = chatsWindow(t, e, c, "&days=0&hidden=1")
	got := chatOne(list, sid)
	if got == nil {
		t.Fatalf("запись с hidden не нашлась поиском (&hidden=1): %+v", list)
	}
	if !got.Hidden {
		t.Errorf("найденная строка не несёт признак hidden в ответе: %+v", got)
	}
}

// TestChatListKeepsOrdinarySession: соседняя запись без hidden остаётся в
// обоих запросах, признак не режет список целиком.
func TestChatListKeepsOrdinarySession(t *testing.T) {
	e, c := chatEnv(t)
	sid := "eeee5555-5555-4555-8555-555555555555"
	writeSession(t, e.home, e.proj, "", sid, saidLine("разговор человека", time.Now()), time.Now())

	for _, query := range []string{"&days=0", "&days=0&hidden=1"} {
		list, _ := chatsWindow(t, e, c, query)
		got := chatOne(list, sid)
		if got == nil {
			t.Fatalf("обычная запись пропала при запросе %q: %+v", query, list)
		}
		if got.Hidden {
			t.Errorf("обычная запись пришла с признаком hidden при запросе %q: %+v", query, got)
		}
	}
}
