package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestPushDoesNotHangOnCredentials: DK-697. Пуш, упёршийся в помощника учётки,
// который ждёт человека, кончается отказом по пределу времени. Раньше он висел
// вечно, и вместе с ним вставали слияние, отметка выката и откат: пуш зовут все
// три пути shipctl.
func TestPushDoesNotHangOnCredentials(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	helper := filepath.Join(t.TempDir(), "sleepy-helper.sh")
	if err := os.WriteFile(helper, []byte("#!/bin/sh\nsleep 60\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Сервер просит представиться: ровно на этом ответе git зовёт помощника.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("WWW-Authenticate", `Basic realm="devkit"`)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	gitT(t, root, "remote", "add", "origin", srv.URL+"/repo.git")
	gitT(t, root, "config", "credential.helper", helper)
	gitT(t, root, "config", "branch.main.remote", "origin")
	gitT(t, root, "config", "branch.main.merge", "refs/heads/main")
	t.Setenv("DEVKIT_GIT_TIMEOUT", "2s")

	done := make(chan error, 1)
	go func() {
		_, err := git(root, "push")
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("пуш без учётки должен провалиться")
		}
		for _, want := range []string{"не кончился за 2s", "учётк", "DEVKIT_GIT_TIMEOUT"} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("в отказе нет %q:\n%v", want, err)
			}
		}
	case <-time.After(20 * time.Second):
		t.Fatal("пуш не вернулся за 20s: предел времени не сработал")
	}
}
