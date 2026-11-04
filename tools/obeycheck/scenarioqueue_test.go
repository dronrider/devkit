package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/dronrider/devkit/internal/draftproposal"
)

func TestQueueDraftStartScenarioCheck(t *testing.T) {
	devkit := devkitRoot(t)
	s, err := readScenario(filepath.Join(devkit, "tools", "obeycheck", "scenarios", "71-queue-draft-start.md"))
	if err != nil {
		t.Fatal(err)
	}
	bin := os.Getenv("TASKCTL_ACCEPT_BIN")
	if bin == "" {
		bin = filepath.Join(t.TempDir(), "taskctl")
		build := exec.Command("go", "build", "-o", bin, ".")
		build.Dir = filepath.Join(devkit, "tools", "taskctl")
		build.Env = append(os.Environ(), "GOWORK=off")
		if out, err := build.CombinedOutput(); err != nil {
			t.Fatalf("build taskctl: %v\n%s", err, out)
		}
	}
	bin, err = filepath.Abs(bin)
	if err != nil {
		t.Fatal(err)
	}
	binDir := t.TempDir()
	if err := os.Symlink(bin, filepath.Join(binDir, "taskctl")); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, missing, resultID, wantFailure string
	}{
		{name: "confirmed complete result", resultID: "OB-072"},
		{name: "stderr requirement lost", missing: "EXPORTED_ROWS=2", resultID: "OB-072", wantFailure: "DoD потерял часть пункта из export-review"},
		{name: "receipt points to another task", resultID: "OB-999", wantFailure: "нет подтверждённой карточки для строки"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			project := t.TempDir()
			origin := filepath.Join(t.TempDir(), "origin.git")
			env := append(os.Environ(),
				"HOME="+t.TempDir(), "PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
				"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull,
				"GIT_AUTHOR_NAME=obeycheck", "GIT_AUTHOR_EMAIL=obeycheck@example.invalid",
				"GIT_COMMITTER_NAME=obeycheck", "GIT_COMMITTER_EMAIL=obeycheck@example.invalid",
				"DEVKIT_HEADLESS=obeycheck", "DEVKIT_TMUX=", "DEVKIT_RUN_DEPTH=",
				"CLAUDE_CODE_SESSION_ID=", "OBEY_PROJECT="+project, "OBEY_DEVKIT="+devkit, "DEVKIT_HOME="+devkit)
			run := func(name string, args ...string) string {
				t.Helper()
				cmd := exec.Command(name, args...)
				cmd.Dir, cmd.Env = project, env
				out, err := cmd.CombinedOutput()
				if err != nil {
					t.Fatalf("%s %v: %v\n%s", name, args, err, out)
				}
				return string(out)
			}
			write := func(rel string, data []byte) {
				t.Helper()
				path := filepath.Join(project, rel)
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, data, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			run("git", "init", "-q", "-b", "main")
			run("git", "config", "core.hooksPath", "/dev/null")
			run("git", "config", "commit.gpgsign", "false")
			run("git", "init", "-q", "--bare", "-b", "main", origin)
			run("git", "remote", "add", "origin", origin)
			run(bin, "init", "--prefix", "OB")
			run("git", "add", "docs")
			run("git", "commit", "-q", "-m", "test: initial board")
			run("sh", "-eu", "-c", s.Setup)
			run("git", "switch", "-q", "export-review")
			write(".devkit/log", nil)
			run(bin, "guard", "OB-071")
			run(bin, "show", "OB-071")

			boardPath := filepath.Join(project, "docs", "TASKS.md")
			board, err := os.ReadFile(boardPath)
			if err != nil {
				t.Fatal(err)
			}
			row := "| OB-072 | Выгружать обе записи | bug | P2 | 41 (25+5+1+5+3, S+2) | S | [tasks/OB-072.md](tasks/OB-072.md) |\n"
			if strings.Count(string(board), "\n## Blocked") != 1 {
				t.Fatal("unexpected taskctl init board skeleton")
			}
			write("docs/TASKS.md", []byte(strings.Replace(string(board), "\n## Blocked", "\n"+row+"\n## Blocked", 1)))
			card := "# OB-072: Выгружать обе записи\n\n[Черновик OB-071](drafts/OB-071.md).\n\n## DoD\n\n- stdout содержит first,1 и second,2 во входном порядке.\n- stderr содержит EXPORTED_ROWS=2; stdout содержит только две строки CSV.\n"
			if tc.missing != "" {
				card = strings.ReplaceAll(card, tc.missing, "счётчик")
			}
			write("docs/tasks/OB-072.md", []byte(card))
			state := draftproposal.State{Version: 1, State: "history",
				Proposals: []draftproposal.Proposal{{ID: "p1", Revision: 1, Outcome: "add", Status: "accepted", Title: "Выгружать обе записи", Text: card}},
				Receipts:  []draftproposal.Receipt{{Source: "OB-071", Key: "fixture-confirmation", Proposal: "p1", Revision: 1, Outcome: "add", IDs: []string{tc.resultID}, Result: "задача оформлена"}},
			}
			prepared, err := draftproposal.RenderPrepared(state)
			if err != nil {
				t.Fatal(err)
			}
			write("docs/tasks/prepared/OB-071.md", prepared)
			var view struct {
				State draftproposal.State `json:"state"`
			}
			if err := json.Unmarshal([]byte(run(bin, "draft", "prepared", "OB-071")), &view); err != nil {
				t.Fatal(err)
			}
			if view.State.State != "history" || len(view.State.Proposals) != 1 || view.State.Proposals[0].Status != "accepted" || view.State.Proposals[0].Result == nil || !reflect.DeepEqual(view.State.Proposals[0].Result.IDs, []string{tc.resultID}) {
				t.Fatalf("real prepared CLI did not return fixture receipt: %+v", view.State)
			}
			check := exec.Command("sh", "-eu", "-c", s.Check)
			check.Dir, check.Env = project, env
			out, checkErr := check.CombinedOutput()
			if tc.wantFailure == "" {
				if checkErr != nil {
					t.Fatalf("complete accepted result rejected: %v\n%s", checkErr, out)
				}
			} else if checkErr == nil || !strings.Contains(string(out), tc.wantFailure) {
				t.Fatalf("want checker failure %q; got %v\n%s", tc.wantFailure, checkErr, out)
			}
		})
	}
}
