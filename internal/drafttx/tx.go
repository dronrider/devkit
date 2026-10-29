// Package drafttx serializes draft transactions and restores durable after-images.
package drafttx

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

type Image struct {
	Exists bool   `json:"exists"`
	Data   []byte `json:"data,omitempty"`
	Mode   uint32 `json:"mode,omitempty"`
}
type Change struct {
	Path   string `json:"path"`
	Before Image  `json:"before"`
	After  Image  `json:"after"`
}
type plan struct {
	Version int      `json:"version"`
	Root    string   `json:"root"`
	Changes []Change `json:"changes"`
}
type Lock struct {
	file *os.File
	dir  string
}

func serviceDir(root string) string {
	cmd := exec.Command("git", "-C", root, "rev-parse", "--git-common-dir")
	out, err := cmd.Output()
	if err == nil {
		dir := strings.TrimSpace(string(out))
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(root, dir)
		}
		return filepath.Join(dir, "devkit", "draft-tx")
	}
	return filepath.Join(root, ".devkit", "draft-tx")
}

// Acquire takes the common exclusive lock and recovers a pending transaction.
func Acquire(root string) (*Lock, error) {
	if root == "" {
		return nil, fmt.Errorf("пустой корень проекта")
	}
	info, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s не каталог", root)
	}
	dir := serviceDir(root)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	if err := os.Chmod(dir, 0700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, "lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, err
	}
	l := &Lock{file: f, dir: dir}
	if err = l.Recover(); err != nil {
		l.Close()
		return nil, err
	}
	return l, nil
}
func (l *Lock) Close() error {
	err := syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
	e := l.file.Close()
	if err != nil {
		return err
	}
	return e
}

func Read(path string) (Image, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return Image{}, nil
	}
	if err != nil {
		return Image{}, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return Image{}, err
	}
	return Image{Exists: true, Data: data, Mode: uint32(info.Mode().Perm())}, nil
}
func equal(a, b Image) bool {
	return a.Exists == b.Exists && (!a.Exists || (a.Mode == b.Mode && bytes.Equal(a.Data, b.Data)))
}
func syncDir(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}
func atomic(path string, image Image) error {
	dir := filepath.Dir(path)
	if !image.Exists {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return syncDir(dir)
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".draft-tx-")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if err = f.Chmod(os.FileMode(image.Mode)); err == nil {
		_, err = f.Write(image.Data)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err = os.Rename(name, path); err != nil {
		return err
	}
	return syncDir(dir)
}
func full(root, rel string) (string, error) {
	if filepath.IsAbs(rel) || rel == "" || filepath.Clean(rel) != rel || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("неверный путь транзакции %q", rel)
	}
	return filepath.Join(root, rel), nil
}
func (l *Lock) install(p plan, afterEach func(int) error) error {
	if p.Version != 1 || !filepath.IsAbs(p.Root) {
		return fmt.Errorf("неверный план транзакции")
	}
	// Check the entire plan before installing any remaining image.
	for _, c := range p.Changes {
		path, err := full(p.Root, c.Path)
		if err != nil {
			return err
		}
		current, err := Read(path)
		if err != nil {
			return err
		}
		if !equal(current, c.Before) && !equal(current, c.After) {
			return fmt.Errorf("конфликт восстановления %s: файл изменён", path)
		}
	}
	for i, c := range p.Changes {
		path, err := full(p.Root, c.Path)
		if err != nil {
			return err
		}
		current, err := Read(path)
		if err != nil {
			return err
		}
		if equal(current, c.After) {
			continue
		}
		if !equal(current, c.Before) {
			return fmt.Errorf("конфликт восстановления %s: файл изменён", path)
		}
		if err = atomic(path, c.After); err != nil {
			return err
		}
		if afterEach != nil {
			if err = afterEach(i); err != nil {
				return err
			}
		}
	}
	if err := os.Remove(filepath.Join(l.dir, "pending.json")); err != nil {
		return err
	}
	return syncDir(l.dir)
}

// Apply saves the plan durably, then installs its images. Caller holds the lock.
func (l *Lock) Apply(root string, changes []Change) error { return l.apply(root, changes, nil) }
func (l *Lock) apply(root string, changes []Change, afterEach func(int) error) error {
	if _, err := os.Stat(filepath.Join(l.dir, "pending.json")); err == nil {
		return fmt.Errorf("есть незавершённая транзакция; сначала восстановите её")
	} else if !os.IsNotExist(err) {
		return err
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, c := range changes {
		path, e := full(root, c.Path)
		if e != nil {
			return e
		}
		if seen[path] {
			return fmt.Errorf("повтор пути %s", path)
		}
		seen[path] = true
		current, e := Read(path)
		if e != nil {
			return e
		}
		if !equal(current, c.Before) {
			return fmt.Errorf("конфликт %s: файл изменён", path)
		}
	}
	p := plan{Version: 1, Root: root, Changes: changes}
	data, err := json.Marshal(p)
	if err != nil {
		return err
	}
	if err = atomic(filepath.Join(l.dir, "pending.json"), Image{Exists: true, Data: data, Mode: 0600}); err != nil {
		return err
	}
	return l.install(p, afterEach)
}
func (l *Lock) Recover() error {
	path := filepath.Join(l.dir, "pending.json")
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var p plan
	if err = json.Unmarshal(data, &p); err != nil {
		return fmt.Errorf("план %s: %w", path, err)
	}
	return l.install(p, nil)
}
