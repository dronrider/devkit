package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

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
