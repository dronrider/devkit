package main

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/dronrider/devkit/internal/taskhead"
)

func TestLockedAttachRejectsLiveTargetBeforeWrite(t *testing.T) {
	root := setup(t)
	if _, err := cmdDraft(root, "частный материал", "mid", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	source := draftPath(root, "XR-008")
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	data = []byte(strings.Replace(string(data), "## ", "замок: locked\n\n## ", 1))
	if err = os.WriteFile(source, data, 0644); err != nil {
		t.Fatal(err)
	}
	target := taskFileAbs(root, "XR-001")
	before, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	lock := taskhead.LockPath(os.Getenv("HOME"), "XR-001")
	if err = os.MkdirAll(filepath.Dir(lock), 0755); err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(lock, 0755); err != nil {
		t.Fatal(err)
	}
	if _, err = cmdDraftAttach(root, "XR-008", "XR-001", CommitOpts{}); err == nil {
		t.Fatal("locked attach to running target")
	}
	after, err := os.ReadFile(target)
	if err != nil || string(after) != string(before) {
		t.Fatalf("target changed: %v", err)
	}
	if _, err = os.Stat(source); err != nil {
		t.Fatalf("source consumed: %v", err)
	}
	if err = os.Remove(lock); err != nil {
		t.Fatal(err)
	}
	if _, err = cmdDraftAttach(root, "XR-008", "XR-001", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	after, err = os.ReadFile(target)
	if err != nil || !strings.Contains(string(after), "защита: locked") {
		t.Fatalf("target not protected: %v", err)
	}
}

func TestLockedAttachRejectsActiveCloudReservation(t *testing.T) {
	root := setup(t)
	if _, err := cmdDraft(root, "частный материал", "mid", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdDraftMeta(root, "XR-008", "", "", "locked", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	source, target := draftPath(root, "XR-008"), taskFileAbs(root, "XR-001")
	beforeSource, _ := os.ReadFile(source)
	beforeTarget, _ := os.ReadFile(target)
	active, err := testCloudShared(root, "XR-001")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = cmdDraftAttach(root, "XR-008", "XR-001", CommitOpts{}); err == nil {
		active.Close()
		t.Fatal("active cloud accepted private material")
	}
	a, _ := os.ReadFile(source)
	b, _ := os.ReadFile(target)
	if string(a) != string(beforeSource) || string(b) != string(beforeTarget) {
		active.Close()
		t.Fatal("partial write")
	}
	if err = active.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = cmdDraftAttach(root, "XR-008", "XR-001", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
}

func testCloudShared(root, id string) (*os.File, error) {
	path := filepath.Join(root, ".devkit", "draft-tx", "cloud-locks", id)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_SH); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}
