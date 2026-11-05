package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Этот сценарий обращается только к прежним символам и CLI: regcheck должен
// показать поведенческий отказ старого бинаря на reopen, а не ошибку сборки.
func TestReopenCLI(t *testing.T) {
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
	stub := t.TempDir()
	if err := os.WriteFile(filepath.Join(stub, "shipctl"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", stub+string(os.PathListSeparator)+os.Getenv("PATH"))
	run := func(ok bool, args ...string) string {
		t.Helper()
		out, err := exec.Command(bin, append([]string{"-C", root}, args...)...).CombinedOutput()
		if (err == nil) != ok {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	read := func(rel string) string {
		t.Helper()
		b, e := os.ReadFile(filepath.Join(root, rel))
		if e != nil {
			t.Fatal(e)
		}
		return string(b)
	}
	write := func(rel, text string) {
		t.Helper()
		if e := os.WriteFile(filepath.Join(root, rel), []byte(text), 0o644); e != nil {
			t.Fatal(e)
		}
	}
	run(true, "add", "--id", "XR-100", "--title", "Возврат", "--type", "task", "--rank", "25+5+2+0+1", "--cost", "M", "--accept", "user", "--barrier", "событие", "--body-file", fixtureAddBody(t, root, "XR-100"))
	doc := "# XR-100\n" + fixtureScenario + "\n## Приёмка\n\n- вид: user\n- барьер «событие»: причина\n  - событие в логе: годится\n  - пустой лог: годится\n  - сторонний источник: годится\n\n## Проверка\n\nПрежний результат сохранён.\n\n## Ход работы\n\n- Первая попытка.\n\n[сосед](XR-005.md)\n[сценарий](XR-100-smoke.sh)\n\n```sh\nsh docs/tasks/XR-100-smoke.sh\n```\n"
	write("docs/tasks/XR-100.md", doc)
	write("docs/tasks/XR-100-smoke.sh", "exit 0\n")
	write("docs/tasks/XR-100.review.md", "# Журнал\n")
	write("docs/reference.md", "[задача](tasks/XR-100.md)\n")
	gitSetup(t, root)
	legacy := "| XR-007 | Закрытая | bug | P1 | 2026-06-12 | `abc1234` |"
	run(true, "close", "XR-100", "--date", "2026-10-01")
	archive := read("docs/TASKS-archive.md")
	write("docs/tasks/XR-100.md", "дубликат\n")
	if out := run(false, "reopen", "XR-100", "--reason", "коллизия"); !strings.Contains(out, "уже существует") {
		t.Fatal(out)
	}
	if e := os.Remove(filepath.Join(root, "docs/tasks/XR-100.md")); e != nil {
		t.Fatal(e)
	}
	run(true, "reopen", "XR-100", "--reason", "продолжение")
	if !strings.Contains(archive, "| 34 (25+5+2+0+1, M+1) | M | user |") || !strings.Contains(archive, legacy+"\n") {
		t.Fatal(archive)
	}

	var shown map[string]any
	if e := json.Unmarshal([]byte(run(true, "show", "XR-100", "--json")), &shown); e != nil {
		t.Fatal(e)
	}
	if shown["sect"] != "backlog" || shown["r"] != float64(34) || shown["cost"] != "M" || shown["accept"] != "user" || shown["reopen_count"] != float64(1) {
		t.Fatalf("%v", shown)
	}
	restored := read("docs/tasks/XR-100.md")
	for _, want := range []string{"Прежний результат сохранён.", "Первая попытка.", "[сосед](XR-005.md)", "[сценарий](XR-100-smoke.sh)", "sh docs/tasks/XR-100-smoke.sh", "закрыта 2026-10-01", "taskctl:reopen"} {
		if !strings.Contains(restored, want) {
			t.Fatalf("потеряно %s:\n%s", want, restored)
		}
	}
	if read("docs/reference.md") != "[задача](tasks/XR-100.md)\n" {
		t.Fatal("входящая ссылка не восстановлена")
	}
	read("docs/tasks/XR-100.review.md")
	read("docs/tasks/XR-100-smoke.sh")
	if strings.Contains(read("docs/TASKS-archive.md"), "| XR-100 |") {
		t.Fatal("осталась строка архива")
	}
	before := read("docs/TASKS.md") + read("docs/TASKS-archive.md") + restored
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"reopen", "XR-100", "--reason", "ещё раз"}, "уже на доске"},
		{[]string{"reopen", "XR-999", "--reason", "неизвестная"}, "нет в архиве"},
		{[]string{"reopen", "XR-007"}, "--reason"},
		{[]string{"reopen", "XR-007", "--reason", "старый формат"}, "--rank"},
	} {
		if out := run(false, tc.args...); !strings.Contains(out, tc.want) {
			t.Fatal(out)
		}
	}
	if got := read("docs/TASKS.md") + read("docs/TASKS-archive.md") + read("docs/tasks/XR-100.md"); got != before {
		t.Fatal("отказ изменил данные")
	}
	run(true, "close", "XR-100", "--date", "2026-11-02")
	run(true, "reopen", "XR-100", "--reason", "вторая доработка")
	var stats map[string]int
	if e := json.Unmarshal([]byte(run(true, "reopen", "stats", "--json")), &stats); e != nil {
		t.Fatal(e)
	}
	if stats["tasks"] != 1 || stats["reopen_count"] != 2 {
		t.Fatal(stats)
	}
	run(true, "close", "XR-100", "--date", "2026-11-03")
	if out := run(true, "show", "XR-100", "--json"); !strings.Contains(out, `"reopen_count":2`) || !strings.Contains(out, `"closed":"2026-11-03"`) {
		t.Fatal(out)
	}
	if e := json.Unmarshal([]byte(run(true, "reopen", "stats", "--json")), &stats); e != nil || stats["tasks"] != 1 || stats["reopen_count"] != 2 {
		t.Fatalf("архивная сводка: %v %v", stats, e)
	}
	// Старые строки требуют явных значений; незакрытый выкат и дубликаты
	// проверяются до любого переноса и изменения доски.
	write("docs/tasks/XR-007.md", mergedDoc("XR-007", "- 2026-10-12 слито: 1a2b3c4d5e"))
	args := []string{"reopen", "XR-007", "--reason", "старый формат", "--rank", "25+5+2+0+1", "--size", "S", "--accept", "agent"}
	if out := run(false, args...); !strings.Contains(out, "shipctl smoke XR-007") {
		t.Fatal(out)
	}
	write("docs/tasks/XR-007.md", "# XR-007\n\n## Ход работы\n\n- Старое закрытие.\n")
	arch := read("docs/TASKS-archive.md")
	write("docs/TASKS-archive.md", arch+legacy+"\n")
	if out := run(false, args...); !strings.Contains(out, "дубликаты") {
		t.Fatal(out)
	}
	write("docs/TASKS-archive.md", arch)
	run(true, args...)
	if out := run(true, "show", "XR-007", "--json"); !strings.Contains(out, `"cost":"S"`) || !strings.Contains(out, `"r":35`) {
		t.Fatal(out)
	}
}
