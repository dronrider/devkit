package main

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"sort"
	"strings"

	"github.com/dronrider/devkit/internal/chat"
	"github.com/dronrider/devkit/internal/draftproposal"
	"github.com/dronrider/devkit/internal/sessions"
	"github.com/dronrider/devkit/internal/stage"
)

func sameGroomTree(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	x, ex := os.Stat(stage.MainRoot(a))
	y, ey := os.Stat(stage.MainRoot(b))
	return ex == nil && ey == nil && os.SameFile(x, y)
}

func (s *server) inheritedGroomContext(sid, task string) (string, string) {
	binds := s.bindsAll()
	visited := map[string]bool{}
	var visit func(string) (string, string)
	visit = func(id string) (string, string) {
		if !chatKeyRe.MatchString(id) || visited[id] || len(visited) >= 64 {
			return "", ""
		}
		visited[id] = true
		last := sessions.Last(binds[id])
		if last.Task != task || last.Tree == "" {
			return "", ""
		}
		st := s.chatStoreRead(id)
		if st.GroomSource == task && (st.GroomMode == "run" || st.GroomMode == "groom") && sameGroomTree(last.Tree, st.GroomTree) {
			return st.GroomSource, st.GroomMode
		}
		if st.From != "" {
			if source, mode := visit(st.From); source != "" {
				return source, mode
			}
		}
		if last.Tmux != "" && sessions.TmuxOwner(binds, last.Tmux) == id {
			window := s.chatStoreRead("tmux-" + last.Tmux)
			if window.GroomSource == task && (window.GroomMode == "run" || window.GroomMode == "groom") && sameGroomTree(last.Tree, window.GroomTree) {
				return task, window.GroomMode
			}
			if window.From != "" {
				return visit(window.From)
			}
		}
		return "", ""
	}
	return visit(sid)
}

func (s *server) preserveGroomContext(sid, task string) error {
	source, mode := s.inheritedGroomContext(sid, task)
	if source == "" {
		return nil
	}
	last := sessions.Last(s.bindsAll()[sid])
	st := s.chatStoreRead(sid)
	if st.GroomSource == source && st.GroomMode == mode && st.GroomTree == last.Tree {
		return nil
	}
	st.GroomSource, st.GroomMode, st.GroomTree = source, mode, last.Tree
	return s.chatStoreWrite(sid, st)
}

// Delivery metadata contains no decisions or card text. Receipt keys and the
// source IDs identify which existing records must be guarded and re-read.
type proposalDeliveryContext struct {
	Key          string   `json:"key"`
	Tree         string   `json:"tree"`
	Anchor       string   `json:"anchor"`
	Sources      []string `json:"sources"`
	NativeTools  []string `json:"nativeTools,omitempty"`
	ShownSources []string `json:"shownSources,omitempty"`
}

func proposalContextKey(a proposalAnswer) (string, error) {
	req := draftproposal.ConfirmRequest{Snapshots: append([]draftproposal.Snapshot(nil), a.Snapshots...), Decisions: append([]draftproposal.Decision(nil), a.Decisions...)}
	sort.Slice(req.Snapshots, func(i, j int) bool { return req.Snapshots[i].Source < req.Snapshots[j].Source })
	sort.Slice(req.Decisions, func(i, j int) bool {
		if req.Decisions[i].Source != req.Decisions[j].Source {
			return req.Decisions[i].Source < req.Decisions[j].Source
		}
		return req.Decisions[i].Proposal < req.Decisions[j].Proposal
	})
	b, err := json.Marshal(req)
	if err != nil {
		return "", err
	}
	return draftproposal.Digest(b), nil
}

func mergeProposalContexts(groups ...[]proposalDeliveryContext) []proposalDeliveryContext {
	var result []proposalDeliveryContext
	index := map[string]int{}
	for _, group := range groups {
		for _, item := range group {
			sources := item.Sources
			nativeTools := item.NativeTools
			shownSources := item.ShownSources
			identity := item.Tree + "\x00" + item.Anchor + "\x00" + item.Key
			i, ok := index[identity]
			if !ok {
				i = len(result)
				index[identity] = i
				item.Sources = nil
				result = append(result, item)
			}
			seen := map[string]bool{}
			for _, id := range result[i].Sources {
				seen[id] = true
			}
			for _, id := range sources {
				if !seen[id] {
					result[i].Sources = append(result[i].Sources, id)
					seen[id] = true
				}
			}
			knownSources := map[string]bool{}
			for _, id := range result[i].ShownSources {
				knownSources[id] = true
			}
			for _, id := range shownSources {
				if !knownSources[id] {
					result[i].ShownSources = append(result[i].ShownSources, id)
					knownSources[id] = true
				}
			}
			knownTools := map[string]bool{}
			for _, id := range result[i].NativeTools {
				knownTools[id] = true
			}
			for _, id := range nativeTools {
				if !knownTools[id] {
					result[i].NativeTools = append(result[i].NativeTools, id)
					knownTools[id] = true
				}
			}
		}
	}
	return result
}

