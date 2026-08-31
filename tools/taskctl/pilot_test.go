package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dronrider/devkit/internal/stage"
)

// pilotRepo это синтетический репозиторий под счётчики: git-история с
// коммитами провала и отката, файлы задач с «Ходом работы» и «Выкатом».
// Живая доска для этого не годится: счётчики читают её историю, и прогон против
// настоящего дерева мерил бы чужие задачи.
func pilotRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("нет git")
	}
	root := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.email", "t@example"},
		{"config", "user.name", "t"},
	} {
		if out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", args[0], err, out)
		}
	}
	if err := os.MkdirAll(filepath.Join(root, "docs", "tasks"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "docs", "TASKS.md"), []byte("# доска\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", root, "add", "-A").CombinedOutput(); err != nil {
		t.Fatalf("git add: %v\n%s", err, out)
	}
	gitCommitDated(t, root, time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC), "docs(tasks): доска заведена")
	return root
}

// writePilotTask кладёт файл задачи с заходами разработки и строками слияний.
func writePilotTask(t *testing.T, root, id string, merges, devs []string) {
	t.Helper()
	var b strings.Builder
	b.WriteString("# " + id + "\n\n## Ход работы\n\n")
	for _, d := range devs {
		b.WriteString("- Разработка: субагент sonnet/high по вердикту pick (квота: week_all 10%, снимок 1м назад, сдвига нет), " + d + " 12:00-12:40.\n")
	}
	b.WriteString("\n## Выкат\n\n")
	for _, m := range merges {
		b.WriteString("- " + m + " слито: abc1234\n")
	}
	if err := os.WriteFile(filepath.Join(root, "docs", "tasks", id+".md"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

// pilotRuns готовит каталог записей runs с живым этапом разработки задачи.
// Корень проходит через MainRoot, как у прод-писателей записи: пик и ask зовут
// Open именно так, и тест обязан класть файл туда же.
func pilotRuns(t *testing.T, root, id string, start time.Time) string {
	t.Helper()
	home := t.TempDir()
	if err := stage.Open(home, stage.MainRoot(root), id, stage.Dev, "субагент sonnet/high по вердикту pick", start); err != nil {
		t.Fatal(err)
	}
	return stage.Dir(home)
}

// pilotCommit коммитит пустышку с одним subject: событиям доски важен текст
// коммита, а не дерево, и стейджить ради них нечего.
func pilotCommit(t *testing.T, root string, when time.Time, msg string) {
	t.Helper()
	cmd := exec.Command("git", "-C", root, "commit", "-q", "--allow-empty", "-m", msg)
	ts := when.UTC().Format("2006-01-02T15:04:05Z")
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_DATE="+ts, "GIT_COMMITTER_DATE="+ts)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v\n%s", err, out)
	}
}

func TestPilotCounters(t *testing.T) {
	root := pilotRepo(t)

	// Окно пилота: PL-1 вернулась вторым слиянием, PL-2 чистая, но с откатом
	// выката и живым этапом runs, который файл ещё не забрал.
	writePilotTask(t, root, "PL-1",
		[]string{"2026-09-03", "2026-09-10"},
		[]string{"2026-09-01", "2026-09-04"})
	writePilotTask(t, root, "PL-2",
		[]string{"2026-09-05"},
		[]string{"2026-09-02"})
	// История до старта: PL-4 вернулась, PL-5 чистая, они ближе всех к границе
	// и попадают в зеркальное окно, PL-3 остаётся за его пределами.
	writePilotTask(t, root, "PL-3", []string{"2026-08-20"}, []string{"2026-08-18"})
	writePilotTask(t, root, "PL-4",
		[]string{"2026-08-25", "2026-08-28"},
		[]string{"2026-08-22"})
	writePilotTask(t, root, "PL-5", []string{"2026-08-30"}, []string{"2026-08-29"})
	// Архив обходится тем же шагом, что и живые файлы.
	if err := os.MkdirAll(filepath.Join(root, "docs", "tasks", "archive", "2026"), 0o755); err != nil {
		t.Fatal(err)
	}
	writePilotTask(t, root, filepath.Join("archive", "2026", "PL-6"), []string{"2026-08-10"}, []string{"2026-08-08"})
	// Неслитая задача в счёт не входит вовсе.
	writePilotTask(t, root, "PL-9", nil, []string{"2026-09-02"})

	// События доски: провал PL-1 и откат PL-2. Прочие subject идут приманками:
	// провал вне коммита доски, откат чужого ID, слово «откатом» в переводе
	// строки назад.
	pilotCommit(t, root, time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC), "docs(tasks): PL-1 провал проверки, разлив упал")
	pilotCommit(t, root, time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC), "revert: PL-2 откат abc1234")
	pilotCommit(t, root, time.Date(2026, 9, 6, 13, 0, 0, 0, time.UTC), "feat(x): PL-1 провал проверки ловится только коммитом доски")
	pilotCommit(t, root, time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC), "docs(tasks): PL-4 обратно в In progress, признак провала снят откатом")
	pilotCommit(t, root, time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC), "revert: XX-9 откат чужой")

	runs := pilotRuns(t, root, "PL-2", time.Date(2026, 9, 4, 10, 0, 0, 0, time.UTC))
	out, err := cmdPilot(root, "2026-09-01", runs)
	if err != nil {
		t.Fatalf("pilot: %v", err)
	}
	for _, want := range []string{
		"окно: 2 задачи, история до старта: 2 задачи",
		"возвраты с ревью: 1/2 (50.0%) против 1/2 (50.0%) у истории, рост 1.0",
		"заходы до слияния: 1.5 на задачу (2 задачи с записанными заходами) против 1.0 на задачу (2 задачи с записанными заходами) у истории",
		"краснота Check: 1/2 (50.0%) против 0/2 (0.0%) у истории",
		"откаты выката: 1 против 0 у истории",
		"вердикт: рост 1.0 в пределах порога 1.5, полоса держится",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("в выводе нет %q:\n%s", want, out)
		}
	}
}

