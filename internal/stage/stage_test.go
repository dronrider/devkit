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
	if !NeedsSession(Dev) || !NeedsSession(Review) || !NeedsSession(Proof) || !NeedsSession(Merge) {
		t.Fatal("этап агента объявлен не требующим сессии")
	}
	for _, k := range []string{WaitHuman, WaitEvent, WaitQueue, Outside, Ask} {
		if NeedsSession(k) {
			t.Fatalf("ожидание %q потребовало живой сессии", k)
		}
	}
}

// TestDictionarySplit: словарь DK-911 делится на восемь этапов работы и три
// ожидания, и деление отвечает предикатами, а не сравнением со словом. Слова
// прежнего словаря читаются ожиданиями.
func TestDictionarySplit(t *testing.T) {
	if len(Kinds) != 11 {
		t.Fatalf("в словаре %d слов, жду 11: %v", len(Kinds), Kinds)
	}
	work, wait := 0, 0
	for _, k := range Kinds {
		switch {
		case IsWork(k) && !IsWait(k):
			work++
		case IsWait(k) && !IsWork(k):
			wait++
		default:
			t.Fatalf("вид %q и работа, и ожидание разом либо ни то, ни другое", k)
		}
	}
	if work != 8 || wait != 3 {
		t.Fatalf("этапов работы %d, ожиданий %d, жду 8 и 3", work, wait)
	}
	if !IsExec(Dev) || !IsExec(Rework) || IsExec(Review) || IsExec(Proof) {
		t.Fatal("работа исполнителя это разработка и доработка, и только они")
	}
	for _, k := range []string{Outside, Ask} {
		if !Known(k) || !IsWait(k) || IsWork(k) || !Legacy(k) {
			t.Fatalf("слово прежнего словаря %q читается не ожиданием", k)
		}
	}
}

// TestCanonTurnsOldWordsIntoWaits: «уточнение» это вопрос человеку, «снаружи»
// из Blocked с машинным разрядом слияния или закрытия это событие, остальное
// «снаружи» это человек.
func TestCanonTurnsOldWordsIntoWaits(t *testing.T) {
	cases := []struct{ kind, note, want string }{
		{Ask, "вопрос: какая раскладка", WaitHuman},
		{Outside, "проверка после выката", WaitHuman},
		{Outside, "блок: окружение: нет доступа", WaitHuman},
		{Outside, "блок: слияние: DK-100 ждёт", WaitEvent},
		{Outside, "блок: закрытие: DK-100", WaitEvent},
		{Dev, "субагент opus/high", Dev},
		{WaitQueue, "", WaitQueue},
	}
	for _, c := range cases {
		if got := Canon(c.kind, c.note); got != c.want {
			t.Errorf("Canon(%q, %q) = %q, жду %q", c.kind, c.note, got, c.want)
		}
	}
}

// TestPutRejectsOldWords: новые записи пишутся нынешним словарём, а старое
// слово отбивается с подсказкой, каким ожиданием его писать.
func TestPutRejectsOldWords(t *testing.T) {
	home := t.TempDir()
	err := Open(home, "/p", "T-1", Outside, "проверка после выката", at(10, 0))
	if err == nil || !strings.Contains(err.Error(), WaitHuman) {
		t.Fatalf("старое слово принято либо отказ без подсказки: %v", err)
	}
}

// TestOldRecordReadsAsWait: пакет прежней сборки со «снаружи» и «уточнением»
// читается ожиданиями, а не теряется и не приходит старым словом.
func TestOldRecordReadsAsWait(t *testing.T) {
	home := t.TempDir()
	path := Path(home, "/p", "T-1")
	os.MkdirAll(Dir(home), 0o755)
	os.WriteFile(path, []byte("id = T-1\nroot = /p\nэтап = разработка | 2026-08-15T10:00:00 | субагент opus/high по вердикту pick\nэтап = уточнение | 2026-08-15T11:00:00 | вопрос: какая раскладка | sess\nэтап = снаружи | 2026-08-15T12:00:00 | блок: слияние: T-2\n"), 0o644)
	rec, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(rec.Stages) != 3 || rec.Stages[1].Kind != WaitHuman || rec.Stages[2].Kind != WaitEvent {
		t.Fatalf("старые слова прочитаны не ожиданиями: %+v", rec.Stages)
	}
	if rec.Stages[1].Session != "sess" {
		t.Fatalf("разговор старой записи потерян: %+v", rec.Stages[1])
	}
}

