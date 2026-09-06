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
// фоновой работы меряется им и хвостом самого журнала.
func subLogAt(t *testing.T, path, id, content string, at time.Time) string {
	t.Helper()
	log := writeSubLog(t, path, id, "долгий поиск по дереву", content)
	if err := os.Chtimes(log, at, at); err != nil {
		t.Fatal(err)
	}
	return log
}

// subLogBusyTail это хвост журнала субагента, ушедшего в долгий инструмент:
// вызов есть, ответа на него нет. Так выглядит журнал всё время сборки или
// прогона тестов, и трогать файл субагенту в эти минуты нечем.
const subLogBusyTail = `{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"call-1","name":"Bash","input":{"command":"go test ./..."}}]},"timestamp":"2026-08-10T09:57:10.000Z"}
`

// subLogIdleTail это хвост вернувшейся работы: на вызов пришёл ответ.
const subLogIdleTail = subLogBusyTail + `{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"call-1","content":"ok"}]},"timestamp":"2026-08-10T09:57:40.000Z"}
`

// stopTranscript это транскрипт разговора, чей ход уже кончился: последняя
// запись старше рубежа занятости, незакрытых вызовов в хвосте нет. Вызов
// субагента в нём отвечен сразу, как отвечает харнес фоновой работе (живой
// прогон 2026-09-05: вызов открыт в 11:54:18, ответ пришёл в 11:54:20, а сам
// субагент работал ещё три минуты).
func stopTranscript(now time.Time, sub string, answered bool) string {
	at := func(d time.Duration) string { return now.Add(d).Format(time.RFC3339) }
	lines := []string{
		fmt.Sprintf(`{"type":"user","message":{"role":"user","content":"отдай работу субагенту"},"timestamp":%q,"gitBranch":"main"}`, at(-5*time.Minute)),
		fmt.Sprintf(`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"беру"},{"type":"tool_use","id":%q,"name":"Agent","input":{}}]},"timestamp":%q}`, "tool-"+sub, at(-4*time.Minute)),
	}
	if answered {
		lines = append(lines, fmt.Sprintf(
			`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":%q,"content":"поднят"}]},"timestamp":%q}`,
			"tool-"+sub, at(-4*time.Minute)))
	}
	lines = append(lines, fmt.Sprintf(
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"субагент работает, жду"}]},"timestamp":%q}`, at(-3*time.Minute)))
	return strings.Join(lines, "\n") + "\n"
}

// stoppedChatEnv это стенд разговора, чей ход кончился, а фоновая работа жива:
// строка XR-004 за ним, транскрипт остыл, боковой журнал субагента лежит с
// названным хвостом и временем молчания.
func stoppedChatEnv(t *testing.T, quiet time.Duration, tail string, answered bool) (*testEnv, *http.Client, string, string) {
	t.Helper()
	sid := "dff98764-1111-4111-8111-111111111111"
	e, c, _ := chatWorkEnv(t, sid, "chat-XR-004-1")
	now := e.s.now()
	path := writeSession(t, e.home, e.proj, "", sid, stopTranscript(now, "live", answered), now.Add(-3*time.Minute))
	subLogAt(t, path, "live", tail, now.Add(-quiet))
	forgetDigests()
	return e, c, sid, path
}

// Живость фоновой работы: мерок три, и хватает любой. Свежий журнал, вызов без
// ответа в транскрипте и незакрытый вызов в хвосте самого журнала (субагент
// ушёл в долгий инструмент и файла не трогает). Метка возврата в мете кончает
// работу раньше всех трёх, протухший журнал работой не считается.
func TestSubBusyOfSeesBackgroundWork(t *testing.T) {
	e := newTestEnv(t)
	now := time.Date(2026, 8, 10, 10, 0, 10, 0, time.UTC)
	e.s.now = func() time.Time { return now }
	sid := "aaaa1111-1111-4111-8111-111111111111"
	path := writeSession(t, e.home, e.proj, "", sid, stopTranscript(now, "live", true), now.Add(-3*time.Minute))

	log := subLogAt(t, path, "live", "", now.Add(-10*time.Second))
	if !e.s.subBusyOf(path, now) {
		t.Error("свежий боковой журнал работой не считается")
	}

	// Журнал молчит три минуты, а в хвосте висит вызов без ответа: субагент в
	// сборке или в прогоне тестов, и работа идёт.
	if err := os.WriteFile(log, []byte(subLogBusyTail), 0o644); err != nil {
		t.Fatal(err)
	}
	quiet := now.Add(-3 * time.Minute)
	if err := os.Chtimes(log, quiet, quiet); err != nil {
		t.Fatal(err)
	}
	if !e.s.subBusyOf(path, now) {
		t.Error("субагент на долгом инструменте считается вставшим: журнал молчит, а вызов в нём без ответа")
	}

	// Работа вернулась: вызов в журнале закрыт, ответ на её собственный вызов
	// стоит в транскрипте, молчит она три минуты.
	if err := os.WriteFile(log, []byte(subLogIdleTail), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(log, quiet, quiet); err != nil {
		t.Fatal(err)
	}
	forgetDigests()
	if e.s.subBusyOf(path, now) {
		t.Error("вернувшаяся работа считается идущей")
	}

	// Ответа на вызов в транскрипте нет вовсе: мерка кольца пульса держит такую
	// работу идущей до получаса молчания.
	nosid := "bbbb2222-2222-4222-8222-222222222222"
	nopath := writeSession(t, e.home, e.proj, "", nosid, stopTranscript(now, "wait", false), now.Add(-3*time.Minute))
	subLogAt(t, nopath, "wait", subLogIdleTail, quiet)
	forgetDigests()
	if !e.s.subBusyOf(nopath, now) {
		t.Error("работа без ответа на свой вызов считается вставшей раньше получаса")
	}

	// Метка возврата старше всех мерок.
	markEnded(t, nopath, "wait", now.Add(-time.Minute))
	if e.s.subBusyOf(nopath, now) {
		t.Error("работа с меткой возврата считается идущей")
	}

	// Журнал молчит дольше получаса: работой это не считается ни по одной
	// мерке.
	oldsid := "cccc3333-3333-4333-8333-333333333333"
	oldpath := writeSession(t, e.home, e.proj, "", oldsid, stopTranscript(now, "old", true), now.Add(-3*time.Minute))
	subLogAt(t, oldpath, "old", subLogBusyTail, now.Add(-40*time.Minute))
	forgetDigests()
	if e.s.subBusyOf(oldpath, now) {
		t.Error("журнал, молчащий сорок минут, считается идущей работой")
	}
}

// Строка доски не гаснет, пока живы фоновые субагенты: ход кончился, транскрипт
// разговора молчит, а работа по задаче идёт. До правки строка тут простаивала, и
// с неё поднимался второй исполнитель поверх идущей работы.
func TestRowStaysBusyWhileSubagentWorks(t *testing.T) {
	e, _, _, _ := stoppedChatEnv(t, 3*time.Second, "", true)
	got := boardRows(t, e)["XR-004"]
	if got.RunState != workBusy || !got.RunBusy {
		t.Errorf("строка с живой фоновой работой простаивает: state=%q busy=%v", got.RunState, got.RunBusy)
	}
	if got.Run != runChat {
		t.Errorf("признак работы строки %q, ожидал %q", got.Run, runChat)
	}
}

// Субагент на долгом инструменте: журнал молчит три минуты, а в хвосте висит
// вызов без ответа. Строка обязана остаться занятой, а стоп со строки обязан
// оставить привязку. Мерка по одной свежести журнала объявляла такую работу
// вставшей, то есть возвращала расхождение приёмки.
func TestStopWaitsForLongToolSubagent(t *testing.T) {
	e, c, sid, _ := stoppedChatEnv(t, 3*time.Minute, subLogBusyTail, true)
	if got := boardRows(t, e)["XR-004"]; got.RunState != workBusy {
		t.Errorf("строка с субагентом на долгом инструменте простаивает: state=%q", got.RunState)
	}
	resp := doReq(t, c, "DELETE", e.srv.URL+"/api/projects/demo/runs/XR-004", "")
	text := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("стоп разговора: %d %s", resp.StatusCode, text)
	}
	if !strings.Contains(text, "фоновые субагенты") {
		t.Errorf("ответ стопа объявил работу оконченной: %s", text)
	}
	if !sessions.WorksOn(sessions.LoadAll(e.home)[sid], "XR-004") {
		t.Error("привязка снята при субагенте на долгом инструменте")
	}
}

// Ответа на вызов субагента в транскрипте нет вовсе: кольцо пульса держит такую
// работу идущей до получаса, и стоп меряет её тем же.
func TestStopWaitsForUnansweredCall(t *testing.T) {
	e, c, sid, _ := stoppedChatEnv(t, 3*time.Minute, subLogIdleTail, false)
	resp := doReq(t, c, "DELETE", e.srv.URL+"/api/projects/demo/runs/XR-004", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("стоп разговора: %d %s", resp.StatusCode, body(t, resp))
	}
	if !sessions.WorksOn(sessions.LoadAll(e.home)[sid], "XR-004") {
		t.Error("привязка снята при работе, чей вызов остался без ответа")
	}
}

// Стоп со строки, у которой ход уже кончился, а фоновая работа жива: Escape
// подаётся, но привязка не снимается, а стоп остаётся заказом. Иначе строка
// объявляет работу оконченной, пока субагенты дописывают своё, и вернувшийся
// агент начинает новый ход по свободной с виду строке.
func TestRunStopWaitsForBackgroundWork(t *testing.T) {
	e, c, sid, _ := stoppedChatEnv(t, 3*time.Second, "", true)
	tmuxLog := filepath.Join(e.home, "tmux.log")

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
	if !sessions.WorksOn(sessions.LoadAll(e.home)[sid], "XR-004") {
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

// Свой же след прерывания за поднявшийся ход не считается: клиент дописывает
// запись сразу после Escape, и без тишины сторож дожимал бы в пустоту на первом
// же заходе, а журнал говорил бы о прерванном ходе, которого не было.
func TestStopWaitHoldsAfterOwnEscape(t *testing.T) {
	e, c, _, path := stoppedChatEnv(t, 3*time.Second, "", true)
	now := e.s.now()
	tmuxLog := filepath.Join(e.home, "tmux.log")
	if resp := doReq(t, c, "DELETE", e.srv.URL+"/api/projects/demo/runs/XR-004", ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("стоп разговора: %d %s", resp.StatusCode, body(t, resp))
	}
	appendLine(t, path, fmt.Sprintf(
		`{"type":"user","message":{"role":"user","content":"[Request interrupted by user]"},"timestamp":%q}`+"\n",
		now.Add(time.Second).Format(time.RFC3339)))
	e.s.now = func() time.Time { return now.Add(6 * time.Second) }
	e.s.stopWaitOne("chat-XR-004-1", func(string) bool { return true })
	if strings.Count(readFile(t, tmuxLog), "send-keys -t =chat-XR-004-1: Escape") != 2 {
		t.Errorf("сторож дожал собственный след прерывания: %s", readFile(t, tmuxLog))
	}
}

// Субагент вернул работу и поднял агента новым ходом: сторож прерывает и его,
// теми же двумя Escape. Ровно этот ход человек прерывал вторым нажатием руками,
// а первое уходило в пустоту.
func TestStopWaitPressesRisenTurn(t *testing.T) {
	e, c, sid, path := stoppedChatEnv(t, 3*time.Second, "", true)
	now := e.s.now()
	tmuxLog := filepath.Join(e.home, "tmux.log")
	if resp := doReq(t, c, "DELETE", e.srv.URL+"/api/projects/demo/runs/XR-004", ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("стоп разговора: %d %s", resp.StatusCode, body(t, resp))
	}

	// Прошла минута, субагент вернулся, и агент пишет снова.
	appendLine(t, path, fmt.Sprintf(
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"иду дальше"}]},"timestamp":%q}`+"\n",
		now.Add(time.Minute).Format(time.RFC3339)))
	e.s.now = func() time.Time { return now.Add(65 * time.Second) }
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
	e, c, sid, _ := stoppedChatEnv(t, 3*time.Second, "", true)
	now := e.s.now()
	if resp := doReq(t, c, "DELETE", e.srv.URL+"/api/projects/demo/runs/XR-004", ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("стоп разговора: %d %s", resp.StatusCode, body(t, resp))
	}

	// Прошло пять минут: боковой журнал больше не пишется, нового хода не было.
	// Окно при этом стоит на приглашении, и это говорит сам клиент своим
	// экраном: заказ кончается по вставшей работе и по молчащему окну разом.
	writePane(t, filepath.Join(e.home, "tmux.log"), paneIdleScreen)
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

