package trace

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestDictionary: словарь знает все свои слова и семейство stage:<вид>, а
// опечатку не пропускает: этап с чужим словом выглядел бы закрытым без
// проверяющего.
func TestDictionary(t *testing.T) {
	for _, w := range Words() {
		if !Known(w) {
			t.Fatalf("слово словаря %q не узнаётся", w)
		}
	}
	if !Known(StageWord("разработка")) || StageKind(StageWord("разработка")) != "разработка" {
		t.Fatal("семейство stage не узнаётся")
	}
	if Known("stage:чужой") {
		t.Fatal("незнакомый вид этапа прошёл словарь")
	}
	for _, w := range []string{"", "нетакого", "тесты"} {
		if Known(w) {
			t.Fatalf("чужое слово %q прошло словарь", w)
		}
	}
}

// TestTaskBound: следы, живущие в файле задачи, у сессии без задачи закрыты
// словом, остальные читаются и без задачи.
func TestTaskBound(t *testing.T) {
	for _, w := range []string{"stand", "scenario", "rehearsal", "verify", "stage:разработка"} {
		if !TaskBound(w) {
			t.Fatalf("след %q не назван живущим в файле задачи", w)
		}
	}
	for _, w := range []string{"tests", "regcheck", "review", "merged", "слово"} {
		if TaskBound(w) {
			t.Fatalf("след %q назван живущим в файле задачи", w)
		}
	}
}

// TestCheckByWord: слово «слово» закрывает этап на слове агента, а след,
// живущий в файле задачи, у сессии без задачи закрывается словом с печатью
// об этом.
func TestCheckByWord(t *testing.T) {
	if v, why := Check(WordWord, Ctx{}); v != ByWord || why != "на слове агента" {
		t.Fatalf("слово не закрылось словом: %v %q", v, why)
	}
	if v, why := Check(StageWord("разработка"), Ctx{}); v != ByWord ||
		why != "след недоступен без задачи, закрыто словом" {
		t.Fatalf("TaskBound-след без задачи не закрылся словом: %v %q", v, why)
	}
}

// TestCheckReview: review без строки уровня отказывает, со строкой проходит,
// пометка-исключение гасит след. Читается тот же разбор, что у ворот слияния.
func TestCheckReview(t *testing.T) {
	if v, why := Check(WordReview, Ctx{ID: "XR-1"}); v != Lack ||
		!strings.Contains(why, "нет строки уровня ревью") {
		t.Fatalf("review без раздела прошёл: %v %q", v, why)
	}
	doc := "## Ревью\n\nУровень 2 до a1b2c3d: тронут internal\n\n- замечание: исправлено\n"
	if v, why := Check(WordReview, Ctx{ID: "XR-1", Doc: doc}); v != Have || why != "" {
		t.Fatalf("review с уровнем не прошёл: %v %q", v, why)
	}
	doc = "- Исключение: ревью (бескодовая правка)\n"
	if v, _ := Check(WordReview, Ctx{ID: "XR-1", Doc: doc}); v != Have {
		t.Fatal("пометка-исключение не погасила review")
	}
}

// TestRegcheckLogged: зелёный прогон после границы считается, красный и чужая
// команда нет. Строку пишет regcheck своим форматом, ворота читают ту же.
func TestRegcheckLogged(t *testing.T) {
	path := filepath.Join(t.TempDir(), "log")
	text := "2026-10-24T09:00:00\tregcheck\trun\t1\tотворот\n" +
		"2026-10-24T10:00:00\tregcheck\trun\t0\tчисто\n" +
		"2026-10-24T10:30:00\tagentctl\tплан\t0\tчисто\n"
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	if !regcheckLogged(path, time.Date(2026, 10, 24, 9, 30, 0, 0, time.Local)) {
		t.Fatal("зелёный прогон после границы не найден")
	}
	if regcheckLogged(path, time.Date(2026, 10, 24, 11, 0, 0, 0, time.Local)) {
		t.Fatal("прогон старее границы посчитан")
	}
	if regcheckLogged(filepath.Join(t.TempDir(), "нет"), time.Time{}) {
		t.Fatal("чтение пустого журнала прошло успехом")
	}
}

// gitRepo ставит свежий репозиторий с коммитом в main и именованной веткой:
// PathsTouched читает дифф между ними.
func gitRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		args = append([]string{"-C", root}, args...)
		cmd := exec.Command("git", args...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(root, "code.go"), []byte("package x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-q", "-m", "старт")
	run("checkout", "-q", "-b", "dk-x")
	if err := os.MkdirAll(filepath.Join(root, "docs", "tasks"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "docs", "tasks", "DK-X.md"), []byte("текст"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-q", "-m", "дока")
	return root
}

// TestPathsTouched: условие этапа по путям спрашивает дифф ветки, а без ветки
// и без масок ответа нет, и этап спрашивает след дальше.
func TestPathsTouched(t *testing.T) {
	root := gitRepo(t)
	if touched, known := PathsTouched(root, "main", "dk-x", nil); touched || known {
		t.Fatalf("пустые маски ответили: %v %v", touched, known)
	}
	if touched, known := PathsTouched(root, "main", "dk-x", []string{"docs/**"}); !touched || !known {
		t.Fatalf("задетая маска не нашлась: %v %v", touched, known)
	}
	if touched, known := PathsTouched(root, "main", "dk-x", []string{"tools/**"}); touched || !known {
		t.Fatalf("незадетая маска названа задетой: %v %v", touched, known)
	}
	if _, known := PathsTouched(root, "main", "нет-ветки", []string{"docs/**"}); known {
		t.Fatal("чужая ветка дала ответ")
	}
}

// TestPathMatch: звезда не переходит через косую черту, двойная покрывает
// каталоги, включая ноль их.
func TestPathMatch(t *testing.T) {
	cases := []struct {
		pattern, name string
		want          bool
	}{
		{"*.md", "README.md", true},
		{"*.md", "docs/README.md", false},
		{"docs/**", "docs/tasks/DK-1.md", true},
		{"docs/**", "docs/DK-1.md", true},
		{"**/*.md", "docs/tasks/DK-1.md", true},
		{"**/*.md", "DK-1.md", true},
		{"a/*/c", "a/b/c", true},
		{"a/*/c", "a/b/d/c", false},
		{"**", "любой/путь/тут", true},
		{"**/obey*", "tools/obey.md", true},
		{"**/obey*", "obey.md", true},
		{"**/obey*", "tools/obeycheck/main.go", false},
	}
	for _, c := range cases {
		if got := pathMatch(c.pattern, c.name); got != c.want {
			t.Fatalf("pathMatch(%q, %q) = %v, жду %v", c.pattern, c.name, got, c.want)
		}
	}
}
