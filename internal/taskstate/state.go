package taskstate

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"
)

type Run struct {
	ID      string `json:"id"`
	Harness string `json:"harness"`
	Model   string `json:"model"`
	PID     int    `json:"pid"`
	Group   int    `json:"group"`
	Started string `json:"started"`
	Attempt string `json:"attempt,omitempty"`
}

type State struct {
	Version int    `json:"version"`
	Root    string `json:"root"`
	Task    string `json:"task"`
	Stopped bool   `json:"stopped"`
	Run     *Run   `json:"run,omitempty"`
}

type Guard struct {
	State State
	path  string
	lock  *os.File
}

var taskID = regexp.MustCompile(`^[A-Z][A-Z0-9]*-[0-9]+$`)

func Path(home, root, task string) (string, string, error) {
	task = strings.ToUpper(strings.TrimSpace(task))
	if home == "" || !taskID.MatchString(task) {
		return "", "", fmt.Errorf("нет дома либо неверный ID задачи")
	}
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", "", err
	}
	canonical, err = filepath.Abs(canonical)
	if err != nil {
		return "", "", err
	}
	sum := sha256.Sum256([]byte(canonical))
	return filepath.Join(home, ".devkit", "task-state", hex.EncodeToString(sum[:]), task+".json"), canonical, nil
}

// Acquire держит общий для остановки и нового запуска замок вне task-*.lock.
func Acquire(home, root, task string) (*Guard, error) {
	return acquire(home, root, task, 30*time.Second)
}

func acquire(home, root, task string, wait time.Duration) (*Guard, error) {
	path, canonical, err := Path(home, root, task)
	if err != nil {
		return nil, err
	}
	if err = mkdirDurable(filepath.Dir(path)); err != nil {
		return nil, err
	}
	fd, err := syscall.Open(path+".lock", syscall.O_CREAT|syscall.O_RDWR|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0600)
	if err != nil {
		return nil, err
	}
	lock := os.NewFile(uintptr(fd), path+".lock")
	deadline := time.Now().Add(wait)
	for {
		err = syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
			lock.Close()
			return nil, err
		}
		if !time.Now().Before(deadline) {
			lock.Close()
			return nil, fmt.Errorf("замок состояния задачи занят")
		}
		time.Sleep(10 * time.Millisecond)
	}

	g := &Guard{path: path, lock: lock, State: State{Version: 1, Root: canonical, Task: strings.ToUpper(strings.TrimSpace(task))}}
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if errors.Is(err, os.ErrNotExist) {
		return g, nil
	}
	if err != nil {
		g.Close()
		return nil, err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, 65537))
	if err != nil || len(raw) > 65536 {
		g.Close()
		return nil, fmt.Errorf("состояние не прочитано либо слишком велико")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var state State
	if err = dec.Decode(&state); err == nil {
		var extra any
		if dec.Decode(&extra) != io.EOF {
			err = fmt.Errorf("лишние данные состояния")
		}
	}
	if err == nil && (state.Version != 1 || state.Root != canonical || state.Task != g.State.Task) {
		err = fmt.Errorf("чужое либо неизвестное состояние задачи")
	}
	if err != nil {
		g.Close()
		return nil, fmt.Errorf("состояние задачи не прочитано: %w", err)
	}
	var required map[string]json.RawMessage
	if err = json.Unmarshal(raw, &required); err != nil || len(required["stopped"]) == 0 || string(required["stopped"]) == "null" {
		g.Close()
		return nil, fmt.Errorf("нет явного признака остановки")
	}
	g.State = state
	return g, nil
}

func (g *Guard) Close() error {
	if g.lock == nil {
		return nil
	}
	f := g.lock
	g.lock = nil
	return f.Close()
}

func (g *Guard) Save() error {
	if g.lock == nil {
		return fmt.Errorf("замок состояния уже закрыт")
	}
	data, err := json.Marshal(g.State)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(g.path), ".state-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if _, err = f.Write(append(data, '\n')); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Rename(name, g.path); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(g.path))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func SetStopped(home, root, task string, stopped bool) error {
	g, err := Acquire(home, root, task)
	if err != nil {
		return err
	}
	defer g.Close()
	if g.State.Stopped && !stopped {
		g.State.Run = nil
	}
	g.State.Stopped = stopped
	return g.Save()
}

func (g *Guard) Start(id, harness, model, attempt string) error {
	if g.State.Stopped {
		return fmt.Errorf("задача остановлена пользователем; требуется явное продолжение")
	}
	g.State.Run = &Run{ID: id, Harness: harness, Model: model, Attempt: attempt, Started: time.Now().UTC().Format(time.RFC3339Nano)}
	return g.Save()
}

func Invalidate(home, root, task string) error {
	g, err := Acquire(home, root, task)
	if err != nil {
		return err
	}
	defer g.Close()
	g.State.Run = nil
	return g.Save()
}

func mkdirDurable(path string) error {
	if info, err := os.Stat(path); err == nil {
		if !info.IsDir() {
			return fmt.Errorf("%s не каталог", path)
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	parent := filepath.Dir(path)
	if err := mkdirDurable(parent); err != nil {
		return err
	}
	if err := os.Mkdir(path, 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	dir, err := os.Open(parent)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
