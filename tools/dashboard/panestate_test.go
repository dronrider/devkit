package main

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dronrider/devkit/internal/sessions"
)

// Состояния окна разбираются по словам клиента, и слова эти у каждого окна
// свои. Подсказка про отмену по Escape общая у трёх: меню отката, виджета
// вопроса и экрана входа. По ней одной все три читались откатом, и стоп бил
// Escape в вопрос человека, отчитываясь о закрытом меню клиента (замечание
// ревью 13). Экраны в таблице сняты с живых окон этой машины.
func TestPaneStateTellsClientScreensApart(t *testing.T) {
	cases := []struct {
		name   string
		screen string
		want   string
	}{
		{"ход", paneTurnScreen, paneTurn},
		{"простой", paneIdleScreen, paneIdle},
		{"меню отката", paneRewindScreen, paneRewind},
		{"вопрос человеку", paneAskScreen, paneAsk},
		{"экран входа", paneLoginScreen, paneLogin},
		{"эхо снимка в ленте", paneEchoScreen, paneIdle},
		{"эхо слов меню при идущем ходе", paneEchoRewindScreen, paneTurn},
		{"эхо слов меню на простое", paneEchoRewindIdleScreen, paneIdle},
		{"эхо слов входа при идущем ходе", paneEchoLoginScreen, paneTurn},
	}
	for _, c := range cases {
		if got := paneState(c.screen, true); got != c.want {
			t.Errorf("%s: состояние %q, ждал %q", c.name, got, c.want)
		}
	}
	if got := paneState("", true); got != paneBlind {
		t.Errorf("пустой снимок: состояние %q, ждал %q", got, paneBlind)
	}
	if got := paneState(paneIdleScreen, false); got != paneBlind {
		t.Errorf("непрочитанный снимок: состояние %q, ждал %q", got, paneBlind)
	}
}

// Стоп из панели чата в окно с вопросом клавиш не подаёт. Escape тут отменил бы
// вопрос, которого человек ещё не прочитал, а ответ соврал бы про закрытое меню
// клиента.
func TestChatStopHoldsAskWidget(t *testing.T) {
	sid := "dff98764-4444-4111-8111-111111111111"
	e, c, tmuxLog := chatWorkLiveEnv(t, sid, "chat-XR-004-1")
	now := e.s.now()
	writeSession(t, e.home, e.proj, "", sid, stopTranscript(now, "live", true), now.Add(-3*time.Minute))
	forgetDigests()
	writePane(t, tmuxLog, paneAskScreen)

	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/stop", "{}")
	text := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("стоп чата на вопросе: %d %s", resp.StatusCode, text)
	}
	if strings.Contains(readFile(t, tmuxLog), "Escape") {
		t.Errorf("Escape ушёл в окно с вопросом: %s", readFile(t, tmuxLog))
	}
	if !strings.Contains(text, "вопрос") {
		t.Errorf("ответ не назвал вопрос человеку: %s", text)
	}
	if strings.Contains(text, "меню клиента") {
		t.Errorf("вопрос человеку выдан за меню отката: %s", text)
	}
}

// Тот же запрет на экране входа: Escape бросил бы начатый вход, а дашборд про
// это молчал бы словами про меню клиента.
func TestChatStopHoldsLoginScreen(t *testing.T) {
	sid := "dff98764-5555-4111-8111-111111111111"
	e, c, tmuxLog := chatWorkLiveEnv(t, sid, "chat-XR-004-1")
	now := e.s.now()
	writeSession(t, e.home, e.proj, "", sid, stopTranscript(now, "live", true), now.Add(-3*time.Minute))
	forgetDigests()
	writePane(t, tmuxLog, paneLoginScreen)

	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/stop", "{}")
	text := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("стоп чата на входе: %d %s", resp.StatusCode, text)
	}
	if strings.Contains(readFile(t, tmuxLog), "Escape") {
		t.Errorf("Escape ушёл в окно входа: %s", readFile(t, tmuxLog))
	}
	if !strings.Contains(text, "экране входа") {
		t.Errorf("ответ не назвал экран входа: %s", text)
	}
}

