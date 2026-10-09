package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestReadPeerKindsFromConfigDir: клиент пишет реестр в каталог подписки из
// CLAUDE_CONFIG_DIR, а старый ~/.claude/sessions остаётся пустым. Вид сессии
// для реплики канала живых сессий берётся и оттуда, не только из старого
// каталога (DK-1335).
func TestReadPeerKindsFromConfigDir(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	dir := filepath.Join(cfg, "sessions")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	live := `{"pid":1,"sessionId":"aaa","name":"devkit-cfg","kind":"interactive"}`
	if err := os.WriteFile(filepath.Join(dir, "1.json"), []byte(live), 0o644); err != nil {
		t.Fatal(err)
	}

	kinds := readPeerKinds()
	if kinds["devkit-cfg"] != "interactive" {
		t.Fatalf("вид сессии из каталога подписки не прочитан: %+v", kinds)
	}
}
