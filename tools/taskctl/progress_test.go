package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withSections заводит задаче файл с выбранными разделами поверх болванки:
// так выглядит файл задачи, прошедший ревью (раздел «Ревью») и слияние
// (раздел «Выкат», его кладёт shipctl).
func withSections(t *testing.T, root, id string, sections ...string) {
	t.Helper()
	body := "# " + id + "\n" + fixtureScenario + fixtureVerification
	for _, s := range sections {
		body += "\n## " + s + "\n\n- запись раздела\n"
	}
	if err := os.WriteFile(filepath.Join(root, "docs", "tasks", id+".md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// wantProgress сверяет рубеж целиком: число, название и признак.
func wantProgress(t *testing.T, root, id string, f float64, mark, sign string) {
	t.Helper()
	p, err := progressOf(root, id)
	if err != nil {
		t.Fatalf("progressOf(%s): %v", id, err)
	}
	if p.F != f || p.Mark != mark || p.Sign != sign {
		t.Fatalf("progressOf(%s) = %.2f/%s/%s; ожидал %.2f/%s/%s", id, p.F, p.Mark, p.Sign, f, mark, sign)
	}
}

// TestProgressMarks прогоняет все пять рубежей по признакам из LLD DK-400
// (решение 1): пустая строка 0.00, раздел «Ревью» 0.56, раздел «Выкат» 0.89,
// строка в архиве 1.00. Признак «первый коммит кода» живёт в git и покрыт
// отдельным тестом ниже.
func TestProgressMarks(t *testing.T) {
	root := setup(t)
	wantProgress(t, root, "XR-005", progressBoard,
		"строка заведена", "строка на доске, разделов файла и ветки нет")
	wantProgress(t, root, "XR-007", progressDone,
		"приёмка пройдена", "строка в архиве")

	withSections(t, root, "XR-005", "Ревью")
	wantProgress(t, root, "XR-005", progressReady,
		"код и тесты готовы", "раздел «Ревью» в docs/tasks/XR-005.md")

	// «Выкат» старше «Ревью»: проверка идёт от старшего к младшему, и задача
	// с обоими разделами обязана отдавать 0.89, а не 0.56.
	withSections(t, root, "XR-005", "Ревью", "Выкат")
	wantProgress(t, root, "XR-005", progressRelease,
		"ревью пройдено, слито и выкачено", "раздел «Выкат» в docs/tasks/XR-005.md")
}

// TestProgressIgnoresFenced: заголовки внутри ограждённого блока это чужой
// вывод прогона, вложенный в файл задачи, а не её разделы. Рубеж по цитате
// врал бы «слито» задаче, которая ничего не выкатывала.
func TestProgressIgnoresFenced(t *testing.T) {
	root := setup(t)
	doc := "# XR-005\n" + fixtureScenario + "\n## Прогон\n\n```\n## Выкат\n\n- слито\n```\n"
	if err := os.WriteFile(filepath.Join(root, "docs", "tasks", "XR-005.md"), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	if p, err := progressOf(root, "XR-005"); err != nil || p.F != progressBoard {
		t.Fatalf("рубеж по цитате в блоке кода: %.2f, %v; ожидал 0.00", p.F, err)
	}
}

// TestProgressBranchCommit: живая ветка задачи с коммитами впереди main это
// признак рубежа 0.35. Ветка называется по ID строчными, как того требует
// branchOfTask, общий опознаватель с воротами Check.
func TestProgressBranchCommit(t *testing.T) {
	root := setup(t)
	gitOut(t, root, "init", "-q", "-b", "main")
	gitOut(t, root, "config", "user.email", "test@test")
	gitOut(t, root, "config", "user.name", "test")
	gitOut(t, root, "add", "docs/TASKS.md")
	gitOut(t, root, "commit", "-q", "-m", "init")

	wantProgress(t, root, "XR-005", progressBoard,
		"строка заведена", "строка на доске, разделов файла и ветки нет")
	branchWithCommit(t, root, "xr-005")
	p, err := progressOf(root, "XR-005")
	if err != nil {
		t.Fatal(err)
	}
	if p.F != progressCommit || p.Mark != "первый коммит кода" {
		t.Fatalf("ветка с коммитом не подняла рубеж: %.2f (%s)", p.F, p.Mark)
	}
	if !strings.Contains(p.Sign, "xr-005") || !strings.Contains(p.Sign, "1") {
		t.Fatalf("признак не называет ветку и счёт коммитов: %s", p.Sign)
	}
}

// TestProgressSurvivesStatusChange: рубеж производный от долговечных меток,
// и смена статуса его не стирает. Возврат из Check в работу и парковка в
// Blocked оставляют разделы файла на месте, значит F остаётся 0.89 (LLD
// DK-400, решение 1: монотонность).
func TestProgressSurvivesStatusChange(t *testing.T) {
	root := setup(t)
	withSections(t, root, "XR-005", "Ревью", "Выкат")
	f := func() float64 {
		p, err := progressOf(root, "XR-005")
		if err != nil {
			t.Fatal(err)
		}
		return p.F
	}
	if f() != progressRelease {
		t.Fatalf("до смены статуса рубеж не 0.89: %.2f", f())
	}
	if _, err := cmdMove(root, "XR-005", SectCheck, "", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdMove(root, "XR-005", SectInProgress, "", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdMove(root, "XR-005", SectBlocked, "вопрос: ждём ответа", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	if got := f(); got != progressRelease {
		t.Fatalf("смена статуса стёрла рубеж: %.2f", got)
	}
}

// TestProgressJSON: машинный вид рубежа для планировщика слота и дашборда
// (DK-404 читают одну команду, второй копии рубежа нет).
func TestProgressJSON(t *testing.T) {
	root := setup(t)
	withSections(t, root, "XR-005", "Ревью")
	out, err := cmdProgressJSON(root, "XR-005")
	if err != nil {
		t.Fatal(err)
	}
	want := `{"id":"XR-005","f":0.56,"mark":"код и тесты готовы","sign":"раздел «Ревью» в docs/tasks/XR-005.md"}`
	if out != want {
		t.Fatalf("json:\n%s\nожидал:\n%s", out, want)
	}
}

// TestProgressPrint: печатный вывод несёт число и признак одной строкой.
func TestProgressPrint(t *testing.T) {
	root := setup(t)
	withSections(t, root, "XR-005", "Ревью")
	out, err := cmdProgress(root, "XR-005")
	if err != nil {
		t.Fatal(err)
	}
	want := "XR-005: рубеж 0.56 (код и тесты готовы: раздел «Ревью» в docs/tasks/XR-005.md)"
	if out != want {
		t.Fatalf("печать: %q, ожидал %q", out, want)
	}
}

// TestProgressUnknown: у несуществующей ID и у черновика рубежа нет, отказ
// называет, кого спрашивали.
func TestProgressUnknown(t *testing.T) {
	root := setup(t)
	if _, err := progressOf(root, "XR-404"); err == nil ||
		!strings.Contains(err.Error(), "нет ни на доске, ни в архиве") {
		t.Fatalf("отказ за неизвестной задачей не называет её: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "docs", "tasks", "drafts"), 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(root, "docs", "tasks", "drafts", "XR-009.md")
	if err := os.WriteFile(p, []byte("идея мимо доски\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := progressOf(root, "XR-009"); err == nil ||
		!strings.Contains(err.Error(), "черновик") {
		t.Fatalf("черновик не отбивается: %v", err)
	}
}
