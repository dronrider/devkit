package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestReopenGoalCLI(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "taskctl")
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Env = append(os.Environ(), "GOWORK=off")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("сборка: %v\n%s", err, out)
	}
	root := setup(t)
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	t.Setenv("CODEX_THREAD_ID", "")
	t.Setenv("DEVKIT_TMUX", "")
	dir := filepath.Join(root, "docs", "tasks", "archive", "2026")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "# Цель\n\n## Задачи цели\n\n- [XR-004](../../XR-004.md)\n\n## Ход работы\n\n- Закрыта.\n"
	if err := os.WriteFile(filepath.Join(dir, "XR-100.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	arch := fixtureArchive + "| XR-100 | Цель: Проверить возврат | task | P0 | 2026-10-01 | - | 85 (75+6+1+0+3) | L | agent |\n"
	if err := os.WriteFile(archivePath(root), []byte(arch), 0o644); err != nil {
		t.Fatal(err)
	}
	gitSetup(t, root)
	run := func(args ...string) string {
		t.Helper()
		out, err := exec.Command(bin, append([]string{"-C", root}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	run("reopen", "XR-100", "--reason", "продолжить цель")
	saved, err := os.ReadFile(boardPath(root))
	if err != nil {
		t.Fatal(err)
	}
	child := "| XR-004 | Хвост | task | P0 | 85 (0+4+1+0+4, от XR-100) | - | (LLD позже) |"
	if !strings.Contains(string(saved), child+"\n") {
		t.Fatalf("сохранённая строка не получила ранг цели:\n%s", saved)
	}
	goalAt, childAt, nextAt := strings.Index(string(saved), "| XR-100 |"), strings.Index(string(saved), "| XR-004 |"), strings.Index(string(saved), "| XR-002 |")
	if goalAt < 0 || childAt <= goalAt || nextAt <= childAt {
		t.Fatalf("Backlog после возврата цели не отсортирован:\n%s", saved)
	}
	var shown struct {
		R     int    `json:"r"`
		P     string `json:"p"`
		Parts [5]int `json:"r_parts"`
	}
	if err := json.Unmarshal([]byte(run("show", "XR-004", "--json")), &shown); err != nil {
		t.Fatal(err)
	}
	if shown.R != 85 || shown.P != "P0" || shown.Parts != [5]int{0, 4, 1, 0, 4} {
		t.Fatalf("JSON не согласован с сохранённой строкой: %+v", shown)
	}
	restored, err := os.ReadFile(taskFilePath(root, "XR-100"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(restored), "[XR-004](XR-004.md)") {
		t.Fatalf("ссылка цели не восстановлена:\n%s", restored)
	}
	run("sort")
	sorted, err := os.ReadFile(boardPath(root))
	if err != nil {
		t.Fatal(err)
	}
	if string(saved) != string(sorted) {
		t.Fatalf("sort изменил результат reopen:\n%s", sorted)
	}
}
