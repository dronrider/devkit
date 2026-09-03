package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// branchNoID заводит ветку задачи, в коммитах которой ID не упомянут: ровно
// тот случай, ради которого коммиты пишутся в файл задачи. Тестовый файл
// выводится из имени правки, чтобы второй круг доработки дал свой тест, а не
// повторил уже слитый в main.
func branchNoID(t *testing.T, root, id, branch, file string) {
	t.Helper()
	gitT(t, root, "checkout", "-qb", branch, "main")
	write(t, root, file, "правка\n")
	write(t, root, strings.TrimSuffix(file, filepath.Ext(file))+"_test.go", "package main\n")
	gitT(t, root, "add", ".")
	gitT(t, root, "commit", "-qm", "fix: правка без номера задачи")
}

// TestMergeRecordsCommits: слияние пишет коммиты диапазона в файл задачи, и
// дальше очередь держится этой записью, а не ID в subject. Без записи merge
// следующей задачи прошёл бы при непроверенном выкате на проде.
func TestMergeRecordsCommits(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	branchNoID(t, root, "XR-001", "xr-001-fix", "code.txt")
	msg, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "в коммитах ветки нет XR-001 в subject") {
		t.Fatalf("про потерянный ID должно быть сказано: %q", msg)
	}
	if !strings.Contains(msg, "коммиты задачи записаны") {
		t.Fatalf("про запись должно быть сказано: %q", msg)
	}
	rec, err := mergedShas(root, "XR-001")
	if err != nil {
		t.Fatal(err)
	}
	if len(rec) != 1 {
		t.Fatalf("в записи ждал один коммит, получил %v", rec)
	}
	code := gitT(t, root, "log", "main", "--format=%h", "--grep", "правка без номера")
	if !inRecord(rec, code) {
		t.Fatalf("записан не тот коммит: %v против %s", rec, code)
	}
	// Коммит записи трогает только docs/, поэтому кодом не считается и в
	// очередь задачу не тянет сам по себе.
	if files := gitT(t, root, "show", "--name-only", "--pretty=", "main"); !docsOnly(files) {
		t.Fatalf("коммит записи трогает не только docs/: %s", files)
	}

	// Задача уехала в Check (стаб taskctl доску не двигает, ставим руками),
	// следующее слияние обязано упереться в занятую очередь.
	board, err := os.ReadFile(filepath.Join(root, "docs", "TASKS.md"))
	if err != nil {
		t.Fatal(err)
	}
	moved := strings.Replace(string(board), "## In progress\n\n"+section(rowInProg),
		"## In progress\n\n"+section(rowInProg3), 1)
	moved = strings.Replace(moved, "## Check\n\nНет.\n", "## Check\n\n"+section(rowInProg), 1)
	write(t, root, "docs/TASKS.md", moved)
	gitT(t, root, "add", ".")
	gitT(t, root, "commit", "-qm", "docs(tasks): XR-001 в Check")

	taskWithScenario(t, root, "XR-003")
	branchFor(t, root, "XR-003", "xr-003-fix", "b.txt")
	msg, err = cmdMerge(root, MergeParams{ID: "XR-003", Test: "true"})
	if err != nil {
		t.Fatal(err)
	}
	// Очередь держится записью, а не ID в subject: без записи XR-001 не
	// нашлась бы выкаченной, и слияние прошло бы обычным одиночным выкатом.
	if !strings.Contains(msg, "очередь занята: XR-001") {
		t.Fatalf("очередь должна держаться записью, а не ID в subject: %q", msg)
	}
	if !strings.Contains(msg, "слияние поездное") {
		t.Fatalf("при занятой очереди слияние должно стать поездным: %q", msg)
	}
}

// TestTrainTasksFromRecord: состав поезда собирается по записи. Задача с
// коммитами без ID иначе уехала бы на прод, не попав ни в поезд, ни в Check.
func TestTrainTasksFromRecord(t *testing.T) {
	root, _ := setup(t, rowInProg+rowInProg3, "")
	branchNoID(t, root, "XR-001", "xr-001-fix", "a.txt")
	if _, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true", Train: true}); err != nil {
		t.Fatal(err)
	}
	st, err := cmdStatus(root)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(st, "поезд: XR-001 слиты") {
		t.Fatalf("задача с коммитами без ID пропала из поезда:\n%s", st)
	}
}