// TestPilotRollbackVerdict: рост доли возвратов выше порога это приказ убирать
// полосу, и он обязан звучать в выводе, а не считаться на глаз.
func TestPilotRollbackVerdict(t *testing.T) {
	root := pilotRepo(t)
	writePilotTask(t, root, "PL-1",
		[]string{"2026-09-03", "2026-09-06"},
		[]string{"2026-09-01"})
	writePilotTask(t, root, "PL-2",
		[]string{"2026-09-05", "2026-09-08"},
		[]string{"2026-09-02"})
	writePilotTask(t, root, "PL-3", []string{"2026-08-20"}, []string{"2026-08-18"})
	writePilotTask(t, root, "PL-4",
		[]string{"2026-08-25", "2026-08-28"},
		[]string{"2026-08-22"})
	out, err := cmdPilot(root, "2026-09-01", t.TempDir())
	if err != nil {
		t.Fatalf("pilot: %v", err)
	}
	if !strings.Contains(out, "вердикт: возвраты выросли в 2.0 раза против истории, порог 1.5, полоса возвращается на прежнее место") {
		t.Fatalf("вердикт отката не прозвучал:\n%s", out)
	}
}

// TestPilotZeroReturns: в начале пилота возвратов нет ни в окне, ни в истории,
// и рост это 0.0, а не деление нуля на ноль.
func TestPilotZeroReturns(t *testing.T) {
	root := pilotRepo(t)
	writePilotTask(t, root, "PL-1", []string{"2026-09-03"}, []string{"2026-09-01"})
	writePilotTask(t, root, "PL-2", []string{"2026-09-05"}, []string{"2026-09-02"})
	writePilotTask(t, root, "PL-3", []string{"2026-08-20"}, []string{"2026-08-18"})
	writePilotTask(t, root, "PL-4", []string{"2026-08-25"}, []string{"2026-08-22"})
	out, err := cmdPilot(root, "2026-09-01", t.TempDir())
	if err != nil {
		t.Fatalf("pilot: %v", err)
	}
	if !strings.Contains(out, "возвраты с ревью: 0/2 (0.0%) против 0/2 (0.0%) у истории, рост 0.0") {
		t.Fatalf("рост на нуле возвратов не напечатан нулём:\n%s", out)
	}
	if !strings.Contains(out, "вердикт: возвратов в окне нет, полоса держится") {
		t.Fatalf("вердикт на нуле возвратов не прозвучал:\n%s", out)
	}
}

// TestPilotEmptyWindow: до первого слияния после даты старта счётчики пусты, и
// вывод обязан это сказать, а не делить нули.
func TestPilotEmptyWindow(t *testing.T) {
	root := pilotRepo(t)
	writePilotTask(t, root, "PL-3", []string{"2026-08-20"}, []string{"2026-08-18"})
	out, err := cmdPilot(root, "2026-09-01", t.TempDir())
	if err != nil {
		t.Fatalf("pilot: %v", err)
	}
	if !strings.Contains(out, "в окне нет слитых задач, счётчики пусты, сравнивать не с чем") {
		t.Fatalf("пустое окно не названо:\n%s", out)
	}
}

// TestPilotBadSince: дата среза разбирается командой, а не молча превращается
// в нулевую.
func TestPilotBadSince(t *testing.T) {
	root := pilotRepo(t)
	if _, err := cmdPilot(root, "сентябрь", t.TempDir()); err == nil {
		t.Fatal("ожидал отказ на неразборчивой дате")
	}
}
