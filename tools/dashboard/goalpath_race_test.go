package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestGoalMessageReadsThroughRealLockedTaskctl(t *testing.T) {
	e, c, _ := tasksEnv(t)
	runTaskctl(t, e.proj, "add", "--id", "XR-100", "--title", "Цель: пробный цикл", "--rank", "25+9+3+0+4", "--cost", "XL", "--accept", "agent")
	goalDoc(t, e, "XR-100", goalDocBody)
	oldTimeout := procTimeout
	procTimeout = 4 * time.Second
	t.Cleanup(func() { procTimeout = oldTimeout })
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", e.srv.URL+"/api/projects/demo/goals/XR-100/message", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("real taskctl list blocked by project lock: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("goal read: %d %s", resp.StatusCode, body(t, resp))
	}
}

func TestGoalMessageRejectsChangedBoardWhileWaiting(t *testing.T) {
	e, c, _ := messagesEnv(t, "")
	signal := filepath.Join(e.home, "board-read")
	writeScript(t, e.bin, "taskctl", fmt.Sprintf("touch %q\necho '%s'", signal, runsBoardJSON))
	lock := e.s.inboxLock(e.proj)
	lock.Lock()
	done := make(chan int, 1)
	go func() {
		resp := postMessage(t, c, e, "XR-100", "гонка")
		resp.Body.Close()
		done <- resp.StatusCode
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(signal); err == nil {
			break
		}
		if time.Now().After(deadline) {
			lock.Unlock()
			t.Fatal("ожидание выбора пути")
		}
		time.Sleep(10 * time.Millisecond)
	}
	path := filepath.Join(e.proj, "docs", "TASKS.md")
	old, err := os.ReadFile(path)
	if err != nil {
		lock.Unlock()
		t.Fatal(err)
	}
	if err = os.WriteFile(path, append(old, []byte("\nновая редакция доски\n")...), 0644); err != nil {
		lock.Unlock()
		t.Fatal(err)
	}
	lock.Unlock()
	if status := <-done; status != 409 {
		t.Fatalf("status %d", status)
	}
	doc := readFile(t, filepath.Join(e.proj, "docs", "tasks", "XR-100.md"))
	if strings.Contains(doc, "гонка") {
		t.Fatal("изменён прежний файл цели")
	}
}

func TestTaskRelayRejectsChangedGoalPathWhileWaiting(t *testing.T) {
	e, c, _ := messagesEnv(t, "")
	goalWithTasks(t, e)
	signal := filepath.Join(e.home, "board-reads")
	writeScript(t, e.bin, "taskctl", fmt.Sprintf("echo read >> %q\necho '%s'", signal, runsBoardJSON))
	lock := e.s.inboxLock(e.proj)
	lock.Lock()
	done := make(chan int, 1)
	go func() {
		resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/tasks/XR-004/relay", `{"goal":"XR-100","text":"гонка пути"}`)
		resp.Body.Close()
		done <- resp.StatusCode
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		data, _ := os.ReadFile(signal)
		if strings.Contains(string(data), "read\n") {
			break
		}
		if time.Now().After(deadline) {
			lock.Unlock()
			t.Fatal("goal path was not selected")
		}
		time.Sleep(10 * time.Millisecond)
	}
	// The held inbox mutex keeps the handler between its board read and write.
	time.Sleep(100 * time.Millisecond)
	path := filepath.Join(e.proj, "docs", "TASKS.md")
	board, err := os.ReadFile(path)
	if err != nil {
		lock.Unlock()
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(board, []byte("\nновая редакция доски\n")...), 0644); err != nil {
		lock.Unlock()
		t.Fatal(err)
	}
	lock.Unlock()
	if status := <-done; status != 409 {
		t.Fatalf("status %d", status)
	}
	if strings.Contains(readFile(t, filepath.Join(e.proj, "docs", "tasks", "XR-100.md")), "гонка пути") {
		t.Fatal("old goal modified")
	}
}
