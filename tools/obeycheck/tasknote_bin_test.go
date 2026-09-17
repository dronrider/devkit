package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Отказ на собранном бинаре: до правки стенд с --task на раскладке без текста
// предмета гонял сессии и клал зачтённую отметку с предупреждением (две такие
// у DK-996 на full без ядра, DK-1025). Теперь он выходит до первой сессии, и
// в файле задачи следа нет. Юнит на staleLayout рядом не доказывает, что main
// зовёт его раньше прогона.
func TestTaskRefusesStaleLayoutOnTheBinary(t *testing.T) {
	root := devkitRoot(t)
	bin := filepath.Join(t.TempDir(), toolName)
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Env = append(os.Environ(), "GOWORK=off")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("сборка не прошла: %v\n%s", err, out)
	}
	repo := t.TempDir()
	if out, err := exec.Command("git", "-C", repo, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	task := filepath.Join(repo, "docs", "tasks", "DK-836.md")
	if err := os.MkdirAll(filepath.Dir(task), 0o755); err != nil {
		t.Fatal(err)
	}
	doc := "# DK-836\n\n## Проверка\n\nШаги гоняются из корня чекаута.\n"
	if err := os.WriteFile(task, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	stale := t.TempDir()
	if err := os.WriteFile(filepath.Join(stale, "RULES.md"), []byte("разбор без ядра\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	scen, err := filepath.Abs(filepath.Join("testdata", "scenarios"))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "--devkit", root, "--scenarios", scen, "--only", "press",
		"--task", "DK-836", "-k", "3", "--base", "пусто", "--agent-cmd", "false",
		"--no-preflight", stale, stale)
	cmd.Dir = repo
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("раскладка без текста предмета прошла:\n%s", out)
	}
	for _, want := range []string{"след не пишется", "текст предмета RULES.core.md «Тесты обязательны»"} {
		if !strings.Contains(string(out), want) {
			t.Fatalf("в отказе нет %q:\n%s", want, out)
		}
	}
	if after := read(t, task); after != doc {
		t.Fatalf("след лёг в файл задачи:\n%s", after)
	}
}
