package frame

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// setupRepo делает временный git-репозиторий с каталогом .devkit/cmdout: полный
// вывод пишется в корень репозитория, и Capture ищет его через git. Тесты
// детерминированы: временный HOME страхует от чужого git-конфига, команды с
// фиксированным выводом (printf, sh -c) не зависят от тулчейна.
func setupRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	gitT(t, root, "init", "-q", "-b", "main")
	// Глобальный конфиг_user на машине может быть пустым (новый CI), и тогда
	// commit падает: ставим свой, только для теста.
	gitT(t, root, "config", "user.email", "test@test")
	gitT(t, root, "config", "user.name", "test")
	write(t, root, "README", "seed\n")
	gitT(t, root, "add", ".")
	gitT(t, root, "commit", "-qm", "seed")
	if err := os.MkdirAll(filepath.Join(root, ".devkit", "cmdout"), 0o755); err != nil {
		t.Fatal(err)
	}
	return root
}

func gitT(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func write(t *testing.T, root, name, content string) {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// sh это одноклеточный прогон: argv[0] всегда sh, а в -c лежит скрипт. Так
// тесты не зависят от того, какие именно команды собраны на машине.
func sh(script string) []string {
	return []string{"sh", "-c", script}
}

// TestCaptureBelowThreshold отдаёт короткий вывод как есть, выжимка не
// строится: ниже порога резать нечего.
func TestCaptureBelowThreshold(t *testing.T) {
	root := setupRepo(t)
	s, err := Capture(root, sh("printf 'line %d\\n' 1 2 3"))
	if err != nil {
		t.Fatal(err)
	}
	if s.Summarized {
		t.Fatalf("короткий вывод отдалён выжимкой: %+v", s)
	}
	if s.Raw != "line 1\nline 2\nline 3\n" {
		t.Fatalf("raw вывод: %q", s.Raw)
	}
	if s.Exit != 0 {
		t.Fatalf("exit: %d", s.Exit)
	}
}

// TestCaptureThresholdLines сто строк это порог, выжимка строится.
func TestCaptureThresholdLines(t *testing.T) {
	root := setupRepo(t)
	// seq на macOS и GNU совместим по выводу для положительных целых.
	s, err := Capture(root, sh("seq 1 100"))
	if err != nil {
		t.Fatal(err)
	}
	if !s.Summarized {
		t.Fatalf("100 строк это порог, выжимка должна строиться: %+v", s)
	}
	if s.LinesTotal != 100 {
		t.Fatalf("lines_total: %d, хотели 100", s.LinesTotal)
	}
}

// TestCaptureThresholdRunes порог измеряется в символах UTF-8 (рунах): 4097 цифр
// одной строкой это и 4097 рун, и 4097 байт, выжимка строится.
func TestCaptureThresholdRunes(t *testing.T) {
	root := setupRepo(t)
	// Одна строка длиннее 4K, строка всего одна: порог по символам решает.
	s, err := Capture(root, sh("printf '%04097d\\n' 0"))
	if err != nil {
		t.Fatal(err)
	}
	if !s.Summarized {
		t.Fatalf("4097 рун это порог, выжимка должна строиться: %+v", s)
	}
	if s.LinesTotal != 1 {
		t.Fatalf("lines_total: %d, хотели 1", s.LinesTotal)
	}
}

// TestCaptureThresholdMultibyte мультибайтный вывод 3000 кириллических рун это
// 6000 байт UTF-8: по байтам выше порога, по символам ниже, выжимка не строится.
// На байтовом пороге тест падал бы, ибо 6000 >= 4096.
func TestCaptureThresholdMultibyte(t *testing.T) {
	root := setupRepo(t)
	s, err := Capture(root, sh("printf 'а%.0s' $(seq 1 3000); echo"))
	if err != nil {
		t.Fatal(err)
	}
	if s.Summarized {
		t.Fatalf("3000 рун ниже порога 4096, выжимка не должна строиться: байт=%d", len(s.Raw))
	}
}

// TestSignificant ловит маркеры ошибок и считает lines_hidden без потерь.
// Significant строки стоят в середине и не пересекаются с хвостом: скрыты
// все, кроме значимых и хвоста.
func TestSignificant(t *testing.T) {
	root := setupRepo(t)
	// 150 строк: «line 1» ... «line 150». В строки 10, 20, 30, 40, 50 встроены
	// маркеры FAIL и panic:, чтобы покрыть минимум два паттерна из набора.
	script := `n=1
while [ $n -le 150 ]; do
  case $n in
    10) echo "FAIL case_ten";;
    20) echo "panic: at row twenty";;
    30) echo "Error: row thirty";;
    40) echo "fatal: row forty";;
    50) echo "not found row fifty";;
    *) echo "line $n";;
  esac
  n=$((n+1))
done`
	s, err := Capture(root, sh(script))
	if err != nil {
		t.Fatal(err)
	}
	if !s.Summarized {
		t.Fatal("150 строк должны дать выжимку")
	}
	if len(s.Significant) != 5 {
		t.Fatalf("significant: %d строк, хотели 5: %v", len(s.Significant), s.Significant)
	}
	for _, want := range []string{"FAIL case_ten", "panic: at row twenty",
		"Error: row thirty", "fatal: row forty", "not found row fifty"} {
		found := false
		for _, got := range s.Significant {
			if got == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("significant не содержит %q: %v", want, s.Significant)
		}
	}
	// Хвост это строки 121..150 (30 штук), significant на 10, 20, 30, 40, 50.
	// union индексов = 35, lines_hidden = 150 - 35 = 115.
	if s.LinesHidden != 115 {
		t.Errorf("lines_hidden: %d, хотели 115", s.LinesHidden)
	}
	if len(s.Tail) != 30 {
		t.Errorf("tail: %d строк, хотели 30", len(s.Tail))
	}
	if s.Tail[len(s.Tail)-1] != "line 150" {
		t.Errorf("последняя строка tail: %q, хотели line 150", s.Tail[len(s.Tail)-1])
	}
}

// TestSignificantOverflow режет значимые после 30 и считает скрытые честно:
// обрезанные значимые в lines_hidden входят, потому что они не показаны.
func TestSignificantOverflow(t *testing.T) {
	root := setupRepo(t)
	// 150 значимых строк с FAIL, всего 150 строк. Significant покрывает всё,
	// но к показу идут первые 30 + пометка. Хвост строки 121..150.
	script := `n=1
while [ $n -le 150 ]; do
  echo "FAIL row $n"
  n=$((n+1))
done`
	s, err := Capture(root, sh(script))
	if err != nil {
		t.Fatal(err)
	}
	if !s.Summarized {
		t.Fatal("должна быть выжимка")
	}
	// 30 показанных + пометка «... и ещё 120 значимых строк».
	if len(s.Significant) != 31 {
		t.Fatalf("significant: %d строк, хотели 31 (30 + пометка): %v",
			len(s.Significant), s.Significant)
	}
	if !strings.Contains(s.Significant[30], "... и ещё 120 значимых строк") {
		t.Errorf("пометка обрезки: %q", s.Significant[30])
	}
	// union: показанные significant это индексы 0..29, хвост 120..149.
	// Индексы 120..129 входят и в significant, и в tail (считаем один раз).
	// union = {0..29} объединить с {120..149} = 30 + 30 = 60. hidden = 150 - 60 = 90.
	if s.LinesHidden != 90 {
		t.Errorf("lines_hidden: %d, хотели 90", s.LinesHidden)
	}
}

// TestSignificantTailOverlap покрывает пересечение: significant в хвосте не
// считается дважды. lines_hidden это lines_total минус мощность объединения.
func TestSignificantTailOverlap(t *testing.T) {
	root := setupRepo(t)
	// 150 строк, значимые в строках 130, 140, 150 (всё в хвосте 121..150).
	// union = {130, 140, 150} объединить с {121..150} = {121..150} = 30. hidden = 120.
	script := `n=1
while [ $n -le 150 ]; do
  case $n in
    130) echo "FAIL row 130";;
    140) echo "panic: row 140";;
    150) echo "Error: row 150";;
    *) echo "line $n";;
  esac
  n=$((n+1))
done`
	s, err := Capture(root, sh(script))
	if err != nil {
		t.Fatal(err)
	}
	if s.LinesHidden != 120 {
		t.Errorf("lines_hidden: %d, хотели 120 (significant внутри tail)", s.LinesHidden)
	}
}

// TestPathWrittenToFile проверяет, что полный вывод действительно лежит в файле
// по пути из Path: файл существует, читается и совпадает с тем, что выдала
// команда. DoD требует «файл по пути читается целиком».
func TestPathWrittenToFile(t *testing.T) {
	root := setupRepo(t)
	s, err := Capture(root, sh("seq 1 200"))
	if err != nil {
		t.Fatal(err)
	}
	if s.Path == "" {
		t.Fatal("Path пустой, файл не записан")
	}
	info, err := os.Stat(s.Path)
	if err != nil {
		t.Fatalf("файл вывода не существует: %v", err)
	}
	if info.Size() == 0 {
		t.Fatal("файл вывода пуст")
	}
	data, err := os.ReadFile(s.Path)
	if err != nil {
		t.Fatalf("файл вывода не читается: %v", err)
	}
	// Каждая из 200 строк seq должна лежать в файле по порядку.
	body := string(data)
	rows := strings.Split(strings.TrimSuffix(body, "\n"), "\n")
	if len(rows) != 200 {
		t.Fatalf("строк в файле: %d, хотели 200", len(rows))
	}
	for i, row := range rows {
		want := strconv.Itoa(i + 1)
		if row != want {
			t.Fatalf("строка %d: %q, хотели %q", i+1, row, want)
		}
	}
	if !strings.HasSuffix(body, "200\n") {
		t.Errorf("файл вывода должен кончаться на «200\\n», конец: %q", body[len(body)-20:])
	}
	if !filepath.IsAbs(s.Path) {
		t.Errorf("Path не абсолютный: %q", s.Path)
	}
}

// TestExitCode проваливается через код возврата команды. Exit ненулевой при
// ошибке, не оттого что Capture не нашёл бинарь.
func TestExitCode(t *testing.T) {
	root := setupRepo(t)
	s, err := Capture(root, sh("exit 7"))
	if err != nil {
		t.Fatal(err)
	}
	if s.Exit != 7 {
		t.Errorf("exit: %d, хотели 7", s.Exit)
	}
	s2, err := Capture(root, sh("exit 0"))
	if err != nil {
		t.Fatal(err)
	}
	if s2.Exit != 0 {
		t.Errorf("exit: %d, хотели 0", s2.Exit)
	}
}

// TestCaptureNoArgs это ошибка контракта: Capture без команды nobody не должен
// звать.
func TestCaptureNoArgs(t *testing.T) {
	root := setupRepo(t)
	if _, err := Capture(root, nil); err == nil {
		t.Fatal("Capture без argv должен ошибаться")
	}
}

// TestSlug одинаково трактует имя команды и полный путь.
func TestSlug(t *testing.T) {
	for in, want := range map[string]string{
		"go":              "go",
		"git":             "git",
		"/usr/bin/git":    "git",
		"./regcheck":      "regcheck",
		`C:\bin\go.exe`:   "go.exe",
		"":                "cmd",
	} {
		if got := slug([]string{in}); got != want {
			t.Errorf("slug(%q) = %q, хотели %q", in, got, want)
		}
	}
}

// TestRender сохраняет порядок полей LLD. Парсинг фиксированным списком строк
// ловит разъезд порядка и пропуски: первое поле обязано быть exit, последнее
// path.
func TestRender(t *testing.T) {
	s := &Summary{
		Exit:        2,
		Path:        "/tmp/out",
		LinesTotal:  10,
		LinesHidden: 3,
		Significant: []string{"FAIL: x", "panic: y"},
		Tail:        []string{"last1", "last2"},
		Summarized:  true,
	}
	out := s.Render()
	wantOrder := []string{
		"exit: 2",
		"lines_total: 10",
		"lines_hidden: 3",
		"significant:",
		"FAIL: x",
		"panic: y",
		"tail:",
		"last1",
		"last2",
		"path: /tmp/out",
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != len(wantOrder) {
		t.Fatalf("Render отдал %d строк, хотели %d: %q", len(lines), len(wantOrder), out)
	}
	for i, w := range wantOrder {
		if lines[i] != w {
			t.Errorf("строка %d: %q, хотели %q (весь вывод: %q)", i, lines[i], w, out)
		}
	}
}

// TestRenderEmpty покрывает выжимку без значимых строк: significant остаётся
// пустым блококом, выжимка остаётся читаемой.
func TestRenderEmpty(t *testing.T) {
	s := &Summary{
		Exit:        0,
		Path:        "/tmp/out",
		LinesTotal:  5,
		LinesHidden: 0,
		Tail:        []string{"a", "b"},
		Summarized:  true,
	}
	out := s.Render()
	if !strings.Contains(out, "significant:\n") {
		t.Errorf("significant блок пропал: %q", out)
	}
	if strings.Contains(out, "FAIL") {
		t.Errorf("пустой significant не должен содержать маркеры: %q", out)
	}
}

// TestCaptureNotFound это ошибка запуска несуществующей команды, а не тихий
// нулевой exit: Capture обязан вернуть ошибку, потому что ExitCode у неё взять
// неоткуда.
func TestCaptureNotFound(t *testing.T) {
	root := setupRepo(t)
	if _, err := Capture(root, []string{"нет-такой-команды-xyz"}); err == nil {
		t.Fatal("несуществующая команда должна дать ошибку")
	}
}

// TestSummarizeFromBytes строит сводку из готового вывода, не запуская команду.
// Короткий вывод ниже порога: Summarized ложь, Raw совпадает с входом, exit
// сохранён, путь к файлу полного вывода заполнен. Это путь shipctl с уже
// готовым out от exec.Command: повторно команду пускать нельзя.
func TestSummarizeFromBytes(t *testing.T) {
	root := setupRepo(t)
	out := []byte("line 1\nline 2\nline 3\n")
	s := Summarize(root, []string{"git", "status"}, out, 42)
	if s.Summarized {
		t.Fatalf("короткий вывод не должен строить выжимку: %+v", s)
	}
	if s.Raw != string(out) {
		t.Fatalf("Raw: %q, хотели %q", s.Raw, string(out))
	}
	if s.Exit != 42 {
		t.Fatalf("exit: %d, хотели 42", s.Exit)
	}
	if s.Path == "" {
		t.Fatal("Path пустой, файл полного вывода не записан")
	}
	if !filepath.IsAbs(s.Path) {
		t.Errorf("Path не абсолютный: %q", s.Path)
	}
	data, err := os.ReadFile(s.Path)
	if err != nil {
		t.Fatalf("файл вывода не читается: %v", err)
	}
	if string(data) != string(out) {
		t.Fatalf("содержимое файла: %q, хотели %q", string(data), string(out))
	}
}

// TestSummarizeThreshold сто строк это порог, выжимка строится из готового
// вывода тем же путём, что и в Capture. Состав полей не должен разъехаться.
func TestSummarizeThreshold(t *testing.T) {
	root := setupRepo(t)
	// seq 1 100 одной строкой, без запуска команды.
	var b strings.Builder
	for i := 1; i <= 100; i++ {
		fmt.Fprintf(&b, "%d\n", i)
	}
	s := Summarize(root, []string{"seq"}, []byte(b.String()), 0)
	if !s.Summarized {
		t.Fatal("100 строк должны дать выжимку")
	}
	if s.LinesTotal != 100 {
		t.Errorf("lines_total: %d, хотели 100", s.LinesTotal)
	}
	if len(s.Tail) != 30 {
		t.Errorf("tail: %d строк, хотели 30", len(s.Tail))
	}
	if s.Tail[len(s.Tail)-1] != "100" {
		t.Errorf("последняя строка tail: %q, хотели 100", s.Tail[len(s.Tail)-1])
	}
}

// TestSummarizeSignificant это регрессионный тест бага DK-266: вывод со
// значимым маркером (FAIL, panic:) в середине и хвостом без маркеров на старом
// tail(out) показывал только хвост, на Summarize маркер ловится в significant.
// Двести строк, маркеры в строках 40 и 80, хвост 171..200 без маркеров.
func TestSummarizeSignificant(t *testing.T) {
	root := setupRepo(t)
	var b strings.Builder
	for i := 1; i <= 200; i++ {
		switch i {
		case 40:
			b.WriteString("FAIL row_forty\n")
		case 80:
			b.WriteString("panic: row_eighty\n")
		default:
			fmt.Fprintf(&b, "line %d\n", i)
		}
	}
	s := Summarize(root, []string{"test"}, []byte(b.String()), 1)
	if !s.Summarized {
		t.Fatal("200 строк должны дать выжимку")
	}
	want := []string{"FAIL row_forty", "panic: row_eighty"}
	if len(s.Significant) != len(want) {
		t.Fatalf("significant: %d строк, хотели %d: %v", len(s.Significant), len(want), s.Significant)
	}
	for i, w := range want {
		if s.Significant[i] != w {
			t.Errorf("significant[%d]: %q, хотели %q", i, s.Significant[i], w)
		}
	}
	// Хвост 171..200 без маркеров: ни одна significant строка в хвост не попадает.
	for _, tl := range s.Tail {
		for _, w := range want {
			if tl == w {
				t.Errorf("significant строка %q попала в tail, а хвост был без маркеров", tl)
			}
		}
	}
	// lines_total: 200, significant 2, tail 30, без пересечений. hidden = 200 - 32 = 168.
	if s.LinesHidden != 168 {
		t.Errorf("lines_hidden: %d, хотели 168", s.LinesHidden)
	}
}

// TestSummarizeEmptyArgv не ошибается на пустом argv: готовый вывод мог
// прийти не из команды. Slug падает на «cmd», файл всё равно пишется.
func TestSummarizeEmptyArgv(t *testing.T) {
	root := setupRepo(t)
	var b strings.Builder
	for i := 1; i <= 150; i++ {
		fmt.Fprintf(&b, "line %d\n", i)
	}
	s := Summarize(root, nil, []byte(b.String()), 1)
	if !s.Summarized {
		t.Fatal("150 строк должны дать выжимку даже без argv")
	}
	if s.Path == "" {
		t.Fatal("Path пустой без argv, файл не записан")
	}
	// Slug «cmd» виден в пути каталога.
	if !strings.Contains(s.Path, "-cmd") {
		t.Errorf("Path без slug «cmd» при пустом argv: %q", s.Path)
	}
}

// TestRenderRaw это Render для Summarized ложь: вывод отдаётся как есть, к нему
// приписывается path. Поле path единственное, никаких полей LLD выше порога.
func TestRenderRaw(t *testing.T) {
	s := &Summary{
		Exit:       0,
		Path:       "/tmp/short",
		Raw:        "line 1\nline 2\n",
		Summarized: false,
	}
	out := s.Render()
	if !strings.HasPrefix(out, "line 1\nline 2\n") {
		t.Errorf("Raw не в начале вывода: %q", out)
	}
	if !strings.Contains(out, "path: /tmp/short") {
		t.Errorf("path пропал в коротком выводе: %q", out)
	}
	if strings.Contains(out, "lines_total") {
		t.Errorf("поле lines_total не должно присутствовать ниже порога: %q", out)
	}
}

// TestRenderRawNoPath: если git-корня нет, path пустой, и Render ниже порога
// отдаёт только Raw без хвоста «path: ». Так сводка остаётся читаемой в тестах
// и во внешних каталогах.
func TestRenderRawNoPath(t *testing.T) {
	s := &Summary{
		Raw:        "короткий вывод\n",
		Summarized: false,
	}
	out := s.Render()
	if out != "короткий вывод\n" {
		t.Fatalf("Render с пустым Path: %q, хотели только Raw", out)
	}
}

// TestSignificantMarkersGuard ловит регрессию набора: случайно выкинутый маркер
// перестаёт ловить строки, которые ловил. Список ведётся в LLD DK-137, и тест
// держит код и текст синхронно.
func TestSignificantMarkersGuard(t *testing.T) {
	want := []string{
		"FAIL", "--- FAIL", "error", "panic:", "Error:", "fatal:",
		"undefined", "cannot find", "not found", "Permission denied", "CONFLICT",
	}
	if len(significantMarkers) != len(want) {
		t.Fatalf("significant markers: %d, хотели %d (%v)", len(significantMarkers), len(want), significantMarkers)
	}
	for i, w := range want {
		if significantMarkers[i] != w {
			t.Errorf("marker %d: %q, хотели %q", i, significantMarkers[i], w)
		}
	}
	for _, m := range want {
		if !isSignificant(m) {
			t.Errorf("маркер %q не ловится isSignificant", m)
		}
	}
	if isSignificant("обычная строка без маркеров") {
		t.Error("обычная строка попала в significant")
	}
}

// cmdoutDir кладёт каталог вывода с указанным содержимым и mtime. mtime ставится
// через os.Chtimes, а не временем создания: Clean сверяется именно с ModTime
// каталога, и тест задаёт его прямо.
func cmdoutDir(t *testing.T, root, name string, body []byte, mtime time.Time) string {
	t.Helper()
	dir := filepath.Join(root, ".devkit", "cmdout", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "out"), body, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(dir, mtime, mtime); err != nil {
		t.Fatal(err)
	}
	// Chtimes каталога на некоторых ФС не меняет mtime файлов внутри, а Clean
	// считает размер по дереву: страхуем, чтобы размер не плавал от платформы.
	if err := os.Chtimes(filepath.Join(dir, "out"), mtime, mtime); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestCleanRemovesStaleDirs: каталог старше порога удаляется, свежий остаётся.
// now параметром, возраст каталога фиксирован относительно него, а не от
// реального времени: тест детерминирован на любой машине.
func TestCleanRemovesStaleDirs(t *testing.T) {
	root := setupRepo(t)
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	maxAge := 7 * 24 * time.Hour
	cmdoutDir(t, root, "20260101T000000-old", []byte("старый вывод\n"),
		now.Add(-30*24*time.Hour))
	cmdoutDir(t, root, "20260812T120000-fresh", []byte("свежий вывод\n"),
		now.Add(-1*time.Hour))
	stats, err := Clean(root, maxAge, now, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(stats.Removed) != 1 {
		t.Fatalf("Removed: %d, хотели 1: %v", len(stats.Removed), stats.Removed)
	}
	if filepath.Base(stats.Removed[0]) != "20260101T000000-old" {
		t.Errorf("удалён %q, хотели old", stats.Removed[0])
	}
	if stats.Bytes != int64(len("старый вывод\n")) {
		t.Errorf("Bytes: %d, хотели %d", stats.Bytes, len("старый вывод\n"))
	}
	oldPath := filepath.Join(root, ".devkit", "cmdout", "20260101T000000-old")
	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Errorf("старый каталог не удалён: %v", err)
	}
	freshPath := filepath.Join(root, ".devkit", "cmdout", "20260812T120000-fresh")
	if _, err := os.Stat(freshPath); err != nil {
		t.Errorf("свежий каталог пропал: %v", err)
	}
}

// TestCleanDryRun не трогает файлы: старые каталоги попадают в Removed, но
// остаются на диске, потому что dry-run только сообщает, что ушло бы.
func TestCleanDryRun(t *testing.T) {
	root := setupRepo(t)
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	maxAge := 7 * 24 * time.Hour
	cmdoutDir(t, root, "20260101T000000-old", []byte("старый вывод\n"),
		now.Add(-30*24*time.Hour))
	stats, err := Clean(root, maxAge, now, true)
	if err != nil {
		t.Fatal(err)
	}
	if !stats.DryRun {
		t.Fatal("статистика без DryRun")
	}
	if len(stats.Removed) != 1 || filepath.Base(stats.Removed[0]) != "20260101T000000-old" {
		t.Fatalf("Removed: %v, хотели только old", stats.Removed)
	}
	oldPath := filepath.Join(root, ".devkit", "cmdout", "20260101T000000-old")
	if _, err := os.Stat(oldPath); err != nil {
		t.Errorf("dry-run удалил каталог: %v", err)
	}
}

// TestCleanEmptyRepo: пустой .devkit/cmdout или отсутствующий не ошибочны,
// чистильщик отдаёт пустую статистику. Так подкоманда молчит на свежем проекте.
func TestCleanEmptyRepo(t *testing.T) {
	root := setupRepo(t)
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	stats, err := Clean(root, 7*24*time.Hour, now, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(stats.Removed) != 0 {
		t.Fatalf("на пустом cmdout Removed не пуст: %v", stats.Removed)
	}
}

// TestCleanThresholdBoundary: возраст ровно maxAge каталог не подпадает, чистка
// строгая по порогу (старше, а не старше или равно). Граничный тест держит
// правило, пока возраст меньше или равен maxAge.
func TestCleanThresholdBoundary(t *testing.T) {
	root := setupRepo(t)
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	maxAge := 7 * 24 * time.Hour
	cmdoutDir(t, root, "20260805T120000-boundary", []byte("ровно порог\n"),
		now.Add(-maxAge))
	cmdoutDir(t, root, "20260805T115900-over", []byte("чуть старше\n"),
		now.Add(-maxAge-1*time.Second))
	stats, err := Clean(root, maxAge, now, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range stats.Removed {
		if filepath.Base(p) == "20260805T120000-boundary" {
			t.Errorf("каталог возрастом ровно maxAge подпал под чистку")
		}
	}
	found := false
	for _, p := range stats.Removed {
		if filepath.Base(p) == "20260805T115900-over" {
			found = true
		}
	}
	if !found {
		t.Errorf("каталог на секунду старше maxAge не подпал под чистку: %v", stats.Removed)
	}
}

// TestCleanNotGit молчит вне репозитория, как и writeFull: чистить вне корня
// некого, и ошибка тут была бы шумом на машинах без git-проекта.
func TestCleanNotGit(t *testing.T) {
	stats, err := Clean(t.TempDir(), 7*24*time.Hour, time.Now(), false)
	if err != nil {
		t.Fatalf("Clean вне git должен молчать, а не ошибаться: %v", err)
	}
	if stats != nil && len(stats.Removed) != 0 {
		t.Errorf("Clean вне git вернул непустую статистику: %+v", stats)
	}
}

// TestCleanIgnoresFiles: случайный файл в .devkit/cmdout не подпадает под
// чистку, удаляются только каталоги. writeFull кладёт только каталоги, но
// ручной файл там оказаться мог, и чистильщик его не трогает.
func TestCleanIgnoresFiles(t *testing.T) {
	root := setupRepo(t)
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	base := filepath.Join(root, ".devkit", "cmdout")
	if err := os.WriteFile(filepath.Join(base, "loose-file"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(filepath.Join(base, "loose-file"),
		now.Add(-30*24*time.Hour), now.Add(-30*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	stats, err := Clean(root, 7*24*time.Hour, now, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(stats.Removed) != 0 {
		t.Errorf("Clean тронул файл в cmdout: %v", stats.Removed)
	}
	if _, err := os.Stat(filepath.Join(base, "loose-file")); err != nil {
		t.Errorf("файл пропал после чистки: %v", err)
	}
}
