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

func gitOut(t *testing.T, root string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestCommitFlagLeavesIndexAlone(t *testing.T) {
	root := setup(t)
	gitSetup(t, root)
	// Чужой staged-файл не должен попасть в коммит доски.
	if err := os.WriteFile(filepath.Join(root, "stray.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitOut(t, root, "add", "stray.txt")
	msg, err := cmdMove(root, "XR-004", SectInProgress, "", CommitOpts{Msg: "docs(tasks): XR-004 в работу"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, ", коммит ") {
		t.Fatalf("сообщение без хеша коммита: %q", msg)
	}
	if subj := gitOut(t, root, "log", "-1", "--pretty=%s"); subj != "docs(tasks): XR-004 в работу" {
		t.Fatalf("тема коммита: %q", subj)
	}
	if files := gitOut(t, root, "show", "--name-only", "--pretty="); files != "docs/TASKS.md" {
		t.Fatalf("в коммите не только доска: %q", files)
	}
	if cached := gitOut(t, root, "diff", "--cached", "--name-only"); cached != "stray.txt" {
		t.Fatalf("чужой индекс тронут: %q", cached)
	}
}

func TestCloseCommitIncludesRename(t *testing.T) {
	root := setup(t)
	gitSetup(t, root)
	p := CloseParams{ID: "XR-005", Date: "2026-07-08", Commit: CommitOpts{Msg: "docs(tasks): XR-005 закрыта"}}
	if _, err := cmdClose(root, p); err != nil {
		t.Fatal(err)
	}
	files := gitOut(t, root, "show", "--name-status", "--pretty=", "-M")
	for _, want := range []string{"docs/TASKS.md", "docs/TASKS-archive.md", "tasks/archive/2026/XR-005.md"} {
		if !strings.Contains(files, want) {
			t.Errorf("в коммите нет %s:\n%s", want, files)
		}
	}
	if st := gitOut(t, root, "status", "--porcelain", "docs"); st != "" {
		t.Fatalf("после close -m в docs/ осталось незакоммиченное: %q", st)
	}
}

// TestFileIdempotentSkipsEmptyCommit: холостой второй вызов file возвращается
// раньше c.apply, поэтому с -m и --push он не плодит пустой коммит и не падает
// на «git commit» без изменений (тот отдаёт код 1 сам по себе).
func TestFileIdempotentSkipsEmptyCommit(t *testing.T) {
	root := setup(t)
	gitSetup(t, root)
	if _, err := cmdFile(root, "XR-001", CommitOpts{Msg: "docs(tasks): XR-001 файл"}); err != nil {
		t.Fatal(err)
	}
	head1 := gitOut(t, root, "rev-parse", "HEAD")

	msg, err := cmdFile(root, "XR-001", CommitOpts{Msg: "docs(tasks): XR-001 файл", Push: true})
	if err != nil {
		t.Fatalf("холостой file с -m/--push не должен падать: %v", err)
	}
	if !strings.Contains(msg, "уже есть и файл, и ссылка") {
		t.Fatalf("сообщение: %q", msg)
	}
	if strings.Contains(msg, ", коммит ") {
		t.Fatalf("холостой вызов не должен коммитить: %q", msg)
	}
	head2 := gitOut(t, root, "rev-parse", "HEAD")
	if head1 != head2 {
		t.Fatalf("холостой вызов создал новый коммит: %s -> %s", head1, head2)
	}
}

// TestCommitMsgRejectsFlagValue: flag.StringVar берёт значением -m следующий
// аргумент, даже если тот сам флаг, поэтому вызов «... -m --push» даёт
// Msg "--push", а сам --push остаётся невыставленным. validate отбивает такое
// до записи на диск: иначе коммит получал бы subject "--push", пуш молча
// пропадал, и доска отставала от origin.
func TestCommitMsgRejectsFlagValue(t *testing.T) {
	root := setup(t)
	gitSetup(t, root)
	before, _ := os.ReadFile(boardPath(root))
	head0 := gitOut(t, root, "rev-parse", "HEAD")
	_, err := cmdMove(root, "XR-004", SectInProgress, "", CommitOpts{Msg: "--push"})
	if err == nil {
		t.Fatal("значение -m, начинающееся с дефиса, должно отбиваться")
	}
	if after, _ := os.ReadFile(boardPath(root)); string(after) != string(before) {
		t.Fatal("доска изменилась при отбитом -m")
	}
	if head1 := gitOut(t, root, "rev-parse", "HEAD"); head1 != head0 {
		t.Fatalf("отбитый -m создал коммит: %s -> %s", head0, head1)
	}
}

// TestCommitOptsValidate: границы проверки -m. Дефис в начале это проглоченный
// флаг (отказ), дефис в середине текста и рабочие формы с пушем проходят.
func TestCommitOptsValidate(t *testing.T) {
	cases := []struct {
		name string
		c    CommitOpts
		ok   bool
	}{
		{"пустое сообщение", CommitOpts{}, true},
		{"рабочее сообщение", CommitOpts{Msg: "docs(tasks): XR-004 в работу"}, true},
		{"дефис в середине текста", CommitOpts{Msg: "docs: -Werror потушен"}, true},
		{"-m --push проглочен", CommitOpts{Msg: "--push"}, false},
		{"-m -x проглочен", CommitOpts{Msg: "-x"}, false},
		{"--push без -m", CommitOpts{Push: true}, false},
		{"рабочее сообщение с пушем", CommitOpts{Msg: "docs: правка", Push: true}, true},
	}
	for _, tc := range cases {
		err := tc.c.validate()
		if tc.ok && err != nil {
			t.Errorf("%s: ждал успех, получил %v", tc.name, err)
		}
		if !tc.ok && err == nil {
			t.Errorf("%s: ждал отказ, получил успех", tc.name)
		}
	}
}

func TestPush(t *testing.T) {
	root := setup(t)
	gitSetup(t, root)
	// --push без -m отбивается до записи на диск.
	before, _ := os.ReadFile(boardPath(root))
	if _, err := cmdMove(root, "XR-004", SectCheck, "", CommitOpts{Push: true}); err == nil {
		t.Fatal("--push без -m должен падать")
	}
	if after, _ := os.ReadFile(boardPath(root)); string(after) != string(before) {
		t.Fatal("доска изменилась при отбитом --push")
	}
	remote := t.TempDir()
	gitOut(t, remote, "init", "-q", "--bare", "-b", "main")
	gitOut(t, root, "remote", "add", "origin", remote)
	gitOut(t, root, "push", "-q", "-u", "origin", "main")
	giveScenario(t, root, "XR-004")
	markRehearsed(t, root)
	c := CommitOpts{Msg: "docs(tasks): XR-004 в Check", Push: true}
	if _, err := cmdMove(root, "XR-004", SectCheck, "", c); err != nil {
		t.Fatal(err)
	}
	if subj := gitOut(t, remote, "log", "-1", "--pretty=%s"); subj != "docs(tasks): XR-004 в Check" {
		t.Fatalf("коммит не доехал до remote: %q", subj)
	}
}

// reviewJournal кладёт журнал чужого ревью рядом с файлом задачи: так его
// оставляют `review draft` и `publish` к моменту закрытия строки.
func reviewJournal(t *testing.T, root, id string) {
	t.Helper()
	body := "# Замечания ревью " + id + "\n\n" +
		"- MR: https://gl.example.com/group/proj/-/merge_requests/42\n" +
		"- ревью до: a1b2c3d\n- уровень: 2\n\n## Замечание 1\n\n" +
		"- метка: issue\n- состояние: снято, тред d1, MR закрыт\n\nворота merge не видят раздел\n"
	if err := os.WriteFile(reviewDraftAbs(root, id), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestCloseCommitCarriesReviewJournal: журнал чужого ревью уезжает в архив
// вместе с файлом задачи, и его переименование обязано войти в коммит
// закрытия. Без своего пути в pathspec `git commit -- <пути>` переименование
// остаётся в индексе, дерево доски стоит грязным, и следующий merge отбивается
// предполётом (хвост DK-758, починен DK-759).
func TestCloseCommitCarriesReviewJournal(t *testing.T) {
	root := setup(t)
	reviewJournal(t, root, "XR-005")
	gitSetup(t, root)
	p := CloseParams{ID: "XR-005", Date: "2026-07-08", Commit: CommitOpts{Msg: "docs(tasks): XR-005 закрыта"}}
	if _, err := cmdClose(root, p); err != nil {
		t.Fatal(err)
	}
	files := gitOut(t, root, "show", "--name-status", "--pretty=", "-M")
	for _, want := range []string{"docs/tasks/XR-005.review.md", "docs/tasks/archive/2026/XR-005.review.md"} {
		if !strings.Contains(files, want) {
			t.Errorf("в коммите закрытия нет %s:\n%s", want, files)
		}
	}
	if st := gitOut(t, root, "status", "--porcelain"); st != "" {
		t.Fatalf("после close -m дерево не чистое: %q", st)
	}
}

// TestCloseCommitWithoutJournalUnchanged: у задачи без чужого ревью закрытие
// осталось прежним. Пути журнала в pathspec появляются только там, где журнал
// был, иначе `git commit` падал бы на несуществующем пути.
func TestCloseCommitWithoutJournalUnchanged(t *testing.T) {
	root := setup(t)
	gitSetup(t, root)
	p := CloseParams{ID: "XR-005", Date: "2026-07-08", Commit: CommitOpts{Msg: "docs(tasks): XR-005 закрыта"}}
	if _, err := cmdClose(root, p); err != nil {
		t.Fatal(err)
	}
	files := gitOut(t, root, "show", "--name-status", "--pretty=", "-M")
	if strings.Contains(files, ".review.md") {
		t.Errorf("в коммите закрытия взялся журнал ревью:\n%s", files)
	}
	if st := gitOut(t, root, "status", "--porcelain"); st != "" {
		t.Fatalf("после close -m дерево не чистое: %q", st)
	}
}
