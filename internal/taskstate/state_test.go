package taskstate

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestStopSurvivesLaunchAndRestart(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	if err := SetStopped(home, root, "DK-1", true); err != nil {
		t.Fatal(err)
	}
	g, err := Acquire(home, root, "DK-1")
	if err != nil {
		t.Fatal(err)
	}
	if err = g.Start("first", "glm-code-2", "glm-5.3", ""); err == nil {
		t.Fatal("остановка обойдена")
	}
	g.Close()
	if err = SetStopped(home, root, "DK-1", false); err != nil {
		t.Fatal(err)
	}
	g, err = Acquire(home, root, "DK-1")
	if err != nil {
		t.Fatal(err)
	}
	if err = g.Start("second", "glm-code-2", "glm-5.3", ""); err != nil {
		t.Fatal(err)
	}
	g.Close()
	g, err = Acquire(home, root, "DK-1")
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	if g.State.Run.ID != "second" || g.State.Stopped {
		t.Fatalf("%+v", g.State)
	}
}

func TestUnknownStateFailsClosed(t *testing.T) {
	for _, data := range []string{`{`, `{}`, `{"version":2}`, `{} {}`} {
		t.Run(data, func(t *testing.T) {
			home, root := t.TempDir(), t.TempDir()
			path, _, err := Path(home, root, "DK-1")
			if err != nil {
				t.Fatal(err)
			}
			os.MkdirAll(filepath.Dir(path), 0700)
			os.WriteFile(path, []byte(data), 0600)
			if g, err := Acquire(home, root, "DK-1"); err == nil {
				g.Close()
				t.Fatal("неизвестное состояние принято")
			}
		})
	}
}

func TestCanonicalRootAndIsolation(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(root, alias); err != nil {
		t.Fatal(err)
	}
	a, _, _ := Path(home, root, "DK-1")
	b, _, _ := Path(home, alias, "DK-1")
	c, _, _ := Path(home, t.TempDir(), "DK-1")
	if a != b || a == c {
		t.Fatal("корни не различены")
	}
}

func TestStateSymlinkFailsClosed(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	path, _, _ := Path(home, root, "DK-1")
	os.MkdirAll(filepath.Dir(path), 0700)
	other := filepath.Join(t.TempDir(), "other")
	os.WriteFile(other, []byte(`{}`), 0600)
	os.Symlink(other, path)
	if g, err := Acquire(home, root, "DK-1"); err == nil {
		g.Close()
		t.Fatal("ссылка принята")
	}
}

func TestLockNotInheritedByWorker(t *testing.T) {
	g, err := Acquire(t.TempDir(), t.TempDir(), "DK-1")
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	flags, _, errno := syscall.Syscall(syscall.SYS_FCNTL, g.lock.Fd(), syscall.F_GETFD, 0)
	if errno != 0 {
		t.Fatal(errno)
	}
	if flags&syscall.FD_CLOEXEC == 0 {
		t.Fatal("работник унаследует замок состояния")
	}
}

func TestStateLockWaitIsBounded(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	g, err := Acquire(home, root, "RV-1")
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	start := time.Now()
	if other, err := acquire(home, root, "RV-1", 20*time.Millisecond); err == nil {
		other.Close()
		t.Fatal("общий замок проигнорирован")
	}
	if time.Since(start) > time.Second { // стенной порог: проверяется предел ожидания занятого замка
		t.Fatal("срок ожидания не выдержан")
	}
}

func TestContinuationInvalidatesOnlyStoppedSource(t *testing.T) {
	for _, stopped := range []bool{false, true} {
		home, root := t.TempDir(), t.TempDir()
		g, err := Acquire(home, root, "RV-1")
		if err != nil {
			t.Fatal(err)
		}
		if err = g.Start("old", "h", "m", ""); err != nil {
			t.Fatal(err)
		}
		g.State.Stopped = stopped
		if err = g.Save(); err != nil {
			t.Fatal(err)
		}
		g.Close()
		if err = SetStopped(home, root, "RV-1", false); err != nil {
			t.Fatal(err)
		}
		g, err = Acquire(home, root, "RV-1")
		if err != nil {
			t.Fatal(err)
		}
		if (g.State.Run == nil) != stopped {
			t.Fatal("неверная сохранность исходного запуска")
		}
		g.Close()
	}
}
