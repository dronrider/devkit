package main

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"
)

const appSharedSocketEnv = "DEVKIT_DASH_CODEX_SOCKET"

var errAppSharedUnknown = errors.New("общий сервер Codex не подтвердил доставку. Проверьте ленту перед повторной отправкой")

func appSharedHarness(h *Harness) bool {
	if h == nil || h.Protocol != "codex" {
		return false
	}
	for _, name := range h.Env {
		if name != "CODEX_HOME" || h.Home == "" {
			return false
		}
	}
	return true
}

func appSharedHome(h *Harness) string {
	if h.Home != "" {
		return h.Home
	}
	return filepath.Join(realHomeFn(), ".codex")
}

func appNewSharedSocket(h *Harness, st chatStore) (string, error) {
	if !appSharedHarness(h) || st.GroomSource != "" || st.TaskRun || st.TaskRunPID != 0 {
		return "", nil
	}
	socket := os.Getenv(appSharedSocketEnv)
	if socket == "" {
		// Иначе выбор общего сервера мог бы выдать профилю чужой аккаунт.
		if h.Home == "" && !h.Default {
			return "", nil
		}
		bin := filepath.Base(h.Bin)
		if bin != h.Protocol && bin != h.Protocol+"-cli" {
			return "", nil
		}
		if home := os.Getenv("CODEX_HOME"); home != "" && !appSamePath(home, appSharedHome(h)) {
			return "", nil
		}
		if !appSamePath(appSharedHome(h), filepath.Join(realHomeFn(), ".codex")) {
			return "", nil
		}
		socket = filepath.Join(appSharedHome(h), "app-server-control", "app-server-control.sock")
	}
	if !filepath.IsAbs(socket) {
		return "", fmt.Errorf("%s требует абсолютный путь к Unix-сокету Codex", appSharedSocketEnv)
	}
	return socket, nil
}

func appSharedStartID(project, claim string) string {
	key := sha256.Sum256([]byte(project + "\x00" + claim))
	return fmt.Sprintf("as%x", key)
}

