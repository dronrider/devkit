package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/dronrider/devkit/internal/mainchat"
	"github.com/dronrider/devkit/internal/taskhead"
	"github.com/dronrider/devkit/internal/taskstate"
)

type appTaskTurn struct {
	thread string
	turn   string
	last   time.Time
	seq    uint64
	early  map[string]appTaskActivity
	work   bool
	wake   chan struct{}
	ends   chan json.RawMessage
}

type appTaskActivity struct {
	last time.Time
	work bool
}

func appTaskTurnID(method string, params json.RawMessage, thread string) string {
	var ids struct {
		Thread string `json:"threadId"`
		TurnID string `json:"turnId"`
		Turn   struct {
			ID string `json:"id"`
		} `json:"turn"`
	}
	if json.Unmarshal(params, &ids) != nil || ids.Thread != thread {
		return ""
	}
	if method == "turn/started" || method == "turn/completed" {
		return ids.Turn.ID
	}
	return ids.TurnID
}

func appTaskWorkEvent(method string, params json.RawMessage) bool {
	switch method {
	case "item/agentMessage/delta", "item/reasoning/summaryTextDelta", "item/reasoning/textDelta", "item/plan/delta":
		return true
	case "item/started", "item/completed":
		var event struct {
			Item struct {
				Type string `json:"type"`
			} `json:"item"`
		}
		if json.Unmarshal(params, &event) == nil {
			switch event.Item.Type {
			case "agentMessage", "reasoning", "commandExecution", "fileChange", "mcpToolCall", "collabAgentToolCall":
				return true
			}
		}
	}
	return false
}

// The chat lock guards activity while the request is still returning its turn ID.
func (t *appTaskTurn) note(method string, params json.RawMessage, now time.Time) {
	turn := appTaskTurnID(method, params, t.thread)
	if turn == "" || t.turn != "" && t.turn != turn {
		return
	}
	t.seq++
	work := appTaskWorkEvent(method, params)
	if t.turn == "" {
		t.early[turn] = appTaskActivity{last: now, work: t.early[turn].work || work}
	} else {
		t.last, t.work = now, t.work || work
	}
	if method == "turn/completed" {
		select {
		case t.ends <- params:
		default:
		}
	}
	select {
	case t.wake <- struct{}{}:
	default:
	}
}

func (c *appChat) waitTaskTurn(t *appTaskTurn, turn string, stop <-chan struct{}, cap time.Duration, confirm func()) error {
	return c.waitTaskTurnProbe(t, turn, stop, cap, confirm, func() (json.RawMessage, error) {
		return c.proc.call("thread/read", map[string]any{"threadId": t.thread, "includeTurns": true})
	})
}

