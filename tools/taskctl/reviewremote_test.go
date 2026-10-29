package main

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dronrider/devkit/internal/drafttx"
)

func TestReviewPublishKeepsConcurrentEditAndReplaysReceipt(t *testing.T) {
	root := setupDraft(t)
	writeReviewConf(t, root, "publish = auto\npause = 0\n")
	if _, err := cmdReviewDraft(root, "XR-005", "одно замечание", reviewDraftParams{}, CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	old := runPublish
	t.Cleanup(func() { runPublish = old })
	calls := 0
	runPublish = func(string) ([]byte, error) {
		calls++
		lock, err := drafttx.Acquire(root)
		if err != nil {
			return nil, err
		}
		defer lock.Close()
		d, err := openReviewDraft(root, "XR-005", false)
		if err != nil {
			return nil, err
		}
		d.Mark = "чужая правка"
		if err := d.save(); err != nil {
			return nil, err
		}
		return []byte(`{"id":"thread-1"}`), nil
	}
	if _, err := cmdReviewPublish(root, "XR-005", CommitOpts{}); err == nil || !strings.Contains(err.Error(), "отправлено") {
		t.Fatalf("expected conflict after send: %v", err)
	}
	d, err := openReviewDraft(root, "XR-005", false)
	if err != nil || d.Mark != "чужая правка" || d.Blocks[0].State != reviewStateApproved {
		t.Fatalf("concurrent edit lost: %+v %v", d, err)
	}
	if _, err := cmdReviewPublish(root, "XR-005", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("sent %d times", calls)
	}
	d, err = openReviewDraft(root, "XR-005", false)
	if err != nil || d.Mark != "чужая правка" || d.Blocks[0].Thread != "thread-1" {
		t.Fatalf("receipt not installed: %+v %v", d, err)
	}
}

func TestReviewPollReleasesLockAndRejectsConcurrentEdit(t *testing.T) {
	root := setupDraft(t)
	if _, err := cmdReviewDraft(root, "XR-005", "одно замечание", reviewDraftParams{}, CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), "taskctl")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, out)
	}
	old := runPublish
	t.Cleanup(func() { runPublish = old })
	runPublish = func(string) ([]byte, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
		defer cancel()
		out, err := exec.CommandContext(ctx, bin, "-C", root, "show", "XR-005").CombinedOutput()
		if ctx.Err() != nil {
			return nil, fmt.Errorf("nested taskctl deadlocked")
		}
		if err != nil {
			return nil, fmt.Errorf("nested taskctl: %v %s", err, out)
		}
		lock, err := drafttx.Acquire(root)
		if err != nil {
			return nil, err
		}
		defer lock.Close()
		d, err := openReviewDraft(root, "XR-005", false)
		if err != nil {
			return nil, err
		}
		d.Mark = "чужая правка"
		if err := d.save(); err != nil {
			return nil, err
		}
		return []byte(`{"state":"opened","sha":"bbbbbbb"}`), nil
	}
	before := sect(t, root, "XR-005")
	_, err := cmdReviewPoll(root, "XR-005", time.Now(), "", false, CommitOpts{})
	if err == nil || !strings.Contains(err.Error(), "изменились") {
		t.Fatalf("expected conflict: %v", err)
	}
	d, err := openReviewDraft(root, "XR-005", false)
	if err != nil || d.Mark != "чужая правка" || sect(t, root, "XR-005") != before {
		t.Fatalf("concurrent change lost: %+v %v", d, err)
	}
}

