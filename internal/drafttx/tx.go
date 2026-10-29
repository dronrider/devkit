// Package drafttx serializes draft transactions and restores durable after-images.
package drafttx

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
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
	file   *os.File
	dir    string
	common string
	root   string
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
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return nil, err
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	common := serviceDir(root)
	digest := sha256.Sum256([]byte(root))
	dir := filepath.Join(common, fmt.Sprintf("%x", digest))
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	if err := os.Chmod(dir, 0700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(common, "lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, err
	}
	l := &Lock{file: f, dir: dir, common: common, root: root}
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
func (l *Lock) install(p plan, pending string, afterEach func(int) error) error {
	if p.Version != 1 || p.Root != l.root {
		return fmt.Errorf("неверный план транзакции")
	}
	// Check the entire plan before installing any remaining image.
	seen := map[string]bool{}
	for _, c := range p.Changes {
		path, err := full(p.Root, c.Path)
		if err != nil {
			return err
		}
		if seen[path] {
			return fmt.Errorf("повтор пути %s", path)
		}
		seen[path] = true
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
	if err := os.Remove(pending); err != nil {
		return err
	}
	return syncDir(filepath.Dir(pending))
}

// Apply saves the plan durably, then installs its images. Caller holds the lock.
func (l *Lock) Apply(root string, changes []Change) error { return l.apply(root, changes, nil) }
func (l *Lock) apply(root string, changes []Change, afterEach func(int) error) error {
	if _, err := os.Stat(filepath.Join(l.dir, "pending.json")); err == nil {
		return fmt.Errorf("есть незавершённая транзакция; сначала восстановите её")
	} else if !os.IsNotExist(err) {
		return err
	}
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return err
	}
	if root != l.root {
		return fmt.Errorf("корень транзакции не совпадает с замком")
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
	return l.install(p, filepath.Join(l.dir, "pending.json"), afterEach)
}
func (l *Lock) Recover() error {
	legacy := filepath.Join(l.common, "pending.json")
	current := filepath.Join(l.dir, "pending.json")
	if _, err := os.Stat(legacy); err == nil {
		if _, currentErr := os.Stat(current); currentErr == nil {
			return fmt.Errorf("обнаружены два незавершённых плана: %s и %s", legacy, current)
		} else if !os.IsNotExist(currentErr) {
			return currentErr
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := l.recoverFile(legacy); err != nil {
		return err
	}
	return l.recoverFile(current)
}

func (l *Lock) recoverFile(path string) error {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var p plan
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err = dec.Decode(&p); err != nil {
		return fmt.Errorf("план %s: %w", path, err)
	}
	var extra any
	if dec.Decode(&extra) != io.EOF || p.Version != 1 || !filepath.IsAbs(p.Root) || len(p.Changes) == 0 {
		return fmt.Errorf("план %s: неверный формат", path)
	}
	canonical, err := filepath.EvalSymlinks(p.Root)
	if err != nil || canonical != l.root {
		return fmt.Errorf("план %s принадлежит корню %q: откройте этот корень для восстановления", path, p.Root)
	}
	p.Root = canonical
	return l.install(p, path, nil)
}
