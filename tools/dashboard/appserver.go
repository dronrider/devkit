package main

// Сервер приложения клиента чужого протокола (Codex app-server): дочерний
// процесс с обменом JSONL по stdin/stdout. Контракт проверен прямым прогоном
// установленного клиента (DK-1624): запрос это строка с id и method, ответ
// приходит тем же id, уведомления (method без id) идут между ответами, и
// конца хода_result самого по себе не значит: ждут уведомление turn/completed.

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

// appServer это поднятый процесс сервера приложения. Горутина чтения разбирает
// stdout построчно: ответы уходят ждущим по карте, уведомления зовут note.
type appServer struct {
	cmd  *exec.Cmd
	in   io.WriteCloser
	note func(method string, params json.RawMessage)

	mu      sync.Mutex
	pending map[int64]chan *appMsg
	nextID  int64
	closed  bool

	// Смерть процесса видна один раз: канал закрывается, а слова причины
	// собираются из stderr к этому моменту.
	dead   chan struct{}
	deadDo sync.Once
	errMu  sync.Mutex
	errLog []string
	errWhy string
}

// appServerStart поднимает сервер приложения клиента через обвязку agentctl
// exec: пары окружения подписки (каталог конфигурации, токен) кладёт она, и
// чат мимо обвязки не идёт (то же правило, что у tmux-дороги, DK-1271). dir это
// рабочий каталог разговора: нить клиента привязывается к нему и работает в нём.
func appServerStart(h *Harness, dir, agentctl string, note func(method string, params json.RawMessage)) (*appServer, error) {
	cmd := exec.Command(agentctl, "exec", "--harness", h.Name, "--", h.Bin, "app-server", "--stdio")
	cmd.Dir = dir
	cmd.Env = homeEnvAt(realHome(), false)
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
	go p.readOut(stdout)
	go p.readErr(stderr)
	go p.await()
	return p, nil
}

// readOut разбирает stdout: ответы находят ждущего по id, уведомления уходят
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

// errWhyFmt это причина смерти словами: отказ процесса, а при молчаливом
// выходе хвост stderr, которым клиент обычно и называет свою причину.
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

// call шлёт запрос и ждёт ответа. Пустой msg это смерть процесса: refusal
// собирается из причины смерти.
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
		ID     int64 `json:"id"`
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
			return nil, fmt.Errorf("%s: %s", method, msg.Error.Error())
		}
		return msg.Result, nil
	case <-time.After(appCallCap):
		p.resolve(id, nil)
		return nil, fmt.Errorf("%s не ответил за %s", method, appCallCap)
	}
}

// notify шлёт уведомление без ответа: им останавливают ход, и отказ записи
// значит, что процесс уже мёртв.
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

// kill снимает процесс: им кончают ход, на который клиент не отвечает, и
// снимают живой процесс под смену модели. Повторный вызов безобиден.
func (p *appServer) kill() {
	p.cmd.Process.Kill()
	select {
	case <-p.dead:
	case <-time.After(5 * time.Second):
	}
}

// gone говорит, закрылся ли процесс.
func (p *appServer) gone() bool {
	select {
	case <-p.dead:
		return true
	default:
		return false
	}
}
