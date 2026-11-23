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

	"github.com/dronrider/devkit/internal/chat"
)

type runStartReceipt struct {
	Fingerprint string          `json:"fingerprint"`
	Status      int             `json:"status,omitempty"`
	Reply       json.RawMessage `json:"reply,omitempty"`
}

// runStartResponse буферизует ответ до сохранения квитанции. Status
// появляется на диске после подтверждения исхода. Маркер пишется до снятия
// ручного стопа и других изменений. При обрыве между этими записями повтор
// увидит неизвестный исход.
type runStartResponse struct {
	http.ResponseWriter
	receiptPath string
	fingerprint string
	dispatched  bool
	status      int
	reply       bytes.Buffer
}

func (w *runStartResponse) WriteHeader(status int) { w.status = status }

func (w *runStartResponse) Write(p []byte) (int, error) { return w.reply.Write(p) }

func markRunStart(w http.ResponseWriter) bool {
	out, ok := w.(*runStartResponse)
	if !ok {
		return true
	}
	if err := writeChatStartReceipt(out.receiptPath, runStartReceipt{Fingerprint: out.fingerprint}); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "не удалось сохранить квитанцию перед запуском: " + err.Error()})
		return false
	}
	return true
}

func dispatchRunStart(w http.ResponseWriter) {
	if out, ok := w.(*runStartResponse); ok {
		out.dispatched = true
	}
}

func writeRunStartUnknown(w http.ResponseWriter, detail string) {
	message := "Исход запуска из чата не подтверждён, повтор не отправлен"
	if detail != "" {
		message += ". " + detail
	}
	writeJSON(w, http.StatusBadGateway, map[string]any{"unknown": true, "error": message})
}

func replyRunStartReceipt(w http.ResponseWriter, path, fingerprint string) bool {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return false
	}
	var receipt runStartReceipt
	if err != nil || json.Unmarshal(data, &receipt) != nil || receipt.Fingerprint == "" {
		writeRunStartUnknown(w, "Квитанция не прочиталась")
		return true
	}
	if receipt.Fingerprint != fingerprint {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "ключ запуска уже относится к другому запросу"})
		return true
	}
	if receipt.Status < http.StatusOK || receipt.Status > 599 || !json.Valid(receipt.Reply) {
		writeRunStartUnknown(w, "")
		return true
	}
	writeJSON(w, receipt.Status, receipt.Reply)
	return true
}

func (s *server) runStartWithReceipt(w http.ResponseWriter, p *Project, body runStartRequest) {
	path := filepath.Join(chatStoreDir(s.cfg.Home), fmt.Sprintf(".run-start-%x", sha256.Sum256([]byte(body.MsgID))))
	fields, _ := json.Marshal([]string{p.Name, p.Path, body.ID, body.Intent, body.Text, body.Harness, body.Tier, body.Effort})
	fingerprint := fmt.Sprintf("%x", sha256.Sum256(fields))
	if body.ReceiptOnly {
		if !replyRunStartReceipt(w, path, fingerprint) {
			writeJSON(w, http.StatusNotFound, map[string]any{"missing": true, "error": "квитанция запуска ещё не записана"})
		}
		return
	}
	lock, err := chat.Lock(path + ".lock")
	if err != nil {
		if !replyRunStartReceipt(w, path, fingerprint) {
			writeRunStartUnknown(w, "Квитанция запуска занята или недоступна")
		}
		return
	}
	defer lock.Close()
	if replyRunStartReceipt(w, path, fingerprint) {
		return
	}
	out := &runStartResponse{ResponseWriter: w, receiptPath: path, fingerprint: fingerprint}
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
		s.logf("квитанция запуска %s в %s не сохранилась: %v", body.ID, p.Name, err)
		if out.dispatched {
			writeRunStartUnknown(w, "Подтверждение запуска не сохранилось")
			return
		}
	}
	writeJSON(w, out.status, reply)
}
