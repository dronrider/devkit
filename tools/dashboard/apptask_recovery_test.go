package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/dronrider/devkit/internal/chat"
	"github.com/dronrider/devkit/internal/mainchat"
	"github.com/dronrider/devkit/internal/taskhead"
	"github.com/dronrider/devkit/internal/taskstate"
)

func TestAppTaskRecoveryPreservesWaitAndQuotaDecodeErrors(t *testing.T) {
	e, _ := appTaskEnv(t, "mute")
	appTaskRecoveryFixture(t, e)
	project, ok := e.s.projectByName("demo")
	if !ok {
		t.Fatal("project missing")
	}
	putDoc(t, e.home, ".devkit/waits/XR-002.json", "{")
	eligible, err := e.s.appTaskRecoveryEligible(project, chatStore{Task: "XR-002"})
	var decodeErr *json.SyntaxError
	if eligible || !errors.As(err, &decodeErr) {
		t.Errorf("ожидалась причина декодирования отметки ожидания: %v", err)
	}

	writeScript(t, e.bin, "agentctl", `printf '%s' '{'`)
	err = appTaskRecoveryQuota(e.proj, "codextest")
	if !errors.As(err, &decodeErr) {
		t.Errorf("ожидалась причина декодирования ответа о квоте: %v", err)
	}
}

func TestAppTaskAcceptSayExplainsUnreadableQueue(t *testing.T) {
	e, _ := appTaskEnv(t, "mute")
	const sid = "as-thread"
	putDoc(t, e.s.cfg.Home, ".devkit/chats/app-task-queue/"+sid+".json", "{")
	answer := httptest.NewRecorder()
	e.s.appTaskAcceptSay(answer, sid, "Реплика пользователя", "id")
	if answer.Code != http.StatusBadGateway || !strings.Contains(answer.Body.String(), "не удалось подтвердить сохранение реплики в очереди рабочего разговора Codex") ||
		!strings.Contains(answer.Body.String(), "unexpected end of JSON input") {
		t.Fatalf("ответ не объясняет отказ сохранения и причину: %d %s", answer.Code, answer.Body.String())
	}
}

func waitAppTaskRecoveryCarrierExit(t *testing.T, e *testEnv, sid string) {
	t.Helper()
	st := e.s.chatStoreRead(sid)
	state, err := taskstate.Acquire(e.home, e.proj, st.Task)
	if err != nil {
		t.Fatal(err)
	}
	run := state.State.Run
	state.Close()
	if run == nil || run.ID != st.TaskRunID {
		t.Fatal("состояние исходного запуска отсутствует")
	}

	// Оболочка снимает замок до выхода. Восстановление ждёт, пока
	// завершатся и её процесс, и группа.
	deadline := time.After(10 * time.Second)
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for taskhead.Alive(run.PID) || run.Group > 0 && !errors.Is(syscall.Kill(-run.Group, 0), syscall.ESRCH) {
		select {
		case <-deadline:
			t.Fatalf("исходная оболочка не вышла: pid %d, группа %d", run.PID, run.Group)
		case <-tick.C:
		}
	}
}

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
	waitAppTaskRecoveryCarrierExit(t, e, sid)
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

