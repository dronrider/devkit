package main

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dronrider/devkit/internal/draftmeta"
)

func TestStartingChatReservesBeforeTmux(t *testing.T) {
	t.Setenv("DEVKIT_TEST_CLOUD_REAL", "1")
	e := newTestEnv(t)
	putDoc(t, e.proj, "docs/tasks/XR-7.md", "# XR-7: публичная задача\n")
	arrived, release, alive := filepath.Join(e.home, "arrived"), filepath.Join(e.home, "release"), filepath.Join(e.home, "alive")
	script := `case "$1" in
new-session) touch '` + arrived + `'; while [ ! -f '` + release + `' ]; do sleep 0.05; done; touch '` + alive + `';;
has-session) test -f '` + alive + `'; exit $?;;
ls) exit 1;;
esac
exit 0`
	writeScript(t, e.bin, "tmux", script)
	writeAgentctlFake(t, e.bin, harnessTiersFixture)
	writeClientFakes(t, e.bin, "клиент-1", "клиент-2")
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		w := httptest.NewRecorder()
		e.s.startFresh(w, &Project{Name: "demo", Path: e.proj}, "XR-7", "продолжить", "XR-7 работа")
		done <- w
	}()
	t.Cleanup(func() { os.WriteFile(release, []byte("release"), 0600); os.Remove(alive) })
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(arrived); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("tmux не достиг старта")
		}
		time.Sleep(20 * time.Millisecond)
	}
	lock, err := draftmeta.CloudExclusive(e.proj, "XR-7")
	if err == nil {
		lock.Close()
		os.WriteFile(release, []byte("release"), 0600)
		<-done
		t.Fatal("private attach прошёл во время старта")
	}
	os.WriteFile(release, []byte("release"), 0600)
	w := <-done
	if w.Code != 200 {
		t.Fatalf("start %d %s", w.Code, w.Body.String())
	}
	os.Remove(alive)
	deadline = time.Now().Add(5 * time.Second)
	for {
		l, err := draftmeta.CloudExclusive(e.proj, "XR-7")
		if err == nil {
			l.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("завершённый запуск оставил резерв")
		}
		time.Sleep(50 * time.Millisecond)
	}
}
