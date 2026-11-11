package main

import (
	"os"
	"os/exec"
	"testing"
)

func TestEffortMenuBrowserGeometry(t *testing.T) {
	t.Setenv("DASHBOARD_EFFORT_CASE", "")
	t.Setenv("DASHBOARD_EFFORT_LEVELS_ONLY", "")
	effortMenuBrowser(t)
}

func TestEffortMenuClippingRegressions(t *testing.T) {
	for _, tc := range []struct {
		name, focus string
	}{
		{"DesktopChatLow", "1280/8/chat/bottom"},
		{"MobileRunLow", "390/24/run/top"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("DASHBOARD_EFFORT_CASE", tc.focus)
			t.Setenv("DASHBOARD_EFFORT_LEVELS_ONLY", "1")
			effortMenuBrowser(t)
		})
	}
}

func effortMenuBrowser(t *testing.T) {
	t.Helper()
	chrome := findChrome()
	if chrome == "" {
		if os.Getenv("DASHBOARD_EFFORT_REQUIRE_BROWSER") == "1" {
			t.Fatal("обязательный браузер геометрии не найден")
		}
		t.Skip("браузер стенда не найден; задать DASHBOARD_CHROME")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	if !needPlaywright(t, node) {
		if os.Getenv("DASHBOARD_EFFORT_REQUIRE_BROWSER") == "1" {
			t.Fatal("обязательный playwright недоступен; задать NODE_PATH")
		}
		t.Skip("playwright недоступен; задать NODE_PATH")
	}
	e, _ := chatEnv(t)
	probe := exec.Command(node, "testdata/effort_browser.mjs", e.srv.URL, chrome, e.cfg.Token)
	probe.Env = os.Environ()
	if out, err := probe.CombinedOutput(); err != nil {
		t.Fatalf("геометрия меню в Chromium: %v\n%s", err, out)
	} else {
		t.Log(string(out))
	}
}
