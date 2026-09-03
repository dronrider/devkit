package reviewnote

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// repo заводит временный репозиторий с одним коммитом: заметка ложится на
// коммит, поэтому пустого дерева тут мало.
func repo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	run(t, root, "init", "-q", "-b", "main")
	run(t, root, "config", "user.email", "test@test")
	run(t, root, "config", "user.name", "test")
	commit(t, root, "code.txt", "первый\n", "feat: первая правка")
	return root
}

func run(t *testing.T, root string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func commit(t *testing.T, root, name, body, subject string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, root, "add", ".")
	run(t, root, "commit", "-qm", subject)
}

func TestWriteAndRead(t *testing.T) {
	root := repo(t)
	line := LevelLine(2, "a1b2c3d", "неопределённость 1, тронут разбор диффа")
	if err := Write(root, "HEAD", line); err != nil {
		t.Fatal(err)
	}
	got, err := Read(root, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if got != line {
		t.Fatalf("заметка прочиталась не той: %q, ждали %q", got, line)
	}
	has, err := Has(root, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if !has {
		t.Fatal("след ревью на HEAD не найден, хотя заметка записана")
	}
}

// TestWriteReplaces: повторная запись переписывает заметку, а не кладёт вторую
// строку рядом. Пересмотр уровня по ходу ревью идёт этим же путём.
func TestWriteReplaces(t *testing.T) {
	root := repo(t)
	if err := Write(root, "HEAD", LevelLine(1, "a1b2c3d", "рутина")); err != nil {
		t.Fatal(err)
	}
	second := LevelLine(3, "a1b2c3d", "тронуты доступы")
	if err := Write(root, "HEAD", second); err != nil {
		t.Fatal(err)
	}
	got, err := Read(root, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if got != second {
		t.Fatalf("повтор не переписал заметку: %q", got)
	}
	if strings.Contains(got, "рутина") {
		t.Fatalf("прежняя строка осталась в заметке: %q", got)
	}
}

// TestReadWithoutNote: у коммита без заметки чтение отдаёт пусто и не ошибку,
// а Has говорит «следа нет».
func TestReadWithoutNote(t *testing.T) {
	root := repo(t)
	got, err := Read(root, "HEAD")
	if err != nil {
		t.Fatalf("коммит без заметки читается без ошибки: %v", err)
	}
	if got != "" {
		t.Fatalf("у коммита без заметки прочиталось %q", got)
	}
	has, err := Has(root, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if has {
		t.Fatal("след ревью нашёлся там, где заметки нет")
	}
}

// TestForeignNoteIsNotTrace: заметка с чужим текстом следом ревью не считается,
// иначе за ревью сошла бы любая приписка к коммиту.
func TestForeignNoteIsNotTrace(t *testing.T) {
	root := repo(t)
	if err := Write(root, "HEAD", "замер сборки: 12 секунд"); err != nil {
		t.Fatal(err)
	}
	has, err := Has(root, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if has {
		t.Fatal("чужая заметка сошла за след ревью")
	}
}

// TestNoteOnAncestorIsNotTraceOfHead: заметка на прошлом коммите след с HEAD не
// снимает. Ровно этим ворот пуша и отличает отревьюенный код от дописанного
// после ревью.
func TestNoteOnAncestorIsNotTraceOfHead(t *testing.T) {
	root := repo(t)
	if err := Write(root, "HEAD", LevelLine(2, "a1b2c3d", "разбор диффа")); err != nil {
		t.Fatal(err)
	}
	commit(t, root, "code.txt", "второй\n", "feat: правка после ревью")
	has, err := Has(root, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if has {
		t.Fatal("заметка предка сошла за след ревью на HEAD")
	}
}

// TestNextRoundCountsAncestors: круг после правок ложится на новый коммит, и
// счёт продолжается по заметкам предков, а не начинается заново.
func TestNextRoundCountsAncestors(t *testing.T) {
	root := repo(t)
	first, err := NextRound(root, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if first != 1 {
		t.Fatalf("первый круг на чистой истории посчитан как %d", first)
	}
	if err := Write(root, "HEAD", RoundLine(first, "a1b2c3d", "путь от симптома пройден")); err != nil {
		t.Fatal(err)
	}
	commit(t, root, "code.txt", "второй\n", "fix: правки по замечаниям")
	second, err := NextRound(root, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if second != 2 {
		t.Fatalf("круг после правок посчитан как %d, ждали 2", second)
	}
}

// TestOutsideGitRefused: вне git-дерева запись отказывает, а не молчит. Тихий
// провал тут значил бы ревью без следа при уверенности, что след поставлен.
func TestOutsideGitRefused(t *testing.T) {
	dir := t.TempDir()
	if err := Write(dir, "HEAD", LevelLine(1, "a1b2c3d", "рутина")); err == nil {
		t.Fatal("запись заметки вне git-дерева должна отказывать")
	}
	if _, err := Read(dir, "HEAD"); err == nil {
		t.Fatal("чтение заметки вне git-дерева должно отказывать")
	}
}

func TestTraceLines(t *testing.T) {
	yes := []string{
		"Уровень 2 до a1b2c3d: неопределённость 1",
		"Круг 1 до a1b2c3d: без замечаний",
		"Круг 12 до a1b2c3d: без замечаний",
	}
	for _, ln := range yes {
		if !IsTrace(ln) {
			t.Fatalf("след ревью не узнан: %q", ln)
		}
	}
	no := []string{"", "круг первый прошёл без замечаний", "Кругом ошибка", "замер сборки"}
	for _, ln := range no {
		if IsTrace(ln) {
			t.Fatalf("за след ревью принята чужая строка: %q", ln)
		}
	}
}
