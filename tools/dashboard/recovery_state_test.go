package main

import (
	"github.com/dronrider/devkit/internal/taskstate"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRecoveryStopWriteFailureDoesNotKill(t *testing.T) {
	e, c, log := runsEnv(t, "task-XR-004\t1\t1786000000\n")
	if err := os.WriteFile(filepath.Join(e.home, ".devkit", "task-state"), []byte("blocked"), 0600); err != nil {
		t.Fatal(err)
	}
	response := doReq(t, c, "DELETE", e.srv.URL+"/api/projects/demo/runs/XR-004", "")
	text := body(t, response)
	if response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("%d %s", response.StatusCode, text)
	}
	data, _ := os.ReadFile(log)
	if strings.Contains(string(data), "kill-session") {
		t.Fatal("процесс снят до сохранения остановки")
	}
}

func TestRecoveryManualRunClearsStop(t *testing.T) {
	e, c, _ := runsEnv(t, "")
	if err := taskstate.SetStopped(e.home, e.proj, "XR-004", true); err != nil {
		t.Fatal(err)
	}
	response := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/runs", `{"id":"XR-004"}`)
	text := body(t, response)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("%d %s", response.StatusCode, text)
	}
	g, err := taskstate.Acquire(e.home, e.proj, "XR-004")
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	if g.State.Stopped {
		t.Fatal("явный запуск не снял остановку")
	}
}

func TestRecoveryStopWithoutTmuxStillInhibits(t *testing.T) {
	e, c, _ := runsEnv(t, "")
	if err := os.Remove(filepath.Join(e.bin, "tmux")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", e.bin)
	response := doReq(t, c, "DELETE", e.srv.URL+"/api/projects/demo/runs/XR-004", "")
	response.Body.Close()
	if response.StatusCode != http.StatusBadGateway {
		t.Fatal(response.StatusCode)
	}
	g, err := taskstate.Acquire(e.home, e.proj, "XR-004")
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	if !g.State.Stopped {
		t.Fatal("намерение остановки потеряно после отказа tmux")
	}
}
