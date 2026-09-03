package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// noteRepo это репозиторий без доски: ровно тот случай, ради которого заведён
// режим заметки.
func noteRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	noteGit(t, root, "init", "-q", "-b", "main")
	noteGit(t, root, "config", "user.email", "test@test")
	noteGit(t, root, "config", "user.name", "test")
	noteCommitFile(t, root, "code.txt", "первый\n", "feat: первая правка")
	return root
}

func noteGit(t *testing.T, root string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func noteCommitFile(t *testing.T, root, name, body, subject string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	noteGit(t, root, "add", ".")
	noteGit(t, root, "commit", "-qm", subject)
}

// TestNoteLevelWrites: уровень ревью вне доски ложится заметкой на HEAD вместе
// с ярлыком правки.
func TestNoteLevelWrites(t *testing.T) {
	root := noteRepo(t)
	msg, err := cmdNoteLevel(root, "MR-42", 2, "неопределённость 1, тронут разбор диффа", CommitOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "MR-42") || !strings.Contains(msg, "заметкой git") {
		t.Fatalf("ответ не называет ярлык и заметку: %q", msg)
	}
	note := noteGit(t, root, "notes", "--ref=review", "show", "HEAD")
	if !strings.HasPrefix(note, "Уровень 2 до ") {
		t.Fatalf("заметка начинается не со строки уровня:\n%s", note)
	}
	if !strings.Contains(note, "\nЯрлык: MR-42") {
		t.Fatalf("ярлык не доехал до заметки:\n%s", note)
	}
	if sha, err := headSha(root); err != nil || !strings.Contains(note, sha) {
		t.Fatalf("в строке уровня нет sha HEAD (%v):\n%s", err, note)
	}
}

// TestNoteLevelRewrites: повторный вызов переписывает заметку, а не кладёт
// вторую строку следом.
func TestNoteLevelRewrites(t *testing.T) {
	root := noteRepo(t)
	if _, err := cmdNoteLevel(root, "MR-42", 1, "рутина", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	msg, err := cmdNoteLevel(root, "MR-42", 3, "тронуты доступы", CommitOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "переписан") {
		t.Fatalf("ответ не говорит о переписанном следе: %q", msg)
	}
	note := noteGit(t, root, "notes", "--ref=review", "show", "HEAD")
	if strings.Contains(note, "рутина") {
		t.Fatalf("прежняя строка осталась в заметке:\n%s", note)
	}
	if !strings.Contains(note, "Уровень 3") {
		t.Fatalf("новый уровень не записался:\n%s", note)
	}
}

// TestNoteCleanCountsRounds: круг без замечаний пишется своей строкой, а после
// правок на новом коммите счёт идёт дальше, а не с единицы.
func TestNoteCleanCountsRounds(t *testing.T) {
	root := noteRepo(t)
	msg, err := cmdNoteClean(root, "MR-42", "путь от симптома пройден", CommitOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "круг 1") {
		t.Fatalf("первый круг посчитан не первым: %q", msg)
	}
	note := noteGit(t, root, "notes", "--ref=review", "show", "HEAD")
	if !strings.HasPrefix(note, "Круг 1 до ") {
		t.Fatalf("заметка начинается не со строки круга:\n%s", note)
	}
	noteCommitFile(t, root, "code.txt", "второй\n", "fix: правки по замечаниям")
	msg, err = cmdNoteClean(root, "MR-42", "", CommitOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "круг 2") {
		t.Fatalf("круг после правок посчитан не вторым: %q", msg)
	}
}

// TestNoteShow: show печатает заметку HEAD, а без неё говорит, что следа нет.
func TestNoteShow(t *testing.T) {
	root := noteRepo(t)
	msg, err := cmdNoteShow(root, "MR-42")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "нет заметки ревью") {
		t.Fatalf("пустой след показан не как пустой: %q", msg)
	}
	if _, err := cmdNoteLevel(root, "MR-42", 2, "разбор диффа", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	msg, err = cmdNoteShow(root, "MR-42")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "Уровень 2 до ") || !strings.Contains(msg, "Ярлык: MR-42") {
		t.Fatalf("show печатает заметку не целиком: %q", msg)
	}
}

// TestNoteOutsideGitRefused: вне git-дерева запись отказывает, а не молчит.
func TestNoteOutsideGitRefused(t *testing.T) {
	dir := t.TempDir()
	if _, err := cmdNoteLevel(dir, "MR-42", 2, "разбор диффа", CommitOpts{}); err == nil {
		t.Fatal("запись следа вне git-дерева должна отказывать")
	}
}

// TestNoteCommitFlagsRefused: -m и --push в режиме заметки отказывают, а не
// молчат: коммитить тут нечего.
func TestNoteCommitFlagsRefused(t *testing.T) {
	root := noteRepo(t)
	_, err := cmdNoteLevel(root, "MR-42", 2, "разбор диффа", CommitOpts{Msg: "docs: след"})
	if err == nil {
		t.Fatal("-m в режиме заметки должен отказывать")
	}
	if !strings.Contains(err.Error(), "заметкой") {
		t.Fatalf("отказ не называет причину: %v", err)
	}
}

// TestNoteRootPrefersBoard: доска вверх от директории главнее заметки, и в
// проекте с доской режим не включается.
func TestNoteRootPrefersBoard(t *testing.T) {
	board := setup(t)
	if got := noteRoot(board); got != "" {
		t.Fatalf("в проекте с доской включился режим заметки: %q", got)
	}
	repo := noteRepo(t)
	got := noteRoot(repo)
	if got == "" {
		t.Fatal("в репозитории без доски режим заметки не включился")
	}
	// t.TempDir на macOS отдаёт путь через /var, а git печатает его через
	// /private/var: сверяем по хвосту, а не по строке целиком.
	if filepath.Base(got) != filepath.Base(repo) {
		t.Fatalf("корень заметки посчитан не тем деревом: %q, ждали %q", got, repo)
	}
	if noteRoot(t.TempDir()) != "" {
		t.Fatal("вне git-дерева режим заметки включаться не должен")
	}
}
