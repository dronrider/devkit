package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dronrider/devkit/internal/chat"
	"github.com/dronrider/devkit/internal/taskhead"
	"github.com/dronrider/devkit/internal/taskstate"
)

func TestAppTaskRecoveryRestartsSameThread(t *testing.T) {
	e, c := appTaskEnv(t, "mute")
	appTaskRecoveryFixture(t, e)
	code, first := appTaskStart(t, e, c)
	if code != http.StatusOK {
		t.Fatalf("start %d: %+v", code, first)
	}
	sid := first["session"]
	before := e.s.chatStoreRead(sid)
	text := "Возьми ожидающую реплику"
	if _, err := chat.Put(e.proj, chat.TaskName(before.Task), text, chat.TaskLine(e.s.now(), text)); err != nil {
		t.Fatal(err)
	}
	e.s.appTasksClose()
	waitAppTaskLockGone(t, e)
	e.s.appWorkers.Wait()
	putDoc(t, e.proj, "fake-app-mode", "recover-die")
	// The first continuation accepts the input and drops the connection before
	// responding. Its receipt must be reconciled from the original thread.
	project, ok := e.s.projectByName("demo")
	if !ok {
		t.Fatal("project missing")
	}
	if err := e.s.appTaskRecover(project, sid); err == nil {
		t.Fatal("expected connection to be lost before turn/start acknowledgement")
	}
	waitAppTaskLockGone(t, e)
	e.s.appWorkers.Wait()
	putDoc(t, e.proj, "fake-app-mode", "mute")
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() { e.s.appTaskRecoveryTick(nil) })
	}
	wg.Wait()
	live := e.s.appLive(sid)
	after := e.s.chatStoreRead(sid)
	if live == nil || !live.busyNow() || after.Thread != before.Thread || after.Harness != before.Harness || after.Model != before.Model || after.TaskRunID == before.TaskRunID {
		t.Fatalf("before %+v after %+v live %t", before, after, live != nil)
	}
	if bind := e.s.mainChatBind(before.Task); bind.Chat != sid {
		t.Fatalf("main chat changed: %+v", bind)
	}
	log := readFile(t, filepath.Join(e.proj, "fake-app-log"))
	if strings.Count(log, "start модель-codex") != 1 || strings.Count(log, "resume th-1") != 2 {
		t.Fatalf("incorrect continuation: %s", log)
	}
	count, resume := 0, false
	for _, line := range strings.Split(log, "\n") {
		if raw, ok := strings.CutPrefix(line, "input "); ok {
			var input []map[string]string
			if json.Unmarshal([]byte(raw), &input) != nil || len(input) != 1 {
				continue
			}
			if input[0]["text"] == text {
				count++
			}
			if input[0]["text"] == "Продолжай работу" {
				resume = true
			}
		}
	}
	if !resume || count != 1 || len(chat.ReadLines(chat.Path(e.proj, chat.TaskName(before.Task)))) != 0 {
		t.Fatalf("inbox was not delivered exactly once (%d): %s", count, log)
	}
	count = 0
	for _, item := range e.s.appFeedRead(sid) {
		if item.Role == "user" && item.Text == text {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("feed contains %d copies", count)
	}
	during := "Забери реплику, пришедшую во время восстановления"
	if _, err := chat.Put(e.proj, chat.TaskName(before.Task), during, chat.TaskLine(e.s.now(), during)); err != nil {
		t.Fatal(err)
	}
	if _, err := live.proc.call("test/finish", map[string]any{"keepOpen": true}); err != nil {
		t.Fatal(err)
	}
	appWaitFeed(t, e, sid, during)
	if len(chat.ReadLines(chat.Path(e.proj, chat.TaskName(before.Task)))) != 0 {
		t.Fatal("реплика следующего прохода осталась во входе")
	}
	if !taskhead.Working(taskhead.LockPath(e.home, before.Task)) {
		t.Fatal("later pipeline pass lost its lock")
	}
}

