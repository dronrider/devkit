package main

import (
	"context"
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
)

func TestScenarioBrowserEmbedded(t *testing.T) {
	chrome := findChrome()
	if chrome == "" {
		t.Skip("браузер стенда не найден; задать DASHBOARD_CHROME")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(node, "-e", "require.resolve('playwright')").CombinedOutput(); err != nil {
		if os.Getenv("DASHBOARD_ACCEPT_BIN") != "" {
			t.Fatalf("playwright недоступен; задать NODE_PATH: %s", out)
		}
		t.Skip("playwright недоступен; задать NODE_PATH")
	}
	st := newScenarioStand(t)
	st.ask(t)
	bin := os.Getenv("DASHBOARD_ACCEPT_BIN")
	if bin == "" {
		bin = filepath.Join(filepath.Dir(st.bin), "dashboard")
		cmd := exec.Command("go", "build", "-o", bin, ".")
		cmd.Env = append(os.Environ(), "GOWORK=off")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("сборка dashboard: %v\n%s", err, out)
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
	if err := os.WriteFile(filepath.Join(st.e.home, ".devkit/dashboard.local"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	// И установленный сервер находит настоящий CLI, фикстуры прочих утилит остаются в PATH.
	writeScript(t, st.e.bin, "taskctl", "exec '"+strings.ReplaceAll(st.bin, "'", "'\\''")+"' \"$@\"")
	log, err := os.Create(filepath.Join(t.TempDir(), "dashboard.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	cmd := exec.Command(bin, "serve")
	cmd.Dir = st.e.proj
	cmd.Env = os.Environ()
	cmd.Stdout, cmd.Stderr = log, log
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
	base := "http://" + address
	ctx, stop := context.WithTimeout(context.Background(), 15*time.Second)
	defer stop()
	for {
		req, _ := http.NewRequestWithContext(ctx, "GET", base+"/healthz", nil)
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			resp.Body.Close()
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("сервер не поднялся: " + readFile(t, log.Name()))
		case <-time.After(30 * time.Millisecond):
		}
	}
	target, _ := url.Parse(base)
	proxy := httputil.NewSingleHostReverseProxy(target)
	mux := http.NewServeMux()
	mux.HandleFunc("/__scenario", func(w http.ResponseWriter, r *http.Request) {
		var err error
		switch r.URL.Query().Get("do") {
		case "resume":
			doc := readFile(t, st.path)
			cards := "### СЦ-4\n\n"
			for _, field := range []string{"Ситуация", "Действие", "Результат", "Польза", "Рекомендация", "Ограничение", "Источник"} {
				cards += field + ": свой сценарий\n"
			}
			doc = strings.Replace(doc, "## Развилки", cards+"\n## Развилки", 1)
			err = os.WriteFile(st.path, []byte(doc), 0o644)
			st.run(t, "--ask", "срок", "--hint", "30 дней: достаточно", "сколько хранить?")
			st.run(t, "срок", "--by", "человек", "30 дней")
			doc = readFile(t, st.path) + "  - требует сверки 2026-10-28: сверить после перерыва\n"
			err = os.WriteFile(st.path, []byte(doc), 0o644)
			st.ask(t)
		case "reorder":
			doc := readFile(t, st.path)
			a := "вариант: СЦ-1 (слово пользователя): выбрать"
			b := "вариант: СЦ-2 (вывод из примера): собрать"
			doc = strings.Replace(doc, a, "вариант: СЦ-99 (слово пользователя): временно", 1)
			doc = strings.Replace(doc, b, a, 1)
			doc = strings.Replace(doc, "вариант: СЦ-99 (слово пользователя): временно", b, 1)
			err = os.WriteFile(st.path, []byte(doc), 0o644)
		case "ordinary":
			st.run(t, "--ask", "срок", "--hint", "30 дней: достаточно", "--option", "7 дней", "сколько хранить?")
			st.run(t, "--ask", "вывод", "--hint", "полный: нужен", "--option", "краткий", "какой вывод?")
			st.publish(t, false)
		case "fresh":
			st.ask(t)
		}
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		waiting, _ := os.ReadFile(filepath.Join(st.e.proj, ".devkit/chat/task-XR-4.ask"))
		writeJSON(w, 200, map[string]string{"doc": readFile(t, st.path), "waiting": string(waiting)})
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
	probe := exec.Command(node, "testdata/scenario_browser.mjs", px.URL, chrome, st.e.cfg.Token)
	probe.Dir = "."
	probe.Env = os.Environ()
	out, err := probe.CombinedOutput()
	if err != nil {
		t.Fatalf("живой браузер со вшитой статикой: %v\n%s\nсервер:\n%s", err, out, readFile(t, log.Name()))
	}
	var got map[string]any
	if json.Unmarshal(out, &got) != nil {
		t.Fatalf("итог браузера: %s", out)
	}
	t.Logf("вшитый бинарь %s, CLI %s: %s", bin, st.bin, out)
}
