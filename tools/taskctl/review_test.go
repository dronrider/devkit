package main

import (
	"os"
	"os/exec"
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

// TestReviewAddStdin: без позиционного текста review add читает замечание со
// stdin (DK-452). Так текст с обратными кавычками доезжает дословно, а не
// исполняется подстановкой bash по дороге.
func TestReviewAddStdin(t *testing.T) {
	root := setup(t)
	note := "позвать `devkitctl update` до отказа"
	cmd := exec.Command("go", "run", ".", "-C", root, "review", "add", "XR-005")
	cmd.Stdin = strings.NewReader(note + "\n")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("review add со stdin: %v\n%s", err, out)
	}
	if got := readTaskFile(t, root, "XR-005"); !strings.Contains(got, "- "+note+"\n") {
		t.Fatalf("замечание со stdin не доехало до файла задачи:\n%q", got)
	}
}

// TestReviewAddStdinEmpty: пустой stdin без аргумента это отказ с подсказкой
// про heredoc, а не пустое замечание в файле.
func TestReviewAddStdinEmpty(t *testing.T) {
	root := setup(t)
	cmd := exec.Command("go", "run", ".", "-C", root, "review", "add", "XR-005")
	cmd.Stdin = strings.NewReader("")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("пустой stdin должен отбиваться, вывод: %s", out)
	}
	if !strings.Contains(string(out), "жду текст замечания") {
		t.Fatalf("отказ без подсказки: %s", out)
	}
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
	// Раздел встаёт на своё место по форме (TASKFORM.md), выше сценария
	// проверки, а не в хвост файла.
	want := "# XR-005\n\n## Ревью\n\n- гонка в close\n- нейминг\n" + fixtureScenario + fixtureVerification
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
	// У XR-001 снимаем файл: review add заводит и файл, и ссылку в строке.
	dropTaskFile(t, root, "XR-001")
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

