package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/dronrider/devkit/internal/taskhead"
	"github.com/dronrider/devkit/internal/taskstate"
)

func TestCodexCheckWorkerHelper(t *testing.T) {
	if os.Getenv("CHECK_WORKER") == "" {
		return
	}
	codexCheckWorker(os.Getenv("CHECK_ORDER"))
	os.Exit(0)
}

func TestCodexCheckWorkerStartsFreshTurn(t *testing.T) {
	home := t.TempDir()
	write(t, home, "docs/tasks/XR-1.md", "# XR-1\n")
	bin := t.TempDir()
	events, err := filepath.Abs(filepath.Join("testdata", "codex-check-start.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(home, "arguments")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" \"${CLAUDE_CODE_SESSION_ID-unset}\" \"${CODEX_THREAD_ID-unset}\" \"${DEVKIT_TASK-unset}\" \"${DEVKIT_RUN_DEPTH-unset}\" \"${DEVKIT_PARENT_SESSION-unset}\" \"${DEVKIT_TASK_HEAD_SESSION-unset}\" \"${DEVKIT_GOAL_SHELL-unset}\" > \"$CHECK_ARGS\"\ncat \"$CHECK_EVENTS\"\n"
	if err := os.WriteFile(filepath.Join(bin, "agentctl"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	o := codexCheckOrder{Root: home, Home: home, ID: "XR-1", Model: "model", Effort: "high", Harness: "codex", Order: "проверить"}
	raw, _ := json.Marshal(o)
	cmd := exec.Command(os.Args[0], "-test.run=^TestCodexCheckWorkerHelper$")
	cmd.Env = append(os.Environ(), "CHECK_WORKER=1", "CHECK_ORDER="+string(raw), "CHECK_ARGS="+log, "CHECK_EVENTS="+events, "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"), "CLAUDE_CODE_SESSION_ID=parent", "CODEX_THREAD_ID=parent", "DEVKIT_TASK=OTHER", "DEVKIT_RUN_DEPTH=1", "DEVKIT_PARENT_SESSION=parent", "DEVKIT_TASK_HEAD_SESSION=old-thread", "DEVKIT_GOAL_SHELL=old-goal")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("воркер: %v %s", err, out)
	}
	var got codexCheckStart
	if err := json.Unmarshal(out, &got); err != nil || !got.Started || !strings.Contains(got.Message, "00000000-0000-4000-8000-000000000001") {
		t.Fatalf("нет подтверждения хода: %s %v", out, err)
	}
	args, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"--approve-for-me", "--model model", "model_reasoning_effort=\"high\"", "проверить", "unset\nunset\nXR-1\nunset\nunset\nunset\nunset"} {
		if !strings.Contains(string(args), want) {
			t.Fatalf("нет %q в %q", want, args)
		}
	}
	if taskhead.Owner(taskhead.LockPath(home, "XR-1")) != 0 {
		t.Fatal("замок не снят")
	}
}

func TestCodexCheckWorkerFailsBeforeTurn(t *testing.T) {
	for _, tc := range []struct {
		name, command, diagnostic string
	}{
		{"client_exit", "exit 1\n", "exit status 1"},
		{"long_event", "head -c 8388608 /dev/zero | tr '\\000' x\nprintf '\\n'\n", "token too long"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			write(t, home, "docs/tasks/XR-1.md", "# XR-1\n")
			bin := t.TempDir()
			if err := os.WriteFile(filepath.Join(bin, "agentctl"), []byte("#!/bin/sh\nprintf '%s\\n' '{\"type\":\"thread.started\",\"thread_id\":\"fresh\"}'\n"+tc.command), 0755); err != nil {
				t.Fatal(err)
			}
			raw, _ := json.Marshal(codexCheckOrder{Root: home, Home: home, ID: "XR-1", Harness: "codex"})
			cmd := exec.Command(os.Args[0], "-test.run=^TestCodexCheckWorkerHelper$")
			cmd.Env = append(os.Environ(), "CHECK_WORKER=1", "CHECK_ORDER="+string(raw), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatal(err)
			}
			var got codexCheckStart
			if err := json.Unmarshal(out, &got); err != nil || got.Started || !strings.Contains(got.Message, "не начал ход") {
				t.Fatalf("должен быть отказ: %s %v", out, err)
			}
			data, err := os.ReadFile(filepath.Join(home, ".devkit", "heads", "XR-1-check.log"))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(data), tc.diagnostic) {
				t.Fatalf("журнал без причины остановки: %q", data)
			}
		})
	}
}

