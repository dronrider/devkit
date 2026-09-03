package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readDraftFile(t *testing.T, root, id string) string {
	t.Helper()
	data, err := os.ReadFile(reviewDraftAbs(root, id))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func writeDraftFile(t *testing.T, root, id, body string) {
	t.Helper()
	if err := os.WriteFile(reviewDraftAbs(root, id), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// setupDraft готовит строку с файлом задачи, где уже стоят ссылка на MR и
// строка уровня: ровно то, что кладут trackctl review и review level до
// первого замечания.
func setupDraft(t *testing.T) string {
	t.Helper()
	root := setup(t)
	body := "# XR-005\n\n## Что происходит\n\nЧужой тикет.\n\nMR: https://gl.example.com/group/proj/-/merge_requests/42\n" +
		fixtureScenario + fixtureVerification +
		"\n## Ревью\n\nУровень 2 до a1b2c3d: неопределённость 1, тронут tools/shipctl.\n"
	if err := os.WriteFile(taskFileAbs(root, "XR-005"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

// TestReviewDraftTwoBlocks: два вызова draft дают два блока в состоянии
// «черновик», шапка берёт MR, sha и уровень из файла задачи.
func TestReviewDraftTwoBlocks(t *testing.T) {
	root := setupDraft(t)
	if _, err := cmdReviewDraft(root, "XR-005", "ворота merge не видят раздел за ограждённым блоком",
		reviewDraftParams{File: "tools/shipctl/ops.go", Line: 214}, CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	msg, err := cmdReviewDraft(root, "XR-005", "имя флага --at читается как время", reviewDraftParams{Label: "suggestion"}, CommitOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "замечание 2") {
		t.Fatalf("сообщение: %q", msg)
	}
	got := readDraftFile(t, root, "XR-005")
	for _, want := range []string{
		"- MR: https://gl.example.com/group/proj/-/merge_requests/42",
		"- ревью до: a1b2c3d",
		"- уровень: 2",
		"## Замечание 1",
		"- файл: tools/shipctl/ops.go",
		"- строка: 214",
		"- метка: issue",
		"- состояние: черновик",
		"## Замечание 2",
		"- метка: suggestion (non-blocking)",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("в файле замечаний нет %q:\n%s", want, got)
		}
	}
	d, err := loadReviewDraft(reviewDraftAbs(root, "XR-005"), "XR-005")
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Blocks) != 2 || d.Blocks[0].State != reviewStateNew || d.Blocks[1].Label != reviewLabelSuggestion {
		t.Fatalf("разбор своего же файла разошёлся с записью: %+v", d.Blocks)
	}
}

// TestReviewDraftSummaryBlock: итоговый комментарий уровня идёт тем же блоком
// без файла и строки, а с файлом отбивается.
func TestReviewDraftSummaryBlock(t *testing.T) {
	root := setupDraft(t)
	if _, err := cmdReviewDraft(root, "XR-005", "проверен живой путь по DoD и прогон тестов ветки",
		reviewDraftParams{Label: "итог"}, CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	if got := readDraftFile(t, root, "XR-005"); !strings.Contains(got, "- метка: итог") {
		t.Fatalf("итоговый блок не записан:\n%s", got)
	}
	_, err := cmdReviewDraft(root, "XR-005", "итог", reviewDraftParams{Label: "итог", File: "a.go"}, CommitOpts{})
	if err == nil || !strings.Contains(err.Error(), "без файла и строки") {
		t.Fatalf("итог с файлом должен отбиваться, вышло: %v", err)
	}
}

// TestReviewDraftManualEdit: ручная правка не ломает структуру. Порядок полей
// свободный, лишние пустые строки терпятся, список внутри текста реплики полем
// не считается.
func TestReviewDraftManualEdit(t *testing.T) {
	root := setupDraft(t)
	writeDraftFile(t, root, "XR-005", `# Замечания ревью XR-005

- MR: https://gl.example.com/group/proj/-/merge_requests/42
- ревью до: a1b2c3d
- уровень: 2

## Замечание 1

- состояние: одобрено
- метка: issue
- строка: 12
- файл: tools/x.go


issue правится по смыслу, а не по строке:

- первое место
- второе место

## Замечание

- метка: итог
- состояние: опубликовано, тред 7f21c

что проверено
`)
	d, err := loadReviewDraft(reviewDraftAbs(root, "XR-005"), "XR-005")
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Blocks) != 2 {
		t.Fatalf("блоков %d, ждём 2", len(d.Blocks))
	}
	first := d.Blocks[0]
	if first.File != "tools/x.go" || first.Line != "12" || first.State != reviewStateApproved {
		t.Fatalf("поля первого блока разобраны не так: %+v", first)
	}
	if !strings.Contains(first.Text, "- первое место") {
		t.Fatalf("список внутри текста реплики потерян: %q", first.Text)
	}
	if d.Blocks[1].Thread != "7f21c" || !d.Blocks[1].published() {
		t.Fatalf("id треда не разобран: %+v", d.Blocks[1])
	}
	// Следующая запись перенумеровывает заголовки: блок без номера получает
	// свой, руками номера держать не надо.
	if _, err := cmdReviewDraft(root, "XR-005", "третье", reviewDraftParams{}, CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	if got := readDraftFile(t, root, "XR-005"); !strings.Contains(got, "## Замечание 2\n") || !strings.Contains(got, "## Замечание 3\n") {
		t.Fatalf("заголовки не перенумерованы:\n%s", got)
	}
}

// TestReviewDraftBrokenBlock: нечитаемый блок это отказ с именем блока, а не
// молча проглоченное поле. Отказывает и разбор, и всякая команда над файлом.
func TestReviewDraftBrokenBlock(t *testing.T) {
	root := setupDraft(t)
	writeDraftFile(t, root, "XR-005", `# Замечания ревью XR-005

- MR: https://gl.example.com/group/proj/-/merge_requests/42
- ревью до: a1b2c3d
- уровень: 2

## Замечание 1

- метка: issue
- состояние: черновик

первое

## Замечание 2

- метка: issue
- состояние: подумаю

второе
`)
	for _, call := range []struct {
		name string
		run  func() error
	}{
		{"approve", func() error { _, err := cmdReviewApprove(root, "XR-005", 1, CommitOpts{}); return err }},
		{"draft", func() error {
			_, err := cmdReviewDraft(root, "XR-005", "третье", reviewDraftParams{}, CommitOpts{})
			return err
		}},
		{"publish", func() error { _, err := cmdReviewPublish(root, "XR-005", CommitOpts{}); return err }},
	} {
		err := call.run()
		if err == nil {
			t.Fatalf("%s: нечитаемый файл должен отбиваться", call.name)
		}
		if !strings.Contains(err.Error(), "замечание 2") || !strings.Contains(err.Error(), "подумаю") {
			t.Fatalf("%s: отказ без имени блока и без места: %v", call.name, err)
		}
	}
}

// TestReviewDraftBrokenHead: непонятный ключ шапки тоже отказ, а не тихая
// потеря ссылки на MR.
func TestReviewDraftBrokenHead(t *testing.T) {
	root := setupDraft(t)
	writeDraftFile(t, root, "XR-005", "# Замечания ревью XR-005\n\n- ссылка: https://x\n\n## Замечание 1\n\n- метка: issue\n- состояние: черновик\n\nтекст\n")
	_, err := cmdReviewApprove(root, "XR-005", 1, CommitOpts{})
	if err == nil || !strings.Contains(err.Error(), "шапка") {
		t.Fatalf("отказ шапки: %v", err)
	}
}

// TestReviewApproveAndDrop: одобрение и снятие переводят состояние блока,
// повтор отбивается, опубликованный блок не трогается.
func TestReviewApproveAndDrop(t *testing.T) {
	root := setupDraft(t)
	for _, text := range []string{"первое", "второе", "третье"} {
		if _, err := cmdReviewDraft(root, "XR-005", text, reviewDraftParams{}, CommitOpts{}); err != nil {
			t.Fatal(err)
		}
	}
	msg, err := cmdReviewApprove(root, "XR-005", 1, CommitOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "первое") {
		t.Fatalf("одобрение без эха текста: %q", msg)
	}
	if _, err := cmdReviewDrop(root, "XR-005", 2, CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	d, err := loadReviewDraft(reviewDraftAbs(root, "XR-005"), "XR-005")
	if err != nil {
		t.Fatal(err)
	}
	if d.Blocks[0].State != reviewStateApproved || d.Blocks[1].State != reviewStateDropped || d.Blocks[2].State != reviewStateNew {
		t.Fatalf("состояния блоков: %+v", d.Blocks)
	}
	if _, err := cmdReviewApprove(root, "XR-005", 1, CommitOpts{}); err == nil {
		t.Fatal("повторное одобрение должно отбиваться")
	}
	if _, err := cmdReviewDrop(root, "XR-005", 9, CommitOpts{}); err == nil {
		t.Fatal("номер вне списка должен отбиваться")
	}
	d.Blocks[2].State = reviewStatePublished
	d.Blocks[2].Thread = "7f21c"
	if err := d.save(); err != nil {
		t.Fatal(err)
	}
	_, err = cmdReviewDrop(root, "XR-005", 3, CommitOpts{})
	if err == nil || !strings.Contains(err.Error(), "уже опубликовано") {
		t.Fatalf("снятие опубликованного: %v", err)
	}
}

// TestReviewDraftNeedsRow: файл замечаний привязан к строке доски, ID с
// опечаткой второго журнала не заводит.
func TestReviewDraftNeedsRow(t *testing.T) {
	root := setupDraft(t)
	_, err := cmdReviewDraft(root, "XR-777", "текст", reviewDraftParams{}, CommitOpts{})
	if err == nil || !strings.Contains(err.Error(), "нет на доске") {
		t.Fatalf("строка без доски: %v", err)
	}
	if _, err := os.Stat(reviewDraftAbs(root, "XR-777")); err == nil {
		t.Fatal("файл замечаний заведён строке, которой нет")
	}
}

// TestReviewDraftNoFileYet: команды над несуществующим файлом отказывают с
// подсказкой, с чего начать.
func TestReviewDraftNoFileYet(t *testing.T) {
	root := setupDraft(t)
	_, err := cmdReviewApprove(root, "XR-005", 1, CommitOpts{})
	if err == nil || !strings.Contains(err.Error(), "review draft") {
		t.Fatalf("отказ без подсказки: %v", err)
	}
}

// TestLintSkipsReviewJournal: журнал ревью лежит в docs/tasks рядом с файлами
// задач, и сторож сирот не должен принимать его за файл задачи с чужим ID.
func TestLintSkipsReviewJournal(t *testing.T) {
	root := setupDraft(t)
	if _, err := cmdReviewDraft(root, "XR-005", "текст", reviewDraftParams{}, CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	finds, err := cmdLint(root)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(finds, "\n"); strings.Contains(got, "XR-005.review.md") {
		t.Fatalf("журнал ревью попал в находки сторожа:\n%s", got)
	}
}

// TestCloseArchivesReviewJournal: журнал уезжает в архив вместе с файлом
// задачи, а не остаётся в docs/tasks без хозяина.
func TestCloseArchivesReviewJournal(t *testing.T) {
	root := setupDraft(t)
	if _, err := cmdReviewDraft(root, "XR-005", "текст", reviewDraftParams{}, CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdClose(root, CloseParams{ID: "XR-005", Commits: "deadbee", Date: "2026-07-08"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(reviewDraftAbs(root, "XR-005")); err == nil {
		t.Fatal("журнал остался в docs/tasks после закрытия")
	}
	if _, err := os.Stat(filepath.Join(root, "docs", "tasks", "archive", "2026", "XR-005.review.md")); err != nil {
		t.Fatalf("журнала нет в архиве: %v", err)
	}
}
