package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/dronrider/devkit/internal/chat"
	"github.com/dronrider/devkit/internal/mainchat"
	"github.com/dronrider/devkit/internal/taskhead"
	"github.com/dronrider/devkit/internal/taskstate"
)

const (
	appTaskRecoveryOrder = "Продолжай работу"
	appTaskRecoveryLimit = 3
)

var errAppTaskRecoveryOwner = errors.New("основной рабочий разговор сменился")

func (s *server) appTaskLaunchLock(sid string) (*os.File, error) {
	return chat.Lock(filepath.Join(chatStoreDir(s.cfg.Home), "app-task-queue", sid+".launch"))
}

func (s *server) appTaskAcceptSay(w http.ResponseWriter, sid, text, claim string) {
	entry, err := s.appTaskQueueAdd(sid, claim, text)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	if entry.Accepted {
		s.chatSayDone(sid, claim, "appserver")
		writeJSON(w, http.StatusOK, map[string]string{"way": "dup", "was": "appserver", "app_message_id": entry.ID})
		return
	}
	s.appTaskQueued(w, sid, entry.ID)
}

func (s *server) appTaskQueued(w http.ResponseWriter, sid, messageID string) {
	writeJSON(w, http.StatusAccepted, map[string]string{"way": "queued", "session": sid, "app_message_id": messageID, "message": "реплика сохранена во входе рабочего разговора Codex"})
}

func (s *server) appTaskNextOrder(a *appTask, p *Project, c *appChat, order string) (string, appTaskMessage, error) {
	recovering := a.recover && order == appTaskRecoveryOrder
	if a.recover {
		if err := s.appTaskQueueImport(p, a.sid, a.task); err != nil {
			return "", appTaskMessage{}, err
		}
	}
	entry, err := s.appTaskQueueNext(a.sid)
	if err != nil {
		return "", entry, err
	}
	if recovering || entry.Sent {
		thread, err := appTaskReadThread(c)
		if err != nil {
			return "", entry, err
		}
		for entry.Sent && thread.accepted(entry.ID) {
			if err := s.appTaskQueueFeed(a.sid, entry); err != nil {
				return "", entry, err
			}
			if err := s.appTaskQueueMark(a.sid, entry.ID, true, true); err != nil {
				return "", entry, err
			}
			if a.replyOnce && entry.Claim == a.replyClaim {
				a.firstClaim = entry.Claim
			}
			entry, err = s.appTaskQueueNext(a.sid)
			if err != nil {
				return "", entry, err
			}
		}
	}
	if entry.ID != "" {
		return entry.Text, entry, nil
	}
	if a.replyOnce {
		return appTaskRecoveryOrder, entry, nil
	}
	return order, entry, nil
}

func (s *server) appTaskRecoveryKeeper(stop <-chan struct{}) {
	path := filepath.Join(realHomeFn(), ".devkit", "app-task-tick")
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	previous := ""
	for {
		raw, err := os.ReadFile(path)
		if err == nil && len(raw) != 0 && string(raw) != previous {
			previous = string(raw)
			s.appTaskRecoveryTick(stop)
		}
		select {
		case <-stop:
			return
		case <-ticker.C:
		}
	}
}

func (s *server) appTaskRecoveryTick(stop <-chan struct{}) {
	dir := chatStoreDir(s.cfg.Home)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		select {
		case <-stop:
			return
		default:
		}
		sid, ok := strings.CutSuffix(entry.Name(), ".json")
		if !ok || !sessionIDRe.MatchString(sid) {
			continue
		}
		st := s.chatStoreRead(sid)
		if st.Protocol != "codex" || st.TaskRunPID <= 0 || st.Thread == "" || st.Task == "" {
			continue
		}
		p, ok := s.projectByName(st.Project)
		if !ok || s.appTaskFind(sid, p.Path, st.Task) != nil {
			continue
		}
		if err := s.appTaskRecover(p, sid); err != nil && !errors.Is(err, chat.ErrLocked) {
			s.logf("восстановление Codex %s: %v", sid, err)
		}
	}
}

