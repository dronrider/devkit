package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/dronrider/devkit/internal/draftproposal"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"

	"github.com/dronrider/devkit/internal/chat"
	"github.com/dronrider/devkit/internal/draftmeta"
	"github.com/dronrider/devkit/internal/sessions"
	"github.com/dronrider/devkit/internal/taskform"
)

type selectionDeliveryContext struct {
	Delivered bool           `json:"delivered,omitempty"`
	Transport string         `json:"transport,omitempty"`
	Reply     string         `json:"reply"`
	TextKey   string         `json:"textKey"`
	Selection chat.Selection `json:"selection"`
	Name      string         `json:"name"`
	Session   string         `json:"session,omitempty"`
}

func selectionMatches(h handedAsk, answer *chat.Selection) bool {
	if h.Ask.Task != answer.Task || len(answer.Questions) == 0 || len(answer.Questions) > chat.PackLimit {
		return false
	}
	tree := h.Tree
	if h.Ask.Tree != "" {
		tree = h.Ask.Tree
	}
	for _, q := range h.Ask.Questions {
		if q.Scenario != nil && q.Scenario.Tree != "" {
			tree = q.Scenario.Tree
		}
	}
	if tree != answer.Tree {
		return false
	}
	seen := map[string]bool{}
	for i, a := range answer.Questions {
		if seen[a.Fork] || a.IDs == nil {
			return false
		}
		seen[a.Fork] = true
		found := false
		for _, q := range h.Ask.Questions {
			fork, snapshot := askForkName(q.Text), chat.QuestionSnapshot(q)
			if q.Scenario != nil {
				fork, snapshot = q.Scenario.Fork, q.Scenario.Snapshot
			}
			if fork != a.Fork || snapshot != a.Snapshot {
				continue
			}
			if q.Proposal != nil {
				return false
			}
			if q.Scenario == nil && (!q.Multi && len(a.IDs) != 1 || len(a.IDs) == 0) {
				return false
			}
			labels, ids := []string{}, map[string]bool{}
			for _, id := range a.IDs {
				if ids[id] {
					return false
				}
				ids[id] = true
				pos := -1
				for j := range q.Options {
					own := strconv.Itoa(j + 1)
					if q.Scenario != nil {
						if j >= len(q.Scenario.IDs) {
							return false
						}
						own = q.Scenario.IDs[j]
					}
					if own == id {
						pos = j
						break
					}
				}
				if pos < 0 {
					return false
				}
				labels = append(labels, q.Options[pos].Label)
			}
			answer.Questions[i].Labels = labels
			found = true
			break
		}
		if !found {
			return false
		}
	}
	return true
}

func selectionIdentity(a chat.Selection) chat.Selection {
	a.Questions = append([]chat.SelectedQuestion(nil), a.Questions...)
	for i := range a.Questions {
		a.Questions[i].Labels = nil
	}
	return a
}

func selectionContextMigrates(old, current selectionDeliveryContext) bool {
	if old.Delivered || current.Transport == "" || old.Transport != "" || old.Selection.Tree != current.Transport || old.Reply != current.Reply || old.TextKey != current.TextKey || old.Name != current.Name || old.Session != current.Session {
		return false
	}
	previous := old.Selection
	previous.Tree = current.Selection.Tree
	return reflect.DeepEqual(selectionIdentity(previous), selectionIdentity(current.Selection))
}

func selectionMigrationApplied(old selectionDeliveryContext) (bool, error) {
	taskRoot := filepath.Join(old.Selection.Tree, "docs", "tasks")
	paths := []string{filepath.Join(taskRoot, old.Selection.Task+".md"), filepath.Join(taskRoot, "drafts", old.Selection.Task+".md")}
	archive := filepath.Join(taskRoot, "archive")
	years, err := os.ReadDir(archive)
	if err != nil && !os.IsNotExist(err) {
		return false, err
	}
	for _, year := range years {
		if year.IsDir() {
			paths = append(paths, filepath.Join(archive, year.Name(), old.Selection.Task+".md"))
		}
	}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return false, err
		}
		for _, fork := range taskform.ParseForks(string(data)) {
			for _, sub := range fork.Sub {
				const prefix = "вложение ответа: "
				if !strings.HasPrefix(sub, prefix) {
					continue
				}
				var receipt struct {
					Reply string `json:"reply"`
				}
				if json.Unmarshal([]byte(strings.TrimPrefix(sub, prefix)), &receipt) == nil && receipt.Reply == old.Reply {
					return true, nil
				}
			}
		}
	}
	return false, nil
}

