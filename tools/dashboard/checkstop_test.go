package main

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"testing"

	"github.com/dronrider/devkit/internal/taskhead"
	"github.com/dronrider/devkit/internal/taskstate"
)

func TestCheckStopSupervisorHelper(t *testing.T) {
	if os.Getenv("CHECK_STOP_HELPER") == "" {
		return
	}
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGTERM)
	fmt.Println("ready")
	<-signals
	if os.Getenv("CHECK_STOP_IGNORE") != "" {
		select {}
	}
	taskhead.Release(taskhead.LockPath(os.Getenv("CHECK_STOP_HOME"), "XR-003"), os.Getpid())
	os.Exit(0)
}

func checkStopStand(t *testing.T, foreign bool) (*testEnv, *http.Client, *exec.Cmd) {
	t.Helper()
	e, c, _ := runsEnv(t, "")
	cmd := exec.Command(os.Args[0], "-test.run=^TestCheckStopSupervisorHelper$")
	cmd.Env = append(os.Environ(), "CHECK_STOP_HELPER=1", "CHECK_STOP_HOME="+e.home)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	var ready string
	if _, err := fmt.Fscanln(stdout, &ready); err != nil || ready != "ready" {
		t.Fatalf("supervisor: %q %v", ready, err)
	}
	t.Cleanup(func() {
		if cmd.ProcessState == nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			_ = cmd.Wait()
		}
	})
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
	if err := cmd.Wait(); err != nil {
		t.Fatalf("процесс не завершился: %v", err)
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

func TestRunStopCodexCheckerReportsUnreleasedLock(t *testing.T) {
	t.Setenv("CHECK_STOP_IGNORE", "1")
	old := checkStopTimeout
	checkStopTimeout = 0
	t.Cleanup(func() { checkStopTimeout = old })
	e, c, _ := checkStopStand(t, false)
	resp := doReq(t, c, "DELETE", e.srv.URL+"/api/projects/demo/runs/XR-003", "")
	text := body(t, resp)
	if resp.StatusCode != http.StatusServiceUnavailable || !strings.Contains(text, "не освободил замок") {
		t.Fatalf("стоп: %d %s", resp.StatusCode, text)
	}
}