func (s *server) rememberProposalContext(sid string, q chat.PreparedQuestion, a proposalAnswer) error {
	if !chatKeyRe.MatchString(sid) {
		return fmt.Errorf("неверная сессия")
	}
	key, err := proposalContextKey(a)
	if err != nil {
		return err
	}
	item := proposalDeliveryContext{Key: key, Tree: q.Tree, Anchor: q.Anchor}
	for _, source := range a.Snapshots {
		item.Sources = append(item.Sources, source.Source)
	}
	for _, source := range q.Sources {
		item.ShownSources = append(item.ShownSources, source.Source)
	}
	if shown, tools := s.groomConsent(q.Tree, sid); shown != nil && reflect.DeepEqual(*shown, q) {
		item.NativeTools = tools
	}
	st := s.chatStoreRead(sid)
	if source, mode := s.inheritedGroomContext(sid, q.Anchor); source != "" {
		st.GroomSource, st.GroomMode, st.GroomTree = source, mode, q.Tree
	}
	st.ProposalContexts = append(st.ProposalContexts, item)
	return s.chatStoreWrite(sid, st)
}

func (s *server) rememberProposalShown(sid string, q chat.PreparedQuestion) error {
	if !chatKeyRe.MatchString(sid) || q.Validate() != nil {
		return fmt.Errorf("неверный показанный вопрос")
	}
	item := proposalDeliveryContext{Tree: q.Tree, Anchor: q.Anchor}
	for _, source := range q.Sources {
		item.Sources = append(item.Sources, source.Source)
	}
	st := s.chatStoreRead(sid)
	source, mode := s.inheritedGroomContext(sid, q.Anchor)
	modeChanged := source != "" && (st.GroomSource != source || st.GroomMode != mode || st.GroomTree != q.Tree)
	if modeChanged {
		st.GroomSource, st.GroomMode, st.GroomTree = source, mode, q.Tree
	}
	for _, old := range s.proposalContexts(sid) {
		if old.Key != "" || old.Tree != item.Tree || old.Anchor != item.Anchor {
			continue
		}
		seen := map[string]bool{}
		for _, id := range old.Sources {
			seen[id] = true
		}
		all := true
		for _, id := range item.Sources {
			if !seen[id] {
				all = false
				break
			}
		}
		if all {
			if modeChanged {
				return s.chatStoreWrite(sid, st)
			}
			return nil
		}
	}
	st.ProposalContexts = append(st.ProposalContexts, item)
	return s.chatStoreWrite(sid, st)
}

func (s *server) proposalContexts(sid string) []proposalDeliveryContext {
	var out []proposalDeliveryContext
	visited := map[string]bool{}
	var visit func(string)
	visit = func(key string) {
		if !chatKeyRe.MatchString(key) || visited[key] || len(visited) >= 64 {
			return
		}
		visited[key] = true
		st := s.chatStoreRead(key)
		out = mergeProposalContexts(out, st.ProposalContexts)
		if st.From != "" {
			visit(st.From)
		}
	}
	visit(sid)
	binds := s.bindsAll()
	last := sessions.Last(binds[sid])
	if last.Tmux != "" && sessions.TmuxOwner(binds, last.Tmux) == sid {
		visit("tmux-" + last.Tmux)
	}
	return out
}

type preparedConsentCapture struct {
	ask handedAsk
	key string
}

