package draftmeta

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/dronrider/devkit/internal/drafttx"
)

// CloudLock serializes private attachment with managed client lifetimes.
type CloudLock struct{ file *os.File }

func cloudFile(root, id string) (string, error) {
	id = strings.ToUpper(strings.TrimSpace(id))
	if id == "" || filepath.Base(id) != id || id == "." || id == ".." {
		return "", fmt.Errorf("неверный ID записи %q", id)
	}
	dir, err := drafttx.ServiceDir(root)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "cloud-locks", id), nil
}

// CloudShared is held from before checking material until the client exits.
func CloudShared(root, id string) (*CloudLock, error) { return cloudLock(root, id, syscall.LOCK_SH) }

// CloudExclusive never waits while a board transaction holds its lock.
func CloudExclusive(root, id string) (*CloudLock, error) {
	return cloudLock(root, id, syscall.LOCK_EX|syscall.LOCK_NB)
}

func CloudExclusiveIn(serviceDir, id string) (*CloudLock, error) {
	id = strings.ToUpper(strings.TrimSpace(id))
	if id == "" || filepath.Base(id) != id || id == "." || id == ".." {
		return nil, fmt.Errorf("неверный ID записи %q", id)
	}
	return cloudLockPath(filepath.Join(serviceDir, "cloud-locks", id), id, syscall.LOCK_EX|syscall.LOCK_NB)
}

func cloudLock(root, id string, how int) (*CloudLock, error) {
	path, err := cloudFile(root, id)
	if err != nil {
		return nil, err
	}
	return cloudLockPath(path, id, how)
}

func cloudLockPath(path, id string, how int) (*CloudLock, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(f.Fd()), how); err != nil {
		f.Close()
		if err == syscall.EWOULDBLOCK || err == syscall.EAGAIN {
			return nil, fmt.Errorf("%s: облачная работа уже идёт, закрытый материал нельзя приписать", id)
		}
		return nil, err
	}
	return &CloudLock{file: f}, nil
}

// File can be passed to a child to keep the reservation if the launcher dies.
func (l *CloudLock) File() *os.File { return l.file }

func (l *CloudLock) Close() error {
	err := syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
	closeErr := l.file.Close()
	if err != nil {
		return err
	}
	return closeErr
}
