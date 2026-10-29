package main

import (
	"testing"
	"time"

	"github.com/dronrider/devkit/internal/drafttx"
)

func TestWakeWaitsForTransactionBeforeReading(t *testing.T) {
	root := setup(t)
	lock, err := drafttx.Acquire(root)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, _, err := cmdWake(root, []string{"XR-404"}, wakeOpts{}); done <- err }()
	select {
	case err := <-done:
		lock.Close()
		t.Fatalf("wake ran during transaction: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	if err = lock.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("wake stayed blocked")
	}
}
