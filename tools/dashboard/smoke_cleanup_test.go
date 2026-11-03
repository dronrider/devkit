package main

import (
	"bytes"
	"fmt"
	"github.com/dronrider/devkit/internal/draftmeta"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSmokeCloseKillsAllGroupsBeforeCheckingSharedReservation(t *testing.T) {
	s, err := newSmoke(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(s.dir, "started")
	body := `case "$1" in new-session) trap '' TERM; echo started >> ` + shQuote(marker) + `; sleep 30;; *) exit 1;; esac`
	if err := smokeWrite(filepath.Join(s.bin, "tmux"), "#!/bin/sh\n"+body, 0755); err != nil {
		s.close()
		t.Fatal(err)
	}
	launch := startReservedFn
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func(i int) {
			results <- launch(s.proj, s.proj, smokeTask, fmt.Sprintf("synthetic-%d", i), func(string) string { return "true" })
		}(i)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		raw, _ := os.ReadFile(marker)
		if bytes.Count(raw, []byte("started\n")) == 2 {
			break
		}
		if time.Now().After(deadline) {
			s.close()
			t.Fatal("two launches did not reach fake tmux")
		}
		time.Sleep(10 * time.Millisecond)
	}
	err = s.close()
	<-results
	<-results
	if err != nil {
		t.Fatalf("own groups sharing a reservation did not drain: %v", err)
	}
	lock, err := draftmeta.CloudExclusive(s.proj, smokeTask)
	if err != nil {
		t.Fatalf("shared reservation remains: %v", err)
	}
	lock.Close()
}
