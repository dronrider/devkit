package main

import (
	"os"
	"strings"
	"testing"

	"github.com/dronrider/devkit/internal/drafttx"
)

func TestCatchupRecoversInterruptedBarrier(t *testing.T) {
	for _, state := range []string{"before", "after", "third"} {
		t.Run(state, func(t *testing.T) {
			root, wt, _ := laggedWorktree(t, 1)
			before := headAt(t, wt)
			target := gitOut(t, root, "rev-parse", "origin/main")
			lock, err := drafttx.Acquire(wt)
			if err != nil {
				t.Fatal(err)
			}
			digest, err := catchupDocsDigest(wt)
			if err != nil {
				lock.Close()
				t.Fatal(err)
			}
			_, lease, err := lock.BeginExternal("catchup:" + before + ":" + target + ":" + digest)
			lock.Close()
			if err != nil {
				t.Fatal(err)
			}
			if state == "after" {
				gitOut(t, wt, "checkout", "-q", target)
			}
			if state == "third" {
				f, err := os.OpenFile(taskFileAbs(wt, "XR-001"), os.O_APPEND|os.O_WRONLY, 0)
				if err != nil {
					lease.Close()
					t.Fatal(err)
				}
				f.WriteString("\nчужая правка\n")
				f.Close()
			}
			lease.Close()
			_, err = cmdCatchup(wt, false)
			if state == "third" {
				if err == nil || !strings.Contains(err.Error(), "защитный журнал сохранён") {
					t.Fatalf("third state accepted: %v", err)
				}
				if l, err := drafttx.Acquire(wt); err == nil {
					l.Close()
					t.Fatal("third-state barrier lost")
				}
				data, err := os.ReadFile(taskFileAbs(wt, "XR-001"))
				if err != nil || !strings.Contains(string(data), "чужая правка") {
					t.Fatalf("edit lost: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := headAt(t, wt); got != target {
				t.Fatalf("HEAD %s != %s", got, target)
			}
			if l, err := drafttx.Acquire(wt); err != nil {
				t.Fatal(err)
			} else {
				l.Close()
			}
		})
	}
}

func TestCatchupWaitsForPendingPublication(t *testing.T) {
	_, wt, _ := laggedWorktree(t, 1)
	before := headAt(t, wt)
	lock, err := drafttx.Acquire(wt)
	if err != nil {
		t.Fatal(err)
	}
	token, err := lock.BeginPublication()
	lock.Close()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cmdCatchup(wt, false); err == nil || !strings.Contains(err.Error(), "публикация Git не завершена") {
		t.Fatalf("pending publication ignored: %v", err)
	}
	if headAt(t, wt) != before {
		t.Fatal("checkout raced publication")
	}
	if err := drafttx.FinishPublication(wt, token); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdCatchup(wt, false); err != nil {
		t.Fatal(err)
	}
}
