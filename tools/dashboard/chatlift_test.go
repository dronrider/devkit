package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dronrider/devkit/internal/chat"
	"github.com/dronrider/devkit/internal/draftproposal"
)

// Подъём мёртвого носителя под ответ вручённого вопроса (DK-1569). Прежде
// дорога ответа клала адресную строку в вход разговора и считала это успехом:
// носитель, умерший между вручением и ответом, не узнавал о строке никогда.
// Теперь ответ доезжает сокетом живого клиента либо подъёмом продолжения той
// же сессии, а безадресная строка остаётся запасной дорогой на случай, когда
// подъём не вышел. Стенды ниже поднимают носитель с журналом вызовов tmux.

// liftStand заменяет отказывающий tmux стенда сценариев на журналящий: окно
// подъёма рождается, и вызовы видно по логу. Носитель остаётся мёртвым, ведь
// списка сессий у фикстуры нет.
func liftStand(t *testing.T) (*scenarioStand, string) {
	t.Helper()
	st := newScenarioStand(t)
	log := filepath.Join(st.e.home, "tmux.log")
	writeTmuxFake(t, st.e.bin, log, "")
	writeScript(t, st.e.bin, "claude", "exit 0")
	return st, log
}

// liftAsserts проверяет подъём одну дорогу за другой: продолжение поднято
// резюмом той же сессии с ответом вводной, записью в реестре на прежний адрес,
// а вход задачи пуст, потому что дубль доставки сторожок считал бы вторым
// ответом.
func liftAsserts(t *testing.T, st *scenarioStand, log, reply string) {
	t.Helper()
	said := readFile(t, log)
	if !strings.Contains(said, "new-session -d -s chat-XR-4-1 ") {
		t.Errorf("продолжение носителя не поднято своим окном: %s", said)
	}
	if !strings.Contains(said, "--resume '"+scenarioPanelSID+"'") {
		t.Errorf("клиент поднят без продолжения того же разговора: %s", said)
	}
	if !strings.Contains(said, reply) {
		t.Errorf("ответ не уехал вводной резюма: %s", said)
	}
	if lines := chatLines(t, st.e.proj, chat.TaskName("XR-4")); len(lines) != 0 {
		t.Errorf("ответ продублирован строкой во вход задачи: %q", lines)
	}
	binds := readFile(t, filepath.Join(st.e.home, ".devkit", "sessions.log"))
	if !strings.Contains(binds, "сессия "+scenarioPanelSID+" задача XR-4 ") ||
		!strings.Contains(binds, "повод резюм чата tmux chat-XR-4-1") {
		t.Errorf("подъём не записан реестром на прежний адрес: %s", binds)
	}
}

// Ответ на вопрос сценариев поднимает умершего носителя: окно рождается
// резюмом той же сессии, ответ уезжает вводной, и адресная строка в мёртвый
// вход не пишется вовсе.
func TestScenarioAnswerLiftsDeadCarrier(t *testing.T) {
	st, log := liftStand(t)
	ask := st.ask(t)
	st.send(t, "сценарии СЦ-1", ask, http.StatusOK)
	liftAsserts(t, st, log, "сценарии СЦ-1")
}

// Ответ с выбором позиций едет той же дорогой: носитель записи вопроса это сам
// разговор, и мёртвый разговор продолжается, а не получает строку в вход.
func TestSelectionAnswerLiftsDeadCarrier(t *testing.T) {
	st, log := liftStand(t)
	ask := st.ask(t)
	request := selectionTestBody(t, ask, "берём первый и третий", "selection-lift", map[string][]string{"сценарии": {"СЦ-1", "СЦ-3"}})
	st.sendSelection(t, request, http.StatusOK)
	liftAsserts(t, st, log, "берём первый и третий")
}

