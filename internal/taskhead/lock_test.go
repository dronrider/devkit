package taskhead

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// liveProc поднимает процесс, который живёт дольше теста: замок с его pid
// занят живым владельцем.
func liveProc(t *testing.T) int {
	t.Helper()
	cmd := exec.Command("/bin/sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
	return cmd.Process.Pid
}

// deadPid это pid процесса, который уже вышел.
func deadPid(t *testing.T) int {
	t.Helper()
	cmd := exec.Command("/bin/sleep", "0")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	return cmd.Process.Pid
}

func TestTakeRefusesLiveOwner(t *testing.T) {
	path := LockPath(t.TempDir(), "dk-1")
	if !strings.HasSuffix(path, filepath.Join(".devkit", "task-DK-1.lock")) {
		t.Fatalf("замок назван %s, жду .devkit/task-DK-1.lock", path)
	}
	owner := liveProc(t)
	if err := Take(path, owner); err != nil {
		t.Fatal(err)
	}
	err := Take(path, os.Getpid())
	busy, ok := err.(*BusyError)
	if !ok {
		t.Fatalf("жду отказ занятого замка, а пришло %v", err)
	}
	if busy.Owner != owner || !strings.Contains(busy.Error(), "уже поднята") {
		t.Fatalf("отказ назвал владельца %d (%s), жду %d", busy.Owner, busy.Error(), owner)
	}
}

func TestTakeRetakesDeadOwner(t *testing.T) {
	path := LockPath(t.TempDir(), "DK-1")
	if err := Take(path, deadPid(t)); err != nil {
		t.Fatal(err)
	}
	if err := Take(path, os.Getpid()); err != nil {
		t.Fatalf("замок мёртвого владельца не взят заново: %v", err)
	}
	if Owner(path) != os.Getpid() {
		t.Fatalf("владелец %d, жду свой pid", Owner(path))
	}
}

func TestTakeRefusesYoungEmptyLock(t *testing.T) {
	// Соседний подъём успел завести каталог, а pid ещё не вписал.
	path := LockPath(t.TempDir(), "DK-1")
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, ok := Take(path, os.Getpid()).(*BusyError); !ok {
		t.Fatal("свежий пустой замок снят как брошенный")
	}
	old := time.Now().Add(-time.Minute)
	os.Chtimes(path, old, old)
	if err := Take(path, os.Getpid()); err != nil {
		t.Fatalf("старый пустой замок не взят: %v", err)
	}
}

func TestReleaseKeepsHandedLock(t *testing.T) {
	path := LockPath(t.TempDir(), "DK-1")
	if err := Take(path, os.Getpid()); err != nil {
		t.Fatal(err)
	}
	owner := liveProc(t)
	if err := writePid(path, owner); err != nil {
		t.Fatal(err)
	}
	Release(path, os.Getpid())
	if Owner(path) != owner {
		t.Fatal("команда сняла замок, уже переданный оболочке")
	}
	Release(path, owner)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("владелец не снял свой замок")
	}
}

func TestWaitHandoff(t *testing.T) {
	path := LockPath(t.TempDir(), "DK-1")
	me := os.Getpid()
	if err := Take(path, me); err != nil {
		t.Fatal(err)
	}
	if got := WaitHandoff(path, me, 200*time.Millisecond, nil); got != 0 {
		t.Fatalf("замок никто не принимал, а ожидание вернуло %d", got)
	}
	owner := liveProc(t)
	go func() {
		time.Sleep(100 * time.Millisecond)
		writePid(path, owner)
	}()
	if got := WaitHandoff(path, me, 5*time.Second, nil); got != owner {
		t.Fatalf("ожидание вернуло %d, жду %d", got, owner)
	}
	if got := WaitHandoff(LockPath(t.TempDir(), "DK-2"), me, 5*time.Second,
		func() bool { return true }); got != 0 {
		t.Fatalf("носитель умер, а ожидание вернуло %d", got)
	}
}