func (s *server) selectionTextKey(sid, claim, text string, answer chat.Selection) (string, error) {
	sum := sha256.Sum256([]byte(text))
	key := hex.EncodeToString(sum[:])
	for _, old := range s.chatStoreRead(sid).SelectionContexts {
		if old.Reply == claim && (!reflect.DeepEqual(selectionIdentity(old.Selection), selectionIdentity(answer)) || old.TextKey != key) {
			return "", fmt.Errorf("ключ сообщения уже использован для другого ответа")
		}
	}
	return key, nil
}

func (s *server) handleSelectionSay(w http.ResponseWriter, p *Project, sid, claim, text string, answer chat.Selection) {
	if claim == "" || !goalIDRe.MatchString(answer.Task) || !s.scenarioTree(answer.Tree, answer.Task) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "нужны дерево и задача показанного вопроса"})
		return
	}
	sum := sha256.Sum256([]byte(text))
	textKey := hex.EncodeToString(sum[:])

	context := selectionDeliveryContext{}
	matched := false
	for _, h := range s.handedAsks(sid, s.binds(), s.now()) {
		if selectionMatches(h, &answer) {
			context = selectionDeliveryContext{Reply: claim, TextKey: textKey, Selection: answer, Name: h.Name, Session: h.Ask.Session, Transport: h.Tree}
			matched = true
			break
		}
	}
	for _, old := range s.chatStoreRead(sid).SelectionContexts {
		if old.Reply != claim {
			continue
		}
		previous := old.Selection
		if matched && selectionContextMigrates(old, context) {
			applied, err := selectionMigrationApplied(old)
			if err != nil {
				writeJSON(w, http.StatusBadGateway, map[string]string{"error": "проверка прежнего ответа: " + err.Error()})
				return
			}
			if !applied {
				previous.Tree = answer.Tree
			}
		}
		if old.TextKey != textKey || !reflect.DeepEqual(selectionIdentity(previous), selectionIdentity(answer)) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "ключ сообщения уже использован для другого ответа"})
			return
		}
	}
	if !matched {
		for _, old := range s.chatStoreRead(sid).SelectionContexts {
			if old.Reply == claim && old.TextKey == textKey && reflect.DeepEqual(selectionIdentity(old.Selection), selectionIdentity(answer)) {
				context, answer = old, old.Selection
				break
			}
		}
	}
	for _, old := range s.chatStoreRead(sid).SelectionContexts {
		if old.Reply == claim && old.Delivered {
			context.Delivered = true
		}
	}
	if context.Name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "показанный вопрос, снимок или ID выбора не найден"})
		return
	}
	if context.Delivered {
		s.chatSayDone(sid, claim, "ask")
		writeJSON(w, http.StatusOK, map[string]any{"way": "dup", "task": answer.Task})
		return
	}
	if err := draftmeta.GuardRecord(answer.Tree, answer.Task); err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	if matched {
		if err := s.chatStoreUpdate(sid, func(st *chatStore) error {
			st.SelectionContexts = mergeSelectionContexts(st.SelectionContexts, []selectionDeliveryContext{context})
			return nil
		}); err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
			return
		}
	}
	data, _ := json.Marshal(answer)
	args := []string{"decide", answer.Task, "--selection", string(data), "--selection-reply", claim}
	if !matched {
		args = append(args, "--selection-repeat-only")
	}
	result, code, err := s.taskctlWrite(answer.Tree, args...)
	if err != nil {
		writeJSON(w, code, map[string]string{"error": err.Error()})
		return
	}
	wire := (chat.SelectionMessage{Version: 1, Text: text, Selection: &answer, Result: result}).Wire()
	if err := s.deliverSelection(p, sid, context, wire); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "выбор сохранён; доставка агенту: " + procErr(err)})
		return
	}
	context.Delivered = true
	if err := s.chatStoreUpdate(sid, func(st *chatStore) error {
		st.SelectionContexts = mergeSelectionContexts(st.SelectionContexts, []selectionDeliveryContext{context})
		return nil
	}); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "выбор доставлен; сохранение доставки: " + err.Error()})
		return
	}
	s.chatSayDone(sid, claim, "ask")
	s.saidSay(saidSessionKey(sid), wire, "ask")
	writeJSON(w, http.StatusOK, map[string]any{"way": "ask", "task": answer.Task, "wire": wire})
}

func selectionTransport(context selectionDeliveryContext) string {
	if context.Transport != "" {
		return context.Transport
	}
	return context.Selection.Tree
}