// Комментарий к карточкам предложений поднимает носителя так же: вопрос
// показан, адресат записи известен, и мёртвая сессия продолжает разговор с
// ответом вводной.
func TestProposalCommentLiftsDeadCarrier(t *testing.T) {
	st, log := liftStand(t)
	q := &chat.PreparedQuestion{Version: 1, Tree: st.e.proj, Anchor: "XR-4",
		Sources: []chat.PreparedSource{{Source: "XR-4", Basis: "basis",
			Proposals: []draftproposal.Proposal{{ID: "p", Revision: 1, Outcome: "drop", Text: "показано"}}}}}
	if err := chat.WriteAsk(st.e.proj, chat.TaskName("XR-4"), chat.Ask{Task: "XR-4", Session: scenarioPanelSID,
		Questions: []chat.Question{{Text: "вопрос", Proposal: q}}}); err != nil {
		t.Fatal(err)
	}
	answer := proposalAnswer{Mode: "comment", Anchor: "XR-4", Tree: st.e.proj,
		Snapshots: []draftproposal.Snapshot{{Source: "XR-4", Digest: "basis"}}}
	data, _ := json.Marshal(map[string]any{"text": "почему этот исход?", "msg_id": "comment-lift", "proposal": answer})
	resp := doReq(t, st.c, "POST", st.e.srv.URL+"/api/projects/demo/chats/"+scenarioPanelSID+"/say", string(data))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("комментарий к карточкам: %d %s", resp.StatusCode, body(t, resp))
	}
	liftAsserts(t, st, log, "почему этот исход?")
}

// Подъём не выдался: окно не родилось, и ответ уходит запасной безадресной
// строкой, которую сторожок считает ответом и будит припаркованную строку
// тиком. Адресная строка в мёртвом входе лежала бы молча.
func TestAnswerFallsBackUnaddressedWhenLiftFails(t *testing.T) {
	st := newScenarioStand(t)
	ask := st.ask(t)
	st.send(t, "сценарии СЦ-2", ask, http.StatusOK)
	lines := chatLines(t, st.e.proj, chat.TaskName("XR-4"))
	if len(lines) != 1 {
		t.Fatalf("запасная дорога не положила строку: %q", lines)
	}
	if chat.Addressee(lines[0]) != "" {
		t.Fatalf("строка адресована мёртвой сессии и лежала бы молча: %q", lines[0])
	}
}

// Транскрипта нет, и резюмить нечего: подъём не заказывается, ответ уезжает
// безадресной строкой той же запасной дорогой.
func TestAnswerFallsBackUnaddressedWithoutTranscript(t *testing.T) {
	st, _ := liftStand(t)
	ask := st.ask(t)
	transcript := filepath.Join(st.e.home, ".claude", "projects", claudeDirName(st.e.proj), scenarioPanelSID+".jsonl")
	if err := os.Remove(transcript); err != nil {
		t.Fatal(err)
	}
	st.send(t, "сценарии СЦ-3", ask, http.StatusOK)
	lines := chatLines(t, st.e.proj, chat.TaskName("XR-4"))
	if len(lines) != 1 || chat.Addressee(lines[0]) != "" {
		t.Fatalf("ответ без транскрипта не лёг безадресной строкой: %q", lines)
	}
}

// Живой терминал остаётся первой дорогой: носитель вернулся в своё окно, и
// ответ уходит клавишами туда, не поднимая второго клиента.
func TestAnswerPrefersLiveTerminalOverLift(t *testing.T) {
	st := newScenarioStand(t)
	ask := st.ask(t)
	log := filepath.Join(st.e.home, "tmux.log")
	writeTmuxFake(t, st.e.bin, log, "chat-live\t1\t1754770421\n")
	appendBinds(t, st.e.home, "2026-10-28T13:00:00 сессия "+scenarioPanelSID+" задача XR-4"+
		" проект demo дерево "+st.e.proj+" транскрипт "+standTranscript(st.e.home, "t")+
		" источник заказ повод startup tmux chat-live\n")
	st.send(t, "сценарии СЦ-1", ask, http.StatusOK)
	said := readFile(t, log)
	if !strings.Contains(said, "send-keys") || !strings.Contains(said, "сценарии СЦ-1") {
		t.Fatalf("ответ не ушёл клавишами в живое окно: %s", said)
	}
	if strings.Contains(said, "new-session") {
		t.Fatalf("живому окну поднят второй клиент: %s", said)
	}
}
