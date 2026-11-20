package main

// Разговор чужого протокола (дорога app-server). Подъём чата семейства
// claude-code идёт tmux-окном с клавиатурным входом, а у клиента чужого
// протокола терминальной механики нет: его сервер приложения поднимается
// дочерним процессом этого демона, реплики уходят запросами, а лента
// собирается из уведомлений. Раскладку дороги называет профиль подписки ключом
// protocol, дашборд имён протоколов не сочиняет.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/dronrider/devkit/internal/chat"
	"github.com/dronrider/devkit/internal/deployconf"
	"github.com/dronrider/devkit/internal/obey"
)

// appChat это живой разговор дороги app-server: процесс сервера, нить клиента
// и состояние хода. Живёт он в памяти демона, а разговор переживает перезапуск:
// лента лежит файлом, нить клиента продолжается resume, и следующий ход
// поднимает процесс заново сам.
type appChat struct {
	sid       string
	h         *Harness
	model     string
	effort    string
	dir       string
	proc      *appServer
	thread    string
	project   string
	automatic bool

	mu       sync.Mutex
	busy     bool
	turnAt   time.Time
	taskTurn *appTaskTurn
}

func (c *appChat) busyNow() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.busy
}

// setEffort и effortNow держат уровень глубины под тем же замком, что и
// занятость: реплика пишет его из HTTP-горутины, а ход читает при отправке,
// и две одновременные реплики без замка гоняли бы строковое поле.
func (c *appChat) setEffort(e string) {
	c.mu.Lock()
	c.effort = e
	c.mu.Unlock()
}

func (c *appChat) effortNow() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.effort
}

// appBusyCap это потолок одного хода без новостей: дольше он не «идёт», а
// висит, и состояние чата обязано это назвать, а не молчать часами.
const appBusyCap = 30 * time.Minute

func (s *server) appAutomaticReview(sid string, st chatStore) bool {
	return st.GroomSource != "" || s.appTaskOwns(sid, st)
}

type appFeedEvent struct {
	Method        string         `json:"method"`
	ThreadID      string         `json:"threadId,omitempty"`
	TurnID        string         `json:"turnId,omitempty"`
	Item          map[string]any `json:"item"`
	StartedAtMS   *int64         `json:"startedAtMs,omitempty"`
	CompletedAtMS *int64         `json:"completedAtMs,omitempty"`
}

func appEventIdentity(method, thread, turn, id string) string {
	if id == "" {
		return ""
	}
	return method + "\x00" + thread + "\x00" + turn + "\x00" + id
}

func (e *appFeedEvent) identity() string {
	if e == nil {
		return ""
	}
	id, _ := e.Item["id"].(string)
	return appEventIdentity(e.Method, e.ThreadID, e.TurnID, id)
}

type appFeedIndex struct {
	info    os.FileInfo
	offset  int64
	pending []byte
	seen    map[string]bool
}

// Поля индекса меняются под файловым замком appFeedAppend. appMu защищает
// только таблицу индексов, чтение диска не задерживает остальные разговоры.
func (s *server) appFeedIndex(sid string) *appFeedIndex {
	s.appMu.Lock()
	defer s.appMu.Unlock()
	if s.appFeedIndexes == nil {
		s.appFeedIndexes = map[string]*appFeedIndex{}
	}
	if s.appFeedIndexes[sid] == nil {
		s.appFeedIndexes[sid] = &appFeedIndex{}
	}
	return s.appFeedIndexes[sid]
}

func (idx *appFeedIndex) remember(line []byte) {
	var record struct {
		App struct {
			Method   string `json:"method"`
			ThreadID string `json:"threadId"`
			TurnID   string `json:"turnId"`
			Item     struct {
				ID string `json:"id"`
			} `json:"item"`
		} `json:"app"`
	}
	if json.Unmarshal(line, &record) == nil {
		app := record.App
		if id := appEventIdentity(app.Method, app.ThreadID, app.TurnID, app.Item.ID); id != "" {
			idx.seen[id] = true
		}
	}
}

func (idx *appFeedIndex) refresh(f *os.File) error {
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if idx.info == nil || !os.SameFile(idx.info, info) || info.Size() < idx.offset ||
		(info.Size() == idx.offset && !info.ModTime().Equal(idx.info.ModTime())) {
		idx.offset, idx.pending, idx.seen = 0, nil, map[string]bool{}
	}
	if info.Size() > idx.offset {
		data, err := io.ReadAll(io.NewSectionReader(f, idx.offset, info.Size()-idx.offset))
		feedIO.opens.Add(1)
		feedIO.bytes.Add(int64(len(data)))
		if err != nil {
			return err
		}
		idx.offset += int64(len(data))
		data = append(idx.pending, data...)
		lines := bytes.Split(data, []byte{'\n'})
		for _, line := range lines[:len(lines)-1] {
			idx.remember(line)
		}
		idx.pending = bytes.Clone(lines[len(lines)-1])
		if len(idx.pending) > 0 {
			idx.remember(idx.pending)
		}
	}
	idx.info = info
	return nil
}

// appFeedDir это каталог лент дороги app-server, рядом с записями разговоров.
func appFeedDir(home string) string {
	return filepath.Join(chatStoreDir(home), "appserver")
}

func (s *server) appFeedFile(sid string) string {
	return filepath.Join(appFeedDir(s.cfg.Home), sid+".jsonl")
}

