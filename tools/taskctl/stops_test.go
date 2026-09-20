package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dronrider/devkit/internal/stage"
)

// stopsFixtureBoard держит одну строку в Blocked: DK-906 нужна collectLiveStops,
// остальные задачи счёта в докe DK-901..DK-908 на доску не заводятся вовсе,
// закрытые остановки читаются прямо из файлов задач, доска им не нужна.
const stopsFixtureBoard = `# Тест: доска (префикс DK)

## In progress

| ID | Задача | Тип | P | R | Цена | Ссылка |
|--------|--------|-----|---|---|------|--------|

## Check (готово, ждёт проверки пользователем)

| ID | Задача | Тип | P | R | Цена | Ссылка |
|--------|--------|-----|---|---|------|--------|

## Backlog

| ID | Задача | Тип | P | R | Цена | Ссылка |
|--------|--------|-----|---|---|------|--------|

## Blocked

| ID | Задача | Тип | P | R | Цена | Ссылка |
|--------|--------|-----|---|---|------|--------|
| DK-906 | Живая остановка | task | P2 | 30 (25+2+1+0+2) | - | [tasks/DK-906.md](tasks/DK-906.md) |
`

// setupStops кладёт доску и docs/tasks без git: закрытые остановки читаются из
// раздела «Ход работы» файлов задач, живые из отдельного каталога runs,
// коммиты счёту не нужны.
func setupStops(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "docs", "tasks"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(boardPath(root), []byte(stopsFixtureBoard), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

// writeStopsTask кладёт файл задачи с одной закрытой остановкой в «Ход
// работы»: строку рисует тот же stage.Lines, каким флашится пакет на смене
// статуса, поэтому разбор строки в cmdStops гарантированно читает тот же
// формат, что пишет прод-код.
func writeStopsTask(t *testing.T, root, id, note string, start time.Time, dur time.Duration) {
	t.Helper()
	stages := []stage.Stage{{Kind: stage.WaitHuman, Start: start, Note: note}}
	var b strings.Builder
	b.WriteString("# " + id + "\n\n## Ход работы\n\n")
	for _, ln := range stage.Lines(stages, start.Add(dur)) {
		b.WriteString(ln + "\n")
	}
	if err := os.WriteFile(filepath.Join(root, "docs", "tasks", id+".md"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

// stopsRuns кладёт живую запись runs с одним открытым ожиданием: пакет
// ещё не уехал в файл задачи, задача стоит в Blocked прямо сейчас.
func stopsRuns(t *testing.T, root, id, note string, start time.Time) string {
	t.Helper()
	home := t.TempDir()
	if err := stage.Open(home, stage.MainRoot(root), id, stage.WaitHuman, note, start); err != nil {
		t.Fatal(err)
	}
	return stage.Dir(home)
}

// TestStopsCounts проверяет все четыре разряда разом: две закрытые остановки
// «вопрос:», по одной «окружение:» и «спор:», одна закрытая и одна ещё живая
// «автор:», остановка до начала окна и два случая, которые в счёт не идут
// вовсе (запись «снаружи» у Check и машинное ожидание соседа «слияние:»).
func TestStopsCounts(t *testing.T) {
	root := setupStops(t)

	writeStopsTask(t, root, "DK-900", "блок: вопрос: до окна счёта",
		time.Date(2026, 8, 15, 9, 0, 0, 0, time.UTC), time.Hour)
	writeStopsTask(t, root, "DK-901", "блок: вопрос: нужен эталон вида",
		time.Date(2026, 9, 2, 9, 0, 0, 0, time.UTC), time.Hour)
	writeStopsTask(t, root, "DK-902", "блок: вопрос: ждём ответа человека",
		time.Date(2026, 9, 3, 9, 0, 0, 0, time.UTC), 3*time.Hour)
	writeStopsTask(t, root, "DK-903", "блок: окружение: нет доступа к стенду",
		time.Date(2026, 9, 4, 9, 0, 0, 0, time.UTC), 5*time.Hour)
	writeStopsTask(t, root, "DK-904", "блок: автор: правка чужого MR",
		time.Date(2026, 9, 5, 9, 0, 0, 0, time.UTC), 2*time.Hour)
	writeStopsTask(t, root, "DK-905", "блок: спор: автор отказал в правке",
		time.Date(2026, 9, 6, 9, 0, 0, 0, time.UTC), 2*time.Hour)
	writeStopsTask(t, root, "DK-907", "проверка после выката",
		time.Date(2026, 9, 6, 9, 0, 0, 0, time.UTC), time.Hour)
	writeStopsTask(t, root, "DK-908", "блок: слияние: DK-1 ждём соседа",
		time.Date(2026, 9, 6, 9, 0, 0, 0, time.UTC), time.Hour)

	fixedNow := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	was := timeNow
	timeNow = func() time.Time { return fixedNow }
	defer func() { timeNow = was }()

	runs := stopsRuns(t, root, "DK-906", "блок: автор: ждём ответа ревьюера",
		fixedNow.Add(-6*time.Hour))

	out, err := cmdStops(root, "2026-09-01", runs)
	if err != nil {
		t.Fatalf("stops: %v", err)
	}
	for _, want := range []string{
		"остановки конвейера по разрядам парковки, с 2026-09-01",
		"вопрос: 2 (33.3%), медиана простоя 2.0 ч",
		"окружение: 1 (16.7%), медиана простоя 5.0 ч",
		"автор: 2 (33.3%), медиана простоя 4.0 ч",
		"спор: 1 (16.7%), медиана простоя 2.0 ч",
		"итого остановок: 6",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("в выводе нет %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "DK-900") || strings.Contains(out, "DK-907") || strings.Contains(out, "DK-908") {
		t.Fatalf("в счёт попало то, что не должно: до окна либо не разряд парковки:\n%s", out)
	}
}

// TestStopsEmptyWindow: окно без остановок обязано сказать об этом прямо, а не
// напечатать четыре нуля.
func TestStopsEmptyWindow(t *testing.T) {
	root := setupStops(t)
	out, err := cmdStops(root, "", t.TempDir())
	if err != nil {
		t.Fatalf("stops: %v", err)
	}
	if !strings.Contains(out, "остановки конвейера по разрядам парковки, за всю историю") {
		t.Fatalf("заголовок без --since не назвал всю историю:\n%s", out)
	}
	if !strings.Contains(out, "в этом окне остановок нет") {
		t.Fatalf("пустое окно не названо:\n%s", out)
	}
}

// TestStopsBadSince: дата среза разбирается командой, а не молча превращается
// в нулевую.
func TestStopsBadSince(t *testing.T) {
	root := setupStops(t)
	if _, err := cmdStops(root, "сентябрь", t.TempDir()); err == nil {
		t.Fatal("ожидал отказ на неразборчивой дате")
	}
}
