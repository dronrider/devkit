package drafttx

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestExternalBarrierAndRecoveryLease(t *testing.T) {
	root := t.TempDir()
	lock, err := Acquire(root)
	if err != nil {
		t.Fatal(err)
	}
	token, lease, err := lock.BeginExternal("catchup:test")
	if err != nil {
		lock.Close()
		t.Fatal(err)
	}
	if err := lock.Close(); err != nil {
		lease.Close()
		t.Fatal(err)
	}
	if l, err := Acquire(root); err == nil {
		l.Close()
		t.Fatal("writer passed active barrier")
	}
	if _, _, l, _, err := PendingExternal(root); err == nil {
		l.Close()
		t.Fatal("recovery passed active lease")
	}
	child := exec.Command("/bin/sh", "-c", "read x || true")
	child.ExtraFiles = []*os.File{lease}
	in, err := child.StdinPipe()
	if err != nil {
		lease.Close()
		t.Fatal(err)
	}
	if err := child.Start(); err != nil {
		lease.Close()
		t.Fatal(err)
	}
	lease.Close()
	if _, _, l, _, err := PendingExternal(root); err == nil {
		l.Close()
		child.Process.Kill()
		t.Fatal("child did not retain lease")
	}
	in.Close()
	if err := child.Wait(); err != nil {
		t.Fatal(err)
	}
	kind, got, recovered, exists, err := PendingExternal(root)
	if err != nil || !exists || kind != "catchup:test" || got != token {
		t.Fatalf("intent: %q %q %v %v", kind, got, exists, err)
	}
	defer recovered.Close()
	if err := FinishExternal(root, "wrong"); err == nil {
		t.Fatal("wrong token removed barrier")
	}
	if l, err := Acquire(root); err == nil {
		l.Close()
		t.Fatal("wrong token lost barrier")
	}
	if err := FinishExternal(root, token); err != nil {
		t.Fatal(err)
	}
	if l, err := Acquire(root); err != nil {
		t.Fatal(err)
	} else {
		l.Close()
	}
}

func TestExternalBarrierMalformedFailsClosed(t *testing.T) {
	root := t.TempDir()
	lock, err := Acquire(root)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(lock.dir, "external.json")
	if err := os.WriteFile(path, []byte("broken"), 0600); err != nil {
		lock.Close()
		t.Fatal(err)
	}
	lock.Close()
	if l, err := Acquire(root); err == nil {
		l.Close()
		t.Fatal("malformed barrier ignored")
	}
	if _, _, lease, _, err := PendingExternal(root); err == nil {
		if lease != nil {
			lease.Close()
		}
		t.Fatal("malformed barrier accepted")
	} else if !strings.Contains(err.Error(), "сохранён") {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "broken" {
		t.Fatalf("barrier changed: %q %v", data, err)
	}
}

func TestPublicationBarrierBlocksCheckoutUntilFinished(t *testing.T) {
	root := t.TempDir()
	lock, err := Acquire(root)
	if err != nil {
		t.Fatal(err)
	}
	token, err := lock.BeginPublication()
	if err != nil {
		lock.Close()
		t.Fatal(err)
	}
	lock.Close()
	lock, err = Acquire(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, lease, err := lock.BeginExternal("catchup:test"); err == nil {
		lease.Close()
		lock.Close()
		t.Fatal("checkout passed publication")
	}
	lock.Close()
	if err := FinishPublication(root, strings.Repeat("0", 32)); err == nil {
		t.Fatal("wrong publication token accepted")
	}
	if err := FinishPublication(root, token); err != nil {
		t.Fatal(err)
	}
	lock, err = Acquire(root)
	if err != nil {
		t.Fatal(err)
	}
	finish, lease, err := lock.BeginExternal("catchup:test")
	lock.Close()
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	if err := FinishExternal(root, finish); err != nil {
		t.Fatal(err)
	}
}