// Стоп со строки доски держит тот же запрет: дорог у стопа две, и вопрос
// человека одинаково отменяется с обеих. Работа со строки при этом снимается:
// ход не идёт, и держать строку под «Стопом» не за что.
func TestRowStopHoldsAskWidget(t *testing.T) {
	sid := "dff98764-6666-4111-8111-111111111111"
	e, c, tmuxLog := chatWorkEnv(t, sid, "chat-XR-004-1")
	writePane(t, tmuxLog, paneAskScreen)

	resp := doReq(t, c, "DELETE", e.srv.URL+"/api/projects/demo/runs/XR-004", "")
	text := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("стоп строки на вопросе: %d %s", resp.StatusCode, text)
	}
	if strings.Contains(readFile(t, tmuxLog), "Escape") {
		t.Errorf("Escape ушёл в окно с вопросом: %s", readFile(t, tmuxLog))
	}
	if !strings.Contains(text, "ждёт ответа") {
		t.Errorf("ответ не назвал ожидание ответа человека: %s", text)
	}
	if sessions.WorksOn(sessions.LoadAll(e.home)[sid], "XR-004") {
		t.Error("работа со строки не снята, хотя ход не идёт")
	}
}

// Сторож дожима в окно с вопросом не бьёт ни разу. Прежде такое окно читалось
// меню отката, и сторож, пока жив заказ, отменял вопрос агента каждые пять
// секунд без всякого нового действия человека.
func TestStopWaitHoldsAskWidget(t *testing.T) {
	e, c, _, path := stoppedChatEnv(t, 3*time.Second, "", true)
	now := e.s.now()
	tmuxLog := filepath.Join(e.home, "tmux.log")
	if resp := doReq(t, c, "DELETE", e.srv.URL+"/api/projects/demo/runs/XR-004", ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("стоп разговора: %d %s", resp.StatusCode, body(t, resp))
	}
	was := strings.Count(readFile(t, tmuxLog), "Escape")
	// Ход прерван, агент спросил человека, а фоновая работа ещё пишет журнал:
	// заказ жив, и сторож ходит по окну каждые несколько секунд.
	writePane(t, tmuxLog, paneAskScreen)
	for i := 1; i <= 3; i++ {
		at := now.Add(time.Duration(i) * time.Minute)
		subLogAt(t, path, "live", "", at)
		e.s.now = func() time.Time { return at }
		e.s.stopWaitOne("chat-XR-004-1", func(string) bool { return true })
	}
	if got := strings.Count(readFile(t, tmuxLog), "Escape"); got != was {
		t.Errorf("сторож отменял вопрос человека: было %d нажатий, стало %d", was, got)
	}
	if !e.s.stopWaitOn("chat-XR-004-1") {
		t.Error("заказ снят, пока фоновая работа идёт")
	}
	// Фоновая работа встала, а вопрос на экране остался: заказ кончается, и
	// причина в журнале названа человеком, а не вставшей работой.
	lc := &logCapture{}
	e.s.logf = lc.log
	at := now.Add(40 * time.Minute)
	e.s.now = func() time.Time { return at }
	e.s.stopWaitOne("chat-XR-004-1", func(string) bool { return true })
	if got := strings.Count(readFile(t, tmuxLog), "Escape"); got != was {
		t.Errorf("сторож ударил Escape по вопросу на конце заказа: было %d, стало %d", was, got)
	}
	if !lc.contains(t, "окно держит человек") {
		t.Errorf("журнал не назвал причину конца заказа: %v", lc.lines)
	}
}

