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

// Барьеры HTTP держат ответ старой отправки, пока человек меняет черновик
// и глубину следующей. Проверяется настоящий compose и переезд к новому sid.
func TestChatComposerPendingSnapshotBrowser(t *testing.T) {
	chrome := findChrome()
	if chrome == "" {
		t.Fatal("для проверки границы отправки нужен браузер стенда")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	if !needPlaywright(t, node) {
		t.Fatal("для проверки границы отправки нужен playwright")
	}
	e, _ := chatEnv(t)
	out, err := exec.Command(node, "testdata/chatcomposer_pending_browser.mjs", e.srv.URL, chrome, e.cfg.Token).CombinedOutput()
	if err != nil {
		t.Fatalf("граница отправки в Chromium: %v\n%s", err, out)
	}
	t.Log(string(out))
}

// Пришивание меняет адрес до готовности нового DOM. Ответы /say могут
// запаздывать ещё дольше и относятся к исходной реплике.
func TestChatComposerHandoffBrowser(t *testing.T) {
	chrome := findChrome()
	if chrome == "" {
		t.Fatal("для проверки пришивания нужен Chromium")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	if !needPlaywright(t, node) {
		t.Fatal("для проверки пришивания нужен playwright")
	}
	e, _ := chatEnv(t)
	for _, mode := range []string{
		"runs-handoff", "raise-handoff", "raise-upload", "raise-upload-image",
		"retry-after-refusal", "retry-after-reload", "retry-after-lost-answer", "retry-task-receipt",
		"crash-task-before-receipt", "crash-tmux-before-receipt",
		"crash-task-before-queue", "crash-task-feed-during-receipt",
		"crash-task-feed-receipt-503", "crash-task-feed-receipt-exhausted",
		"runs-fragment", "raise-image", "runs-height",
		"draft-eviction", "draft-removal",
	} {
		t.Run(mode, func(t *testing.T) {
			out, err := exec.Command(node, "testdata/chatcomposer_handoff_browser.mjs",
				e.srv.URL, chrome, e.cfg.Token, mode).CombinedOutput()
			if err != nil {
				t.Fatalf("поле чата в Chromium: %v\n%s", err, out)
			}
			t.Log(string(out))
		})
	}
}
