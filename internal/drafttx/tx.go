// Package drafttx serializes draft transactions and restores durable after-images.
package drafttx

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
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
	if err == nil && strings.TrimSpace(string(out)) != "" {
		dir := strings.TrimSpace(string(out))
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(root, dir)
		}
		return filepath.Join(dir, "devkit", "draft-tx")
	}
	return filepath.Join(root, ".devkit", "draft-tx")
}

// ServiceDir identifies the shared service directory of linked worktrees.
func ServiceDir(root string) (string, error) {
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	canonical, err = filepath.Abs(canonical)
	if err != nil {
		return "", err
	}
	return serviceDir(canonical), nil
}

// Acquire takes the common exclusive lock and recovers a pending transaction.
func Acquire(root string) (*Lock, error) {
	return acquire(root, false)
}

func acquire(root string, allowExternal bool) (*Lock, error) {
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
	_, markerErr := os.Lstat(filepath.Join(dir, "external.json"))
	if markerErr == nil {
		if !allowExternal {
			l.Close()
			return nil, fmt.Errorf("%s: внешнее обновление дерева не завершено; проверьте состояние checkout и журнал %s", root, filepath.Join(dir, "external.json"))
		}
		return l, nil
	} else if !os.IsNotExist(markerErr) {
		l.Close()
		return nil, markerErr
	}
	if err = l.Recover(); err != nil {
		l.Close()
		return nil, err
	}
	return l, nil
}

type externalIntent struct {
	Version int    `json:"version"`
	Root    string `json:"root"`
	Kind    string `json:"kind"`
	Token   string `json:"token"`
	Owner   int    `json:"owner"`
}

// BeginExternal installs a durable, root-bound barrier while the caller holds
// the common lock. Other managed operations fail closed instead of waiting on
// an external process which might itself invoke taskctl.
func (l *Lock) BeginExternal(kind string) (string, *os.File, error) {
	if kind == "" {
		return "", nil, fmt.Errorf("не названо внешнее обновление")
	}
	entries, err := os.ReadDir(filepath.Join(l.dir, "publications"))
	if err != nil && !os.IsNotExist(err) {
		return "", nil, err
	}
	if len(entries) != 0 {
		return "", nil, fmt.Errorf("публикация Git не завершена; checkout остановлен, журналы %s", filepath.Join(l.dir, "publications"))
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", nil, err
	}
	token := fmt.Sprintf("%x", nonce)
	data, err := json.Marshal(externalIntent{Version: 1, Root: l.root, Kind: kind, Token: token, Owner: os.Getpid()})
	if err != nil {
		return "", nil, err
	}
	path := filepath.Join(l.dir, "external.json")
	if _, err := os.Lstat(path); err == nil {
		return "", nil, fmt.Errorf("внешнее обновление уже зарегистрировано: %s", path)
	} else if !os.IsNotExist(err) {
		return "", nil, err
	}
	lease, err := os.OpenFile(filepath.Join(l.dir, "external.lease"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return "", nil, err
	}
	if err := syscall.Flock(int(lease.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		lease.Close()
		return "", nil, err
	}
	if err := atomic(path, Image{Exists: true, Mode: 0600, Data: data}); err != nil {
		lease.Close()
		return "", nil, err
	}
	return token, lease, nil
}

// BeginPublication records an outstanding Git index/publication action before
// releasing the board lock. A crash leaves evidence that blocks checkout.
func (l *Lock) BeginPublication() (string, error) {
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", err
	}
	token := fmt.Sprintf("%x", nonce)
	data, err := json.Marshal(externalIntent{Version: 1, Root: l.root, Kind: "publication", Token: token, Owner: os.Getpid()})
	if err != nil {
		return "", err
	}
	dir := filepath.Join(l.dir, "publications")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	if err := syncDir(l.dir); err != nil {
		return "", err
	}
	if err := atomic(filepath.Join(dir, token+".json"), Image{Exists: true, Mode: 0600, Data: data}); err != nil {
		return "", err
	}
	return token, nil
}

func FinishPublication(root, token string) error {
	if len(token) != 32 {
		return fmt.Errorf("неверный ключ публикации")
	}
	if _, err := hex.DecodeString(token); err != nil {
		return fmt.Errorf("неверный ключ публикации")
	}
	l, err := Acquire(root)
	if err != nil {
		return err
	}
	defer l.Close()
	dir := filepath.Join(l.dir, "publications")
	path := filepath.Join(dir, token+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var intent externalIntent
	if json.Unmarshal(data, &intent) != nil || intent.Version != 1 || intent.Root != l.root || intent.Kind != "publication" || intent.Token != token {
		return fmt.Errorf("неверный журнал публикации %s; он сохранён", path)
	}
	if err := os.Remove(path); err != nil {
		return err
	}
	return syncDir(dir)
}

// FinishExternal removes only the caller's barrier after a verified outcome.
func FinishExternal(root, token string) error {
	l, err := acquire(root, true)
	if err != nil {
		return err
	}
	defer l.Close()
	path := filepath.Join(l.dir, "external.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var intent externalIntent
	if json.Unmarshal(data, &intent) != nil || intent.Version != 1 || intent.Root != l.root || intent.Token == "" || intent.Token != token {
		return fmt.Errorf("неверный журнал внешнего обновления %s; он сохранён", path)
	}
	if err := os.Remove(path); err != nil {
		return err
	}
	return syncDir(l.dir)
}

// PendingExternal returns the root-bound intent without clearing it.
func PendingExternal(root string) (kind, token string, lease *os.File, exists bool, err error) {
	l, err := acquire(root, true)
	if err != nil {
		return "", "", nil, false, err
	}
	defer l.Close()
	path := filepath.Join(l.dir, "external.json")
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "", "", nil, false, nil
	}
	if err != nil {
		return "", "", nil, false, err
	}
	var intent externalIntent
	if json.Unmarshal(data, &intent) != nil || intent.Version != 1 || intent.Root != l.root || intent.Kind == "" || intent.Token == "" || intent.Owner <= 0 {
		return "", "", nil, false, fmt.Errorf("неверный журнал внешнего обновления %s; он сохранён", path)
	}
	lease, err = os.OpenFile(filepath.Join(l.dir, "external.lease"), os.O_RDWR, 0600)
	if err != nil {
		return "", "", nil, false, err
	}
	if err := syscall.Flock(int(lease.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		lease.Close()
		return "", "", nil, false, fmt.Errorf("внешнее обновление ещё выполняется: %w", err)
	}
	return intent.Kind, intent.Token, lease, true, nil
}

// ServiceDir returns the common service directory protected by this lock.
func (l *Lock) ServiceDir() string { return l.common }

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
