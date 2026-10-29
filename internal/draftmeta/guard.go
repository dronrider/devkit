package draftmeta

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// GuardDraft prevents a managed cloud launch for locked or unreadable drafts.
// It returns nil for an absent draft so callers can also handle board tasks.
func GuardDraft(root, id string) error {
	if id == "" || filepath.Base(id) != id || id == "." || id == ".." {
		return fmt.Errorf("неверный ID записи %q", id)
	}
	path := filepath.Join(root, "docs", "tasks", "drafts", id+".md")
	_, err := os.Stat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	h, err := Read(path)
	if err != nil {
		return err
	}
	if h.Lock == "locked" {
		return fmt.Errorf("%s: ждёт локальную модель", id)
	}
	return nil
}

// GuardRecord checks both a draft and the persistent protection of a task.
func GuardRecord(root, id string) error {
	if err := GuardDraft(root, id); err != nil {
		return err
	}
	path := filepath.Join(root, "docs", "tasks", id+".md")
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "## ") {
			break
		}
		if strings.HasPrefix(line, "защита") {
			key, value, ok := strings.Cut(line, ":")
			if !ok || strings.TrimSpace(key) != "защита" || strings.TrimSpace(value) != "locked" {
				return fmt.Errorf("%s: неверная защита материала", path)
			}
			return fmt.Errorf("%s: ждёт локальную модель", id)
		}
	}
	return nil
}

// ProtectRecord adds a durable marker without changing existing material.
func ProtectRecord(data []byte) ([]byte, error) {
	text := string(data)
	lines := strings.Split(text, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "## ") {
			break
		}
		if strings.HasPrefix(line, "защита") {
			if line == "защита: locked" {
				return data, nil
			}
			return nil, fmt.Errorf("неверная защита материала")
		}
	}
	if len(lines) == 0 || !strings.HasPrefix(strings.TrimSpace(lines[0]), "# ") {
		return nil, fmt.Errorf("нет заголовка материала")
	}
	return []byte(lines[0] + "\nзащита: locked\n" + strings.Join(lines[1:], "\n")), nil
}
