package stage

import (
	"os"
	"os/exec"
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

func TestVerifyKindKnown(t *testing.T) {
	if !Known(Verify) {
		t.Fatal("вид «проверка» не признан своим")
	}
	if !NeedsSession(Verify) {
		t.Fatal("прогон сценария объявлен не требующим сессии")
	}
}

func TestVerifyNoteRoundTrip(t *testing.T) {
	for _, name := range []string{"sonnet", "glm:glm-5.2"} {
		got, ok := VerifyRunner(VerifyNote(name))
		if !ok || got != name {
			t.Fatalf("прогонявший %q не вернулся из записи: %q, %v", name, got, ok)
		}
	}
}

func TestVerifyRunnerFromFlushedLine(t *testing.T) {
	line := "- Проверка: сценарий прогнал glm:glm-5.2, 2026-08-31 12:00-12:05."
	got, ok := VerifyRunner(line)
	if !ok || got != "glm:glm-5.2" {
		t.Fatalf("из строки «Хода работы» прогонявший не достался: %q, %v", got, ok)
	}
	if _, ok := VerifyRunner("- Проверка: вывод вложен в раздел."); ok {
		t.Fatal("строка без записи прогона отдала прогонявшего")
	}
}

func TestExecutorFromPickNote(t *testing.T) {
	cases := map[string]string{
		"субагент opus/high по вердикту pick (квота: week_all 93%)":           "opus",
		"- Разработка: opus/medium по вердикту pick, 2026-08-18 14:01-15:20.": "opus",
		"грумминговый вердикт, делегат glm:glm-5.2/high по вердикту pick":     "glm:glm-5.2",
	}
	for text, want := range cases {
		got, ok := Executor(text)
		if !ok || got != want {
			t.Fatalf("из %q исполнитель не достался: %q, %v", text, got, ok)
		}
	}
	if _, ok := Executor("- Разработка: руками, 2026-08-18 14:01."); ok {
		t.Fatal("строка без вердикта pick отдала исполнителя")
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

// gitT гоняет git в директории и валит тест на отказе: разбирать провал сетапа
// по красноте самой проверки пришлось бы дольше, чем прочитать вывод git.
func gitT(t *testing.T, dir string, args ...string) {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// TestMainRootFoldsWorktreeToCheckout: запись задачи сходится к одному файлу что
// из основного чекаута, что из дерева задачи. Этап разработки открывают из
// дерева (pick зовут с -C <worktree>), а пакет уносит смена статуса из основного
// чекаута, и без приведения через git-common-dir это были бы две разные записи:
// имя файла и поле root считаются от пути, а у линкованного дерева путь свой.
// Тесту поэтому нужен настоящий git с линкованным деревом: на временной
// директории без git отрабатывает только запасной путь, где приведения нет вовсе.
func TestMainRootFoldsWorktreeToCheckout(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git не найден, приведение корня проверять нечем")
	}
	main := t.TempDir()
	gitT(t, main, "init", "-q", "-b", "main")
	gitT(t, main, "config", "user.email", "test@test")
	gitT(t, main, "config", "user.name", "test")
	if err := os.WriteFile(filepath.Join(main, "README.md"), []byte("# стенд\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitT(t, main, "add", ".")
	gitT(t, main, "commit", "-qm", "seed")
	tree := filepath.Join(t.TempDir(), "task")
	gitT(t, main, "worktree", "add", "-q", "-b", "t-001", tree)

	if got := MainRoot(tree); got == tree {
		t.Fatalf("линкованное дерево не приведено к чекауту: %s", got)
	}
	home := t.TempDir()
	// Открываем этап из дерева задачи, забираем пакет из основного чекаута.
	if err := Open(home, MainRoot(tree), "T-001", Dev, "субагент opus/high", at(10, 0)); err != nil {
		t.Fatal(err)
	}
	stages, err := Flush(home, MainRoot(main), "T-001")
	if err != nil {
		t.Fatal(err)
	}
	if len(stages) != 1 || stages[0].Kind != Dev {
		t.Fatalf("пакет из основного чекаута не нашёл этап, открытый из дерева задачи: %+v", stages)
	}
}

// TestMainRootKeepsNonGitRoot: вне git-дерева возвращается то, что дали. Проекты
// без git и временные корни тестов обязаны работать по-прежнему, а не оставаться
// без записи вовсе.
func TestMainRootKeepsNonGitRoot(t *testing.T) {
	dir := t.TempDir()
	if got := MainRoot(dir); got != dir {
		t.Fatalf("корень вне git подменён: %s, жду %s", got, dir)
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

// TestElapsedFindsLastMatchingKind: запись живёт «разработка» -> «ревью», и
// Elapsed по Dev обязан найти первый этап, а не молчать из-за того, что живой
// этап уже другого вида.
func TestElapsedFindsLastMatchingKind(t *testing.T) {
	rec := Record{Stages: []Stage{
		{Kind: Dev, Start: at(10, 0)},
		{Kind: Review, Start: at(12, 30)},
	}}
	d, ok := Elapsed(rec, Dev, at(11, 46))
	if !ok || d != 106*time.Minute {
		t.Fatalf("Elapsed(Dev) = %v,%v; ждал 106m,true", d, ok)
	}
}

// TestElapsedPastCeiling проверяет заход, ушедший за плановый лимит:
// вызывающий сравнивает длительность с лимитом сам, Elapsed только считает.
func TestElapsedPastCeiling(t *testing.T) {
	rec := Record{Stages: []Stage{{Kind: Dev, Start: at(9, 10)}}}
	d, ok := Elapsed(rec, Dev, at(11, 35))
	if !ok || d != 145*time.Minute {
		t.Fatalf("Elapsed(Dev) = %v,%v; ждал 145m,true", d, ok)
	}
}

// TestElapsedNoMatchingStage: этапа искомого вида в записи нет вовсе (пустая
// запись или только другие виды), и тогда второе значение false, а не
// нулевая длительность, которую легко принять за «только что открыт».
func TestElapsedNoMatchingStage(t *testing.T) {
	if _, ok := Elapsed(Record{}, Dev, at(11, 0)); ok {
		t.Fatal("пустая запись не должна находить этап")
	}
	rec := Record{Stages: []Stage{{Kind: Review, Start: at(10, 0)}}}
	if _, ok := Elapsed(rec, Dev, at(11, 0)); ok {
		t.Fatal("запись без этапа Dev не должна находить его")
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

// TestInsertSkipsHeadingInsideFence: заголовок раздела, процитированный в блоке
// кода, за настоящий не считается. Случай не гипотетический: в файле задачи
// DK-338 раздел «Проверка» цитирует вывод прогона со строкой «## Ход работы», и
// поиск по strings.HasPrefix ловил цитату раньше настоящего заголовка, отчего
// пакет этапов ложился в «Проверку» посреди чужого транскрипта.
func TestInsertSkipsHeadingInsideFence(t *testing.T) {
	doc := "# T-011\n\n## Проверка\n\n```text\n## Ход работы\n\n- Разработка: цитата вывода.\n```\n\n" +
		"## Ранг\n\n`25+6+3+0+4 = 38`, P2.\n\n## Ход работы\n\n- Разработка: настоящая запись.\n"
	got := InsertIntoSection(doc, "## Ход работы", "- Ревью: пакет ревьювера.")
	want := "# T-011\n\n## Проверка\n\n```text\n## Ход работы\n\n- Разработка: цитата вывода.\n```\n\n" +
		"## Ранг\n\n`25+6+3+0+4 = 38`, P2.\n\n## Ход работы\n\n- Разработка: настоящая запись.\n- Ревью: пакет ревьювера.\n"
	if got != want {
		t.Fatalf("запись ушла не в тот раздел:\n%s", got)
	}
}

// TestInsertSkipsSectionEndInsideFence: граница следующего раздела внутри блока
// кода тоже не граница. Иначе запись обрывалась бы на цитате «## ...» и вставала
// посреди своего же раздела.
func TestInsertSkipsSectionEndInsideFence(t *testing.T) {
	doc := "# T-012\n\n## Ход работы\n\n- Разработка: было.\n\n```text\n## Ранг\n```\n\n## Приёмка\n\n- вид: agent\n"
	got := InsertIntoSection(doc, "## Ход работы", "- Ревью: стало.")
	want := "# T-012\n\n## Ход работы\n\n- Разработка: было.\n\n```text\n## Ранг\n```\n- Ревью: стало.\n\n## Приёмка\n\n- вид: agent\n"
	if got != want {
		t.Fatalf("граница раздела найдена в блоке кода:\n%s", got)
	}
}

func TestFenceMask(t *testing.T) {
	rows := strings.Split("вне\n```text\nвнутри\n```\nснова вне\n", "\n")
	mask, at := FenceMask(rows)
	if at != 0 {
		t.Fatalf("закрытый блок объявлен незакрытым на строке %d", at)
	}
	for i, want := range []bool{false, true, true, true, false, false} {
		if mask[i] != want {
			t.Fatalf("строка %d (%q): маска %v, жду %v", i, rows[i], mask[i], want)
		}
	}
	// Ограждение другого знака чужой блок не закрывает, а незакрытый уводит в
	// блок весь остаток файла.
	_, open := FenceMask(strings.Split("~~~\n```\nхвост\n", "\n"))
	if open != 1 {
		t.Fatalf("незакрытое ограждение названо строкой %d, жду 1", open)
	}
}

func TestInsertNothingKeepsFile(t *testing.T) {
	doc := "# T-010\n\n## Ход работы\n\n- Разработка: было.\n"
	if got := InsertIntoSection(doc, "## Ход работы"); got != doc {
		t.Fatalf("пустой пакет тронул файл: %q", got)
	}
}