func (c *appChat) waitTaskTurnProbe(t *appTaskTurn, turn string, stop <-chan struct{}, cap time.Duration, confirm func(), probe func() (json.RawMessage, error)) error {
	c.mu.Lock()
	t.turn = turn
	if activity := t.early[turn]; activity.last.After(t.last) {
		t.last = activity.last
	}
	t.work = t.early[turn].work
	t.early = nil
	c.mu.Unlock()
	remaining := func() time.Duration { c.mu.Lock(); defer c.mu.Unlock(); return time.Until(t.last.Add(cap)) }
	confirmWork := func() {
		if confirm != nil {
			confirm()
			confirm = nil
		}
	}
	finish := func(err error) error {
		c.mu.Lock()
		c.busy = false
		c.mu.Unlock()
		if err == nil {
			confirmWork()
		}
		return err
	}
	type probeResult struct {
		raw json.RawMessage
		err error
		seq uint64
	}
	var results <-chan probeResult
	timer := time.NewTimer(remaining())
	defer timer.Stop()
	for {
		if confirm != nil {
			c.mu.Lock()
			work := t.work
			c.mu.Unlock()
			if work {
				select {
				case <-stop:
					return errors.New("конвейер задачи остановлен")
				case <-c.proc.dead:
					return fmt.Errorf("процесс клиента вышел: %s", c.proc.errWhyFmt())
				default:
					confirmWork()
				}
			}
		}
		select {
		case <-stop:
			return errors.New("конвейер задачи остановлен")
		case <-c.proc.dead:
			return fmt.Errorf("процесс клиента вышел: %s", c.proc.errWhyFmt())
		case <-timer.C:
			c.mu.Lock()
			left, seq := time.Until(t.last.Add(cap)), t.seq
			c.mu.Unlock()
			if left > 0 {
				timer.Reset(left)
				continue
			}
			ch := make(chan probeResult, 1)
			results = ch
			go func() { raw, err := probe(); ch <- probeResult{raw, err, seq} }()
		case <-t.wake:
			if results == nil {
				timer.Reset(remaining())
			}
		case params := <-t.ends:
			select {
			case <-stop:
				return errors.New("конвейер задачи остановлен")
			default:
			}
			complete, err := appTaskCompletion(params, t.thread, turn)
			if complete {
				return finish(err)
			}
		case result := <-results:
			results = nil
			active, status, err := appTaskProbeStatus(result.raw, t.thread, turn, result.err)
			// A newer event wins over a stale response, including errors and terminal states.
			c.mu.Lock()
			if t.seq != result.seq {
				c.mu.Unlock()
				timer.Reset(remaining())
				continue
			}
			select {
			case <-stop:
				c.mu.Unlock()
				return errors.New("конвейер задачи остановлен")
			default:
			}
			select {
			case <-c.proc.dead:
				c.mu.Unlock()
				return fmt.Errorf("процесс клиента вышел: %s", c.proc.errWhyFmt())
			default:
			}
			if err != nil {
				c.mu.Unlock()
				return err
			}
			if active {
				t.last = time.Now()
				c.mu.Unlock()
				timer.Reset(cap)
				continue
			}
			_, err = appTaskCompletion(status, t.thread, turn)
			c.busy = false
			c.mu.Unlock()
			if err == nil {
				confirmWork()
			}
			return err
		}
	}
}

// The exact turn supplies terminal status. An active matching thread also
// renews waiting when Codex has not yet projected the running turn into history.
func appTaskProbeStatus(raw json.RawMessage, thread, turn string, callErr error) (bool, json.RawMessage, error) {
	if callErr != nil {
		return false, nil, fmt.Errorf("не удалось проверить ход Codex после тишины: %w", callErr)
	}
	var response struct {
		Thread struct {
			ID     string `json:"id"`
			Status struct {
				Type string `json:"type"`
			} `json:"status"`
			Turns []json.RawMessage `json:"turns"`
		} `json:"thread"`
	}
	if json.Unmarshal(raw, &response) != nil || response.Thread.ID != thread {
		return false, nil, errors.New("не удалось проверить ход Codex: нить не подтверждена")
	}
	for _, entry := range response.Thread.Turns {
		var item struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		}
		if json.Unmarshal(entry, &item) != nil || item.ID != turn {
			continue
		}
		switch item.Status {
		case "inProgress":
			return true, nil, nil
		case "completed", "failed", "interrupted":
			params, err := json.Marshal(struct {
				Thread string          `json:"threadId"`
				Turn   json.RawMessage `json:"turn"`
			}{thread, entry})
			return false, params, err
		}
	}
	if response.Thread.Status.Type == "active" {
		return true, nil, nil
	}
	return false, nil, errors.New("не удалось проверить ход Codex: состояние хода не подтверждено")
}

type appTask struct {
	sid        string
	root       string
	task       string
	home       string
	dir        string
	listener   net.Listener
	token      string
	launched   chan struct{}
	ready      chan error
	stop       chan struct{}
	once       sync.Once
	lifecycle  sync.Mutex
	pid        int
	recover    bool
	replyOnce  bool
	replyClaim string
	firstClaim string
}