func (s *server) deliverSelection(p *Project, sid string, context selectionDeliveryContext, wire string) error {
	answer := context.Selection
	recs := s.bindsAll()
	last := sessions.Last(recs[sid])
	if last.Task == answer.Task && (last.Tree == answer.Tree || last.Tree == "" && p.Path == answer.Tree) {
		if term := s.sayTermOf(sid, recs); term != "" {
			return chatSend(term, wire)
		}
	}
	// Носитель записи это сам разговор, и живого терминала у него нет: ответ
	// доезжает сокетом либо подъёмом продолжения (DK-1569), а адресная строка
	// в мёртвый вход больше не пишется. Вопрос черновика приходит из чата,
	// чья привязка задачу не называет (DK-1679): адресат живёт в самом
	// признаке, поэтому дорога носителя открыта и без собственной привязки.
	if context.Session != "" && s.deliverToCarrier(p, context.Session, answer.Task, wire) {
		return nil
	}
	// Запасной путь безадресен: безадресную строку сторожок считает ответом и
	// будит припаркованную строку тиком, адресная в мёртвом входе лежала бы
	// молча (DK-1569).
	_, _, err := putChat(selectionTransport(context), context.Name, wire, chat.TaskLine(s.now(), wire))
	return err
}

func (s *server) handleScenarioCommentSay(w http.ResponseWriter, p *Project, sid, claim, text string) {
	for _, h := range s.handedAsks(sid, s.binds(), s.now()) {
		for _, q := range h.Ask.Questions {
			if q.Scenario == nil {
				continue
			}
			tree := h.Tree
			if q.Scenario.Tree != "" {
				tree = q.Scenario.Tree
			}
			context := selectionDeliveryContext{Selection: chat.Selection{Task: h.Ask.Task, Tree: tree}, Name: h.Name, Session: h.Ask.Session}
			wire := (chat.SelectionMessage{Version: 1, Text: text}).Wire()
			if err := s.deliverSelection(p, sid, context, wire); err != nil {
				writeJSON(w, http.StatusBadGateway, map[string]string{"error": procErr(err)})
				return
			}
			s.chatSayDone(sid, claim, "ask")
			s.saidSay(saidSessionKey(sid), wire, "ask")
			writeJSON(w, http.StatusOK, map[string]any{"way": "ask", "task": h.Ask.Task, "wire": wire})
			return
		}
	}
	writeJSON(w, http.StatusConflict, map[string]string{"error": "вопрос изменился; повторите отправку"})
}

func projectPanelReply(item *reply) {
	if item.Role != "user" {
		return
	}
	if message, ok := chat.ReadSelectionMessage(item.Text); ok {
		item.Wire, item.Text, item.Selection = item.Text, message.Text, message.Selection
		if w := cutWraps(message.Text); len(w.shots) > 0 || len(w.frags) > 0 || w.pick != "" || w.sel != "" {
			item.Text = w.rest
			item.Shots = append(item.Shots, w.shots...)
			item.Frags = append(item.Frags, w.frags...)
			item.Pick, item.PickScreen = w.pick, w.screen
			item.Sel, item.SelFile = w.sel, w.file
		}
		return
	}
	const prefix = "Ответ панели обработан taskctl: "
	if strings.HasPrefix(item.Text, prefix) {
		var message struct {
			Text     string                  `json:"text"`
			Key      string                  `json:"key"`
			Results  []draftproposal.Receipt `json:"results"`
			Proposal proposalAnswer          `json:"proposal"`
		}
		if json.Unmarshal([]byte(strings.TrimPrefix(item.Text, prefix)), &message) == nil && message.Key != "" && len(message.Results) > 0 {
			item.Wire, item.Text = item.Text, message.Text
			item.Proposal = &proposalAttachment{Decisions: message.Proposal.Decisions, Results: message.Results}
		}
	}
}

func replyWire(item reply) string {
	if item.Wire != "" {
		return item.Wire
	}
	return item.Text
}

func mergeSelectionContexts(groups ...[]selectionDeliveryContext) []selectionDeliveryContext {
	var out []selectionDeliveryContext
	for _, group := range groups {
		for _, context := range group {
			found := false
			for i, old := range out {
				if selectionContextMigrates(old, context) {
					out[i] = context
					found = true
					break
				}
				if selectionContextMigrates(context, old) {
					found = true
					break
				}
				previous := old
				previous.Delivered = context.Delivered
				if reflect.DeepEqual(previous, context) {
					if context.Delivered {
						out[i].Delivered = true
					}
					found = true
					break
				}
			}
			if !found {
				out = append(out, context)
			}
		}
	}
	return out
}
