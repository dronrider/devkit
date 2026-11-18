package main

import (
	"os"
	"os/exec"
	"testing"
)

func TestChatSideWorkComposer(t *testing.T) {
	chrome := findChrome()
	if chrome == "" {
		t.Skip("браузер стенда не найден")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	if !needPlaywright(t, node) {
		t.Skip("playwright недоступен")
	}
	e, _ := chatEnv(t)
	cmd := exec.Command(node, "testdata/chat_sidework_browser.mjs", e.srv.URL, chrome, e.cfg.Token)
	cmd.Env = os.Environ()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("отправка после побочной задачи: %v\n%s", err, out)
	} else {
		t.Log(string(out))
	}
}
