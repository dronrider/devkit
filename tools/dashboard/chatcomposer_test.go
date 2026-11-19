package main

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestStaticChatAttachmentOnlyComposer(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node не найден: стенд отправки вложений пропущен")
	}
	out, err := exec.Command(node, filepath.Join("testdata", "poc_composerclips.mjs"),
		filepath.Join("static", "app.js")).CombinedOutput()
	if err != nil {
		t.Fatalf("отправка вложений из поля ввода: %v\n%s", err, out)
	}
	t.Log(strings.TrimSpace(string(out)))
}

func TestChatComposerBrowser(t *testing.T) {
	chrome := findChrome()
	if chrome == "" {
		t.Fatal("для DK-1741 нужен браузер стенда")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	if !needPlaywright(t, node) {
		t.Fatal("для DK-1741 нужен playwright")
	}
	e, _ := chatEnv(t)
	out, err := exec.Command(node, "testdata/chatcomposer_browser.mjs", e.srv.URL, chrome, e.cfg.Token).CombinedOutput()
	if err != nil {
		t.Fatalf("поле чата в Chromium: %v\n%s", err, out)
	}
	t.Log(string(out))
}
