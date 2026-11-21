package main

import (
	"fmt"
	"strings"
	"syscall"
	"time"

	"github.com/dronrider/devkit/internal/checkrun"
	"github.com/dronrider/devkit/internal/taskhead"
	"github.com/dronrider/devkit/internal/taskstate"
)

var checkStopTimeout = 5 * time.Second

func stopCodexChecker(home, root, id string) (bool, error) {
	g, err := taskstate.Acquire(home, root, id)
	if err != nil {
		return false, err
	}
	defer g.Close()
	run := g.State.Run
	if !g.State.Stopped || run == nil || run.PID <= 0 || run.Group != run.PID ||
		!strings.HasPrefix(run.ID, fmt.Sprintf("check-%d-", run.PID)) || run.Harness != checkrun.CodexHarness {
		return false, nil
	}
	lock := taskhead.LockPath(home, id)
	if taskhead.Owner(lock) != run.PID || !taskhead.Working(lock) {
		return false, nil
	}
	group, err := syscall.Getpgid(run.PID)
	if err == syscall.ESRCH {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("группа процесса проверяющего %d: %w", run.PID, err)
	}
	if group != run.Group {
		return false, nil
	}
	if err := syscall.Kill(run.PID, syscall.SIGTERM); err != nil {
		return false, err
	}
	_ = g.Close()
	deadline := time.Now().Add(checkStopTimeout)
	for taskhead.Owner(lock) == run.PID {
		if time.Now().After(deadline) {
			return false, fmt.Errorf("процесс проверки не освободил замок")
		}
		time.Sleep(25 * time.Millisecond)
	}
	return true, nil
}