func TestAppTaskRecoveryHonorsStopAndExhaustion(t *testing.T) {
	for _, mode := range []string{"stopped", "exhausted", "model"} {
		t.Run(mode, func(t *testing.T) {
			e, c := appTaskEnv(t, "mute")
			appTaskRecoveryFixture(t, e)
			code, first := appTaskStart(t, e, c)
			if code != http.StatusOK {
				t.Fatalf("start %d: %+v", code, first)
			}
			sid := first["session"]
			e.s.appTasksClose()
			waitAppTaskLockGone(t, e)
			e.s.appWorkers.Wait()
			if mode == "stopped" {
				resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/stop", `{}`)
				if raw := body(t, resp); resp.StatusCode != http.StatusOK {
					t.Fatalf("stop %d: %s", resp.StatusCode, raw)
				}
			} else if mode == "exhausted" {
				putDoc(t, e.proj, "fake-app-mode", "resume-error")
			} else {
				putDoc(t, e.proj, "fake-app-mode", "rejected-model")
				if _, err := e.s.appTaskQueueAdd(sid, "m1", "Ожидающая реплика"); err != nil {
					t.Fatal(err)
				}
			}
			for range appTaskRecoveryLimit + 1 {
				e.s.appTaskRecoveryTick(nil)
				waitAppTaskLockGone(t, e)
				e.s.appWorkers.Wait()
			}
			state, err := taskstate.Acquire(e.home, e.proj, "XR-002")
			if err != nil {
				t.Fatal(err)
			}
			run := state.State.Run
			state.Close()
			if mode == "stopped" {
				if run.AppRecoveryAttempts != 0 || run.AppRecoveryTerminal {
					t.Fatalf("stopped attempted: %+v", run)
				}
				return
			}
			want := appTaskRecoveryLimit
			if mode == "model" {
				want = 1
				entry, err := e.s.appTaskQueueNext(sid)
				if err != nil || entry.Text != "Ожидающая реплика" {
					t.Fatalf("queued input lost: %+v %v", entry, err)
				}
			}
			if run.AppRecoveryAttempts != want || !run.AppRecoveryTerminal || run.AppRecoveryWhy == "" {
				t.Fatalf("wrong recovery outcome: %+v", run)
			}
			appWaitFeed(t, e, sid, "Восстановление конвейера Codex завершилось отказом")
		})
	}
}