func TestReviewPollSerializesConcurrentPolls(t *testing.T) {
	root := setupDraft(t)
	if _, err := cmdReviewDraft(root, "XR-005", "одно замечание", reviewDraftParams{}, CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	old := runPublish
	t.Cleanup(func() { runPublish = old })
	started := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	runPublish = func(string) ([]byte, error) {
		close(started)
		<-release
		return []byte(`{"state":"opened","sha":"a1b2c3d"}`), nil
	}
	go func() { _, err := cmdReviewPoll(root, "XR-005", time.Now(), "", false, CommitOpts{}); done <- err }()
	select {
	case <-started:
	case <-time.After(6 * time.Second):
		t.Fatal("first poll did not start")
	}
	_, err := cmdReviewPoll(root, "XR-005", time.Now(), "", false, CommitOpts{})
	close(release)
	if err == nil || !strings.Contains(err.Error(), "уже идёт") {
		t.Fatalf("parallel poll accepted: %v", err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(6 * time.Second):
		t.Fatal("first poll stuck")
	}
}

func TestReviewPollWorklogAllowsNestedTaskctl(t *testing.T) {
	root, home := pollEnv(t)
	writeReviewConf(t, root, "worklog = on\n")
	stubPoll(t, mrMerged, threadQuiet)
	bin := filepath.Join(t.TempDir(), "taskctl")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, out)
	}
	old := runTrackctl
	t.Cleanup(func() { runTrackctl = old })
	calls := 0
	runTrackctl = func(root string, _ ...string) ([]byte, error) {
		calls++
		ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
		defer cancel()
		out, err := exec.CommandContext(ctx, bin, "-C", root, "show", "XR-001").CombinedOutput()
		if ctx.Err() != nil {
			return nil, fmt.Errorf("nested taskctl deadlocked")
		}
		return out, err
	}
	out, err := cmdReviewPoll(root, "XR-005", time.Now(), home, false, CommitOpts{})
	if err != nil || calls != 1 || !strings.Contains(out, "ворклог ABC-12:") || strings.Contains(out, "повтор остановлен") {
		t.Fatalf("poll worklog: calls=%d err=%v %s", calls, err, out)
	}
}

