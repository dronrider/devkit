package draftmeta

import (
	"testing"
	"time"
)

func TestCloudLocksSerializeLaunchAndPrivateAttach(t *testing.T) {
	home := t.TempDir()
	shared, err := CloudShared(home, "XR-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = CloudExclusive(home, "XR-1"); err == nil {
		t.Fatal("attachment entered while client active")
	}
	if err = shared.Close(); err != nil {
		t.Fatal(err)
	}
	exclusive, err := CloudExclusive(home, "XR-1")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		l, err := CloudShared(home, "XR-1")
		if err == nil {
			err = l.Close()
		}
		done <- err
	}()
	select {
	case err := <-done:
		exclusive.Close()
		t.Fatalf("launch entered early: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	if err = exclusive.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("launch stuck")
	}
}
