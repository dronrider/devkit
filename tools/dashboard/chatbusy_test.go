package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"testing"
	"time"
)

// Состояние чата, каким его читает плашка работы в панели.
type chatStatusView struct {
	Live  bool   `json:"live"`
	Busy  bool   `json:"busy"`
	Sec   int    `json:"sec"`
	Since int64  `json:"since"`
	Gone  bool   `json:"gone"`
	At    int64  `json:"at"`
	Where string `json:"where"`
}

// chatBusyEnv поднимает разговор с молчащим транскриптом: занятость тут
// доказывает реестр клиента, а не свежая запись журнала, и меры возраста хода
// не мешает никто.
func chatBusyEnv(t *testing.T, now time.Time) (*testEnv, string) {
	t.Helper()
	e := newTestEnv(t)
	e.s.now = func() time.Time { return now }
	writeTmuxFake(t, e.bin, filepath.Join(e.home, "tmux.log"), "chat-XR-1\n")
	sid := "aaaa1111-1111-4111-8111-111111111111"
	// Последняя запись журнала лежит час назад: занятость по хвосту
	// транскрипта тут не сработает, и ответ ручки идёт от реестра.
	old := fmt.Sprintf(`{"type":"assistant","message":{"role":"assistant","content":`+
		`[{"type":"text","text":"давно"}]},"timestamp":%q}`,
		now.Add(-time.Hour).Format(time.RFC3339)) + "\n"
	writeSession(t, e.home, e.proj, "", sid, sessionLine("поговорим", "main")+old, now.Add(-time.Hour))
	writeBinds(t, e.home, listedBind(e.home, sid, "XR-1", "chat-XR-1"))
	return e, sid
}

func chatStatus(t *testing.T, e *testEnv, sid string) chatStatusView {
	t.Helper()
	c := e.loggedClient(t)
	resp := doReq(t, c, "GET", e.srv.URL+"/api/projects/demo/chats/"+sid+"/status", "")
	text := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("состояние чата: %d %s", resp.StatusCode, text)
	}
	var got chatStatusView
	if err := json.Unmarshal([]byte(text), &got); err != nil {
		t.Fatalf("ответ не разобрался: %v\n%s", err, text)
	}
	return got
}

// Возраст хода в ответе ручки состояния (DK-893). Плашка в панели гасла своим
// потолком в десять минут и не знала о ходе ничего, кроме «занят»: время с
// начала хода лежит в реестре клиента (statusUpdatedAt), а структура записи это
// поле не разбирала вовсе и на клиент его не отдавала. Ход в двенадцать минут
// после этого выглядел мёртвым чатом при живом процессе.
func TestChatStatusTellsTurnAge(t *testing.T) {
	now := time.Date(2026, 9, 9, 23, 21, 0, 0, time.UTC)
	e, sid := chatBusyEnv(t, now)
	began := now.Add(-12 * time.Minute)
	writePeerAged(t, e.home, sid, "chat-XR-1:@1.%1", "busy", began.UnixMilli())

	got := chatStatus(t, e, sid)
	if !got.Live || !got.Busy {
		t.Fatalf("идущий ход не назван занятым: %+v", got)
	}
	if got.Sec != 720 {
		t.Errorf("возраст хода приехал как %d с, ждал 720: %+v", got.Sec, got)
	}
	if got.Since != began.UnixMilli() {
		t.Errorf("начало хода приехало как %d, ждал %d", got.Since, began.UnixMilli())
	}
	if got.Gone {
		t.Errorf("живой ход объявлен пропажей: %+v", got)
	}

	// Простаивающая сессия возраста хода не имеет вовсе: метка там говорит,
	// когда сессия освободилась, и счётчик по ней врал бы о работе.
	writePeerAged(t, e.home, sid, "chat-XR-1:@1.%1", "idle", began.UnixMilli())
	if got := chatStatus(t, e, sid); got.Busy || got.Sec != 0 || got.Since != 0 {
		t.Errorf("простой отдал возраст хода: %+v", got)
	}
}

// Пропажа процесса посреди хода (DK-893). Клиент, снятый на ходу, оставляет в
// реестре своё «busy» и больше запись не трогает: панель по такому ответу
// красит плашку и называет пропажу своими словами, отличимыми от размышления.
func TestChatStatusTellsGoneMidTurn(t *testing.T) {
	now := time.Date(2026, 9, 9, 23, 21, 0, 0, time.UTC)
	e, sid := chatBusyEnv(t, now)
	pid := deadPID(t)
	began := now.Add(-3 * time.Minute)
	writePeerPID(t, e.home, pid, sid, "chat-XR-1:@1.%1", "busy", began.UnixMilli())

	got := chatStatus(t, e, sid)
	if got.Live || got.Busy {
		t.Fatalf("сессии нет, а ручка назвала её живой: %+v", got)
	}
	if !got.Gone {
		t.Fatalf("процесс пропал посреди хода, а ручка молчит: %+v", got)
	}
	// Времени смерти в реестре нет вовсе, и панель подписывает пропажу
	// последним следом жизни: записью транскрипта либо началом самого хода.
	if got.At != began.UnixMilli() {
		t.Errorf("последний след жизни приехал как %d, ждал %d", got.At, began.UnixMilli())
	}

	// Разговор, законченный по-человечески, пропажей не зовётся: клиент успел
	// записать простой, и ждать в таком чате нечего.
	writePeerPID(t, e.home, pid, sid, "chat-XR-1:@1.%1", "idle", began.UnixMilli())
	if got := chatStatus(t, e, sid); got.Gone {
		t.Errorf("законченный разговор объявлен пропажей: %+v", got)
	}

	// Старое «busy» мёртвой записи это не пропажа, а давно остановленный
	// разговор: слова про пропажу тут врали бы про час, которого человек не
	// ждал.
	writePeerPID(t, e.home, pid, sid, "chat-XR-1:@1.%1", "busy", now.Add(-time.Hour).UnixMilli())
	if got := chatStatus(t, e, sid); got.Gone {
		t.Errorf("часовая запись мёртвого клиента выдана за свежую пропажу: %+v", got)
	}
}

// Возраст хода в строке списка чатов (DK-893): занятый разговор видно и не
// открывая его, а без минут «активна» одинаково выглядит и у хода, начатого
// секунду назад, и у получасового.
func TestChatListTellsTurnAge(t *testing.T) {
	now := time.Date(2026, 9, 9, 23, 21, 0, 0, time.UTC)
	e, sid := chatBusyEnv(t, now)
	writePeerAged(t, e.home, sid, "chat-XR-1:@1.%1", "busy", now.Add(-6*time.Minute).UnixMilli())

	entry := func() chatEntry {
		t.Helper()
		for _, c := range e.s.chatEntries(e.proj, 20) {
			if c.ID == sid {
				return c
			}
		}
		t.Fatalf("разговор пропал из списка")
		return chatEntry{}
	}

	if got := entry(); got.Idle || got.Sec != 360 {
		t.Errorf("строка занятого разговора приехала с простоем %v и возрастом %d с, ждал ход в 360 с",
			got.Idle, got.Sec)
	}

	// Простаивающий разговор возраста хода не несёт: показывать нечего.
	writePeerAged(t, e.home, sid, "chat-XR-1:@1.%1", "idle", now.Add(-6*time.Minute).UnixMilli())
	if got := entry(); got.Sec != 0 {
		t.Errorf("простаивающая строка отдала возраст хода %d с", got.Sec)
	}
}
