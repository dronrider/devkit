package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gitSetup превращает временную доску в git-репозиторий с начальным коммитом.
func gitSetup(t *testing.T, root string) {
	t.Helper()
	gitOut(t, root, "init", "-q", "-b", "main")
	gitOut(t, root, "config", "user.email", "test@test")
	gitOut(t, root, "config", "user.name", "test")
	gitOut(t, root, "add", ".")
	gitOut(t, root, "commit", "-q", "-m", "init")
}

// gitSetupNoRemote это gitSetup без remote: git init в tempdir не создаёт
// origin, и пуш коммита строки уходит в честный отказ «нет destination», а
// не вешает прогон попыткой сети.
func gitSetupNoRemote(t *testing.T, root string) {
	t.Helper()
	gitSetup(t, root)
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
	gitSetupNoRemote(t, root)

	if _, err := cmdPick(root, "T-001", true, roleReview, ""); err != nil {
		t.Fatalf("pick --role review --record: %v", err)
	}

	if out := gitOut(t, root, "status", "--porcelain"); out != "" {
		t.Fatalf("после pick --record дерево не чистое:\n%s", out)
	}
	subject := recordCommitSubject("T-001", "Ревью")
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
	gitSetupNoRemote(t, root)
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
