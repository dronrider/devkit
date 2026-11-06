package main

import (
	"os"
	"path/filepath"
)

// Ранг цели зависит от её файла в docs/tasks. Проекция позволяет вернуть
// тело и ссылки до пересчёта, сохранив исходные файлы при ошибке пересчёта.
func rehydrateReopened(root string, b *Board, src, dst string) (*Board, error) {
	staged, err := newStagingBoard(root)
	if err != nil {
		return nil, err
	}
	defer staged.Close()
	oldRel, err := filepath.Rel(root, src)
	if err != nil {
		return nil, err
	}
	newRel, err := filepath.Rel(root, dst)
	if err != nil {
		return nil, err
	}
	oldPath, newPath := filepath.Join(staged.root, oldRel), filepath.Join(staged.root, newRel)
	if oldPath != newPath {
		if err := os.MkdirAll(filepath.Dir(newPath), 0o755); err != nil {
			return nil, err
		}
		if err := os.Rename(oldPath, newPath); err != nil {
			return nil, err
		}
		if err := rewriteLinksInFile(newPath, filepath.Dir(oldPath), filepath.Dir(newPath)); err != nil {
			return nil, err
		}
	}
	projected := *b
	projected.Path = boardPath(staged.root)
	_, restored, err := rehydrate(&projected)
	if err != nil {
		return nil, err
	}
	restored.Path = b.Path
	return restored, nil
}