func (s *server) appTaskClose(a *appTask) {
	<-a.launched
	a.once.Do(func() {
		close(a.stop)
		a.listener.Close()
		if a.pid > 0 && taskhead.Owner(taskhead.LockPath(a.home, a.task)) == a.pid {
			syscall.Kill(-a.pid, syscall.SIGTERM)
		}
		a.lifecycle.Lock()
		defer a.lifecycle.Unlock()
		s.appDrop(a.sid, "конвейер задачи остановлен")
		if err := s.chatStoreUpdate(a.sid, func(st chatStore) chatStore {
			st.TaskRun = false
			return st
		}); err != nil {
			s.logf("конец конвейера %s не записался: %v", a.task, err)
		}
		s.appMu.Lock()
		if s.appTasks[a.sid] == a {
			delete(s.appTasks, a.sid)
		}
		s.appMu.Unlock()
		os.RemoveAll(a.dir)
	})
}

func (s *server) appTasksClose() {
	s.appMu.Lock()
	tasks := make([]*appTask, 0, len(s.appTasks))
	for _, a := range s.appTasks {
		tasks = append(tasks, a)
	}
	s.appMu.Unlock()
	for _, a := range tasks {
		s.appTaskClose(a)
	}
}

func (s *server) appTaskFind(sid, root, id string) *appTask {
	s.appMu.Lock()
	defer s.appMu.Unlock()
	for _, a := range s.appTasks {
		if sid != "" && a.sid == sid || root != "" && a.root == root && a.task == id {
			return a
		}
	}
	return nil
}

func (s *server) appTaskServe(a *appTask, p *Project) {
	<-a.launched
	for {
		conn, err := a.listener.Accept()
		if err != nil {
			return
		}
		err = s.appTaskPass(a, p, conn)
		conn.Close()
		if err == nil {
			continue
		}
		select {
		case <-a.stop:
			return
		default:
		}
		s.appTaskRecoveryFailure(a.sid, a.root, a.task, a.pid, err)
		s.appDrop(a.sid, "проход задачи завершился отказом")
		select {
		case a.ready <- err:
		default:
		}
		return
	}
}