// appFeedAppend дописывает записи в ленту разговора. Ключ записи это её номер
// в файле: он устойчив, как «источник:номер» транскриптной ленты, и по нему же
// режется история.
func (s *server) appFeedAppend(sid string, items []reply) error {
	file := s.appFeedFile(sid)
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return err
	}
	lock, err := chat.Lock(file + ".lock")
	if err != nil {
		return err
	}
	defer lock.Close()
	f, err := os.OpenFile(file, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	idx := s.appFeedIndex(sid)
	if err := idx.refresh(f); err != nil {
		return err
	}
	if len(idx.pending) > 0 {
		if _, err := f.Write([]byte{'\n'}); err != nil {
			return err
		}
		idx.offset++
		idx.pending = nil
	}
	for _, it := range items {
		if id := it.App.identity(); id != "" {
			if idx.seen[id] {
				continue
			}
		}
		body, err := json.Marshal(it)
		if err != nil {
			return err
		}
		line := append(body, '\n')
		n, err := f.Write(line)
		if err != nil {
			return err
		}
		if n != len(line) {
			return io.ErrShortWrite
		}
		idx.offset += int64(n)
		if id := it.App.identity(); id != "" {
			idx.seen[id] = true
		}
	}
	idx.info, err = f.Stat()
	return err
}

// appFeedRead читает ленту разговора целиком: файл это и есть разговор, он
// растёт хвостом и не переписывается. Ключ и номер записи ставятся при чтении.
func (s *server) appFeedRead(sid string) []reply {
	data, err := os.ReadFile(s.appFeedFile(sid))
	if err != nil {
		return nil
	}
	return appFeedParse(data)
}

// appFeedParse разбирает ленту из прочитанного файла: ключ и номер записи это
// её строка в файле, и разбор отделён от чтения, чтобы поток брал записи и
// смещение хвоста из одного снимка.
func appFeedParse(data []byte) []reply {
	out := []reply{}
	for i, line := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
		if line == "" {
			continue
		}
		var it reply
		if json.Unmarshal([]byte(line), &it) != nil {
			continue
		}
		it.Seq, it.Key = i, fmt.Sprintf("app:%d", i)
		if appFeedDisplay(&it) {
			out = append(out, it)
		}
	}
	return out
}

func appFeedDisplay(it *reply) bool {
	if it.Role == "user" {
		if it.Wire == "" {
			it.Wire = it.Text
		}
		w := cutWraps(it.Text)
		it.Text = w.rest
		if len(w.shots) > 0 {
			it.Shots = w.shots
		}
		if len(w.frags) > 0 {
			it.Frags = w.frags
		}
		if w.sel != "" {
			it.Sel, it.SelFile = w.sel, w.file
		}
		if w.pick != "" {
			it.Pick, it.PickScreen = w.pick, w.screen
		}
	}
	if it.Role == "tool" && (it.Tool == "userMessage" ||
		((it.Tool == "collabAgentToolCall" || it.Tool == "subAgentActivity") && it.App == nil && strings.TrimSpace(it.Text+it.Note) == "")) {
		return false
	}
	return true
}

func appFirst(items []reply) string {
	for _, it := range items {
		if it.Role != "user" {
			continue
		}
		if text := strings.TrimSpace(it.Text); text != "" {
			return truncate(text, 200)
		}
		if len(it.Shots) > 0 {
			return "Снимок экрана"
		}
		if len(it.Frags) > 0 {
			return "Текстовый фрагмент"
		}
	}
	return ""
}

// appLive отдает живой процесс разговора. Мёртвый процесс вычищается на месте:
// смерть могла прийти между заходами, и лента узнаёт о ней здесь же.
func (s *server) appLive(sid string) *appChat {
	s.appMu.Lock()
	c := s.appChats[sid]
	if c == nil {
		s.appMu.Unlock()
		return nil
	}
	if !c.proc.gone() {
		s.appMu.Unlock()
		return c
	}
	s.appMu.Unlock()
	s.appDead(sid, c)
	return nil
}

// appDead убирает мёртвый процесс из реестра и, если ход был открыт, называет
// его обрыв в ленте: вечного ожидания ответа дорога не оставляет.
func (s *server) appDead(sid string, expected *appChat) {
	s.appMu.Lock()
	c := s.appChats[sid]
	if c != expected {
		s.appMu.Unlock()
		return
	}
	delete(s.appChats, sid)
	s.appMu.Unlock()
	if c == nil {
		return
	}
	c.mu.Lock()
	busy := c.busy
	c.busy = false
	why := c.proc.errWhyFmt()
	c.mu.Unlock()
	if busy {
		s.appFeedAppend(sid, []reply{{Role: "assistant", Fail: true,
			Text: "ход прерван: процесс клиента вышел (" + why + ")",
			Time: s.now().UTC().Format(time.RFC3339)}})
		s.logf("процесс app-server разговора %s вышел посреди хода", sid)
	}
}

type appThreadWriterConflict struct {
	thread string
	cause  error
}

func (e *appThreadWriterConflict) Error() string {
	return fmt.Sprintf("разговор %s открыт в другом клиенте Codex. Закройте этот разговор в другом клиенте, дождитесь выгрузки нити и повторите отправку здесь. История и выбранная модель сохранены", e.thread)
}

func (e *appThreadWriterConflict) Unwrap() error { return e.cause }

