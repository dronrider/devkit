package main

import (
	"fmt"
	"os"
	"os/exec"
	"reflect"

	"github.com/dronrider/devkit/internal/drafttx"
	"github.com/dronrider/devkit/internal/sessions"
	"github.com/dronrider/devkit/internal/stage"
	"github.com/dronrider/devkit/internal/taskhead"
)

// guardOutsideLock keeps cloud reservations while tmux is queried. Reacquire
// and compare all source material and the session registry before any writes.
func guardOutsideLock(root string, lock *drafttx.Lock, ids []string) (*drafttx.Lock, error) {
	before, err := collectDocs(root)
	if err != nil {
		lock.Close()
		return nil, err
	}
	registry, err := drafttx.Read(sessions.Path(stage.Home()))
	if err != nil {
		lock.Close()
		return nil, err
	}
	if err = lock.Close(); err != nil {
		return nil, err
	}
	for _, id := range ids {
		if err = lockedAttachGuard(id); err != nil {
			return nil, err
		}
	}
	lock, err = drafttx.Acquire(root)
	if err != nil {
		return nil, err
	}
	fail := func(err error) (*drafttx.Lock, error) { lock.Close(); return nil, err }
	after, err := collectDocs(root)
	if err != nil {
		return fail(err)
	}
	now, err := drafttx.Read(sessions.Path(stage.Home()))
	if err != nil {
		return fail(err)
	}
	if !reflect.DeepEqual(before, after) || !reflect.DeepEqual(registry, now) {
		return fail(fmt.Errorf("материал или реестр разговоров изменился при проверке; повторите операцию"))
	}
	for _, id := range ids {
		if _, err := os.Stat(taskhead.LockPath(stage.Home(), id)); err == nil {
			return fail(fmt.Errorf("%s: работа уже запущена, закрытый материал нельзя приписать", id))
		} else if !os.IsNotExist(err) {
			return fail(err)
		}
	}
	return lock, nil
}

// lockedAttachGuard rejects a target whose managed session may still be live.
func lockedAttachGuard(id string) error {
	home := stage.Home()
	if _, err := os.Stat(taskhead.LockPath(home, id)); err == nil {
		return fmt.Errorf("%s: работа уже запущена, закрытый материал нельзя приписать", id)
	} else if !os.IsNotExist(err) {
		return err
	}
	data, err := os.ReadFile(sessions.Path(home))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, records := range sessions.All(data) {
		b := sessions.Last(records)
		if b.Task != id || b.ReleasedAll {
			continue
		}
		if b.Tmux == "" {
			return fmt.Errorf("%s: состояние связанного разговора неизвестно, закрытый материал нельзя приписать", id)
		}
		bin, err := exec.LookPath("tmux")
		if err != nil {
			return fmt.Errorf("%s: состояние связанного разговора неизвестно: %w", id, err)
		}
		err = exec.Command(bin, "has-session", "-t", "="+b.Tmux).Run()
		if err == nil {
			return fmt.Errorf("%s: разговор ещё открыт, закрытый материал нельзя приписать", id)
		}
		if e, ok := err.(*exec.ExitError); !ok || e.ExitCode() != 1 {
			return fmt.Errorf("%s: состояние связанного разговора неизвестно: %v", id, err)
		}
	}
	return nil
}