func (s *server) appTaskPass(a *appTask, p *Project, conn net.Conn) (err error) {
	var req struct {
		Token string `json:"token"`
		Order string `json:"order"`
	}
	conn.SetReadDeadline(time.Now().Add(appCallCap))
	if err = json.NewDecoder(io.LimitReader(conn, 1024*1024)).Decode(&req); err != nil {
		return err
	}
	if req.Token != a.token || req.Order == "" {
		return errors.New("неверный заказ носителя app-server")
	}
	conn.SetReadDeadline(time.Time{})
	defer func() {
		code, why := 0, ""
		if err != nil {
			code, why = 75, err.Error()
		}
		json.NewEncoder(conn).Encode(map[string]any{"code": code, "error": why})
	}()
	if taskhead.Owner(taskhead.LockPath(a.home, a.task)) != a.pid {
		return errors.New("замок конвейера задачи сменился")
	}
	a.lifecycle.Lock()
	select {
	case <-a.stop:
		a.lifecycle.Unlock()
		return errors.New("конвейер задачи остановлен")
	default:
	}
	locked := true
	defer func() {
		if locked {
			a.lifecycle.Unlock()
		}
	}()
	st := s.chatStoreRead(a.sid)
	c := s.appLive(a.sid)
	if c == nil {
		h := s.chatHarnessOf(st.Model, st.Harness)
		if !h.foreign() || h.Name != st.Harness {
			return errors.New("исходная подписка конвейера Codex недоступна")
		}
		c, err = s.appSpawn(a.sid, h, st.Model, chatTree(p.Path, st.Task), p.Name, st.Thread, a.stop)
		if err != nil {
			return err
		}
		select {
		case <-a.stop:
			return errors.New("конвейер задачи остановлен")
		default:
		}
		st.Thread = c.thread
		if err = s.chatStoreUpdate(a.sid, func(current chatStore) chatStore {
			current.Thread = c.thread
			st.CxEffort = current.CxEffort
			return current
		}); err != nil {
			return err
		}
	}
	recovering := a.recover && req.Order == appTaskRecoveryOrder
	if recovering {
		if err := s.appTaskRecoveryGuard(p, a.sid, st); err != nil {
			return err
		}
	}

	order, queued, err := s.appTaskNextOrder(a, p, c, req.Order)
	if err != nil {
		return err
	}
	// The client may have been waiting in thread/read while the task changed.
	if recovering {
		if err := s.appTaskRecoveryGuard(p, a.sid, st); err != nil {
			return err
		}
	}
	select {
	case <-a.stop:
		return errors.New("конвейер задачи остановлен")
	default:
	}
	turn := &appTaskTurn{thread: c.thread, last: time.Now(), early: make(map[string]appTaskActivity),
		wake: make(chan struct{}, 1), ends: make(chan json.RawMessage, 128)}
	c.mu.Lock()
	if c.busy {
		c.mu.Unlock()
		return errAppTurnBusy
	}
	c.taskTurn = turn
	c.busy, c.turnAt = true, s.now()
	c.mu.Unlock()
	defer func() { c.mu.Lock(); c.taskTurn = nil; c.mu.Unlock() }()
	params := s.appTurnParams(c, order, st.CxEffort)
	if a.recover || a.replyOnce {
		params["model"] = st.Model
	}
	if queued.ID != "" {
		if err = s.appTaskQueueMark(a.sid, queued.ID, appTaskDelivery{sent: true}); err != nil {
			return err
		}
		params["clientUserMessageId"] = queued.ID
	} else {
		s.appSayRecord(a.sid, order)
	}
	raw, err := c.proc.call("turn/start", params)
	if err != nil {
		var rejected *appError
		if queued.ID != "" && errors.As(err, &rejected) && appTaskRecoveryPermanent(err) {
			err = errors.Join(err, s.appTaskQueueMark(a.sid, queued.ID, appTaskDelivery{}))
		}
		return err
	}
	var started struct {
		Turn struct {
			ID string `json:"id"`
		} `json:"turn"`
	}
	if json.Unmarshal(raw, &started) != nil || started.Turn.ID == "" {
		return errors.New("клиент не назвал ID хода конвейера")
	}
	if queued.ID != "" {
		if err = s.appTaskQueueFeed(a.sid, queued); err != nil {
			return err
		}
	}
	a.lifecycle.Unlock()
	locked = false
	ready := func() {
		if a.replyOnce && a.firstClaim == "" {
			a.firstClaim = queued.Claim
		}
		select {
		case a.ready <- nil:
		default:
		}
	}
	var confirm func()
	if recovering {
		confirm = func() {
			s.appTaskRecoveryNote(a.sid, "Конвейер Codex продолжен в исходном разговоре", false, "confirmed-"+st.TaskRunID)
			ready()
		}
	} else {
		ready()
	}
	if err = c.waitTaskTurn(turn, started.Turn.ID, a.stop, appBusyCap, confirm); err != nil {
		return err
	}
	if queued.ID != "" {
		if err = s.appTaskQueueMark(a.sid, queued.ID, appTaskDelivery{sent: true, accepted: true}); err != nil {
			return err
		}
	}
	s.appTaskRecoveryProgress(a.sid, a.root, a.task, a.pid)
	return nil
}

func (s *server) startAppTask(w http.ResponseWriter, p *Project, id string, h *Harness, tier, effort, order string) {
	sid := "as" + msgID()
	st := chatStore{Task: id, Project: p.Name, Model: h.tierModel(tier), Harness: h.Name, Protocol: h.Protocol, TaskRun: true, CxEffort: effort, Born: s.now().Unix()}
	s.raiseAppTask(w, p, sid, st, h, tier, order, appTaskStartOptions{}, appTaskMessage{})
}

