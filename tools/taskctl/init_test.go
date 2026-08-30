package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestInitAndFirstTask(t *testing.T) {
	dir := t.TempDir()
	msg, err := cmdInit(dir, InitParams{Prefix: "AB", Name: "проба"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "docs/TASKS.md") {
		t.Fatalf("неожиданное сообщение: %s", msg)
	}
	finds, err := cmdLint(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(finds) != 0 {
		t.Fatalf("на свежей доске находки: %v", finds)
	}
	// Префикс из шапки: первая задача заводится без --id.
	id, err := cmdID(dir)
	if err != nil {
		t.Fatal(err)
	}
	if id != "AB-001" {
		t.Fatalf("ожидал AB-001, получил %s", id)
	}
	if _, err := cmdAdd(dir, AddParams{Title: "Первая", Type: "task", Rank: "0+1+1+0+1", Link: "x", Accept: "agent"}); err != nil {
		t.Fatal(err)
	}
	if ids := backlogIDs(t, dir); len(ids) != 1 || ids[0] != "AB-001" {
		t.Fatalf("Backlog после первой задачи: %v", ids)
	}
}

func TestInitRefusesExisting(t *testing.T) {
	root := setup(t)
	if _, err := cmdInit(root, InitParams{Prefix: "XR"}); err == nil {
		t.Fatal("init поверх существующей доски должен падать")
	}
}

func TestInitPrefixValidation(t *testing.T) {
	for _, p := range []string{"", "xr", "X R", "XR1"} {
		if _, err := cmdInit(t.TempDir(), InitParams{Prefix: p}); err == nil {
			t.Errorf("ожидал ошибку на префиксе %q", p)
		}
	}
}

func TestInitFindsGitTop(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("нет git")
	}
	dir := t.TempDir()
	if out, err := exec.Command("git", "-C", dir, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	sub := filepath.Join(dir, "внутри", "глубже")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdInit(sub, InitParams{Prefix: "GT"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(boardPath(dir)); err != nil {
		t.Fatal("доска не в корне git-репозитория:", err)
	}
}

// TestInitHereStaysInTheDirectory: с --here доска ложится в названную
// директорию, а не в корне репозитория. Так подключается проект корп-контура:
// боковая директория контура лежит подкаталогом его репозитория, и досок там
// столько, сколько проектов (DK-583).
func TestInitHereStaysInTheDirectory(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("нет git")
	}
	dir := t.TempDir()
	if out, err := exec.Command("git", "-C", dir, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	sub := filepath.Join(dir, "проект")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdInit(sub, InitParams{Prefix: "HR", Here: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(boardPath(sub)); err != nil {
		t.Fatal("доска не в названной директории:", err)
	}
	if _, err := os.Stat(boardPath(dir)); err == nil {
		t.Fatal("доска легла ещё и в корень репозитория")
	}
}

func TestNextIDWithoutHeaderPrefix(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	board := strings.Replace(fixtureBoard, "# Тест: доска (префикс XR)", "# Тест: доска", 1)
	empty := []string{
		"| XR-005 | Задача в работе | task | P2 | 30 (25+2+1+0+2) | - | [tasks/XR-005.md](tasks/XR-005.md) |",
		"| XR-002 | Верхняя | bug | P1 | 55 (50+0+0+5+0) | - | [tasks/XR-002.md](tasks/XR-002.md) |",
		"| XR-001 | Средняя | task/LLD | P2 | 30 (25+2+1+0+2) | - | (LLD позже) |",
		"| XR-003 | Та же R, больший ID | LLD | P2 | 30 (25+3+2+0+0) | - | (LLD позже) |",
		"| XR-004 | Хвост | task | P3 | 9 (0+4+1+0+4) | - | (LLD позже) |",
	}
	for _, ln := range empty {
		board = strings.Replace(board, ln+"\n", "", 1)
	}
	if err := os.WriteFile(boardPath(root), []byte(board), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(archivePath(root), []byte("# Тест: архив\n\n| ID | Задача | Тип | P | Закрыто | Ссылка |\n|--------|--------|-----|---|---------|--------|\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdID(root); err == nil {
		t.Fatal("пустая доска без префикса в шапке должна требовать --id")
	}
}
