// Package draftmeta reads the metadata at the beginning of a draft.
package draftmeta

import (
	"fmt"
	"os"
	"strings"
	"time"
)

// Header describes metadata before the first second-level heading.
type Header struct {
	Type     string    `json:"type"`
	Source   string    `json:"source"`
	Lock     string    `json:"lock"`
	Written  time.Time `json:"written"`
	Priority string    `json:"priority"`
}

// Parse reads only the header; absent fields retain compatible defaults.
// Repeated or invalid metadata is rejected rather than interpreted as unlocked.
func Parse(data []byte) (Header, error) {
	h := Header{Type: "task", Source: "thought", Lock: "unlocked"}
	seen := make(map[string]bool)
	lines := strings.Split(string(data), "\n")
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, "## ") {
			break
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			if strings.HasPrefix(line, "замок") {
				return Header{}, fmt.Errorf("неверная строка замка %q", line)
			}
			if strings.HasPrefix(line, "записан ") {
				value = strings.TrimSpace(strings.TrimPrefix(line, "записан "))
				t, err := time.ParseInLocation("2006-01-02", value, time.Local)
				if err == nil && h.Written.IsZero() {
					h.Written = t
				}
			}
			continue
		}
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		if strings.HasPrefix(key, "замок") && key != "замок" {
			return Header{}, fmt.Errorf("неверная строка замка %q", line)
		}
		if key != "тип" && key != "источник" && key != "замок" && key != "приоритет" {
			continue
		}
		if seen[key] && key != "приоритет" {
			return Header{}, fmt.Errorf("повтор поля %s", key)
		}
		seen[key] = true
		switch key {
		case "тип":
			if value == "" {
				return Header{}, fmt.Errorf("пустое поле тип")
			}
			h.Type = value
		case "источник":
			switch value {
			case "thought", "meeting", "mail", "messenger", "link":
				h.Source = value
			default:
				return Header{}, fmt.Errorf("неизвестный источник %q", value)
			}
		case "замок":
			switch value {
			case "locked", "unlocked":
				h.Lock = value
			default:
				return Header{}, fmt.Errorf("неизвестное значение замка %q", value)
			}
		case "приоритет":
			if h.Priority != "" {
				continue
			}
			switch value {
			case "высокий":
				h.Priority = "high"
			case "средний":
				h.Priority = "mid"
			case "низкий":
				h.Priority = "low"
			}
		}
	}
	return h, nil
}

// Read reads and validates a draft header, including the file name in errors.
func Read(path string) (Header, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Header{}, err
	}
	h, err := Parse(data)
	if err != nil {
		return Header{}, fmt.Errorf("%s: %w", path, err)
	}
	return h, nil
}
