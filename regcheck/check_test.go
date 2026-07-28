package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitT(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// setupRepo делает репозиторий с багом в code.txt: строка «bug» вместо «fixed».
func setupRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	gitT(t, root, "init", "-q", "-b", "main")
	gitT(t, root, "config", "user.email", "test@test")
	gitT(t, root, "config", "user.name", "test")
	write(t, root, "code.txt", "bug\n")
	gitT(t, root, "add", ".")
	gitT(t, root, "commit", "-qm", "seed")
	return root
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

// TestRunLog: журнал пишется в .devkit/log репозитория, без .devkit не
// заводится.
func TestRunLog(t *testing.T) {
	root := setupRepo(t)
	logRun(root, 0)
	if _, err := os.Stat(filepath.Join(root, ".devkit", "log")); err == nil {
		t.Fatal("журнал не должен заводиться без .devkit")
	}
	if err := os.Mkdir(filepath.Join(root, ".devkit"), 0o755); err != nil {
		t.Fatal(err)
	}
	logRun(root, 1)
	data, err := os.ReadFile(filepath.Join(root, ".devkit", "log"))
	if err != nil {
		t.Fatalf("журнал не записан: %v", err)
	}
	if !strings.Contains(string(data), "\tregcheck\trun\t1\n") {
		t.Fatalf("строки журнала: %q", data)
	}
}

// Пробный «тест» это shell-скрипт с grep, чтобы прогоны не зависели от
// тулчейнов: на новом коде grep находит «fixed», на старом падает.
const probe = "grep -q fixed code.txt\n"

func TestCatchesRegression(t *testing.T) {
	root := setupRepo(t)
	write(t, root, "code.txt", "fixed\n")
	write(t, root, "probe_test.sh", probe)
	msg, err := Run(Params{Dir: root, Cmd: []string{"sh", "probe_test.sh"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "краснеет") || !strings.Contains(msg, "probe_test.sh") {
		t.Fatalf("сообщение: %q", msg)
	}
	if wt := gitT(t, root, "worktree", "list"); strings.Contains(wt, "\n") {
		t.Fatalf("worktree не убран:\n%s", wt)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "code.txt")); string(got) != "fixed\n" {
		t.Fatalf("рабочее дерево тронуто: %q", got)
	}
}

func TestUselessTestFails(t *testing.T) {
	root := setupRepo(t)
	write(t, root, "code.txt", "fixed\n")
	// test -f зелёный что с багом, что без, такое покрытием не считается.
	write(t, root, "probe_test.sh", "test -f code.txt\n")
	_, err := Run(Params{Dir: root, Cmd: []string{"sh", "probe_test.sh"}})
	if err == nil || !strings.Contains(err.Error(), "зелёный и на старом") {
		t.Fatalf("ожидал находку про зелёный тест, получил: %v", err)
	}
}

func TestFailingOnNewCode(t *testing.T) {
	root := setupRepo(t)
	write(t, root, "code.txt", "fixed\n")
	write(t, root, "probe_test.sh", "grep -q nothing code.txt\n")
	_, err := Run(Params{Dir: root, Cmd: []string{"sh", "probe_test.sh"}})
	if err == nil || !strings.Contains(err.Error(), "не проходит на текущем") {
		t.Fatalf("ожидал ошибку про красный тест, получил: %v", err)
	}
}

func TestCommittedFixAgainstMain(t *testing.T) {
	root := setupRepo(t)
	gitT(t, root, "checkout", "-qb", "xr-001-fix")
	write(t, root, "code.txt", "fixed\n")
	write(t, root, "probe_test.sh", probe)
	gitT(t, root, "add", ".")
	gitT(t, root, "commit", "-qm", "fix: XR-001 чиним баг")
	msg, err := Run(Params{Dir: root, Base: "main", Cmd: []string{"sh", "probe_test.sh"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "краснеет на main") {
		t.Fatalf("сообщение: %q", msg)
	}
}

func TestNoTestsFound(t *testing.T) {
	root := setupRepo(t)
	write(t, root, "code.txt", "fixed\n")
	_, err := Run(Params{Dir: root, Cmd: []string{"true"}})
	if err == nil || !strings.Contains(err.Error(), "не нашёл изменённых тестов") {
		t.Fatalf("ожидал ошибку про отсутствие тестов, получил: %v", err)
	}
}

func TestExplicitTestsFlag(t *testing.T) {
	root := setupRepo(t)
	write(t, root, "code.txt", "fixed\n")
	// Имя probe.sh автопоиском не ловится, файл передаётся флагом.
	write(t, root, "probe.sh", probe)
	msg, err := Run(Params{Dir: root, Tests: []string{"probe.sh"}, Cmd: []string{"sh", "probe.sh"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "probe.sh") {
		t.Fatalf("сообщение: %q", msg)
	}
}

func TestMissingCommand(t *testing.T) {
	root := setupRepo(t)
	if _, err := Run(Params{Dir: root}); err == nil {
		t.Fatal("без команды теста должно падать")
	}
}

// setupInlineRepo делает репозиторий с багом в lib.rs, тестового блока
// в базовой версии ещё нет: он появится вместе с правкой.
func setupInlineRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	gitT(t, root, "init", "-q", "-b", "main")
	gitT(t, root, "config", "user.email", "test@test")
	gitT(t, root, "config", "user.name", "test")
	write(t, root, "lib.rs", "const CODE: &str = \"bug\";\n")
	gitT(t, root, "add", ".")
	gitT(t, root, "commit", "-qm", "seed")
	return root
}

func TestInlineCatchesRegression(t *testing.T) {
	root := setupInlineRepo(t)
	write(t, root, "lib.rs", "const CODE: &str = \"fixed\";\n\n#[cfg(test)]\nmod tests {\n    // проверяет CODE\n}\n")
	write(t, root, "probe_test.sh", "grep -q fixed lib.rs\n")
	msg, err := Run(Params{Dir: root, Inline: []string{"lib.rs"}, Cmd: []string{"sh", "probe_test.sh"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "краснеет") || !strings.Contains(msg, "lib.rs") {
		t.Fatalf("сообщение: %q", msg)
	}
}

func TestInlineMarkers(t *testing.T) {
	root := setupInlineRepo(t)
	write(t, root, "lib.rs", "const CODE: &str = \"fixed\";\n\n"+
		"# regcheck:test-begin\n# проверка CODE\n# regcheck:test-end\n")
	write(t, root, "probe_test.sh", "grep -q fixed lib.rs\n")
	msg, err := Run(Params{Dir: root, Inline: []string{"lib.rs"}, Cmd: []string{"sh", "probe_test.sh"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "краснеет") {
		t.Fatalf("сообщение: %q", msg)
	}
}

func TestInlineNoRegionFails(t *testing.T) {
	root := setupInlineRepo(t)
	// в файле нет ни маркеров, ни #[cfg(test)]: отделить тест от правки нечем.
	write(t, root, "lib.rs", "const CODE: &str = \"fixed\";\n")
	write(t, root, "probe_test.sh", "grep -q fixed lib.rs\n")
	_, err := Run(Params{Dir: root, Inline: []string{"lib.rs"}, Cmd: []string{"sh", "probe_test.sh"}})
	if err == nil || !strings.Contains(err.Error(), "не нашёл тестовый регион") {
		t.Fatalf("ожидал ошибку про ненайденный регион, получил: %v", err)
	}
}

func TestInlineFalseGreen(t *testing.T) {
	root := setupInlineRepo(t)
	write(t, root, "lib.rs", "const CODE: &str = \"fixed\";\n\n"+
		"#[cfg(test)]\nmod tests {\n    // не проверяет CODE вообще\n}\n")
	// тест смотрит на сам факт наличия тестового блока, а не на CODE, поэтому
	// зелёный что с багом, что без.
	write(t, root, "probe_test.sh", "grep -q cfg lib.rs\n")
	_, err := Run(Params{Dir: root, Inline: []string{"lib.rs"}, Cmd: []string{"sh", "probe_test.sh"}})
	if err == nil || !strings.Contains(err.Error(), "зелёный и на старом") {
		t.Fatalf("ожидал находку про зелёный тест, получил: %v", err)
	}
}

func TestFindTestRegionRust(t *testing.T) {
	lines := splitLines("fn f(){}\n\n#[cfg(test)]\nmod tests {\n    fn g(){}\n}\n")
	start, end, err := findTestRegion(lines)
	if err != nil {
		t.Fatal(err)
	}
	if start != 3 || end != 6 {
		t.Fatalf("границы %d-%d", start, end)
	}
}

func TestFindTestRegionAmbiguous(t *testing.T) {
	lines := splitLines("#[cfg(test)]\nmod a {}\n#[cfg(test)]\nmod b {}\n")
	if _, _, err := findTestRegion(lines); err == nil {
		t.Fatal("ожидал ошибку про несколько блоков")
	}
}

func TestFindTestRegionMarkers(t *testing.T) {
	lines := splitLines("code\n// regcheck:test-begin\ntest\n// regcheck:test-end\nmore\n")
	start, end, err := findTestRegion(lines)
	if err != nil {
		t.Fatal(err)
	}
	if start != 2 || end != 4 {
		t.Fatalf("границы %d-%d", start, end)
	}
}

func TestFindTestRegionNone(t *testing.T) {
	lines := splitLines("just code\nno tests here\n")
	if _, _, err := findTestRegion(lines); err != errNoTestRegion {
		t.Fatalf("ожидал errNoTestRegion, получил %v", err)
	}
}

func TestSpliceInlineAppendsWhenBaseHasNoRegion(t *testing.T) {
	base := "const X: i32 = 1;\n"
	cur := "const X: i32 = 2;\n\n#[cfg(test)]\nmod tests {\n    fn t(){}\n}\n"
	got, err := spliceInline(base, cur)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "const X: i32 = 1;") || !strings.Contains(got, "mod tests") {
		t.Fatalf("итог: %q", got)
	}
	if strings.Contains(got, "const X: i32 = 2;") {
		t.Fatalf("исправление просочилось в базу: %q", got)
	}
}

func TestSpliceInlineReplacesExistingRegion(t *testing.T) {
	base := "const X: i32 = 1;\n\n#[cfg(test)]\nmod tests {\n    fn old(){}\n}\n"
	cur := "const X: i32 = 2;\n\n#[cfg(test)]\nmod tests {\n    fn newt(){}\n}\n"
	got, err := spliceInline(base, cur)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "const X: i32 = 2;") || strings.Contains(got, "fn old") || !strings.Contains(got, "fn newt") {
		t.Fatalf("итог: %q", got)
	}
}

func TestIsTestPath(t *testing.T) {
	yes := []string{
		"pkg/board_test.go", "tests/api.py", "test_ops.py", "src/app.spec.ts",
		"web/__tests__/form.js", "a/b/testdata/fixture.json", "lib/util.test.js",
	}
	no := []string{"pkg/board.go", "main.py", "docs/testing.md", "protest.go"}
	for _, p := range yes {
		if !isTestPath(p) {
			t.Errorf("%s должен считаться тестовым", p)
		}
	}
	for _, p := range no {
		if isTestPath(p) {
			t.Errorf("%s не должен считаться тестовым", p)
		}
	}
}