type appTaskStartOptions struct {
	replyOnce bool
	recover   bool
}

func (s *server) raiseAppTask(w http.ResponseWriter, p *Project, sid string, st chatStore, h *Harness, tier, order string, options appTaskStartOptions, message appTaskMessage) error {
	replyOnce, recover := options.replyOnce, options.recover
	id := st.Task
	respond := func(status int, body map[string]string) {
		if w != nil {
			writeJSON(w, status, body)
		}
	}
	if st.CxEffort == "" {
		raw, err := runProcQuiet(p.Path, true, binPath(agentctlBin), "pick", id)
		if err == nil {
			_, _, st.CxEffort = pickLines(raw)
		}
		if st.CxEffort == "" {
			st.CxEffort = "medium"
		}
	}
	fail := func(err error) error {
		if !recover {
			if !taskhead.Working(taskhead.LockPath(realHomeFn(), id)) {
				s.mainChatAssign(id, sid, p.Name, "работа")
			}
			s.appFeedAppend(sid, []reply{{Role: "assistant", Fail: true, Text: "конвейер задачи не поднялся: " + err.Error(), Time: s.now().UTC().Format(time.RFC3339)}})
		}
		status := http.StatusBadGateway
		body := map[string]string{"error": err.Error(), "session": sid}
		var conflict *appThreadWriterConflict
		if errors.As(err, &conflict) || errors.Is(err, errAppStarting) {
			status = http.StatusConflict
			body["refused"] = err.Error()
		}
		respond(status, body)
		return err
	}
	if !replyOnce && !recover {
		if err := s.chatStoreWrite(sid, st); err != nil {
			return fail(err)
		}
	}
	dir, err := os.MkdirTemp("/tmp", "dk-at-")
	if err != nil {
		return fail(err)
	}
	socket := filepath.Join(dir, "turn")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		os.RemoveAll(dir)
		return fail(err)
	}
	if err = os.Chmod(socket, 0600); err != nil {
		listener.Close()
		os.RemoveAll(dir)
		return fail(err)
	}
	q, err := s.headRequest(p, id, "", h, st.Model, order, runPrompt("in-progress", id), "работа", false, st.CxEffort)
	if err != nil {
		listener.Close()
		os.RemoveAll(dir)
		return fail(err)
	}
	a := &appTask{sid: sid, root: p.Path, task: id, home: q.Home, dir: dir, listener: listener, token: msgID(), launched: make(chan struct{}), ready: make(chan error, 1), stop: make(chan struct{}), recover: recover, replyOnce: replyOnce, replyClaim: message.Claim}
	q.ContinueStopped, q.ExpectedRun = replyOnce, st.TaskRunID
	q.AppServer = &taskhead.AppServerCarrier{ReplyOnce: replyOnce, Chat: sid, Recover: recover, Command: []string{"python3", taskRunPath(s.cfg.Roots), "--app-turn-client", socket, a.token}}
	if recover {
		q.AppServer.BeforeStart = func() error { return s.appTaskRecoveryGuard(p, sid, st) }
	}

	s.appMu.Lock()
	for _, existing := range s.appTasks {
		if existing.sid == sid || existing.root == p.Path && existing.task == id {
			s.appMu.Unlock()
			listener.Close()
			os.RemoveAll(dir)
			why := errors.New("конвейер задачи уже идёт")
			respond(http.StatusConflict, map[string]string{"error": why.Error()})
			return why
		}
	}
	s.appTasks[sid] = a
	s.appMu.Unlock()
	if replyOnce || recover {
		if live := s.appLive(sid); live != nil && live.busyNow() {
			s.appMu.Lock()
			delete(s.appTasks, sid)
			s.appMu.Unlock()
			listener.Close()
			os.RemoveAll(dir)
			close(a.launched)
			why := errors.New("ход прежнего клиента ещё идёт: дождитесь ответа либо остановите его кнопкой стоп")
			respond(http.StatusConflict, map[string]string{"error": why.Error()})
			return why
		}
		s.appDrop(sid, "возобновление рабочего запуска задачи")
	}
	s.appWorkers.Go(func() { s.appTaskServe(a, p) })
	res, err := taskhead.Raise(q)
	a.pid = res.PID
	st.TaskRunPID = res.PID
	if err == nil && res.Code == taskhead.CodeRaised {
		var state *taskstate.Guard
		state, err = taskstate.Acquire(q.Home, p.Path, id)
		if err == nil {
			if state.State.Run == nil {
				err = errors.New("состояние запуска задачи не подтверждено")
			} else {
				st.TaskRun, st.TaskRunRoot, st.TaskRunID, st.TaskRunSession = true, state.State.Root, state.State.Run.ID, sid
				err = s.chatStoreUpdate(sid, func(current chatStore) chatStore {
					current.TaskRun, current.TaskRunRoot, current.TaskRunID, current.TaskRunSession, current.TaskRunPID = st.TaskRun, st.TaskRunRoot, st.TaskRunID, st.TaskRunSession, st.TaskRunPID
					return current
				})
			}
			state.Close()
		}
	}
	close(a.launched)
	if err != nil || res.Code != taskhead.CodeRaised {
		s.appTaskClose(a)
		if err == nil && res.Code == taskhead.CodeBusy {
			why := errors.New(runBusy(id, "общий замок головы занят"))
			respond(http.StatusConflict, map[string]string{"error": why.Error()})
			return why
		}
		if err == nil {
			err = fmt.Errorf("запуск не удался: %v", res.Lines)
		}
		return fail(err)
	}
	if !recover {
		s.mainChatAssign(id, sid, p.Name, "работа")
	}
	s.appWorkers.Go(func() {
		tick := time.NewTicker(100 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-a.stop:
				return
			case <-tick.C:
				if !taskhead.Alive(a.pid) {
					s.appTaskClose(a)
					return
				}
			}
		}
	})
	select {
	case err = <-a.ready:
	case <-a.stop:
		select {
		case err = <-a.ready:
		default:
			err = errors.New("оболочка задачи вышла до первого хода")
		}
	case <-time.After(appCallCap):
		err = errors.New("оболочка задачи не подтвердила первый рабочий ход в срок")
	}
	if err != nil {
		s.appTaskClose(a)
		return fail(err)
	}
	if replyOnce {
		if a.firstClaim != message.Claim {
			s.appTaskQueued(w, sid, message.ID)
			return nil
		}
		s.chatSayDone(sid, message.Claim, "appserver")
	}
	body := map[string]string{"way": "appserver", "id": id, "kind": "task", "session": sid, "harness": h.Name, "model": st.Model, "tier": tier, "effort": st.CxEffort, "message": "конвейер задачи " + id + " поднят через app-server, модель " + st.Model + ", effort " + st.CxEffort}
	if message.ID != "" {
		body["app_message_id"] = message.ID
	}
	respond(http.StatusOK, body)
	return nil
}