// Экран входа держит тот же сторож и теми же правилами: клавиш туда не идёт.
func TestStopWaitHoldsLoginScreen(t *testing.T) {
	e, c, _, path := stoppedChatEnv(t, 3*time.Second, "", true)
	now := e.s.now()
	tmuxLog := filepath.Join(e.home, "tmux.log")
	if resp := doReq(t, c, "DELETE", e.srv.URL+"/api/projects/demo/runs/XR-004", ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("стоп разговора: %d %s", resp.StatusCode, body(t, resp))
	}
	was := strings.Count(readFile(t, tmuxLog), "Escape")
	writePane(t, tmuxLog, paneLoginScreen)
	at := now.Add(time.Minute)
	subLogAt(t, path, "live", "", at)
	e.s.now = func() time.Time { return at }
	e.s.stopWaitOne("chat-XR-004-1", func(string) bool { return true })
	if got := strings.Count(readFile(t, tmuxLog), "Escape"); got != was {
		t.Errorf("сторож бил Escape по экрану входа: было %d нажатий, стало %d", was, got)
	}
}

// Слова меню отката, напечатанные агентом в свою ленту, окном не считаются.
// Они лежат в исходниках самого дашборда и в тексте задачи, и печатает их
// всякий агент, который тут работает. Прежде такой снимок читался меню отката,
// и стоп бил Escape по идущему ходу, отвечал «ход не шёл» и снимал работу со
// строки, пока та шла (замечание ревью 15).
func TestRowStopReadsEchoOfRewindWords(t *testing.T) {
	sid := "dff98764-9999-4111-8111-111111111111"
	e, c, tmuxLog := chatWorkEnv(t, sid, "chat-XR-004-1")
	writePane(t, tmuxLog, paneEchoRewindScreen)

	resp := doReq(t, c, "DELETE", e.srv.URL+"/api/projects/demo/runs/XR-004", "")
	text := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("стоп строки на эхе слов меню: %d %s", resp.StatusCode, text)
	}
	log := readFile(t, tmuxLog)
	if n := strings.Count(log, "send-keys -t =chat-XR-004-1: Escape"); n != 2 {
		t.Errorf("ход прерван %d нажатиями, ждал два: %s", n, log)
	}
	if !strings.Contains(text, "прерван") {
		t.Errorf("идущий ход выдан за неидущий: %s", text)
	}
	if !e.s.stopWaitOn("chat-XR-004-1") {
		t.Error("заказ дожима не поставлен на прерванном ходе")
	}
}

// Незнакомая подсказка клиента читается простоем, и стоп тогда молчит вместо
// работы: клавиш он не шлёт, а человеку отвечает «прерывать нечего». Отличить
// это от честного простоя можно вторым источником: незакрытый вызов в журнале
// сессии значит, что агент в ходе, о котором окно не сказало (замечание ревью
// 14). Расхождение едет человеку словами и строкой в журнал дашборда.
func TestChatStopDoubtsUnknownScreen(t *testing.T) {
	sid := "dff98764-7777-4111-8111-111111111111"
	e, c, tmuxLog := chatWorkLiveEnv(t, sid, "chat-XR-004-1")
	lc := &logCapture{}
	e.s.logf = lc.log
	now := e.s.now()
	// Ход идёт, а клиент подписал окно словами, которых мы не знаем: своей
	// новой формулировкой или чужим языком.
	writeSession(t, e.home, e.proj, "", sid, doubtTranscript(now, 5*time.Second), now.Add(-5*time.Second))
	forgetDigests()
	writePane(t, tmuxLog, "  режим работы включён . для агентов")

	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/stop", "{}")
	text := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("стоп чата на незнакомом экране: %d %s", resp.StatusCode, text)
	}
	if !strings.Contains(text, "подсказки клиента не узнал") {
		t.Errorf("ответ выдал незнакомый экран за простой: %s", text)
	}
	if !lc.contains(t, "подсказки клиента не узнал") {
		t.Errorf("журнал промолчал о неузнанном экране: %v", lc.lines)
	}
}