func (s *server) appAutonomousInstruction() (string, error) {
	root := devkitOwnTree()
	if profiles := s.harnesses().ProfileDir; profiles != "" {
		root = filepath.Dir(filepath.Dir(profiles))
	}
	if root == "" {
		return "", fmt.Errorf("не найден источник правил чата задачи; выполните devkitctl doctor --fix")
	}
	path := filepath.Join(root, "RULES.board.core.md")
	text, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("не удалось прочитать правила чата задачи %s: %w", path, err)
	}
	const section = "Автономная работа"
	lines, found := obey.SectionBody(string(text), section)
	body := strings.TrimSpace(strings.Join(lines, "\n"))
	if !found || body == "" {
		return "", fmt.Errorf("в %s нет раздела «%s»; выполните devkitctl doctor --fix", path, section)
	}
	return "## " + section + "\n\n" + body, nil
}

func (s *server) appTurnParams(c *appChat, text, effort string) map[string]any {
	params := map[string]any{
		"threadId": c.thread,
		"input":    []map[string]string{{"type": "text", "text": text}},
		"effort":   appEffort(effort),
	}
	if s.chatStoreRead(c.sid).Task == "" {
		return params
	}
	value := "autonomous = false"
	if p, ok := s.projectByName(c.project); ok && deployconf.Autonomous(p.Path) {
		value = "autonomous = true"
		instruction, err := s.appAutonomousInstruction()
		if err != nil {
			message := "правило автономной работы не обновилось: " + err.Error()
			s.logf("разговор %s: %s", c.sid, message)
			if err := s.appFeedAppend(c.sid, []reply{{Role: "note", Fail: true, Text: message, Time: s.now().UTC().Format(time.RFC3339)}}); err != nil {
				s.logf("предупреждение разговора %s не записалось: %v", c.sid, err)
			}
		} else {
			value += "\n\n" + instruction
		}
	}
	params["additionalContext"] = map[string]any{"devkit-autonomous": map[string]string{"kind": "application", "value": value}}
	return params
}

var errAppStarting = errors.New("клиент разговора уже запускается: дождитесь запуска и повторите отправку")
var errAppTurnBusy = errors.New("ход уже идёт: дождитесь ответа либо остановите ход кнопкой стоп")

// appSpawn поднимает процесс сервера приложения и готовит нить: свежую либо
// продолжением. Отказ на любом шаге убивает процесс и называется словами с
// хвостом stderr, потому что причины входа клиент пишет именно туда.
func (s *server) appSpawn(sid string, h *Harness, model, dir, project, thread string, cancelled ...<-chan struct{}) (*appChat, error) {
	s.appMu.Lock()
	if s.appStarting[sid] != nil || s.appChats[sid] != nil {
		s.appMu.Unlock()
		return nil, errAppStarting
	}
	started := make(chan struct{})
	s.appStarting[sid] = started
	s.appMu.Unlock()
	defer func() {
		s.appMu.Lock()
		delete(s.appStarting, sid)
		close(started)
		s.appMu.Unlock()
	}()
	c := &appChat{sid: sid, h: h, model: model, dir: dir, project: project, thread: thread}
	st := s.chatStoreRead(sid)
	c.automatic = s.appAutomaticReview(sid, st)
	toolEnv := map[string]string{"DEVKIT_CHAT_SESSION_ID": sid, "DEVKIT_HARNESS": h.Name}
	if st.GroomSource != "" {
		toolEnv["DEVKIT_TASK"], toolEnv["DEVKIT_GROOM_SOURCE"], toolEnv["DEVKIT_GROOM_MODE"] = st.GroomSource, st.GroomSource, st.GroomMode
	}
	s.appTaskToolEnv(sid, st, toolEnv)
	var env []string
	for key, value := range toolEnv {
		env = append(env, key+"="+value)
	}
	proc, err := s.appServerStart(h, dir, binPath(agentctlBin), func(method string, params json.RawMessage) {
		s.appNote(sid, method, params)
	}, env...)
	if err != nil {
		return nil, err
	}
	c.proc = proc
	if len(cancelled) > 0 && cancelled[0] != nil {
		s.appWorkers.Go(func() {
			select {
			case <-cancelled[0]:
				proc.kill()
			case <-proc.dead:
			}
		})
	}
	fail := func(err error) (*appChat, error) {
		proc.kill()
		// Хвост stderr читается после смерти процесса: клиент пишет причину
		// отказа туда, и гонять чтение раньше выхода значило бы терять её.
		time.Sleep(200 * time.Millisecond)
		if tail := strings.TrimSpace(proc.errTail()); tail != "" {
			return nil, fmt.Errorf("%w (%s)", err, tail)
		}
		return nil, err
	}
	if _, err := proc.call("initialize", map[string]any{
		"clientInfo":   map[string]string{"name": "devkit-dashboard", "version": "0"},
		"capabilities": map[string]any{"experimentalApi": true},
	}); err != nil {
		return fail(fmt.Errorf("клиент не поднялся: %w", err))
	}
	if err := proc.notify("initialized", map[string]any{}); err != nil {
		return fail(fmt.Errorf("клиент не принял приветствие: %w", err))
	}
	config := map[string]any{}
	for key, value := range toolEnv {
		config["shell_environment_policy.set."+key] = value
	}
	policy := map[string]any{"approvalPolicy": "on-request", "approvalsReviewer": "auto_review", "config": config, "sandbox": "workspace-write"}
	var configured json.RawMessage
	if thread != "" {
		policy["threadId"], policy["excludeTurns"] = thread, true
		configured, err = proc.call("thread/resume", policy)
		if err != nil {
			var rejected *appError
			if errors.As(err, &rejected) && strings.Contains(rejected.Message, "thread already has an active writer") {
				return fail(&appThreadWriterConflict{thread: thread, cause: err})
			}
			return fail(fmt.Errorf("нить разговора не продолжилась: %w", err))
		}
	} else {
		policy["cwd"], policy["model"], policy["sandbox"] = dir, model, "workspace-write"
		raw, err := proc.call("thread/start", policy)
		configured = raw
		if err != nil {
			return fail(fmt.Errorf("клиент не завёл разговор: %w", err))
		}
		var got struct {
			Thread struct {
				ID string `json:"id"`
			} `json:"thread"`
		}
		if json.Unmarshal(raw, &got) != nil || got.Thread.ID == "" {
			return fail(fmt.Errorf("клиент не назвал нить разговора"))
		}
		c.thread = got.Thread.ID
	}
	var actual struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
		ApprovalPolicy string `json:"approvalPolicy"`
		Reviewer       string `json:"approvalsReviewer"`
		Sandbox        struct {
			Type string `json:"type"`
		} `json:"sandbox"`
	}
	if json.Unmarshal(configured, &actual) != nil || actual.ApprovalPolicy != "on-request" || actual.Reviewer != "auto_review" || actual.Sandbox.Type != "workspaceWrite" {
		return fail(fmt.Errorf("клиент не подтвердил workspace-write и автоматическую проверку эскалаций; обновите Codex"))
	}
	if thread != "" && c.automatic && actual.Thread.ID != thread {
		return fail(errors.New("клиент не подтвердил ID исходной нити Codex"))
	}
	s.appMu.Lock()
	s.appChats[sid] = c
	s.appMu.Unlock()
	// Смерть процесса после подъёма: о ней узнаёт лента сразу же, а не следующим
	// заходом человека в разговор.
	s.appWorkers.Go(func() {
		<-proc.dead
		s.appDead(sid, c)
	})
	return c, nil
}

