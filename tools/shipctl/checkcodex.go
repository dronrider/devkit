package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
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
		why := err.Error()
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			if stderr := strings.Join(strings.Fields(string(exit.Stderr)), " "); stderr != "" {
				why = stderr
			}
		}
		return checkrun.Report{Failed: true, Line: fmt.Sprintf("%s: %s, agentctl check: %s", id, checkrun.NotRaised, why)}
	}
	fields := map[string]string{}
	for _, line := range strings.Split(string(verdict), "\n") {
		key, value, ok := strings.Cut(line, ": ")
		if ok {
			fields[key] = strings.TrimSpace(value)
		}
	}
	var invalid []string
	if fields["model"] != p.Choice.Model {
		invalid = append(invalid, "model")
	}
	if fields["harness"] != p.Choice.Harness {
		invalid = append(invalid, "harness")
	}
	if fields["effort"] == "" {
		invalid = append(invalid, "effort")
	}
	if len(invalid) != 0 {
		return checkrun.Report{Failed: true, Line: fmt.Sprintf("%s: %s, поля вердикта изменились или пусты: %s. Повторите `agentctl check %s` перед новым запуском",
			id, checkrun.NotRaised, strings.Join(invalid, ", "), id)}
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
		key, _, _ := strings.Cut(env, "=")
		switch key {
		case "CLAUDE_CODE_SESSION_ID", "CODEX_THREAD_ID", "DEVKIT_CHAT_SESSION_ID",
			"DEVKIT_TASK", "DEVKIT_TMUX", "DEVKIT_TASK_RUN_ID", "DEVKIT_HEADLESS",
			"DEVKIT_RUN_DEPTH", "DEVKIT_PARENT_SESSION", "DEVKIT_TASK_HEAD_SESSION",
			"DEVKIT_TASK_HEAD_SESSION_ARGS", "DEVKIT_EFFORT", "DEVKIT_GOAL_SHELL",
			"DEVKIT_PLAN_LABEL", "DEVKIT_RECOVERY_ATTEMPT", "DEVKIT_CLOUD_ACK", taskhead.LockFromEnv:
			continue
		}
		cmd.Env = append(cmd.Env, env)
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
			sinkErr := exec.CommandContext(logCtx, "logger", "-t", "shipctl", o.ID+": "+logErr.Error()).Run()
			cancel()
			if sinkErr != nil {
				diagnostic := "check-run " + o.ID + ": " + logErr.Error() + ", logger: " + sinkErr.Error()
				diagnostic = strings.NewReplacer("\t", " ", "\r", " ", "\n", " ").Replace(diagnostic)
				if err := appendRunLog(o.Root, diagnostic, 1); err != nil {
					message := fmt.Sprintf("%s, журнал проекта: %v\n", diagnostic, err)
					if err := os.WriteFile(logPath+".error", []byte(message), 0o600); err != nil {
						logErr = fmt.Errorf("%s, резервный журнал: %w", strings.TrimSpace(message), err)
					}
				}
			}
		}
		var event struct {
			Type     string `json:"type"`
			ThreadID string `json:"thread_id"`
		}
		if json.Unmarshal(line, &event) != nil {
			continue
		}
		if event.Type == "thread.started" {
			thread = strings.TrimSpace(event.ThreadID)
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
