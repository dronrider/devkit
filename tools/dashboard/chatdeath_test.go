package main

import (
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Стенд сторожа подъёма (DK-728). Подъём отвечал удачей по коду возврата tmux,
// а он говорит только о том, что сессия создана: клиент, умерший через секунду,
// уносил её с собой, и панель до скончания ожидания обещала сессию, которой
// нет. Правка, от которой стенд краснеет: сверка живости поднятой сессии в
// поиске по имени tmux (chatWatchOne) и смерть, названная словами с хвостом
// терминала.

// tmuxWatchFake это фикстура tmux сторожа: список сессий и снимок панели
// приезжают строками, а всё остальное молча удаётся. Имя пустое значит «сессий
// нет», снимок пустой значит «панели уже нет», и capture-pane отвечает отказом,
// как настоящий tmux на снятой сессии.
func tmuxWatchFake(t *testing.T, e *testEnv, name, pane string) {
	t.Helper()
	list := ""
	if name != "" {
		list = "printf '" + name + "\\t1\\t1754770421\\n'"
	}
	snap := "exit 1"
	if pane != "" {
		snap = "printf '" + pane + "'"
	}
	writeScript(t, e.bin, "tmux", `case "$1" in
ls) `+list+`;;
capture-pane) `+snap+`;;
esac
exit 0`)
}

// chatDeadOf достаёт слово о смерти из поиска разговора по имени tmux-сессии:
// этим поиском панель ждёт родившийся диалог, и ответ на него это единственное
// место, где ожидание может кончиться.
func chatDeadOf(t *testing.T, e *testEnv, c *http.Client, name string) struct {
	Tmux string `json:"tmux"`
	Why  string `json:"why"`
	Tail string `json:"tail"`
} {
	t.Helper()
	resp := doReq(t, c, "GET", e.srv.URL+"/api/projects/demo/chats?tmux="+name, "")
	text := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("поиск разговора по имени tmux: %d %s", resp.StatusCode, text)
	}
	var got struct {
		Chats []struct {
			ID string `json:"id"`
		} `json:"chats"`
		Dead struct {
			Tmux string `json:"tmux"`
			Why  string `json:"why"`
			Tail string `json:"tail"`
		} `json:"dead"`
	}
	if err := json.Unmarshal([]byte(text), &got); err != nil {
		t.Fatalf("ответ поиска не разобрался: %v (%s)", err, text)
	}
	if len(got.Chats) > 0 {
		t.Fatalf("поиск нашёл разговор, которого стенд не заводил: %s", text)
	}
	return got.Dead
}

// saidMarks собирает пометки ленты разговора: смерть сессии человек читает в
// ленте, а не в журнале демона.
func saidMarks(t *testing.T, home, key string) []string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(home, "said", key+".jsonl"))
	if err != nil {
		return nil
	}
	data := string(raw)
	var out []string
	for _, line := range strings.Split(data, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var rec saidRec
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("строка журнала не разобралась: %v (%s)", err, line)
		}
		if rec.Kind == saidKindMark {
			out = append(out, rec.Text)
		}
	}
	return out
}

// Молчаливая смерть поднятой сессии называется человеку: ожидание подъёма
// кончается словом о смерти с хвостом терминала, а не молчанием до конца опроса.
func TestChatSpawnDeathSaid(t *testing.T) {
	e, c := chatEnv(t)
	writeScript(t, e.bin, "claude", "exit 0")
	// До подъёма сессий нет: имя диалогу выбирается свободное.
	tmuxWatchFake(t, e, "", "")

	sid := "dead-1111-2222-3333"
	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/say",
		`{"text": "разбери накопитель"}`)
	text := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("реплика в чат без сессии: %d %s", resp.StatusCode, text)
	}
	var raise struct {
		Way  string `json:"way"`
		Tmux string `json:"tmux"`
	}
	if err := json.Unmarshal([]byte(text), &raise); err != nil {
		t.Fatal(err)
	}
	if raise.Way != "start" || raise.Tmux == "" {
		t.Fatalf("подъём не назвал сессию: %s", text)
	}

	// Первый заход ожидания: сессия жива, клиент ещё не назвался в реестре.
	// Панель ждёт дальше, а сторож снимает панель терминала про запас.
	tmuxWatchFake(t, e, raise.Tmux, `Credit balance is too low\nдобавьте оплату и запустите заново\n`)
	if dead := chatDeadOf(t, e, c, raise.Tmux); dead.Why != "" {
		t.Fatalf("живую сессию объявили мёртвой: %+v", dead)
	}

	// Клиент умер, tmux-сессии больше нет.
	tmuxWatchFake(t, e, "", "")
	dead := chatDeadOf(t, e, c, raise.Tmux)
	if dead.Why == "" {
		t.Fatal("смерть поднятой сессии не названа: панель ждёт диалог, которого не будет")
	}
	if !strings.Contains(dead.Why, raise.Tmux) {
		t.Errorf("в словах о смерти нет имени сессии: %q", dead.Why)
	}
	if dead.Tmux != raise.Tmux {
		t.Errorf("смерть названа не той сессией: %q, ждал %q", dead.Tmux, raise.Tmux)
	}
	if !strings.Contains(dead.Tail, "Credit balance is too low") {
		t.Errorf("в исходе нет хвоста терминала, причину смерти брать негде: %q", dead.Tail)
	}

	marks := saidMarks(t, e.home, "sess-"+sid)
	if len(marks) == 0 {
		t.Fatal("в ленте разговора нет строки о смерти сессии")
	}
	last := marks[len(marks)-1]
	if !strings.Contains(last, raise.Tmux) || !strings.Contains(last, "Credit balance is too low") {
		t.Errorf("строка ленты не называет ни сессии, ни причины: %q", last)
	}
}

