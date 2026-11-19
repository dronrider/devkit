package main

import (
	"crypto/sha256"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func appArtifactEnv(t *testing.T, mode string) (*testEnv, *http.Client) {
	e, c, _ := appArtifactRestartable(t, mode)
	return e, c
}

func appArtifactRestartable(t *testing.T, mode string) (*testEnv, *http.Client, func()) {
	t.Helper()
	binary := os.Getenv("DEVKIT_DASHBOARD_BIN")
	if binary == "" {
		t.Skip("DEVKIT_DASHBOARD_BIN не назван: проверка выкаченного бинаря")
	}
	if !filepath.IsAbs(binary) {
		t.Fatal("DEVKIT_DASHBOARD_BIN должен быть абсолютным путём")
	}
	raw, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("artifact %s sha256 %x", binary, sha256.Sum256(raw))
	e, _ := appTaskEnv(t, mode)
	copyDir := filepath.Join(e.home, "artifact")
	if err = os.MkdirAll(copyDir, 0755); err != nil {
		t.Fatal(err)
	}
	copyPath := filepath.Join(copyDir, "dashboard")
	if err = os.WriteFile(copyPath, raw, 0755); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	config := fmt.Sprintf("root = %s\naddr = 127.0.0.1\nport = %d\ntoken = test-token\ncatchup_interval = 24h\n", filepath.Dir(e.proj), port)
	if err = os.WriteFile(filepath.Join(e.home, ".devkit", "dashboard.local"), []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	kit := filepath.Join(filepath.Dir(e.proj), "devkit")
	if source := os.Getenv("DEVKIT_TASK_RUN_ARTIFACT"); source != "" {
		if !filepath.IsAbs(source) {
			t.Fatal("DEVKIT_TASK_RUN_ARTIFACT должен быть абсолютным путём")
		}
		runtime, err := os.ReadFile(source)
		if err != nil {
			t.Fatal(err)
		}
		selected := taskRunPath(e.cfg.Roots)
		if err := os.WriteFile(selected, runtime, 0755); err != nil {
			t.Fatal(err)
		}
		t.Logf("task-run artifact %s sha256 %x; selected %s sha256 %x", source, sha256.Sum256(runtime), selected, sha256.Sum256([]byte(readFile(t, selected))))
	}
	var cmd *exec.Cmd
	var done chan struct{}
	var processErr error
	stop := func() {
		if cmd == nil {
			return
		}
		cmd.Process.Signal(syscall.SIGTERM)
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			cmd.Process.Kill()
			<-done
		}
		cmd = nil
	}
	start := func() {
		logFile, err := os.OpenFile(filepath.Join(e.home, "artifact.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			t.Fatal(err)
		}
		defer logFile.Close()
		cmd = exec.Command(copyPath, "serve")
		cmd.Dir = e.home
		cmd.Env = standSpawnEnv("HOME="+e.home, "DEVKIT_DASH_HOME="+e.home, "DEVKIT_HOME="+kit, "DEVKIT_NOTIFY_OFF=1", "DEVKIT_TASK_PASS_PAUSE=0", "CLAUDE_CODE_SESSION_ID=", "CODEX_THREAD_ID=", "CLAUDE_CONFIG_DIR=", "DEVKIT_TMUX=", "DEVKIT_TASK=")
		cmd.Stdout, cmd.Stderr = logFile, logFile
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		done = make(chan struct{})
		current, finished := cmd, done
		go func() { processErr = current.Wait(); close(finished) }()
		until := time.After(10 * time.Second)
		tick := time.NewTicker(20 * time.Millisecond)
		defer tick.Stop()
		for {
			resp, err := http.Get(e.srv.URL + "/healthz")
			if err == nil {
				ready := resp.StatusCode == http.StatusOK
				resp.Body.Close()
				if ready {
					return
				}
			}
			select {
			case <-done:
				t.Fatalf("artifact вышел: %v\n%s", processErr, readFile(t, logFile.Name()))
			case <-until:
				t.Fatalf("artifact не поднялся:\n%s", readFile(t, logFile.Name()))
			case <-tick.C:
			}
		}
	}
	e.srv.Close()
	e.srv.URL = fmt.Sprintf("http://127.0.0.1:%d", port)
	t.Cleanup(stop)
	start()
	restart := func() { stop(); start() }
	return e, e.loggedClient(t), restart
}

func TestAppTaskInstalledArtifact(t *testing.T) {
	e, c := appArtifactEnv(t, "mute")
	code, got := appTaskStart(t, e, c)
	if code != http.StatusOK {
		t.Fatalf("artifact start: %d %+v\n%s", code, got, readFile(t, filepath.Join(e.home, ".devkit", "heads", "XR-002.log"))+readFile(t, filepath.Join(e.home, ".devkit", "dashboard.log")))
	}
	sid := got["session"]
	st := e.s.chatStoreRead(sid)
	if got["model"] != "модель-codex" || got["effort"] != "high" || st.Task != "XR-002" || st.Thread == "" {
		t.Fatalf("artifact потерял выбор/привязку: %+v %+v", got, st)
	}
	code, again := appTaskStart(t, e, c)
	if code != http.StatusConflict {
		t.Fatalf("artifact duplicate: %d %+v", code, again)
	}
	resp := doReq(t, c, "DELETE", e.srv.URL+"/api/projects/demo/runs/XR-002", "")
	said := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("artifact stop: %d %s", resp.StatusCode, said)
	}
	waitAppTaskLockGone(t, e)
	log := readFile(t, filepath.Join(e.proj, "fake-app-log"))
	if !strings.Contains(log, "turn high") || !strings.Contains(log, `"approvalsReviewer": "auto_review"`) {
		t.Fatalf("artifact не передал выбор/политику: %s", log)
	}
	if values := appContextValues(t, e.proj); len(values) != 1 || values[0] != "autonomous = true\n\n## Автономная работа\n\nSynthetic commit instruction." {
		t.Fatalf("artifact не передал контекст автономной задачи: %q", values)
	}
	t.Log("artifact: HTTP start, task binding, model/effort, devkit-autonomous context, duplicate refusal and stop passed")
}

func TestAppTaskContextInstalledArtifact(t *testing.T) {
	e, client, restart := appArtifactRestartable(t, "")
	kit := filepath.Join(filepath.Dir(e.proj), "devkit")
	stale := e.proj + "-xr-002"
	putDoc(t, stale, "RULES.board.core.md", "# Old rules\n\n## Автономная работа\n\nOld worktree instruction.\n")
	putDoc(t, stale, "AGENTS.md", "Old embedded instructions.\n")
	putDoc(t, kit, "RULES.board.core.md", "# Current rules\n\n## Автономная работа\n\nArtifact instruction.\n")
	sid := "asartifact-context"
	stored := chatStore{Project: "demo", Task: "XR-002", Model: "модель-codex", Harness: "codextest", Protocol: "codex", Thread: "th-1", CxEffort: "high", Born: e.s.now().Unix()}
	if err := e.s.chatStoreWrite(sid, stored); err != nil {
		t.Fatal(err)
	}
	say := func(id string) {
		t.Helper()
		r := doReq(t, client, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/say", `{"text":"проверь синтетическую запись","msg_id":"`+id+`"}`)
		if raw := body(t, r); r.StatusCode != http.StatusOK {
			t.Fatalf("artifact turn %s: %d %s", id, r.StatusCode, raw)
		}
		appWaitBusy(t, e, client, sid, false)
	}
	say("context-first")
	before := e.s.chatStoreRead(sid)
	putDoc(t, kit, "RULES.board.core.md", "# Current rules\n\n## Автономная работа\n\nUpdated artifact instruction.\n")
	say("context-live")
	restart()
	client = e.loggedClient(t)
	say("context-resume")
	putDoc(t, e.proj, ".devkit/deploy.local", "autonomous = false\n")
	say("context-disabled")
	after := e.s.chatStoreRead(sid)
	if before.Thread == "" || before.Thread != after.Thread || after.Task != "XR-002" || after.TaskRun {
		t.Fatalf("artifact изменил нить или выдал роль конвейера: before=%+v after=%+v", before, after)
	}
	want := []string{
		"autonomous = true\n\n## Автономная работа\n\nArtifact instruction.",
		"autonomous = true\n\n## Автономная работа\n\nUpdated artifact instruction.",
		"autonomous = true\n\n## Автономная работа\n\nUpdated artifact instruction.",
		"autonomous = false",
	}
	if got := appContextValues(t, stale); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("artifact контекст старого дерева: got=%q, want=%q", got, want)
	}
	if readFile(t, filepath.Join(stale, "AGENTS.md")) != "Old embedded instructions.\n" {
		t.Fatal("artifact изменил старое дерево")
	}
	t.Log("artifact: devkit-autonomous from current root, live update, same-thread resume and autonomous=false passed")
}
