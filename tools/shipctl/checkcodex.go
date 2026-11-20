package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/dronrider/devkit/internal/checkrun"
	"github.com/dronrider/devkit/internal/draftmeta"
	"github.com/dronrider/devkit/internal/drafttx"
	"github.com/dronrider/devkit/internal/taskhead"
	"github.com/dronrider/devkit/internal/taskstate"
)

type codexCheckOrder struct {
	Root, Home, ID, Model, Effort, Harness, Order string
}

type codexCheckStart struct {
	Started, Busy bool
	Message       string
}

func (c *shipCarrier) raiseCodex(p checkrun.Plan) checkrun.Report {
	id := p.Row.ID
	ctx, cancel := context.WithTimeout(context.Background(), checkRunTimeout)
	defer cancel()
	verdict, err := exec.CommandContext(ctx, "agentctl", "-C", c.root, "check", id).Output()
	if err != nil {
		return checkrun.Report{Failed: true, Line: fmt.Sprintf("%s: %s, agentctl check: %v", id, checkrun.NotRaised, err)}
	}
	fields := map[string]string{}
	for _, line := range strings.Split(string(verdict), "\n") {
		key, value, ok := strings.Cut(line, ": ")
		if ok {
			fields[key] = strings.TrimSpace(value)
		}
	}
	if fields["model"] != p.Choice.Model || fields["harness"] != p.Choice.Harness || fields["effort"] == "" {
		return checkrun.Report{Failed: true, Line: fmt.Sprintf("%s: %s, вердикт agentctl check изменился или не назвал effort", id, checkrun.NotRaised)}
	}
	order, _ := json.Marshal(codexCheckOrder{c.root, c.home, id, p.Choice.Model, fields["effort"], p.Choice.Harness, p.Order})
	exe, err := os.Executable()
	if err != nil {
		return checkrun.Report{Failed: true, Line: id + ": " + checkrun.NotRaised + ", " + err.Error()}
	}
	cmd := exec.Command(exe, "__check-codex", string(order))
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	pipe, err := cmd.StdoutPipe()
	if err == nil {
		err = cmd.Start()
	}
	if err != nil {
		return checkrun.Report{Failed: true, Line: id + ": " + checkrun.NotRaised + ", " + err.Error()}
	}
	type answer struct {
		start codexCheckStart
		err   error
	}
	answers := make(chan answer, 1)
	go func() { var s codexCheckStart; err := json.NewDecoder(pipe).Decode(&s); answers <- answer{s, err} }()
	var start codexCheckStart
	select {
	case a := <-answers:
		start, err = a.start, a.err
	case <-ctx.Done():
		_ = cmd.Process.Signal(syscall.SIGTERM)
		go func() { _ = cmd.Wait() }()
		return checkrun.Report{Failed: true, Line: fmt.Sprintf("%s: %s, Codex не начал ход за %s", id, checkrun.NotRaised, checkRunTimeout)}
	}
	if err != nil {
		_ = cmd.Wait()
		return checkrun.Report{Failed: true, Line: fmt.Sprintf("%s: %s, носитель не ответил: %v", id, checkrun.NotRaised, err)}
	}
	if !start.Started {
		_ = cmd.Wait()
		return checkrun.Report{Failed: !start.Busy, Line: fmt.Sprintf("%s: %s, %s", id, checkrun.NotRaised, start.Message)}
	}
	go func() { _ = cmd.Wait() }()
	return checkrun.Report{Raised: true, Line: p.Raised("Codex, " + start.Message)}
}

