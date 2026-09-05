package main

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dronrider/devkit/internal/sessions"
)

// Стенды дожима стопа (доработка DK-716 после провала приёмки). Живой прогон
// 2026-09-05: ход разговора кончился в 14:54:26, стоп со строки пришёл в
// 14:54:33, два Escape ушли в пустоту, боковой журнал субагента писался ещё три
// минуты, а привязка сессии к задаче снялась сразу же.

// subLogAt кладёт боковой журнал субагента и ставит ему время правки: живость
// фоновой работы меряется именно им.
func subLogAt(t *testing.T, path, id string, at time.Time) string {
	t.Helper()
	log := writeSubLog(t, path, id, "долгий поиск по дереву", "")
	if err := os.Chtimes(log, at, at); err != nil {
		t.Fatal(err)
	}
	return log
}

// Фоновая работа сессии видна по боковым журналам: пока журнал пишется, работа
// идёт, даже когда сам разговор молчит. Метка возврата в мете кончает работу
// раньше срока молчания, а протухший журнал работой не считается.
func TestSubBusyOfSeesBackgroundWork(t *testing.T) {
	e := newTestEnv(t)
	now := time.Date(2026, 8, 10, 10, 0, 10, 0, time.UTC)
	sid := "aaaa1111-1111-4111-8111-111111111111"
	path := writeSession(t, e.home, e.proj, "", sid, transcriptFixture, now)

	subLogAt(t, path, "live", now.Add(-10*time.Second))
	if !subBusyOf(path, now) {
		t.Error("свежий боковой журнал работой не считается")
	}

	markEnded(t, path, "live", now.Add(-5*time.Second))
	if subBusyOf(path, now) {
		t.Error("вернувшаяся работа считается идущей: метка возврата старше свежести")
	}

	stale := writeSession(t, e.home, e.proj, "", "bbbb2222-2222-4222-8222-222222222222", transcriptFixture, now)
	subLogAt(t, stale, "old", now.Add(-10*time.Minute))
	if subBusyOf(stale, now) {
		t.Error("протухший боковой журнал считается идущей работой")
	}
}

// Строка доски не гаснет, пока живы фоновые субагенты: ход кончился, транскрипт
// разговора молчит, а работа по задаче идёт, и объявлять её оконченной нельзя.
// До правки строка тут простаивала, и с неё поднимался второй исполнитель
// поверх идущей работы.
func TestRowStaysBusyWhileSubagentWorks(t *testing.T) {
	sid := "dff98764-1111-4111-8111-111111111111"
	e, _, _ := chatWorkEnv(t, sid, "chat-XR-004-1")
	now := e.s.now()
	// Ход кончился две минуты назад: транскрипт остыл, незакрытых вызовов в
	// нём нет.
	path := writeSession(t, e.home, e.proj, "", sid, transcriptFixture, now.Add(-2*time.Minute))
	subLogAt(t, path, "live", now.Add(-3*time.Second))

	got := boardRows(t, e)["XR-004"]
	if got.RunState != workBusy || !got.RunBusy {
		t.Errorf("строка с живой фоновой работой простаивает: state=%q busy=%v", got.RunState, got.RunBusy)
	}
	if got.Run != runChat {
		t.Errorf("признак работы строки %q, ожидал %q", got.Run, runChat)
	}
}

// Стоп со строки, у которой ход уже кончился, а фоновая работа жива: Escape
// подаётся, но привязка не снимается, а стоп остаётся заказом. Иначе строка
// объявляет работу оконченной, пока субагенты дописывают своё, и вернувшийся
// агент начинает новый ход по свободной с виду строке.
func TestRunStopWaitsForBackgroundWork(t *testing.T) {
	sid := "dff98764-1111-4111-8111-111111111111"
	e, c, tmuxLog := chatWorkEnv(t, sid, "chat-XR-004-1")
	now := e.s.now()
	path := writeSession(t, e.home, e.proj, "", sid, transcriptFixture, now.Add(-2*time.Minute))
	subLogAt(t, path, "live", now.Add(-3*time.Second))

	resp := doReq(t, c, "DELETE", e.srv.URL+"/api/projects/demo/runs/XR-004", "")
	text := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("стоп разговора с фоновой работой: %d %s", resp.StatusCode, text)
	}
	if !strings.Contains(text, "фоновые субагенты") {
		t.Errorf("ответ стопа промолчал о живой фоновой работе: %s", text)
	}
	if strings.Count(readFile(t, tmuxLog), "send-keys -t =chat-XR-004-1: Escape") != 2 {
		t.Errorf("ход не прерван двумя Escape: %s", readFile(t, tmuxLog))
	}
	// Привязка держится: работа по строке идёт, и отдавать кнопку запуска
	// нельзя.
	recs := sessions.LoadAll(e.home)[sid]
	if !sessions.WorksOn(recs, "XR-004") {
		t.Error("привязка снята при живой фоновой работе: строка объявила работу оконченной")
	}
	if got := boardRows(t, e)["XR-004"]; got.Run != runChat || !got.RunStopping {
		t.Errorf("строка не держит остановку: run=%q stopping=%v", got.Run, got.RunStopping)
	}
	if !e.s.stopWaitOn("chat-XR-004-1") {
		t.Error("заказ дожима не поставлен: поднявшийся ход прерывать будет некому")
	}
	said := readFile(t, saidFile(e.s.cfg.Home, saidSessionKey(sid)))
	if !strings.Contains(said, "фоновые субагенты") {
		t.Errorf("лента разговора не узнала, что стоп дожимается: %s", said)
	}
}

