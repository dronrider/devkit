package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/dronrider/devkit/internal/draftproposal"
	"net/http"
	"reflect"
	"strconv"
	"strings"

	"github.com/dronrider/devkit/internal/chat"
	"github.com/dronrider/devkit/internal/draftmeta"
	"github.com/dronrider/devkit/internal/sessions"
)

type selectionDeliveryContext struct {
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

func (s *server) handleSelectionSay(w http.ResponseWriter, p *Project, sid, claim, text string, answer chat.Selection) {
	if claim == "" || !goalIDRe.MatchString(answer.Task) || !s.scenarioTree(answer.Tree, answer.Task) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "нужны дерево и задача показанного вопроса"})
		return
	}
	sum := sha256.Sum256([]byte(text))
	textKey := hex.EncodeToString(sum[:])
	for _, old := range s.chatStoreRead(sid).SelectionContexts {
		if old.Reply == claim && (!reflect.DeepEqual(selectionIdentity(old.Selection), selectionIdentity(answer)) || old.TextKey != textKey) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "ключ сообщения уже использован для другого ответа"})
			return
		}
	}
	context := selectionDeliveryContext{}
	matched := false
	for _, h := range s.handedAsks(sid, s.binds(), s.now()) {
		if selectionMatches(h, &answer) {
			context = selectionDeliveryContext{Reply: claim, TextKey: textKey, Selection: answer, Name: h.Name, Session: h.Ask.Session}
			matched = true
			break
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
	if context.Name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "показанный вопрос, снимок или ID выбора не найден"})
		return
	}
	if err := draftmeta.GuardRecord(answer.Tree, answer.Task); err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	if matched {
		st := s.chatStoreRead(sid)
		exists := false
		for _, old := range st.SelectionContexts {
			if reflect.DeepEqual(old, context) {
				exists = true
			}
		}
		if !exists {
			st.SelectionContexts = append(st.SelectionContexts, context)
			if err := s.chatStoreWrite(sid, st); err != nil {
				writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
				return
			}
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
	s.chatSayDone(sid, claim, "ask")
	s.saidSay(saidSessionKey(sid), wire, "ask")
	writeJSON(w, http.StatusOK, map[string]any{"way": "ask", "task": answer.Task, "wire": wire})
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
	line := chat.TaskLine(s.now(), wire)
	if last.Task == answer.Task && (last.Tree == answer.Tree || last.Tree == "" && p.Path == answer.Tree) && context.Session != "" {
		line = chat.Line(s.now(), context.Session, wire)
	}
	_, _, err := putChat(answer.Tree, context.Name, wire, line)
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
			for _, old := range out {
				if reflect.DeepEqual(old, context) {
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