// appEffort переводит уровень глубины дашборда в уровень клиента: верхний
// уровень клиента ниже дашбордного, и всё, что выше, идёт его потолком.
func appEffort(effort string) string {
	switch effort {
	case "low", "medium", "high", "xhigh":
		return effort
	default:
		return "xhigh"
	}
}

// appTurn отправляет реплику в нить и открывает ход. Занятость ставится до
// запроса: конец хода приходит уведомлением того же потока, вплотную за
// ответом и даже раньше него, и занятость, открытая после ответа, опаздывает
// за собственным погашением и висит вечно. Ручка отвечает сразу: реплика у
// клиента.
func (s *server) appTurn(c *appChat, text string, record bool) error {
	c.mu.Lock()
	if c.busy {
		c.mu.Unlock()
		return errAppTurnBusy
	}
	c.busy, c.turnAt = true, s.now()
	c.mu.Unlock()
	if record {
		s.appSayRecord(c.sid, text)
	}
	_, err := c.proc.call("turn/start", s.appTurnParams(c, text, c.effortNow()))
	if err != nil {
		c.mu.Lock()
		c.busy = false
		c.mu.Unlock()
		return err
	}
	return nil
}

// appNote разбирает уведомления сервера: элементы хода ложатся в ленту, конец
// хода гасит занятость, а ошибка хода приходит словами в тот же хвост ленты.
func (s *server) appNote(sid, method string, params json.RawMessage) {
	s.appMu.Lock()
	if c := s.appChats[sid]; c != nil {
		c.mu.Lock()
		if c.taskTurn != nil {
			c.taskTurn.note(method, params, time.Now())
		}
		c.mu.Unlock()
	}
	s.appMu.Unlock()
	if method != "item/started" && method != "item/completed" && method != "turn/completed" {
		return
	}
	now := s.now().UTC().Format(time.RFC3339)
	if method == "turn/completed" {
		var p struct {
			ThreadID string `json:"threadId"`
			Turn     struct {
				Status string `json:"status"`
				Error  string `json:"error"`
			} `json:"turn"`
		}
		json.Unmarshal(params, &p)
		s.appMu.Lock()
		if c := s.appChats[sid]; c != nil && (p.ThreadID == "" || p.ThreadID == c.thread) {
			c.mu.Lock()
			if c.taskTurn == nil {
				c.busy = false
			}
			c.mu.Unlock()
		}
		s.appMu.Unlock()
		if p.Turn.Error != "" {
			s.appFeedAppend(sid, []reply{{Role: "assistant", Fail: true,
				Text: "ход кончился отказом клиента: " + p.Turn.Error, Time: now}})
		}
		return
	}
	var event appFeedEvent
	if json.Unmarshal(params, &event) != nil || event.Item == nil {
		return
	}
	event.Method = method
	kind, _ := event.Item["type"].(string)
	if method == "item/started" && kind != "commandExecution" && kind != "collabAgentToolCall" && kind != "subAgentActivity" {
		return
	}
	items := appItemReplies(event.Item, now)
	s.appMu.Lock()
	owner := ""
	if c := s.appChats[sid]; c != nil {
		owner = c.thread
	}
	s.appMu.Unlock()
	for i := range items {
		items[i].App = &event
		if event.ThreadID != "" && owner != "" && event.ThreadID != owner {
			items[i].Sub = event.ThreadID
		}
	}
	if len(items) > 0 {
		if err := s.appFeedAppend(sid, items); err != nil {
			s.logf("событие %s разговора %s не записалось: %v", method, sid, err)
		}
	}
}

