package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/dronrider/devkit/internal/draftmeta"
	"github.com/dronrider/devkit/internal/stage"
)

var startReservedFn = startReserved

func startManaged(root, tree, id, sess string, command func(string) string) error {
	if id == "" {
		_, err := runProc("tmux", "new-session", "-d", "-s", sess, "-c", tree, command(""))
		return err
	}
	return startReservedFn(root, tree, id, sess, command)
}

// startReserved runs tmux in a guardian which retains the shared cloud locks
// until agentctl takes its own locks or the new session disappears.
func startReserved(root, tree, id, sess string, command func(string) string) error {
	roots := []string{root, tree, stage.MainRoot(tree)}
	seen := map[string]bool{}
	var locks []*draftmeta.CloudLock
	for _, r := range roots {
		if seen[r] {
			continue
		}
		seen[r] = true
		l, err := draftmeta.CloudShared(r, id)
		if err != nil {
			for _, v := range locks {
				v.Close()
			}
			return err
		}
		locks = append(locks, l)
	}
	for _, r := range roots {
		if err := draftmeta.GuardRecord(r, id); err != nil {
			for _, v := range locks {
				v.Close()
			}
			return err
		}
	}
	dir, err := os.MkdirTemp("", "devkit-cloud-start-")
	if err != nil {
		for _, v := range locks {
			v.Close()
		}
		return err
	}
	tmux, err := exec.LookPath("tmux")
	if err != nil {
		os.RemoveAll(dir)
		for _, v := range locks {
			v.Close()
		}
		return err
	}
	script := `d=$1; tmux=$2; sess=$3; shift 3
"$tmux" "$@" >"$d/out" 2>&1
rc=$?
printf '%s' "$rc" >"$d/status.tmp"
/bin/mv "$d/status.tmp" "$d/status"
while [ ! -f "$d/ack" ]; do
  if ! "$tmux" has-session -t "=$sess" >/dev/null 2>&1; then
    # A failed probe is not evidence that the client died. Only a successful
    # enumeration without the session, or a missing tmux server, releases it.
    if "$tmux" list-sessions -F '#{session_name}' >"$d/sessions" 2>"$d/probe-error"; then
      /usr/bin/grep -Fx -- "$sess" "$d/sessions" >/dev/null || break
    else
      case "$(/bin/cat "$d/probe-error")" in
        *"no server running"*) break;;
      esac
    fi
  fi
  /bin/sleep 1
done
n=0
while [ ! -f "$d/seen" ] && [ "$n" -lt 30 ]; do /bin/sleep 1; n=$((n+1)); done
/bin/rm -rf "$d"`
	args := []string{"-c", script, "cloud-start", dir, tmux, sess, "new-session", "-d", "-s", sess, "-c", tree, command(filepath.Join(dir, "ack"))}
	cmd := exec.Command("/bin/sh", args...)
	cmd.Dir = tree
	for _, l := range locks {
		cmd.ExtraFiles = append(cmd.ExtraFiles, l.File())
	}
	if err = cmd.Start(); err != nil {
		os.RemoveAll(dir)
		for _, v := range locks {
			v.Close()
		}
		return err
	}
	// Closing the descriptors without LOCK_UN leaves the inherited reservation
	// held by the guardian, including when the dashboard process exits.
	for _, l := range locks {
		l.File().Close()
	}
	go cmd.Wait()
	deadline := time.Now().Add(30 * time.Second)
	for {
		data, err := os.ReadFile(filepath.Join(dir, "status"))
		if err == nil {
			code, e := strconv.Atoi(strings.TrimSpace(string(data)))
			if e != nil {
				os.WriteFile(filepath.Join(dir, "seen"), []byte("seen"), 0600)
				return fmt.Errorf("неверный ответ запуска tmux")
			}
			if code != 0 {
				out, _ := os.ReadFile(filepath.Join(dir, "out"))
				os.WriteFile(filepath.Join(dir, "seen"), []byte("seen"), 0600)
				return fmt.Errorf("tmux: %s", strings.TrimSpace(string(out)))
			}
			os.WriteFile(filepath.Join(dir, "seen"), []byte("seen"), 0600)
			return nil
		}
		if !os.IsNotExist(err) {
			return err
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("tmux не подтвердил запуск; резерв остаётся у процесса запуска")
		}
		time.Sleep(25 * time.Millisecond)
	}
}
