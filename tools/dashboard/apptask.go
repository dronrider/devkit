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

	"github.com/dronrider/devkit/internal/taskhead"
)

type appTaskNote struct {
	Method string
	Params json.RawMessage
}

type appTask struct {
	sid       string
	root      string
	task      string
	home      string
	dir       string
	listener  net.Listener
	token     string
	launched  chan struct{}
	ready     chan error
	stop      chan struct{}
	once      sync.Once
	lifecycle sync.Mutex
	pid       int
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
		st := s.chatStoreRead(a.sid)
		st.TaskRun = false
		if err := s.chatStoreWrite(a.sid, st); err != nil {
			s.logf("конец конвейера %s не записался: %v", a.task, err)
		}
		s.appMu.Lock()
		delete(s.appTasks, a.sid)
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
		if err != nil {
			select {
			case a.ready <- err:
			default:
			}
			s.appFeedAppend(a.sid, []reply{{Role: "assistant", Fail: true, Text: "конвейер задачи прерван: " + err.Error(), Time: s.now().UTC().Format(time.RFC3339)}})
			s.appDrop(a.sid, "проход задачи завершился отказом")
			select {
			case <-a.stop:
				return
			default:
			}
		}
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
			code, why = 2, err.Error()
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
		if h == nil || h.Protocol != "codex" {
			return errors.New("подписка конвейера Codex не определилась")
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
		if err = s.chatStoreWrite(a.sid, st); err != nil {
			return err
		}
	}
	notes := make(chan appTaskNote, 128)
	c.mu.Lock()
	c.taskNotes = notes
	c.busy, c.turnAt = true, s.now()
	c.mu.Unlock()
	defer func() { c.mu.Lock(); c.taskNotes = nil; c.mu.Unlock() }()
	s.appSayRecord(a.sid, req.Order)
	raw, err := c.proc.call("turn/start", map[string]any{"threadId": c.thread, "input": []map[string]string{{"type": "text", "text": req.Order}}, "effort": appEffort(st.CxEffort)})
	if err != nil {
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
	a.lifecycle.Unlock()
	locked = false
	select {
	case a.ready <- nil:
	default:
	}
	timer := time.NewTimer(appBusyCap)
	defer timer.Stop()
	for {
		select {
		case <-a.stop:
			return errors.New("конвейер задачи остановлен")
		case <-c.proc.dead:
			return fmt.Errorf("процесс клиента вышел: %s", c.proc.errWhyFmt())
		case <-timer.C:
			return errors.New("ход конвейера не завершился в срок")
		case n := <-notes:
			if n.Method != "turn/completed" {
				continue
			}
			complete, err := appTaskCompletion(n.Params, c.thread, started.Turn.ID)
			if complete {
				c.mu.Lock()
				c.busy = false
				c.mu.Unlock()
				return err
			}
		}
	}
}

func (s *server) startAppTask(w http.ResponseWriter, p *Project, id string, h *Harness, tier, effort, order string) {
	sid := "as" + msgID()
	st := chatStore{Task: id, Project: p.Name, Model: h.tierModel(tier), Harness: h.Name, Protocol: h.Protocol, TaskRun: true, CxEffort: effort, Born: s.now().Unix()}
	if st.CxEffort == "" {
		raw, err := runProcQuiet(p.Path, true, binPath(agentctlBin), "pick", id)
		if err == nil {
			_, _, st.CxEffort = pickLines(raw)
		}
		if st.CxEffort == "" {
			st.CxEffort = "medium"
		}
	}
	fail := func(err error) {
		if !taskhead.Working(taskhead.LockPath(realHomeFn(), id)) {
			s.mainChatAssign(id, sid, p.Name, "работа")
		}
		s.appFeedAppend(sid, []reply{{Role: "assistant", Fail: true, Text: "конвейер задачи не поднялся: " + err.Error(), Time: s.now().UTC().Format(time.RFC3339)}})
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error(), "session": sid})
	}
	if err := s.chatStoreWrite(sid, st); err != nil {
		fail(err)
		return
	}
	dir, err := os.MkdirTemp("/tmp", "dk-at-")
	if err != nil {
		fail(err)
		return
	}
	socket := filepath.Join(dir, "turn")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		os.RemoveAll(dir)
		fail(err)
		return
	}
	if err = os.Chmod(socket, 0600); err != nil {
		listener.Close()
		os.RemoveAll(dir)
		fail(err)
		return
	}
	q, err := s.headRequest(p, id, "", h, st.Model, order, runPrompt("in-progress", id), "работа", false, st.CxEffort)
	if err != nil {
		listener.Close()
		os.RemoveAll(dir)
		fail(err)
		return
	}
	a := &appTask{sid: sid, root: p.Path, task: id, home: q.Home, dir: dir, listener: listener, token: msgID(), launched: make(chan struct{}), ready: make(chan error, 1), stop: make(chan struct{})}
	q.AppServer = &taskhead.AppServerCarrier{Command: []string{"python3", taskRunPath(s.cfg.Roots), "--app-turn-client", socket, a.token}}
	s.appMu.Lock()
	s.appTasks[sid] = a
	s.appMu.Unlock()
	go s.appTaskServe(a, p)
	res, err := taskhead.Raise(q)
	a.pid = res.PID
	st.TaskRunPID = res.PID
	if err == nil && res.Code == taskhead.CodeRaised {
		err = s.chatStoreWrite(sid, st)
	}
	close(a.launched)
	if err != nil || res.Code != taskhead.CodeRaised {
		s.appTaskClose(a)
		if err == nil && res.Code == taskhead.CodeBusy {
			writeJSON(w, http.StatusConflict, map[string]string{"error": runBusy(id, "общий замок головы занят")})
			return
		}
		if err == nil {
			err = fmt.Errorf("запуск не удался: %v", res.Lines)
		}
		fail(err)
		return
	}
	s.mainChatAssign(id, sid, p.Name, "работа")
	go func() {
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
	}()
	select {
	case err = <-a.ready:
	case <-a.stop:
		select {
		case err = <-a.ready:
		default:
			err = errors.New("оболочка задачи вышла до первого хода")
		}
	case <-time.After(appCallCap):
		err = errors.New("оболочка задачи не начала первый ход в срок")
	}
	if err != nil {
		s.appTaskClose(a)
		fail(err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"id": id, "kind": "task", "session": sid, "harness": h.Name, "model": st.Model, "tier": tier, "effort": st.CxEffort, "message": "конвейер задачи " + id + " поднят через app-server, модель " + st.Model + ", effort " + st.CxEffort})
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
		return true, fmt.Errorf("ход %s завершился состоянием %s: %s", end.Turn.ID, end.Turn.Status, end.Turn.Error)
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