func codexCheckWorker(raw string) {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()
	var o codexCheckOrder
	reply := func(s codexCheckStart) { _ = json.NewEncoder(os.Stdout).Encode(s) }
	if err := json.Unmarshal([]byte(raw), &o); err != nil {
		reply(codexCheckStart{Message: err.Error()})
		return
	}
	if err := taskhead.GuardGroomRun(o.Root, os.Getenv("DEVKIT_GROOM_SOURCE"), os.Getenv("DEVKIT_GROOM_MODE"), o.ID); err != nil {
		reply(codexCheckStart{Message: err.Error()})
		return
	}
	if err := draftmeta.GuardRecord(o.Root, o.ID); err != nil {
		reply(codexCheckStart{Message: err.Error()})
		return
	}
	state, err := taskstate.Acquire(o.Home, o.Root, o.ID)
	if err != nil {
		reply(codexCheckStart{Message: err.Error()})
		return
	}
	defer state.Close()
	if state.State.Stopped {
		reply(codexCheckStart{Message: "задача остановлена пользователем"})
		return
	}
	tx, err := drafttx.Acquire(o.Root)
	if err != nil {
		reply(codexCheckStart{Message: err.Error()})
		return
	}
	if err = draftmeta.GuardRecordLocked(o.Root, o.ID); err != nil {
		tx.Close()
		reply(codexCheckStart{Message: err.Error()})
		return
	}
	hold, err := taskhead.HoldForDelegate(o.Home, o.ID, "")
	_ = tx.Close()
	if err != nil {
		_, busy := err.(*taskhead.BusyError)
		reply(codexCheckStart{Busy: busy, Message: err.Error()})
		return
	}
	defer hold.Release()
	logPath := filepath.Join(o.Home, ".devkit", "heads", o.ID+"-check.log")
	if err := os.MkdirAll(filepath.Dir(logPath), 0755); err != nil {
		reply(codexCheckStart{Message: err.Error()})
		return
	}
	log, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		reply(codexCheckStart{Message: err.Error()})
		return
	}
	defer log.Close()
	cmd := exec.CommandContext(ctx, "agentctl", "exec", "--harness", o.Harness, "--", "codex", "exec", "--json", "--approve-for-me", "--model", o.Model, "-c", "model_reasoning_effort=\""+o.Effort+"\"", o.Order)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.Dir = o.Root
	for _, env := range os.Environ() {
		if !strings.HasPrefix(env, "CLAUDE_CODE_SESSION_ID=") && !strings.HasPrefix(env, "CODEX_THREAD_ID=") && !strings.HasPrefix(env, "DEVKIT_CHAT_SESSION_ID=") && !strings.HasPrefix(env, "DEVKIT_TASK=") && !strings.HasPrefix(env, "DEVKIT_TMUX=") && !strings.HasPrefix(env, "DEVKIT_TASK_RUN_ID=") && !strings.HasPrefix(env, "DEVKIT_HEADLESS=") && !strings.HasPrefix(env, "DEVKIT_CLOUD_ACK=") && !strings.HasPrefix(env, taskhead.LockFromEnv+"=") {
			cmd.Env = append(cmd.Env, env)
		}
	}
	cmd.Env = append(cmd.Env, "DEVKIT_TASK="+o.ID, fmt.Sprintf("%s=%d", taskhead.LockFromEnv, os.Getpid()))
	cmd.Stderr = log
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		reply(codexCheckStart{Message: err.Error()})
		return
	}
	runID := fmt.Sprintf("check-%d-%d", os.Getpid(), time.Now().UnixNano())
	if err := state.Start(runID, o.Harness, o.Model, ""); err != nil {
		reply(codexCheckStart{Message: err.Error()})
		return
	}
	state.State.Run.PID, state.State.Run.Group = os.Getpid(), syscall.Getpgrp()
	if err := state.Save(); err != nil {
		reply(codexCheckStart{Message: err.Error()})
		return
	}
	if err := cmd.Start(); err != nil {
		reply(codexCheckStart{Message: err.Error()})
		return
	}
	_ = state.Close()
	scan := bufio.NewScanner(stdout)
	scan.Buffer(make([]byte, 4096), 8*1024*1024)
	thread, started := "", false
	var logErr error
	for scan.Scan() {
		line := scan.Bytes()
		if _, err := fmt.Fprintln(log, string(line)); err != nil && logErr == nil {
			logErr = fmt.Errorf("запись журнала Codex: %w", err)
			// Отдельный канал нужен после ответа check-run. Файл журнала
			// уже недоступен, а вызывающий процесс мог завершиться.
			logCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			_ = exec.CommandContext(logCtx, "logger", "-t", "shipctl", o.ID+": "+logErr.Error()).Run()
			cancel()
		}
		var event struct {
			Type     string `json:"type"`
			ThreadID string `json:"thread_id"`
		}
		if json.Unmarshal(line, &event) != nil {
			continue
		}
		if event.Type == "thread.started" {
			thread = event.ThreadID
		}
		if !started && event.Type == "turn.started" && thread != "" {
			started = true
			message := "поток " + thread + ", журнал " + logPath
			if logErr != nil {
				message += ", " + logErr.Error()
			}
			reply(codexCheckStart{Started: true, Message: message})
		}
	}
	streamErr := scan.Err()
	if streamErr != nil {
		streamErr = fmt.Errorf("чтение событий Codex: %w", streamErr)
		fmt.Fprintln(log, streamErr)
		_ = cmd.Cancel()
	}
	if err := cmd.Wait(); err != nil {
		fmt.Fprintf(log, "Codex завершился: %v\n", err)
	}
	if !started {
		message := "Codex не начал ход, журнал " + logPath
		if streamErr != nil {
			message += ", " + streamErr.Error()
		}
		if logErr != nil {
			message += ", " + logErr.Error()
		}
		reply(codexCheckStart{Message: message})
	}
}