// TestCloseEndsLiveStageOfKind: писатель закрывает свой этап, и у записи нет
// живого этапа до следующего; чужой живой этап закрытие не трогает.
func TestCloseEndsLiveStageOfKind(t *testing.T) {
	home := t.TempDir()
	if err := Put(home, "/p", "T-1", Stage{Kind: Review, Start: at(10, 0), Note: "субагент sonnet/high по определению review-high", Work: "a1b2"}); err != nil {
		t.Fatal(err)
	}
	if ok, err := Close(home, "/p", "T-1", Merge, at(10, 30), ""); err != nil || ok {
		t.Fatalf("закрытие чужого вида тронуло запись: ok=%v, %v", ok, err)
	}
	ok, err := Close(home, "/p", "T-1", Review, at(10, 30), WorkNote(12, 30))
	if err != nil || !ok {
		t.Fatalf("закрытие своего вида: ok=%v, %v", ok, err)
	}
	rec, _ := Load(Path(home, "/p", "T-1"))
	if _, live := rec.Live(); live {
		t.Fatal("закрытый этап остался живым")
	}
	s := rec.Stages[0]
	if !s.End.Equal(at(10, 30)) || s.Work != "a1b2" || !strings.HasSuffix(s.Note, "ходов 12, минут 30") {
		t.Fatalf("конец, работа или хвост записи потерялись: %+v", s)
	}
	ln := Lines(rec.Stages, at(23, 0))[0]
	if !strings.Contains(ln, "10:00-10:30") {
		t.Fatalf("строка «Хода работы» не взяла свой конец: %s", ln)
	}
	if ok, _ := Close(home, "/p", "T-1", Review, at(11, 0), ""); ok {
		t.Fatal("закрытый этап закрылся второй раз")
	}
}

// TestParseLineCanonsOldLabels: строки «Хода работы» прежних пакетов читаются
// ожиданиями, свод цели складывает их в нынешние слова.
func TestParseLineCanonsOldLabels(t *testing.T) {
	s, span, ok := ParseLine("- Снаружи: проверка после выката, 2026-08-15 10:00-12:00.")
	if !ok || s.Kind != WaitHuman || span != 2*time.Hour {
		t.Fatalf("старый ярлык прочитан как %+v (%v, ok=%v)", s, span, ok)
	}
	s, _, ok = ParseLine("- Уточнение: вопрос: раскладка, 2026-08-15 10:00-10:05.")
	if !ok || s.Kind != WaitHuman {
		t.Fatalf("уточнение прочитано как %+v", s)
	}
}

// TestExecutorFromHookNote: запись хука спавна называет исполнителя тем же
// местом, что и прежний вердикт pick, и доработка считается работой
// исполнителя у ворот закрытия.
func TestExecutorFromHookNote(t *testing.T) {
	name, ok := Executor("субагент opus/high по определению exec-high, работа a1b2c3")
	if !ok || name != "opus" {
		t.Fatalf("исполнитель из записи хука %q, ok=%v", name, ok)
	}
	lines := []string{
		"- Разработка: субагент opus/high по определению exec-high, 2026-08-15 10:00-12:00.",
		"- Ревью: субагент sonnet/high по определению review-high, 2026-08-15 12:00-12:30.",
		"- Доработка: субагент haiku/low по определению exec-low, 2026-08-15 12:30-13:00.",
	}
	if name, ok := LastExecutor(lines, nil); !ok || name != "haiku" {
		t.Fatalf("исполнитель доработки не найден: %q, ok=%v", name, ok)
	}
	pending := []Stage{{Kind: Rework, Note: "субагент glm-5.2/high по определению exec-high"}}
	if name, ok := LastExecutor(lines, pending); !ok || name != "glm-5.2" {
		t.Fatalf("исполнитель из живой доработки не найден: %q, ok=%v", name, ok)
	}
}