func (s *server) appTaskToolEnv(sid string, st chatStore, env map[string]string) {
	if !s.appTaskOwns(sid, st) {
		if st.TaskRunPID > 0 {
			env[taskhead.LockFromEnv], env["DEVKIT_HEADLESS"], env["DEVKIT_EFFORT"] = "", "", ""
		}
		return
	}
	env["DEVKIT_TASK"] = st.Task
	env[taskhead.LockFromEnv] = strconv.Itoa(st.TaskRunPID)
	env["DEVKIT_HEADLESS"] = "task-run"
	env["DEVKIT_EFFORT"] = st.CxEffort
}

type appTaskTurnFailure struct {
	id, status string
	cause      json.RawMessage
}

func (e *appTaskTurnFailure) Error() string {
	return fmt.Sprintf("ход %s завершился состоянием %s: %s", e.id, e.status, e.cause)
}

func appTaskCompletion(params json.RawMessage, thread, turn string) (bool, error) {
	var end struct {
		Thread string `json:"threadId"`
		Turn   struct {
			ID     string          `json:"id"`
			Status string          `json:"status"`
			Error  json.RawMessage `json:"error"`
		} `json:"turn"`
	}
	if json.Unmarshal(params, &end) != nil || end.Thread != thread || end.Turn.ID != turn {
		return false, nil
	}
	if end.Turn.Status != "completed" {
		return true, &appTaskTurnFailure{id: end.Turn.ID, status: end.Turn.Status, cause: end.Turn.Error}
	}
	return true, nil
}

