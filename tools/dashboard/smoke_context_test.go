package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSmokeCanceledAndTimedOutCleanup(t *testing.T) {
	for _, timeout := range []bool{false, true} {
		t.Run(map[bool]string{false: "cancel", true: "timeout"}[timeout], func(t *testing.T) {
			s, err := newSmoke(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			ack := ""
			if err := startReserved(s.proj, s.proj, smokeTask, "synthetic", func(p string) string { ack = p; return "true" }); err != nil {
				s.close()
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			if timeout {
				ctx, cancel = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
			}
			cancel()
			s.ctx = ctx
			err = s.call("GET", "/", "", 200, nil)
			s.close()
			if err == nil {
				t.Fatal("canceled request succeeded")
			}
			if _, err := os.Stat(filepath.Dir(ack)); !os.IsNotExist(err) {
				t.Fatalf("guardian remains after cancellation: %v", err)
			}
		})
	}
}

func TestSmokeKeepHasNoGuardians(t *testing.T) {
	var dirsStarted []string
	oldStart := startReservedFn
	startReservedFn = func(root, tree, id, sess string, command func(string) string) error {
		return startReserved(root, tree, id, sess, func(ack string) string {
			dirsStarted = append(dirsStarted, filepath.Dir(ack))
			return command(ack)
		})
	}
	defer func() { startReservedFn = oldStart }()
	var out bytes.Buffer
	if err := cmdSmoke(&out, true); err != nil {
		t.Fatalf("%v: %s", err, out.String())
	}
	for _, dir := range dirsStarted {
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Errorf("--keep retained guardian state: %s", dir)
		}
	}
	dirs := smokeDirs(t)
	// The normal smoke test checks removal; --keep must preserve its materials.
	found := false
	for dir := range dirs {
		if bytes.Contains(out.Bytes(), []byte(dir)) {
			found = true
			os.RemoveAll(dir)
		}
	}
	if !found {
		t.Fatal("--keep did not preserve materials")
	}
}
