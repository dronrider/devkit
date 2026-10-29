package main

import (
	"encoding/json"
	"fmt"
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
)

func TestProposalBrowserRealProcesses(t *testing.T) {
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
			t.Fatalf("playwright: %v %s", err, out)
		}
		t.Skip("нет playwright")
	}
	st := newScenarioStand(t)
	kit, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEVKIT_HOME", kit)
	putDoc(t, st.e.proj, "docs/TASKS-archive.md", "# Архив\n\n| ID | Задача | Тип | P | Закрыто | Ссылка |\n|--------|--------|-----|---|---------|--------|\n")
	cli := func(input string, args ...string) (string, error) {
		cmd := exec.Command(st.bin, append([]string{"-C", st.e.proj}, args...)...)
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
	raw, err = cli(`{"proposals":[{"id":"p1","revision":1,"outcome":"drop","text":"карточка один","reason":"устарело"},{"id":"p2","revision":1,"outcome":"drop","text":"карточка два","reason":"лишнее"}]}`, "draft", "proposals", "save", "XR-4", "--base", view.Snapshot)
	if err != nil {
		t.Fatal(raw, err)
	}
	publish := func() error {
		out, err := cli("", "draft", "proposals", "ask", "XR-4")
		if err != nil {
			return fmt.Errorf("%s: %w", out, err)
		}
		return nil
	}
	if err = publish(); err != nil {
		t.Fatal(err)
	}
	bin := os.Getenv("DASHBOARD_ACCEPT_BIN")
	if bin == "" {
		bin = filepath.Join(t.TempDir(), "dashboard")
		cmd := exec.Command("go", "build", "-o", bin, ".")
		cmd.Env = append(os.Environ(), "GOWORK=off")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("build: %s %v", out, err)
		}
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	_, port, _ := net.SplitHostPort(address)
	listener.Close()
	cfg := "root = " + filepath.Dir(st.e.proj) + "\naddr = 127.0.0.1\nport = " + port + "\ntoken = test-token\n"
	if err = os.WriteFile(filepath.Join(st.e.home, ".devkit/dashboard.local"), []byte(cfg), 0600); err != nil {
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
	mux.HandleFunc("/__proposal", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("fresh") == "1" {
			if err := publish(); err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
		}
		out, err := cli("", "draft", "proposals", "XR-4")
		if err != nil {
			http.Error(w, out, 500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, out)
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
	probe := exec.Command(node, "testdata/proposal_browser.mjs", px.URL, chrome, "test-token", scenarioPanelSID)
	probe.Env = os.Environ()
	if out, err := probe.CombinedOutput(); err != nil {
		t.Fatalf("browser: %v\n%s\nserver: %s", err, out, readFile(t, log.Name()))
	} else {
		t.Logf("%s", out)
	}
}
