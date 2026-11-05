package drafttx

import (
	"errors"
	"os"
	"path/filepath"
)

// ReservationDir находит общую область закрепления номеров доски (DK-1564):
// git-common-dir один на основное дерево и связанные worktree, поэтому номер,
// выданный в одном дереве до коммита, остаётся видимым другому. Локальные
// снимки доски и резерва тут не помогают, и общий lock их чтения тоже: общей
// должна быть сама область. У корня вне git своя область в .devkit.
func ReservationDir(root string) (string, error) {
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	canonical, err = filepath.Abs(canonical)
	if err != nil {
		return "", err
	}
	return filepath.Join(commonDevkit(canonical), "board-ids"), nil
}

// ClaimBoardID закрепляет номер в общей области. Захват атомарен созданием
// файла, поэтому двум деревьям один номер не достанется и без общего lock.
// Занятый номер отвечает os.ErrExist.
func ClaimBoardID(root, id string) error {
	dir, err := ReservationDir(root)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(dir, 0o700); err != nil {
		return serviceAccessError(dir, err)
	}
	f, err := os.OpenFile(filepath.Join(dir, id), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return serviceAccessError(dir, err)
	}
	return f.Close()
}

// ClaimedBoardIDs перечисляет номера, закреплённые в общей области.
func ClaimedBoardIDs(root string) (map[string]bool, error) {
	dir, err := ReservationDir(root)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return map[string]bool{}, nil
		}
		return nil, serviceAccessError(dir, err)
	}
	out := map[string]bool{}
	for _, e := range entries {
		if !e.IsDir() {
			out[e.Name()] = true
		}
	}
	return out, nil
}
