package main

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/dronrider/devkit/internal/chat"
)

type appTaskMessage struct {
	ID       string `json:"id"`
	Claim    string `json:"claim"`
	Text     string `json:"text,omitempty"`
	Effort   string `json:"effort,omitempty"`
	Sent     bool   `json:"sent,omitempty"`
	Accepted bool   `json:"accepted,omitempty"`
}

func (s *server) appTaskQueue(sid string, change func(*[]appTaskMessage) bool) error {
	path := filepath.Join(chatStoreDir(s.cfg.Home), "app-task-queue", sid+".json")
	lock, err := chat.Lock(path + ".lock")
	if err != nil {
		return err
	}
	defer lock.Close()
	var entries []appTaskMessage
	raw, err := os.ReadFile(path)
	if err == nil {
		if err = json.Unmarshal(raw, &entries); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if !change(&entries) {
		return nil
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".queue-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	err = json.NewEncoder(f).Encode(entries)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		return errors.Join(err, closeErr)
	}
	if err = os.Rename(f.Name(), path); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

var errAppTaskClaimConflict = errors.New("ключ реплики уже занят другим текстом")

func appTaskReplyEffort(chosen, saved string) string {
	if chosen != "" {
		return chosen
	}
	return appEffort(saved)
}

func (s *server) appTaskQueueAdd(sid, claim, text string, effort ...string) (appTaskMessage, error) {
	entry := appTaskMessage{ID: msgID(), Claim: claim, Text: text}
	if len(effort) != 0 {
		entry.Effort = effort[0]
	}
	if entry.Claim == "" {
		entry.Claim = entry.ID
	}
	conflict := false
	err := s.appTaskQueue(sid, func(entries *[]appTaskMessage) bool {
		for _, old := range *entries {
			if old.Claim == entry.Claim {
				conflict = !old.Accepted && old.Text != text
				entry = old
				return false
			}
		}
		*entries = append(*entries, entry)
		return true
	})
	if conflict {
		err = errAppTaskClaimConflict
	}
	return entry, err
}

var appInboxID = regexp.MustCompile(`(?:^| )\[app-import:[a-f0-9-]+\]$`)
var appInboxStamp = regexp.MustCompile(`^\d{4}-\d{2}-\d{2} \d{2}:\d{2}`)

func appTaskImportLine(line, sid string) string {
	if !chat.ForMe(line, sid) {
		return line
	}
	head, tail, dashboard := strings.Cut(line, ", из дашборда: ")
	if dashboard && chat.Addressee(line) == sid && appInboxID.MatchString(head) {
		return line
	}
	mark := ", [app-import:" + msgID() + "]"
	address := ""
	if chat.Addressee(line) == "" {
		address = ", сессии " + sid
	}
	if dashboard {
		return head + address + mark + ", из дашборда: " + tail
	}
	stamp := appInboxStamp.FindString(line)
	if stamp == "" {
		stamp = "вход"
	}
	return stamp + address + mark + ", из дашборда: " + line

}

func (s *server) appTaskQueueImport(p *Project, sid, id string) error {
	return chat.TransferLines(p.Path, chat.TaskName(id), func(line string) string {
		return appTaskImportLine(line, sid)
	}, func(lines []string) ([]string, error) {
		var keep []string
		for _, line := range lines {
			if !chat.ForMe(line, sid) {
				keep = append(keep, line)
				continue
			}
			claim := fmt.Sprintf("inbox-%x", sha256.Sum256([]byte(line)))
			if _, err := s.appTaskQueueAdd(sid, claim, chat.Said(line)); err != nil {
				return nil, err
			}
		}
		return keep, nil
	})
}

func (s *server) appTaskQueueNext(sid string) (appTaskMessage, error) {
	var entry appTaskMessage
	err := s.appTaskQueue(sid, func(entries *[]appTaskMessage) bool {
		for _, candidate := range *entries {
			if !candidate.Accepted {
				entry = candidate
				break
			}
		}
		return false
	})
	return entry, err
}

type appTaskDelivery struct {
	sent     bool
	accepted bool
}

func (s *server) appTaskQueueMark(sid, id string, delivery appTaskDelivery) error {
	return s.appTaskQueue(sid, func(entries *[]appTaskMessage) bool {
		for i := range *entries {
			entry := &(*entries)[i]
			if entry.ID == id {
				entry.Sent, entry.Accepted = delivery.sent, delivery.accepted
				if delivery.accepted {
					entry.Text = ""
				}
				return true
			}
		}
		return false
	})
}

func (s *server) appTaskQueueFeed(sid string, entry appTaskMessage) error {
	it := reply{Role: "user", Text: entry.Text, Wire: entry.Text, Time: s.now().UTC().Format(time.RFC3339),
		App: &appFeedEvent{Method: "devkit/appTaskMessage", Item: map[string]any{"id": entry.ID}}}
	appFeedDisplay(&it)
	return s.appFeedAppend(sid, []reply{it})
}

type appTaskThread struct {
	ID     string `json:"id"`
	Status struct {
		Type string `json:"type"`
	} `json:"status"`
	Turns []struct {
		ID        string `json:"id"`
		Status    string `json:"status"`
		ItemsView string `json:"itemsView"`
		Items     []struct {
			Type     string `json:"type"`
			ClientID string `json:"clientId"`
		} `json:"items"`
	} `json:"turns"`
}

func appTaskReadThread(c *appChat) (*appTaskThread, error) {
	raw, err := c.proc.call("thread/read", map[string]any{"threadId": c.thread, "includeTurns": true})
	if err != nil {
		return nil, fmt.Errorf("не удалось сверить исходную нить Codex: %w", err)
	}
	var got struct {
		Thread appTaskThread `json:"thread"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		return nil, fmt.Errorf("не прочитан ответ о состоянии исходной нити Codex: %w", err)
	}
	if got.Thread.ID != c.thread || got.Thread.Status.Type == "" || got.Thread.Turns == nil {
		return nil, errors.New("клиент не подтвердил историю и состояние исходной нити Codex")
	}
	if got.Thread.Status.Type == "active" {
		return nil, errors.New("в исходной нити Codex ещё идёт операция")
	}
	for _, turn := range got.Thread.Turns {
		if turn.Status == "inProgress" {
			return nil, errors.New("в исходной нити Codex ещё идёт ход")
		}
		if turn.ItemsView != "full" {
			return nil, errors.New("клиент не подтвердил полные записи ходов исходной нити Codex")
		}
	}
	return &got.Thread, nil
}

func (t *appTaskThread) accepted(id string) bool {
	for _, turn := range t.Turns {
		for _, item := range turn.Items {
			if item.Type == "userMessage" && item.ClientID == id {
				return true
			}
		}
	}
	return false
}
