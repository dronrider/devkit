package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Кнопка чата у цели (DK-938). Цикл цели прячется из списка чатов (DK-847), а
// виток идёт `claude -p`, и имени окна в реестре у него нет. Прежде кнопка
// строки и формы цели находила вместо витка вчерашний груминг той же задачи:
// чужой заголовок, красная плашка, реплика резюмом мёртвой сессии. Теперь
// сессию витка называет замок оболочки, скрытую запись отдаёт адресный вход
// (ключ keep), а реплика из чата цели ложится во «Входящие» её файла.
//
// Тесты кейсов собираются и на старом коде: краснота каждого сверена
// прогоном regcheck, а не падением сборки.

const goalTurnSID = "abab1212-1212-4212-8212-121212121212"

// goalLock кладёт замок оболочки цикла так, как его пишет goal-run.py: pid
// держателя и имя идущего витка.
func goalLock(t *testing.T, proj, id string, pid int, sid string) {
	t.Helper()
	dir := filepath.Join(proj, ".devkit", "goal-"+id+".lock")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pid"), []byte(fmt.Sprintf("%d\n", pid)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "session"), []byte(sid+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// goalTurnTalk это транскрипт витка: первой репликой идёт заказ оболочки.
func goalTurnTalk(id string) string {
	return saidLine("продолжай цель "+id+" по скиллу goal-loop", time.Now())
}

// goalChatRow это строка общего списка чатов в том виде, какой нужен тестам
// цели. Разбор свой, а не chatEntry: поле goal есть только у исправленного
// сервера, а тест обязан собираться и на старом коде.
type goalChatRow struct {
	ID     string `json:"id"`
	State  string `json:"state"`
	Hidden bool   `json:"hidden"`
	Goal   string `json:"goal"`
}

func goalChatRows(t *testing.T, e *testEnv, c *http.Client, query string) map[string]goalChatRow {
	t.Helper()
	resp := doReq(t, c, "GET", e.srv.URL+"/api/projects/demo/chats?all=1"+query, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("общий список (%s): %d", query, resp.StatusCode)
	}
	var got struct {
		Chats []goalChatRow `json:"chats"`
	}
	if err := json.Unmarshal([]byte(body(t, resp)), &got); err != nil {
		t.Fatal(err)
	}
	rows := map[string]goalChatRow{}
	for _, r := range got.Chats {
		rows[r.ID] = r
	}
	return rows
}

// TestGoalTurnNamesRowChat: пока оболочка держит замок, строка цели несёт адрес
// идущего витка (run_chat), и кнопка чата открывает его, а не ищет разговор по
// ID среди видимых. Брошенный замок адреса не даёт, имя в нём ведёт в
// кончившийся виток.
func TestGoalTurnNamesRowChat(t *testing.T) {
	e, _, _ := runsEnv(t, `goal-XR-100\t1\t1000\n`)
	writeSession(t, e.home, e.proj, "", goalTurnSID, goalTurnTalk("XR-100"), time.Now())
	writePeerAged(t, e.home, goalTurnSID, "", "busy", time.Now().UnixMilli())

	goalLock(t, e.proj, "XR-100", os.Getpid(), goalTurnSID)
	if got := boardRows(t, e)["XR-100"].RunChat; got != goalTurnSID {
		t.Fatalf("строка цели с живым витком несёт run_chat %q, ждал сессию витка %s", got, goalTurnSID)
	}
	w := workByID(boardWorks(t, e), "XR-100")
	if w == nil || w.Session != goalTurnSID || w.Live != workBusy {
		t.Errorf("работа цели не знает своего витка: %+v", w)
	}
	// Своей по имени окна работа не становится: tmux-сессию цикла поднимала
	// оболочка, а не запись реестра дашборда.
	if w != nil && (w.Own || w.Tmux != "") {
		t.Errorf("виток из замка сделал работу цели своей: %+v", w)
	}

	goalLock(t, e.proj, "XR-100", deadPID(t), goalTurnSID)
	if got := boardRows(t, e)["XR-100"].RunChat; got != "" {
		t.Errorf("брошенный замок дал строке адрес %q: кнопка открыла бы кончившийся виток", got)
	}
}

// TestGoalTurnNamesRegistryGoal: цикл без своего окна (--foreground в
// терминале человека) виден только записью реестра целей, и сессию витка ему
// называет тот же замок.
func TestGoalTurnNamesRegistryGoal(t *testing.T) {
	e, _, _ := runsEnv(t, `task-XR-004\t1\t1000\n`)
	writeSession(t, e.home, e.proj, "", goalTurnSID, goalTurnTalk("XR-112"), time.Now())
	writePeerAged(t, e.home, goalTurnSID, "", "busy", time.Now().UnixMilli())
	goalLock(t, e.proj, "XR-112", os.Getpid(), goalTurnSID)

	w := workByID(boardWorks(t, e), "XR-112")
	if w == nil || w.Session != goalTurnSID || w.Live != workBusy {
		t.Fatalf("цель из реестра не знает своего витка: %+v", w)
	}
	if w.Own || w.Tmux != "" {
		t.Errorf("виток из замка сделал цель из реестра своей: %+v", w)
	}
}

// TestChatKeepOpensHiddenGoalTurn: адресный вход (ключ keep, им панель просит
// открытый разговор) отдаёт скрытый виток живым и с именем цели, по нему
// панель шлёт реплику во «Входящие». Список без адреса по-прежнему скрытых не
// показывает (DK-847), и адрес одного витка соседние скрытые записи не
// открывает.
func TestChatKeepOpensHiddenGoalTurn(t *testing.T) {
	e, c := chatEnv(t)
	other := "cdcd3434-3434-4434-8434-343434343434"
	writeSession(t, e.home, e.proj, "", goalTurnSID, goalTurnTalk("XR-100"), time.Now())
	writeSession(t, e.home, e.proj, "", other, saidLine("прогон сценария проверки", time.Now()), time.Now())
	for _, sid := range []string{goalTurnSID, other} {
		if err := e.s.chatStoreWrite(sid, chatStore{Hidden: true}); err != nil {
			t.Fatal(err)
		}
	}
	writePeer(t, e.home, goalTurnSID, os.Getpid())

	rows := goalChatRows(t, e, c, "&days=0&keep="+goalTurnSID)
	got, ok := rows[goalTurnSID]
	if !ok {
		t.Fatalf("адресный вход не отдал скрытый виток: %+v", rows)
	}
	if got.State != chatLive {
		t.Errorf("живой виток пришёл в состоянии %q: панель нарисовала бы его мёртвым", got.State)
	}
	if !got.Hidden || got.Goal != "XR-100" {
		t.Errorf("виток пришёл без признака hidden или без имени цели: %+v", got)
	}
	if _, ok := rows[other]; ok {
		t.Errorf("адрес одного витка открыл соседнюю скрытую запись: %+v", rows)
	}

	rows = goalChatRows(t, e, c, "&days=0")
	if _, ok := rows[goalTurnSID]; ok {
		t.Errorf("список без адреса показал скрытый виток: %+v", rows)
	}
	if _, ok := rows[other]; ok {
		t.Errorf("список без адреса показал скрытую запись: %+v", rows)
	}
}

// TestStaticGoalChatReplyToInbox: реплика из чата цели уходит ручкой цели во
// «Входящие», и ни /say, ни резюм второго агента она не поднимает. Живой виток
// и стоящий цикл проверяются в поддельном DOM стендом testdata/poc_goalchat.mjs
// над настоящим app.js. Без node шаг пропускается.
func TestStaticGoalChatReplyToInbox(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node не найден: стенд реплики цели пропущен")
	}
	out, err := exec.Command(node, filepath.Join("testdata", "poc_goalchat.mjs"),
		filepath.Join("static", "app.js")).CombinedOutput()
	if err != nil {
		t.Fatalf("реплика из чата цели: %v\n%s", err, out)
	}
	t.Log(strings.TrimSpace(string(out)))
}
