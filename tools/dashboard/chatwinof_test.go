package main

import (
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Запись рождения сессии вырезана ротацией журнала, а живой клиент на месте и
// называет своё окно сам. Список чатов такое окно видел, а стоп под перезапуск
// читал одну запись журнала и отвечал 409 со словами «поднимал не дашборд»
// про окно, которое дашборд поднял сам (живой случай chat-DK-713-1, DK-793).
// Правка выносит сборку имени в chatWinOf, и ручка находит окно у клиента.
func TestChatStopFindsWindowByLivePeer(t *testing.T) {
	e, c := chatEnv(t)
	sid := "ffff7777-7777-4777-8777-777777777777"
	writeSession(t, e.home, e.proj, "", sid, plainTalk, time.Now())
	// В журнале остался только сосед: строка старта этой сессии уехала за
	// хвост обрезки.
	other := "ffff8888-8888-4888-8888-888888888888"
	writeBinds(t, e.home, fmt.Sprintf("2026-08-23T14:40:00 сессия %s задача XR-2 проект demo "+
		"дерево %s транскрипт "+standTranscript(e.home, "o")+" источник заказ повод startup tmux chat-XR-2-1\n", other, e.proj))
	writePeerTmux(t, e.home, sid, "chat-XR-1-1:@997.%997", "idle")
	tmuxLog := filepath.Join(e.home, "tmux.log")
	writeTmuxFake(t, e.bin, tmuxLog, "chat-XR-1-1\t1\t1786000000\n")

	// Снимок панели с вопросом клиента идёт той же свёрткой: окно у живого
	// клиента есть, и ответ «не живёт в нашей tmux» тут врал бы.
	resp := doReq(t, c, "GET", e.srv.URL+"/api/projects/demo/chats/"+sid+"/ask", "")
	text := body(t, resp)
	if resp.StatusCode != http.StatusOK || strings.Contains(text, "не живёт в нашей tmux") {
		t.Errorf("снимок панели не нашёл окна у живого клиента: %d %s", resp.StatusCode, text)
	}

	resp = doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/stop", `{"drop": true}`)
	text = body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("снятие под перезапуск при вырезанной записи рождения: %d %s", resp.StatusCode, text)
	}
	if !strings.Contains(text, `"way":"drop"`) || !strings.Contains(text, `"tmux":"chat-XR-1-1"`) {
		t.Errorf("ручка не нашла окно у живого клиента: %s", text)
	}
	if got := readFile(t, tmuxLog); !strings.Contains(got, "kill-session -t chat-XR-1-1") {
		t.Errorf("tmux-сессия не снята: %s", got)
	}

	// Список чатов зовёт ту же свёртку: окно у строки то же, что нашла ручка.
	resp = doReq(t, c, "GET", e.srv.URL+"/api/projects/demo/chats?days=0", "")
	text = body(t, resp)
	if !strings.Contains(text, `"tmux":"chat-XR-1-1"`) || !strings.Contains(text, `"own":true`) {
		t.Errorf("список не показал окно живого клиента своим: %s", text)
	}

	// Окна нет ни у кого, а транскрипт свеж: отказ называет, где искали, а
	// не сочиняет чужого хозяина.
	alien := "cccc9999-9999-4999-8999-999999999999"
	writeSession(t, e.home, e.proj, "", alien, plainTalk, time.Now())
	for _, req := range []string{`{"drop": true}`, `{}`} {
		resp = doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+alien+"/stop", req)
		text = body(t, resp)
		if resp.StatusCode != http.StatusConflict {
			t.Errorf("стоп %s без окна не отбит: %d %s", req, resp.StatusCode, text)
		}
		for _, want := range []string{"журнале привязок", "живого клиента", "памяти окон"} {
			if !strings.Contains(text, want) {
				t.Errorf("отказ на %s не назвал источник «%s»: %s", req, want, text)
			}
		}
		if strings.Contains(text, "поднимал не дашборд") {
			t.Errorf("отказ на %s снова говорит про чужое окно: %s", req, text)
		}
	}
}

// Порядок источников свёртки: слово живого клиента старше записи журнала, а
// память окна отвечает только тому разговору, резюмом которого окно подняли,
// и только пока имя не заняли ни журнал, ни живой клиент.
func TestChatWinOfSources(t *testing.T) {
	e, _ := chatEnv(t)
	sid := "aaaa1111-1111-4111-8111-111111111111"
	recs := map[string][]sessionBind{sid: {{Tmux: "chat-old-1", Time: "2026-08-23T14:40:00"}}}
	live := map[string]peer{sid: {Tmux: "chat-new-1:@1.%1"}}
	if name, src := e.s.chatWinOf(sid, recs, live); name != "chat-new-1" || src != chatWinByPeer {
		t.Errorf("живой клиент не старше журнала: %s (%s)", name, src)
	}
	if name, src := e.s.chatWinOf(sid, recs, nil); name != "chat-old-1" || src != chatWinByBind {
		t.Errorf("журнал без клиента не назвал окна: %s (%s)", name, src)
	}
	// Окно поднято резюмом этого разговора, хук старта ещё не написал записи.
	e.s.chatRaised("chat-res-1", sid, "", "")
	if name, src := e.s.chatWinOf(sid, nil, nil); name != "chat-res-1" || src != chatWinByMemory {
		t.Errorf("память окна не назвала окна резюма: %s (%s)", name, src)
	}
	// Имя занял родившийся разговор: память окна прежнему хозяину уже не отвечает.
	born := "bbbb2222-2222-4222-8222-222222222222"
	taken := map[string][]sessionBind{born: {{Tmux: "chat-res-1", Time: "2026-08-23T15:00:00"}}}
	if name, src := e.s.chatWinOf(sid, taken, nil); name != "" || src != "" {
		t.Errorf("память окна отдала занятое имя: %s (%s)", name, src)
	}
	if name, src := e.s.chatWinOf(sid, nil, map[string]peer{born: {Tmux: "chat-res-1"}}); name != "" || src != "" {
		t.Errorf("память окна отдала имя живого чужого клиента: %s (%s)", name, src)
	}
}
