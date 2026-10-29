package main

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/dronrider/devkit/internal/sessions"
	"github.com/dronrider/devkit/internal/stage"
	"github.com/dronrider/devkit/internal/taskhead"
)

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