func TestCodexCheckWorkerRespectsStop(t *testing.T) {
	home := t.TempDir()
	write(t, home, "docs/tasks/XR-1.md", "# XR-1\n")
	if err := taskstate.SetStopped(home, home, "XR-1", true); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(codexCheckOrder{Root: home, Home: home, ID: "XR-1", Harness: "codex"})
	cmd := exec.Command(os.Args[0], "-test.run=^TestCodexCheckWorkerHelper$")
	cmd.Env = append(os.Environ(), "CHECK_WORKER=1", "CHECK_ORDER="+string(raw))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatal(err)
	}
	var got codexCheckStart
	if err := json.Unmarshal(out, &got); err != nil || got.Started || !strings.Contains(got.Message, "остановлена") {
		t.Fatalf("остановка: %s %v", out, err)
	}
}

func TestCodexCheckWorkerDoesNotDuplicate(t *testing.T) {
	home := t.TempDir()
	write(t, home, "docs/tasks/XR-1.md", "# XR-1\n")
	hold, err := taskhead.HoldForDelegate(home, "XR-1", "")
	if err != nil {
		t.Fatal(err)
	}
	defer hold.Release()
	raw, _ := json.Marshal(codexCheckOrder{Root: home, Home: home, ID: "XR-1", Harness: "codex"})
	cmd := exec.Command(os.Args[0], "-test.run=^TestCodexCheckWorkerHelper$")
	cmd.Env = append(os.Environ(), "CHECK_WORKER=1", "CHECK_ORDER="+string(raw))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatal(err)
	}
	var got codexCheckStart
	if err := json.Unmarshal(out, &got); err != nil || got.Started || !got.Busy {
		t.Fatalf("замок: %s %v", out, err)
	}
}

