package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"syscall"
	"time"

	"github.com/dronrider/devkit/internal/chat"
)

const (
	runReceiptAge       = 7 * 24 * time.Hour
	runReceiptClockSkew = 5 * time.Minute
	runReceiptDate      = "2006-01-02"
)

var runReceiptKey = regexp.MustCompile(`^m-([0-9a-z]{1,11})-[0-9]{1,16}-[0-9a-z]{1,4}$`)

type runStartPhase string

const (
	runStartBeforeDispatch runStartPhase = "before_dispatch"
	runStartDispatching    runStartPhase = "dispatching"
)

type runStartReceipt struct {
	Fingerprint string          `json:"fingerprint"`
	Phase       runStartPhase   `json:"phase,omitempty"`
	Status      int             `json:"status,omitempty"`
	Reply       json.RawMessage `json:"reply,omitempty"`
}

// runStartResponse буферизует ответ до сохранения квитанции. Первый
// маркер пишется до снятия ручного стопа. Перед вызовом носителя его
// сменяет маркер, при котором исход без сохранённого ответа неизвестен.
type runStartResponse struct {
	http.ResponseWriter
	receiptPath string
	fingerprint string
	logFailure  func(string, error)
	dispatched  bool
	status      int
	reply       bytes.Buffer
}

func (w *runStartResponse) WriteHeader(status int) { w.status = status }

func (w *runStartResponse) Write(p []byte) (int, error) { return w.reply.Write(p) }

func markRunStart(w http.ResponseWriter, phase runStartPhase) bool {
	out, ok := w.(*runStartResponse)
	if !ok || out.dispatched {
		return true
	}
	if err := writeChatStartReceipt(out.receiptPath, runStartReceipt{Fingerprint: out.fingerprint, Phase: phase}); err != nil {
		out.logFailure("запись маркера "+string(phase), err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "не удалось сохранить квитанцию перед запуском"})
		return false
	}
	out.dispatched = phase == runStartDispatching
	return true
}

func writeRunStartUnknown(w http.ResponseWriter, detail string) {
	message := "Исход запуска из чата не подтверждён, повтор не отправлен"
	if detail != "" {
		message += ". " + detail
	}
	writeJSON(w, http.StatusBadGateway, map[string]any{"unknown": true, "error": message})
}

func writeRunStartExpired(w http.ResponseWriter) {
	writeJSON(w, http.StatusGone, map[string]any{"expired": true,
		"error": "Срок квитанции запуска истёк, проверьте состояние задачи"})
}

func parseRunReceiptTime(key string) (time.Time, bool) {
	parts := runReceiptKey.FindStringSubmatch(key)
	if parts == nil {
		return time.Time{}, false
	}
	ms, err := strconv.ParseInt(parts[1], 36, 64)
	return time.UnixMilli(ms).UTC(), err == nil
}

func runReceiptDir(home string) string {
	return filepath.Join(chatStoreDir(home), ".run-starts")
}

func runReceiptPath(dir, key string, stamp time.Time) string {
	return filepath.Join(dir, stamp.Format(runReceiptDate), fmt.Sprintf("%x", sha256.Sum256([]byte(key))))
}

func syncRunReceiptParents(dayDir, home string) error {
	home = filepath.Clean(home)
	for dir := filepath.Dir(dayDir); ; dir = filepath.Dir(dir) {
		if err := syncChatDir(dir); err != nil {
			return err
		}
		if dir == home {
			return nil
		}
	}
}

func lockRunReceipts(dir string, mode int) (*os.File, error) {
	f, err := os.OpenFile(filepath.Join(dir, ".lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), mode|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

func runReceiptCutoff(dir string) (time.Time, error) {
	data, err := os.ReadFile(filepath.Join(dir, ".pruned-before"))
	if errors.Is(err, os.ErrNotExist) {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, err
	}
	var day string
	if err := json.Unmarshal(data, &day); err != nil {
		return time.Time{}, err
	}
	return time.Parse(runReceiptDate, day)
}

func pruneRunReceipts(dir string, now time.Time) error {
	if _, err := os.Stat(dir); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	lock, err := lockRunReceipts(dir, syscall.LOCK_EX)
	if errors.Is(err, syscall.EWOULDBLOCK) {
		return nil
	}
	if err != nil {
		return err
	}
	defer lock.Close()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	saved, err := runReceiptCutoff(dir)
	if err != nil {
		return err
	}
	cutoff := now.Add(-runReceiptAge).UTC().Truncate(24 * time.Hour)
	if saved.After(cutoff) {
		cutoff = saved
	}
	for _, entry := range entries {
		day, err := time.Parse(runReceiptDate, entry.Name())
		if !entry.IsDir() || err != nil || day.Format(runReceiptDate) != entry.Name() || !day.Before(cutoff) {
			continue
		}
		// Сначала сохраняем границу уборки. При откате часов ключ удалённой
		// квитанции всё равно не сможет запустить работу.
		if cutoff.After(saved) {
			if err := writeChatStartReceipt(filepath.Join(dir, ".pruned-before"), cutoff.Format(runReceiptDate)); err != nil {
				return err
			}
			saved = cutoff
		}
		if err := pruneRunReceiptDay(filepath.Join(dir, entry.Name())); err != nil {
			return err
		}
	}
	return nil
}

func pruneRunReceiptDay(dir string) error {
	lock, err := lockRunReceipts(dir, syscall.LOCK_EX)
	if errors.Is(err, syscall.EWOULDBLOCK) {
		return nil
	}
	if err != nil {
		return err
	}
	defer lock.Close()
	return os.RemoveAll(dir)
}

func (s *server) runStartReceiptKeeper(stop <-chan struct{}) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		if err := pruneRunReceipts(runReceiptDir(s.cfg.Home), s.now()); err != nil {
			s.logf("уборка квитанций запуска: %v", err)
		}
		select {
		case <-stop:
			return
		case <-ticker.C:
		}
	}
}

func replyRunStartReceipt(w http.ResponseWriter, path, fingerprint string, logFailure func(string, error)) bool {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return false
	}
	var receipt runStartReceipt
	if err == nil {
		err = json.Unmarshal(data, &receipt)
		if err == nil && receipt.Fingerprint == "" {
			err = errors.New("нет контрольного отпечатка")
		}
	}
	if err != nil {
		logFailure("чтение квитанции", err)
		writeRunStartUnknown(w, "Квитанция не прочиталась")
		return true
	}
	if receipt.Fingerprint != fingerprint {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "ключ запуска уже относится к другому запросу"})
		return true
	}
	if receipt.Phase == runStartBeforeDispatch && receipt.Status == 0 && len(receipt.Reply) == 0 {
		return false
	}
	if receipt.Status < http.StatusOK || receipt.Status > 599 || !json.Valid(receipt.Reply) {
		writeRunStartUnknown(w, "")
		return true
	}
	writeJSON(w, receipt.Status, receipt.Reply)
	return true
}