func (s *server) appTaskOwns(sid string, st chatStore) bool {
	if !st.TaskRun {
		return false
	}
	a := s.appTaskFind(sid, "", "")
	if a == nil {
		return false
	}
	select {
	case <-a.launched:
	default:
		return false
	}
	return st.Task == a.task && st.TaskRunPID > 0 && st.TaskRunPID == a.pid && taskhead.Owner(taskhead.LockPath(a.home, a.task)) == a.pid && taskhead.Working(taskhead.LockPath(a.home, a.task))
}

// Старые записи сохраняли только PID; сверка состояния и назначения отличает
// прежний запуск с формы от чата, которому вручную приписали ID задачи.
func (s *server) appTaskContinuation(p *Project, sid string, st *chatStore) (bool, error) {
	if st.TaskRunPID <= 0 && st.TaskRunID == "" && !st.TaskRun {
		return false, nil
	}
	if st.Task == "" || st.Thread == "" || st.Project != p.Name {
		return false, errors.New("исходный рабочий разговор задачи не подтверждён")
	}
	raw, err := boardJSON(s.cfg.Home, p.Path)
	if err != nil {
		return false, fmt.Errorf("состояние задачи недоступно: %w", err)
	}
	rows, err := parseBoardRows(raw)
	if err != nil {
		return false, fmt.Errorf("состояние задачи не прочитано: %w", err)
	}
	row, ok := rows[st.Task]
	if !ok {
		if _, closed := s.archiveRows(p.Path)[st.Task]; closed {
			st.TaskRun = false
			return false, nil
		}
		return false, errors.New("задача отсутствует на доске; рабочее продолжение недоступно")
	}
	if row.Sect == "done" || row.Sect == "archive" {
		st.TaskRun = false
		return false, nil
	}
	switch row.Sect {
	case "backlog", "ready", "in-progress", "check", "blocked":
	default:
		return false, errors.New("состояние задачи не допускает рабочее продолжение")
	}
	state, err := taskstate.Acquire(realHomeFn(), p.Path, st.Task)
	if err != nil {
		return false, fmt.Errorf("состояние запуска недоступно: %w", err)
	}
	defer state.Close()
	run := state.State.Run
	if run == nil || run.ID == "" || run.PID != st.TaskRunPID || run.Harness != st.Harness || run.Model != st.Model || run.AppChat != "" && run.AppChat != sid {
		return false, errors.New("исходный запуск задачи изменился либо не подтверждён")
	}
	if st.TaskRunID != "" {
		if st.TaskRunID != run.ID || st.TaskRunRoot != state.State.Root || st.TaskRunSession != sid {
			return false, errors.New("исходный запуск задачи относится к другому дереву либо запуску")
		}
	} else {
		bind := s.mainChatBind(st.Task)
		if bind.Chat != sid || bind.Project != p.Name || bind.Source != mainchat.ByWork {
			return false, errors.New("происхождение прежнего рабочего разговора не подтверждено")
		}
	}
	st.TaskRunID, st.TaskRunRoot, st.TaskRunSession = run.ID, state.State.Root, sid
	return true, nil
}