// capturePreparedFormConsent находит живые вопросы согласования, которые
// подтверждение с формы закрывает. Вопрос узнаётся по показанному снимку, той
// же сверкой, какой отвечает чат (CheckPreparedAnswer), а не по открытой
// нативной модалке: сессия груминга спрашивает taskctl из хода, и окно
// нативного инструмента у неё пусто. Прежние ворота согласия промахивались на
// каждом таком вопросе, признак переживал подтверждение, и чат груминга стоял
// «ждёт человека» после применённого разбора (живой случай DK-1672, DK-1679).
func (s *server) capturePreparedFormConsent(tree string, request draftproposal.ConfirmRequest) ([]preparedConsentCapture, error) {
	var captures []preparedConsentCapture
	for _, h := range askScan(tree, s.now(), nil) {
		shown := preparedFromAsk(h.Ask)
		sid := h.Ask.Session
		if shown == nil || shown.Tree != tree || !chatKeyRe.MatchString(sid) || chat.CheckPreparedAnswer(*shown, tree, request) != nil {
			continue
		}
		answer := proposalAnswer{Anchor: shown.Anchor, Tree: tree, Snapshots: request.Snapshots, Decisions: request.Decisions}
		key, err := proposalContextKey(answer)
		if err != nil {
			return nil, err
		}
		if err := s.rememberProposalContext(sid, *shown, answer); err != nil {
			return nil, err
		}
		captures = append(captures, preparedConsentCapture{h, key})
	}
	return captures, nil
}

func finishPreparedFormConsent(captures []preparedConsentCapture, key string) error {
	for _, capture := range captures {
		if key == "" || capture.key != key {
			continue
		}
		if _, err := chat.DropAskIf(capture.ask.Tree, capture.ask.Name, capture.ask.Ask); err != nil {
			return err
		}
	}
	return nil
}

// deliverPreparedFormReceipt довозит исход подтверждения с формы сессии,
// которая спрашивала согласие (DK-1679). Форма отвечает мимо чата, и без этой
// дороги спрашивавший узнавал о решении никогда: признак снят, чат молчит, а
// дозор мёртвых окон откладывал бы реплику в файл записи. Квитанция идёт той
// же строкой и той же дорогой, что и ответ в чате: клавишами в живое окно
// спрашивавшего либо носителю без терминала, и лишь без адреса ложится
// безадресной строкой для сессии, которая продолжит запись.
func (s *server) deliverPreparedFormReceipt(p *Project, captures []preparedConsentCapture, key string, results []draftproposal.Receipt, repeated bool, request draftproposal.ConfirmRequest) {
	wire, anchor := "", ""
	var undelivered []string
	for _, capture := range captures {
		if key == "" || capture.key != key || capture.ask.Ask.Session == "" {
			continue
		}
		if wire == "" {
			anchor = capture.ask.Ask.Task
			payload, err := json.Marshal(struct {
				Text     string                  `json:"text"`
				Key      string                  `json:"key"`
				Results  []draftproposal.Receipt `json:"results"`
				Repeated bool                    `json:"repeated"`
				Proposal proposalAnswer          `json:"proposal"`
			}{"", key, results, repeated, proposalAnswer{Anchor: anchor, Tree: p.Path, Snapshots: request.Snapshots, Decisions: request.Decisions}})
			if err != nil {
				s.logf("квитанция подтверждения с формы не собралась: %v", err)
				return
			}
			wire = "Ответ панели обработан taskctl: " + string(payload)
		}
		sid := capture.ask.Ask.Session
		if s.processedNativeConsent(p.Path, sid) {
			// Нативная модалка вопроса ещё открыта, и клавиши ушли бы в неё,
			// а не в разговор: у формы тот же барьер, что и у ответа в чате.
			peer, live := s.peers()[sid]
			if live && peerSay(peer.Sock, wire) == nil {
				continue
			}
			if _, _, err := putChat(stage.MainRoot(p.Path), chat.TaskName(anchor), wire, chat.Line(s.now(), sid, wire)); err != nil {
				s.logf("исход подтверждения с формы не записан в разговор %s: %v", sid, err)
			}
			continue
		}
		if s.deliverToCarrier(p, sid, anchor, wire) {
			continue
		}
		undelivered = append(undelivered, sid)
	}
	if len(undelivered) == 0 {
		return
	}
	if _, _, err := putChat(stage.MainRoot(p.Path), chat.TaskName(anchor), wire, chat.TaskLine(s.now(), wire)); err != nil {
		s.logf("исход подтверждения с формы не доставлен (%s): %v", strings.Join(undelivered, ", "), err)
	}
}
