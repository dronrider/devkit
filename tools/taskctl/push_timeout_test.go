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

// hangingCredentials поднимает remote, который просит представиться, и
// помощника учётки, который вместо ответа спит. Так себя ведёт связка ключей
// macOS в сессии без человека: диалог висит, git ждёт его вечно.
func hangingCredentials(t *testing.T, root string) {
	t.Helper()
	helper := filepath.Join(t.TempDir(), "sleepy-helper.sh")
	if err := os.WriteFile(helper, []byte("#!/bin/sh\nsleep 60\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("WWW-Authenticate", `Basic realm="devkit"`)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(srv.Close)
	gitOut(t, root, "remote", "add", "origin", srv.URL+"/repo.git")
	gitOut(t, root, "config", "credential.helper", helper)
	gitOut(t, root, "config", "branch.main.remote", "origin")
	gitOut(t, root, "config", "branch.main.merge", "refs/heads/main")
}

// TestPushDoesNotHangOnCredentials: DK-697. Пуш доски, упёршийся в учётку,
// кончается отказом по пределу времени, а не вечным ожиданием: раньше сессия
// вставала намертво, коммиты копились локально и соседи ловили конфликты на
// отставшем remote.
func TestPushDoesNotHangOnCredentials(t *testing.T) {
	root := setup(t)
	gitSetup(t, root)
	hangingCredentials(t, root)
	t.Setenv("DEVKIT_GIT_TIMEOUT", "2s")

	board := boardPath(root)
	body, _ := os.ReadFile(board)
	if err := os.WriteFile(board, append(body, []byte("\n<!-- правка -->\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := CommitOpts{Msg: "docs(tasks): XR-004 правка", Push: true}.apply(root, []string{"docs/TASKS.md"})
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
	// Коммит доски при этом сделан: отбит только пуш, и человеку есть что
	// досылать руками.
	if subj := gitOut(t, root, "log", "-1", "--pretty=%s"); subj != "docs(tasks): XR-004 правка" {
		t.Fatalf("коммит доски не состоялся: %q", subj)
	}
}