func appItemReplies(item map[string]any, now string) []reply {
	str := func(k string) string {
		v, _ := item[k].(string)
		return v
	}
	switch str("type") {
	case "userMessage":
		return nil
	case "agentMessage":
		if text := str("text"); strings.TrimSpace(text) != "" {
			return []reply{{Role: "assistant", Text: text, Time: now}}
		}
		return nil
	case "commandExecution":
		out := reply{Role: "tool", Tool: "Bash", Time: now,
			Note: str("command"), Text: str("aggregatedOutput")}
		out.Args = map[string]string{"command": str("command")}
		out.Fail = str("status") == "failed" || str("status") == "declined"
		if code, ok := item["exitCode"].(float64); ok && code != 0 {
			out.Fail = true
		}
		return []reply{out}
	case "collabAgentToolCall":
		args := map[string]string{}
		for _, key := range []string{"prompt", "model", "reasoningEffort", "tool", "status"} {
			if v := str(key); v != "" {
				args[key] = v
			}
		}
		return []reply{{Role: "tool", Tool: "Agent", Time: now, About: str("tool"), Text: str("prompt"), Args: args, Fail: str("status") == "failed"}}
	case "subAgentActivity":
		if str("agentPath") == "" && str("agentThreadId") == "" {
			return nil
		}
		word := map[string]string{"started": "запущен", "interacted": "получил сообщение", "interrupted": "прерван", "completed": "завершён"}[str("kind")]
		if word == "" {
			word = str("kind")
		}
		details := []string{}
		for _, key := range []string{"agentPath", "agentThreadId", "model", "reasoningEffort"} {
			if v := str(key); v != "" {
				details = append(details, v)
			}
		}
		return []reply{{Role: "note", Mark: "agent", Time: now, Note: "Субагент: " + word, Sub: str("agentPath"), Text: strings.Join(details, "\n")}}
	case "reasoning":
		if text := str("text"); strings.TrimSpace(text) != "" {
			return []reply{{Role: "thinking", Text: text, Time: now}}
		}
		return nil
	default:
		kind := str("type")
		if kind == "" {
			return nil
		}
		note := strings.TrimSpace(str("command") + " " + str("name"))
		return []reply{{Role: "tool", Tool: kind, Note: note,
			Text: str("output") + str("aggregatedOutput"), Time: now}}
	}
}

// appSayRecord кладёт реплику человека в ленту: она и есть эхо отправки,
// транскрипта клиента дорога не читает.
func (s *server) appSayRecord(sid, text string) {
	it := reply{Role: "user", Text: text, Wire: text, Time: s.now().UTC().Format(time.RFC3339)}
	appFeedDisplay(&it)
	if err := s.appFeedAppend(sid, []reply{it}); err != nil {
		s.logf("реплика разговора %s не записалась: %v", sid, err)
	}
}

