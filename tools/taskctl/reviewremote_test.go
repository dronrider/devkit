package main

import (
	"fmt"
	"strings"
	"testing"

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
