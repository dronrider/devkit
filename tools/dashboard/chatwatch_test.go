package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Стенд сторожа демона (DK-728): смерть сессии, уже начавшей ход, слова исхода
// и хвост терминала. Опрос панели этот случай не закрывает: человек уходит с
// экрана, а разговор замирает на последней реплике, и отличить его от
// думающего агента нечем.

// Смерть сессии, уже начавшей ход, говорит о себе тем же порядком: запись
// реестра стоит, лента замирает на последней реплике, и без этой строки
// отличить её от думающего агента нечем.
func TestChatWatchDeathAfterTurnSaid(t *testing.T) {
	e, c := chatEnv(t)
	writeScript(t, e.bin, "claude", "exit 0")
	tmuxWatchFake(t, e, "", "")

	sid := "turn-1111-2222-3333"
	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/say",
		`{"text": "прогони тесты"}`)
	text := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("реплика в чат без сессии: %d %s", resp.StatusCode, text)
	}
	var raise struct {
		Tmux string `json:"tmux"`
	}
	if err := json.Unmarshal([]byte(text), &raise); err != nil {
		t.Fatal(err)
	}
	// Клиент назвался в реестре: с этой минуты разговор живёт своим ID, и
	// смерть сессии обязана прийти в его ленту.
	born := "1111-2222-3333-4444"
	writeBinds(t, e.home, "2026-09-02T12:00:00 сессия "+born+
		" задача - проект demo дерево "+e.proj+" транскрипт /tmp/t.jsonl "+
		"источник заказ повод startup tmux "+raise.Tmux+"\n")
	tmuxWatchFake(t, e, raise.Tmux, `> прогони тесты\nagent: сейчас\n`)
	e.s.chatWatchTick()
	if marks := saidMarks(t, e.home, "sess-"+born); len(marks) != 0 {
		t.Fatalf("живую сессию похоронили: %v", marks)
	}

	tmuxWatchFake(t, e, "", "")
	e.s.chatWatchTick()
	marks := saidMarks(t, e.home, "sess-"+born)
	if len(marks) == 0 {
		t.Fatal("смерть начавшей ход сессии не видна в ленте разговора")
	}
	if !strings.Contains(marks[0], raise.Tmux) {
		t.Errorf("строка ленты не называет сессию: %q", marks[0])
	}
	// Второй обход той же смерти новой строки не пишет: лента не место для
	// повторов, а сторож ходит каждые несколько секунд.
	e.s.chatWatchTick()
	if got := saidMarks(t, e.home, "sess-"+born); len(got) != len(marks) {
		t.Errorf("сторож повторил строку о смерти: %v", got)
	}
}

// Сессия, снятая самим дашбордом, смертью не считается: человек нажал «снять
// под перезапуск», и строка о смерти в ленте была бы враньём.
func TestChatDropNoDeathSaid(t *testing.T) {
	e, c := chatEnv(t)
	writeScript(t, e.bin, "claude", "exit 0")
	tmuxWatchFake(t, e, "", "")

	sid := "drop-1111-2222-3333"
	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/say",
		`{"text": "подними разбор"}`)
	text := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("реплика в чат без сессии: %d %s", resp.StatusCode, text)
	}
	var raise struct {
		Tmux string `json:"tmux"`
	}
	if err := json.Unmarshal([]byte(text), &raise); err != nil {
		t.Fatal(err)
	}
	born := "2222-3333-4444-5555"
	writeBinds(t, e.home, "2026-09-02T12:00:00 сессия "+born+
		" задача - проект demo дерево "+e.proj+" транскрипт /tmp/t.jsonl "+
		"источник заказ повод startup tmux "+raise.Tmux+"\n")
	tmuxWatchFake(t, e, raise.Tmux, `> подними разбор\n`)
	drop := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+born+"/stop", `{"drop": true}`)
	if drop.StatusCode != http.StatusOK {
		t.Fatalf("снятие под перезапуск: %d %s", drop.StatusCode, body(t, drop))
	}
	tmuxWatchFake(t, e, "", "")
	e.s.chatWatchTick()
	if marks := saidMarks(t, e.home, "sess-"+born); len(marks) != 0 {
		t.Fatalf("снятую рукой сессию объявили умершей: %v", marks)
	}
}