func TestCodexCheckWorkerStopsDescendant(t *testing.T) {
	home, bin := t.TempDir(), t.TempDir()
	write(t, home, "docs/tasks/XR-1.md", "# XR-1\n")
	fifo, child := filepath.Join(home, "pipe"), filepath.Join(home, "child")
	if err := syscall.Mkfifo(fifo, 0600); err != nil {
		t.Fatal(err)
	}
	fd, err := syscall.Open(fifo, syscall.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer syscall.Close(fd)
	script := `#!/bin/sh
/bin/sh -c 'trap "" TERM; exec 3>"$CHECK_FIFO"; echo $$ > "$CHECK_CHILD"; exec /bin/sleep 300' &
while [ ! -s "$CHECK_CHILD" ]; do :; done
printf '%s\n' '{"type":"thread.started","thread_id":"fresh"}' '{"type":"turn.started"}'
wait
`
	if err := os.WriteFile(filepath.Join(bin, "agentctl"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(codexCheckOrder{Root: home, Home: home, ID: "XR-1", Harness: "codex"})
	cmd := exec.Command(os.Args[0], "-test.run=^TestCodexCheckWorkerHelper$")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Env = append(os.Environ(), "CHECK_WORKER=1", "CHECK_ORDER="+string(raw), "CHECK_FIFO="+fifo, "CHECK_CHILD="+child, "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	descendantStopped := false
	t.Cleanup(func() {
		if b, e := os.ReadFile(child); !descendantStopped && e == nil {
			if pid, e := strconv.Atoi(strings.TrimSpace(string(b))); e == nil {
				_ = syscall.Kill(pid, syscall.SIGKILL)
			}
		}
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	})
	var start codexCheckStart
	if err := json.NewDecoder(stdout).Decode(&start); err != nil || !start.Started {
		t.Fatalf("старт: %+v %v", start, err)
	}
	var buf [1]byte
	if n, err := syscall.Read(fd, buf[:]); n != -1 || err != syscall.EAGAIN {
		t.Fatalf("потомок не держит канал: %d %v", n, err)
	}
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("supervisor: %v", err)
	}
	if n, err := syscall.Read(fd, buf[:]); n != 0 || err != nil {
		t.Fatalf("потомок остался жив: %d %v", n, err)
	}
	descendantStopped = true
	if taskhead.Owner(taskhead.LockPath(home, "XR-1")) != 0 {
		t.Fatal("замок не освобождён")
	}
}

func TestCodexCheckWorkerReportsLateLogFailure(t *testing.T) {
	for _, tc := range []struct {
		name       string
		loggerFail int
		diagnostic string
	}{
		{"system_log", 0, "-t shipctl XR-1: запись журнала Codex"},
		{"project_log", 1, "check-run XR-1: запись журнала Codex"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			write(t, home, "docs/tasks/XR-1.md", "# XR-1\n")
			logPath := filepath.Join(home, ".devkit", "heads", "XR-1-check.log")
			if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
				t.Fatal(err)
			}
			gatePath := filepath.Join(home, "gate")
			for _, path := range []string{logPath, gatePath} {
				if err := syscall.Mkfifo(path, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			reader, err := os.OpenFile(logPath, os.O_RDWR, 0)
			if err != nil {
				t.Fatal(err)
			}
			defer reader.Close()
			gate, err := os.OpenFile(gatePath, os.O_RDWR, 0)
			if err != nil {
				t.Fatal(err)
			}
			defer gate.Close()
			bin := t.TempDir()
			for name, script := range map[string]string{
				"agentctl": "#!/bin/sh\nprintf '%s\\n' '{\"type\":\"thread.started\",\"thread_id\":\"fresh\"}' '{\"type\":\"turn.started\"}'\nread -r ready < \"$CHECK_GATE\"\nprintf '%s\\n' '{\"type\":\"turn.completed\"}'\ntouch \"$CHECK_FINISHED\"\n",
				"logger":   "#!/bin/sh\nif [ \"$CHECK_LOGGER_FAIL\" = 1 ]; then exit 1; fi\nprintf '%s\\n' \"$*\" > \"$CHECK_ALERT\"\n",
			} {
				if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			alertPath := filepath.Join(home, "alert")
			finishedPath := filepath.Join(home, "finished")
			raw, _ := json.Marshal(codexCheckOrder{Root: home, Home: home, ID: "XR-1", Harness: "codex"})
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestCodexCheckWorkerHelper$")
			cmd.Env = append(os.Environ(), "CHECK_WORKER=1", "CHECK_ORDER="+string(raw),
				"PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
				"CHECK_GATE="+gatePath, "CHECK_ALERT="+alertPath, "CHECK_FINISHED="+finishedPath,
				"CHECK_LOGGER_FAIL="+strconv.Itoa(tc.loggerFail))
			out, err := cmd.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() {
				if cmd.ProcessState == nil {
					cancel()
					_ = cmd.Wait()
				}
			}()
			var start codexCheckStart
			if err := json.NewDecoder(out).Decode(&start); err != nil || !start.Started {
				t.Fatalf("проверяющий не запущен: %+v, %v", start, err)
			}
			if err := reader.Close(); err != nil {
				t.Fatal(err)
			}
			if _, err := gate.WriteString("continue\n"); err != nil {
				t.Fatal(err)
			}
			if err := cmd.Wait(); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(finishedPath); err != nil {
				t.Fatalf("проверяющий не закончил ход: %v", err)
			}
			if tc.loggerFail != 0 {
				alertPath = filepath.Join(home, ".devkit", "log")
			}
			alert, err := os.ReadFile(alertPath)
			if err != nil || !strings.Contains(string(alert), tc.diagnostic) {
				t.Fatalf("предупреждение в %s: %q, %v", alertPath, alert, err)
			}
		})
	}
}