func TestReviewPublishAmbiguousFailureDoesNotRepeat(t *testing.T) {
	root := setupDraft(t)
	writeReviewConf(t, root, "publish = auto\npause = 0\n")
	if _, err := cmdReviewDraft(root, "XR-005", "одно замечание", reviewDraftParams{}, CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	old := runPublish
	t.Cleanup(func() { runPublish = old })
	calls := 0
	runPublish = func(string) ([]byte, error) { calls++; return nil, fmt.Errorf("connection lost") }
	if _, err := cmdReviewPublish(root, "XR-005", CommitOpts{}); err == nil {
		t.Fatal("ambiguous send accepted")
	}
	if _, err := cmdReviewPublish(root, "XR-005", CommitOpts{}); err == nil || !strings.Contains(err.Error(), "повтор остановлен") {
		t.Fatalf("repeat: %v", err)
	}
	if calls != 1 {
		t.Fatalf("sent %d times", calls)
	}
}

func TestReviewPublishKeepsDistinctIdenticalBlocks(t *testing.T) {
	root := setupDraft(t)
	writeReviewConf(t, root, "publish = auto\npause = 0\n")
	for i := 0; i < 2; i++ {
		if _, err := cmdReviewDraft(root, "XR-005", "одно замечание", reviewDraftParams{}, CommitOpts{}); err != nil {
			t.Fatal(err)
		}
	}
	old := runPublish
	t.Cleanup(func() { runPublish = old })
	calls := 0
	runPublish = func(string) ([]byte, error) { calls++; return []byte(fmt.Sprintf(`{"id":"thread-%d"}`, calls)), nil }
	if _, err := cmdReviewPublish(root, "XR-005", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	d, err := openReviewDraft(root, "XR-005", false)
	if err != nil || calls != 2 || d.Blocks[0].Thread != "thread-1" || d.Blocks[1].Thread != "thread-2" {
		t.Fatalf("calls=%d draft=%+v err=%v", calls, d, err)
	}
}

func TestReviewPublishRejectsChangedIdenticalBlockCount(t *testing.T) {
	root := setupDraft(t)
	writeReviewConf(t, root, "publish = auto\npause = 0\n")
	if _, err := cmdReviewDraft(root, "XR-005", "одно замечание", reviewDraftParams{}, CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	old := runPublish
	t.Cleanup(func() { runPublish = old })
	calls := 0
	runPublish = func(string) ([]byte, error) {
		calls++
		lock, err := drafttx.Acquire(root)
		if err != nil {
			return nil, err
		}
		defer lock.Close()
		d, err := openReviewDraft(root, "XR-005", false)
		if err != nil {
			return nil, err
		}
		d.Blocks = append(d.Blocks, d.Blocks[0])
		if err := d.save(); err != nil {
			return nil, err
		}
		return []byte(`{"id":"thread-1"}`), nil
	}
	if _, err := cmdReviewPublish(root, "XR-005", CommitOpts{}); err == nil {
		t.Fatal("concurrent edit accepted")
	}
	if _, err := cmdReviewPublish(root, "XR-005", CommitOpts{}); err == nil || !strings.Contains(err.Error(), "данные изменились после отправки") {
		t.Fatalf("changed count accepted: %v", err)
	}
	if calls != 1 {
		t.Fatalf("sent %d times", calls)
	}
}

func TestReviewPublishReplaySurvivesChangedDiffRefs(t *testing.T) {
	root := setupDraft(t)
	writeReviewConf(t, root, "publish = auto\npause = 0\n")
	if _, err := cmdReviewDraft(root, "XR-005", "одно замечание", reviewDraftParams{File: "x.go", Line: 1}, CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	old := runPublish
	t.Cleanup(func() { runPublish = old })
	reads, sends := 0, 0
	runPublish = func(script string) ([]byte, error) {
		if !strings.Contains(script, "--request POST") {
			reads++
			return []byte(fmt.Sprintf(`{"diff_refs":{"base_sha":"b","start_sha":"s","head_sha":"h%d"}}`, reads)), nil
		}
		sends++
		lock, err := drafttx.Acquire(root)
		if err != nil {
			return nil, err
		}
		defer lock.Close()
		d, err := openReviewDraft(root, "XR-005", false)
		if err != nil {
			return nil, err
		}
		d.Mark = "чужая правка"
		if err := d.save(); err != nil {
			return nil, err
		}
		return []byte(`{"id":"thread-1"}`), nil
	}
	if _, err := cmdReviewPublish(root, "XR-005", CommitOpts{}); err == nil {
		t.Fatal("concurrent edit accepted")
	}
	if _, err := cmdReviewPublish(root, "XR-005", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	if sends != 1 || reads != 2 {
		t.Fatalf("reads=%d sends=%d", reads, sends)
	}
}

func TestReviewApproveDoesNotOverwriteConcurrentMarkOrResend(t *testing.T) {
	root := setupDraft(t)
	writeReviewConf(t, root, "publish = auto\npause = 0\n")
	if _, err := cmdReviewDraft(root, "XR-005", "одно замечание", reviewDraftParams{}, CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	old := runPublish
	t.Cleanup(func() { runPublish = old })
	calls := 0
	runPublish = func(string) ([]byte, error) {
		calls++
		lock, err := drafttx.Acquire(root)
		if err != nil {
			return nil, err
		}
		defer lock.Close()
		d, err := openReviewDraft(root, "XR-005", false)
		if err != nil {
			return nil, err
		}
		d.Mark = "чужая правка"
		if err := d.save(); err != nil {
			return nil, err
		}
		return []byte(`{}`), nil
	}
	if _, err := cmdReviewApproveMR(root, "XR-005", true, CommitOpts{}); err == nil || !strings.Contains(err.Error(), "апрув отправлен") {
		t.Fatalf("expected conflict: %v", err)
	}
	if _, err := cmdReviewApproveMR(root, "XR-005", true, CommitOpts{}); err == nil || !strings.Contains(err.Error(), "данные изменились после отправки") {
		t.Fatalf("unsafe retry: %v", err)
	}
	d, err := openReviewDraft(root, "XR-005", false)
	if err != nil || d.Mark != "чужая правка" || calls != 1 {
		t.Fatalf("mark or send changed: %+v calls=%d err=%v", d, calls, err)
	}
}