func (s *server) appTaskRecoveryWaiting(p *Project, sid string, st chatStore) (bool, error) {
	state, err := taskstate.Acquire(realHomeFn(), p.Path, st.Task)
	if err != nil {
		return false, err
	}
	run := state.State.Run
	protected := run != nil && run.AppChat == sid && (run.AppRecovery || run.AppRecoveryTerminal)
	waiting := protected && !state.State.Stopped && !run.AppRecoveryTerminal
	state.Close()
	if protected && !s.appTaskRecoveryOwner(p, sid, st.Task) {
		return false, errAppTaskRecoveryOwner
	}
	if !waiting {
		return false, nil
	}
	return s.appTaskRecoveryEligible(p, st)
}

func (s *server) appTaskRecover(p *Project, sid string) error {
	lock, err := s.appTaskLaunchLock(sid)
	if err != nil {
		return err
	}
	defer lock.Close()
	st := s.chatStoreRead(sid)
	if s.appTaskFind(sid, p.Path, st.Task) != nil {
		return nil
	}
	owner := s.appTaskRecoveryOwner(p, sid, st.Task)
	home := realHomeFn()
	state, err := taskstate.Acquire(home, p.Path, st.Task)
	if err != nil {
		return err
	}
	run := state.State.Run
	if state.State.Stopped || run == nil || run.Model != st.Model || run.Harness != st.Harness {
		state.Close()
		return nil
	}
	if owner && run.AppChat == "" && run.ID == st.TaskRunID && run.PID == st.TaskRunPID && state.State.Root == st.TaskRunRoot && st.TaskRunSession == sid &&
		(st.TaskRun || appTaskInterruptedLegacy(home, st.Task, run.Started)) {
		stopped, err := s.appTaskLegacyStopped(home, state.State.Root, st.Task, run.Started)
		if err != nil || stopped {
			if stopped {
				state.State.Stopped = true
				err = state.Save()
			}
			state.Close()
			return err
		}
		run.AppChat, run.AppRecovery = sid, true
		if err := state.Save(); err != nil {
			state.Close()
			return err
		}
	}
	canonical := state.State.Root
	state.Close()
	if run.AppChat != sid || !run.AppRecovery || st.TaskRunRoot != canonical || st.TaskRunSession != sid {
		return nil
	}
	if !owner {
		s.appTaskRecoveryNote(sid, "Восстановление конвейера Codex невозможно: "+errAppTaskRecoveryOwner.Error(), true, "owner-"+run.ID)
		return errAppTaskRecoveryOwner
	}
	if run.AppRecoveryTerminal {
		s.appTaskRecoveryNote(sid, "Восстановление конвейера Codex завершилось отказом: "+run.AppRecoveryWhy, true, "terminal-"+run.ID)
		return nil
	}
	if taskhead.Alive(run.PID) || run.Group > 0 && !errors.Is(syscall.Kill(-run.Group, 0), syscall.ESRCH) ||
		taskhead.Alive(taskhead.Owner(taskhead.LockPath(realHomeFn(), st.Task))) {
		return nil
	}
	st.TaskRunID, st.TaskRunPID, st.TaskRunRoot, st.TaskRunSession = run.ID, run.PID, canonical, sid
	eligible, err := s.appTaskRecoveryEligible(p, st)
	if err != nil {
		s.appTaskRecoveryNote(sid, "Восстановление конвейера Codex ждёт: "+err.Error(), true, "board-"+run.ID)
		return err
	}
	if !eligible {
		return nil
	}
	if err := s.appTaskQueueImport(p, sid, st.Task); err != nil {
		return err
	}
	h, err := s.appTaskRecoveryHarness(st)
	if err != nil {
		s.appTaskRecoveryFailure(sid, p.Path, st.Task, run.PID, err)
		return err
	}
	if err := appTaskRecoveryQuota(p.Path, h.Name); err != nil {
		s.appTaskRecoveryNote(sid, "Восстановление конвейера Codex ждёт: "+err.Error(), true, "quota-"+run.ID)
		return err
	}
	budget := strings.TrimSpace(os.Getenv("DEVKIT_RECOVERY_LOG"))
	if budget == "" {
		budget = filepath.Join(home, ".devkit", "recovery.jsonl")
	}
	machine, err := chat.Lock(budget + ".lock")
	if err != nil {
		return err
	}
	defer machine.Close()
	state, err = taskstate.Acquire(realHomeFn(), p.Path, st.Task)
	if err != nil {
		return err
	}
	current := state.State.Run
	if state.State.Stopped || current == nil || current.ID != run.ID || current.AppRecoveryTerminal || !current.AppRecovery {
		state.Close()
		return nil
	}
	if current.AppRecoveryAttempts >= appTaskRecoveryLimit {
		state.Close()
		s.appTaskRecoveryFailure(sid, p.Path, st.Task, run.PID, errors.New("исчерпаны попытки восстановления: "+run.AppRecoveryWhy))
		return nil
	}
	if taskhead.Alive(current.PID) || current.Group > 0 && !errors.Is(syscall.Kill(-current.Group, 0), syscall.ESRCH) ||
		taskhead.Alive(taskhead.Owner(taskhead.LockPath(home, st.Task))) {
		state.Close()
		return nil
	}
	incident := fmt.Sprintf("codex-%s-%d", current.ID, current.AppRecoveryAttempts+1)
	mark, err := s.appTaskBudgetCommand(home, "app-reserve", canonical, st.Task, incident)
	if err != nil {
		state.Close()
		s.appTaskRecoveryNote(sid, "Восстановление конвейера Codex ждёт: "+err.Error(), true, "budget-"+run.ID)
		return err
	}
	current.AppRecoveryAttempts++
	if err := state.Save(); err != nil {
		state.Close()
		return err
	}
	state.Close()
	outcome := "confirmed"
	if err = s.raiseAppTask(nil, p, sid, st, h, "", appTaskRecoveryOrder, false, true, appTaskMessage{}); err != nil {
		outcome = "launch-failed"
		s.appTaskRecoveryFailure(sid, p.Path, st.Task, 0, err)
	}
	_, finishErr := s.appTaskBudgetCommand(home, "app-finish", mark, outcome)
	return errors.Join(err, finishErr)
}