// appSay это дорога реплики: живой процесс получает ход, мёртвый или новый
// разговор поднимается на месте. Отказ подъёма, занятость и пропажа входа
// называются словами, молчания тут не бывает.
func (s *server) appSay(w http.ResponseWriter, found *Project, sid, text, claim, effort string) {
	st := s.chatStoreRead(sid)
	if a := s.appTaskFind(sid, "", ""); a != nil {
		if a.recover {
			s.appTaskAcceptSay(w, sid, text, claim)
			return
		}
		writeJSON(w, http.StatusConflict, map[string]string{"error": "конвейер задачи уже идёт: дождитесь его остановки"})
		return
	}
	wasTaskRun := st.TaskRun
	continuation, err := s.appTaskContinuation(found, sid, &st)
	if err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error(), "refused": err.Error()})
		return
	}
	model, harness := st.Model, st.Harness
	if model == "" {
		model, harness = s.chatChoice(sid, "")
	}
	var entry appTaskMessage
	if continuation {
		if live := s.appLive(sid); live != nil && live.busyNow() {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "ход прежнего клиента ещё идёт: дождитесь ответа либо остановите его кнопкой стоп"})
			return
		}
		entry, err = s.appTaskQueueAdd(sid, claim, text)
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
			return
		}
		if entry.Accepted {
			s.chatSayDone(sid, claim, "appserver")
			writeJSON(w, http.StatusOK, map[string]string{"way": "dup", "was": "appserver"})
			return
		}
	}
	if !chatModelCurrent(w, model) {
		if continuation {
			s.appTaskRecoveryNote(sid, "Реплика сохранена в очереди. Исходная модель "+model+" недоступна", true, "model-"+st.TaskRunID)
		}
		return
	}
	h := s.chatHarnessOf(model, harness)
	if h == nil || !h.foreign() || continuation && st.Harness != h.Name {
		why := fmt.Sprintf("подписка разговора не определилась (модель %s): разложить слой: devkitctl doctor --fix", chatModelName(model, harness))
		if continuation {
			s.appTaskRecoveryNote(sid, "Реплика сохранена в очереди. "+why, true, "harness-"+st.TaskRunID)
		}
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": why})
		return
	}
	// Уровень глубины ложится в запись разговора каждым изменением, а не только
	// сменой модели или нити: процесс смерти переживает, и без записи выбор
	// ползунка молча возвращался к умолчанию после каждого обрыва.
	effortSet := false
	if effort != "" && effort != st.CxEffort {
		st.CxEffort, effortSet = effort, true
	}
	if st.CxEffort == "" {
		st.CxEffort = "medium"
	}
	if continuation {
		waiting, err := s.appTaskRecoveryWaiting(found, sid, st)
		if err != nil {
			why := "Реплика сохранена в очереди. " + err.Error()
			s.appTaskRecoveryNote(sid, why, true, "waiting-"+st.TaskRunID)
			writeJSON(w, http.StatusConflict, map[string]string{"error": why})
			return
		}
		if waiting {
			s.appTaskQueued(w, sid, claim)
			return
		}
		lock, err := s.appTaskLaunchLock(sid)
		if errors.Is(err, chat.ErrLocked) {
			s.appTaskQueued(w, sid, claim)
			return
		}
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
			return
		}
		defer lock.Close()
		if s.appTaskFind(sid, "", "") != nil {
			s.appTaskQueued(w, sid, claim)
			return
		}
		s.raiseAppTask(w, found, sid, st, h, "", text, true, false, entry.Claim)
		return
	}
	if wasTaskRun && !st.TaskRun {
		if err := s.chatStoreWrite(sid, st); err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "состояние завершённого запуска не записалось: " + err.Error()})
			return
		}
	}
	if live := s.appLive(sid); live != nil && live.automatic && st.TaskRunPID > 0 {
		if live.busyNow() {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "ход ещё идёт: дождитесь ответа либо остановите его кнопкой стоп"})
			return
		}
		s.appDrop(sid, "закрытая задача продолжается обычным разговором")
	}
	dir := chatTree(found.Path, st.Task)
	if st.GroomTree != "" {
		dir = st.GroomTree
	}
	c := s.appLive(sid)
	if c == nil {
		var err error
		c, err = s.appSpawn(sid, h, model, dir, found.Name, st.Thread)
		if err != nil {
			msg := err.Error()
			s.logf("разговор %s не поднялся дорогой app-server: %s", sid, msg)
			status := http.StatusBadGateway
			var conflict *appThreadWriterConflict
			if errors.As(err, &conflict) || errors.Is(err, errAppStarting) {
				status = http.StatusConflict
			}
			body := map[string]string{"error": "чат не поднялся: " + msg}
			if status == http.StatusConflict {
				body["refused"] = msg
			}
			writeJSON(w, status, body)
			return
		}
	}
	c.setEffort(st.CxEffort)
	if st.Model != model || st.Harness != harness || st.Thread != c.thread || effortSet {
		st.Model, st.Harness, st.Protocol = model, harness, h.Protocol
		if err := s.chatStoreWrite(sid, st); err != nil {
			s.logf("настройки разговора %s не записались: %v", sid, err)
		}
	}
	if err := s.appTurn(c, text, true); err != nil {
		if errors.Is(err, errAppTurnBusy) {
			writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error(), "stuck": "ход чужого протокола идёт, реплика не принята"})
			return
		}
		msg := err.Error()
		s.logf("реплика разговора %s не ушла в app-server: %s", sid, msg)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "реплика не ушла: " + msg})
		return
	}
	s.chatSayDone(sid, claim, "appserver")
	s.logf("реплика подана в app-server разговор %s (модель %s)", sid, chatModelName(model, harness))
	writeJSON(w, http.StatusOK, map[string]any{"way": "appserver", "session": sid,
		"message": "реплика ушла клиенту чужого протокола: ответ придёт в ленту"})
}

// appStart это подъём нового разговора с первой реплики: свежий ID, запись
// настроек и тот же ход, что у живого разговора. tmux-механики дороге не нужны,
// и ответ называет адрес ленты сразу, ждать родившейся сессии нечего.
func (s *server) appStart(w http.ResponseWriter, found *Project, id, blank, text, model, harness, effort string) {
	s.appStartRecord(w, found, id, blank, text, model, harness, effort, chatStore{})
}

func (s *server) appStartRecord(w http.ResponseWriter, found *Project, id, blank, text, model, harness, effort string, st chatStore) {
	h := s.chatHarnessOf(model, harness)
	if h == nil || !h.foreign() {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": fmt.Sprintf(
			"модель %s не определилась подпиской чужого протокола", chatModelName(model, harness))})
		return
	}
	sid := "as" + msgID()
	st.Model, st.Harness, st.Protocol = model, harness, h.Protocol
	st.Task, st.Project, st.Born, st.CxEffort = id, found.Name, s.now().Unix(), effort
	if st.CxEffort == "" {
		st.CxEffort = "medium"
	}
	if err := s.chatStoreWrite(sid, st); err != nil {
		s.logf("настройки разговора %s не записались: %v", sid, err)
		if st.GroomSource != "" {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "контекст груминга не записался: " + err.Error()})
			return
		}
	}
	// Незачатая запись выросла этим разговором: панель, стоящая на её адресе,
	// переедет на живой разговор по полю Grown, как у tmux-дороги.
	if blank != "" && chatKeyRe.MatchString(blank) {
		if bs := s.chatStoreRead(blank); bs.Blank {
			bs.Blank, bs.Grown, bs.Lifted = false, sid, s.now().Unix()
			if err := s.chatStoreWrite(blank, bs); err != nil {
				s.logf("незачатая запись %s не пришилась к разговору %s: %v", blank, sid, err)
			}
		}
	}
	s.appSayRecord(sid, text)
	dir := chatTree(found.Path, id)
	if st.GroomTree != "" {
		dir = st.GroomTree
	}
	c, err := s.appSpawn(sid, h, model, dir, found.Name, "")
	if err != nil {
		msg := err.Error()
		s.logf("разговор %s не поднялся дорогой app-server: %s", sid, msg)
		s.appFeedAppend(sid, []reply{{Role: "assistant", Fail: true, Text: "чат не поднялся: " + msg, Time: s.now().UTC().Format(time.RFC3339)}})
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "чат не поднялся: " + msg})
		return
	}
	c.setEffort(st.CxEffort)
	st.Thread = c.thread
	if err := s.chatStoreWrite(sid, st); err != nil {
		s.logf("настройки разговора %s не записались: %v", sid, err)
	}
	if err := s.appTurn(c, text, false); err != nil {
		msg := err.Error()
		s.logf("первая реплика разговора %s не ушла: %s", sid, msg)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "чат поднялся, но реплика не ушла: " + msg})
		return
	}
	s.chatSayDone(sid, "", "appserver")
	s.logf("чат поднят дорогой app-server в %s (модель %s, адрес %s)",
		found.Name, chatModelName(model, harness), sid)
	writeJSON(w, http.StatusOK, map[string]any{"way": "appserver", "session": sid,
		"model": model, "harness": harness,
		"message": "чат поднят дорогой app-server: ответ придёт в ленту этого разговора"})
}

