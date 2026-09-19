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

// До правки DK-1055 main звал taskFile от "." вместо дерева --devkit, и след
// искался только по cwd. Тест гоняет собранный бинарь из дерева cwd, в
// котором файла задачи нет, а дерево --devkit его держит: на старом коде
// стенд отказывал раньше срока, «файла ... нет», даже не дойдя до проверки
// раскладки. На исправленном коде он находит файл в дереве --devkit и
// доходит до следующего шага, staleLayout, который здесь нарочно проваливает
// пустая раскладка-кандидат.
func TestTaskFileWritesToDevkitTreeNotCwd(t *testing.T) {
	bin := filepath.Join(t.TempDir(), toolName)
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Env = append(os.Environ(), "GOWORK=off")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("сборка не прошла: %v\n%s", err, out)
	}

	devkit := t.TempDir()
	if out, err := exec.Command("git", "-C", devkit, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init дерева --devkit: %v\n%s", err, out)
	}
	rules := "# Ядро правил\n\n## Тесты обязательны\n\nЛюбая правка кода едет вместе с тестами.\n"
	if err := os.WriteFile(filepath.Join(devkit, "RULES.core.md"), []byte(rules), 0o644); err != nil {
		t.Fatal(err)
	}
	task := filepath.Join(devkit, "docs", "tasks", "DK-9001.md")
	if err := os.MkdirAll(filepath.Dir(task), 0o755); err != nil {
		t.Fatal(err)
	}
	doc := "# DK-9001\n\n## Проверка\n\nШаги гоняются из корня чекаута.\n"
	if err := os.WriteFile(task, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}

	cwd := t.TempDir()
	if out, err := exec.Command("git", "-C", cwd, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init дерева cwd: %v\n%s", err, out)
	}

	// Каталог сценариев везёт только press.md: loadScenarios проверяет привязку
	// каждого файла раньше отбора по --only, а фейковое дерево --devkit несёт
	// текст лишь одного предмета.
	press, err := os.ReadFile(filepath.Join("testdata", "scenarios", "press.md"))
	if err != nil {
		t.Fatal(err)
	}
	scen := t.TempDir()
	if err := os.WriteFile(filepath.Join(scen, "press.md"), press, 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "--devkit", devkit, "--scenarios", scen, "--only", "press",
		"--task", "DK-9001", "-k", "3", "--base", "пусто", "--agent-cmd", "false",
		"--no-preflight", t.TempDir(), t.TempDir())
	cmd.Dir = cwd
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("пустая раскладка-кандидат должна была отбить прогон:\n%s", out)
	}
	if strings.Contains(string(out), "а текущая директория не в репозитории") ||
		strings.Contains(string(out), "нет ни в дереве --devkit") {
		t.Fatalf("файл задачи искался не в дереве --devkit:\n%s", out)
	}
	want := "текст предмета RULES.core.md «Тесты обязательны»"
	if !strings.Contains(string(out), want) {
		t.Fatalf("в отказе нет %q, файл задачи не нашёлся в дереве --devkit:\n%s", want, out)
	}
	if after := read(t, task); after != doc {
		t.Fatalf("след лёг в файл задачи дерева --devkit при отказе staleLayout:\n%s", after)
	}
}
