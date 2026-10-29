package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dronrider/devkit/internal/draftmeta"
)

func TestExecCloudAckTransfersReservation(t *testing.T) {
	kit := execKit(t)
	root := t.TempDir()
	t.Chdir(root)
	t.Setenv("DEVKIT_TASK", "XR-1")
	path := filepath.Join(root, "docs", "tasks", "XR-1.md")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("# XR-1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	ack, started, stop, dump := filepath.Join(root, "ack"), filepath.Join(root, "started"), filepath.Join(root, "stop"), filepath.Join(root, "env")
	t.Setenv("DEVKIT_CLOUD_ACK", ack)
	done := make(chan error, 1)
	go func() {
		var out, errout bytes.Buffer
		_, err := cmdExec(kit, "secondcli", []string{"/bin/sh", "-c", `env > "$3"; touch "$1"; while [ ! -e "$2" ]; do sleep 0.05; done`, "sh", started, stop, dump}, &out, &errout)
		done <- err
	}()
	t.Cleanup(func() { os.WriteFile(stop, []byte("stop"), 0600) })
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(started); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("client did not start")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, err := os.Stat(ack); err != nil {
		t.Fatal("missing ack", err)
	}
	env, err := os.ReadFile(dump)
	if err != nil || strings.Contains(string(env), "DEVKIT_CLOUD_ACK=") {
		t.Fatal("ack leaked into client", err)
	}
	if lock, err := draftmeta.CloudExclusive(root, "XR-1"); err == nil {
		lock.Close()
		t.Fatal("private attach entered")
	}
	os.WriteFile(stop, []byte("stop"), 0600)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