// appStop останавливает ход: сперва уведомлением прерывания, а клиент,
// который не ответил на него за пару секунд, снимается процессом. Ход не
// остаётся вечным ни в одном исходе, и лента называет случившееся словами.
func (s *server) appStop(w http.ResponseWriter, sid string) {
	if a := s.appTaskFind(sid, "", ""); a != nil {
		if !s.recoveryStopped(w, a.root, a.task, true) {
			return
		}
		s.appTaskClose(a)
		writeJSON(w, http.StatusOK, map[string]string{"way": "stopped", "message": "конвейер задачи остановлен"})
		return
	}

	c := s.appLive(sid)
	if c == nil || !c.busyNow() {
		writeJSON(w, http.StatusOK, map[string]any{"way": "gone",
			"message": "ход не идёт: останавливать нечего"})
		return
	}
	err := c.proc.notify("turn/interrupt", map[string]any{"threadId": c.thread})
	for i := 0; i < 20 && err == nil; i++ {
		if !c.busyNow() {
			writeJSON(w, http.StatusOK, map[string]any{"way": "stopped",
				"message": "ход остановлен: реплика клиента оборвана"})
			return
		}
		time.Sleep(150 * time.Millisecond)
	}
	c.proc.kill()
	s.appMu.Lock()
	if s.appChats[sid] == c {
		delete(s.appChats, sid)
	}
	s.appMu.Unlock()
	c.mu.Lock()
	c.busy = false
	c.mu.Unlock()
	s.appFeedAppend(sid, []reply{{Role: "assistant", Fail: true,
		Text: "ход остановлен снятием процесса: клиент не принял прерывание",
		Time: s.now().UTC().Format(time.RFC3339)}})
	writeJSON(w, http.StatusOK, map[string]any{"way": "killed",
		"message": "клиент не принял прерывание, процесс снят: разговор продолжится новой репликой"})
}

// appDrop снимает процесс разговора целиком: под смену модели, уборку в архив
// и перезапуск. Лента остаётся, нить клиента продолжится следующим подъёмом.
func (s *server) appDrop(sid, why string) {
	s.appMu.Lock()
	for s.appStarting[sid] != nil {
		started := s.appStarting[sid]
		s.appMu.Unlock()
		<-started
		s.appMu.Lock()
	}
	c := s.appChats[sid]
	delete(s.appChats, sid)
	s.appMu.Unlock()
	if c == nil {
		return
	}
	c.proc.kill()
	s.logf("процесс app-server разговора %s снят: %s", sid, why)
}

// appStatus это состояние разговора для опроса панели: жив ли процесс, идёт
// ли ход и сколько он длится. Возраст хода говорит сервер, не браузер.
func (s *server) appStatus(w http.ResponseWriter, sid string) {
	c := s.appLive(sid)
	if c == nil {
		writeJSON(w, http.StatusOK, map[string]any{"session": sid, "live": false, "busy": false})
		return
	}
	busy := c.busyNow()
	out := map[string]any{"session": sid, "live": true, "busy": busy,
		"where": "app-server " + c.h.Name}
	if busy {
		out["status"] = "ход идёт"
		c.mu.Lock()
		at := c.turnAt
		last := at
		if c.taskTurn != nil {
			last = c.taskTurn.last
		}
		c.mu.Unlock()
		if !at.IsZero() {
			age := s.now().Sub(at)
			out["since"] = at.UnixMilli()
			out["sec"] = int(age / time.Second)
			if s.now().Sub(last) > appBusyCap {
				out["status"] = "ход идёт подозрительно долго"
			}
		}
	} else {
		out["status"] = "ждёт реплики"
		out["idle"] = true
	}
	writeJSON(w, http.StatusOK, out)
}

