package main

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dronrider/devkit/internal/chat"
	"github.com/dronrider/devkit/internal/draftproposal"
	"github.com/dronrider/devkit/internal/sessions"
)

func TestProposalWorktreeBrowserRealProcesses(t *testing.T) {
	t.Run("linked_refusal", func(t *testing.T) { proposalWorktreeBrowser(t, true) })
	t.Run("primary_retry", func(t *testing.T) { proposalWorktreeBrowser(t, false) })
}

func proposalWorktreeBrowser(t *testing.T, linked bool) {
	chrome := findChrome()
	if chrome == "" {
		if os.Getenv("DASHBOARD_ACCEPT_BIN") != "" || os.Getenv("TASKCTL_ACCEPT_BIN") != "" {
			t.Fatal("нет браузера")
		}
		t.Skip("нет браузера")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(node, "-e", "require.resolve('playwright')").CombinedOutput(); err != nil {
		if os.Getenv("DASHBOARD_ACCEPT_BIN") != "" || os.Getenv("TASKCTL_ACCEPT_BIN") != "" {
			t.Fatalf("playwright: %s %v", out, err)
		}
		t.Skip("нет playwright")
	}
	st := newScenarioStand(t)
	putDoc(t, st.e.proj, "docs/TASKS-archive.md", "# Архив\n\n| ID | Задача | Тип | P | Закрыто | Ссылка |\n|--------|--------|-----|---|---------|--------|\n")
	kit, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEVKIT_HOME", kit)
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-c", "user.name=Stand", "-c", "user.email=stand@example.invalid", "-C", st.e.proj}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s %v", args, out, err)
		}
	}
	git("init")
	git("add", "--", "docs")
	git("commit", "-m", "synthetic fixture")
	tree := filepath.Join(filepath.Dir(st.e.proj), "demo-branch")
	git("worktree", "add", "-b", "stand", tree)
	before := readFile(t, st.path)
	root := st.e.proj
	if linked {
		root = tree
	}
	cli := func(input string, args ...string) (string, error) {
		cmd := exec.Command(st.bin, append([]string{"-C", root}, args...)...)
		cmd.Env = append(os.Environ(), "CLAUDE_CODE_SESSION_ID="+scenarioPanelSID, "DEVKIT_TMUX=task-test")
		cmd.Stdin = strings.NewReader(input)
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	raw, err := cli("", "draft", "proposals", "XR-4")
	if err != nil {
		t.Fatal(raw, err)
	}
	var view struct {
		Snapshot string `json:"snapshot"`
	}
	if err = json.Unmarshal([]byte(raw), &view); err != nil {
		t.Fatal(err)
	}
	material := "## Что происходит\n\nветочный результат\n\n## Чего хотим\n\nПроверка выполняется до записи.\n\n## DoD\n\n- Неполная запись отклоняется.\n\n## Ранг\n\n`0+1+1+0+1 = 3`, P3.\n\n- Серьёзность 0: работа продолжается.\n- Ценность 1: касается одного сценария.\n- Неопределённость 1: место правки известно.\n- Поправка на баг 0: новая работа.\n- Рычаг 1: убирает повторный разбор.\n"
	payload, _ := json.Marshal(map[string]any{"proposals": []draftproposal.Proposal{{ID: "p1", Revision: 1, Outcome: "add", Title: "Результат", Text: material, BoardType: "task", Rank: "0+1+1+0+1", Size: "M", Accept: "agent"}}})
	raw, err = cli(string(payload), "draft", "proposals", "save", "XR-4", "--base", view.Snapshot)
	if err != nil {
		t.Fatal(raw, err)
	}
	raw, err = cli("", "draft", "proposals", "ask", "XR-4")
	if err != nil {
		t.Fatal(raw, err)
	}
	writeBinds(t, st.e.home, "2026-10-30T12:00:00 сессия "+scenarioPanelSID+" задача XR-4 проект demo дерево "+tree+" транскрипт "+standTranscript(st.e.home, "t")+" источник заказ повод startup tmux -\n")
	other := filepath.Join(filepath.Dir(st.e.proj), "other")
	mkProject(t, other)
	putDoc(t, other, "docs/tasks/XR-4.md", "# XR-4: другая задача\n")
	bin := os.Getenv("DASHBOARD_ACCEPT_BIN")
	if bin == "" {
		bin = filepath.Join(t.TempDir(), "dashboard")
		cmd := exec.Command("go", "build", "-o", bin, ".")
		cmd.Env = append(os.Environ(), "GOWORK=off")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("dashboard build: %s %v", out, err)
		}
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	_, port, _ := net.SplitHostPort(address)
	listener.Close()
	config := "root = " + filepath.Dir(st.e.proj) + "\naddr = 127.0.0.1\nport = " + port + "\ntoken = test-token\n"
	if err = os.WriteFile(filepath.Join(st.e.home, ".devkit/dashboard.local"), []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	writeScript(t, st.e.bin, "taskctl", "exec '"+strings.ReplaceAll(st.bin, "'", "'\\''")+"' \"$@\"")
	log, err := os.Create(filepath.Join(t.TempDir(), "dashboard.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	server := exec.Command(bin, "serve")
	server.Dir = st.e.proj
	server.Env = os.Environ()
	server.Stdout = log
	server.Stderr = log
	if err = server.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { server.Process.Kill(); server.Wait() })
	base := "http://" + address
	deadline := time.Now().Add(15 * time.Second)
	for {
		resp, err := http.Get(base + "/healthz")
		if err == nil {
			resp.Body.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal(readFile(t, log.Name()))
		}
		time.Sleep(30 * time.Millisecond)
	}
	target, _ := url.Parse(base)
	proxy := httputil.NewSingleHostReverseProxy(target)
	mux := http.NewServeMux()
	mux.HandleFunc("/__worktree", func(w http.ResponseWriter, r *http.Request) {
		carrier := chat.Path(st.e.proj, chat.TaskName("XR-4"))
		switch r.URL.Query().Get("mode") {
		case "break":
			if err := os.Mkdir(carrier, 0700); err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
		case "rebind":
			if err := os.Remove(carrier); err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			if err := os.Remove(chat.AskPath(st.e.proj, chat.TaskName("XR-4"))); err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			line := "2026-10-30T12:01:00 сессия " + scenarioPanelSID + " задача XR-4 проект other дерево " + other + " транскрипт " + standTranscript(st.e.home, "t") + " источник заказ повод startup tmux -\n"
			if err := os.WriteFile(sessions.Path(st.e.home), []byte(line), 0644); err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
		case "state":
			source := filepath.Join(root, "docs", "tasks", "drafts", "XR-4.md")
			body, err := os.ReadFile(source)
			if err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			state, err := draftproposal.Parse(body)
			if err != nil || len(state.Proposals) == 0 {
				http.Error(w, "state", 500)
				return
			}
			main, _ := os.ReadFile(st.path)
			lines := chat.ReadLines(carrier)
			old := len(lines) > 0 && strings.Contains(lines[len(lines)-1], `"results":`)
			otherLines := chat.ReadLines(chat.Path(other, chat.TaskName("XR-4")))
			rows := 0
			if receipt := state.Proposals[0].Result; receipt != nil && len(receipt.IDs) == 1 {
				board, _ := os.ReadFile(filepath.Join(root, "docs", "TASKS.md"))
				rows = strings.Count(string(board), "| "+receipt.IDs[0]+" |")
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{"status": state.Proposals[0].Status, "mainUnchanged": string(main) == before, "oldCarrier": old, "otherCarrier": len(otherLines) > 0, "rows": rows})
		default:
			http.Error(w, "mode", 400)
		}
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.Header.Get("Accept"), "text/event-stream") {
			w.WriteHeader(204)
			return
		}
		proxy.ServeHTTP(w, r)
	})
	px := httptest.NewServer(mux)
	defer px.Close()
	mode := "primary"
	if linked {
		mode = "linked"
	}
	probe := exec.Command(node, "testdata/proposal_worktree_browser.mjs", px.URL, chrome, "test-token", scenarioPanelSID, mode)
	probe.Env = os.Environ()
	if out, err := probe.CombinedOutput(); err != nil {
		t.Fatalf("browser: %v\n%s\nserver: %s", err, out, readFile(t, log.Name()))
	} else {
		t.Logf("%s", out)
	}
}
