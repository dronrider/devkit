package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dronrider/devkit/internal/stage"
	"github.com/dronrider/devkit/internal/team"
)

func TestRoutePolicy(t *testing.T) {
	p := team.Participant{Kind: team.Agent, Model: "synthetic", Roles: []string{team.RoleExec}, Route: "cloud"}
	for _, text := range []string{"# запись\nзамок: locked\n\n## Текст\ntripwire", "# запись\nзащита: locked\n\n## Текст\ntripwire", "# запись\nзамок: broken\n", "# запись\nисточник: unknown\n", "# запись\nисточник mail\n", "# запись\nисточник: meeting\nзамок: unlocked\n", "# запись\nисточник: mail\n", "# запись\nисточник: messenger\n", "# запись\nзамок: locked\nзамок: unlocked\n"} {
		if raw, err := routePayload([]byte(text), p, []string{"tripwire"}); err == nil || raw != nil {
			t.Fatalf("private payload escaped: %q %v", raw, err)
		}
	}
	p.Route = "local"
	raw, err := routePayload([]byte("# запись\nзамок: locked\n\n## Текст\ntripwire"), p, []string{"tripwire"})
	if err != nil || !bytes.Contains(raw, []byte("tripwire")) {
		t.Fatalf("local: %s %v", raw, err)
	}
	p.Route = "cloud"
	raw, err = routePayload([]byte("# запись\nисточник: thought\n\n## Текст\nalice@example.invalid and alice"), p, []string{"alice", "alice@example.invalid"})
	if err != nil || bytes.Contains(raw, []byte("alice")) || bytes.Contains(raw, []byte("example.invalid")) {
		t.Fatalf("mask: %s %v", raw, err)
	}
}

func TestRouteURL(t *testing.T) {
	for _, endpoint := range []string{"https://127.0.0.1/v1", "http://localhost/v1", "http://127.0.0.1.evil.invalid/v1", "http://192.0.2.1/v1", "http://u:p@127.0.0.1/v1", "http://127.0.0.1/v1?x=y"} {
		if _, err := routeURL(team.Participant{Route: "local", Endpoint: endpoint}); err == nil {
			t.Fatalf("accepted %s", endpoint)
		}
	}
}

func TestRouteHTTPNoCloudNoRedirect(t *testing.T) {
	home := t.TempDir()
	cloudCalls, localCalls := 0, 0
	cloud := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { cloudCalls++; w.Write([]byte(`{}`)) }))
	defer cloud.Close()
	redirect := false
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		localCalls++
		if redirect {
			http.Redirect(w, r, cloud.URL, http.StatusTemporaryRedirect)
			return
		}
		var v map[string]any
		if err := json.NewDecoder(r.Body).Decode(&v); err != nil {
			t.Error(err)
		}
		if !strings.Contains(v["messages"].([]any)[0].(map[string]any)["content"].(string), "tripwire") {
			t.Error("missing private value")
		}
		w.Write([]byte(`{"choices":[]}`))
	}))
	defer local.Close()
	p := team.Participant{Handle: "local", Kind: team.Agent, Tier: "base", Model: "synthetic", Roles: []string{team.RoleExec}, Route: "local", Endpoint: local.URL + "/v1"}
	if err := team.Save(home, []team.Participant{p}); err != nil {
		t.Fatal(err)
	}
	record := filepath.Join(home, "record.md")
	if err := os.WriteFile(record, []byte("# запись\nисточник: meeting\nзамок: locked\n\n## Текст\ntripwire"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HTTP_PROXY", cloud.URL)
	t.Setenv("HTTPS_PROXY", cloud.URL)
	var out, diag bytes.Buffer
	if err := cmdRoute(home, "local", record, "", &out, &diag); err != nil {
		t.Fatal(err)
	}
	if localCalls != 1 || cloudCalls != 0 {
		t.Fatalf("local=%d cloud=%d", localCalls, cloudCalls)
	}
	redirect = true
	if err := cmdRoute(home, "local", record, "", &out, &diag); err == nil {
		t.Fatal("followed redirect")
	}
	if cloudCalls != 0 {
		t.Fatal("cloud received private material")
	}
	p.Route = "cloud"
	p.Endpoint = cloud.URL + "/v1"
	if err := team.Save(home, []team.Participant{p}); err != nil {
		t.Fatal(err)
	}
	if err := cmdRoute(home, "local", record, "", &out, &diag); err == nil {
		t.Fatal("cloud accepted locked")
	}
	if cloudCalls != 0 {
		t.Fatal("cloud called before lock check")
	}
}

func TestPickLocalParticipantNeverUsesCloudLadder(t *testing.T) {
	isolateQuota(t)
	fixNow(t, testNow)
	root := teamBoard(t, "local")
	if err := team.Save(stage.Home(), []team.Participant{{Handle: "local", Kind: team.Agent, Tier: "base", Model: "synthetic", Roles: []string{team.RoleExec}, Route: "local", Endpoint: "http://127.0.0.1:1/v1"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdPick(root, "T-007", roleExec, ""); err == nil || !strings.Contains(err.Error(), "agentctl route") {
		t.Fatalf("local fell through to cloud: %v", err)
	}
}
