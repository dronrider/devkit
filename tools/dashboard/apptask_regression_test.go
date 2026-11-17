package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dronrider/devkit/internal/taskhead"
)

func appTaskEnv(t *testing.T, mode string) (*testEnv, *http.Client) {
	t.Helper()
	e, c := appEnv(t)
	t.Setenv("DEVKIT_TASK_PASS_PAUSE", "0")
	useHeadHome(t, e)
	putDoc(t, e.proj, "docs/tasks/XR-002.md", "# XR-002: тест\n")
	writeTaskRunFake(t, filepath.Dir(e.proj))
	kit := filepath.Join(filepath.Dir(e.proj), "devkit")
	for _, rel := range []string{taskhead.TaskRunRel, "tools/devkitctl/taskstate.py"} {
		raw, err := os.ReadFile(filepath.Join("..", "..", filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		dst := filepath.Join(kit, filepath.FromSlash(rel))
		os.MkdirAll(filepath.Dir(dst), 0755)
		if err = os.WriteFile(dst, raw, 0644); err != nil {
			t.Fatal(err)
		}
	}
	profile := filepath.Join(kit, "kit", "harness", "codextest.toml")
	if err := os.WriteFile(profile, []byte("[hooks]\nprotocol = \"codex\"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	writeScript(t, e.bin, "taskctl", `case "$1" in
 list) if [ -f `+shQuote(filepath.Join(e.proj, "board-state"))+` ]; then cat `+shQuote(filepath.Join(e.proj, "board-state"))+`; else printf '%s' '`+runsBoardJSON+`'; fi;;
 show) state=backlog; if [ -f task-state ]; then state=$(cat task-state); fi; echo "XR-002 в $state"; echo "  вид user";;
 guard) exit 0;;
 *) exit 0;;
esac`)
	if err := os.WriteFile(filepath.Join(e.proj, "fake-app-mode"), []byte(mode), 0644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		req, _ := http.NewRequest("DELETE", e.srv.URL+"/api/projects/demo/runs/XR-002", nil)
		if resp, err := c.Do(req); err == nil {
			resp.Body.Close()
		}
	})
	return e, c
}

func appTaskStart(t *testing.T, e *testEnv, c *http.Client) (int, map[string]string) {
	t.Helper()
	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/runs", `{"id":"XR-002","harness":"codextest","tier":"pro","effort":"high"}`)
	raw := body(t, resp)
	var got map[string]string
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("%s: %v", raw, err)
	}
	return resp.StatusCode, got
}

func TestAppTaskRunFromForm(t *testing.T) {
	e, c := appTaskEnv(t, "task-done")
	code, got := appTaskStart(t, e, c)
	if code != http.StatusOK {
		t.Fatalf("запуск формы: %d %+v", code, got)
	}
	sid := got["session"]
	if !strings.HasPrefix(sid, "as") || got["model"] != "модель-codex" || got["effort"] != "high" {
		t.Fatalf("потерян выбор: %+v", got)
	}
	st := e.s.chatStoreRead(sid)
	if st.Task != "XR-002" || st.Thread != "th-1" || st.CxEffort != "high" {
		t.Fatalf("не связана задача/нить: %+v", st)
	}
	log := readFile(t, filepath.Join(e.proj, "fake-app-log"))
	for _, want := range []string{"start модель-codex", "turn high", `"approvalPolicy": "on-request"`, `"approvalsReviewer": "auto_review"`, `shell_environment_policy.set.DEVKIT_TASK": "XR-002"`, `shell_environment_policy.set.DEVKIT_TASK_LOCK_FROM`} {
		if !strings.Contains(log, want) {
			t.Errorf("в клиенте нет %q: %s", want, log)
		}
	}
	appWaitFeed(t, e, sid, "ответ на реплику: Выполни XR-002")
	waitAppTaskLockGone(t, e)
}

func waitAppTaskLockGone(t *testing.T, e *testEnv) {
	t.Helper()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	deadline := time.After(10 * time.Second)
	for taskhead.Working(taskhead.LockPath(e.home, "XR-002")) {
		select {
		case <-tick.C:
		case <-deadline:
			t.Fatal("замок конвейера не снят")
		}
	}
}
