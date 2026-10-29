package draftmeta

import (
	"fmt"
	"strings"
)

// Material hides metadata fields in the header while retaining the text body.
func Material(data []byte) ([]byte, bool, error) {
	if _, err := Parse(data); err != nil {
		return nil, false, err
	}
	lines := strings.Split(string(data), "\n")
	out := make([]string, 0, len(lines))
	head, found := true, false
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, "## ") {
			head = false
		}
		key, _, ok := strings.Cut(line, ":")
		if head && ok && (strings.TrimSpace(key) == "тип" || strings.TrimSpace(key) == "источник" || strings.TrimSpace(key) == "замок") {
			found = true
			continue
		}
		out = append(out, raw)
	}
	return []byte(strings.Join(out, "\n")), found, nil
}

// Restore preserves a record's metadata when an editor saves its material.
func Restore(edited, original []byte) ([]byte, error) {
	header, err := Parse(original)
	if err != nil {
		return nil, err
	}
	_, had, err := Material(original)
	if err != nil {
		return nil, err
	}
	clean, _, err := Material(edited)
	if err != nil {
		return nil, err
	}
	if !had {
		return clean, nil
	}
	lines := strings.Split(string(clean), "\n")
	at := -1
	for i, line := range lines {
		if strings.TrimSpace(line) != "" {
			at = i + 1
			break
		}
	}
	if at < 0 {
		return nil, fmt.Errorf("пустой текст записи")
	}
	fields := []string{"тип: " + header.Type, "источник: " + header.Source, "замок: " + header.Lock}
	out := append([]string{}, lines[:at]...)
	out = append(out, fields...)
	out = append(out, lines[at:]...)
	return []byte(strings.Join(out, "\n")), nil
}
