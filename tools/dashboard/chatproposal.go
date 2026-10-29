package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/dronrider/devkit/internal/chat"
	"github.com/dronrider/devkit/internal/draftmeta"
	"github.com/dronrider/devkit/internal/draftproposal"
	"github.com/dronrider/devkit/internal/sessions"
	"github.com/dronrider/devkit/internal/stage"
)

type proposalAnswer struct {
	Anchor    string                   `json:"anchor"`
	Tree      string                   `json:"tree"`
	Snapshots []draftproposal.Snapshot `json:"snapshots"`
	Decisions []draftproposal.Decision `json:"decisions"`
}

func proposalAnswerInput(a proposalAnswer) ([]byte, error) {
	if a.Anchor == "" || a.Tree == "" || len(a.Snapshots) == 0 || len(a.Decisions) == 0 {
		return nil, fmt.Errorf("нужен контекст показанных предложений")
	}
	return json.Marshal(struct {
		Tree      string                   `json:"tree"`
		Snapshots []draftproposal.Snapshot `json:"snapshots"`
		Decisions []draftproposal.Decision `json:"decisions"`
	}{a.Tree, a.Snapshots, a.Decisions})
}

func proposalFromAsk(a chat.Ask) *chat.ProposalQuestion {
	var found *chat.ProposalQuestion
	for _, q := range a.Questions {
		if q.Proposal == nil {
			continue
		}
		if found != nil || q.Scenario != nil || q.Proposal.Validate() != nil || q.Proposal.Anchor != a.Task {
			return nil
		}
		found = q.Proposal
	}
	return found
}

func (s *server) proposalContextRequired(p *Project, sid string) bool {
	asks := s.handedAsks(sid, s.binds(), s.now())
	if len(asks) == 0 {
		return false
	}
	active := asks[0].Ask
	if info, ok := findSession(s.transcriptRoots(), p.Path, sid); ok {
		head := s.sessionHeadCached(info.path, info.stamp)
		name, _ := s.sessionChatName(info, head)
		for _, h := range asks {
			if h.Tree == p.Path && h.Name == name && h.Ask.Session == sid {
				active = h.Ask
				break
			}
		}
	}
	for _, q := range active.Questions {
		if q.Proposal != nil {
			return true
		}
	}
	return false
}

func (s *server) handleProposalSay(w http.ResponseWriter, p *Project, sid, claim, text string, answer proposalAnswer) {
	if !goalIDRe.MatchString(answer.Anchor) || !s.scenarioTree(answer.Tree, answer.Anchor) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "нужно показанное дерево и адрес вопроса"})
		return
	}
	data, err := proposalAnswerInput(answer)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	var match *handedAsk
	for _, h := range s.handedAsks(sid, s.binds(), s.now()) {
		q := proposalFromAsk(h.Ask)
		if q != nil && q.Anchor == answer.Anchor && q.Tree == answer.Tree && chat.CheckProposalAnswer(*q, answer.Tree, draftproposal.ConfirmRequest{Snapshots: answer.Snapshots, Decisions: answer.Decisions}) == nil {
			own := h
			match = &own
			break
		}
	}
	for _, snap := range answer.Snapshots {
		if !goalIDRe.MatchString(snap.Source) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "неверный источник"})
			return
		}
		if err := draftmeta.GuardRecord(answer.Tree, snap.Source); err != nil {
			writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
			return
		}
	}
	args := []string{"draft", "proposals", "answer", answer.Anchor}
	if match == nil {
		args = append(args, "--repeat-only")
	}
	result, code, err := s.taskctlWriteIn(answer.Tree, string(data), args...)
	if err != nil {
		writeJSON(w, code, map[string]string{"error": err.Error()})
		return
	}
	for _, snap := range answer.Snapshots {
		if err := draftmeta.GuardRecord(answer.Tree, snap.Source); err != nil {
			writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
			return
		}
	}
	var canonical struct {
		Key     string                  `json:"key"`
		Results []draftproposal.Receipt `json:"results"`
	}
	if err := json.Unmarshal([]byte(result), &canonical); err != nil || canonical.Key == "" {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "ответ taskctl не разобран"})
		return
	}
	payload, _ := json.Marshal(struct {
		Text    string                  `json:"text"`
		Key     string                  `json:"key"`
		Results []draftproposal.Receipt `json:"results"`
	}{text, canonical.Key, canonical.Results})
	wire := "Ответ панели обработан taskctl: " + string(payload)
	// The shown tree and anchor remain the address even after a session rebind.
	tree := stage.MainRoot(answer.Tree)
	name := chat.TaskName(answer.Anchor)
	line := chat.TaskLine(s.now(), wire)
	if match != nil && match.Ask.Session != "" {
		line = chat.Line(s.now(), match.Ask.Session, wire)
	}
	term := ""
	binds := s.bindsAll()
	last := sessions.Last(binds[sid])
	if match != nil && last.Task == answer.Anchor && (last.Tree == answer.Tree || (last.Tree == "" && p.Path == answer.Tree)) {
		term = s.sayTermOf(sid, binds)
	}
	if term != "" {
		err = chatSend(term, wire)
	} else {
		_, _, err = putChat(tree, name, wire, line)
	}
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "CLI принял ответ; доставка агенту: " + procErr(err)})
		return
	}
	if match != nil {
		if _, err = chat.DropAskIf(match.Tree, match.Name, match.Ask); err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "ответ доставлен; признак вопроса: " + procErr(err)})
			return
		}
	}
	s.chatSayDone(sid, claim, "ask")
	s.saidSay(saidSessionKey(sid), text, "ask")
	writeJSON(w, http.StatusOK, map[string]any{"way": "ask", "task": answer.Anchor, "message": result})
}

func (s *server) proposalContinuation(root, sid string) string {
	type item struct{ tree, id string }
	var items []item
	for _, h := range s.handedAsks(sid, s.binds(), s.now()) {
		if q := proposalFromAsk(h.Ask); q != nil {
			for _, source := range q.Sources {
				items = append(items, item{q.Tree, source.Source})
			}
		}
	}
	last := sessions.Last(s.bindsAll()[sid])
	if last.Tree != "" {
		root = last.Tree
	}
	if last.Task != "" {
		items = append(items, item{root, last.Task})
	}
	seen := map[item]bool{}
	var blocks []string
	for _, one := range items {
		if seen[one] || !s.scenarioTree(one.tree, one.id) {
			continue
		}
		seen[one] = true
		out, _, err := taskctlDo(one.tree, "draft", "proposals", one.id)
		if err != nil {
			continue
		}
		var view struct {
			State draftproposal.State `json:"state"`
		}
		if json.Unmarshal([]byte(out), &view) != nil || len(view.State.Proposals) == 0 {
			continue
		}
		var lines []string
		for _, p := range view.State.Proposals {
			status := "ждёт решения"
			if p.Status == "accepted" {
				status = "принято"
			}
			if p.Status == "rejected" {
				status = "отклонено"
			}
			line := fmt.Sprintf("- %s, редакция %d: %s", p.ID, p.Revision, status)
			if p.Title != "" {
				line += "; " + p.Title
			}
			if p.Text != "" {
				line += "; " + p.Text
			}
			if p.Result != nil {
				if len(p.Result.IDs) > 0 {
					line += "; " + strings.Join(p.Result.IDs, ", ")
				}
				if p.Result.Reason != "" {
					line += "; " + p.Result.Reason
				}
			}
			lines = append(lines, line)
		}
		blocks = append(blocks, "Предложения "+one.id+"\n\n"+strings.Join(lines, "\n"))
	}
	return strings.Join(blocks, "\n\n")
}
