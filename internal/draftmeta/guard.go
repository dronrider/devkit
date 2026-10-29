package draftmeta

import (
	"fmt"
	"os"
	"path/filepath"
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
