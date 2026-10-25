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

func TestTakeRetakesFrozenOwner(t *testing.T) {
	// Живой pid сам по себе работой не считается (DK-1454): замерзшее окно
	// держит pid годами, и пока живость меряет один pid, замок занят вечно.
	// Признак жизни это отметка beat, и замок без неё снимается как брошенный.
	path := LockPath(t.TempDir(), "DK-1")
	owner := liveProc(t)
	if err := Take(path, owner); err != nil {
		t.Fatal(err)
	}
	staleBeat(t, path, -3*time.Hour)
	if err := Take(path, os.Getpid()); err != nil {
		t.Fatalf("замок замерзшего владельца не взят заново: %v", err)
	}
	if Owner(path) != os.Getpid() {
		t.Fatalf("владелец %d, жду свой pid", Owner(path))
	}
}

func TestTakeKeepsWorkingOwner(t *testing.T) {
	// Обратная сторона того же правила: живой владелец со свежей отметкой
	// работы занят по-настоящему, и второй головы поверх него не встаёт.
	path := LockPath(t.TempDir(), "DK-1")
	owner := liveProc(t)
	if err := Take(path, owner); err != nil {
		t.Fatal(err)
	}
	freshBeat(t, path)
	if _, ok := Take(path, os.Getpid()).(*BusyError); !ok {
		t.Fatal("замок работающего владельца снят как брошенный")
	}
}

// freshBeat заявляет признак жизни замка руками теста: пишет отметку beat.
func freshBeat(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(path, "beat"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
}

// staleBeat состаривает признак жизни замка на age: замерзший владелец.
func staleBeat(t *testing.T, path string, age time.Duration) {
	t.Helper()
	name := filepath.Join(path, "beat")
	if err := os.WriteFile(name, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(age)
	if err := os.Chtimes(name, old, old); err != nil {
		t.Fatal(err)
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
