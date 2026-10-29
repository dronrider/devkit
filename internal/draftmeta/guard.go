package draftmeta

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dronrider/devkit/internal/drafttx"
)

// guardDraft checks a draft while the project lock is held.
// It returns nil for an absent draft so callers can also handle board tasks.
func guardDraft(root, id string) error {
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
	if id == "" || filepath.Base(id) != id || id == "." || id == ".." {
		return fmt.Errorf("неверный ID записи %q", id)
	}
	lock, err := drafttx.Acquire(root)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := guardDraft(root, id); err != nil {
		return err
	}
	path := filepath.Join(root, "docs", "tasks", id+".md")
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		archive := filepath.Join(root, "docs", "tasks", "archive")
		years, readErr := os.ReadDir(archive)
		if readErr != nil && !os.IsNotExist(readErr) {
			return readErr
		}
		var paths []string
		for _, year := range years {
			if !year.IsDir() {
				continue
			}
			candidate := filepath.Join(archive, year.Name(), id+".md")
			if _, statErr := os.Stat(candidate); statErr == nil {
				paths = append(paths, candidate)
			} else if !os.IsNotExist(statErr) {
				return statErr
			}
		}
		if len(paths) == 0 {
			return nil
		}
		if len(paths) != 1 {
			return fmt.Errorf("%s: несколько архивных записей", id)
		}
		path = paths[0]
		data, err = os.ReadFile(path)
	}
	if err != nil {
		return err
	}
	protected, err := RecordProtected(data)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	if protected {
		return fmt.Errorf("%s: ждёт локальную модель", id)
	}

	return nil
}

// ProtectRecord adds a durable marker without changing existing material.
func ProtectRecord(data []byte) ([]byte, error) {
	protected, err := RecordProtected(data)
	if err != nil {
		return nil, err
	}
	if protected {
		return data, nil
	}
	lines := strings.Split(string(data), "\n")

	if len(lines) == 0 || !strings.HasPrefix(strings.TrimSpace(lines[0]), "# ") {
		return nil, fmt.Errorf("нет заголовка материала")
	}
	return []byte(lines[0] + "\nзащита: locked\n" + strings.Join(lines[1:], "\n")), nil
}

// RecordProtected reads the machine protection marker in a task header.
func RecordProtected(data []byte) (bool, error) {
	found := false
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "## ") {
			break
		}
		if strings.HasPrefix(line, "защита") {
			key, value, ok := strings.Cut(line, ":")
			if !ok || strings.TrimSpace(key) != "защита" || strings.TrimSpace(value) != "locked" || found {
				return false, fmt.Errorf("неверная защита материала")
			}
			found = true
		}
	}
	return found, nil
}