func TestAppTaskRecoveryTickSignal(t *testing.T) {
	e, c := appTaskEnv(t, "mute")
	appTaskRecoveryFixture(t, e)
	e.s.cfg.Home = t.TempDir()
	code, first := appTaskStart(t, e, c)
	if code != http.StatusOK {
		t.Fatalf("start %d: %+v", code, first)
	}
	sid := first["session"]
	e.s.appDrop(sid, "test technical disconnect")
	waitTaskStopped(t, e, sid)
	file := filepath.Join(e.home, ".devkit", "app-task-tick")
	if err := os.WriteFile(file, []byte("test tick\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	stop, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		e.s.appTaskRecoveryKeeper(stop)
	}()
	t.Cleanup(func() { close(stop); <-done })
	appWaitFeed(t, e, sid, "Конвейер Codex продолжен в исходном разговоре")
	var params map[string]json.RawMessage
	live := e.s.appLive(sid)
	if live == nil {
		t.Fatal("no recovered app-server")
	}
	if _, err := live.proc.call("test/finish", params); err != nil {
		t.Fatal(err)
	}
	waitTaskStopped(t, e, sid)
	// A later tick leaves a shell that ended normally alone.
	if err := os.WriteFile(file, []byte(time.Now().String()), 0o600); err != nil {
		t.Fatal(err)
	}
	e.s.appTaskRecoveryTick(nil)
	if e.s.appTaskFind(sid, "", "") != nil {
		t.Fatal("terminal closed run was restarted")
	}
}
func TestAppTaskInterruptedLegacy(t *testing.T) {
	home := t.TempDir()
	started := time.Now().Truncate(time.Second)
	logPath := ".devkit/heads/XR-002.log"
	prefix := started.Format(chat.Stamp) + " task-run XR-002: "
	open := prefix + "проход поднят: python3 task-run.py --app-turn-client /tmp/test token\n"
	putDoc(t, home, logPath, open)
	if !appTaskInterruptedLegacy(home, "XR-002", started.Format(time.RFC3339Nano)) {
		t.Fatal("legacy interruption was lost")
	}
	putDoc(t, home, logPath, open+prefix+"голова вышла кодом 0 за 3 с, задача в check\n")
	if appTaskInterruptedLegacy(home, "XR-002", started.Format(time.RFC3339Nano)) {
		t.Fatal("normal legacy completion was mistaken for interruption")
	}
}

func TestAppTaskManualQueueAcknowledgesOnlySentMessage(t *testing.T) {
	e, c := appTaskEnv(t, "mute")
	appTaskRecoveryFixture(t, e)
	code, first := appTaskStart(t, e, c)
	if code != http.StatusOK {
		t.Fatalf("start %d: %+v", code, first)
	}
	sid := first["session"]
	resp := doReq(t, c, "DELETE", e.srv.URL+"/api/projects/demo/runs/XR-002", "")
	if raw := body(t, resp); resp.StatusCode != http.StatusOK {
		t.Fatalf("stop %d: %s", resp.StatusCode, raw)
	}
	waitTaskStopped(t, e, sid)
	if _, err := e.s.appTaskQueueAdd(sid, "first", "Первая реплика"); err != nil {
		t.Fatal(err)
	}
	code, raw := appTaskSay(t, e, c, sid, "Вторая реплика", "second")
	if code != http.StatusAccepted || !strings.Contains(raw, `"way":"queued"`) {
		t.Fatalf("new input was acknowledged early: %d %s", code, raw)
	}
	if live := e.s.appLive(sid); live != nil {
		if _, err := live.proc.call("test/finish", map[string]any{"keepOpen": true}); err != nil {
			t.Fatal(err)
		}
	}
	waitTaskStopped(t, e, sid)
	entry, err := e.s.appTaskQueueNext(sid)
	if err != nil || entry.Claim != "second" || entry.Sent {
		t.Fatalf("new input did not stay in queue: %+v %v", entry, err)
	}
	putDoc(t, e.proj, "fake-app-mode", "reply-open")
	code, raw = appTaskSay(t, e, c, sid, "Третья реплика", "third")
	if code != http.StatusAccepted {
		t.Fatalf("third input was acknowledged early: %d %s", code, raw)
	}
	waitTaskStopped(t, e, sid)
	appWaitFeed(t, e, sid, "ответ на реплику: Вторая реплика")
	entry, err = e.s.appTaskQueueNext(sid)
	if err != nil || entry.Claim != "third" || entry.Sent {
		t.Fatalf("reply-once processed more than one queued input: %+v %v", entry, err)
	}
	if log := readFile(t, filepath.Join(e.proj, "fake-app-log")); strings.Count(log, "turn high") != 3 {
		t.Fatalf("initial run and two explicit replies must use three turns: %s", log)
	}
}

func TestAppTaskRecoveryBudgetWaitsForWork(t *testing.T) {
	e, c := appTaskEnv(t, "mute")
	appTaskRecoveryFixture(t, e)
	ledger := filepath.Join(e.home, ".devkit", "recovery.jsonl")
	t.Setenv("DEVKIT_RECOVERY_LOG", ledger)
	code, first := appTaskStart(t, e, c)
	if code != http.StatusOK {
		t.Fatalf("start %d: %+v", code, first)
	}
	sid := first["session"]
	e.s.appTasksClose()
	waitAppTaskLockGone(t, e)
	e.s.appWorkers.Wait()

	putDoc(t, e.proj, "fake-app-mode", "ack-only")
	project, ok := e.s.projectByName("demo")
	if !ok {
		t.Fatal("project missing")
	}
	text := "В очереди до доказанного хода"
	if _, err := e.s.appTaskQueueAdd(sid, "budget-reply", text); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- e.s.appTaskRecover(project, sid) }()
	appWaitFeed(t, e, sid, text)
	type recoveryRecord struct {
		Kind, Attempt, Outcome string
	}
	readLedger := func() []recoveryRecord {
		t.Helper()
		var records []recoveryRecord
		for _, line := range strings.Split(strings.TrimSpace(readFile(t, ledger)), "\n") {
			var record recoveryRecord
			if err := json.Unmarshal([]byte(line), &record); err != nil {
				t.Fatal(err)
			}
			records = append(records, record)
		}
		return records
	}
	before := readLedger()
	if len(before) != 1 || before[0].Kind != "reserve" || before[0].Attempt == "" {
		t.Fatalf("один ack должен оставить открытый резерв: %+v", before)
	}
	live := e.s.appLive(sid)
	if live == nil {
		t.Fatal("ack не оставил открытый ход")
	}
	if _, err := live.proc.call("test/finish", map[string]any{"keepOpen": true, "status": "failed"}); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err == nil {
		t.Fatal("ack без рабочей активности был засчитан возобновлением")
	}
	waitTaskStopped(t, e, sid)
	after := readLedger()
	if len(after) != 2 || after[1].Kind != "result" || after[1].Attempt != before[0].Attempt || after[1].Outcome != "launch-failed" {
		t.Fatalf("неверный исход неподтверждённой попытки: %+v", after)
	}
	for _, item := range e.s.appFeedRead(sid) {
		if strings.Contains(item.Text, "Конвейер Codex продолжен") {
			t.Fatal("лента сообщила об успехе после одного ack")
		}
	}
}

