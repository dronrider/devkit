package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dronrider/devkit/internal/draftmeta"
)

func cmdFilePut(root, id, text string, c CommitOpts) (string, error) {
	if err := c.validate(); err != nil {
		return "", err
	}
	b, err := LoadBoard(boardPath(root))
	if err != nil {
		return "", err
	}
	if b.find(id) == nil {
		return "", fmt.Errorf("%s нет на доске", id)
	}
	path := taskFileAbs(root, id)
	old, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("файл задачи %s не найден: сначала заведите его кнопкой «Завести файл»: %w", id, err)
	}
	if strings.TrimSpace(text) == "" {
		return "", fmt.Errorf("пустой текст затёр бы постановку")
	}
	protected, err := draftmeta.RecordProtected(old)
	if err != nil {
		return "", err
	}
	data := []byte(strings.TrimRight(text, "\n") + "\n")
	if protected {
		data, err = draftmeta.ProtectRecord(data)
		if err != nil {
			return "", err
		}
	}
	if err = os.WriteFile(path, data, 0644); err != nil {
		return "", err
	}
	tail, err := c.apply(root, []string{filepath.Join("docs", "tasks", id+".md")})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("текст %s записан%s", filepath.Join("docs", "tasks", id+".md"), tail), nil
}
