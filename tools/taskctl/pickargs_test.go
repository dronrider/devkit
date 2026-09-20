package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// buildAgentctl собирает agentctl из соседнего пакета во временный файл:
// сверить ключи вызова можно только с живым набором флагов, стаб из pickStand
// принимает любые.
func buildAgentctl(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "agentctl")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	cmd.Dir = filepath.Join("..", "agentctl")
	cmd.Env = append(os.Environ(), "GOWORK=off")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build agentctl: %v\n%s", err, out)
	}
	return bin
}

// DK-911, замечание ревью: askPick звал снятый ключ --record, pick отбивал
// его кодом 2, и голова от taskctl run шла без модели со строкой в журнале.
// Стаб agentctl этого не ловил, поэтому вердикт спрашивается у настоящего
// бинаря на временном репозитории: набор ключей askPick обязан совпадать с
// живым набором флагов pick.
func TestAskPickArgsAcceptedByLiveAgentctl(t *testing.T) {
	root, _, _ := runDevkit(t)
	bin := buildAgentctl(t)
	t.Setenv("PATH", filepath.Dir(bin)+string(os.PathListSeparator)+os.Getenv("PATH"))
	tasks := filepath.Join(root, "docs", "tasks")
	if err := os.MkdirAll(tasks, 0o755); err != nil {
		t.Fatal(err)
	}
	board := "# Стенд: задачи (префикс XR)\n\n## In progress\n\n" +
		"| ID | Задача | Тип | P | R | Цена | Ссылка |\n|---|---|---|---|---|---|---|\n" +
		"| XR-001 | Починка бага | task | P2 | 30 (25+4+0+0+1) | S | [tasks/XR-001.md](tasks/XR-001.md) |\n\n" +
		"## Check\n\nНет.\n\n## Backlog\n\nНет.\n\n## Blocked\n\nНет.\n"
	if err := os.WriteFile(filepath.Join(root, "docs", "TASKS.md"), []byte(board), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tasks, "XR-001.md"), []byte("# XR-001: починка бага\n\n## Ход работы\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	model, err := askPick(root, "XR-001", "claude-code")
	if err != nil {
		if strings.Contains(err.Error(), "flag provided but not defined") {
			t.Fatalf("askPick зовёт ключ, которого у pick нет: %v", err)
		}
		t.Fatalf("живой pick отказал: %v", err)
	}
	if model == "" {
		t.Fatal("живой pick не назвал модели")
	}
}
