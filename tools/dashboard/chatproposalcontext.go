package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"

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
	Key     string   `json:"key"`
	Tree    string   `json:"tree"`
	Anchor  string   `json:"anchor"`
	Sources []string `json:"sources"`
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
		}
	}
	return result
}

func (s *server) rememberProposalContext(sid string, q chat.ProposalQuestion, a proposalAnswer) error {
	if !chatKeyRe.MatchString(sid) {
		return fmt.Errorf("неверная сессия")
	}
	key, err := proposalContextKey(a)
	if err != nil {
		return err
	}
	item := proposalDeliveryContext{Key: key, Tree: q.Tree, Anchor: q.Anchor}
	for _, source := range q.Sources {
		item.Sources = append(item.Sources, source.Source)
	}
	st := s.chatStoreRead(sid)
	if source, mode := s.inheritedGroomContext(sid, q.Anchor); source != "" {
		st.GroomSource, st.GroomMode, st.GroomTree = source, mode, q.Tree
	}
	st.ProposalContexts = append(st.ProposalContexts, item)
	return s.chatStoreWrite(sid, st)
}

func (s *server) rememberProposalShown(sid string, q chat.ProposalQuestion) error {
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
