package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gitSetup превращает временную доску в git-репозиторий с начальным коммитом
// и без remote: git init в tempdir не создаёт origin, и пуш коммита строки
// уходит в честный отказ «нет destination», а не вешает прогон попыткой сети.
func gitSetup(t *testing.T, root string) {
	t.Helper()
	gitOut(t, root, "init", "-q", "-b", "main")
	gitOut(t, root, "config", "user.email", "test@test")
	gitOut(t, root, "config", "user.name", "test")
	gitOut(t, root, "add", ".")
	gitOut(t, root, "commit", "-q", "-m", "init")
}

func gitOut(t *testing.T, root string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// TestRecordCommitsVerdictLine это регрессия DK-120: строка вердикта от
// pick --record оставалась незакоммиченной, дерево задачи переставало быть
// чистым, и shipctl merge отказывал «в worktree незакоммиченное». Проверка
// та же, что в постановке: pick с --record, сразу git status пуст.
func TestRecordCommitsVerdictLine(t *testing.T) {
	isolateQuota(t)
	root := writeBoard(t)
	if err := os.MkdirAll(filepath.Join(root, "docs", "tasks"), 0o755); err != nil {
		t.Fatal(err)
	}
	taskFile := filepath.Join(root, "docs", "tasks", "T-001.md")
	if err := os.WriteFile(taskFile, []byte("# T-001\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitSetup(t, root)

	if _, err := cmdPick(root, "T-001", true, roleReview, ""); err != nil {
		t.Fatalf("pick --role review --record: %v", err)
	}

	if out := gitOut(t, root, "status", "--porcelain"); out != "" {
		t.Fatalf("после pick --record дерево не чистое:\n%s", out)
	}
	subject := recordCommitSubject("T-001", "Ревью")
	if subject != "docs(tasks): T-001 строка ревью в ход работы" {
		t.Fatalf("ярлык в subject не строчный: %q", subject)
	}
	if subj := gitOut(t, root, "log", "-1", "--pretty=%s"); subj != subject {
		t.Fatalf("тема коммита %q, жду %q", subj, subject)
	}
	if files := gitOut(t, root, "show", "--name-only", "--pretty="); files != "docs/tasks/T-001.md" {
		t.Fatalf("в коммите не только файл задачи: %q", files)
	}
	data, _ := os.ReadFile(taskFile)
	if !strings.Contains(string(data), "- Ревью: субагент sonnet/high") {
		t.Fatalf("строка ревью не в файле:\n%s", data)
	}
}

// TestRecordCommitLeavesIndexAlone: коммит строки не забирает в себя чужой
// staged-файл, тот же рубеж, что у taskctl -m.
func TestRecordCommitLeavesIndexAlone(t *testing.T) {
	isolateQuota(t)
	root := writeBoard(t)
	if err := os.MkdirAll(filepath.Join(root, "docs", "tasks"), 0o755); err != nil {
		t.Fatal(err)
	}
	taskFile := filepath.Join(root, "docs", "tasks", "T-002.md")
	if err := os.WriteFile(taskFile, []byte("# T-002\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitSetup(t, root)
	if err := os.WriteFile(filepath.Join(root, "stray.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitOut(t, root, "add", "stray.txt")

	if _, err := cmdPick(root, "T-002", true, roleExec, ""); err != nil {
		t.Fatalf("pick --record: %v", err)
	}

	if cached := gitOut(t, root, "diff", "--cached", "--name-only"); cached != "stray.txt" {
		t.Fatalf("чужой индекс тронут: %q", cached)
	}
	if files := gitOut(t, root, "show", "--name-only", "--pretty="); files != "docs/tasks/T-002.md" {
		t.Fatalf("в коммит ушёл чужой файл: %q", files)
	}
}

// TestRecordCommitFailureSpeaks: провал коммита строки печатается в stderr, а
// не молчит. Молчание вернуло бы исходный баг DK-120 в тихом виде: строка
// лежит, коммита нет, и узнаёт об этом только shipctl merge.
func TestRecordCommitFailureSpeaks(t *testing.T) {
	root := t.TempDir()
	gitOut(t, root, "init", "-q", "-b", "main")
	var warn bytes.Buffer
	prev := recordWarn
	recordWarn = &warn
	t.Cleanup(func() { recordWarn = prev })
	// В свежем пустом репозитории git add по несуществующему пути провалится.
	commitTaskRecord(root, filepath.Join("docs", "tasks", "X-001.md"), "subject")
	if !strings.Contains(warn.String(), "не закоммичена") {
		t.Fatalf("провал коммита молчит, stderr: %q", warn.String())
	}
}

// TestRecordPushSeenByHook: пуш коммита строки разрешён хуку pre-push
// переменной DEVKIT_PUSH_OK. Разрешение ставится окружением именно пуша, и
// единственный способ это увидеть - хук на живом пуше: он пишет значение
// переменной в файл, тест читает файл после прогона.
func TestRecordPushSeenByHook(t *testing.T) {
	isolateQuota(t)
	root := writeBoard(t)
	if err := os.MkdirAll(filepath.Join(root, "docs", "tasks"), 0o755); err != nil {
		t.Fatal(err)
	}
	taskFile := filepath.Join(root, "docs", "tasks", "T-001.md")
	if err := os.WriteFile(taskFile, []byte("# T-001\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitSetup(t, root)

	remote := filepath.Join(t.TempDir(), "remote.git")
	gitOut(t, root, "init", "-q", "--bare", remote)
	gitOut(t, root, "remote", "add", "origin", remote)
	gitOut(t, root, "push", "-q", "-u", "origin", "main")
	// Хук живёт на отправляющей стороне, поэтому hooksPath ставится самому
	// репозиторию с доской, а не голому remote.
	if err := os.MkdirAll(filepath.Join(root, "hooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	seen := filepath.Join(root, "hooks", "seen")
	if err := os.WriteFile(filepath.Join(root, "hooks", "pre-push"), []byte(
		"#!/bin/sh\nprintf 'DEVKIT_PUSH_OK=%s\\n' \"$DEVKIT_PUSH_OK\" > "+seen+"\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	gitOut(t, root, "config", "core.hooksPath", filepath.Join(root, "hooks"))

	taskRecordPush(root)

	data, err := os.ReadFile(seen)
	if err != nil {
		t.Fatal("хук pre-push не вызван: пуш не дошёл до remote")
	}
	if strings.TrimSpace(string(data)) != "DEVKIT_PUSH_OK=1" {
		t.Fatalf("хук не увидел разрешение: %q", string(data))
	}
}

// TestRecordPushArgv: пуш уходит ровно «git -C <корень> push», как у taskctl
// -m --push. Открутка credential.helper в этом пуше не лечит виснущий на
// залоченной связке osxkeychain, а отрезает рабочую учётку на разблокированной.
func TestRecordPushArgv(t *testing.T) {
	prev := taskRecordGitCmd
	var gotArgs []string
	var gotEnv []string
	taskRecordGitCmd = func(root string, env []string, args ...string) (string, error) {
		gotArgs, gotEnv = args, env
		return "", nil
	}
	t.Cleanup(func() { taskRecordGitCmd = prev })
	taskRecordPush("/tmp/root")
	if strings.Join(gotArgs, " ") != "push" {
		t.Fatalf("пуш ушёл не тем argv: %v", gotArgs)
	}
	ok := false
	for _, e := range gotEnv {
		if e == "DEVKIT_PUSH_OK=1" {
			ok = true
		}
	}
	if !ok {
		t.Fatalf("у пуша нет DEVKIT_PUSH_OK=1: %v", gotEnv)
	}
}