func TestAppTaskManualRetryPreservesTerminalUntilSuccess(t *testing.T) {
	e, c := appTaskEnv(t, "mute")
	appTaskRecoveryFixture(t, e)
	code, first := appTaskStart(t, e, c)
	if code != http.StatusOK {
		t.Fatalf("start %d: %+v", code, first)
	}
	sid := first["session"]
	e.s.appTasksClose()
	waitAppTaskLockGone(t, e)
	e.s.appWorkers.Wait()
	text := "Реплика после отказа"
	code, raw := appTaskSay(t, e, c, sid, text, "terminal-first")
	if code != http.StatusAccepted {
		t.Fatalf("ожидающая восстановления реплика: %d %s", code, raw)
	}
	putDoc(t, e.proj, "fake-app-mode", "rejected-model")
	e.s.appTaskRecoveryTick(nil)
	waitTaskStopped(t, e, sid)

	readRun := func() taskstate.Run {
		t.Helper()
		state, err := taskstate.Acquire(e.home, e.proj, "XR-002")
		if err != nil {
			t.Fatal(err)
		}
		defer state.Close()
		if state.State.Run == nil {
			t.Fatal("состояние запуска потеряно")
		}
		return *state.State.Run
	}
	terminal := readRun()
	if !terminal.AppRecoveryTerminal || terminal.AppRecoveryWhy == "" {
		t.Fatalf("нет исходного отказа: %+v", terminal)
	}

	code, raw = appTaskSay(t, e, c, sid, text, "terminal-first")
	if code != http.StatusBadGateway {
		t.Fatalf("ручной отказ: %d %s", code, raw)
	}
	waitTaskStopped(t, e, sid)
	rejected := readRun()
	if rejected.ID == terminal.ID || !rejected.AppRecoveryTerminal || rejected.AppRecoveryWhy != terminal.AppRecoveryWhy || rejected.AppChat != sid {
		t.Fatalf("ручной отказ потерял защиту: %+v", rejected)
	}
	entry, err := e.s.appTaskQueueNext(sid)
	if err != nil || entry.Claim != "terminal-first" || entry.Accepted {
		t.Fatalf("ручной отказ потерял реплику: %+v %v", entry, err)
	}

	putDoc(t, e.proj, "fake-app-mode", "mute")
	code, raw = appTaskSay(t, e, c, sid, "Следующая ручная реплика", "terminal-second")
	if code != http.StatusAccepted {
		t.Fatalf("новая реплика должна ждать: %d %s", code, raw)
	}
	working := readRun()
	if !working.AppRecoveryTerminal || working.AppRecoveryWhy != terminal.AppRecoveryWhy {
		t.Fatalf("защита снялась до завершения хода: %+v", working)
	}
	live := e.s.appLive(sid)
	if live == nil {
		t.Fatal("ручной клиент не поднялся")
	}
	if _, err := live.proc.call("test/finish", map[string]any{"keepOpen": true}); err != nil {
		t.Fatal(err)
	}
	waitTaskStopped(t, e, sid)
	finished := readRun()
	if finished.AppRecoveryTerminal || finished.AppRecoveryWhy != "" || finished.AppRecoveryAttempts != 0 || finished.AppRecovery {
		t.Fatalf("успешный ответ не снял отказ: %+v", finished)
	}
	entry, err = e.s.appTaskQueueNext(sid)
	if err != nil || entry.Claim != "terminal-second" || entry.Sent {
		t.Fatalf("следующая реплика не дождалась своего хода: %+v %v", entry, err)
	}
}