// Чистый вердикт с маркером списка замечанием не считается, а замечание,
// перенесённое на несколько строк, судится целиком: исход на строке переноса
// закрывает его. Критерий должен согласовывать review show с shipctl merge,
// иначе одна и та же строка расходится в оценке (DK-277).
func TestReviewOutcomeMarkup(t *testing.T) {
	root := setup(t)
	content := "# XR-005\n\n## Ревью\n\n" +
		"- Вердикт: без замечаний. Путь от симптома пройден по ops.go.\n" +
		"- длинное замечание,\n  перенесённое на две строки: исправлено\n" +
		"- гибрид: исправлено, теперь без замечаний\n" +
		"- открытое, без исхода\n"
	if err := os.WriteFile(taskFileAbs(root, "XR-005"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	rf, err := loadReview(taskFileAbs(root, "XR-005"))
	if err != nil {
		t.Fatal(err)
	}
	if len(rf.notes) != 4 {
		t.Fatalf("ожидал 4 элемента списка, разобрано %d", len(rf.notes))
	}
	// «исправлено» бьёт «без замечаний»: порядок проверок исход -> чистый итог,
	// поэтому гибрид остаётся исправленным, а не уезжает в «чисто».
	want := []string{"чисто", "исправлено", "исправлено", ""}
	for i, w := range want {
		if got := rf.notes[i].outcome(); got != w {
			t.Errorf("замечание %d: outcome %q, жду %q (текст: %s)", i+1, got, w, rf.notes[i].Text)
		}
	}
	show, err := cmdReviewShow(root, "XR-005")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(show, "[чисто] Вердикт") {
		t.Errorf("чистый вердикт не помечен [чисто]:\n%s", show)
	}
	if !strings.Contains(show, "[исправлено] длинное") {
		t.Errorf("перенесённое замечание не помечено [исправлено]:\n%s", show)
	}
	if c := strings.Count(show, "[открыто]"); c != 1 {
		t.Errorf("открытым должно быть только замечание без исхода, отмечено %d:\n%s", c, show)
	}
}

// Замечание, цитирующее слово исхода не в хвосте resolve-формата, остаётся
// открытым, а resolve на него не отбивается словами «уже закрыто» (DK-503,
// DK-514): outcome ищет исход по позиции, где его пишет cmdReviewResolve, а
// не по факту появления слова где-то в тексте.
func TestReviewOutcomeQuotedWord(t *testing.T) {
	root := setup(t)
	content := "# XR-005\n\n## Ревью\n\n" +
		"- замечание 18 цитировало текст сценария со словами «остаются закрытыми исходом «исправлено»»\n"
	if err := os.WriteFile(taskFileAbs(root, "XR-005"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	rf, err := loadReview(taskFileAbs(root, "XR-005"))
	if err != nil {
		t.Fatal(err)
	}
	if len(rf.notes) != 1 {
		t.Fatalf("ожидал 1 элемент, разобрано %d", len(rf.notes))
	}
	if got := rf.notes[0].outcome(); got != "" {
		t.Fatalf("замечание с цитатой должно быть открыто, outcome %q (текст: %s)", got, rf.notes[0].Text)
	}
	if _, err := cmdReviewResolve(root, "XR-005", 1, "fixed", "", CommitOpts{}); err != nil {
		t.Fatalf("resolve на замечание с цитатой должен пройти: %v", err)
	}
	got := readTaskFile(t, root, "XR-005")
	if !strings.Contains(got, ": исправлено\n") {
		t.Errorf("в файле нет исхода после resolve:\n%s", got)
	}
}

// Оборот «без замечаний» внутри сути замечания чистым вердиктом не считается:
// outcome ищет его в голове элемента, куда ревьювер пишет вердикт, а суть
// живого замечания 11 из DK-460 кончается словами «про вердикт без замечаний
// нет ничего» и раньше читалась чистой (DK-469). Вердикты, записанные рукой по
// той же форме и утилитой без пояснения, чистыми и остаются.
func TestReviewOutcomeVerdictPhrase(t *testing.T) {
	root := setup(t)
	content := "# XR-005\n\n## Ревью\n\n" +
		"- Строка 1 плана меняет поведение merge раньше, чем процедура учится писать запись, а порядок относительно строки 5 объявлен свободным («5 в любой момент», при том что сама строка 5 обещает увести третий разряд в редкий случай). В составе строки 1 названы RULES.board.md п. 2 и tools/shipctl/README.md, но не скилл board-ship, а именно его читает диспетчер в момент ревью: в разделе «Ревью» пп. 5-6 (SKILL.md:91-95) сказано только «строка на замечание, суть и исход», про вердикт без замечаний нет ничего. В промежутке между строками 1 и 5 отказ merge будет приходить на честной работе, а где брать запись, не написано нигде.\n" +
		"- Вердикт: без замечаний. Путь от симптома пройден по ops.go.\n" +
		"- замечаний нет\n" +
		"- Замечаний неточностей в схеме не осталось, но про вердикт в скилле не написано\n"
	if err := os.WriteFile(taskFileAbs(root, "XR-005"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	rf, err := loadReview(taskFileAbs(root, "XR-005"))
	if err != nil {
		t.Fatal(err)
	}
	if len(rf.notes) != 4 {
		t.Fatalf("ожидал 4 элемента списка, разобрано %d", len(rf.notes))
	}
	want := []string{"", "чисто", "чисто", ""}
	for i, w := range want {
		if got := rf.notes[i].outcome(); got != w {
			t.Errorf("замечание %d: outcome %q, жду %q (текст: %s)", i+1, got, w, rf.notes[i].Text)
		}
	}
	show, err := cmdReviewShow(root, "XR-005")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(show, "[открыто] Строка 1 плана") {
		t.Errorf("замечание с оборотом в сути должно быть открыто:\n%s", show)
	}
	if strings.Contains(show, "[чисто] Строка 1") {
		t.Errorf("замечание с оборотом в сути помечено чистым:\n%s", show)
	}
	if _, err := cmdReviewResolve(root, "XR-005", 1, "fixed", "", CommitOpts{}); err != nil {
		t.Fatalf("resolve на замечание с оборотом в сути должен пройти: %v", err)
	}
}

// Пустая строка между маркером замечания и абзацем со словом исхода закрывает
// элемент: loadReview не прирастает к замечанию через пустую строку, иначе
// исход чужого абзаца закрывал бы открытое замечание и расходился бы с shipctl,
// чей reviewItems сбрасывает элемент на пустой строке (DK-277, DoD).
func TestReviewBlankLineClosesItem(t *testing.T) {
	root := setup(t)
	taskContent := "# XR-005\n\n## Ревью\n\n- открытое\n\nисправлено в коммите abc\n"
	if err := os.WriteFile(taskFileAbs(root, "XR-005"), []byte(taskContent), 0o644); err != nil {
		t.Fatal(err)
	}
	rf, err := loadReview(taskFileAbs(root, "XR-005"))
	if err != nil {
		t.Fatal(err)
	}
	if len(rf.notes) != 1 {
		t.Fatalf("ожидал 1 элемент, разобрано %d: абзац не должен прирастать через пустую строку", len(rf.notes))
	}
	if got := rf.notes[0].outcome(); got != "" {
		t.Errorf("замечание должно быть открыто, outcome %q (текст: %s)", got, rf.notes[0].Text)
	}
	if strings.Contains(rf.notes[0].Text, "исправлено") {
		t.Errorf("абзац прирастал к замечанию через пустую строку: %s", rf.notes[0].Text)
	}
}

// resolve многострочного замечания схлопывает элемент в одну строку: loadReview
// собрал продолжения в n.Text, и строки переноса уходят из файла, иначе резолвнутая
// строка повторяла бы их после себя (DK-277).
func TestReviewResolveWrapped(t *testing.T) {
	root := setup(t)
	content := "# XR-005\n\n## Ревью\n\n" +
		"- длинное замечание,\n  перенесённое на две строки\n"
	if err := os.WriteFile(taskFileAbs(root, "XR-005"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdReviewResolve(root, "XR-005", 1, "fixed", "", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	got := readTaskFile(t, root, "XR-005")
	want := "- длинное замечание, перенесённое на две строки: исправлено\n"
	if !strings.Contains(got, want) {
		t.Fatalf("перенесённое замечание не схлопнуто в одну строку:\n%s\nожидалось %q", got, want)
	}
	if c := strings.Count(got, "перенесённое"); c != 1 {
		t.Errorf("слово перенесённое должно встречаться 1 раз, встречается %d:\n%s", c, got)
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

// TestReviewStatsLevels: свод по уровням берёт уровень из строки раздела
// «Ревью», а ходы и минуты из уже выгруженной строки «Хода работы» (DK-731),
// живых задач и архива вперемешку. p90 уровня, вышедшего за бюджет
// review.conf, назван в выводе; уровень без записанной работы и уровень без
// единого ревью печатаются словами, а не пропадают из свода.
func TestReviewStatsLevels(t *testing.T) {
	root := setup(t)
	tasks := filepath.Join(root, "docs", "tasks")
	write := func(rel, body string) {
		p := filepath.Join(tasks, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Уровень 1, две живые задачи: ходы 10 и 20, минуты 3 и 5, обе в вилке
	// бюджета level1 из конфига теста.
	write("XR-005.md", "# XR-005\n\n## Ревью\n\nУровень 1 до a1b2c3d: рутина\n\n"+
		"## Ход работы\n\n- Ревью: ревью провёл sonnet, ходов 10, минут 3, 2026-08-31 12:00-12:07.\n")
	write("XR-002.md", "# XR-002\n\n## Ревью\n\nУровень 1 до a1b2c3d: рутина\n\n"+
		"## Ход работы\n\n- Ревью: ревью провёл sonnet, ходов 20, минут 5, 2026-08-31 12:00-12:09.\n")
	// Уровень 2, задача в архиве: ходы 85 выше бюджета level2 (70), минуты 15
	// в вилке.
	write("archive/2025/XR-100.md", "# XR-100\n\n## Ревью\n\nУровень 2 до e5f6a7b: тронут tools/shipctl\n\n"+
		"## Ход работы\n\n- Ревью: ревью провёл opus, ходов 85, минут 15, 2026-08-31 13:00-13:20.\n")
	// Уровень 0, задача в архиве: строка уровня есть, работа не записана.
	write("archive/2025/XR-101.md", "# XR-101\n\n## Ревью\n\nУровень 0 до 1234567: мелочь мимо ветки\n\n"+
		"## Ход работы\n\n- Разработка: субагент opus/low по вердикту pick, 2026-08-31 09:00-09:10.\n")
	writeReviewConf(t, root, "level1 = 5 минут, 20 ходов\nlevel2 = 20 минут, 70 ходов\nlevel3 = 40 минут, 100 ходов\n")

	msg, err := cmdReviewStats(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"уровни ревью:",
		"0: 1 ревью, ходы и минуты не записаны",
		"1: 2 ревью, ходов медиана 15 p90 19, минут медиана 4 p90 5",
		"2: 1 ревью, ходов медиана 85 p90 85, минут медиана 15 p90 15",
		"уровень 2 выше бюджета: ходы p90 85 выше бюджета 70",
		"3: ревью нет",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("в статистике нет %q:\n%s", want, msg)
		}
	}
	if strings.Contains(msg, "уровень 1 выше бюджета") {
		t.Errorf("уровень 1 внутри бюджета помечен как вышедший:\n%s", msg)
	}
}

// TestReviewStatsNoConf: без review.conf сравнение с бюджетом пропускается
// строкой об этом, а не молчанием и не отказом.
func TestReviewStatsNoConf(t *testing.T) {
	root := setup(t)
	tasks := filepath.Join(root, "docs", "tasks")
	body := "# XR-005\n\n## Ревью\n\nУровень 1 до a1b2c3d: рутина\n\n" +
		"## Ход работы\n\n- Ревью: ревью провёл sonnet, ходов 10, минут 3, 2026-08-31 12:00-12:07.\n"
	if err := os.WriteFile(filepath.Join(tasks, "XR-005.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	msg, err := cmdReviewStats(root)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "1: 1 ревью") {
		t.Fatalf("уровень 1 не посчитан без конфига:\n%s", msg)
	}
	if !strings.Contains(msg, reviewConfRel+" нет, сравнение с бюджетом пропущено") {
		t.Fatalf("нет строки про пропуск сравнения без конфига:\n%s", msg)
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

// TestReviewCleanWritesVerdict: чистый вердикт ложится в раздел «Ревью»
// готовой формой, и критерий исхода узнаёт его как «чисто». Ради этого
// команда и заведена: раньше чистое ревью изображали замечанием с текстом
// «замечаний нет» (DK-471).
func TestReviewCleanWritesVerdict(t *testing.T) {
	root := setup(t)
	msg, err := cmdReviewClean(root, "XR-005", "Путь от симптома пройден по ops.go.", CommitOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "вердикт без замечаний записан") {
		t.Fatalf("сообщение: %q", msg)
	}
	want := "# XR-005\n\n## Ревью\n\n- Вердикт: без замечаний. Путь от симптома пройден по ops.go.\n" +
		fixtureScenario + fixtureVerification
	if got := readTaskFile(t, root, "XR-005"); got != want {
		t.Fatalf("файл задачи:\n%q\nожидал:\n%q", got, want)
	}
	rf, err := loadReview(taskFileAbs(root, "XR-005"))
	if err != nil {
		t.Fatal(err)
	}
	if len(rf.notes) != 1 || rf.notes[0].outcome() != "чисто" {
		t.Fatalf("исход элемента: %+v", rf.notes)
	}
}

// TestReviewCleanWithoutNote: пояснение необязательно, и без него запись
// кончается точкой, за которую цепляется критерий исхода.
func TestReviewCleanWithoutNote(t *testing.T) {
	root := setup(t)
	if _, err := cmdReviewClean(root, "XR-005", "", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	if got := readTaskFile(t, root, "XR-005"); !strings.Contains(got, "- Вердикт: без замечаний.\n") {
		t.Fatalf("файл задачи:\n%s", got)
	}
	show, err := cmdReviewShow(root, "XR-005")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(show, "[чисто]") {
		t.Fatalf("show не показал чистый исход:\n%s", show)
	}
}

// TestReviewCleanRefusesOpenNote: открытое замечание и чистый вердикт в одном
// разделе противоречат друг другу, и вердикт поверх открытого замечания
// отбивается подсказкой про resolve.
func TestReviewCleanRefusesOpenNote(t *testing.T) {
	root := setup(t)
	if _, err := cmdReviewAdd(root, "XR-005", "гонка в close", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	_, err := cmdReviewClean(root, "XR-005", "", CommitOpts{})
	if err == nil {
		t.Fatal("вердикт поверх открытого замечания должен отбиваться")
	}
	if !strings.Contains(err.Error(), "review resolve") {
		t.Fatalf("отказ без подсказки: %v", err)
	}
	if got := readTaskFile(t, root, "XR-005"); strings.Contains(got, "без замечаний") {
		t.Fatalf("отбитый вердикт всё-таки записан:\n%s", got)
	}
}

// TestReviewCleanAfterResolved: закрытые замечания вердикту не мешают, это
// второй круг ревью, кончившийся чисто.
func TestReviewCleanAfterResolved(t *testing.T) {
	root := setup(t)
	if _, err := cmdReviewAdd(root, "XR-005", "нейминг", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdReviewResolve(root, "XR-005", 1, "fixed", "", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdReviewClean(root, "XR-005", "Правки по замечанию приняты.", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	got := readTaskFile(t, root, "XR-005")
	if !strings.Contains(got, "- нейминг: исправлено\n- Вердикт: без замечаний. Правки по замечанию приняты.\n") {
		t.Fatalf("файл задачи:\n%s", got)
	}
	// Второй вердикт поверх первого это уже дубль, и он отбивается.
	if _, err := cmdReviewClean(root, "XR-005", "", CommitOpts{}); err == nil {
		t.Fatal("повторный вердикт должен отбиваться")
	}
}

// TestReviewCleanCreatesFile: файл задачи заводится сам, как у add, и ссылка
// в строке доски чинится тем же порядком.
func TestReviewCleanCreatesFile(t *testing.T) {
	root := setup(t)
	dropTaskFile(t, root, "XR-001")
	msg, err := cmdReviewClean(root, "XR-001", "", CommitOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "файл задачи создан") {
		t.Fatalf("сообщение: %q", msg)
	}
	if got := readTaskFile(t, root, "XR-001"); !strings.Contains(got, "- Вердикт: без замечаний.") {
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

// TestReviewCleanCLI: подкоманда доезжает до разбора аргументов, а не живёт
// одной функцией, и справка её называет.
func TestReviewCleanCLI(t *testing.T) {
	root := setup(t)
	out, err := exec.Command("go", "run", ".", "-C", root, "review", "clean", "XR-005", "Дифф прочитан целиком.").CombinedOutput()
	if err != nil {
		t.Fatalf("review clean: %v\n%s", err, out)
	}
	if got := readTaskFile(t, root, "XR-005"); !strings.Contains(got, "- Вердикт: без замечаний. Дифф прочитан целиком.\n") {
		t.Fatalf("файл задачи:\n%s", got)
	}
	help, _ := exec.Command("go", "run", ".", "-C", root, "help").CombinedOutput()
	if !strings.Contains(string(help), "review clean <ID>") {
		t.Fatalf("справка без подкоманды:\n%s", help)
	}
	bad, err := exec.Command("go", "run", ".", "-C", root, "review", "clea", "XR-005").CombinedOutput()
	if err == nil {
		t.Fatalf("опечатка в подкоманде должна отбиваться:\n%s", bad)
	}
	if !strings.Contains(string(bad), "clean") {
		t.Fatalf("отказ не называет подкоманду:\n%s", bad)
	}
}

// TestReviewLevelWritesFirstLine: уровень встаёт первой строкой раздела
// «Ревью» и заводит сам раздел, когда его в файле ещё нет. Sha в строке это
// HEAD дерева, по которому шло ревью.
func TestReviewLevelWritesFirstLine(t *testing.T) {
	root := setup(t)
	gitSetup(t, root)
	msg, err := cmdReviewLevel(root, "XR-005", 2, "неопределённость 1, тронут tools/shipctl", CommitOpts{})
	if err != nil {
		t.Fatal(err)
	}
	sha := gitOut(t, root, "rev-parse", "--short=7", "HEAD")
	want := "Уровень 2 до " + sha + ": неопределённость 1, тронут tools/shipctl"
	if !strings.Contains(msg, "уровень ревью 2 до "+sha+" записан") {
		t.Fatalf("сообщение: %q", msg)
	}
	got := readTaskFile(t, root, "XR-005")
	if !strings.Contains(got, "## Ревью\n\n"+want+"\n") {
		t.Fatalf("строка уровня не первая в разделе:\n%s", got)
	}
	show, err := cmdReviewShow(root, "XR-005")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(show, want) {
		t.Fatalf("review show без строки уровня: %q", show)
	}
}

// TestReviewLevelRewrites: повторный вызов переписывает строку, а не кладёт
// вторую. Пересмотр уровня по ходу ревью это та же запись с новым основанием.
func TestReviewLevelRewrites(t *testing.T) {
	root := setup(t)
	gitSetup(t, root)
	if _, err := cmdReviewLevel(root, "XR-005", 1, "рутина", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	msg, err := cmdReviewLevel(root, "XR-005", 3, "тронуты доступы", CommitOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "переписан") {
		t.Fatalf("сообщение: %q", msg)
	}
	got := readTaskFile(t, root, "XR-005")
	if n := strings.Count(got, "Уровень "); n != 1 {
		t.Fatalf("строк уровня %d, ждём одну:\n%s", n, got)
	}
	if !strings.Contains(got, ": тронуты доступы\n") || strings.Contains(got, "рутина") {
		t.Fatalf("строка не переписана:\n%s", got)
	}
}

// TestReviewLevelKeepsNotes: замечания остаются ниже строки уровня и своих
// исходов не теряют.
func TestReviewLevelKeepsNotes(t *testing.T) {
	root := setup(t)
	gitSetup(t, root)
	if _, err := cmdReviewAdd(root, "XR-005", "гонка в close", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdReviewLevel(root, "XR-005", 2, "тронут tools/taskctl", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	got := readTaskFile(t, root, "XR-005")
	lvl, note := strings.Index(got, "Уровень 2 до "), strings.Index(got, "- гонка в close")
	if lvl < 0 || note < 0 || lvl > note {
		t.Fatalf("уровень и замечание встали не в том порядке:\n%s", got)
	}
	if !strings.Contains(got, "\n\n- гонка в close") {
		t.Fatalf("замечание слиплось со строкой уровня:\n%s", got)
	}
	// Нумерация замечаний строкой уровня не сбивается: она не элемент списка.
	if _, err := cmdReviewResolve(root, "XR-005", 1, "fixed", "", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	if got := readTaskFile(t, root, "XR-005"); !strings.Contains(got, "- гонка в close: исправлено\n") {
		t.Fatalf("исход замечания:\n%s", got)
	}
}

// TestReviewLevelBeforeAdd: строка уровня, записанная до первого замечания
// (так диспетчер ставит стартовый уровень перед ревью), не съедает следующее
// замечание. Без правки DK-760 замечание вставало перед строкой уровня, и
// повторный разбор принимал строку уровня за перенос замечания, склеивая их в
// один пункт (было на DK-757). Порядок «level, потом add» должен давать тот
// же раздел, что и «add, потом level» (TestReviewLevelKeepsNotes).
func TestReviewLevelBeforeAdd(t *testing.T) {
	root := setup(t)
	gitSetup(t, root)
	if _, err := cmdReviewLevel(root, "XR-005", 2, "стартовый уровень от диспетчера", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdReviewAdd(root, "XR-005", "гонка в close", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	got := readTaskFile(t, root, "XR-005")
	lvl, note := strings.Index(got, "Уровень 2 до "), strings.Index(got, "- гонка в close")
	if lvl < 0 || note < 0 || lvl > note {
		t.Fatalf("уровень и замечание встали не в том порядке:\n%s", got)
	}
	if !strings.Contains(got, "\n\n- гонка в close") {
		t.Fatalf("замечание слиплось со строкой уровня:\n%s", got)
	}
	if !strings.Contains(got, "\n\n## Сценарий проверки") {
		t.Fatalf("список замечаний слипся со следующим разделом:\n%s", got)
	}
	show, err := cmdReviewShow(root, "XR-005")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(show, "Уровень 2 до ") || !strings.Contains(show, "\n1. [открыто] гонка в close") {
		t.Fatalf("show не развёл уровень и замечание: %q", show)
	}
	if strings.Contains(show, "гонка в close стартовый") || strings.Contains(show, "путьгонка") {
		t.Fatalf("текст уровня слипся с замечанием: %q", show)
	}
}

// TestReviewLevelRefuses: причина обязательна на любом уровне, включая нулевой
// (осознанный пропуск), а уровень вне шкалы 0-3 отбивается. Ни то, ни другое в
// файл не пишется.
func TestReviewLevelRefuses(t *testing.T) {
	root := setup(t)
	gitSetup(t, root)
	if _, err := cmdReviewLevel(root, "XR-005", 0, "  ", CommitOpts{}); err == nil {
		t.Fatal("пустая причина должна отбиваться")
	} else if !strings.Contains(err.Error(), "жду причину уровня") {
		t.Fatalf("отказ без подсказки: %v", err)
	}
	if _, err := cmdReviewLevel(root, "XR-005", 4, "мимо шкалы", CommitOpts{}); err == nil {
		t.Fatal("уровень 4 должен отбиваться")
	} else if !strings.Contains(err.Error(), "0-3") {
		t.Fatalf("отказ без шкалы: %v", err)
	}
	if got := readTaskFile(t, root, "XR-005"); strings.Contains(got, "Уровень") {
		t.Fatalf("отбитая запись всё-таки в файле:\n%s", got)
	}
	// Уровень 0 с причиной проходит: это запись осознанного пропуска.
	if _, err := cmdReviewLevel(root, "XR-005", 0, "мелочь мимо ветки", CommitOpts{}); err != nil {
		t.Fatal(err)
	}
	if got := readTaskFile(t, root, "XR-005"); !strings.Contains(got, "Уровень 0 до ") {
		t.Fatalf("уровень 0 не записан:\n%s", got)
	}
}

// TestReviewLevelCLI: подкоманда доезжает до разбора аргументов, справка её
// называет, а нечисловой уровень отбивается разбором.
func TestReviewLevelCLI(t *testing.T) {
	root := setup(t)
	gitSetup(t, root)
	out, err := exec.Command("go", "run", ".", "-C", root, "review", "level", "XR-005", "2", "тронут tools/shipctl").CombinedOutput()
	if err != nil {
		t.Fatalf("review level: %v\n%s", err, out)
	}
	if got := readTaskFile(t, root, "XR-005"); !strings.Contains(got, ": тронут tools/shipctl\n") {
		t.Fatalf("файл задачи:\n%s", got)
	}
	help, _ := exec.Command("go", "run", ".", "-C", root, "help").CombinedOutput()
	if !strings.Contains(string(help), "review level <ID>") {
		t.Fatalf("справка без подкоманды:\n%s", help)
	}
	bad, err := exec.Command("go", "run", ".", "-C", root, "review", "level", "XR-005", "два", "причина").CombinedOutput()
	if err == nil {
		t.Fatalf("нечисловой уровень должен отбиваться:\n%s", bad)
	}
	if !strings.Contains(string(bad), "не число") {
		t.Fatalf("отказ без причины:\n%s", bad)
	}
}