func (s *server) appTaskBudgetCommand(home string, args ...string) (string, error) {
	runner := taskRunPath(s.cfg.Roots)
	if runner == "" {
		return "", errors.New(taskRunMissing)
	}
	root := strings.TrimSuffix(runner, string(filepath.Separator)+filepath.FromSlash(taskhead.TaskRunRel))
	script := filepath.Join(root, "tools", "devkitctl", "recovery.py")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "python3", append([]string{script}, args...)...)
	cmd.Dir = root
	cmd.Env = homeEnvAt(home, false)
	raw, err := cmd.CombinedOutput()
	line := strings.TrimSpace(string(raw))
	if err != nil {
		return "", fmt.Errorf("журнал бюджета восстановления: %s (%w)", line, err)
	}
	return line, nil
}

func (s *server) appTaskStopOrphan(w http.ResponseWriter, root, sid string, st chatStore) bool {
	state, err := taskstate.Acquire(realHomeFn(), root, st.Task)
	if err == nil {
		defer state.Close()
		run := state.State.Run
		if run != nil && (run.AppChat == sid || run.AppChat == "" && run.ID == st.TaskRunID && run.PID == st.TaskRunPID) {
			state.State.Stopped = true
			err = state.Save()
		}
	}
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "состояние ручной остановки не сохранено: " + err.Error()})
		return false
	}
	return true
}

// Старый дашборд мог сохранить стоп в каталоге конфигурации.
func (s *server) appTaskLegacyStopped(home, root, task, started string) (bool, error) {
	if s.cfg.Home == home {
		return false, nil
	}
	since, err := time.Parse(time.RFC3339Nano, started)
	if err != nil {
		return false, err
	}
	path, _, err := taskstate.Path(s.cfg.Home, root, task)
	if err != nil {
		return false, err
	}
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil || info.ModTime().Before(since) {
		return false, err
	}
	machinePath, _, err := taskstate.Path(home, root, task)
	if err != nil {
		return false, err
	}
	machineInfo, err := os.Stat(machinePath)
	if err != nil || os.SameFile(info, machineInfo) {
		return false, err
	}
	previous, err := taskstate.Acquire(s.cfg.Home, root, task)
	if err != nil {
		return false, err
	}
	defer previous.Close()
	return previous.State.Stopped, nil
}

