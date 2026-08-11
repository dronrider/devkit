package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// formatVer одна строка формата «<имя> <версия> (<коммит>)», как и в соседних
// утилитах: doctor берёт отсюда коммит, разъехавшийся формат ломал бы сверку.
var formatVer = regexp.MustCompile(`^cmdout \S+ \(\S+\)\n$`)

// buildBinary собирает cmdout во временный каталог с GOWORK=off: чужой go.work
// на машине увёл бы сборку из модуля утилиты, а replace на internal разрешается
// из локального go.mod без сети. Один бинарь на тест класса, чтобы не собирать
// перед каждым случаем.
func buildBinary(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "cmdout")
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Env = append(os.Environ(), "GOWORK=off")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("сборка cmdout не прошла: %v\n%s", err, out)
	}
	return bin
}

// setupRepo делает временный git-репозиторий с .devkit/cmdout: cmdout пишет
// полный вывод в корень репо, и без git корня путь не строится.
func setupRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	run := func(args ...string) {
		out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	run("config", "user.email", "test@test")
	run("config", "user.name", "test")
	if err := os.WriteFile(filepath.Join(root, "README"), []byte("seed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-qm", "seed")
	if err := os.MkdirAll(filepath.Join(root, ".devkit", "cmdout"), 0o755); err != nil {
		t.Fatal(err)
	}
	return root
}

// TestPrintVersionLine ловит формат строки версии на printVersion без сборки.
func TestPrintVersionLine(t *testing.T) {
	var b bytes.Buffer
	if !printVersion([]string{"--version"}, &b) {
		t.Fatal("--version не разобран")
	}
	if !formatVer.MatchString(b.String()) {
		t.Fatalf("строка версии не по формату: %q", b.String())
	}
}

// TestPrintVersionStopsAtSeparator: --version после «--» это чужой флаг команды
// прогона, а не наш. Голое «--» останавливает разбор, как и в соседних утилитах.
func TestPrintVersionStopsAtSeparator(t *testing.T) {
	var b bytes.Buffer
	if printVersion([]string{"--", "--version"}, &b) || b.Len() > 0 {
		t.Fatalf("чужой --version после «--» принят за свой: %q", b.String())
	}
}

// TestVersionFlagOnTheBinary на собранном бинаре: флаг ищется до разбора
// команды, отдельный юнит на printVersion этого не доказывает.
func TestVersionFlagOnTheBinary(t *testing.T) {
	bin := buildBinary(t)
	out, err := exec.Command(bin, "--version").CombinedOutput()
	if err != nil {
		t.Fatalf("--version: %v\n%s", err, out)
	}
	if !formatVer.MatchString(string(out)) {
		t.Fatalf("строка версии не по формату: %q", string(out))
	}
}

// TestBinaryNoArgs на собранном бинаре: без команды exit 2 и usage на stderr,
// а не молчаливый ноль и не паника.
func TestBinaryNoArgs(t *testing.T) {
	bin := buildBinary(t)
	var stderr bytes.Buffer
	cmd := exec.Command(bin)
	cmd.Stderr = &stderr
	if err := cmd.Run(); err == nil {
		t.Fatal("ожидали ненулевой exit без аргументов, получили 0")
	} else if ee, ok := err.(*exec.ExitError); !ok || ee.ExitCode() != 2 {
		t.Fatalf("exit без аргументов: %v, хотели 2", err)
	}
	if !strings.Contains(stderr.String(), "cmdout:") {
		t.Errorf("usage не напечатан, stderr: %q", stderr.String())
	}
}

// TestBinaryShortOutput на собранном бинаре: ниже порога вывод отдаётся как
// есть, выжимка не строится. Обёртка прозрачна для короткой команды.
func TestBinaryShortOutput(t *testing.T) {
	bin := buildBinary(t)
	root := setupRepo(t)
	cmd := exec.Command(bin, "sh", "-c", "printf 'line %d\\n' 1 2 3")
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("cmdout короткий прогон: %v\n%s", err, out)
	}
	if string(out) != "line 1\nline 2\nline 3\n" {
		t.Errorf("короткий вывод не прошёл как есть: %q", string(out))
	}
}

// TestBinaryLongOutput на собранном бинаре: выше порога печатается выжимка в
// порядке полей LLD. Первая строка exit, последняя path, между ними
// significant и tail блоки.
func TestBinaryLongOutput(t *testing.T) {
	bin := buildBinary(t)
	root := setupRepo(t)
	// 150 строк, в строках 10 и 20 стоят FAIL и panic:.
	script := `n=1
while [ $n -le 150 ]; do
  case $n in
    10) echo "FAIL row_ten";;
    20) echo "panic: row_twenty";;
    *) echo "line $n";;
  esac
  n=$((n+1))
done`
	cmd := exec.Command(bin, "sh", "-c", script)
	cmd.Dir = root
	out, _ := cmd.CombinedOutput() // exit у sh ноль, ошибку не проверяем
	body := string(out)
	lines := strings.Split(strings.TrimRight(body, "\n"), "\n")
	if lines[0] != "exit: 0" {
		t.Errorf("первая строка: %q, хотели «exit: 0»", lines[0])
	}
	if !strings.Contains(body, "lines_total: 150") {
		t.Errorf("lines_total не 150: %q", body)
	}
	if !strings.Contains(body, "significant:") {
		t.Errorf("significant блок пропал: %q", body)
	}
	if !strings.Contains(body, "FAIL row_ten") || !strings.Contains(body, "panic: row_twenty") {
		t.Errorf("значимые строки не попали в выжимку: %q", body)
	}
	if !strings.Contains(body, "tail:") {
		t.Errorf("tail блок пропал: %q", body)
	}
	if !strings.Contains(body, "line 150") {
		t.Errorf("последняя строка вывода не в tail: %q", body)
	}
	if !strings.HasPrefix(lines[len(lines)-1], "path: ") {
		t.Errorf("последняя строка: %q, хотели «path: ...»", lines[len(lines)-1])
	}
	// Путь в выжимке обязан быть путём к существующему файлу внутри репо.
	var pathLine string
	for _, l := range lines {
		if strings.HasPrefix(l, "path: ") {
			pathLine = strings.TrimPrefix(l, "path: ")
			break
		}
	}
	if pathLine == "" {
		t.Fatal("path строка не найдена")
	}
	if info, err := os.Stat(pathLine); err != nil || info.Size() == 0 {
		t.Errorf("файл вывода по path не читается или пуст: %s (err=%v)", pathLine, err)
	}
}

// TestBinaryExitCodePassThrough: cmdout выходит с тем же кодом, что и команда.
// По коду обвязка отличает «прогон зелёный» от «упал».
func TestBinaryExitCodePassThrough(t *testing.T) {
	bin := buildBinary(t)
	root := setupRepo(t)
	cmd := exec.Command(bin, "sh", "-c", "exit 7")
	cmd.Dir = root
	if err := cmd.Run(); err == nil {
		t.Fatal("ожидали ненулевой exit, получили 0")
	} else if ee, ok := err.(*exec.ExitError); !ok || ee.ExitCode() != 7 {
		t.Fatalf("exit cmdout: %v, хотели 7", err)
	}
}