// TestRevertFromRecord: откат находит коммиты задачи по записи. Раньше он
// отвечал «откатывать нечего», и прод оставался сломанным.
func TestRevertFromRecord(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	branchNoID(t, root, "XR-001", "xr-001-fix", "code.txt")
	if _, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true"}); err != nil {
		t.Fatal(err)
	}
	msg, err := cmdRevert(root, RevertParams{ID: "XR-001", Test: "true"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "откачено коммитов: 1") {
		t.Fatalf("откат по записи: %q", msg)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "code.txt")); string(got) != "old\n" {
		t.Fatalf("код не откатился: %q", got)
	}
}

// TestRecordSecondRound: круг доработки после возврата из Check дописывает
// строку в тот же раздел, прежние коммиты остаются в записи.
func TestRecordSecondRound(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	branchNoID(t, root, "XR-001", "xr-001-fix", "code.txt")
	if _, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true"}); err != nil {
		t.Fatal(err)
	}
	first, err := mergedShas(root, "XR-001")
	if err != nil {
		t.Fatal(err)
	}
	branchNoID(t, root, "XR-001", "xr-001-fix2", "code2.txt")
	if _, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true"}); err != nil {
		t.Fatal(err)
	}
	second, err := mergedShas(root, "XR-001")
	if err != nil {
		t.Fatal(err)
	}
	if len(second) != len(first)+1 {
		t.Fatalf("второй круг должен дописать коммит: было %v, стало %v", first, second)
	}
	body, _ := os.ReadFile(filepath.Join(root, "docs", "tasks", "XR-001.md"))
	if n := strings.Count(string(body), mergedSection); n != 1 {
		t.Fatalf("раздел «Выкат» задвоился (%d):\n%s", n, body)
	}
	if !strings.Contains(string(body), "## Ревью") {
		t.Fatalf("запись съела соседний раздел:\n%s", body)
	}
}

// TestMergedShasParse: разбор берёт из раздела только коммиты и не путается
// в соседних разделах и прозе.
func TestMergedShasParse(t *testing.T) {
	root := t.TempDir()
	write(t, root, "docs/tasks/XR-001.md", "# XR-001: заголовок\n\n"+
		"## Выкат\n\n- 2026-08-02 слито: 1a2b3c4, 5d6e7f8\n- 2026-08-03 слито: abcdef1\n"+
		"- перевыкат руками, коммит не записан\n\n"+
		"## Ревью\n\n- замечание про 9999999: исправлено\n")
	got, err := mergedShas(root, "XR-001")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"1a2b3c4", "5d6e7f8", "abcdef1"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("разбор записи: %v, ждал %v", got, want)
	}
	if none, err := mergedShas(root, "XR-777"); err != nil || none != nil {
		t.Fatalf("задача без файла: %v, %v", none, err)
	}
}

// TestMergedShasSkipsFenced: вывод, вложенный в файл задачи по сценарию
// проверки, цитирует чужую запись выката. Читая её как свою, очередь считает
// выкаченной не ту задачу: sha из цитаты в логе есть.
func TestMergedShasSkipsFenced(t *testing.T) {
	root := t.TempDir()
	write(t, root, "docs/tasks/XR-001.md", "# XR-001: заголовок\n\n"+
		"## Сценарий проверки\n\nВывод shipctl merge на синтетической доске:\n\n"+
		"```\n## Выкат\n\n- 2026-08-01 слито: 9999999, 8888888\n```\n\n"+
		"## Выкат\n\n- 2026-08-02 слито: 1a2b3c4\n")
	got, err := mergedShas(root, "XR-001")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != "1a2b3c4" {
		t.Fatalf("цитата из ограждённого блока попала в запись: %v", got)
	}
}

