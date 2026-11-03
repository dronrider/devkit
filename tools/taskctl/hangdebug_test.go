package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/dronrider/devkit/internal/draftproposal"
)

func TestHangDebugPreparedAsk(t *testing.T) {
	root := setup(t)
	kit, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEVKIT_HOME", kit)
	for _, id := range []string{"XR-008", "XR-009"} {
		if _, err = cmdDraft(root, "материал "+id, "mid", CommitOpts{}); err != nil {
			t.Fatal(err)
		}
		raw, err := cmdDraftPrepared(root, id)
		if err != nil {
			t.Fatal(err)
		}
		var view preparedView
		if err = json.Unmarshal([]byte(raw), &view); err != nil {
			t.Fatal(err)
		}
		cards := []draftproposal.Proposal{{ID: "p1", Revision: 1, Outcome: "drop", Text: "частная мысль", Reason: "лишнее"}}
		if id == "XR-008" {
			cards = append(cards, draftproposal.Proposal{ID: "p2", Revision: 1, Outcome: "add", Title: "Результат", Text: fixtureTaskBody, BoardType: "task", Rank: "0+1+1+0+1", Size: "M", Accept: "agent", After: []string{"p1"}})
		}
		save, _ := json.Marshal(struct {
			Proposals []draftproposal.Proposal `json:"proposals"`
		}{cards})
		if _, err = cmdDraftPreparedSave(root, id, view.Snapshot, save); err != nil {
			t.Fatal(err)
		}
	}
	bin := filepath.Join(t.TempDir(), "taskctl")
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Env = append(os.Environ(), "GOWORK=off")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, out)
	}
	cmd := exec.Command(bin, "-C", root, "draft", "prepared", "ask", "XR-008", "--also", "XR-009")
	cmd.Env = append(os.Environ(), "CLAUDE_CODE_SESSION_ID=cli-stand", "DEVKIT_TMUX=task-stand")
	cmd.Stdin = bytes.NewReader(nil)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		t.Logf("ребёнок кончился сам: %v\nвывод:\n%s", err, buf.String())
	case <-time.After(15 * time.Second):
		if err := cmd.Process.Signal(syscall.SIGQUIT); err != nil {
			t.Logf("SIGQUIT не ушёл: %v", err)
		}
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			cmd.Process.Kill()
		}
		t.Errorf("ребёнок висит, дамп:\n%s", buf.String())
	}
	if !strings.Contains(t.TempDir(), "x") {
		t.Log("фикстура в", root)
	}
}
