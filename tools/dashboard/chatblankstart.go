package main

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"github.com/dronrider/devkit/internal/chat"
	"github.com/dronrider/devkit/internal/sessions"
)

type chatStartReceipt struct {
	Key      string `json:"key"`
	Project  string `json:"project"`
	Task     string `json:"task"`
	Tmux     string `json:"tmux"`
	Model    string `json:"model"`
	Harness  string `json:"harness"`
	Created  int64  `json:"created"`
	Since    int64  `json:"since"`
	Accepted bool   `json:"accepted"`
	Unknown  string `json:"unknown,omitempty"`
}

func chatStartKey(id, text, model, harness, effort string) string {
	data, _ := json.Marshal([]string{id, text, model, harness, effort})
	return fmt.Sprintf("%x", sha256.Sum256(data))
}

func (s *server) chatStartReceiptPath(blank string) string {
	return filepath.Join(chatStoreDir(s.cfg.Home), ".start-"+blank)
}

// Квитанция живёт отдельно от заготовки: поздний черновик и уборка списка
// не должны стереть уже начатый внешний запуск. Файловый замок общий для
// /chats и /say и действует после перезапуска сервера.
func (s *server) chatBlankStart(w http.ResponseWriter, found *Project, blank, id, text, model, harness, effort, claim, way string) {
	if !chatModelCurrent(w, model) {
		return
	}
	fail := func(err error) { writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()}) }
	hold := func(err error) {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "unknown": true})
	}
	if !chatKeyRe.MatchString(blank) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "неверный адрес заготовки чата"})
		return
	}
	path := s.chatStartReceiptPath(blank)
	lock, err := chat.Lock(path + ".lock")
	if err != nil {
		fail(fmt.Errorf("квитанция запуска не заперлась: %w", err))
		return
	}
	defer lock.Close()
	key := chatStartKey(id, text, model, harness, effort)
	var prev chatStartReceipt
	data, err := os.ReadFile(path)
	if err == nil {
		if err := json.Unmarshal(data, &prev); err != nil {
			hold(fmt.Errorf("квитанция запуска не читается: %w", err))
			return
		}
		if id == "" {
			id = prev.Task
			key = chatStartKey(id, text, model, harness, effort)
		}
		if way == "start" {
			id, model, harness = prev.Task, prev.Model, prev.Harness
			key = chatStartKey(id, text, model, harness, effort)
		}
		if prev.Project != found.Name || prev.Key != key {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "чат уже начат: откройте его разговор для следующей реплики", "tmux": prev.Tmux, "session": s.chatStoreRead(blank).Grown})
			return
		}
		if !prev.Accepted {
			hold(fmt.Errorf("исход первого запуска не подтверждён, повтор не отправлен: %s", prev.Unknown))
			return
		}
		session := s.chatStoreRead(blank).Grown
		if session == "" {
			memory := s.chatStoreRead("tmux-" + prev.Tmux)
			roll, err := tmuxRollAsk()
			if err != nil || memory.From != blank || memory.Since < prev.Since || !roll.alive(prev.Tmux) || prev.Created == 0 || roll[prev.Tmux] != prev.Created {
				hold(errors.New("принадлежность первого окна не подтверждена; повтор не отправлен"))
				return
			}
			session = sessions.TmuxOwnerSince(s.bindsAll(), prev.Tmux, bindTimeSince(prev.Since))
		}

		s.chatSayDone(blank, claim, way)
		writeJSON(w, http.StatusOK, map[string]string{"way": way, "tmux": prev.Tmux, "session": session, "model": prev.Model, "harness": prev.Harness})
		return
	}

	if !os.IsNotExist(err) {
		hold(fmt.Errorf("квитанция запуска не прочиталась: %w", err))
		return
	}
	st := s.chatStoreRead(blank)
	if h := s.chatHarnessOf(model, harness); h != nil && h.foreign() {
		if way == "new" {
			s.appStart(w, found, id, blank, text, model, harness, effort)
		} else {
			if st.Protocol == "" {
				st.Protocol, st.Blank, st.Lifted = h.Protocol, false, s.now().Unix()
				if err := s.chatStoreWrite(blank, st); err != nil {
					fail(err)
					return
				}
			}
			s.appSay(w, found, blank, text, claim, effort)
		}
		return
	}
	if !st.Blank || st.Project != found.Name || st.Tmux != "" || st.Grown != "" {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "заготовка уже начата или недоступна: откройте её разговор", "tmux": st.Tmux, "session": st.Grown})
		return
	}
	for _, why := range []string{tmuxMissingCheck(), s.chatClientMissing(model, harness), s.chatHooksGap(model, harness)} {
		if why != "" {
			fail(errors.New(why))
			return
		}
	}
	if err := s.preserveGroomContext(blank, id); err != nil {
		fail(err)
		return
	}
	sess := chatNewName(id, tmuxAliveFn())
	receipt := chatStartReceipt{Key: key, Project: found.Name, Task: id, Tmux: sess, Model: model, Harness: harness, Since: s.now().Unix()}
	if err := writeChatStartReceipt(path, receipt); err != nil {
		fail(fmt.Errorf("квитанция запуска не сохранилась: %w", err))
		return
	}
	if err := s.chatStoreWrite("tmux-"+sess, chatStore{Model: model, Harness: harness, From: blank}); err != nil {
		os.Remove(path)
		fail(fmt.Errorf("настройки запуска не сохранились: %w", err))
		return
	}
	if err := startManaged(found.Path, chatTree(found.Path, id), id, sess, func(ack string) string {
		return chatCmd(s.launchEnv(id, sess, blank)+effortPair(effort)+"DEVKIT_CLOUD_ACK="+shQuote(ack)+" ", model, "", text, "", s.chatHarnessOf(model, harness), binPath(agentctlBin))
	}); err != nil {
		if errors.Is(err, errChatStartUnknown) {
			receipt.Unknown = err.Error()
			// Даже при отказе этой записи исходная квитанция остаётся: повтор не
			// создаст второй процесс поверх неизвестного первого.
			if writeErr := writeChatStartReceipt(path, receipt); writeErr != nil {
				s.logf("неопределённый запуск %s: %v", blank, writeErr)
			}
		} else if removeErr := os.Remove(path); removeErr != nil {
			s.logf("квитанция отказа %s не снялась: %v", blank, removeErr)
		}
		if errors.Is(err, errChatStartUnknown) {
			hold(err)
		} else {
			fail(fmt.Errorf("tmux не подтвердил запуск чата %s: %w", blank, err))
		}
		return
	}
	if roll, err := tmuxRollAsk(); err == nil {
		receipt.Created = roll[sess]
	}
	receipt.Accepted = true
	if err := writeChatStartReceipt(path, receipt); err != nil {
		hold(fmt.Errorf("клиент запущен, но подтверждение не сохранилось; повтор не отправлен: %w", err))
		return
	}
	s.chatBlankLift(blank, sess, model, harness)
	s.chatRaised(sess, blank, id, found.Name)
	s.chatSayDone(blank, claim, way)
	if claim != "" {
		s.saidSay(saidSessionKey(blank), text, way)
	}
	s.logf("чат %s поднят в %s (окно %s, модель %s)", blank, found.Name, sess, chatModelName(model, harness))
	writeJSON(w, http.StatusOK, map[string]string{"way": way, "tmux": sess, "model": model, "harness": harness, "tree": chatTree(found.Path, id)})
}

func writeChatStartReceipt(path string, receipt chatStartReceipt) error {
	data, err := json.Marshal(receipt)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".chat-start-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err := os.Rename(f.Name(), path); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