// doubtTranscript это транскрипт с незакрытым вызовом инструмента: вызов подан
// и ответа на него нет. Время последней записи задаётся молчанием quiet, им и
// решается, движется журнал прямо сейчас или вызов давно брошен.
func doubtTranscript(now time.Time, quiet time.Duration) string {
	at := func(d time.Duration) string { return now.Add(-d).Format(time.RFC3339) }
	return fmt.Sprintf(`{"type":"user","message":{"role":"user","content":"собери проект"},"timestamp":%q}`+"\n"+
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"tool-x","name":"Bash","input":{}}]},"timestamp":%q}`+"\n",
		at(quiet+time.Minute), at(quiet))
}

// Вызов, брошенный прерванным ходом или несданной работой делегата, висит в
// хвосте журнала сколько угодно. Одного его мало: над спокойным окном, где всё
// в порядке, сомнение звучало бы обвинением клиента (замечание ревью 16).
// Второй довод это движение, журнал должен писаться прямо сейчас.
func TestChatStopTrustsAbandonedCall(t *testing.T) {
	sid := "dff98764-aaaa-4111-8111-111111111111"
	e, c, tmuxLog := chatWorkLiveEnv(t, sid, "chat-XR-004-1")
	lc := &logCapture{}
	e.s.logf = lc.log
	now := e.s.now()
	writeSession(t, e.home, e.proj, "", sid, doubtTranscript(now, 3*time.Minute), now.Add(-3*time.Minute))
	forgetDigests()
	writePane(t, tmuxLog, paneIdleScreen)

	text := body(t, doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/stop", "{}"))
	if strings.Contains(text, "подсказки клиента не узнал") {
		t.Errorf("брошенный вызов поднял сомнение над спокойным окном: %s", text)
	}
	if !strings.Contains(text, "ход не идёт") {
		t.Errorf("честный простой назван иначе: %s", text)
	}
	if lc.contains(t, "подсказки клиента не узнал") {
		t.Errorf("журнал обвинил клиента на брошенном вызове: %v", lc.lines)
	}
}

// Честный простой сомнения не поднимает: ход кончился, незакрытых вызовов в
// журнале нет, и стоп отвечает ровно то, что видит.
func TestChatStopTrustsPlainIdle(t *testing.T) {
	sid := "dff98764-8888-4111-8111-111111111111"
	e, c, tmuxLog := chatWorkLiveEnv(t, sid, "chat-XR-004-1")
	lc := &logCapture{}
	e.s.logf = lc.log
	now := e.s.now()
	writeSession(t, e.home, e.proj, "", sid, stopTranscript(now, "live", true), now.Add(-3*time.Minute))
	forgetDigests()
	writePane(t, tmuxLog, paneIdleScreen)

	text := body(t, doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/stop", "{}"))
	if !strings.Contains(text, "ход не идёт") {
		t.Errorf("честный простой назван иначе: %s", text)
	}
	if lc.contains(t, "подсказки клиента не узнал") {
		t.Errorf("сомнение поднято на честном простое: %v", lc.lines)
	}
}

// Живой снимок настоящего окна: те же экраны, снятые с работающей машины и
// разобранные тем же кодом. Стенд по умолчанию пропускается, файлы снимков
// называет переменная DEVKIT_LIVE_PANES парами «файл=состояние», и клавиш он
// никуда не подаёт.
//
//	DEVKIT_LIVE_PANES='/tmp/ask.txt=вопрос,/tmp/login.txt=вход' go test -run TestPaneStateOnLivePanes -v .
func TestPaneStateOnLivePanes(t *testing.T) {
	spec := os.Getenv("DEVKIT_LIVE_PANES")
	if spec == "" {
		t.Skip("DEVKIT_LIVE_PANES не задан: живые снимки пропущены")
	}
	for _, pair := range strings.Split(spec, ",") {
		name, want, ok := strings.Cut(strings.TrimSpace(pair), "=")
		if !ok {
			t.Fatalf("пара снимка без состояния: %q", pair)
		}
		text := readFile(t, name)
		got := paneState(text, true)
		t.Log(fmt.Sprintf("%s: %s", filepath.Base(name), got))
		if got != want {
			t.Errorf("%s: состояние %q, ждал %q", name, got, want)
		}
	}
}
