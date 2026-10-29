package draftmeta

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestCloudLockSurvivesLauncherDeath(t *testing.T) {
	if os.Getenv("CLOUD_LOCK_HELPER") == "1" {
		home := os.Getenv("CLOUD_TEST_HOME")
		lock, err := CloudShared(home, "XR-1")
		if err != nil {
			os.Exit(2)
		}
		child := exec.Command("/bin/sh", "-c", `touch "$1"; while [ ! -e "$2" ]; do sleep 0.05; done`, "sh", os.Getenv("CLOUD_STARTED"), os.Getenv("CLOUD_STOP"))
		child.ExtraFiles = []*os.File{lock.File()}
		if err = child.Start(); err != nil {
			os.Exit(3)
		}
		os.Exit(0)
	}
	home := t.TempDir()
	started, stop := filepath.Join(home, "started"), filepath.Join(home, "stop")
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	helper := exec.Command(exe, "-test.run=^TestCloudLockSurvivesLauncherDeath$")
	helper.Env = append(os.Environ(), "CLOUD_LOCK_HELPER=1", "CLOUD_TEST_HOME="+home, "CLOUD_STARTED="+started, "CLOUD_STOP="+stop)
	if out, err := helper.CombinedOutput(); err != nil {
		t.Fatalf("helper: %v %s", err, out)
	}
	t.Cleanup(func() { os.WriteFile(stop, []byte("stop"), 0600) })
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err = os.Stat(started); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("child did not start")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if l, err := CloudExclusive(home, "XR-1"); err == nil {
		l.Close()
		t.Fatal("reservation lost with launcher")
	}
	if err = os.WriteFile(stop, []byte("stop"), 0600); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(5 * time.Second)
	for {
		l, err := CloudExclusive(home, "XR-1")
		if err == nil {
			l.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("reservation survived client")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
