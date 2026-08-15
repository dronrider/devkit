package stage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// at собирает момент времени по часам и минутам одного дня: тесты про этапы
// целиком про порядок и длительность, и полная дата в каждом вызове только
// мешала бы читать.
func at(hh, mm int) time.Time {
	return time.Date(2026, 8, 15, hh, mm, 0, 0, time.Local)
}

func TestKnownAndSession(t *testing.T) {
	for _, k := range Kinds {
		if !Known(k) {
			t.Fatalf("вид %q не признан своим", k)
		}
	}
	if Known("деплой") {
		t.Fatal("чужое слово принято за вид деятельности")
	}
	if !NeedsSession(Dev) || !NeedsSession(Review) || !NeedsSession(Ask) {
		t.Fatal("этап агента объявлен не требующим сессии")
	}
	if NeedsSession(Outside) {
		t.Fatal("ожидание снаружи потребовало живой сессии")
	}
}

func TestSlugSplitsSameNamedProjects(t *testing.T) {
	a := Slug("/Users/rider/projects/devkit")
	b := Slug("/Users/rider/work/devkit")
	if a == b {
		t.Fatalf("два проекта с одним именем директории дали один slug: %s", a)
	}
	if strings.ContainsAny(a, "/. ") {
		t.Fatalf("slug %q не годится в имя файла", a)
	}
}

func TestOpenAppendsAndLoads(t *testing.T) {
	home, root := t.TempDir(), "/proj/one"
	if err := Open(home, root, "T-001", Dev, "opus/high по вердикту pick", at(10, 0)); err != nil {
		t.Fatalf("первый этап: %v", err)
	}
	if err := Open(home, root, "T-001", Review, "sonnet/medium по вердикту pick", at(12, 30)); err != nil {
		t.Fatalf("второй этап: %v", err)
	}
	rec, err := Load(Path(home, root, "T-001"))
	if err != nil {
		t.Fatalf("чтение записи: %v", err)
	}
	if rec.ID != "T-001" || rec.Root != root {
		t.Fatalf("шапка записи потерялась: %+v", rec)
	}
	if len(rec.Stages) != 2 {
		t.Fatalf("жду два этапа, получил %d", len(rec.Stages))
	}
	live, ok := rec.Live()
	if !ok || live.Kind != Review || !live.Start.Equal(at(12, 30)) {
		t.Fatalf("живой этап не последний: %+v", live)
	}
	if rec.Stages[0].Note != "opus/high по вердикту pick" {
		t.Fatalf("текст записи потерялся: %q", rec.Stages[0].Note)
	}
}

func TestOpenRejectsUnknownKind(t *testing.T) {
	home := t.TempDir()
	if err := Open(home, "/proj", "T-001", "деплой", "", at(10, 0)); err == nil {
		t.Fatal("неизвестный вид деятельности записался")
	}
	if _, err := os.Stat(Path(home, "/proj", "T-001")); err == nil {
		t.Fatal("отбитый вид всё равно завёл файл записи")
	}
}

func TestLoadMissingIsEmpty(t *testing.T) {
	rec, err := Load(filepath.Join(t.TempDir(), "нет.run"))
	if err != nil {
		t.Fatalf("отсутствие записи стало ошибкой: %v", err)
	}
	if len(rec.Stages) != 0 {
		t.Fatalf("у пустой записи взялись этапы: %+v", rec.Stages)
	}
}

