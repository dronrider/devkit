package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"syscall"

	"github.com/dronrider/devkit/internal/drafttx"
	"github.com/dronrider/devkit/internal/stage"
)

// reviewBasis protects the source and configuration across remote calls.
type reviewBasis struct {
	root, canonical string
	docs            map[string]drafttx.Image
	conf            drafttx.Image
}

func openRemoteReview(root, id string) (reviewConf, *reviewDraft, *reviewBasis, error) {
	var conf reviewConf
	lock, err := drafttx.Acquire(root)
	if err != nil {
		return conf, nil, nil, err
	}
	defer lock.Close()
	b := &reviewBasis{root: root}
	if b.canonical, err = filepath.EvalSymlinks(root); err != nil {
		return conf, nil, nil, err
	}
	if b.docs, err = collectDocs(root); err != nil {
		return conf, nil, nil, err
	}
	if b.conf, err = drafttx.Read(filepath.Join(root, filepath.FromSlash(reviewConfRel))); err != nil {
		return conf, nil, nil, err
	}
	if conf, err = loadReviewConf(root); err != nil {
		return conf, nil, nil, err
	}
	d, err := openReviewDraft(root, id, false)
	return conf, d, b, err
}

func (b *reviewBasis) save(d *reviewDraft) error {
	lock, err := drafttx.Acquire(b.root)
	if err != nil {
		return err
	}
	defer lock.Close()
	canonical, err := filepath.EvalSymlinks(b.root)
	if err != nil || canonical != b.canonical {
		return fmt.Errorf("путь проекта изменился; сохранение ревью остановлено")
	}
	docs, err := collectDocs(b.root)
	if err != nil {
		return err
	}
	conf, err := drafttx.Read(filepath.Join(b.root, filepath.FromSlash(reviewConfRel)))
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(docs, b.docs) || !reflect.DeepEqual(conf, b.conf) {
		return fmt.Errorf("материал ревью или настройки изменились; сохранение остановлено")
	}
	if err := d.save(); err != nil {
		return err
	}
	b.docs, err = collectDocs(b.root)
	return err
}

func (b *reviewBasis) check() error {
	lock, err := drafttx.Acquire(b.root)
	if err != nil {
		return err
	}
	defer lock.Close()
	canonical, err := filepath.EvalSymlinks(b.root)
	if err != nil || canonical != b.canonical {
		return fmt.Errorf("путь проекта изменился; отправка ревью остановлена")
	}
	docs, err := collectDocs(b.root)
	if err != nil {
		return err
	}
	conf, err := drafttx.Read(filepath.Join(b.root, filepath.FromSlash(reviewConfRel)))
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(docs, b.docs) || !reflect.DeepEqual(conf, b.conf) {
		return fmt.Errorf("материал ревью или настройки изменились; отправка остановлена")
	}
	return nil
}

func (b *reviewBasis) apply(change func() error) (string, error) {
	if cliTx != nil {
		return "", fmt.Errorf("ревью нельзя применять внутри другой транзакции")
	}
	cliWorktreeRoot = b.root
	cliIsLinkedWorktree = linkedWorktree(b.root)
	cliMainRoot = stage.MainRoot(b.root)
	lock, err := drafttx.Acquire(b.root)
	if err != nil {
		return "", err
	}
	canonical, err := filepath.EvalSymlinks(b.root)
	if err != nil || canonical != b.canonical {
		lock.Close()
		return "", fmt.Errorf("путь проекта изменился")
	}
	docs, err := collectDocs(b.root)
	if err != nil {
		lock.Close()
		return "", err
	}
	conf, err := drafttx.Read(filepath.Join(b.root, filepath.FromSlash(reviewConfRel)))
	if err != nil {
		lock.Close()
		return "", err
	}
	if !reflect.DeepEqual(docs, b.docs) || !reflect.DeepEqual(conf, b.conf) {
		lock.Close()
		return "", fmt.Errorf("материал ревью или настройки изменились; применение остановлено")
	}
	cliTx = lock
	err = change()
	if err != nil && cliTx != nil {
		if after, readErr := collectDocs(b.root); readErr == nil && !reflect.DeepEqual(after, b.docs) {
			err = fmt.Errorf("часть изменений сохранена: %w", err)
		}
	}
	tail, finishErr := cliFinishDraftLock(err == nil)
	if err != nil && finishErr != nil {
		return tail, fmt.Errorf("%v; %w", err, finishErr)
	}
	if err != nil {
		return tail, err
	}
	return tail, finishErr
}

type reviewSendReceipt struct {
	State string `json:"state"`
	Reply []byte `json:"reply,omitempty"`
	Basis string `json:"basis,omitempty"`
}

func reviewPollLock(root, id string) (*os.File, error) {
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, err
	}
	service, err := drafttx.ServiceDir(root)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(service, 0700); err != nil {
		return nil, err
	}
	key := sha256.Sum256([]byte(canonical + "\x00" + id))
	f, err := os.OpenFile(filepath.Join(service, fmt.Sprintf("poll-%x.lock", key)), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("опрос %s уже идёт: %w", id, err)
	}
	return f, nil
}

// reviewSendOnce persists an intent before a mutating tracker call. An
// interrupted or ambiguous call remains pending and cannot be sent again.
func reviewSendOnce(root, id, operation, basis, script string) ([]byte, error) {
	return reviewOnce(root, id, operation, basis, func() ([]byte, error) { return runPublish(script) })
}

func reviewOnce(root, id, operation, basis string, send func() ([]byte, error)) ([]byte, error) {
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, err
	}
	service, err := drafttx.ServiceDir(canonical)
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(service, "review-sends")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	// Persist each newly-created directory entry before the remote action.
	for _, parent := range []string{filepath.Dir(filepath.Dir(service)), filepath.Dir(service), service} {
		if err := syncReviewDir(parent); err != nil {
			return nil, err
		}
	}
	key := sha256.Sum256([]byte(canonical + "\x00" + id + "\x00" + operation))
	path := filepath.Join(dir, fmt.Sprintf("%x.json", key))
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if os.IsExist(err) {
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil, readErr
		}
		var receipt reviewSendReceipt
		if json.Unmarshal(data, &receipt) != nil || receipt.State != "done" {
			return nil, fmt.Errorf("предыдущая отправка не подтверждена (%s); повтор остановлен", path)
		}
		if receipt.Basis != basis {
			return nil, fmt.Errorf("данные изменились после отправки; повтор и сохранение остановлены")
		}
		return receipt.Reply, nil
	}
	if err != nil {
		return nil, err
	}
	_, err = f.Write([]byte(`{"state":"pending"}`))
	if err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return nil, err
	}
	if err := syncReviewDir(dir); err != nil {
		return nil, err
	}
	out, err := send()
	if err != nil {
		return nil, fmt.Errorf("исход отправки не подтверждён, повтор остановлен: %w", err)
	}
	data, err := json.Marshal(reviewSendReceipt{State: "done", Reply: out, Basis: basis})
	if err != nil {
		return nil, err
	}
	tmp, err := os.CreateTemp(dir, ".receipt-")
	if err != nil {
		return nil, fmt.Errorf("внешний результат получен, квитанция не записана: %w", err)
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err = tmp.Chmod(0600); err == nil {
		_, err = tmp.Write(data)
	}
	if err == nil {
		err = tmp.Sync()
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(name, path)
	}
	if err == nil {
		err = syncReviewDir(dir)
	}
	if err != nil {
		return nil, fmt.Errorf("внешний результат получен, квитанция не записана: %w", err)
	}
	return out, nil
}

func syncReviewDir(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}
