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