// Хвост терминала едет в ленту без секретов и без края: длинные строки, похожие
// на токен, затираются, а из панели остаётся дюжина последних строк. Числа тут
// выписаны руками: посчитанные тем же кодом, они сошлись бы при любом его
// поведении.
func TestChatTailCutAndMasked(t *testing.T) {
	pane := ""
	for i := 1; i <= 20; i++ {
		pane += fmt.Sprintf("строка %d\n", i)
	}
	got := strings.Split(chatTailCut(pane), "\n")
	if len(got) != 12 {
		t.Errorf("хвост в %d строк, ждал 12: %q", len(got), got)
	}
	if got[0] != "строка 9" || got[len(got)-1] != "строка 20" {
		t.Errorf("хвост взят не с конца панели: %q ... %q", got[0], got[len(got)-1])
	}
	token := "токен sk-ant-oat01-JHGkjhg8767jhgKJHGkjhg8767jhgKJHGkjhg876 протух"
	if strings.Contains(chatTailCut(token), "JHGkjhg8767jhgKJHGkjhg8767jhgKJHGkjhg876") {
		t.Error("похожая на токен строка уехала в долгий журнал ленты как есть")
	}
	if got := chatTailCut("  \n\n  \n"); got != "" {
		t.Errorf("пустая панель дала хвост %q", got)
	}
}

// Время жизни подъёма называется словами, а не голым числом секунд: строка
// ленты читается человеком.
func TestChatDeathWordTellsLife(t *testing.T) {
	quick := chatDeathWord("chat-1", 3*time.Second, false)
	if !strings.Contains(quick, "chat-1") || !strings.Contains(quick, "3 с") {
		t.Errorf("слова о немом подъёме: %q", quick)
	}
	if !strings.Contains(quick, "не начав") {
		t.Errorf("слова не отличают немой подъём от смерти после хода: %q", quick)
	}
	turn := chatDeathWord("chat-2", 90*time.Minute, true)
	if strings.Contains(turn, "не начав") {
		t.Errorf("смерть после хода названа немым подъёмом: %q", turn)
	}
	if !strings.Contains(turn, "chat-2") {
		t.Errorf("слова о смерти после хода: %q", turn)
	}
}

// Разбор черновика, снятый кнопкой стопа, смертью не считается. Имя сессии у
// разбора и у конвейера задачи одно (`task-<ID>`), стоп ходит ручкой работ, и
// без снятия с присмотра сторож объявил бы снятое рукой смертью.
func TestDraftGroomStopNoDeathSaid(t *testing.T) {
	e, c, _ := tasksEnv(t)
	writeTmuxFake(t, e.bin, filepath.Join(e.home, "tmux.log"), "")
	writeScript(t, e.bin, "claude", "exit 0")
	writeAgentctlFake(t, e.bin, harnessTiersFixture)
	doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/drafts",
		`{"text": "исход подъёма виден человеку", "prio": "mid"}`).Body.Close()
	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/drafts/XR-005/groom", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("груминг черновика: %d %s", resp.StatusCode, body(t, resp))
	}

	tmuxWatchFake(t, e, "task-XR-005", `> разбираю черновик\n`)
	stop := doReq(t, c, "DELETE", e.srv.URL+"/api/projects/demo/runs/XR-005", "")
	if stop.StatusCode != http.StatusOK {
		t.Fatalf("стоп разбора: %d %s", stop.StatusCode, body(t, stop))
	}
	tmuxWatchFake(t, e, "", "")
	e.s.chatWatchTick()
	if marks := saidMarks(t, e.home, "task-XR-005"); len(marks) != 0 {
		t.Fatalf("снятый рукой разбор объявили умершим: %v", marks)
	}
}