// Старый дашборд сбрасывал TaskRun при выгрузке. В этом случае нужен след
// прохода, поднятого оболочкой после записи именно этого запуска.
func appTaskInterruptedLegacy(home, task, started string) bool {
	since, err := time.Parse(time.RFC3339Nano, started)
	if err != nil {
		return false
	}
	file, err := os.Open(filepath.Join(home, ".devkit", "heads", task+".log"))
	if err != nil {
		return false
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.ModTime().Before(since) {
		return false
	}
	size := min(info.Size(), 64*1024)
	raw := make([]byte, size)
	if _, err := file.ReadAt(raw, info.Size()-size); err != nil {
		return false
	}
	open := false
	for _, line := range strings.Split(string(raw), "\n") {
		stamp, event, ok := strings.Cut(line, " task-run "+task+": ")
		if !ok {
			continue
		}
		at, err := time.ParseInLocation(chat.Stamp, stamp, time.Local)
		if err != nil || at.Before(since.Truncate(time.Second)) {
			continue
		}
		switch {
		case strings.HasPrefix(event, "проход поднят:") && strings.Contains(event, "--app-turn-client"):
			open = true
		case strings.HasPrefix(event, "голова вышла кодом "), strings.HasPrefix(event, "стоп: "):
			open = false
		}
	}
	return open
}

func (s *server) appTaskRecoveryOwner(p *Project, sid, id string) bool {
	bind := s.mainChatBind(id)
	return bind.Chat == sid && bind.Project == p.Name && bind.Source == mainchat.ByWork
}

func (s *server) appTaskRecoveryGuard(p *Project, sid string, st chatStore) error {
	if !s.appTaskRecoveryOwner(p, sid, st.Task) {
		return errAppTaskRecoveryOwner
	}
	ok, err := s.appTaskRecoveryEligible(p, st)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("состояние задачи изменилось перед возобновлением")
	}
	return nil
}