func TestLoadSkipsBrokenLines(t *testing.T) {
	home, root := t.TempDir(), "/proj"
	if err := os.MkdirAll(Dir(home), 0o755); err != nil {
		t.Fatal(err)
	}
	text := "# шапка\nid = T-002\nroot = /proj\n" +
		"этап = деплой | 2026-08-15T10:00:00 | чужое слово\n" +
		"этап = разработка | вчера | битое время\n" +
		"этап = разработка | 2026-08-15T11:00:00 | живой\n"
	if err := os.WriteFile(Path(home, root, "T-002"), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	rec, err := Load(Path(home, root, "T-002"))
	if err != nil {
		t.Fatalf("чтение: %v", err)
	}
	if len(rec.Stages) != 1 || rec.Stages[0].Note != "живой" {
		t.Fatalf("битые строки не пропущены: %+v", rec.Stages)
	}
}

func TestNoteKeepsSeparatorOut(t *testing.T) {
	home, root := t.TempDir(), "/proj"
	if err := Open(home, root, "T-003", Dev, "маппинг opus | корректор: сдвиг", at(9, 0)); err != nil {
		t.Fatal(err)
	}
	rec, _ := Load(Path(home, root, "T-003"))
	if len(rec.Stages) != 1 {
		t.Fatalf("черта в тексте развалила запись: %+v", rec.Stages)
	}
	if strings.Contains(rec.Stages[0].Note, "|") {
		t.Fatalf("разделитель остался в тексте: %q", rec.Stages[0].Note)
	}
	if !strings.Contains(rec.Stages[0].Note, "корректор") {
		t.Fatalf("текст обрезан по черте: %q", rec.Stages[0].Note)
	}
}

func TestFlushTakesAndRemoves(t *testing.T) {
	home, root := t.TempDir(), "/proj"
	Open(home, root, "T-004", Dev, "разработка", at(10, 0))
	Open(home, root, "T-004", Review, "ревью", at(12, 0))
	stages, err := Flush(home, root, "T-004")
	if err != nil {
		t.Fatalf("пакет: %v", err)
	}
	if len(stages) != 2 {
		t.Fatalf("жду два этапа в пакете, получил %d", len(stages))
	}
	if _, err := os.Stat(Path(home, root, "T-004")); !os.IsNotExist(err) {
		t.Fatal("запись осталась после пакета, закрытый этап выдаётся за живой")
	}
	again, err := Flush(home, root, "T-004")
	if err != nil || len(again) != 0 {
		t.Fatalf("повторный пакет не пуст: %v, %+v", err, again)
	}
}

func TestListFiltersByRoot(t *testing.T) {
	home := t.TempDir()
	Open(home, "/proj/one", "T-005", Dev, "своя", at(10, 0))
	Open(home, "/proj/two", "T-006", Dev, "чужая", at(10, 0))
	recs := List(home, "/proj/one")
	if len(recs) != 1 || recs[0].ID != "T-005" {
		t.Fatalf("список не отфильтрован по корню: %+v", recs)
	}
	if len(List(home, "/proj/three")) != 0 {
		t.Fatal("нашлись записи проекта без единого этапа")
	}
}

func TestLinesCarryKindNoteAndSpan(t *testing.T) {
	stages := []Stage{
		{Kind: Dev, Start: at(10, 0), Note: "opus/high по вердикту pick"},
		{Kind: Review, Start: at(12, 30), Note: "sonnet/medium по вердикту pick"},
	}
	lines := Lines(stages, at(13, 15))
	if len(lines) != 2 {
		t.Fatalf("жду строку на этап, получил %d", len(lines))
	}
	if lines[0] != "- Разработка: opus/high по вердикту pick, 2026-08-15 10:00-12:30." {
		t.Fatalf("первая строка: %q", lines[0])
	}
	if lines[1] != "- Ревью: sonnet/medium по вердикту pick, 2026-08-15 12:30-13:15." {
		t.Fatalf("вторая строка: %q", lines[1])
	}
}

func TestLinesWithoutNoteAndWithoutSpan(t *testing.T) {
	lines := Lines([]Stage{{Kind: Outside, Start: at(14, 0)}}, at(14, 0))
	if len(lines) != 1 || lines[0] != "- Снаружи: 2026-08-15 14:00." {
		t.Fatalf("строка без текста и без длительности: %+v", lines)
	}
}

func TestInsertIntoSection(t *testing.T) {
	doc := "# T-007\n\n## Ход работы\n\n- Разработка: было.\n\n## Приёмка\n\n- вид: agent\n"
	got := InsertIntoSection(doc, "## Ход работы", "- Ревью: стало.", "- Снаружи: стало.")
	want := "# T-007\n\n## Ход работы\n\n- Разработка: было.\n- Ревью: стало.\n- Снаружи: стало.\n\n## Приёмка\n\n- вид: agent\n"
	if got != want {
		t.Fatalf("вставка в раздел:\n%q", got)
	}
}

func TestInsertIntoEmptyAndMissingSection(t *testing.T) {
	empty := InsertIntoSection("# T-008\n\n## Ход работы\n", "## Ход работы", "- Разработка: раз.")
	if empty != "# T-008\n\n## Ход работы\n\n- Разработка: раз.\n" {
		t.Fatalf("пустой раздел: %q", empty)
	}
	missing := InsertIntoSection("# T-009\n", "## Ход работы", "- Разработка: раз.")
	if !strings.HasSuffix(missing, "## Ход работы\n\n- Разработка: раз.\n") {
		t.Fatalf("раздела не было, а он не добавлен: %q", missing)
	}
}

func TestInsertNothingKeepsFile(t *testing.T) {
	doc := "# T-010\n\n## Ход работы\n\n- Разработка: было.\n"
	if got := InsertIntoSection(doc, "## Ход работы"); got != doc {
		t.Fatalf("пустой пакет тронул файл: %q", got)
	}
}
