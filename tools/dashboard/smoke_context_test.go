package main

import (
	"bytes"
	"context"
	"fmt"
	"github.com/dronrider/devkit/internal/draftmeta"
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
	before, _ := filepath.Glob(filepath.Join(os.TempDir(), "devkit-cloud-start-*"))
	known := map[string]bool{}
	for _, dir := range before {
		known[dir] = true
	}
	var out bytes.Buffer
	if err := cmdSmoke(&out, true); err != nil {
		t.Fatalf("%v: %s", err, out.String())
	}
	after, _ := filepath.Glob(filepath.Join(os.TempDir(), "devkit-cloud-start-*"))
	for _, dir := range after {
		if !known[dir] {
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

func TestSmokeRemovedTmuxCleanupIsBounded(t *testing.T) {
	s, err := newSmoke(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ack := ""
	if err := startReserved(s.proj, s.proj, smokeTask, "synthetic", func(p string) string { ack = p; return "true" }); err != nil {
		s.close()
		t.Fatal(err)
	}
	os.Remove(filepath.Join(s.bin, "tmux"))
	before := time.Now()
	s.close()
	if time.Since(before) > 2*time.Second { // стенной порог: уборка должна завершиться до принудительного SIGKILL стенда
		t.Fatal("damaged fixture cleanup exceeded signal grace")
	}
	if _, err := os.Stat(filepath.Dir(ack)); !os.IsNotExist(err) {
		t.Fatalf("guardian state remains: %v", err)
	}
	lock, err := draftmeta.CloudExclusive(s.proj, smokeTask)
	if err != nil {
		t.Fatalf("dead synthetic launch retained reservation: %v", err)
	}
	lock.Close()
}

func TestSmokeCloseDrainsBlockedNewSessionDescendants(t *testing.T) {
	s, err := newSmoke(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.login(); err != nil {
		s.close()
		t.Fatal(err)
	}
	marker := filepath.Join(s.dir, "tmux-started")
	if err := smokeWrite(filepath.Join(s.bin, "tmux"), "#!/bin/sh\n"+`case "$1" in new-session) touch `+shQuote(marker)+`; trap '' TERM; sleep 30; exit 0;; ls|list-sessions) exit 0;; has-session) exit 1;; esac`, 0755); err != nil {
		s.close()
		t.Fatal(err)
	}
	request := make(chan error, 1)
	go func() {
		request <- s.call("POST", "/api/projects/demo/chats", fmt.Sprintf(`{"id":%q,"text":"synthetic"}`, smokeTask), 200, nil)
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		if time.Now().After(deadline) {
			s.close()
			t.Fatal("new-session not reached")
		}
		time.Sleep(10 * time.Millisecond)
	}
	before := time.Now()
	s.close()
	if time.Since(before) > 2*time.Second { // стенной порог: уборка должна завершиться до принудительного SIGKILL стенда
		t.Fatal("active startup did not drain within signal grace")
	}
	<-request
	lock, err := draftmeta.CloudExclusive(s.proj, smokeTask)
	if err != nil {
		t.Fatalf("descendant retained cloud FD: %v", err)
	}
	lock.Close()
}

func TestSmokeCloseWaitsForHandlerBeforeRestoringEnvironment(t *testing.T) {
	s, err := newSmoke(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.login(); err != nil {
		s.close()
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	startReservedFn = func(root, tree, id, sess string, command func(string) string) error {
		close(entered)
		<-release
		return fmt.Errorf("synthetic delayed start canceled")
	}
	request := make(chan error, 1)
	go func() {
		request <- s.call("POST", "/api/projects/demo/chats", fmt.Sprintf(`{"id":%q,"text":"synthetic"}`, smokeTask), 200, nil)
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		close(release)
		s.close()
		t.Fatal("handler not reached")
	}
	closed := make(chan struct{})
	go func() { s.close(); close(closed) }()
	select {
	case <-closed:
		close(release)
		t.Fatal("close returned with active handler")
	case <-time.After(3300 * time.Millisecond):
	}
	if os.Getenv("HOME") != s.home {
		t.Error("environment restored before handler drained")
	}
	close(release)
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("close did not drain canceled start")
	}
	<-request
}
