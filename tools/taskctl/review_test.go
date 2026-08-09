package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readTaskFile(t *testing.T, root, id string) string {
	t.Helper()
	data, err := os.ReadFile(taskFileAbs(root, id))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestReviewAddAndResolve(t *testing.T) {
	root := setup(t)
	msg, err := cmdReviewAdd(root, "XR-005", "гонка в close", CommitOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "замечание 1") {
		t.Fatalf("сообщение: %q", msg)
	}
	if _, err := cmdReviewAdd(root, "XR-005", "нейминг", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	want := "# XR-005\n" + fixtureScenario + "\n## Ревью\n\n- гонка в close\n- нейминг\n"
	if got := readTaskFile(t, root, "XR-005"); got != want {
		t.Fatalf("файл задачи:\n%q\nожидал:\n%q", got, want)
	}
	if _, err := cmdReviewResolve(root, "XR-005", 1, "fixed", "", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdReviewResolve(root, "XR-005", 2, "rejected", "стиль проекта", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	got := readTaskFile(t, root, "XR-005")
	for _, want := range []string{"- гонка в close: исправлено\n", "- нейминг: отклонено, стиль проекта\n"} {
		if !strings.Contains(got, want) {
			t.Errorf("в файле нет %q:\n%s", want, got)
		}
	}
	show, err := cmdReviewShow(root, "XR-005")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(show, "1. [исправлено]") || !strings.Contains(show, "2. [отклонено]") {
		t.Fatalf("show:\n%s", show)
	}
}

func TestReviewResolveValidation(t *testing.T) {
	root := setup(t)
	if _, err := cmdReviewAdd(root, "XR-005", "замечание", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdReviewResolve(root, "XR-005", 1, "rejected", "", CommitOpts{}); err == nil ||
		!strings.Contains(err.Error(), "--reason") {
		t.Fatalf("rejected без причины должен отбиваться: %v", err)
	}
	if _, err := cmdReviewResolve(root, "XR-005", 1, "wontfix", "", CommitOpts{}); err == nil ||
		!strings.Contains(err.Error(), "fixed или rejected") {
		t.Fatalf("неизвестный исход должен отбиваться: %v", err)
	}
	if _, err := cmdReviewResolve(root, "XR-005", 9, "fixed", "", CommitOpts{}); err == nil ||
		!strings.Contains(err.Error(), "замечания 9 нет") {
		t.Fatalf("номер мимо списка должен отбиваться: %v", err)
	}
	if _, err := cmdReviewResolve(root, "XR-005", 1, "fixed", "", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdReviewResolve(root, "XR-005", 1, "fixed", "", CommitOpts{}); err == nil ||
		!strings.Contains(err.Error(), "уже закрыто") {
		t.Fatalf("повторный resolve должен отбиваться: %v", err)
	}
	if _, err := cmdReviewAdd(root, "XR-404", "мимо", CommitOpts{}); err == nil ||
		!strings.Contains(err.Error(), "нет на доске") {
		t.Fatalf("add по чужому ID должен отбиваться: %v", err)
	}
}

func TestReviewAddCreatesFileAndLink(t *testing.T) {
	root := setup(t)
	// У XR-001 нет ни файла, ни ссылки на него: add заводит и то, и другое.
	msg, err := cmdReviewAdd(root, "XR-001", "первое замечание", CommitOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "файл задачи создан") {
		t.Fatalf("сообщение: %q", msg)
	}
	got := readTaskFile(t, root, "XR-001")
	if !strings.HasPrefix(got, "# XR-001: Средняя") || !strings.Contains(got, "- первое замечание") {
		t.Fatalf("файл задачи:\n%s", got)
	}
	b, err := LoadBoard(boardPath(root))
	if err != nil {
		t.Fatal(err)
	}
	if link := b.find("XR-001").Link; !strings.Contains(link, "tasks/XR-001.md") {
		t.Fatalf("ссылка в строке не обновилась: %q", link)
	}
}

func TestReviewKeepsOtherSections(t *testing.T) {
	root := setup(t)
	content := "# XR-005\n\n## Ревью\n\n- старое: исправлено\n\n## Проверка\n\nшаги\n"
	if err := os.WriteFile(taskFileAbs(root, "XR-005"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdReviewAdd(root, "XR-005", "новое", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	want := "# XR-005\n\n## Ревью\n\n- старое: исправлено\n- новое\n\n## Проверка\n\nшаги\n"
	if got := readTaskFile(t, root, "XR-005"); got != want {
		t.Fatalf("файл задачи:\n%q\nожидал:\n%q", got, want)
	}
	show, err := cmdReviewShow(root, "XR-005")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(show, "шаги") {
		t.Fatalf("show зацепил чужую секцию:\n%s", show)
	}
}

func TestReviewStats(t *testing.T) {
	root := setup(t)
	if msg, err := cmdReviewStats(root); err != nil || !strings.Contains(msg, "пока нет") {
		t.Fatalf("пустая статистика: %q, %v", msg, err)
	}
	for _, step := range []func() (string, error){
		func() (string, error) { return cmdReviewAdd(root, "XR-005", "раз", CommitOpts{}) },
		func() (string, error) { return cmdReviewAdd(root, "XR-005", "два", CommitOpts{}) },
		func() (string, error) { return cmdReviewResolve(root, "XR-005", 1, "fixed", "", CommitOpts{}) },
		func() (string, error) { return cmdReviewResolve(root, "XR-005", 2, "rejected", "мелочь", CommitOpts{}) },
		func() (string, error) { return cmdReviewAdd(root, "XR-002", "открытое", CommitOpts{}) },
	} {
		if _, err := step(); err != nil {
			t.Fatal(err)
		}
	}
	archived := filepath.Join(root, "docs", "tasks", "archive", "2025", "XR-100.md")
	if err := os.MkdirAll(filepath.Dir(archived), 0o755); err != nil {
		t.Fatal(err)
	}
	arch := "# XR-100\n\n## Ревью\n\n- a: исправлено\n- b: исправлено\n"
	if err := os.WriteFile(archived, []byte(arch), 0o644); err != nil {
		t.Fatal(err)
	}
	msg, err := cmdReviewStats(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"задач с ревью: 3 (живых 2, в архиве 1)",
		"замечаний 5: исправлено 3, отклонено 1, открыто 1",
		"доля исправленных среди закрытых: 75%",
		"XR-002, замечание 1: открытое",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("в статистике нет %q:\n%s", want, msg)
		}
	}
}

func TestReviewCommitFlag(t *testing.T) {
	root := setup(t)
	gitSetup(t, root)
	msg, err := cmdReviewAdd(root, "XR-005", "замечание", CommitOpts{Msg: "docs(tasks): XR-005 замечание ревью"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, ", коммит ") {
		t.Fatalf("сообщение без хеша коммита: %q", msg)
	}
	if files := gitOut(t, root, "show", "--name-only", "--pretty="); files != "docs/tasks/XR-005.md" {
		t.Fatalf("в коммите не только файл задачи: %q", files)
	}
}
