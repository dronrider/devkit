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

type appLegacyEchoIndex struct {
	turns   map[string]reply
	pending map[string]reply
}

// appLegacyEchoes учитывает старые записи перед общим подключением.
// Даже если старый ход можно опознать, в ленту пишется событие Codex.
func appLegacyEchoes(items []reply, thread string) appLegacyEchoIndex {
	index := appLegacyEchoIndex{turns: map[string]reply{}, pending: map[string]reply{}}
	seen := map[string]bool{}
	var echo reply
	for _, r := range items {
		if r.Role == "user" {
			echo = reply{}
			if r.App == nil && r.Wire != "" {
				echo = r
				index.pending[r.Key] = r
			}
			continue
		}
		if r.App != nil && r.App.ThreadID == thread && r.App.TurnID != "" && !seen[r.App.TurnID] {
			seen[r.App.TurnID] = true
			if echo.Key != "" {
				index.turns[r.App.TurnID] = echo
				echo = reply{}
			}
		}
	}
	return index
}

func (index appLegacyEchoIndex) reconcile(r *reply) {
	if r.Role != "user" || r.Wire == "" || r.App == nil {
		return
	}
	echo := index.turns[r.App.TurnID]
	clientID, _ := r.App.Item["clientId"].(string)
	if echo.Key == "" || echo.Wire != r.Wire || clientID != "" {
		return
	}
	r.Replaces = echo.Key
	delete(index.turns, r.App.TurnID)
	delete(index.pending, echo.Key)
}

const appLegacyUnconfirmed = "Прежняя отправка без подтверждения"

func (index appLegacyEchoIndex) unconfirmed(thread string) []reply {
	var notes []reply
	for key, old := range index.pending {
		notes = append(notes, reply{
			Role: "note", Note: appLegacyUnconfirmed, Text: old.Text,
			Time: old.Time, Replaces: key,
			App: &appFeedEvent{Method: "legacy/unconfirmed", ThreadID: thread, Item: map[string]any{"id": key}},
		})
	}
	return notes
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
			for i := range items {
				legacy.reconcile(&items[i])
			}
			if err := s.appFeedAppend(c.sid, items); err != nil {
				return fmt.Errorf("записать историю Codex: %w", err)
			}
		}
		cursor := page.NextCursor
		if cursor == "" {
			if notes := legacy.unconfirmed(c.thread); len(notes) > 0 {
				if err := s.appFeedAppend(c.sid, notes); err != nil {
					return fmt.Errorf("записать статус прежних реплик: %w", err)
				}
			}
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
