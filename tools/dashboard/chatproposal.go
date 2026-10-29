package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/dronrider/devkit/internal/chat"
	"github.com/dronrider/devkit/internal/draftmeta"
	"github.com/dronrider/devkit/internal/draftproposal"
	"github.com/dronrider/devkit/internal/sessions"
	"github.com/dronrider/devkit/internal/stage"
)

type proposalAnswer struct {
	Mode      string                   `json:"mode,omitempty"`
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
	if answer.Mode != "" && answer.Mode != "apply" && answer.Mode != "comment" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "неверный вид ответа"})
		return
	}
	var match *handedAsk
	for _, h := range s.handedAsks(sid, s.binds(), s.now()) {
		q := proposalFromAsk(h.Ask)
		valid := false
		if q != nil && answer.Mode == "comment" {
			valid = proposalCommentMatches(*q, answer)
		}
		if q != nil && answer.Mode != "comment" {
			valid = chat.CheckProposalAnswer(*q, answer.Tree, draftproposal.ConfirmRequest{Snapshots: answer.Snapshots, Decisions: answer.Decisions}) == nil
		}
		if q != nil && q.Anchor == answer.Anchor && q.Tree == answer.Tree && valid {
			own := h
			match = &own
			break
		}
	}
	if answer.Mode == "comment" && match == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "показанный вопрос предложений не найден"})
		return
	}
	deliverySources := answer.Snapshots
	if match != nil {
		if shown := proposalFromAsk(match.Ask); shown != nil {
			deliverySources = make([]draftproposal.Snapshot, 0, len(shown.Sources))
			for _, source := range shown.Sources {
				deliverySources = append(deliverySources, draftproposal.Snapshot{Source: source.Source, Digest: source.Basis})
			}
			if answer.Mode != "comment" {
				if err := s.rememberProposalContext(sid, *shown, answer); err != nil {
					writeJSON(w, http.StatusBadGateway, map[string]string{"error": "контекст доставки не сохранён: " + err.Error()})
					return
				}
			}
		}
	} else {
		key, err := proposalContextKey(answer)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		var found bool
		for _, context := range s.proposalContexts(sid) {
			if context.Key != key || context.Tree != answer.Tree || context.Anchor != answer.Anchor {
				continue
			}
			deliverySources = nil
			for _, source := range context.Sources {
				deliverySources = append(deliverySources, draftproposal.Snapshot{Source: source})
			}
			found = len(deliverySources) > 0
			break
		}
		if !found {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "показанный контекст доставки не найден"})
			return
		}
	}
	for _, snap := range deliverySources {
		if !goalIDRe.MatchString(snap.Source) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "неверный источник"})
			return
		}
		if err := draftmeta.GuardRecord(answer.Tree, snap.Source); err != nil {
			writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
			return
		}
	}
	if answer.Mode == "comment" {
		release, err := proposalDeliveryReservation(answer.Tree, deliverySources)
		if err != nil {
			writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
			return
		}
		defer release()
		binds := s.bindsAll()
		last := sessions.Last(binds[sid])
		term := ""
		if last.Task == answer.Anchor && (last.Tree == answer.Tree || (last.Tree == "" && p.Path == answer.Tree)) {
			term = s.sayTermOf(sid, binds)
		}
		err = nil
		if term != "" {
			err = chatSend(term, text)
		} else {
			line := chat.TaskLine(s.now(), text)
			if match.Ask.Session != "" {
				line = chat.Line(s.now(), match.Ask.Session, text)
			}
			_, _, err = putChat(stage.MainRoot(answer.Tree), chat.TaskName(answer.Anchor), text, line)
		}
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "доставка вопроса: " + procErr(err)})
			return
		}
		s.chatSayDone(sid, claim, "ask")
		s.saidSay(saidSessionKey(sid), text, "ask")
		writeJSON(w, http.StatusOK, map[string]any{"way": "ask", "task": answer.Anchor})
		return
	}
	data, err := proposalAnswerInput(answer)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
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
	release, err := proposalDeliveryReservation(answer.Tree, deliverySources)
	if err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	defer release()
	var canonical struct {
		Key      string                  `json:"key"`
		Results  []draftproposal.Receipt `json:"results"`
		Repeated bool                    `json:"repeated"`
	}
	if err := json.Unmarshal([]byte(result), &canonical); err != nil || canonical.Key == "" {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "ответ taskctl не разобран"})
		return
	}
	payload, _ := json.Marshal(struct {
		Text     string                  `json:"text"`
		Key      string                  `json:"key"`
		Results  []draftproposal.Receipt `json:"results"`
		Repeated bool                    `json:"repeated"`
	}{text, canonical.Key, canonical.Results, canonical.Repeated})
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

// Hold the same per-record cloud reservation as managed clients while the
// reply is handed off. An attach which won the race is observed by the guard;
// one which loses cannot change the source between that guard and delivery.
func proposalDeliveryReservation(root string, snapshots []draftproposal.Snapshot) (func(), error) {
	var locks []*draftmeta.CloudLock
	release := func() {
		for _, lock := range locks {
			lock.Close()
		}
	}
	for _, snap := range snapshots {
		lock, err := draftmeta.CloudShared(root, snap.Source)
		if err != nil {
			release()
			return nil, err
		}
		locks = append(locks, lock)
	}
	for _, snap := range snapshots {
		if err := draftmeta.GuardRecord(root, snap.Source); err != nil {
			release()
			return nil, err
		}
	}
	return release, nil
}

func proposalCommentMatches(q chat.ProposalQuestion, a proposalAnswer) bool {
	if q.Validate() != nil || a.Anchor != q.Anchor || a.Tree != q.Tree || len(a.Decisions) != 0 || len(a.Snapshots) != len(q.Sources) {
		return false
	}
	seen := map[string]string{}
	for _, snap := range a.Snapshots {
		if snap.Source == "" || snap.Digest == "" || seen[snap.Source] != "" {
			return false
		}
		seen[snap.Source] = snap.Digest
	}
	for _, source := range q.Sources {
		if seen[source.Source] != source.Basis {
			return false
		}
	}
	return true
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
	for _, context := range s.proposalContexts(sid) {
		if last.Task != "" && last.Task != context.Anchor {
			continue
		}
		if last.Tree != "" {
			current, currentErr := os.Stat(stage.MainRoot(last.Tree))
			original, originalErr := os.Stat(stage.MainRoot(context.Tree))
			if currentErr != nil || originalErr != nil || !os.SameFile(current, original) {
				continue
			}
		}
		for _, source := range context.Sources {
			items = append(items, item{context.Tree, source})
		}
	}
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
		if err := draftmeta.GuardRecord(one.tree, one.id); err != nil {
			continue
		}
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
