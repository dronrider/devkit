package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSnapUsagePanelNamesDir: съёмщик поднимает клиента с явным «-c», и каталог
// этот не дом. Без «-c» клиент брал рабочий каталог agentctl, под launchd это
// корень файловой системы, и обход дерева упирался в защищённые папки macOS
// диалогами доступа с именем службы (DK-1163). tmux тут подменён скриптом: ему
// довольно записать доводы подъёма и отказать, дальше съёмщик уходит сам.
func TestSnapUsagePanelNamesDir(t *testing.T) {
	bin := t.TempDir()
	raise := filepath.Join(t.TempDir(), "raise")
	script := "#!/bin/sh\nif [ \"$1\" = new-session ]; then printf '%s\\n' \"$*\" >" + raise + "; exit 1; fi\nexit 0\n"
	if err := os.WriteFile(filepath.Join(bin, "tmux"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	home := t.TempDir()
	q := specAt(t, filepath.Join(home, ".devkit", "quota", "claude-code.local"))
	q.Home = home

	if _, err := snapUsagePanel(q, testNow); err == nil {
		t.Fatal("стенд оборвал подъём, а съёмщик отказа не вернул")
	}
	raw, err := os.ReadFile(raise)
	if err != nil {
		t.Fatalf("доводы подъёма не записались: %v", err)
	}
	args := strings.Fields(strings.TrimSpace(string(raw)))
	dir := ""
	for i, a := range args {
		if a == "-c" && i+1 < len(args) {
			dir = args[i+1]
		}
	}
	if dir == "" {
		t.Fatalf("клиент поднят без каталога: %v", args)
	}
	if err := clientdirCheck(dir, home); err != nil {
		t.Fatalf("каталог подъёма %q не годится: %v", dir, err)
	}
}

// clientdirCheck это та же проверка каталога, что в internal/clientdir, своими
// словами: стенд обязан судить о каталоге сам, иначе правка и её мерка
// съезжали бы вместе.
func clientdirCheck(dir, home string) error {
	if strings.TrimSpace(dir) == "" {
		return errors.New("каталог подъёма клиента не назван")
	}
	if filepath.Clean(dir) == filepath.Clean(home) {
		return errors.New("каталог подъёма клиента это дом " + home)
	}
	return nil
}