// Медленный старт смертью не считается: клиент, который называется в реестре с
// задержкой (вопрос доверия каталогу, долгий первый запуск), доезжает до ленты
// как прежде, и ожидание подъёма его не хоронит.
func TestChatSpawnSlowStartAlive(t *testing.T) {
	e, c := chatEnv(t)
	writeScript(t, e.bin, "claude", "exit 0")
	tmuxWatchFake(t, e, "", "")

	sid := "slow-1111-2222-3333"
	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/say",
		`{"text": "почему подписка тратится медленнее"}`)
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
	tmuxWatchFake(t, e, raise.Tmux, `Do you trust the files in this folder?\n1. Yes, proceed\n`)
	for i := 0; i < 3; i++ {
		if dead := chatDeadOf(t, e, c, raise.Tmux); dead.Why != "" {
			t.Fatalf("заход %d объявил живую сессию мёртвой: %+v", i, dead)
		}
	}
	if marks := saidMarks(t, e.home, "sess-"+sid); len(marks) != 0 {
		t.Fatalf("в ленте живого подъёма стоят пометки: %v", marks)
	}
}

// Шестая дорога подъёма это разбор черновика (drafts.go). Экран ждёт его тем же
// опросом, что и чат: groomDraft зовёт chatSewHere, та chatSewLoop, а он ходит в
// поиск по имени tmux-сессии. Без отметки подъёма поиск про эту сессию молчит, и
// умерший грумер вешает ту же немую петлю ожидания (замечание ревью DK-728).
func TestDraftGroomDeathSaid(t *testing.T) {
	e, c, _ := tasksEnv(t)
	writeTmuxFake(t, e.bin, filepath.Join(e.home, "tmux.log"), "")
	writeScript(t, e.bin, "claude", "exit 0")
	writeAgentctlFake(t, e.bin, harnessTiersFixture)
	doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/drafts",
		`{"text": "дашборд не показывает исход подъёма", "prio": "mid"}`).Body.Close()

	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/drafts/XR-005/groom", "")
	text := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("груминг черновика: %d %s", resp.StatusCode, text)
	}
	var raise struct {
		Session string `json:"session"`
	}
	if err := json.Unmarshal([]byte(text), &raise); err != nil {
		t.Fatal(err)
	}
	if raise.Session == "" {
		t.Fatalf("груминг не назвал сессии: %s", text)
	}

	tmuxWatchFake(t, e, raise.Session, `Invalid API key. Please run /login\n`)
	if dead := chatDeadOf(t, e, c, raise.Session); dead.Why != "" {
		t.Fatalf("живой разбор объявили мёртвым: %+v", dead)
	}

	tmuxWatchFake(t, e, "", "")
	dead := chatDeadOf(t, e, c, raise.Session)
	if dead.Why == "" {
		t.Fatal("смерть сессии разбора не названа: экран ждёт разговор, которого не будет")
	}
	if !strings.Contains(dead.Tail, "Invalid API key") {
		t.Errorf("в исходе разбора нет хвоста терминала: %q", dead.Tail)
	}
	// Разговора у разбора ещё нет, и смерть едет в журнал задачи: её читает
	// лента той сессии, которая задачу продолжит.
	marks := saidMarks(t, e.home, "task-XR-005")
	if len(marks) == 0 {
		t.Fatal("смерть разбора не легла в журнал задачи")
	}
	if !strings.Contains(marks[len(marks)-1], raise.Session) {
		t.Errorf("строка журнала не называет сессию: %q", marks[len(marks)-1])
	}
}

// Панельная сторона исхода: ожидание кончается смертью, плашка подъёма гаснет,
// а в пустой ленте вместо обещания встаёт причина с хвостом терминала.
// Сторожит стенд testdata/poc_deadraise.mjs: настоящий static/app.js
// поднимается в node с заглушкой DOM, и проверяется собранная разметка, а не
// текст исходника. Без node шаг пропускается, как у остальных стендов статики.
func TestStaticChatDeadRaiseTellsThePanel(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node не найден: стенд смерти подъёма пропущен")
	}
	out, err := exec.Command(node, filepath.Join("testdata", "poc_deadraise.mjs"),
		filepath.Join("static", "app.js")).CombinedOutput()
	if err != nil {
		t.Fatalf("смерть подъёма в панели: %v\n%s", err, out)
	}
	t.Log(strings.TrimSpace(string(out)))
}
