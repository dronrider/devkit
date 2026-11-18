package main

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestAppFeedCards(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node не найден: стенд карточек App пропущен")
	}
	out, err := exec.Command(node, filepath.Join("testdata", "poc_appfeed.mjs"),
		filepath.Join("static", "app.js")).CombinedOutput()
	if err != nil {
		t.Fatalf("карточки App: %v\n%s", err, out)
	}
	t.Log(strings.TrimSpace(string(out)))
}
