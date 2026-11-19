package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

func appSharedUserReplies(item map[string]any, now string) []reply {
	content, _ := item["content"].([]any)
	var parts []string
	for _, part := range content {
		input, _ := part.(map[string]any)
		if input["type"] == "text" {
			if text, ok := input["text"].(string); ok {
				parts = append(parts, text)
			}
		}
	}
	wire := strings.Join(parts, "\n")
	if strings.TrimSpace(wire) == "" {
		return nil
	}
	if ms, ok := item["startedAtMs"].(float64); ok && ms > 0 {
		now = time.UnixMilli(int64(ms)).UTC().Format(time.RFC3339)
	}
	return []reply{{Role: "user", Text: wire, Wire: wire, Time: now}}
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
			if err := s.appFeedAppend(c.sid, items); err != nil {
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