// TestAppendToSectionSkipsFenced: запись нового круга ищет свой раздел мимо
// цитаты, иначе строка уедет внутрь ограждённого блока и запись потеряется.
func TestAppendToSectionSkipsFenced(t *testing.T) {
	doc := "# XR-001: задача\n\n## Ход работы\n\n~~~\n## Выкат\n\n- 2026-08-01 слито: 9999999\n~~~\n\n" +
		"## Выкат\n\n- 2026-08-02 слито: 1111111\n"
	got := appendToSection(doc, "- 2026-08-03 слито: 2222222")
	want := "# XR-001: задача\n\n## Ход работы\n\n~~~\n## Выкат\n\n- 2026-08-01 слито: 9999999\n~~~\n\n" +
		"## Выкат\n\n- 2026-08-02 слито: 1111111\n- 2026-08-03 слито: 2222222\n"
	if got != want {
		t.Fatalf("строка встала не в свой раздел:\n%s", got)
	}
	// Раздела нет вовсе, есть только цитата: заводится настоящий раздел в
	// конце файла, а не дописывается строка в чужой вывод.
	only := "# XR-001: задача\n\n```\n## Выкат\n\n- 2026-08-01 слито: 9999999\n```\n"
	if got := appendToSection(only, "- 2026-08-03 слито: 2222222"); !strings.HasSuffix(got,
		"```\n\n## Выкат\n\n- 2026-08-03 слито: 2222222\n") {
		t.Fatalf("раздел не заведён рядом с цитатой:\n%s", got)
	}
}

// TestMergeIgnoresFencedReviewNote: замечание, процитированное в выводе
// внутри файла задачи, отбивало слияние, хотя своё ревью закрыто.
func TestMergeIgnoresFencedReviewNote(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	write(t, root, "docs/tasks/XR-001.md", "# XR-001: починка бага\n\n"+
		"## Сценарий проверки\n\nВывод taskctl review show соседней задачи:\n\n"+
		"```\n## Ревью\n\n- гонка в close без исхода\n```\n"+
		fixtureReviewLevel+"\n- нейминг: отклонено, стиль проекта\n")
	gitT(t, root, "add", ".")
	gitT(t, root, "commit", "-qm", "docs(tasks): XR-001 сценарий проверки")
	branchWithFix(t, root)
	if _, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true"}); err != nil {
		t.Fatalf("слияние отбито цитатой замечания: %v", err)
	}
}

// TestTrainOverlapFromRecord: подсказка про пересечение файлов поезда должна
// видеть коммиты задачи по записи. Иначе она молчит ровно там, где нужнее
// всего: на ветке, чьи коммиты ID не несут, а файл правят оба.
func TestTrainOverlapFromRecord(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	setBoard7(t, root)
	taskWithScenario(t, root, "XR-003")

	branchNoID(t, root, "XR-001", "xr-001-fix", "a.txt")
	if _, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true", Train: true}); err != nil {
		t.Fatal(err)
	}
	branchFor(t, root, "XR-003", "xr-003-fix", "a.txt")
	msg, err := cmdMerge(root, MergeParams{ID: "XR-003", Test: "true", Train: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "XR-001: a.txt") {
		t.Fatalf("пересечение с задачей поезда без ID в subject не найдено: %q", msg)
	}
}

// TestAppendToSectionKeepsTail: запись встаёт в конец своего раздела, а не в
// конец файла. Раздел «Выкат» оказывается последним не всегда: у задачи с
// разделом «Ревью» после него строка уехала бы в чужой раздел.
func TestAppendToSectionKeepsTail(t *testing.T) {
	doc := "# XR-001: задача\n\n## Выкат\n\n- 2026-08-01 слито: 1111111\n\n## Ревью\n\n- замечание: исправлено\n"
	got := appendToSection(doc, "- 2026-08-02 слито: 2222222")
	want := "# XR-001: задача\n\n## Выкат\n\n- 2026-08-01 слито: 1111111\n- 2026-08-02 слито: 2222222\n\n## Ревью\n\n- замечание: исправлено\n"
	if got != want {
		t.Fatalf("строка ушла из раздела:\n%s", got)
	}
}

// TestAppendToSectionGoesByForm: раздела «Выкат» нет, и он встаёт по форме
// TASKFORM.md перед «Проверкой», а не в хвост файла. Файл, где «Проверка»
// написана до слияния, иначе получал переставленный раздел и находку lint.
func TestAppendToSectionGoesByForm(t *testing.T) {
	doc := "# XR-001: задача\n\n## Сценарий проверки\n\n1. шаг\n\n## Проверка\n\nвывод прогона\n"
	got := appendToSection(doc, "- 2026-08-02 слито: 2222222")
	want := "# XR-001: задача\n\n## Сценарий проверки\n\n1. шаг\n\n## Выкат\n\n- 2026-08-02 слито: 2222222\n\n## Проверка\n\nвывод прогона\n"
	if got != want {
		t.Fatalf("«Выкат» не по форме:\n%s", got)
	}
}