func (s *server) appTaskRecoveryEligible(p *Project, st chatStore) (bool, error) {
	raw, err := boardJSON(s.cfg.Home, p.Path)
	if err != nil {
		return false, err
	}
	rows, err := parseBoardRows(raw)
	if err != nil {
		return false, err
	}
	row, ok := rows[st.Task]
	if !ok || row.Sect != "in-progress" || len(row.HeldBy) != 0 {
		return false, nil
	}
	for _, tree := range []string{p.Path, chatTree(p.Path, st.Task)} {
		if ask, ok := chat.ReadAsk(chat.AskPath(tree, chat.TaskName(st.Task))); ok && ask.Live(s.now()) {
			return false, nil
		}
	}
	path := filepath.Join(realHomeFn(), ".devkit", "waits", st.Task+".json")
	if raw, err := os.ReadFile(path); err == nil {
		var wait struct {
			Task  string `json:"task"`
			Until string `json:"until"`
		}
		if json.Unmarshal(raw, &wait) != nil || wait.Task != st.Task {
			return false, errors.New("не прочитана отметка ожидания задачи")
		}
		until, err := time.ParseInLocation(chat.Stamp, wait.Until, time.Local)
		if err != nil || until.After(s.now()) {
			return false, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	return true, nil
}

func (s *server) appTaskRecoveryHarness(st chatStore) (*Harness, error) {
	for _, h := range s.harnesses().Harnesses {
		if h.Name != st.Harness || h.Protocol != "codex" {
			continue
		}
		if h.HooksGap != "" {
			return nil, fmt.Errorf("исходная подписка %s недоступна автоматике %s", st.Harness, h.HooksGap)
		}
		for _, m := range h.Models {
			if m.Model == st.Model && (m.Via == "" || m.Via == h.Name) {
				return &h, nil
			}
		}
		return nil, fmt.Errorf("исходная модель %s недоступна в подписке %s", st.Model, st.Harness)
	}
	return nil, fmt.Errorf("исходная подписка %s недоступна", st.Harness)
}

func appTaskRecoveryQuota(root, harness string) error {
	raw, err := runProcQuiet(root, true, binPath(agentctlBin), "quota", "--json", "--harness", harness)
	if err != nil {
		return fmt.Errorf("не проверена квота исходной подписки %s: %w", harness, err)
	}
	var q struct {
		Schema, Harness, Reason    string
		Version                    int
		Fresh, Complete, Available bool
	}
	if json.Unmarshal([]byte(raw), &q) != nil || q.Schema != "agentctl/quota" || q.Version != 1 || q.Harness != harness {
		return fmt.Errorf("неизвестное состояние квоты подписки %s", harness)
	}
	if !q.Fresh || !q.Complete || !q.Available {
		return fmt.Errorf("квота подписки %s не разрешает запуск: %s", harness, q.Reason)
	}
	return nil
}

func appTaskRecoveryPermanent(err error) bool {
	message := strings.ToLower(err.Error())
	for _, fragment := range []string{"исходная подписка", "исходная модель", "401 unauthorized", "invalid api key", "invalid_api_key", "model_not_found", "unsupported model", "model is not supported", "not logged in", "authentication failed", "subscription expired", "токен кончился", "approvalsreviewer не поддерживается"} {
		if strings.Contains(message, fragment) {
			return true
		}
	}
	return false
}

func (s *server) appTaskRecoveryFailure(sid, root, id string, pid int, cause error) {
	state, err := taskstate.Acquire(realHomeFn(), root, id)
	if err != nil {
		s.logf("исход восстановления Codex %s не записался: %v", sid, err)
		return
	}
	defer state.Close()
	run := state.State.Run
	if state.State.Stopped || run == nil || run.AppChat != sid || pid != 0 && run.PID != pid || run.AppRecoveryTerminal {
		return
	}
	run.AppRecoveryWhy = cause.Error()
	if run.AppRecovery && (run.AppRecoveryAttempts >= appTaskRecoveryLimit || appTaskRecoveryPermanent(cause)) {
		run.AppRecoveryTerminal = true
	}
	if err := state.Save(); err != nil {
		s.logf("исход восстановления Codex %s не записался: %v", sid, err)
	}
	if run.AppRecoveryTerminal {
		s.appTaskRecoveryNote(sid, "Восстановление конвейера Codex завершилось отказом: "+run.AppRecoveryWhy, true, "terminal-"+run.ID)
	} else {
		s.appTaskRecoveryNote(sid, "конвейер задачи прерван: "+cause.Error(), true, "break-"+run.ID)
	}
}

func (s *server) appTaskRecoveryProgress(sid, root, id string, pid int) {
	state, err := taskstate.Acquire(realHomeFn(), root, id)
	if err != nil {
		s.logf("подтверждение хода Codex %s не записалось: %v", sid, err)
		return
	}
	defer state.Close()
	run := state.State.Run
	if state.State.Stopped || run == nil || run.AppChat != sid || run.PID != pid || run.AppRecoveryAttempts == 0 && !run.AppRecoveryTerminal {
		return
	}
	run.AppRecoveryAttempts, run.AppRecoveryWhy, run.AppRecoveryTerminal = 0, "", false
	if err := state.Save(); err != nil {
		s.logf("подтверждение хода Codex %s не записалось: %v", sid, err)
	}
}

func (s *server) appTaskRecoveryNote(sid, text string, fail bool, key string) {
	key = fmt.Sprintf("%s-%x", key, sha256.Sum256([]byte(text)))
	it := reply{Role: "note", Text: text, Fail: fail, Time: s.now().UTC().Format(time.RFC3339),
		App: &appFeedEvent{Method: "devkit/appTaskRecovery", Item: map[string]any{"id": key}}}
	if err := s.appFeedAppend(sid, []reply{it}); err != nil {
		s.logf("исход восстановления Codex %s не записался в разговор: %v", sid, err)
	}
}
