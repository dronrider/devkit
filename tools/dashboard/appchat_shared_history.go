package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

func appSharedUserReplies(item map[string]any, now string) []reply {
	content, _ := item["content"].([]any)
	var parts, attachments []string
	for _, part := range content {
		input, _ := part.(map[string]any)
		switch input["type"] {
		case "text":
			if text, ok := input["text"].(string); ok {
				parts = append(parts, text)
			}
		case "image", "localImage":
			attachments = append(attachments, "[изображение]")
		}
	}
	wire := strings.Join(parts, "\n")
	if strings.TrimSpace(wire) == "" && len(attachments) == 0 {
		return nil
	}
	text := wire
	if len(attachments) > 0 {
		text = strings.TrimSpace(strings.Join(append(parts, attachments...), "\n"))
	}
	if ms, ok := item["startedAtMs"].(float64); ok && ms > 0 {
		now = time.UnixMilli(int64(ms)).UTC().Format(time.RFC3339)
	}
	return []reply{{Role: "user", Text: text, Wire: wire, Time: now}}
}

type appLegacyEcho struct {
	wire string
	at   string
}

type appLegacyEchoIndex struct {
	turns   map[string]string
	pending map[appLegacyEcho]int
}

// appLegacyEchoes связывает старое эхо с первым записанным событием хода.
// Эхо без события остаётся кандидатом по тексту и времени своего хода.
func appLegacyEchoes(items []reply, thread string) appLegacyEchoIndex {
	index := appLegacyEchoIndex{turns: map[string]string{}, pending: map[appLegacyEcho]int{}}
	seen := map[string]bool{}
	var echo appLegacyEcho
	for _, r := range items {
		if r.Role == "user" {
			echo = appLegacyEcho{}
			if r.App == nil && r.Wire != "" {
				echo = appLegacyEcho{wire: r.Wire, at: r.Time}
				index.pending[echo]++
			}
			continue
		}
		if r.App != nil && r.App.ThreadID == thread && r.App.TurnID != "" && !seen[r.App.TurnID] {
			seen[r.App.TurnID] = true
			if echo.wire != "" {
				index.turns[r.App.TurnID] = echo.wire
				index.pending[echo]--
				echo = appLegacyEcho{}
			}
		}
	}
	return index
}

func (index appLegacyEchoIndex) consume(r reply) bool {
	if r.Role != "user" || r.Wire == "" || r.App == nil {
		return false
	}
	if clientID, _ := r.App.Item["clientId"].(string); clientID != "" {
		return false
	}
	if index.turns[r.App.TurnID] == r.Wire {
		delete(index.turns, r.App.TurnID)
		return true
	}
	at, err := time.Parse(time.RFC3339, r.Time)
	if err != nil {
		return false
	}
	// Дашборд писал старое эхо перед RPC, поэтому начало хода может
	// попасть в ту же или следующую секунду. У новых клиентов есть clientId.
	for _, candidate := range []time.Time{at, at.Add(-time.Second)} {
		key := appLegacyEcho{wire: r.Wire, at: candidate.Format(time.RFC3339)}
		if index.pending[key] > 0 {
			index.pending[key]--
			return true
		}
	}
	return false
}

type appSharedHistoryPage struct {
	Data []struct {
		ID        string           `json:"id"`
		Status    string           `json:"status"`
		StartedAt int64            `json:"startedAt"`
		Items     []map[string]any `json:"items"`
	} `json:"data"`
	NextCursor string `json:"nextCursor"`
}

// appSharedBackfill читает историю, пока новые события копятся в буфере.
// У записей из истории и буфера совпадает ключ завершённого события.
func (s *server) appSharedBackfill(c *appChat) error {
	params := map[string]any{
		"threadId": c.thread, "limit": 50, "sortDirection": "asc", "itemsView": "full",
	}
	legacy := appLegacyEchoes(s.appFeedRead(c.sid), c.thread)
	cursors := map[string]bool{}
	for {
		raw, err := c.proc.call("thread/turns/list", params)
		if err != nil {
			return fmt.Errorf("прочитать историю Codex: %w", err)
		}
		var page appSharedHistoryPage
		if err := json.Unmarshal(raw, &page); err != nil {
			return fmt.Errorf("разобрать историю Codex: %w", err)
		}
		if items := page.replies(c.thread); len(items) > 0 {
			kept := items[:0]
			for _, r := range items {
				if legacy.consume(r) {
					continue
				}
				kept = append(kept, r)
			}
			if err := s.appFeedAppend(c.sid, kept); err != nil {
				return fmt.Errorf("записать историю Codex: %w", err)
			}
		}
		cursor := page.NextCursor
		if cursor == "" {
			return nil
		}
		if cursors[cursor] {
			return fmt.Errorf("Codex повторил указатель страницы истории")
		}
		cursors[cursor] = true
		params["cursor"] = cursor
	}
}

func (p appSharedHistoryPage) replies(thread string) []reply {
	var items []reply
	for _, turn := range p.Data {
		if turn.ID == "" {
			continue
		}
		for _, item := range turn.Items {
			id, _ := item["id"].(string)
			if id == "" {
				continue
			}
			var now string
			if ms, ok := item["completedAtMs"].(float64); ok && ms > 0 {
				now = time.UnixMilli(int64(ms)).UTC().Format(time.RFC3339)
			} else if turn.Status == "inProgress" && item["type"] != "userMessage" {
				continue
			} else if turn.StartedAt > 0 {
				now = time.Unix(turn.StartedAt, 0).UTC().Format(time.RFC3339)
			}
			var replies []reply
			if item["type"] == "userMessage" {
				replies = appSharedUserReplies(item, now)
			} else {
				replies = appItemReplies(item, now)
			}
			event := &appFeedEvent{
				Method: "item/completed", ThreadID: thread, TurnID: turn.ID, Item: item,
			}
			for _, r := range replies {
				r.App = event
				items = append(items, r)
			}
		}
	}
	return items
}