func TestAppTaskRecoveryReportsChangedOwner(t *testing.T) {
	e, c := appTaskEnv(t, "mute")
	appTaskRecoveryFixture(t, e)
	code, first := appTaskStart(t, e, c)
	if code != http.StatusOK {
		t.Fatalf("start %d: %+v", code, first)
	}
	sid := first["session"]
	e.s.appTasksClose()
	waitTaskStopped(t, e, sid)
	e.s.appWorkers.Wait()

	e.s.mainChatAssign("XR-002", "as-other-owner", "demo", "работа")
	e.s.appTaskRecoveryTick(nil)
	appWaitFeed(t, e, sid, "Восстановление конвейера Codex невозможно: основной рабочий разговор сменился")
	code, raw := appTaskSay(t, e, c, sid, "Реплика из исходного разговора", "old-owner")
	if code != http.StatusConflict || !strings.Contains(raw, "основной рабочий разговор сменился") {
		t.Fatalf("реплика в прежний разговор: %d %s", code, raw)
	}
	entry, err := e.s.appTaskQueueNext(sid)
	if err != nil || entry.Claim != "old-owner" || entry.Sent {
		t.Fatalf("реплика из прежнего разговора потерялась: %+v %v", entry, err)
	}
	if e.s.appTaskFind(sid, "", "") != nil || e.s.mainChatBind("XR-002").Chat != "as-other-owner" {
		t.Fatal("восстановление запустило прежнюю нить или сменило основное назначение")
	}
}

func TestAppTaskManualRetryReconcilesExactMessage(t *testing.T) {
	e, c := appTaskEnv(t, "mute")
	appTaskRecoveryFixture(t, e)
	code, first := appTaskStart(t, e, c)
	if code != http.StatusOK {
		t.Fatalf("start %d: %+v", code, first)
	}
	sid := first["session"]
	resp := doReq(t, c, "DELETE", e.srv.URL+"/api/projects/demo/runs/XR-002", "")
	if raw := body(t, resp); resp.StatusCode != http.StatusOK {
		t.Fatalf("stop %d: %s", resp.StatusCode, raw)
	}
	waitTaskStopped(t, e, sid)

	putDoc(t, e.proj, "fake-app-mode", "recover-die")
	text := "Реплика с потерянной квитанцией"
	code, raw := appTaskSay(t, e, c, sid, text, "lost-ack")
	if code != http.StatusBadGateway {
		t.Fatalf("неподтверждённый ручной ход: %d %s", code, raw)
	}
	waitTaskStopped(t, e, sid)

	putDoc(t, e.proj, "fake-app-mode", "reply-open")
	code, raw = appTaskSay(t, e, c, sid, text, "lost-ack")
	if code != http.StatusOK {
		t.Fatalf("повтор после сверенной квитанции: %d %s", code, raw)
	}
	waitTaskStopped(t, e, sid)
	appWaitFeed(t, e, sid, "ответ на реплику: Продолжай работу")
	inputs, originalModel := 0, 0
	log := readFile(t, filepath.Join(e.proj, "fake-app-log"))
	for _, line := range strings.Split(log, "\n") {
		if raw, ok := strings.CutPrefix(line, "params "); ok {
			var params struct{ Model string }
			if err := json.Unmarshal([]byte(raw), &params); err != nil {
				t.Fatal(err)
			}
			if params.Model == "модель-codex" {
				originalModel++
			}
		}
		raw, ok := strings.CutPrefix(line, "input ")
		if !ok {
			continue
		}
		var input []map[string]string
		if err := json.Unmarshal([]byte(raw), &input); err != nil {
			t.Fatal(err)
		}
		for _, item := range input {
			if item["text"] == text {
				inputs++
			}
		}
	}
	if originalModel != 2 {
		t.Fatalf("ручные ходы не передали исходную модель: %s", log)
	}
	if inputs != 1 {
		t.Fatalf("одна принятая реплика ушла %d раз: %s", inputs, log)
	}
}