// Субагент вернул работу и поднял агента новым ходом: сторож прерывает и его,
// теми же двумя Escape. Ровно этот ход человек прерывал вторым нажатием руками,
// а первое уходило в пустоту.
func TestStopWaitPressesRisenTurn(t *testing.T) {
	sid := "dff98764-1111-4111-8111-111111111111"
	e, c, tmuxLog := chatWorkEnv(t, sid, "chat-XR-004-1")
	now := e.s.now()
	path := writeSession(t, e.home, e.proj, "", sid, transcriptFixture, now.Add(-2*time.Minute))
	subLogAt(t, path, "live", now.Add(-3*time.Second))
	if resp := doReq(t, c, "DELETE", e.srv.URL+"/api/projects/demo/runs/XR-004", ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("стоп разговора: %d %s", resp.StatusCode, body(t, resp))
	}

	// Ход поднялся снова: в транскрипте появилась запись позже заказа.
	appendLine(t, path, fmt.Sprintf(
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"иду дальше"}]},"timestamp":%q}`+"\n",
		now.Add(time.Minute).Format(time.RFC3339)))
	e.s.now = func() time.Time { return now.Add(90 * time.Second) }
	e.s.stopWaitOne("chat-XR-004-1", func(string) bool { return true })

	if strings.Count(readFile(t, tmuxLog), "send-keys -t =chat-XR-004-1: Escape") != 4 {
		t.Errorf("поднявшийся ход не дожат: %s", readFile(t, tmuxLog))
	}
	if !sessions.WorksOn(sessions.LoadAll(e.home)[sid], "XR-004") {
		t.Error("привязка снята посреди идущей работы")
	}
	if !e.s.stopWaitOn("chat-XR-004-1") {
		t.Error("заказ дожима снят, пока работа идёт")
	}
}

// Фоновая работа встала: сторож снимает привязку записью «снята», кладёт слова
// в ленту разговора и кончает заказ. Строка отдаёт «Стоп» обратно кнопке
// запуска, а разговор остаётся жить.
func TestStopWaitReleasesWhenWorkStands(t *testing.T) {
	sid := "dff98764-1111-4111-8111-111111111111"
	e, c, _ := chatWorkEnv(t, sid, "chat-XR-004-1")
	now := e.s.now()
	path := writeSession(t, e.home, e.proj, "", sid, transcriptFixture, now.Add(-2*time.Minute))
	subLogAt(t, path, "live", now.Add(-3*time.Second))
	if resp := doReq(t, c, "DELETE", e.srv.URL+"/api/projects/demo/runs/XR-004", ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("стоп разговора: %d %s", resp.StatusCode, body(t, resp))
	}

	// Прошло пять минут: боковой журнал больше не пишется, нового хода не было.
	e.s.now = func() time.Time { return now.Add(5 * time.Minute) }
	e.s.stopWaitOne("chat-XR-004-1", func(string) bool { return true })

	recs := sessions.LoadAll(e.home)[sid]
	last := recs[len(recs)-1]
	if last.Source != sessions.ByOff || last.Task != "XR-004" {
		t.Fatalf("запись об окончании работы: источник %q, задача %q", last.Source, last.Task)
	}
	if sessions.WorksOn(recs, "XR-004") {
		t.Error("после остановки сессия по-прежнему считается работой задачи")
	}
	if e.s.stopWaitOn("chat-XR-004-1") {
		t.Error("заказ дожима остался стоять после конца работы")
	}
	said := readFile(t, saidFile(e.s.cfg.Home, saidSessionKey(sid)))
	if !strings.Contains(said, "остановлена человеком") {
		t.Errorf("лента разговора не узнала о конце работы: %s", said)
	}
	if got := boardRows(t, e)["XR-004"]; got.Run != "" || got.RunStopping {
		t.Errorf("строка не вернула кнопку запуска: run=%q stopping=%v", got.Run, got.RunStopping)
	}
}

// Окно разговора закрылось само: работа кончилась вместе с ним, и заказ
// кончается тем же порядком, что и по вставшей работе.
func TestStopWaitEndsWithWindow(t *testing.T) {
	sid := "dff98764-1111-4111-8111-111111111111"
	e, c, _ := chatWorkEnv(t, sid, "chat-XR-004-1")
	now := e.s.now()
	path := writeSession(t, e.home, e.proj, "", sid, transcriptFixture, now.Add(-2*time.Minute))
	subLogAt(t, path, "live", now.Add(-3*time.Second))
	if resp := doReq(t, c, "DELETE", e.srv.URL+"/api/projects/demo/runs/XR-004", ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("стоп разговора: %d %s", resp.StatusCode, body(t, resp))
	}
	e.s.stopWaitOne("chat-XR-004-1", func(string) bool { return false })
	if sessions.WorksOn(sessions.LoadAll(e.home)[sid], "XR-004") {
		t.Error("окна разговора нет, а строка держит его работой")
	}
	if e.s.stopWaitOn("chat-XR-004-1") {
		t.Error("заказ дожима пережил окно разговора")
	}
}

// Человек написал в тот же разговор: стоп он передумал, и дожимать его ход
// сторожу нечего. Без этого следующий ход человека прерывался бы сам собой.
func TestStopWaitOffOnHumanReply(t *testing.T) {
	sid := "dff98764-1111-4111-8111-111111111111"
	e, c, _ := chatWorkEnv(t, sid, "chat-XR-004-1")
	now := e.s.now()
	path := writeSession(t, e.home, e.proj, "", sid, transcriptFixture, now.Add(-2*time.Minute))
	subLogAt(t, path, "live", now.Add(-3*time.Second))
	if resp := doReq(t, c, "DELETE", e.srv.URL+"/api/projects/demo/runs/XR-004", ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("стоп разговора: %d %s", resp.StatusCode, body(t, resp))
	}
	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/say",
		`{"text":"продолжай, я передумал"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("реплика в разговор: %d %s", resp.StatusCode, body(t, resp))
	}
	if e.s.stopWaitOn("chat-XR-004-1") {
		t.Error("реплика человека не сняла заказ дожима: его же ход и прервётся")
	}
}