// Работа не встала за срок заказа: строка не может стоять под «Стопом» вечно, и
// сторож снимает привязку словами о том, что работа не встала. Срок считается
// от первого нажатия, а не от последнего дожима.
func TestStopWaitEndsBySpell(t *testing.T) {
	e, c, sid, path := stoppedChatEnv(t, 3*time.Second, "", true)
	now := e.s.now()
	if resp := doReq(t, c, "DELETE", e.srv.URL+"/api/projects/demo/runs/XR-004", ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("стоп разговора: %d %s", resp.StatusCode, body(t, resp))
	}
	// Ходы поднимаются один за другим, и каждый дожат: время последнего
	// нажатия при этом двигается вперёд, а срок заказа нет.
	for i := 1; i <= 3; i++ {
		at := now.Add(time.Duration(i*4) * time.Minute)
		appendLine(t, path, fmt.Sprintf(
			`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"иду дальше"}]},"timestamp":%q}`+"\n",
			at.Format(time.RFC3339)))
		e.s.now = func() time.Time { return at.Add(5 * time.Second) }
		e.s.stopWaitOne("chat-XR-004-1", func(string) bool { return true })
	}
	// Пошла шестнадцатая минута от первого нажатия, а фоновая работа всё жива.
	// Ход в окне при этом стоит: срок кончает заказ и по такому окну тоже,
	// иначе строка стояла бы под «Стопом» без конца.
	at := now.Add(16 * time.Minute)
	subLogAt(t, path, "live", "", at.Add(-3*time.Second))
	writePane(t, filepath.Join(e.home, "tmux.log"), paneIdleScreen)
	e.s.now = func() time.Time { return at }
	e.s.stopWaitOne("chat-XR-004-1", func(string) bool { return true })

	if e.s.stopWaitOn("chat-XR-004-1") {
		t.Error("заказ пережил свой срок: строка стоит под «Стопом» без конца")
	}
	if sessions.WorksOn(sessions.LoadAll(e.home)[sid], "XR-004") {
		t.Error("по сроку заказа привязка не снялась")
	}
}

