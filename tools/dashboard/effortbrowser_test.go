package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
	// Принудительное обновление доски в известной точке сценария (DK-1694):
	// стенд подменяет ответ taskctl и зовёт настоящий refresh, и перерисовка
	// #groups живой доской обязана оставить тестовый узел проверки геометрии
	// на месте.
	// Ответ лежит файлом, а не телом заглушки, чтобы подмена не трогала саму
	// заглушку: строка в доске это правка данных, а не правка стенда.
	flip := t.TempDir()
	boardNow := filepath.Join(flip, "board.json")
	boardNext := filepath.Join(flip, "board-next.json")
	const flipRow = `,{"id":"XR-777","title":"Строка принудительного обновления",` +
		`"type":"task","p":"P2","r":51,"r_parts":[25,9,3,0,4],"cost":"-","link":"-"}`
	if err := os.WriteFile(boardNow, []byte(chatBoardJSON+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	next := strings.Replace(chatBoardJSON, `"cost":"XL","link":"-"}]}]}`,
		`"cost":"XL","link":"-"}`+flipRow+`]}]}`, 1)
	if !strings.Contains(next, "XR-777") {
		t.Fatal("строка подмены не встала в доску")
	}
	if err := os.WriteFile(boardNext, []byte(next+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeScript(t, e.bin, "taskctl", "cat "+boardNow)
	probe := exec.Command(node, "testdata/effort_browser.mjs", e.srv.URL, chrome, e.cfg.Token,
		boardNow, boardNext, "Строка принудительного обновления")
	probe.Env = os.Environ()
	if out, err := probe.CombinedOutput(); err != nil {
		t.Fatalf("геометрия меню в Chromium: %v\n%s", err, out)
	} else {
		t.Log(string(out))
	}
}
