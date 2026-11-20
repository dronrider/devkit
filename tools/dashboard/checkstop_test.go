package main

import (
	"fmt"
	"net/http"
	"os/exec"
	"strings"
	"syscall"
	"testing"

	"github.com/dronrider/devkit/internal/taskhead"
	"github.com/dronrider/devkit/internal/taskstate"
)

func checkStopStand(t *testing.T, foreign bool) (*testEnv, *http.Client, *exec.Cmd) {
	t.Helper()
	e, c, _ := runsEnv(t, "")
	cmd := exec.Command("/bin/sleep", "300")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); _ = cmd.Wait() })
	pid := cmd.Process.Pid
	if err := taskhead.Take(taskhead.LockPath(e.home, "XR-003"), pid); err != nil {
		t.Fatal(err)
	}
	g, err := taskstate.Acquire(e.home, e.proj, "XR-003")
	if err != nil {
		t.Fatal(err)
	}
	if err := g.Start(fmt.Sprintf("check-%d-1", pid), "codex", "model", ""); err != nil {
		t.Fatal(err)
	}
	g.State.Run.PID, g.State.Run.Group = pid, pid
	if foreign {
		g.State.Run.Group = pid + 1
	}
	if err := g.Save(); err != nil {
		t.Fatal(err)
	}
	_ = g.Close()
	return e, c, cmd
}

func TestRunStopCodexChecker(t *testing.T) {
	e, c, cmd := checkStopStand(t, false)
	resp := doReq(t, c, "DELETE", e.srv.URL+"/api/projects/demo/runs/XR-003", "")
	text := body(t, resp)
	if resp.StatusCode != http.StatusOK || !strings.Contains(text, `"state":"стоп"`) {
		t.Fatalf("стоп: %d %s", resp.StatusCode, text)
	}
	if err := cmd.Wait(); err == nil {
		t.Fatal("процесс не остановлен")
	}
}

func TestRunStopCodexCheckerRejectsForeignGroup(t *testing.T) {
	e, c, cmd := checkStopStand(t, true)
	resp := doReq(t, c, "DELETE", e.srv.URL+"/api/projects/demo/runs/XR-003", "")
	_ = body(t, resp)
	if resp.StatusCode == http.StatusOK {
		t.Fatal("чужая группа принята за проверяющего")
	}
	if err := syscall.Kill(cmd.Process.Pid, 0); err != nil {
		t.Fatalf("чужой процесс остановлен: %v", err)
	}
}