func TestAppTaskRecoveryHonorsLiveStopFromMachineHome(t *testing.T) {
	for _, mode := range []string{"chat", "run"} {
		t.Run(mode, func(t *testing.T) {
			e, c := appTaskEnv(t, "mute")
			appTaskRecoveryFixture(t, e)
			e.s.cfg.Home = t.TempDir()
			code, first := appTaskStart(t, e, c)
			if code != http.StatusOK {
				t.Fatalf("start %d: %+v", code, first)
			}
			sid := first["session"]
			method, url, request := "POST", "/api/projects/demo/chats/"+sid+"/stop", "{}"
			if mode == "run" {
				method, url, request = "DELETE", "/api/projects/demo/runs/XR-002", ""
			}
			resp := doReq(t, c, method, e.srv.URL+url, request)
			if raw := body(t, resp); resp.StatusCode != http.StatusOK {
				t.Fatalf("stop %d: %s", resp.StatusCode, raw)
			}
			waitTaskStopped(t, e, sid)
			state, err := taskstate.Acquire(e.home, e.proj, "XR-002")
			if err != nil {
				t.Fatal(err)
			}
			stopped := state.State.Stopped
			state.Close()
			if !stopped {
				t.Fatal("ручной стоп не записан рядом с состоянием запуска")
			}
			e.s.appTaskRecoveryTick(nil)
			if e.s.appTaskFind(sid, "", "") != nil {
				t.Fatal("тик восстановил остановленный запуск")
			}
		})
	}
}

func TestAppTaskRecoveryMigratesLegacyStopFromConfigHome(t *testing.T) {
	for _, mode := range []string{"current-stop", "earlier-stop"} {
		t.Run(mode, func(t *testing.T) {
			e, c := appTaskEnv(t, "mute")
			appTaskRecoveryFixture(t, e)
			e.s.cfg.Home = t.TempDir()
			code, first := appTaskStart(t, e, c)
			if code != http.StatusOK {
				t.Fatalf("start %d: %+v", code, first)
			}
			sid := first["session"]
			e.s.appTasksClose()
			waitTaskStopped(t, e, sid)
			e.s.appWorkers.Wait()
			st := e.s.chatStoreRead(sid)
			st.TaskRun = true
			if err := e.s.chatStoreWrite(sid, st); err != nil {
				t.Fatal(err)
			}

			state, err := taskstate.Acquire(e.home, e.proj, st.Task)
			if err != nil {
				t.Fatal(err)
			}
			state.State.Run.AppChat, state.State.Run.AppRecovery = "", false
			started := time.Now().Add(-time.Minute)
			state.State.Run.Started = started.Format(time.RFC3339Nano)
			err = state.Save()
			state.Close()
			if err != nil {
				t.Fatal(err)
			}
			if err := taskstate.SetStopped(e.s.cfg.Home, e.proj, st.Task, true); err != nil {
				t.Fatal(err)
			}
			if mode == "earlier-stop" {
				path, _, err := taskstate.Path(e.s.cfg.Home, e.proj, st.Task)
				if err != nil {
					t.Fatal(err)
				}
				earlier := started.Add(-time.Minute)
				if err := os.Chtimes(path, earlier, earlier); err != nil {
					t.Fatal(err)
				}
			}
			e.s.appTaskRecoveryTick(nil)
			if mode == "earlier-stop" {
				appWaitFeed(t, e, sid, "Конвейер Codex продолжен в исходном разговоре")
				return
			}
			state, err = taskstate.Acquire(e.home, e.proj, st.Task)
			if err != nil {
				t.Fatal(err)
			}
			defer state.Close()
			if !state.State.Stopped || state.State.Run.ID != st.TaskRunID || state.State.Run.AppChat != "" || e.s.appTaskFind(sid, "", "") != nil {
				t.Fatalf("старый явный стоп потерян или старый запуск подменён: %+v", state.State)
			}
		})
	}
}