// chatWorkLiveEnv это тот же стенд рабочего разговора, но с живым окном в
// списке tmux: стоп из панели чата спрашивает про окно, а стоп со строки нет.
func chatWorkLiveEnv(t *testing.T, sid, tmux string) (*testEnv, *http.Client, string) {
	t.Helper()
	e, c, tmuxLog := runsEnv(t, tmux+"\t1\t1786000000\n")
	now := time.Date(2026, 8, 10, 10, 0, 10, 0, time.UTC)
	e.s.now = func() time.Time { return now }
	writeSession(t, e.home, e.proj, "", sid, transcriptFixture, now)
	writeBinds(t, e.home, fmt.Sprintf(
		"2026-08-10T09:59:00 сессия %s задача XR-004 проект demo дерево %s "+
			"транскрипт /tmp/t.jsonl источник заказ повод startup tmux %s\n"+
			"2026-08-10T09:59:30 сессия %s задача XR-004 проект demo дерево %s "+
			"транскрипт - источник работа повод «agentctl stage XR-004 разработка» tmux -\n",
		sid, e.proj, tmux, sid, e.proj))
	return e, c, tmuxLog
}

// Стоп из самой панели чата бьёт тем же Escape, и дыра у него та же: ход
// прерван, а фоновая работа жива. Ответ говорит об этом словами, и дожим
// ставится так же, как со строки доски.
func TestChatStopSaysBackgroundWork(t *testing.T) {
	sid := "dff98764-1111-4111-8111-111111111111"
	e, c, _ := chatWorkLiveEnv(t, sid, "chat-XR-004-1")
	now := e.s.now()
	path := writeSession(t, e.home, e.proj, "", sid, transcriptFixture, now.Add(-2*time.Minute))
	subLogAt(t, path, "live", now.Add(-3*time.Second))

	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/stop", "{}")
	text := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("стоп чата: %d %s", resp.StatusCode, text)
	}
	if !strings.Contains(text, "фоновые субагенты") {
		t.Errorf("ответ стопа чата промолчал о живой фоновой работе: %s", text)
	}
	if !e.s.stopWaitOn("chat-XR-004-1") {
		t.Error("стоп из панели чата заказа дожима не поставил")
	}
}

// Стенд фронта: круг обновления заводит живая сессия, у которой строки ещё нет,
// взятая строка показывает «Стоп» ближайшим заходом круга, возврат на вкладку
// перечитывает доску, а подсказка «Стопа» во время дожима говорит про фоновых
// субагентов. Разметку держал бы и прежний код, предмет тут в поведении, и
// проверить его можно только на настоящем app.js (testdata/poc_rowwake.mjs).
// Без node шаг пропускается: узел стенда, а не рабочей части.
func TestStaticRowWakesOnLiveSession(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node не найден: стенд пробуждения строки пропущен")
	}
	out, err := exec.Command(node, filepath.Join("testdata", "poc_rowwake.mjs"),
		filepath.Join("static", "app.js")).CombinedOutput()
	if err != nil {
		t.Fatalf("пробуждение строки живой сессией: %v\n%s", err, out)
	}
	t.Log(strings.TrimSpace(string(out)))
}
