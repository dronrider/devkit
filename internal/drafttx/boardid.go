package drafttx

import (
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

type NumberAdoption struct{ Before, After string }

func numberAdoptionDir(root, source string) (string, error) {
	if source == "" || source == "." || source == ".." || source != filepath.Base(source) || strings.ContainsAny(source, "/\\") {
		return "", fmt.Errorf("неверный адрес закрепления номеров")
	}
	dir, err := ReservationDir(root)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "adoptions", source), nil
}

func numberDigest(s string) bool {
	if len(s) != 64 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

// RecordNumberAdoption пишется до after-image подготовки. Переход действует
// только при точном совпадении фактического снимка с After, поэтому сбой до
// применения не разрешает изменённый материал, а сбой после не теряет старый ask.
func RecordNumberAdoption(root, source, before, after string) error {
	if !numberDigest(before) || !numberDigest(after) || before == after {
		return fmt.Errorf("неверный снимок закрепления номеров")
	}
	dir, err := numberAdoptionDir(root, source)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(dir, 0700); err != nil {
		return serviceAccessError(dir, err)
	}
	path := filepath.Join(dir, before+"-"+after)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if errors.Is(err, os.ErrExist) {
		info, statErr := os.Lstat(path)
		if statErr != nil {
			return statErr
		}
		if !info.Mode().IsRegular() || info.Size() != 0 {
			return fmt.Errorf("повреждён переход закрепления номеров %s", path)
		}
		f, err = os.OpenFile(path, os.O_WRONLY, 0600)
	}
	if err != nil {
		return serviceAccessError(path, err)
	}
	err = f.Sync()
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	for _, path := range []string{dir, filepath.Dir(dir), filepath.Dir(filepath.Dir(dir))} {
		if err = syncDir(path); err != nil {
			return err
		}
	}
	return nil
}

func NumberAdoptions(root, source string) ([]NumberAdoption, error) {
	dir, err := numberAdoptionDir(root, source)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, serviceAccessError(dir, err)
	}
	var out []NumberAdoption
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() || info.Size() != 0 {
			return nil, fmt.Errorf("повреждён переход закрепления номеров %s", entry.Name())
		}
		before, after, ok := strings.Cut(entry.Name(), "-")
		if entry.IsDir() || !ok || !numberDigest(before) || !numberDigest(after) || before == after {
			return nil, fmt.Errorf("повреждён переход закрепления номеров %s", entry.Name())
		}
		out = append(out, NumberAdoption{Before: before, After: after})
	}
	return out, nil
}