func TestAppTaskRecoveryReportsQueueImportFailure(t *testing.T) {
	e, c := appTaskEnv(t, "mute")
	appTaskRecoveryFixture(t, e)
	code, first := appTaskStart(t, e, c)
	if code != http.StatusOK {
		t.Fatalf("start %d: %+v", code, first)
	}
	sid := first["session"]
	before := e.s.chatStoreRead(sid)
	e.s.appTasksClose()
	waitAppTaskLockGone(t, e)
	e.s.appWorkers.Wait()
	waitAppTaskRecoveryCarrierExit(t, e, sid)

	const text = "Сохрани ожидающую реплику"
	name := chat.TaskName(before.Task)
	if _, err := chat.Put(e.proj, name, text, chat.TaskLine(e.s.now(), text)); err != nil {
		t.Fatal(err)
	}
	queue := filepath.Join(chatStoreDir(e.s.cfg.Home), "app-task-queue", sid+".json")
	if err := os.MkdirAll(queue, 0o755); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		e.s.appTaskRecoveryTick(nil)
	}
	notes := 0
	for _, item := range e.s.appFeedRead(sid) {
		if item.Role == "note" && item.Fail && strings.Contains(item.Text, "не перенесены ожидающие реплики") {
			notes++
		}
	}
	if notes != 1 {
		t.Fatalf("в исходном чате нужна одна причина отказа переноса, получено %d", notes)
	}
	input := chat.ReadLines(chat.Path(e.proj, name))
	if len(input) != 1 || chat.Said(input[0]) != text || e.s.appTaskFind(sid, "", "") != nil {
		t.Fatalf("перенос с отказом изменил вход или запустил задачу: %v", input)
	}

	if err := os.Remove(queue); err != nil {
		t.Fatal(err)
	}
	e.s.appTaskRecoveryTick(nil)
	appWaitFeed(t, e, sid, text)
	after := e.s.chatStoreRead(sid)
	if after.Thread != before.Thread || after.TaskRunID == before.TaskRunID {
		t.Fatalf("после восстановления очереди не продолжилась исходная нить: %q -> %q", before.Thread, after.Thread)
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
			waitAppTaskRecoveryCarrierExit(t, e, sid)
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
				waitAppTaskRecoveryCarrierExit(t, e, sid)
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

func TestAppTaskRecoveryReportsExhaustionWithQuotaUnavailable(t *testing.T) {
	e, c := appTaskEnv(t, "mute")
	appTaskRecoveryFixture(t, e)
	code, first := appTaskStart(t, e, c)
	if code != http.StatusOK {
		t.Fatalf("start %d: %+v", code, first)
	}
	sid := first["session"]
	st := e.s.chatStoreRead(sid)
	e.s.appTasksClose()
	waitAppTaskLockGone(t, e)
	e.s.appWorkers.Wait()
	waitAppTaskRecoveryCarrierExit(t, e, sid)

	// A restart can leave the persisted attempt counter at its limit
	// before the recovery outcome has been recorded.
	const previous = "клиент не подтвердил ход"
	state, err := taskstate.Acquire(e.home, e.proj, st.Task)
	if err != nil {
		t.Fatal(err)
	}
	state.State.Run.AppRecoveryAttempts = appTaskRecoveryLimit
	state.State.Run.AppRecoveryWhy = previous
	err = state.Save()
	state.Close()
	if err != nil {
		t.Fatal(err)
	}
	writeScript(t, e.bin, "agentctl", `case "$1" in
  quota) printf '%s' 'quota unavailable' >&2; exit 1 ;;
  *) printf '%s' '`+appHarnessJSON+`' ;;
esac`)

	if err := e.s.appTaskRecoveryTick(nil); err != nil {
		t.Fatal(err)
	}
	state, err = taskstate.Acquire(e.home, e.proj, st.Task)
	if err != nil {
		t.Fatal(err)
	}
	run := state.State.Run
	state.Close()
	const reason = "исчерпаны попытки восстановления: " + previous
	if run.ID != st.TaskRunID || !run.AppRecoveryTerminal || run.AppRecoveryWhy != reason {
		t.Fatalf("исчерпание попыток не записано в прежнем запуске: %+v", run)
	}
	appWaitFeed(t, e, sid, "Восстановление конвейера Codex завершилось отказом: "+reason)
}

func TestAppTaskRecoveryRetriesUnreadableChatDirectory(t *testing.T) {
	e, c := appTaskEnv(t, "mute")
	appTaskRecoveryFixture(t, e)
	readFailed := make(chan struct{}, 1)
	e.s.logf = func(format string, args ...any) {
		if strings.Contains(fmt.Sprintf(format, args...), "не прочитан каталог чатов") {
			select {
			case readFailed <- struct{}{}:
			default:
			}
		}
	}
	code, first := appTaskStart(t, e, c)
	if code != http.StatusOK {
		t.Fatalf("start %d: %+v", code, first)
	}
	sid := first["session"]
	e.s.appTasksClose()
	waitAppTaskLockGone(t, e)
	e.s.appWorkers.Wait()
	waitAppTaskRecoveryCarrierExit(t, e, sid)

	dir := chatStoreDir(e.s.cfg.Home)
	saved := dir + "-saved"
	if err := os.Rename(dir, saved); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir, []byte("каталог заблокирован"), 0o600); err != nil {
		t.Fatal(err)
	}
	tick := filepath.Join(e.home, ".devkit", "app-task-tick")
	if err := os.WriteFile(tick, []byte("единственный тик\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	stop, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		e.s.appTaskRecoveryKeeper(stop)
	}()
	defer func() { close(stop); <-done }()
	select {
	case <-readFailed:
	case <-time.After(8 * time.Second):
		t.Fatal("ошибка чтения каталога чатов не попала в журнал")
	}

	if err := os.Remove(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(saved, dir); err != nil {
		t.Fatal(err)
	}
	appWaitFeed(t, e, sid, "Конвейер Codex продолжен в исходном разговоре")
}

func TestAppTaskBudgetCommandPreservesDeadline(t *testing.T) {
	e, _ := appTaskEnv(t, "mute")
	script := filepath.Join(filepath.Dir(e.proj), "devkit", "tools", "devkitctl", "recovery.py")
	body := []byte("import time\nprint('ожидаю журнал', flush=True)\ntime.sleep(60)\n")
	if err := os.WriteFile(script, body, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := e.s.appTaskBudgetCommand(e.home, "app-reserve", e.proj, "XR-002", "test")
	if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "ожидаю журнал") {
		t.Fatalf("команда бюджета потеряла срок ожидания или вывод: %v", err)
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
func TestAppTaskRecoveryReportsAmbiguousLegacyOnce(t *testing.T) {
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
	st := e.s.chatStoreRead(sid)
	st.TaskRun = false
	if err := e.s.chatStoreWrite(sid, st); err != nil {
		t.Fatal(err)
	}
	state, err := taskstate.Acquire(e.home, e.proj, st.Task)
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now().Add(-time.Minute).Truncate(time.Second)
	state.State.Run.AppChat, state.State.Run.AppRecovery = "", false
	state.State.Run.Started = started.Format(time.RFC3339Nano)
	if err := state.Save(); err != nil {
		t.Fatal(err)
	}
	state.Close()
	putDoc(t, e.home, ".devkit/heads/"+st.Task+".log", started.Format(chat.Stamp)+" task-run "+st.Task+": проход поднят: task-run.py --app-turn-client\n")
	project, ok := e.s.projectByName("demo")
	if !ok {
		t.Fatal("project missing")
	}
	const reason = "Восстановление конвейера Codex ждёт: журнал не определяет поколение запуска; нужен разбор"
	// Чужой запуск не должен оставлять диагностику в этом чате.
	st.TaskRunID = "other"
	if err := e.s.chatStoreWrite(sid, st); err != nil {
		t.Fatal(err)
	}
	if err := e.s.appTaskRecover(project, sid); err != nil {
		t.Fatal(err)
	}
	for _, item := range e.s.appFeedRead(sid) {
		if item.Text == reason {
			t.Fatal("чужой запуск получил диагностику")
		}
	}
	state, err = taskstate.Acquire(e.home, e.proj, st.Task)
	if err != nil {
		t.Fatal(err)
	}
	st.TaskRunID = state.State.Run.ID
	state.Close()
	if err := e.s.chatStoreWrite(sid, st); err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.appTaskRecoveryStore("as-missing"); err != nil {
		t.Fatalf("исчезнувший чат: %v", err)
	}
	broken := filepath.Join(chatStoreDir(e.s.cfg.Home), "as-broken.json")
	if err := os.WriteFile(broken, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(chatStoreDir(e.s.cfg.Home), "tmux-broken.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := e.s.appTaskRecoveryTick(nil); err == nil || !strings.Contains(err.Error(), "as-broken") {
		t.Fatalf("не сообщена повреждённая запись: %v", err)
	}
	found := false
	for _, item := range e.s.appFeedRead(sid) {
		if item.Text == reason {
			found = true
		}
	}
	if !found {
		t.Fatal("ошибка другой записи остановила обработку исходного чата")
	}
	if err := os.WriteFile(broken, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := e.s.appTaskRecoveryTick(nil); err != nil {
		t.Fatalf("исправленная запись не прочитана: %v", err)
	}
	count := 0
	for _, item := range e.s.appFeedRead(sid) {
		if item.Text == reason {
			count++
		}
	}
	if count != 1 || e.s.appTaskFind(sid, "", "") != nil {
		t.Fatalf("диагностика %d, запуск не должен возобновляться", count)
	}
	script := filepath.Join(filepath.Dir(e.proj), "devkit", "tools", "devkitctl", "recovery.py")
	if err := os.WriteFile(script, []byte("raise RuntimeError('bridge failure')\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := e.s.appTaskRecover(project, sid); err == nil || !strings.Contains(err.Error(), "bridge failure") {
		t.Fatalf("ошибка вызова потеряна: %v", err)
	}
	found = false
	for _, item := range e.s.appFeedRead(sid) {
		if strings.Contains(item.Text, "старый журнал") && strings.Contains(item.Text, "bridge failure") {
			found = true
		}
	}
	if !found || e.s.appTaskFind(sid, "", "") != nil {
		t.Fatal("причина ошибки не показана или запуск возобновлён")
	}
}

func TestAppTaskInterruptedLegacy(t *testing.T) {
	e, _ := appTaskEnv(t, "mute")
	home := t.TempDir()
	read := func(start string) taskhead.LegacyAppPassStatus {
		t.Helper()
		status, err := e.s.appTaskInterruptedLegacy(home, "XR-002", start)
		if err != nil {
			t.Fatal(err)
		}
		return status
	}
	started := time.Date(2026, time.January, 2, 12, 0, 0, 0, time.Local)
	logPath := ".devkit/heads/XR-002.log"
	prefix := started.Add(time.Second).Format(chat.Stamp) + " task-run XR-002: "
	open := prefix + "проход поднят: python3 task-run.py --app-turn-client /tmp/test token\n"
	putDoc(t, home, logPath, started.Format(chat.Stamp)+" task-run XR-002: проход поднят: python3 task-run.py --app-turn-client\n")
	modified := started.Add(time.Second)
	if err := os.Chtimes(filepath.Join(home, logPath), modified, modified); err != nil {
		t.Fatal(err)
	}
	if read(started.Format(time.RFC3339Nano)) != taskhead.LegacyAppPassAmbiguous {
		t.Fatal("same-second legacy entry cannot identify the current generation")
	}
	if read(started.Add(500*time.Millisecond).Format(time.RFC3339Nano)) != taskhead.LegacyAppPassAmbiguous {
		t.Fatal("same-second entry with fractional start cannot identify the current generation")
	}
	precise := started.Add(123456789 * time.Nanosecond)
	for _, tc := range []struct {
		fraction time.Duration
		want     taskhead.LegacyAppPassStatus
	}{
		{123456100, taskhead.LegacyAppPassNone}, {123456788, taskhead.LegacyAppPassNone},
		{123456789, taskhead.LegacyAppPassAmbiguous}, {123456790, taskhead.LegacyAppPassAmbiguous},
	} {
		modified := started.Add(tc.fraction)
		if err := os.Chtimes(filepath.Join(home, logPath), modified, modified); err != nil {
			t.Fatal(err)
		}
		if got := read(precise.Format(time.RFC3339Nano)); got != tc.want {
			t.Fatalf("mtime %v: got %s, want %s", tc.fraction, got, tc.want)
		}
	}
	putDoc(t, home, logPath, open)
	if read(started.Format(time.RFC3339Nano)) != taskhead.LegacyAppPassActive {
		t.Fatal("legacy interruption was lost")
	}
	putDoc(t, home, logPath, open+prefix+"голова вышла кодом 0 за 3 с, задача в check\n")
	if read(started.Format(time.RFC3339Nano)) != taskhead.LegacyAppPassNone {
		t.Fatal("normal legacy completion was mistaken for interruption")
	}
	if _, err := e.s.appTaskInterruptedLegacy(home, "XR-002", "bad"); err == nil || !strings.Contains(err.Error(), "неверное время старта") {
		t.Fatalf("повреждённое время старта ошибочно сняло удержание: %v", err)
	}
	script := filepath.Join(filepath.Dir(e.proj), "devkit", "tools", "devkitctl", "recovery.py")
	if err := os.WriteFile(script, []byte("raise RuntimeError('bridge failure')\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.appTaskInterruptedLegacy(home, "XR-002", started.Format(time.RFC3339Nano)); err == nil || !strings.Contains(err.Error(), "старый журнал") || !strings.Contains(err.Error(), "bridge failure") {
		t.Fatalf("ошибка чтения журнала потеряна: %v", err)
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

func TestAppTaskRecoveryReportsUnreadableOwnerJournal(t *testing.T) {
	t.Run("current", func(t *testing.T) { testAppTaskUnreadableOwnerJournal(t, false) })
	t.Run("legacy", func(t *testing.T) { testAppTaskUnreadableOwnerJournal(t, true) })
}

func testAppTaskUnreadableOwnerJournal(t *testing.T, legacy bool) {
	t.Helper()
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
	waitAppTaskRecoveryCarrierExit(t, e, sid)

	if legacy {
		st := e.s.chatStoreRead(sid)
		st.TaskRun = true
		if err := e.s.chatStoreWrite(sid, st); err != nil {
			t.Fatal(err)
		}
		state, err := taskstate.Acquire(e.home, e.proj, st.Task)
		if err != nil {
			t.Fatal(err)
		}
		state.State.Run.AppChat = ""
		err = state.Save()
		state.Close()
		if err != nil {
			t.Fatal(err)
		}
	}

	path := mainchat.Path(e.s.cfg.Home)
	saved := path + ".saved"
	if err := os.Rename(path, saved); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	tick := filepath.Join(e.home, ".devkit", "app-task-tick")
	if err := os.WriteFile(tick, []byte("единственный тик\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	readFailed := make(chan struct{}, 1)
	e.s.logf = func(format string, args ...any) {
		if strings.Contains(fmt.Sprintf(format, args...), "не прочитан журнал назначений чатов") {
			select {
			case readFailed <- struct{}{}:
			default:
			}
		}
	}
	stop, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		e.s.appTaskRecoveryKeeper(stop)
	}()
	defer func() { close(stop); <-done }()
	select {
	case <-readFailed:
	case <-time.After(8 * time.Second):
		t.Fatal("ошибка чтения назначения не попала в журнал")
	}
	appWaitFeed(t, e, sid, "не прочитан журнал назначений чатов")
	if got := readFile(t, filepath.Join(e.proj, "fake-app-log")); strings.Contains(got, "resume ") {
		t.Fatalf("запуск до проверки владельца: %s", got)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(saved, path); err != nil {
		t.Fatal(err)
	}
	appWaitFeed(t, e, sid, "Конвейер Codex продолжен в исходном разговоре")
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
	for _, mode := range []string{"current-stop", "earlier-stop", "unreadable-stop"} {
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
			if mode == "unreadable-stop" {
				path, _, err := taskstate.Path(e.s.cfg.Home, e.proj, st.Task)
				if err != nil {
					t.Fatal(err)
				}
				saved, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := e.s.appTaskRecoveryTick(nil); !errors.Is(err, errAppTaskRecoveryRead) {
					t.Fatalf("ошибка чтения прежнего стопа потеряна: %v", err)
				}
				if e.s.appTaskFind(sid, "", "") != nil {
					t.Fatal("восстановление обошло нечитаемое состояние стопа")
				}
				if err := os.WriteFile(path, saved, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if err := e.s.appTaskRecoveryTick(nil); err != nil {
				t.Fatal(err)
			}
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

func TestAppTaskRecoveryDeliversRepeatedTaskMessage(t *testing.T) {
	e, c := appTaskEnv(t, "mute")
	appTaskRecoveryFixture(t, e)
	runner := filepath.Join(filepath.Dir(e.proj), "devkit", taskhead.TaskRunRel)
	runnerBody := readFile(t, runner)
	if !strings.Contains(runnerBody, "IDLE_LIMIT = 3") {
		t.Fatal("idle limit missing from fixture")
	}
	if err := os.WriteFile(runner, []byte(strings.Replace(runnerBody, "IDLE_LIMIT = 3", "IDLE_LIMIT = 10", 1)), 0o755); err != nil {
		t.Fatal(err)
	}
	code, first := appTaskStart(t, e, c)
	if code != http.StatusOK {
		t.Fatalf("start %d: %+v", code, first)
	}
	sid := first["session"]
	e.s.appTasksClose()
	waitTaskStopped(t, e, sid)
	e.s.appWorkers.Wait()
	waitAppTaskRecoveryCarrierExit(t, e, sid)

	project, ok := e.s.projectByName("demo")
	if !ok {
		t.Fatal("project missing")
	}
	if err := e.s.appTaskRecover(project, sid); err != nil {
		t.Fatal(err)
	}
	live := e.s.appLive(sid)
	if live == nil {
		t.Fatal("recovered client missing")
	}

	text := "Повтори проверку"
	stamp := time.Now().Truncate(time.Minute)
	e.s.now = func() time.Time { return stamp }
	say := func() {
		t.Helper()
		resp := postTaskMessage(t, c, e, "XR-002", text)
		raw := body(t, resp)
		t.Logf("message response: %s", raw)
		if resp.StatusCode != http.StatusOK || strings.Contains(raw, `"repeat":true`) {
			t.Fatalf("new task message %d: %s", resp.StatusCode, raw)
		}
	}
	finish := func() {
		t.Helper()
		a := e.s.appTaskFind(sid, "", "")
		if a == nil {
			t.Fatal("recovered task missing")
		}
		a.lifecycle.Lock()
		a.lifecycle.Unlock()
		if _, err := live.proc.call("test/finish", map[string]bool{"keepOpen": true}); err != nil {
			t.Fatal(err)
		}
	}
	inputCount := func(log, text string) int {
		count := 0
		for _, line := range strings.Split(log, "\n") {
			raw, ok := strings.CutPrefix(line, "input ")
			if !ok {
				continue
			}
			var parts []struct{ Text string }
			if json.Unmarshal([]byte(raw), &parts) == nil && len(parts) == 1 && parts[0].Text == text {
				count++
			}
		}
		return count
	}
	waitForInput := func(text string, want int) {
		t.Helper()
		deadline := time.After(5 * time.Second)
		tick := time.NewTicker(10 * time.Millisecond)
		defer tick.Stop()
		for {
			log := readFile(t, filepath.Join(e.proj, "fake-app-log"))
			count := inputCount(log, text)
			if count == want {
				return
			}
			if count > want {
				t.Fatalf("input %q duplicated: got %d, want %d", text, count, want)
			}
			select {
			case <-tick.C:
			case <-deadline:
				t.Fatalf("input %q: got %d, want %d; inbox: %v; log: %s", text, count, want, chat.ReadLines(chat.Path(e.proj, chat.TaskName("XR-002"))), log)
			}
		}
	}

	say()
	finish()
	waitForInput(text, 1)
	finish()
	waitForInput("Продолжай выполнение XR-002", 2)
	say()
	finish()
	waitForInput(text, 2)
	finish()
	waitForInput("Продолжай выполнение XR-002", 3)
	if count := inputCount(readFile(t, filepath.Join(e.proj, "fake-app-log")), text); count != 2 {
		t.Fatalf("input %q: got %d, want 2", text, count)
	}
}

func TestAppTaskImportAssignsDistinctIDsToLegacyLines(t *testing.T) {
	for _, raw := range []string{"2026-11-20 12:34, из дашборда: текст", "2026-11-20 12:34, сессии test-session, из дашборда: текст", "текст", "текст [app-import:abc]"} {
		t.Run(raw, func(t *testing.T) {
			e, _ := appTaskEnv(t, "mute")
			p, ok := e.s.projectByName("demo")
			if !ok {
				t.Fatal("project missing")
			}
			const sid, name = "test-session", "task-XR-002"
			text := chat.Said(raw)
			var previous appTaskMessage
			for i := 0; i < 2; i++ {
				if _, err := chat.Put(e.proj, name, text, raw); err != nil {
					t.Fatal(err)
				}
				if err := e.s.appTaskQueueImport(p, sid, "XR-002"); err != nil {
					t.Fatal(err)
				}
				entry, err := e.s.appTaskQueueNext(sid)
				if err != nil || entry.ID == "" || entry.ID == previous.ID || entry.Claim == previous.Claim || entry.Text != text {
					t.Fatalf("entry %+v: %v", entry, err)
				}
				previous = entry
				if err := e.s.appTaskQueueMark(sid, entry.ID, appTaskDelivery{sent: true, accepted: true}); err != nil {
					t.Fatal(err)
				}
				if next, err := e.s.appTaskQueueNext(sid); err != nil || next.ID != "" {
					t.Fatalf("duplicate %+v: %v", next, err)
				}
			}

		})
	}
}

func TestAppTaskRecoveryQueueInComposer(t *testing.T) {
	out, err := exec.Command("node", "testdata/apptask_queued.mjs", "static/app.js").CombinedOutput()
	if err != nil {
		t.Fatalf("очередь рабочего Codex в панели: %v\n%s", err, out)
	}
}

func TestAppTaskQueuedSaySavesEffort(t *testing.T) {
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
	waitAppTaskRecoveryCarrierExit(t, e, sid)
	p, ok := e.s.projectByName("demo")
	if !ok {
		t.Fatal("project missing")
	}
	if err := e.s.appTaskRecover(p, sid); err != nil {
		t.Fatal(err)
	}
	before := e.s.chatStoreRead(sid)
	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/say", `{"text":"новая реплика","msg_id":"effort-test","effort":"low"}`)
	raw := body(t, resp)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("say %d: %s", resp.StatusCode, raw)
	}
	var receipt struct {
		MessageID string `json:"app_message_id"`
	}
	if err := json.Unmarshal([]byte(raw), &receipt); err != nil {
		t.Fatal(err)
	}
	entry, err := e.s.appTaskQueueNext(sid)
	if err != nil || entry.ID == "" || receipt.MessageID != entry.ID || entry.Claim != "effort-test" {
		t.Fatalf("квитанция %q не соответствует записи %+v: %v", receipt.MessageID, entry, err)
	}
	conflict := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/say",
		`{"text":"другой текст","msg_id":"effort-test"}`)
	if raw := body(t, conflict); conflict.StatusCode != http.StatusConflict || !strings.Contains(raw, `"refused":"ключ реплики уже занят другим текстом"`) {
		t.Fatalf("повтор ключа с другим текстом: %d %s", conflict.StatusCode, raw)
	}
	after := e.s.chatStoreRead(sid)
	if after.CxEffort != "low" || after.TaskRunID != before.TaskRunID || after.Thread != before.Thread {
		t.Fatalf("settings before %+v after %+v", before, after)
	}
	live := e.s.appLive(sid)
	if live == nil || live.effortNow() != "low" {
		t.Fatal("live client did not receive effort")
	}
	if _, err := live.proc.call("test/finish", map[string]bool{"keepOpen": true}); err != nil {
		t.Fatal(err)
	}
	appWaitFeed(t, e, sid, entry.Text)
	feed := doReq(t, c, "GET", e.srv.URL+"/api/projects/demo/sessions/"+sid, "")
	raw = body(t, feed)
	if feed.StatusCode != http.StatusOK {
		t.Fatalf("лента %d: %s", feed.StatusCode, raw)
	}
	var delivered struct {
		Items []reply `json:"items"`
	}
	if err := json.Unmarshal([]byte(raw), &delivered); err != nil {
		t.Fatal(err)
	}
	for _, item := range delivered.Items {
		if item.Role == "user" && item.Text == entry.Text {
			if item.App == nil || item.App.Method != "devkit/appTaskMessage" || item.App.Item["id"] != receipt.MessageID {
				t.Fatalf("лента не подтвердила квитанцию %q: %+v", receipt.MessageID, item.App)
			}
			return
		}
	}
	t.Fatalf("в ответе ленты нет принятой реплики: %s", raw)
}

func TestAppTaskImportLeavesOtherAddressee(t *testing.T) {
	e, _ := appTaskEnv(t, "mute")
	p, ok := e.s.projectByName("demo")
	if !ok {
		t.Fatal("project missing")
	}
	line := "2026-11-20 12:34, сессии other, из дашборда: текст"
	if _, err := chat.Put(e.proj, "task-XR-002", "текст", line); err != nil {
		t.Fatal(err)
	}
	if err := e.s.appTaskQueueImport(p, "test-session", "XR-002"); err != nil {
		t.Fatal(err)
	}
	lines := chat.ReadLines(chat.Path(e.proj, "task-XR-002"))
	if len(lines) != 1 || lines[0] != line {
		t.Fatalf("other addressee: %v", lines)
	}
	if next, err := e.s.appTaskQueueNext("test-session"); err != nil || next.ID != "" {
		t.Fatalf("entry %+v: %v", next, err)
	}
}

func TestAppTaskImportRetriesAfterQueueFailure(t *testing.T) {
	e, _ := appTaskEnv(t, "mute")
	p, ok := e.s.projectByName("demo")
	if !ok {
		t.Fatal("project missing")
	}
	const sid, name = "test-session", "task-XR-002"
	if _, err := chat.Put(e.proj, name, "текст", "текст"); err != nil {
		t.Fatal(err)
	}
	queue := filepath.Join(chatStoreDir(e.s.cfg.Home), "app-task-queue", sid+".json")
	if err := os.MkdirAll(queue, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := e.s.appTaskQueueImport(p, sid, "XR-002"); err == nil {
		t.Fatal("queue failure was ignored")
	}
	lines := chat.ReadLines(chat.Path(e.proj, name))
	if len(lines) != 1 || chat.Said(lines[0]) != "текст" || chat.Addressee(lines[0]) != sid {
		t.Fatalf("saved input: %v", lines)
	}
	if err := os.Remove(queue); err != nil {
		t.Fatal(err)
	}
	claim := fmt.Sprintf("inbox-%x", sha256.Sum256([]byte(lines[0])))
	stored, err := e.s.appTaskQueueAdd(sid, claim, "текст")
	if err != nil {
		t.Fatal(err)
	}
	if err := e.s.appTaskQueueImport(p, sid, "XR-002"); err != nil {
		t.Fatal(err)
	}
	entry, err := e.s.appTaskQueueNext(sid)
	if err != nil || entry.Claim != claim || entry.ID != stored.ID {
		t.Fatalf("entry %+v: %v", entry, err)
	}
	if err := e.s.appTaskQueueMark(sid, entry.ID, appTaskDelivery{sent: true, accepted: true}); err != nil {
		t.Fatal(err)
	}
	if next, err := e.s.appTaskQueueNext(sid); err != nil || next.ID != "" {
		t.Fatalf("duplicate %+v: %v", next, err)
	}
	if lines := chat.ReadLines(chat.Path(e.proj, name)); len(lines) != 0 {
		t.Fatalf("input remains: %v", lines)
	}
}

func TestAppTaskRecoveryReportsUnreadableRunState(t *testing.T) {
	e, c := appTaskEnv(t, "mute")
	appTaskRecoveryFixture(t, e)
	code, first := appTaskStart(t, e, c)
	if code != http.StatusOK {
		t.Fatalf("start %d: %+v", code, first)
	}
	sid := first["session"]
	before := e.s.chatStoreRead(sid)
	e.s.appTasksClose()
	waitAppTaskLockGone(t, e)
	e.s.appWorkers.Wait()
	waitAppTaskRecoveryCarrierExit(t, e, sid)

	path, _, err := taskstate.Path(e.home, e.proj, before.Task)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	const reason = "Восстановление конвейера Codex ждёт: не прочитано состояние рабочего запуска"
	e.s.mainChatAssign(before.Task, "as-other-owner", "demo", "работа")
	if err := e.s.appTaskRecoveryTick(nil); !errors.Is(err, errAppTaskRecoveryRead) {
		t.Fatal(err)
	}
	for _, item := range e.s.appFeedRead(sid) {
		if item.Role == "note" && item.Text == reason {
			t.Fatal("причина появилась в прежнем рабочем разговоре")
		}
	}
	e.s.mainChatAssign(before.Task, sid, "demo", "работа")
	for range 2 {
		if err := e.s.appTaskRecoveryTick(nil); !errors.Is(err, errAppTaskRecoveryRead) {
			t.Fatal(err)
		}
	}
	notes := 0
	for _, item := range e.s.appFeedRead(sid) {
		if item.Role == "note" && item.Fail && item.Text == reason {
			notes++
		}
	}
	if notes != 1 || e.s.appTaskFind(sid, "", "") != nil {
		t.Fatalf("ожидалась одна причина в исходном чате и отсутствие запуска, записей: %d", notes)
	}

	if err := os.WriteFile(path, saved, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := e.s.appTaskRecoveryTick(nil); err != nil {
		t.Fatal(err)
	}
	appWaitFeed(t, e, sid, "Конвейер Codex продолжен в исходном разговоре")
	after := e.s.chatStoreRead(sid)
	if after.Thread != before.Thread || after.TaskRunID == before.TaskRunID {
		t.Fatalf("после восстановления состояния не продолжилась исходная нить: %q -> %q", before.Thread, after.Thread)
	}
}

func TestAppTaskRecoveryRechecksGuardAfterThreadRead(t *testing.T) {
	for _, mode := range []string{"closed", "owner", "stop"} {
		t.Run(mode, func(t *testing.T) {
			checkAppTaskRecoveryAfterThreadRead(t, mode)
		})
	}
}

func checkAppTaskRecoveryAfterThreadRead(t *testing.T, mode string) {
	t.Helper()
	e, c := appTaskEnv(t, "mute")
	appTaskRecoveryFixture(t, e)
	code, started := appTaskStart(t, e, c)
	if code != http.StatusOK {
		t.Fatalf("start %d: %+v", code, started)
	}
	sid := started["session"]
	e.s.appTasksClose()
	waitAppTaskLockGone(t, e)
	e.s.appWorkers.Wait()
	waitAppTaskRecoveryCarrierExit(t, e, sid)
	queued, err := e.s.appTaskQueueAdd(sid, "review-guard", "Ожидающая реплика")
	if err != nil {
		t.Fatal(err)
	}

	// Pause after the recovery guard, while awaiting the client response.
	const anchor = "    elif m == \"thread/read\":\n"
	if strings.Count(appClientPy, anchor) != 1 {
		t.Fatal("missing fixture hook")
	}
	client := strings.Replace(appClientPy, anchor, anchor+`        with open("recovery-reading", "w") as mark:
            mark.write("reading\n")
        while not os.path.exists("recovery-read-release"):
            time.sleep(0.01)
`, 1)
	putDoc(t, e.bin, "клиент-app.py", client)
	p, ok := e.s.projectByName("demo")
	if !ok {
		t.Fatal("project missing")
	}
	done := make(chan error, 1)
	go func() { done <- e.s.appTaskRecover(p, sid) }()
	releasePath := filepath.Join(e.proj, "recovery-read-release")
	defer os.WriteFile(releasePath, nil, 0600)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.After(10 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(e.proj, "recovery-reading")); err == nil {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("recovery returned before thread/read barrier: %v", err)
		case <-deadline:
			t.Fatal("recovery did not enter thread/read")
		case <-ticker.C:
		}
	}
	var want string
	var stopped <-chan error
	switch mode {
	case "closed":
		appTaskBoard(t, e, "done")
		want = "состояние задачи изменилось перед возобновлением"
	case "owner":
		e.s.mainChatAssign("XR-002", "as-other-owner", "demo", "работа")
		want = "основной рабочий разговор сменился"
	case "stop":
		carrier := e.s.appTaskFind(sid, "", "")
		if carrier == nil {
			t.Fatal("носитель не поднялся во время ожидания thread/read")
		}
		req, err := http.NewRequest("POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/stop", strings.NewReader("{}"))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		stopResult := make(chan error, 1)
		stopped = stopResult
		go func() {
			resp, err := c.Do(req)
			if err == nil {
				defer resp.Body.Close()
				if resp.StatusCode != http.StatusOK {
					err = fmt.Errorf("stop HTTP %d", resp.StatusCode)
				}
			}
			stopResult <- err
		}()
		select {
		case <-carrier.stop:
		case err := <-stopped:
			t.Fatalf("stop did not close active carrier: %v", err)
		case <-time.After(10 * time.Second):
			t.Fatal("stop did not close active carrier")
		}
	}
	if err := os.WriteFile(releasePath, nil, 0600); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		log := readFile(t, filepath.Join(e.proj, "fake-app-log"))
		if strings.Count(log, "turn high\n") != 1 {
			t.Fatalf("recovery sent turn/start after %s during thread/read, recovery err=%v\n%s", mode, err, log)
		}
		if err == nil || (want != "" && !strings.Contains(err.Error(), want)) {
			t.Fatalf("missing recovery refusal %q, got %v", want, err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("recovery did not complete after thread/read release")
	}
	entry, err := e.s.appTaskQueueNext(sid)
	if err != nil || entry.ID != queued.ID || entry.Sent {
		t.Fatalf("recovery changed queued message after refusal: %+v, %v", entry, err)
	}
	if stopped == nil {
		return
	}
	select {
	case err := <-stopped:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("explicit stop did not finish")
	}
	state, err := taskstate.Acquire(e.home, e.proj, "XR-002")
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	if !state.State.Stopped {
		t.Fatal("explicit stop not recorded")
	}
}

func TestAppTaskRecoveryChecksHumanWaitMarker(t *testing.T) {
	e, _ := appTaskEnv(t, "mute")
	appTaskRecoveryFixture(t, e)
	project, ok := e.s.projectByName("demo")
	if !ok {
		t.Fatal("project missing")
	}
	const id = "XR-002"
	name := chat.TaskName(id)
	st := chatStore{Task: id}
	assertEligible := func(label string, want bool) {
		t.Helper()
		eligible, err := e.s.appTaskRecoveryEligible(project, st)
		if eligible != want || err != nil {
			t.Fatalf("%s: допуск %t, жду %t, ошибка: %v", label, eligible, want, err)
		}
	}

	assertEligible("отметка отсутствует", true)
	if err := chat.WriteAsk(e.proj, name, chat.Ask{Task: id, Until: e.s.now().Add(-time.Hour)}); err != nil {
		t.Fatal(err)
	}
	assertEligible("срок ожидания истёк", true)
	if err := chat.WriteAsk(e.proj, name, chat.Ask{Task: id}); err != nil {
		t.Fatal(err)
	}
	assertEligible("живой вопрос", false)

	path := chat.AskPath(e.proj, name)
	if err := os.WriteFile(path, []byte("повреждён срок живого вопроса\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	eligible, err := e.s.appTaskRecoveryEligible(project, st)
	if eligible || err == nil || !strings.Contains(err.Error(), "повреждён признак ожидания ответа человека") {
		t.Fatalf("повреждённый признак должен остановить восстановление: %t, %v", eligible, err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	eligible, err = e.s.appTaskRecoveryEligible(project, st)
	var readErr *os.PathError
	if eligible || !errors.As(err, &readErr) {
		t.Fatalf("ошибка чтения признака должна остановить восстановление и сохранить причину: %t, %v", eligible, err)
	}
}
