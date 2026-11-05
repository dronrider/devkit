package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/dronrider/devkit/internal/team"
)

// The routers are real pinned third-party executables. Only inference is synthetic.
func TestRealRouterStand(t *testing.T) {
	for _, router := range []struct{ name, env string }{{"llama-swap-v166", "DEVKIT_STAND_LLAMA_SWAP"}, {"litellm-1.63.14", "DEVKIT_STAND_LITELLM"}} {
		t.Run(router.name, func(t *testing.T) {
			binary := os.Getenv(router.env)
			if binary == "" {
				t.Skipf("NOT RUN: set %s to the pinned real router executable; see README", router.env)
			}
			if !filepath.IsAbs(binary) {
				t.Fatal("router executable must be absolute")
			}
			home := t.TempDir()
			var mu sync.Mutex
			localBodies, cloudBodies := []string{}, []string{}
			failLocal := false
			spy := func(local bool) *httptest.Server {
				return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method == http.MethodGet {
						w.Write([]byte(`{}`))
						return
					}
					if r.URL.Path != "/v1/chat/completions" {
						t.Errorf("unexpected upstream path %s", r.URL.Path)
					}
					raw, err := io.ReadAll(r.Body)
					if err != nil {
						t.Error(err)
					}
					mu.Lock()
					defer mu.Unlock()
					if local {
						localBodies = append(localBodies, string(raw))
					} else {
						cloudBodies = append(cloudBodies, string(raw))
					}
					w.Header().Set("Content-Type", "application/json")
					if local && failLocal {
						w.WriteHeader(503)
						w.Write([]byte(`{"error":{"message":"synthetic outage","type":"server_error"}}`))
						return
					}
					w.Write([]byte(`{"id":"synthetic","object":"chat.completion","created":1,"model":"synthetic","choices":[{"index":0,"message":{"role":"assistant","content":"synthetic response"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
				}))
			}
			local, cloud := spy(true), spy(false)
			defer local.Close()
			defer cloud.Close()
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			address := listener.Addr().String()
			listener.Close()
			_, port, _ := net.SplitHostPort(address)
			config := filepath.Join(home, "config.yaml")
			var text string
			var args []string
			if strings.HasPrefix(router.name, "llama-swap") {
				text = fmt.Sprintf("models:\n  local:\n    cmd: /bin/sleep 600\n    proxy: %s\n    checkEndpoint: none\n  cloud:\n    cmd: /bin/sleep 600\n    proxy: %s\n    checkEndpoint: none\n", local.URL, cloud.URL)
				args = []string{"-config", config, "-listen", address}
			} else {
				text = fmt.Sprintf("model_list:\n  - model_name: local\n    litellm_params:\n      model: openai/local\n      api_base: %s/v1\n      api_key: synthetic-only\n  - model_name: cloud\n    litellm_params:\n      model: openai/cloud\n      api_base: %s/v1\n      api_key: synthetic-only\nrouter_settings:\n  num_retries: 0\nlitellm_settings:\n  telemetry: false\n", local.URL, cloud.URL)
				args = []string{"--config", config, "--host", "127.0.0.1", "--port", port}
			}
			if err := os.WriteFile(config, []byte(text), 0600); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(binary, args...)
			cmd.Dir = home
			cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
			cmd.Env = []string{"HOME=" + home, "PATH=" + filepath.Dir(binary) + ":/usr/bin:/bin", "TMPDIR=" + home, "LITELLM_LOCAL_MODEL_COST_MAP=True", "LITELLM_TELEMETRY=False", "DO_NOT_TRACK=1"}
			log, err := os.Create(filepath.Join(home, "router.log"))
			if err != nil {
				t.Fatal(err)
			}
			defer log.Close()
			cmd.Stdout = log
			cmd.Stderr = log
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
				done := make(chan error, 1)
				go func() { done <- cmd.Wait() }()
				select {
				case <-done:
				case <-time.After(5 * time.Second):
					syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
					<-done
				}
			})
			endpoint := "http://" + address
			client := &http.Client{Timeout: time.Second, Transport: &http.Transport{Proxy: nil}}
			ready := false
			deadline := time.Now().Add(40 * time.Second)
			for time.Now().Before(deadline) {
				resp, err := client.Get(endpoint + "/v1/models")
				if err == nil {
					resp.Body.Close()
					if resp.StatusCode == 200 {
						ready = true
						break
					}
				}
				time.Sleep(100 * time.Millisecond)
			}
			if !ready {
				raw, _ := os.ReadFile(log.Name())
				t.Fatalf("real router not ready: %s", raw)
			}
			p := team.Participant{Handle: "stand", Kind: team.Agent, Model: "local", Tier: "base", Roles: []string{team.RoleExec}, Route: "local", Endpoint: endpoint + "/v1"}
			record := filepath.Join(home, "record.md")
			locked := []byte("# synthetic\nисточник: meeting\nзамок: locked\n\n## Текст\nDK1471_PRIVATE_TRIPWIRE")
			os.WriteFile(record, locked, 0600)
			team.Save(home, []team.Participant{p})
			var out, diag bytes.Buffer
			if err := cmdRoute(home, "stand", record, "", &out, &diag); err != nil {
				raw, _ := os.ReadFile(log.Name())
				t.Fatalf("real local route: %v; log %s", err, raw)
			}
			mu.Lock()
			localCount, cloudCount := len(localBodies), len(cloudBodies)
			privatePresent := localCount == 1 && strings.Contains(localBodies[0], "DK1471_PRIVATE_TRIPWIRE")
			mu.Unlock()
			if !privatePresent || cloudCount != 0 {
				t.Fatalf("locked: local=%d cloud=%d", localCount, cloudCount)
			}
			p.Route = "cloud"
			p.Model = "cloud"
			team.Save(home, []team.Participant{p})
			if err := cmdRoute(home, "stand", record, "", &out, &diag); err == nil {
				t.Fatal("locked accepted cloud")
			}
			public := bytes.Replace(locked, []byte("locked"), []byte("unlocked"), 1)
			public = bytes.Replace(public, []byte("meeting"), []byte("thought"), 1)
			os.WriteFile(record, public, 0600)
			mask := filepath.Join(home, "mask.json")
			raw, _ := json.Marshal([]string{"DK1471_PRIVATE_TRIPWIRE"})
			os.WriteFile(mask, raw, 0600)
			if err := cmdRoute(home, "stand", record, mask, &out, &diag); err != nil {
				t.Fatal(err)
			}
			mu.Lock()
			cloudCount = len(cloudBodies)
			masked := cloudCount == 1 && !strings.Contains(cloudBodies[0], "DK1471_PRIVATE_TRIPWIRE") && strings.Contains(cloudBodies[0], "[REDACTED]")
			mu.Unlock()
			if !masked {
				t.Fatalf("real cloud alias did not receive masked payload, calls=%d", cloudCount)
			}
			p.Route = "local"
			p.Model = "local"
			team.Save(home, []team.Participant{p})
			os.WriteFile(record, locked, 0600)
			mu.Lock()
			failLocal = true
			mu.Unlock()
			if err := cmdRoute(home, "stand", record, "", &out, &diag); err == nil {
				t.Fatal("outage accepted")
			}
			mu.Lock()
			cloudCount = len(cloudBodies)
			localCount = len(localBodies)
			mu.Unlock()
			if localCount < 2 {
				t.Fatal("outage never reached real local upstream")
			}
			if cloudCount != 1 {
				t.Fatal("local outage fell back to cloud")
			}
			t.Logf("REAL ROUTER %s: locked->local observed; locked->cloud refused; unlocked masked->cloud observed; local outage->no cloud observed", router.name)
		})
	}
}