// appListRows собирает строки списка у дороги app-server: лента разговора это
// файл, и приходят они из своего каталога, а не из обхода транскриптов. Пустое
// имя проекта значит общий список машины.
func (s *server) appListRows(proj string) []chatEntry {
	ents, err := os.ReadDir(appFeedDir(s.cfg.Home))
	if err != nil {
		return nil
	}
	out := []chatEntry{}
	for _, de := range ents {
		name := de.Name()
		if de.IsDir() || !strings.HasSuffix(name, ".jsonl") {
			continue
		}
		sid := strings.TrimSuffix(name, ".jsonl")
		st := s.chatStoreRead(sid)
		if st.Protocol == "" || st.Hidden {
			continue
		}
		if proj != "" && st.Project != proj {
			continue
		}
		fi, err := de.Info()
		if err != nil {
			continue
		}
		e := chatEntry{ID: sid, Model: st.Model, PickHarness: st.Harness,
			Archived: st.Archived}
		e.State = chatDead
		if s.appLive(sid) != nil {
			e.State = chatLive
		}
		e.Harness = st.Harness
		if proj == "" {
			e.Project = st.Project
		}
		if st.Task != "" {
			e.Tasks = []string{st.Task}
		}
		e.Mtime = fi.ModTime().UTC().Format(time.RFC3339)
		e.Protocol = st.Protocol
		items := s.appFeedRead(sid)
		e.First = appFirst(items)
		e.Title = e.First
		if st.GroomSource != "" {
			e.Title = st.GroomSource + " груминг"
		}
		out = append(out, e)
	}
	return out
}

// appFeedServe отвечает лентой разговора чужого протокола той же формой, что
// лента транскрипта: шапка, окно записей и постраничная история по ключу. Лента
// это файл, целиком собранный чтением, и «раньше» режется по его номерам.
func (s *server) appFeedServe(w http.ResponseWriter, r *http.Request, sid string) {
	st := s.chatStoreRead(sid)
	file := s.appFeedFile(sid)
	fi, err := os.Stat(file)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{
			"error": fmt.Sprintf("ленты разговора %s нет: она появится первой репликой", sid)})
		return
	}
	if r.URL.Query().Get("stream") == "1" {
		s.appFeedStream(w, r, file)
		return
	}
	items := s.appFeedRead(sid)
	head := sessionInfo{ID: sid, Mtime: fi.ModTime().UTC().Format(time.RFC3339),
		Task: st.Task, Reply: "session", Harness: st.Harness, Protocol: st.Protocol,
		Live: s.appLive(sid) != nil}
	head.First = appFirst(items)
	n := intParam(r, "n", repliesDefault, repliesMax)
	total := len(items)
	kept := beforeCut(items, r.URL.Query().Get("before"))
	start := len(kept) > 0 && len(kept) <= n
	if len(kept) > n {
		kept = kept[len(kept)-n:]
	}
	resp := map[string]any{"session": sid, "head": head, "total": total,
		"items": kept, "start": start}
	if total == 0 {
		resp["note"] = "лента разговора пуста: ответ клиента появится здесь ходом"
	}
	s.chatSeenMark(sid)
	writeJSON(w, http.StatusOK, resp)
}

// appFeedStream это лента живым потоком: хвост файла уезжает событиями по
// мере записи, как у транскриптной ленты. Ключ записи это её номер в файле, и
// счётчик продолжает нумерацию чтения без разрыва.
func (s *server) appFeedStream(w http.ResponseWriter, r *http.Request, file string) {
	f, ok := sseOpen(w)
	if !ok {
		return
	}
	// Записи и смещение хвоста берутся из одного снимка файла: смещение из
	// раннего os.Stat опаздывало за записью, дописанной между Stat и открытием
	// потока, и она уезжала в панель дважды под двумя ключами.
	data, _ := os.ReadFile(file)
	items := appFeedParse(data)
	off := int64(len(data))
	if len(items) > repliesDefault {
		items = items[len(items)-repliesDefault:]
	}
	for _, it := range items {
		sseEvent(w, f, "", marshalReply(it))
	}
	if len(items) == 0 {
		sseEvent(w, f, "note", "лента разговора пуста: ответ клиента появится здесь ходом")
	}
	// Номер следующей записи файла: ключи потока продолжают нумерацию чтения,
	// чтобы панель, сменившая поток окном, не увидела одну запись под двумя
	// ключами.
	line := 0
	if len(data) > 0 {
		line = strings.Count(string(data), "\n")
		if !strings.HasSuffix(string(data), "\n") {
			line++
		}
	}
	t := time.NewTicker(tailPoll)
	defer t.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-t.C:
			lines, end := newLines(file, off)
			off = end
			for _, ln := range lines {
				var it reply
				if json.Unmarshal([]byte(ln), &it) != nil {
					line++
					continue
				}
				it.Seq, it.Key = line, fmt.Sprintf("app:%d", line)
				line++
				if appFeedDisplay(&it) {
					sseEvent(w, f, "", marshalReply(it))
				}
			}
		}
	}
}

func (s *server) appCarrierTurn(p *Project, sid, text string) error {
	st := s.chatStoreRead(sid)
	h := s.chatHarnessOf(st.Model, st.Harness)
	if h == nil || !h.foreign() {
		return fmt.Errorf("подписка разговора %s не определилась", sid)
	}
	c := s.appLive(sid)
	if c == nil {
		dir := chatTree(p.Path, st.Task)
		if st.GroomTree != "" {
			dir = st.GroomTree
		}
		var err error
		c, err = s.appSpawn(sid, h, st.Model, dir, p.Name, st.Thread)
		if err != nil {
			return err
		}
		st.Thread = c.thread
		if err := s.chatStoreWrite(sid, st); err != nil {
			return err
		}
	}
	c.setEffort(st.CxEffort)
	err := s.appTurn(c, text, true)
	if errors.Is(err, errAppTurnBusy) {
		return fmt.Errorf("ход уже идёт: ответ согласования пока не доставлен")
	}
	return err
}
