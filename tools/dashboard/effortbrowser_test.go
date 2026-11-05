package main

import (
	"os"
	"os/exec"
	"testing"
)

func TestEffortMenuBrowserGeometry(t *testing.T) {
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
	if out, err := exec.Command(node, "-e", "require.resolve('playwright')").CombinedOutput(); err != nil {
		if os.Getenv("DASHBOARD_EFFORT_REQUIRE_BROWSER") == "1" {
			t.Fatalf("обязательный playwright недоступен: %s", out)
		}
		t.Skipf("playwright недоступен; задать NODE_PATH: %s", out)
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
