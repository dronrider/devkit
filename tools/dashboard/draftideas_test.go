package main

import (
	"encoding/json"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dronrider/devkit/internal/draftproposal"
)

func TestDraftThoughtsBeforeGroomHTTP(t *testing.T) {
	// Доска и сервер одни на все случаи: случай заводит свой черновик POST
	// записи и разбирает его номер из ответа, поэтому случаи не встречаются
	// на одной записи накопителя.
	e, c, _ := tasksEnv(t)
	base := draftThoughtsServer(t, e)
	for _, tc := range []struct {
		name    string
		state   draftproposal.State
		legacy  string
		allowed bool
	}{
		{name: "raw", allowed: true},
		{name: "incomplete", state: draftproposal.State{Version: 1, State: "active", PackRevision: 1}, allowed: true},
		{name: "first-error", state: draftproposal.State{Version: 1, State: "active", Analysis: "error"}, allowed: true},
		{name: "empty", state: draftproposal.State{Version: 1, State: "active", Analysis: "empty"}},
		{name: "conclusion", state: draftproposal.State{Version: 1, State: "active", Conclusion: "Сохранённый разбор"}},
		{name: "proposals", state: draftproposal.State{Version: 1, State: "active", Proposals: []draftproposal.Proposal{{ID: "p", Revision: 1, Outcome: "drop", Reason: "лишнее"}}}},
		{name: "retry-error", state: draftproposal.State{Version: 1, State: "active", Analysis: "error", Conclusion: "Прежний разбор"}},
		{name: "legacy", legacy: "\n## Грумминг\n\nОтложено прежде.\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := doReq(t, c, "POST", base+"/api/projects/demo/drafts", `{"text":"Исходник","prio":"mid"}`)
			if resp.StatusCode != http.StatusOK {
				t.Fatal(body(t, resp))
			}
			var created struct {
				ID string `json:"id"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&created); err != nil || created.ID == "" {
				t.Fatalf("номер черновика не разобран из ответа записи: %v", err)
			}
			resp.Body.Close()
			id := created.ID
			url := base + "/api/projects/demo/drafts/" + id
			resp = doReq(t, c, "POST", url+"/ideas", `{"text":"Старое замечание"}`)
			if resp.StatusCode != http.StatusOK {
				t.Fatal(body(t, resp))
			}
			resp.Body.Close()
			path, _ := draftPathOf(e.proj, id)
			if tc.state.Version != 0 {
				if err := draftproposal.SavePrepared(e.proj, id, tc.state); err != nil {
					t.Fatal(err)
				}
			}
			if tc.legacy != "" {
				putDoc(t, e.proj, draftFileRel(id), readFile(t, path)+tc.legacy)
			}
			before := readFile(t, path)
			resp = doReq(t, c, "GET", url, "")
			var view struct {
				Text    string `json:"text"`
				Allowed bool   `json:"ideas_allowed"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&view); err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if view.Allowed != tc.allowed || !strings.Contains(view.Text, "Старое замечание") {
				t.Fatalf("view: %+v", view)
			}
			resp = doReq(t, c, "POST", url+"/ideas", `{"text":"Позднее замечание"}`)
			raw := body(t, resp)
			if tc.allowed {
				if resp.StatusCode != http.StatusOK || !strings.Contains(readFile(t, path), "Позднее замечание") {
					t.Fatalf("append %d %s", resp.StatusCode, raw)
				}
			} else {
				if resp.StatusCode < 400 || !strings.Contains(raw, "Чате разбора") {
					t.Fatalf("stale form %d %s", resp.StatusCode, raw)
				}
				if readFile(t, path) != before {
					t.Fatal("stale form changed source")
				}
			}
		})
	}
}

func TestDraftThoughtsBeforeGroomNode(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node недоступен")
	}
	out, err := exec.Command(node, filepath.Join("testdata", "poc_draftchatentry.mjs"), filepath.Join("static", "app.js")).CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
}

func draftThoughtsServer(t *testing.T, e *testEnv) string {
	t.Helper()
	bin := os.Getenv("DASHBOARD_ACCEPT_BIN")
	if bin == "" {
		return e.srv.URL
	}
	if !filepath.IsAbs(bin) {
		t.Fatal("DASHBOARD_ACCEPT_BIN должен быть абсолютным путём")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	_, port, _ := net.SplitHostPort(address)
	listener.Close()
	putDoc(t, e.home, ".devkit/dashboard.local", "root = "+filepath.Dir(e.proj)+"\naddr = 127.0.0.1\nport = "+port+"\ntoken = test-token\n")
	if err := os.Chmod(filepath.Join(e.home, ".devkit/dashboard.local"), 0600); err != nil {
		t.Fatal(err)
	}
	log, err := os.Create(filepath.Join(t.TempDir(), "dashboard.log"))
	if err != nil {
		t.Fatal(err)
	}
	server := exec.Command(bin, "serve")
	server.Dir = e.proj
	server.Env = os.Environ()
	server.Stdout, server.Stderr = log, log
	if err := server.Start(); err != nil {
		log.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { server.Process.Kill(); server.Wait(); log.Close() })
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
	return base
}