func appSamePath(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	if p, err := filepath.EvalSymlinks(a); err == nil {
		a = p
	}
	if p, err := filepath.EvalSymlinks(b); err == nil {
		b = p
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

func appSharedHomeMatches(hello json.RawMessage, h *Harness) bool {
	var config struct {
		Home string `json:"codexHome"`
	}
	return json.Unmarshal(hello, &config) == nil && appSamePath(config.Home, appSharedHome(h))
}

type appSharedNotification struct {
	method string
	params json.RawMessage
}

type appSharedNotifications struct {
	mu       sync.Mutex
	thread   string
	receive  func(string, json.RawMessage)
	pending  []appSharedNotification
	bytes    int
	overflow bool
}

func (b *appSharedNotifications) add(method string, params json.RawMessage) {
	switch method {
	case "item/started", "item/completed", "turn/started", "turn/completed":
	default:
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.receive != nil {
		b.deliver(method, params)
		return
	}
	b.bytes += len(params)
	if b.bytes > 16<<20 || len(b.pending) >= 4096 {
		b.overflow = true
		return
	}
	if !b.overflow {
		b.pending = append(b.pending, appSharedNotification{method, params})
	}
}

// Пока читается история, пришедшие по сокету события ждут в памяти. Так
// ответ из истории не уйдёт в ленту раньше вопроса из того же хода.
func (b *appSharedNotifications) flush(thread string, receive func(string, json.RawMessage)) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.overflow {
		return errors.New("событий общего разговора накопилось слишком много. Откройте разговор ещё раз")
	}
	b.thread, b.receive = thread, receive
	for _, n := range b.pending {
		b.deliver(n.method, n.params)
	}
	b.pending = nil
	return nil
}

func (b *appSharedNotifications) deliver(method string, params json.RawMessage) {
	var note struct {
		ThreadID string `json:"threadId"`
	}
	if json.Unmarshal(params, &note) == nil && note.ThreadID == b.thread {
		b.receive(method, params)
	}
}

func (s *server) appSharedReconnect(p *Project, sid string, st chatStore) error {
	s.appMu.Lock()
	send := s.appSayLocks[sid]
	s.appMu.Unlock()
	if send != nil {
		if !send.TryLock() {
			return nil
		}
		defer send.Unlock()
	}
	if s.appLive(sid) != nil {
		return nil
	}
	h := s.chatHarnessOf(st.Model, st.Harness)
	if !appSharedHarness(h) {
		return errors.New("подписка общего чата Codex недоступна")
	}
	_, err := s.appSpawn(sid, h, st.Model, chatTree(p.Path, st.Task), p.Name, st.Thread)
	if errors.Is(err, errAppStarting) {
		return nil
	}
	return err
}

func (s *server) appSharedReceipt(c *appChat, claim string) string {
	key := sha256.Sum256([]byte(c.sid + "\x00" + claim))
	return filepath.Join(chatStoreDir(s.cfg.Home), "appserver", "receipts", fmt.Sprintf("%x", key))
}

func (s *server) appSharedPending(c *appChat, claim string) bool {
	_, err := os.Stat(s.appSharedReceipt(c, claim))
	return !os.IsNotExist(err)
}

func (s *server) appSharedDelivered(c *appChat, claim string) bool {
	file := s.appSharedReceipt(c, claim)
	data, err := os.ReadFile(file)
	if err != nil {
		return false
	}
	if string(data) == "accepted\n" {
		return true
	}
	for _, r := range s.appFeedRead(c.sid) {
		if r.Role == "user" && r.App != nil && r.App.ThreadID == c.thread && r.App.Item["clientId"] == claim {
			return true
		}
	}
	return false
}

func (s *server) appSharedHold(c *appChat, claim string) error {
	path := s.appSharedReceipt(c, claim)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if os.IsExist(err) {
		return errAppSharedUnknown
	}
	if err != nil {
		return err
	}
	return f.Close()
}

func (s *server) appSharedRelease(c *appChat, claim string) {
	if claim != "" {
		os.Remove(s.appSharedReceipt(c, claim))
	}
}

func (s *server) appSharedTurnError(w http.ResponseWriter, err error) bool {
	if errors.Is(err, errAppSharedUnknown) {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "unknown": true})
		return true
	}
	if errors.Is(err, errAppTurnBusy) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error(), "refused": err.Error()})
		return true
	}
	var refused *appError
	if errors.As(err, &refused) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error(), "refused": err.Error()})
		return true
	}
	return false
}

// appSharedMigrate сохраняет адрес общего сервера до следующего хода.
// appMu и c.mu резервируют прежнего клиента, пока обновляется запись разговора.
func (s *server) appSharedMigrate(sid string, h *Harness, st *chatStore) error {
	socket, err := appNewSharedSocket(h, *st)
	if err != nil || socket == "" {
		return err
	}
	if node, err := os.Stat(socket); err != nil || node.Mode()&os.ModeSocket == 0 {
		return nil
	}
	s.appMu.Lock()
	if s.appStarting[sid] != nil {
		s.appMu.Unlock()
		return errAppStarting
	}
	c := s.appChats[sid]
	if c != nil {
		c.mu.Lock()
		if c.busy {
			c.mu.Unlock()
			s.appMu.Unlock()
			return errAppTurnBusy
		}
	}
	st.SharedSocket = socket
	if err = s.chatStoreWrite(sid, *st); err != nil {
		st.SharedSocket = ""
		if c != nil {
			c.mu.Unlock()
		}
		s.appMu.Unlock()
		return fmt.Errorf("привязка общего чата не сохранилась: %w", err)
	}
	if c != nil {
		// Обладатель прежней ссылки на c увидит занятого клиента.
		c.busy = true
		delete(s.appChats, sid)
		c.mu.Unlock()
	}
	s.appMu.Unlock()
	if c != nil {
		c.proc.kill()
	}
	return nil
}