// TestLastOfFindsByPredicate: elapsed спрашивает работу исполнителя, а не
// слово «разработка», и доработка после ревью находится тем же вопросом.
func TestLastOfFindsByPredicate(t *testing.T) {
	rec := Record{Stages: []Stage{{Kind: Dev, Start: at(9, 0)}, {Kind: Review, Start: at(10, 0)}, {Kind: Rework, Start: at(11, 0)}, {Kind: WaitHuman, Start: at(12, 0)}}}
	s, ok := LastOf(rec, IsExec)
	if !ok || s.Kind != Rework {
		t.Fatalf("последняя работа исполнителя %+v, ok=%v", s, ok)
	}
	if _, ok := LastOf(Record{}, IsExec); ok {
		t.Fatal("в пустой записи что-то нашлось")
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

// TestWorkNoteRoundTrip: ходы и минуты возвращаются теми же числами что ушли
// в запись, как и у прогонявшего сценарий (TestVerifyNoteRoundTrip).
func TestWorkNoteRoundTrip(t *testing.T) {
	turns, minutes, ok := ParseWork(WorkNote(44, 9))
	if !ok || turns != 44 || minutes != 9 {
		t.Fatalf("ходы и минуты не вернулись из записи: %d, %d, %v", turns, minutes, ok)
	}
}

// TestParseWorkFromFlushedLine: хвост читается и из уже выгруженной строки
// «Хода работы», а старая строка без него не выдумывает числа.
func TestParseWorkFromFlushedLine(t *testing.T) {
	line := "- Ревью: ревью провёл glm:glm-5.2, ходов 44, минут 9, 2026-08-31 12:00-12:15."
	turns, minutes, ok := ParseWork(line)
	if !ok || turns != 44 || minutes != 9 {
		t.Fatalf("из строки «Хода работы» ходы и минуты не достались: %d, %d, %v", turns, minutes, ok)
	}
	if _, _, ok := ParseWork("- Ревью: субагент sonnet/high по вердикту pick, 2026-08-31 12:00-12:15."); ok {
		t.Fatal("старая строка без хвоста выдумала ходы и минуты")
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

// Исполнителя разработки спрашивают двое: ворота закрытия сверяют с ним
// прогонявшего сценарий, а подъём прогона после выката решает, кому прогон не
// отдавать. Ищется он в двух местах, и незакрытый пакет этапов свежее файла.
func TestLastExecutorPrefersPendingRecord(t *testing.T) {
	lines := []string{
		"- Разработка: субагент haiku/low по вердикту pick, 2026-09-01 10:00-10:20.",
		"- Ревью: субагент opus/high по вердикту pick, 2026-09-01 10:30-10:40.",
	}
	if got, ok := LastExecutor(lines, nil); !ok || got != "haiku" {
		t.Fatalf("из «Хода работы» исполнитель разработки не достался: %q, %v", got, ok)
	}
	pending := []Stage{{Kind: Review, Note: "субагент opus/high по вердикту pick"},
		{Kind: Dev, Note: "субагент sonnet/high по вердикту pick"}}
	if got, ok := LastExecutor(lines, pending); !ok || got != "sonnet" {
		t.Fatalf("незакрытый пакет свежее файла: %q, %v", got, ok)
	}
	if _, ok := LastExecutor([]string{"- Ревью: субагент opus/high по вердикту pick"}, nil); ok {
		t.Fatal("ревью с вердиктом pick сошло за разработку")
	}
	if _, ok := LastExecutor(nil, nil); ok {
		t.Fatal("без записей исполнитель взяться неоткуда")
	}
}

// Прогонявший сценарий ищется теми же двумя источниками и тем же порядком.
func TestLastVerifyRunnerPrefersPendingRecord(t *testing.T) {
	lines := []string{"- Проверка: сценарий прогнал haiku, 2026-09-01 11:00-11:05."}
	if got, ok := LastVerifyRunner(lines, nil); !ok || got != "haiku" {
		t.Fatalf("из «Хода работы» прогонявший не достался: %q, %v", got, ok)
	}
	pending := []Stage{{Kind: Verify, Note: VerifyNote("sonnet")}}
	if got, ok := LastVerifyRunner(lines, pending); !ok || got != "sonnet" {
		t.Fatalf("незакрытый пакет свежее файла: %q, %v", got, ok)
	}
	if _, ok := LastVerifyRunner(nil, nil); ok {
		t.Fatal("без записей прогонявший взяться неоткуда")
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

func TestParseLineReadsBackWhatLinesWrote(t *testing.T) {
	stages := []Stage{
		{Kind: Dev, Start: at(10, 0), Note: "opus/high по вердикту pick"},
		{Kind: Review, Start: at(12, 30), Note: "sonnet/medium по вердикту pick"},
	}
	lines := Lines(stages, at(13, 15))
	want := []time.Duration{150 * time.Minute, 45 * time.Minute}
	for i, ln := range lines {
		got, span, ok := ParseLine(ln)
		if !ok {
			t.Fatalf("строка %q не прочиталась обратно", ln)
		}
		if got.Kind != stages[i].Kind || !got.Start.Equal(stages[i].Start) || got.Note != stages[i].Note {
			t.Fatalf("этап %d прочитан как %+v", i, got)
		}
		if span != want[i] {
			t.Fatalf("длительность этапа %d: %v, жду %v", i, span, want[i])
		}
	}
}

// Пятое слово словаря читается наравне с четырьмя нынешними: сводка цели
// перечня видов у себя не держит, и новый вид ей достаётся сам.
func TestParseLineFollowsKindsDictionary(t *testing.T) {
	for _, k := range Kinds {
		ln := Lines([]Stage{{Kind: k, Start: at(9, 0)}}, at(9, 40))[0]
		got, span, ok := ParseLine(ln)
		if !ok || got.Kind != k || span != 40*time.Minute {
			t.Fatalf("вид %q прочитан как %+v (%v, ok=%v)", k, got, span, ok)
		}
	}
	if _, _, ok := ParseLine("- Деплой: чужой ярлык, 2026-08-15 10:00-11:00."); ok {
		t.Fatal("строка с чужим видом принята за этап")
	}
}

func TestParseLineSkipsProse(t *testing.T) {
	for _, ln := range []string{
		"",
		"Разработка шла долго.",
		"- Разработка: без часов вовсе.",
		"- Разработка: 2026-13-40 10:00-11:00.",
		"- Ревью: 2026-08-15 25:00.",
	} {
		if _, _, ok := ParseLine(ln); ok {
			t.Fatalf("проза %q принята за этап", ln)
		}
	}
}

func TestParseLineWithoutSpanAndOverMidnight(t *testing.T) {
	s, span, ok := ParseLine("- Снаружи: 2026-08-15 14:00.")
	if !ok || span != 0 || !s.Start.Equal(at(14, 0)) || s.Note != "" {
		t.Fatalf("этап без конца прочитан как %+v (%v, ok=%v)", s, span, ok)
	}
	// Дату Lines пишет одну, начала, и перешагнувший полночь этап без переноса
	// дал бы отрицательную длительность.
	if _, span, ok = ParseLine("- Разработка: ночная пачка, 2026-08-15 23:30-01:00."); !ok || span != 90*time.Minute {
		t.Fatalf("этап через полночь: %v, ok=%v", span, ok)
	}
}

// Разговор, открывший этап, стоит в записи четвёртым полем (DK-716). Без него
// от записи нельзя было дойти до чата, который задачу ведёт: экран знал, что
// идёт разработка, а спросить исполнителя было негде.
func TestOpenKeepsSession(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CLAUDE_CODE_SESSION_ID", "dff98764-1111-4111-8111-111111111111")
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.Local)
	if err := Open(home, "/p", "XR-1", Dev, "субагент opus/high по вердикту pick", now); err != nil {
		t.Fatal(err)
	}
	rec, err := Load(Path(home, "/p", "XR-1"))
	if err != nil {
		t.Fatal(err)
	}
	live, ok := rec.Live()
	if !ok {
		t.Fatal("этапа в записи нет")
	}
	if live.Session != "dff98764-1111-4111-8111-111111111111" {
		t.Errorf("разговор этапа %q", live.Session)
	}
	// Текст записи от нового поля не пострадал: по нему ворота закрытия ищут
	// исполнителя и прогнавшего сценарий.
	if _, ok := Executor(live.Note); !ok {
		t.Errorf("текст записи потерялся: %q", live.Note)
	}
}

// Записи прежней сборки читаются по-прежнему: на диске лежат незакрытые пакеты
// без четвёртого поля, и терять их из-за нового поля нельзя.
func TestParseStageWithoutSession(t *testing.T) {
	s, ok := parseStage("разработка | 2026-09-03T12:00:00 | субагент opus/high по вердикту pick")
	if !ok {
		t.Fatal("строка прежнего формата не разобралась")
	}
	if s.Session != "" {
		t.Errorf("разговор взялся из ниоткуда: %q", s.Session)
	}
	if s.Note != "субагент opus/high по вердикту pick" {
		t.Errorf("текст записи: %q", s.Note)
	}
}

// TestClosedStageOnTopKeepsLive (DK-911, замечание ревью): синхронная вычитка
// внутри идущей разработки ложится закрытой поверх живого этапа и не гасит
// его, а конец разработки находит свой этап под ней. Строки «Хода работы»
// держат вычитку отрезком внутри отрезка разработки.
func TestClosedStageOnTopKeepsLive(t *testing.T) {
	home := t.TempDir()
	dev := Stage{Kind: Dev, Start: at(10, 0), Note: "субагент opus/high по определению exec-high, работа d1", Work: "d1"}
	proof := Stage{Kind: Proof, Start: at(10, 20), End: at(10, 25), Note: "субагент sonnet по определению proofread, работа p1", Work: "p1"}
	for _, s := range []Stage{dev, proof} {
		if err := Put(home, "/p", "T-1", s); err != nil {
			t.Fatal(err)
		}
	}
	rec, _ := Load(Path(home, "/p", "T-1"))
	live, ok := rec.Live()
	if !ok || live.Kind != Dev {
		t.Fatalf("закрытая вычитка погасила живую разработку: %+v, %v", live, ok)
	}
	if ok, err := Close(home, "/p", "T-1", Dev, at(11, 0), WorkNote(30, 60)); err != nil || !ok {
		t.Fatalf("конец разработки не нашёл своего этапа под вычиткой: ok=%v, %v", ok, err)
	}
	rec, _ = Load(Path(home, "/p", "T-1"))
	if _, ok := rec.Live(); ok {
		t.Fatal("после конца разработки живой этап остался")
	}
	lines := Lines(rec.Stages, at(23, 0))
	if !strings.Contains(lines[0], "10:00-11:00") || !strings.Contains(lines[1], "10:20-10:25") {
		t.Fatalf("отрезки разошлись:\n%s", strings.Join(lines, "\n"))
	}
	// Фоновое ревью поверх живой разработки: закрылось само, разработка живёт.
	if err := Put(home, "/p", "T-2", dev); err != nil {
		t.Fatal(err)
	}
	if err := Put(home, "/p", "T-2", Stage{Kind: Review, Start: at(10, 30), Note: "субагент sonnet/high по определению review-high", Work: "r1"}); err != nil {
		t.Fatal(err)
	}
	if ok, _ := Close(home, "/p", "T-2", Review, at(10, 50), ""); !ok {
		t.Fatal("ревью не закрылось")
	}
	rec, _ = Load(Path(home, "/p", "T-2"))
	if live, ok := rec.Live(); !ok || live.Kind != Dev {
		t.Fatalf("после конца ревью живой должна остаться разработка: %+v, %v", live, ok)
	}
	if ln := Lines(rec.Stages, at(12, 0))[0]; !strings.Contains(ln, "10:00-12:00") {
		t.Fatalf("незакрытую разработку кончило закрытое ревью: %s", ln)
	}
}

// TestListMatchesRootThroughSymlink: писатель взял корень у git, развернувший
// ссылку, а читатель спрашивает по написанию со ссылкой (DK-910, macOS с
// домом под /var -> /private/var). Записи одни и те же.
func TestListMatchesRootThroughSymlink(t *testing.T) {
	home := t.TempDir()
	base := t.TempDir()
	real := filepath.Join(base, "real")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	if err := Open(home, real, "XR-001", Dev, "", at(10, 0)); err != nil {
		t.Fatal(err)
	}
	if got := List(home, link); len(got) != 1 || got[0].ID != "XR-001" {
		t.Fatalf("по ссылке на корень записи не нашлись: %+v", got)
	}
	if got := List(home, filepath.Join(base, "other")); len(got) != 0 {
		t.Fatalf("чужой корень получил записи: %+v", got)
	}
}