// Окно разговора закрылось само: работа кончилась вместе с ним, и заказ
// кончается тем же порядком, что и по вставшей работе.
func TestStopWaitEndsWithWindow(t *testing.T) {
	e, c, sid, _ := stoppedChatEnv(t, 3*time.Second, "", true)
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
// сторожу нечего. Снимается заказ в общей точке доставки человеческих слов, и
// дорога реплики тут любая.
func TestStopWaitOffOnHumanReply(t *testing.T) {
	e, c, sid, _ := stoppedChatEnv(t, 3*time.Second, "", true)
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

// Имя окна дашборд переиспользует, и заказ дожима не должен доставаться
// следующему жильцу имени: подъём чата его снимает.
func TestStopWaitDropsOnRaise(t *testing.T) {
	e, c, _, _ := stoppedChatEnv(t, 3*time.Second, "", true)
	if resp := doReq(t, c, "DELETE", e.srv.URL+"/api/projects/demo/runs/XR-004", ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("стоп разговора: %d %s", resp.StatusCode, body(t, resp))
	}
	e.s.chatRaised("chat-XR-004-1", "cccc3333-3333-4333-8333-333333333333", "XR-004", "demo")
	if e.s.stopWaitOn("chat-XR-004-1") {
		t.Error("новый разговор под тем же именем окна получил чужой заказ дожима")
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
//
// Поле state несёт то же слово, что у стопа со строки (stopChatWork), и по
// нему клиент решает не гасить плашку хода молча, а назвать ею то же самое,
// что видно в run_stopping строки (замечание ревью 9, вторая приёмка
// DK-716). Без этого поля клиент читал голый 200 как «ход кончен».
func TestChatStopSaysBackgroundWork(t *testing.T) {
	sid := "dff98764-1111-4111-8111-111111111111"
	e, c, _ := chatWorkLiveEnv(t, sid, "chat-XR-004-1")
	now := e.s.now()
	path := writeSession(t, e.home, e.proj, "", sid, stopTranscript(now, "live", true), now.Add(-3*time.Minute))
	subLogAt(t, path, "live", "", now.Add(-3*time.Second))
	forgetDigests()

	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/stop", "{}")
	text := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("стоп чата: %d %s", resp.StatusCode, text)
	}
	if !strings.Contains(text, "фоновые субагенты") {
		t.Errorf("ответ стопа чата промолчал о живой фоновой работе: %s", text)
	}
	if !strings.Contains(text, `"state":"останавливается"`) {
		t.Errorf("ответ стопа чата не назвал state «останавливается»: %s", text)
	}
	if !e.s.stopWaitOn("chat-XR-004-1") {
		t.Error("стоп из панели чата заказа дожима не поставил")
	}
}

// Тот же стоп, но фоновой работы уже нет: state называет обычный «стоп», тем
// же словом, что у стопа со строки доски вне дожима.
func TestChatStopStateWithoutBackgroundWork(t *testing.T) {
	sid := "dff98764-2222-4111-8111-111111111111"
	e, c, _ := chatWorkLiveEnv(t, sid, "chat-XR-005-1")
	now := e.s.now()
	writeSession(t, e.home, e.proj, "", sid, stopTranscript(now, "live", true), now.Add(-3*time.Minute))
	forgetDigests()

	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/stop", "{}")
	text := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("стоп чата: %d %s", resp.StatusCode, text)
	}
	if !strings.Contains(text, `"state":"стоп"`) {
		t.Errorf("ответ стопа чата без фоновой работы не назвал state «стоп»: %s", text)
	}
	if e.s.stopWaitOn("chat-XR-005-1") {
		t.Error("стоп без фоновой работы всё равно поставил заказ дожима")
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

// Ход в окне не идёт: Escape туда не уходит вовсе. Два Escape прерывают ход,
// только пока ход идёт, а при остановленном они открывают меню отката, где одно
// нажатие Enter откатывает разговор человека. Живой случай четвёртой приёмки:
// человек нажал «Стоп», получил два уведомления об остановке, а клиент стоял в
// меню Rewind и не делал ничего.
func TestStopHoldsEscapeWhenTurnIsOver(t *testing.T) {
	e, c, sid, _ := stoppedChatEnv(t, 3*time.Second, "", true)
	tmuxLog := filepath.Join(e.home, "tmux.log")
	writePane(t, tmuxLog, paneIdleScreen)

	resp := doReq(t, c, "DELETE", e.srv.URL+"/api/projects/demo/runs/XR-004", "")
	text := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("стоп по остановленному ходу: %d %s", resp.StatusCode, text)
	}
	if strings.Contains(readFile(t, tmuxLog), "Escape") {
		t.Errorf("Escape ушёл в простаивающее окно: %s", readFile(t, tmuxLog))
	}
	if !strings.Contains(text, "не шёл") {
		t.Errorf("ответ выдал прерывание за сделанное: %s", text)
	}
	// Заказ дожима при этом стоит: фоновая работа жива, и поднявшийся ход
	// прерывать будет кому.
	if !e.s.stopWaitOn("chat-XR-004-1") {
		t.Error("заказ дожима не поставлен")
	}
	if !sessions.WorksOn(sessions.LoadAll(e.home)[sid], "XR-004") {
		t.Error("привязка снята при живой фоновой работе")
	}
}

// Клиент уже стоит в меню отката: закрывается оно одним Escape, и второго не
// шлётся. Пара нажатий тут открыла бы его снова, а человек в это меню не
// заходил, и выйти оттуда, кроме нас, некому.
func TestStopClosesRewindMenu(t *testing.T) {
	e, c, _, _ := stoppedChatEnv(t, 3*time.Second, "", true)
	tmuxLog := filepath.Join(e.home, "tmux.log")
	writePane(t, tmuxLog, paneRewindScreen)

	resp := doReq(t, c, "DELETE", e.srv.URL+"/api/projects/demo/runs/XR-004", "")
	if text := body(t, resp); resp.StatusCode != http.StatusOK {
		t.Fatalf("стоп по запертому клиенту: %d %s", resp.StatusCode, text)
	}
	if n := strings.Count(readFile(t, tmuxLog), "send-keys -t =chat-XR-004-1: Escape"); n != 1 {
		t.Errorf("меню клиента закрыто %d нажатиями, ждал одно: %s", n, readFile(t, tmuxLog))
	}
}

// Сторож дожима в простаивающее окно не бьёт: он ждёт поднявшегося хода и
// прерывает его. Прежде сторож слал Escape по свежести транскрипта шагом в пять
// секунд и загонял клиента в меню отката, где тот и оставался.
func TestStopWaitWaitsForRisenTurn(t *testing.T) {
	e, c, _, path := stoppedChatEnv(t, 3*time.Second, "", true)
	now := e.s.now()
	tmuxLog := filepath.Join(e.home, "tmux.log")
	if resp := doReq(t, c, "DELETE", e.srv.URL+"/api/projects/demo/runs/XR-004", ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("стоп разговора: %d %s", resp.StatusCode, body(t, resp))
	}
	was := strings.Count(readFile(t, tmuxLog), "Escape")
	// Окно простаивает, а заказ жив: фоновая работа ещё пишет свой журнал.
	writePane(t, tmuxLog, paneIdleScreen)
	for i := 1; i <= 3; i++ {
		at := now.Add(time.Duration(i) * time.Minute)
		subLogAt(t, path, "live", "", at)
		e.s.now = func() time.Time { return at }
		e.s.stopWaitOne("chat-XR-004-1", func(string) bool { return true })
	}
	if got := strings.Count(readFile(t, tmuxLog), "Escape"); got != was {
		t.Errorf("сторож бил Escape в простаивающее окно: было %d нажатий, стало %d", was, got)
	}
	if !e.s.stopWaitOn("chat-XR-004-1") {
		t.Error("заказ снят, пока фоновая работа идёт")
	}
	// Клиент, застигнутый в меню отката, выводится оттуда тем же сторожем.
	writePane(t, tmuxLog, paneRewindScreen)
	at := now.Add(5 * time.Minute)
	e.s.now = func() time.Time { return at }
	e.s.stopWaitOne("chat-XR-004-1", func(string) bool { return true })
	if got := strings.Count(readFile(t, tmuxLog), "Escape"); got != was+1 {
		t.Errorf("меню клиента не закрыто одним нажатием: было %d, стало %d", was, got)
	}
}

// Вторая беда четвёртой приёмки: агент, чей ход прерывают, успевает открыть
// этап командой доски, и строка снова считается рабочей уже после стопа. По
// журналу это видно по часам: стоп в 09:59:55, запись «работа» в 09:59:58.
// Остановку отменял тот, кого останавливают.
func TestStopHushesTakeAfterStop(t *testing.T) {
	sid := "dff98764-1111-4111-8111-111111111111"
	e, c, tmuxLog := chatWorkEnv(t, sid, "chat-XR-004-1")

	if resp := doReq(t, c, "DELETE", e.srv.URL+"/api/projects/demo/runs/XR-004", ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("стоп разговора: %d %s", resp.StatusCode, body(t, resp))
	}
	if got := boardRows(t, e)["XR-004"]; got.Run != "" {
		t.Fatalf("предусловие: после стопа строка свободна, а стоит run=%q", got.Run)
	}
	// Через три секунды после стопа агент открыл этап той же командой доски.
	appendBinds(t, e.home, fmt.Sprintf(
		"2026-08-10T10:00:13 сессия %s задача XR-004 проект demo дерево %s "+
			"транскрипт - источник работа повод «agentctl stage XR-004 разработка» tmux -\n",
		sid, e.proj))
	if got := boardRows(t, e)["XR-004"]; got.Run != "" || got.RunBusy {
		t.Errorf("взятие после стопа вернуло строке работу: run=%q busy=%v", got.Run, got.RunBusy)
	}
	// Заказ кончается, и последним словом реестра остаётся «снята»: дальше
	// глушить нечем, и запись обязана пережить заказ.
	writePane(t, tmuxLog, paneIdleScreen)
	e.s.now = func() time.Time { return time.Date(2026, 8, 10, 10, 5, 0, 0, time.UTC) }
	e.s.stopWaitOne("chat-XR-004-1", func(string) bool { return true })
	recs := sessions.LoadAll(e.home)[sid]
	last := recs[len(recs)-1]
	if last.Source != sessions.ByOff || last.Task != "XR-004" {
		t.Fatalf("последнее слово реестра: источник %q, задача %q", last.Source, last.Task)
	}
	if sessions.WorksOn(recs, "XR-004") {
		t.Error("после конца заказа строка снова считается рабочей")
	}
	if got := boardRows(t, e)["XR-004"]; got.Run != "" {
		t.Errorf("строка ожила после конца заказа: run=%q", got.Run)
	}
}

// Стоп из панели чата бьёт тем же Escape, и правило у него то же: в
// простаивающее окно нажатие не уходит. Проверяется отдельно, потому что дорог
// у стопа две, а меню отката запирает клиента одинаково с обеих.
func TestChatStopHoldsEscapeWhenTurnIsOver(t *testing.T) {
	sid := "dff98764-3333-4111-8111-111111111111"
	e, c, tmuxLog := chatWorkLiveEnv(t, sid, "chat-XR-004-1")
	now := e.s.now()
	writeSession(t, e.home, e.proj, "", sid, stopTranscript(now, "live", true), now.Add(-3*time.Minute))
	forgetDigests()
	writePane(t, tmuxLog, paneIdleScreen)

	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/stop", "{}")
	text := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("стоп чата по остановленному ходу: %d %s", resp.StatusCode, text)
	}
	if strings.Contains(readFile(t, tmuxLog), "Escape") {
		t.Errorf("Escape ушёл в простаивающее окно из панели: %s", readFile(t, tmuxLog))
	}
	if !strings.Contains(text, "ход не идёт") {
		t.Errorf("панель выдала прерывание за сделанное: %s", text)
	}
}

// Стоп конвейера меню отката не открывает: его сессию снимают целиком,
// kill-session, и клавиш в окно не подаётся вовсе. Проверяется ради той же
// беды с другой стороны: дорог у стопа три, и запирает клиента только Escape.
func TestPipelineStopSendsNoKeys(t *testing.T) {
	e, c, tmuxLog := runsEnv(t, "task-XR-004\t1\t1786000000\n")
	resp := doReq(t, c, "DELETE", e.srv.URL+"/api/projects/demo/runs/XR-004", "")
	if text := body(t, resp); resp.StatusCode != http.StatusOK {
		t.Fatalf("стоп конвейера: %d %s", resp.StatusCode, text)
	}
	log := readFile(t, tmuxLog)
	if !strings.Contains(log, "kill-session") {
		t.Errorf("конвейер снят не убийством сессии: %s", log)
	}
	if strings.Contains(log, "Escape") || strings.Contains(log, "send-keys") {
		t.Errorf("в окно конвейера ушли клавиши: %s", log)
	}
}

// Живой снимок настоящего окна: тот же признак, что решает про Escape, но
// спрошенный у живого tmux этой машины. Стенд по умолчанию пропускается, окно
// называет переменная DEVKIT_LIVE_PANE, и клавиш он никуда не подаёт. Нужен он
// живому шагу сценария: слова подсказки печатает клиент, они меняются от версии
// к версии, и проверить их можно только на живом окне.
//
//	DEVKIT_LIVE_PANE=chat-XR-198-1 go test -run TestPaneStateOnLiveWindow -v .
func TestPaneStateOnLiveWindow(t *testing.T) {
	name := os.Getenv("DEVKIT_LIVE_PANE")
	if name == "" {
		t.Skip("DEVKIT_LIVE_PANE не задан: живой снимок окна пропущен")
	}
	text, ok := chatPaneText(name)
	// Снятый снимок читается и файлом: экран живого окна живёт секунды, а
	// разобранный случай приёмки надо уметь показать ещё раз.
	if data, err := os.ReadFile(name); err == nil {
		text, ok = string(data), true
	}
	if !ok {
		t.Fatalf("снимок окна %s не прочитался", name)
	}
	state := paneState(text, ok)
	t.Logf("окно %s: %s", name, state)
	tail := strings.Split(strings.TrimRight(text, "\n"), "\n")
	if n := len(tail); n > 3 {
		tail = tail[n-3:]
	}
	t.Logf("хвост экрана:\n%s", strings.Join(tail, "\n"))
	if state == paneBlind {
		t.Errorf("состояние окна не узнано: экран прочитался, а слов клиента в нём нет")
	}
}