func (s *server) runStartWithReceipt(w http.ResponseWriter, p *Project, body runStartRequest) {
	stamp, ok := parseRunReceiptTime(body.MsgID)
	now := s.now()
	if !ok || stamp.After(now.Add(runReceiptClockSkew)) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "недопустимый msg_id запуска, проверьте часы устройства"})
		return
	}
	if !stamp.After(now.Add(-runReceiptAge)) {
		writeRunStartExpired(w)
		return
	}
	logFailure := func(operation string, err error) {
		s.logf("квитанция запуска %q в %s: %s: %v", body.ID, p.Name, operation, err)
	}
	dir := runReceiptDir(s.cfg.Home)
	validCutoff := func() bool {
		cutoff, err := runReceiptCutoff(dir)
		if err != nil {
			logFailure("граница уборки", err)
			writeRunStartUnknown(w, "Хранилище квитанций запуска недоступно")
			return false
		}
		if stamp.Before(cutoff) {
			writeRunStartExpired(w)
			return false
		}
		return true
	}
	if !validCutoff() {
		return
	}
	path := runReceiptPath(dir, body.MsgID, stamp)
	if !body.ReceiptOnly {
		dayDir := filepath.Dir(path)
		if err := os.MkdirAll(dayDir, 0700); err != nil {
			logFailure("каталог", err)
			writeRunStartUnknown(w, "Хранилище квитанций запуска недоступно")
			return
		}
		guard, err := lockRunReceipts(dayDir, syscall.LOCK_SH)
		if err != nil {
			if !errors.Is(err, syscall.EWOULDBLOCK) {
				logFailure("замок хранилища", err)
			}
			writeRunStartUnknown(w, "Хранилище квитанций запуска занято или недоступно")
			return
		}
		defer guard.Close()
		if !validCutoff() {
			return
		}
		// Запись маркера синхронизирует день, а здесь сохраняются его
		// ссылки в родительских каталогах при их первом создании.
		if err := syncRunReceiptParents(dayDir, s.cfg.Home); err != nil {
			logFailure("запись каталогов", err)
			writeRunStartUnknown(w, "Хранилище квитанций запуска недоступно")
			return
		}
	}
	fields, _ := json.Marshal([]string{p.Name, p.Path, body.ID, body.Intent, body.Text, body.Harness, body.Tier, body.Effort})
	fingerprint := fmt.Sprintf("%x", sha256.Sum256(fields))
	if body.ReceiptOnly {
		if !replyRunStartReceipt(w, path, fingerprint, logFailure) {
			writeJSON(w, http.StatusNotFound, map[string]any{"missing": true, "error": "квитанция запуска ещё не записана"})
		}
		return
	}
	lock, err := chat.Lock(path + ".lock")
	if err != nil {
		if !errors.Is(err, chat.ErrLocked) {
			logFailure("замок квитанции", err)
		}
		if !replyRunStartReceipt(w, path, fingerprint, logFailure) {
			writeRunStartUnknown(w, "Квитанция запуска занята или недоступна")
		}
		return
	}
	defer lock.Close()
	if replyRunStartReceipt(w, path, fingerprint, logFailure) {
		return
	}
	out := &runStartResponse{ResponseWriter: w, receiptPath: path, fingerprint: fingerprint, logFailure: logFailure}
	s.runStart(out, p, body)
	if out.status == 0 || (out.dispatched && out.status >= http.StatusInternalServerError) {
		var problem struct{ Error string }
		_ = json.Unmarshal(out.reply.Bytes(), &problem)
		writeRunStartUnknown(w, problem.Error)
		return
	}
	reply := json.RawMessage(out.reply.Bytes())
	if out.status >= http.StatusInternalServerError {
		var problem map[string]json.RawMessage
		if err := json.Unmarshal(reply, &problem); err != nil || problem == nil {
			writeRunStartUnknown(w, "Ответ дашборда не прочитался")
			return
		}
		problem["run_receipt"] = json.RawMessage("true")
		reply, _ = json.Marshal(problem)
	}
	if err := writeChatStartReceipt(path, runStartReceipt{Fingerprint: fingerprint, Status: out.status, Reply: reply}); err != nil {
		logFailure("запись окончательной квитанции", err)
		if out.dispatched {
			writeRunStartUnknown(w, "Подтверждение запуска не сохранилось")
			return
		}
	}
	writeJSON(w, out.status, reply)
}
