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

// TestReadPeerKindsSubscriptionWins: остаточная запись старого каталога не
// затирает свежую запись подписки с тем же именем сессии. Dirs ставит старый
// каталог последним, накладка идёт первым выигрышем (DK-1335).
func TestReadPeerKindsSubscriptionWins(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	sub := filepath.Join(cfg, "sessions")
	old := t.TempDir()
	for _, dir := range []string{sub, old} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	fresh := `{"pid":1,"sessionId":"aaa","name":"devkit","kind":"interactive"}`
	stale := `{"pid":2,"sessionId":"bbb","name":"devkit","kind":"cli"}`
	if err := os.WriteFile(filepath.Join(sub, "1.json"), []byte(fresh), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(old, "2.json"), []byte(stale), 0o644); err != nil {
		t.Fatal(err)
	}
	restore := peerRegistryDirs
	peerRegistryDirs = func() []string { return []string{sub, old} }
	defer func() { peerRegistryDirs = restore }()

	kinds := readPeerKinds()
	if kinds["devkit"] != "interactive" {
		t.Fatalf("вид из каталога подписки затёрт остатком старого каталога: %+v", kinds)
	}
}
