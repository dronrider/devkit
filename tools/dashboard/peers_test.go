package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRealHomeStandEnv: стенд serve держит подписки и клиентов под своим
// домом, а настоящий дом машины читается через getpwuid и переменными
// окружения не повернётся. Поэтому стенд называет дом ключом DEVKIT_DASH_HOME,
// и подпроцессы дашборда получают его HOME. Живой случай DK-1695: стенд
// недоступного клиента поднимал подписки настоящего ~/.devkit/harness.local.
func TestRealHomeStandEnv(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DEVKIT_DASH_HOME", home)
	if got := realHome(); got != home {
		t.Fatalf("дом стенда %q, ключ назвал %q", got, home)
	}
}

// TestRealHomeStandEnvEmpty: пустое значение ключа это его отсутствие, а не
// пустой дом: перекошенная переменная не должна ронять машину в пустоту.
func TestRealHomeStandEnvEmpty(t *testing.T) {
	t.Setenv("DEVKIT_DASH_HOME", "  ")
	if got := realHome(); got == "" || got == "  " {
		t.Fatalf("пустой ключ сломал дом: %q", got)
	}
}

// TestRunProcHomeUnderStandEnv: подпроцесс, поднятый через runProcHome, видит
// дом стенда своим HOME, а служебный каталог подъёма берёт дом той же цепочкой
// realHomeOr: стенд не заводит ничего в настоящем `~/.devkit` машины.
func TestRunProcHomeUnderStandEnv(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DEVKIT_DASH_HOME", home)
	if got := realHomeOr(""); got != home {
		t.Fatalf("дом служебного каталога %q, стенд назвал %q", got, home)
	}
	out, err := runProcHome("sh", "-c", "printf %s \"$HOME\"")
	if err != nil {
		t.Fatalf("запуск под домом стенда отказал: %v", err)
	}
	if got := strings.TrimSpace(string(out)); got != home {
		t.Fatalf("подпроцесс видит дом %q, стенд назвал %q", got, home)
	}
}

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
