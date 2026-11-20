package main

// Codex app-server принимает JSON-RPC через stdin/stdout отдельного процесса
// или через WebSocket общего сервера. Ответ сохраняет ID запроса. Уведомление
// turn/completed обозначает конец хода независимо от ответа на turn/start.

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// appCallCap это срок одного запроса к серверу приложения. Подъём клиента
// бывает занимает десятки секунд, и минутный срок даёт ему подняться, не
// оставляя реплику человека в вечном ожидании.
const appCallCap = time.Minute

// appMsg это одна строка обмена: либо ответ на запрос (id и result/error),
// либо уведомление (method и params).
type appMsg struct {
	ID     *int64          `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *appError       `json:"error,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
}

// appError это отказ сервера: код идёт как есть, потому что в проверенном
// контракте он бывал и строкой, и числом, и сужать его здесь нечем.
type appError struct {
	Code    any    `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}

func (e *appError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return fmt.Sprintf("код %v", e.Code)
}

// appServer обслуживает RPC дочернего процесса или общего сервера Codex.
// Горутина чтения доставляет ответы по ID запроса и передаёт уведомления в note.
type appServer struct {
	cmd  *exec.Cmd
	in   io.WriteCloser
	note func(method string, params json.RawMessage)

	mu      sync.Mutex
	pending map[int64]chan *appMsg
	nextID  int64
	closed  bool

	// Закрытие клиента видно один раз. Для отдельного процесса причина
	// может включать хвост stderr.
	dead   chan struct{}
	deadDo sync.Once
	errMu  sync.Mutex
	errLog []string
	errWhy string
}

// appServerStart запускает отдельный процесс через agentctl exec. Обёртка
// передаёт ему окружение подписки (DK-1271). Нить работает в каталоге dir.
func (s *server) appServerStart(h *Harness, dir, agentctl string, note func(method string, params json.RawMessage), env ...string) (*appServer, error) {
	cmd := exec.Command(agentctl, "exec", "--harness", h.Name, "--", h.Bin, "app-server", "--stdio")
	cmd.Dir = dir
	cmd.Env = append(homeEnvAt(realHome(), false), env...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("процесс не поднялся: %w", err)
	}
	p := &appServer{cmd: cmd, in: stdin, note: note,
		pending: map[int64]chan *appMsg{}, dead: make(chan struct{})}
	s.appWorkers.Go(func() { p.readOut(stdout) })
	s.appWorkers.Go(func() { p.readErr(stderr) })
	s.appWorkers.Go(p.await)
	return p, nil
}

// readOut разбирает поток JSONL: ответы находят ждущего по ID, уведомления уходят
// в note. Пустой method у ответа без id это мусор протокола, и он молчит.
func (p *appServer) readOut(r io.Reader) {
	br := bufio.NewReaderSize(r, 64*1024)
	for {
		line, err := br.ReadString('\n')
		if line != "" {
			var msg appMsg
			if json.Unmarshal([]byte(line), &msg) == nil {
				if msg.ID != nil && (msg.Result != nil || msg.Error != nil) {
					p.resolve(*msg.ID, &msg)
				} else if msg.Method != "" && p.note != nil {
					p.note(msg.Method, msg.Params)
				}
			}
		}
		if err != nil {
			p.die(fmt.Sprintf("поток вывода закрылся: %v", err))
			return
		}
	}
}

// readErr копит хвост stderr: им смерть процесса называется человеку, потому
// что причины входа и отказа клиент пишет именно туда.
func (p *appServer) readErr(r io.Reader) {
	br := bufio.NewReaderSize(r, 16*1024)
	for {
		line, err := br.ReadString('\n')
		if s := strings.TrimSpace(line); s != "" {
			p.errMu.Lock()
			p.errLog = append(p.errLog, s)
			if len(p.errLog) > 12 {
				p.errLog = p.errLog[len(p.errLog)-12:]
			}
			p.errMu.Unlock()
		}
		if err != nil {
			return
		}
	}
}

// await закрывает смерть по выходу процесса.
func (p *appServer) await() {
	err := p.cmd.Wait()
	why := ""
	if err != nil {
		why = err.Error()
	}
	p.die(why)
}

// die срабатывает один раз: ждущие получают отказ, память причины собирается.
func (p *appServer) die(why string) {
	p.deadDo.Do(func() {
		p.mu.Lock()
		p.closed = true
		for _, ch := range p.pending {
			ch <- nil
		}
		clear(p.pending)
		p.mu.Unlock()
		p.errMu.Lock()
		p.errWhy = why
		p.errMu.Unlock()
		close(p.dead)
	})
}

// resolve отдаёт ответ ждущему, если он ещё ждёт.
func (p *appServer) resolve(id int64, msg *appMsg) {
	p.mu.Lock()
	ch := p.pending[id]
	delete(p.pending, id)
	p.mu.Unlock()
	if ch != nil {
		ch <- msg
	}
}

// errTail это последние строки stderr одним куском.
func (p *appServer) errTail() string {
	p.errMu.Lock()
	defer p.errMu.Unlock()
	return strings.Join(p.errLog, "\n")
}

// errWhyFmt возвращает причину закрытия и последние строки stderr,
// когда завершился отдельный процесс.
func (p *appServer) errWhyFmt() string {
	p.errMu.Lock()
	why, tail := p.errWhy, strings.Join(p.errLog, "\n")
	p.errMu.Unlock()
	select {
	case <-p.dead:
	default:
		return ""
	}
	if why != "" {
		if tail != "" {
			return why + "; " + tail
		}
		return why
	}
	if tail != "" {
		return tail
	}
	return "процесс вышел молча"
}

// call отправляет запрос и ждёт ответа. Пустой msg означает, что клиент
// закрылся. Ошибка называет причину закрытия.
func (p *appServer) call(method string, params any) (json.RawMessage, error) {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil, errors.New(p.errWhyFmt())
	}
	p.nextID++
	id := p.nextID
	ch := make(chan *appMsg, 1)
	p.pending[id] = ch
	body, err := json.Marshal(struct {
		ID     int64  `json:"id"`
		Method string `json:"method"`
		Params any    `json:"params"`
	}{id, method, params})
	if err != nil {
		delete(p.pending, id)
		p.mu.Unlock()
		return nil, err
	}
	_, werr := p.in.Write(append(body, '\n'))
	p.mu.Unlock()
	if werr != nil {
		p.resolve(id, nil)
		return nil, fmt.Errorf("запрос %s не записался: %w", method, werr)
	}
	select {
	case msg := <-ch:
		if msg == nil {
			return nil, fmt.Errorf("процесс умер, не ответив на %s: %s", method, p.errWhyFmt())
		}
		if msg.Error != nil {
			return nil, fmt.Errorf("%s: %w", method, msg.Error)
		}
		return msg.Result, nil
	case <-time.After(appCallCap):
		p.resolve(id, nil)
		return nil, fmt.Errorf("%s не ответил за %s", method, appCallCap)
	}
}

// notify отправляет уведомление без RPC-ответа. Ошибка записи сообщает,
// что дашборд не смог отправить уведомление.
func (p *appServer) notify(method string, params any) error {
	body, err := json.Marshal(struct {
		Method string `json:"method"`
		Params any    `json:"params"`
	}{method, params})
	if err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return errors.New("процесс уже закрыт")
	}
	_, err = p.in.Write(append(body, '\n'))
	return err
}

// kill завершает отдельный процесс или закрывает соединение с общим сервером.
// Повторный вызов безопасен.
func (p *appServer) kill() {
	if p.cmd == nil {
		p.in.Close()
	} else {
		p.cmd.Process.Kill()
	}
	select {
	case <-p.dead:
	case <-time.After(5 * time.Second):
	}
}

// gone сообщает, закрылся ли клиент.
func (p *appServer) gone() bool {
	select {
	case <-p.dead:
		return true
	default:
		return false
	}
}
