package main

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestQueueRetryResumesAfterCodeChange(t *testing.T) {
	t.Parallel()
	root := queueSetup(t)
	gitT(t, root, "branch", "xr-001-a", "xr-007-small")
	for i := 0; i < queueRetryLimit; i++ {
		if _, err := cmdQueue(root, QueueParams{Drain: true, Test: loadRedTest}); err != nil {
			t.Fatal(err)
		}
	}

	for _, item := range queueJSONItems(t, root, QueueParams{Drain: true, Test: loadRedTest}) {
		// Снятая строка может ехать в отчёт признаком held, а попыткой слияния
		// стать не должна: служебная запись не возвращает ветку в разлив.
		if item.ID == "XR-001" && (item.Failed || item.Waiting || item.Merged) {
			t.Fatalf("снятая ветка снова ушла на слияние после служебной записи: %+v", item)
		}
	}

	wt, err := taskWorktree(root, "XR-001")
	if err != nil || wt == nil {
		t.Fatalf("дерево задачи: %v, %v", wt, err)
	}
	write(t, wt.Path, "code.txt", "fixed\n")
	gitT(t, wt.Path, "commit", "-qam", "fix: XR-001 новая правка")
	completeReviewAudit(t, wt.Path, "XR-001", true)

	items := queueJSONItems(t, root, QueueParams{Drain: true, Test: loadRedTest})
	if len(items) != 1 || items[0].ID != "XR-001" || !strings.Contains(items[0].Line, "повтор 1 из 3") {
		t.Fatalf("новая правка должна начать счёт заново: %+v", items)
	}
}

func TestQueueRetryResetsAfterReasonChange(t *testing.T) {
	t.Parallel()
	root := queueSetup(t)
	changed := strings.ReplaceAll(loadRedTest, "TestDashboardSmokeHiddenTabQuiet", "TestDashboardOther")
	for _, command := range []string{loadRedTest, loadRedTest, changed} {
		if _, err := cmdQueue(root, QueueParams{Drain: true, Test: command}); err != nil {
			t.Fatal(err)
		}
	}
	msg, err := cmdQueue(root, QueueParams{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "XR-001 (ранг 55), повторов 1 из 3") ||
		!strings.Contains(msg, "TestDashboardOther") {
		t.Fatalf("смена причины должна начать счёт заново: %q", msg)
	}
}

func TestQueueRetryCountsSameFailuresAcrossRebases(t *testing.T) {
	t.Parallel()
	root := queueSetup(t)
	forward := `sh -c 'printf "%s\n" "a (outside-a) 0.1s FAIL" "b (outside-b) 0.1s FAIL"; exit 1'`
	reverse := `sh -c 'printf "%s\n" "b (outside-b) 0.1s FAIL" "a (outside-a) 0.1s FAIL"; exit 1'`
	for i, command := range []string{forward, reverse, forward} {
		gitT(t, root, "commit", "--allow-empty", "-qm", "docs(tasks): движение main")
		items := queueJSONItems(t, root, QueueParams{Drain: true, Test: command})
		want := []string{"повтор 1 из 3", "повтор 2 из 3", "ушла из очереди слияний"}[i]
		if len(items) != 1 || items[0].ID != "XR-001" || !strings.Contains(items[0].Line, want) {
			t.Fatalf("заход %d, ожидалось %q: %+v", i+1, want, items)
		}
	}
}

func TestQueueRetrySkipsConcurrentDrain(t *testing.T) {
	t.Parallel()
	root := queueSetup(t)
	f, err := os.OpenFile(filepath.Join(root, lockPath+".queue"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}

	items := queueJSONItems(t, root, QueueParams{Drain: true, Test: loadRedTest})
	if len(items) != 0 || len(loadQueue(root).Tasks) != 0 {
		t.Fatalf("параллельный разлив начал попытку слияния: %+v", items)
	}
}
