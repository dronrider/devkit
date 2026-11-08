package main

import (
	"encoding/json"
	"io"
	"os"
	"reflect"
	"strings"
	"time"

	"github.com/dronrider/devkit/internal/chat"
	"github.com/dronrider/devkit/internal/draftmeta"
	"github.com/dronrider/devkit/internal/draftproposal"
	"github.com/dronrider/devkit/internal/sessions"
)

type consentTool struct {
	ID    string
	At    time.Time
	Name  string
	Input json.RawMessage
}

type consentActivity struct {
	Last time.Time
	Open map[string]consentTool
}

func readConsentActivity(path string) (consentActivity, bool) {
	out := consentActivity{Open: map[string]consentTool{}}
	f, err := os.Open(path)
	if err != nil {
		return out, false
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return out, false
	}
	if fi.Size() > turnTokLimit {
		if _, err := f.Seek(fi.Size()-turnTokLimit, io.SeekStart); err != nil {
			return out, false
		}
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return out, false
	}
	for _, line := range strings.Split(string(data), "\n") {
		var record struct {
			Timestamp time.Time `json:"timestamp"`
			Message   struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal([]byte(line), &record) != nil {
			continue
		}
		if record.Timestamp.After(out.Last) {
			out.Last = record.Timestamp
		}
		var blocks []struct {
			Type      string          `json:"type"`
			ID        string          `json:"id"`
			Name      string          `json:"name"`
			Input     json.RawMessage `json:"input"`
			ToolUseID string          `json:"tool_use_id"`
		}
		if json.Unmarshal(record.Message.Content, &blocks) != nil {
			continue
		}
		for _, b := range blocks {
			if b.Type == "tool_use" && b.ID != "" {
				out.Open[b.ID] = consentTool{ID: b.ID, At: record.Timestamp, Name: b.Name, Input: b.Input}
			}
			if b.Type == "tool_result" {
				delete(out.Open, b.ToolUseID)
			}
		}
	}
	return out, !out.Last.IsZero()
}

func consentSourceName(id string) string {
	return strings.ReplaceAll(strings.ToUpper(strings.TrimSpace(id)), "-", "")
}

func nativeConsentTool(tool consentTool, sources []string) bool {
	if tool.Name != "AskUserQuestion" {
		return false
	}
	var input struct {
		Questions []struct {
			Header   string `json:"header"`
			Question string `json:"question"`
			Multi    bool   `json:"multiSelect"`
			Options  []struct {
				Label string `json:"label"`
			} `json:"options"`
		} `json:"questions"`
	}
	if json.Unmarshal(tool.Input, &input) != nil || len(input.Questions) != len(sources) {
		return false
	}
	wanted := map[string]bool{}
	for _, source := range sources {
		wanted[consentSourceName(source)] = true
	}
	for _, question := range input.Questions {
		id := consentSourceName(question.Header)
		if !wanted[id] || question.Multi || len(question.Options) != 2 {
			return false
		}
		words := strings.ToLower(question.Question)
		if !strings.Contains(words, "разбор") && !strings.Contains(words, "подготов") && !strings.Contains(words, "груминг") {
			return false
		}
		if !strings.HasPrefix(strings.ToLower(question.Options[0].Label), "подтвердить") || !strings.HasPrefix(strings.ToLower(question.Options[1].Label), "отклонить") {
			return false
		}
		delete(wanted, id)
	}
	return len(wanted) == 0
}

func (s *server) consentQuiet(tree, sid string, sources []string, notBefore time.Time) ([]string, bool) {
	p, ok := s.peers()[sid]
	if !ok || (p.Status != "idle" && p.Status != "waiting") || p.Updated <= 0 {
		return nil, false
	}
	info, ok := findSession(s.transcriptRoots(), tree, sid)
	if !ok || s.subBusyOf(info.path, s.now()) {
		return nil, false
	}
	activity, ok := readConsentActivity(info.path)
	if !ok || activity.Last.After(time.UnixMilli(p.Updated).Add(time.Second)) {
		return nil, false
	}
	var tools []string
	for _, tool := range activity.Open {
		if tool.At.Before(notBefore) || !nativeConsentTool(tool, sources) {
			return nil, false
		}
		tools = append(tools, tool.ID)
	}
	if p.Status == "waiting" && len(tools) == 0 {
		return nil, false
	}
	return tools, true
}

// The CLI checks the original shown snapshot. Waiting alone can refer to an
// old Ask while the agent is writing a different result.
func (s *server) groomConsent(tree, sid string) (*chat.PreparedQuestion, []string) {
	for _, h := range s.handedAsks(sid, s.binds(), s.now()) {
		shown := preparedFromAsk(h.Ask)
		if shown == nil || !sameGroomTree(tree, shown.Tree) {
			continue
		}
		owner := h.Ask.Session
		if owner == "" {
			owner = sid
		}
		sources := make([]string, 0, len(shown.Sources))
		for _, source := range shown.Sources {
			sources = append(sources, source.Source)
		}
		published, statErr := os.Stat(chat.AskPath(h.Tree, h.Name))
		if statErr != nil {
			continue
		}
		tools, quiet := s.consentQuiet(shown.Tree, owner, sources, published.ModTime())
		if !quiet {
			continue
		}
		if owner != sid {
			if _, quiet := s.consentQuiet(tree, sid, sources, time.Time{}); !quiet {
				continue
			}
		}
		if s.consentOtherWriter(shown.Tree, owner, sid, sources) {
			continue
		}
		raw, _, err := s.consentQuery(shown.Tree, "draft", "prepared", "check-ask", shown.Anchor)
		var current chat.PreparedQuestion
		if err != nil || json.Unmarshal([]byte(raw), &current) != nil || !reflect.DeepEqual(*shown, current) {
			continue
		}
		if _, quiet := s.consentQuiet(shown.Tree, owner, sources, published.ModTime()); !quiet {
			continue
		}
		return shown, tools
	}
	return nil, nil
}

func (s *server) hasGroomConsent(tree, sid string) bool {
	shown, _ := s.groomConsent(tree, sid)
	return shown != nil || s.processedNativeConsent(tree, sid)
}

func (s *server) processedNativeConsent(tree, sid string) bool {
	for _, context := range s.proposalContexts(sid) {
		if context.Key == "" || len(context.NativeTools) == 0 || !sameGroomTree(tree, context.Tree) {
			continue
		}
		if s.consentOtherWriter(context.Tree, sid, sid, context.Sources) {
			continue
		}
		tools, quiet := s.consentQuiet(context.Tree, sid, context.Sources, time.Time{})
		if !quiet || len(tools) != len(context.NativeTools) {
			continue
		}
		same := true
		for _, id := range tools {
			found := false
			for _, old := range context.NativeTools {
				if id == old {
					found = true
				}
			}
			if !found {
				same = false
			}
		}
		if !same {
			continue
		}
		applied := true
		for _, source := range context.Sources {
			if draftmeta.GuardRecord(context.Tree, source) != nil {
				applied = false
				break
			}
			raw, _, err := s.consentQuery(context.Tree, "draft", "prepared", source)
			var view struct {
				State draftproposal.State `json:"state"`
			}
			if err != nil || json.Unmarshal([]byte(raw), &view) != nil || view.State.State != "history" {
				applied = false
				break
			}
			matched := false
			for _, receipt := range view.State.Receipts {
				if receipt.Key == context.Key && receipt.Source == source {
					matched = true
				}
			}
			if !matched {
				applied = false
				break
			}
		}
		if applied {
			return true
		}
	}
	return false
}

type consentFlight struct {
	done chan struct{}
	raw  string
	code int
	err  error
}

// Only concurrent polls share a result. A later poll always validates the
// current files; quiescence is checked independently before every query.
func (s *server) consentQuery(tree string, args ...string) (string, int, error) {
	key := tree + "\x00" + strings.Join(args, "\x00")
	s.mu.Lock()
	if fl := s.consentFlights[key]; fl != nil {
		s.mu.Unlock()
		<-fl.done
		return fl.raw, fl.code, fl.err
	}
	fl := &consentFlight{done: make(chan struct{})}
	if s.consentFlights == nil {
		s.consentFlights = map[string]*consentFlight{}
	}
	s.consentFlights[key] = fl
	s.mu.Unlock()
	g := taskctlGate
	g.enter()
	defer func() {
		g.leave()
		s.mu.Lock()
		delete(s.consentFlights, key)
		close(fl.done)
		s.mu.Unlock()
	}()
	fl.raw, fl.code, fl.err = taskctlDo(tree, args...)
	return fl.raw, fl.code, fl.err
}

func (s *server) consentOtherWriter(tree, owner, sid string, sources []string) bool {
	binds := s.bindsAll()
	for other := range s.peers() {
		if other == owner || other == sid {
			continue
		}
		last := sessions.Last(binds[other])
		if !sameGroomTree(last.Tree, tree) {
			continue
		}
		for _, source := range sources {
			if last.Task == source {
				if _, quiet := s.consentQuiet(tree, other, sources, time.Time{}); !quiet {
					return true
				}
			}
		}
	}
	return false
}
