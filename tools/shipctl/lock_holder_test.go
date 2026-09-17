package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// holdLock занимает замок конвейера мимо acquireLock: flock берётся руками, а
// строка держателя пишется литералом в соседний файл. Так тест обходится
// символами, которые были в коде и до DK-122, и краснота на старом коде
// означает молчащий отказ, а не несобравшуюся базу.
func holdLock(t *testing.T, root, who string) func() {
	t.Helper()
	f, err := os.OpenFile(filepath.Join(root, lockPath), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	owner := filepath.Join(root, lockPath+".owner")
	line := fmt.Sprintf("pid=%d\tкоманда=%s\tвзят=%s\n", os.Getpid(), who, time.Now().Format(time.RFC3339))
	if err := os.WriteFile(owner, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	return func() {
		os.Remove(owner)
		f.Close()
	}
}

// TestLockRefusalNamesHolderToCommands: держателя видит не сам захват, а
// команда конвейера, которой отказали. Ради этого отказа задача и заводилась:
// сессия получает «кто и с какого времени» из самой утилиты.
func TestLockRefusalNamesHolderToCommands(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	devkitDir(t, root)
	branchWithFix(t, root)

	defer holdLock(t, root, "start XR-002")()

	_, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true"})
	if err == nil {
		t.Fatal("merge при занятом замке должен отказать")
	}
	for _, want := range []string{"start XR-002", fmt.Sprintf("pid %d", os.Getpid())} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("отказ merge не назвал держателя (нет %q): %v", want, err)
		}
	}
}

// TestLockFileNamesLiveHolder: держателя пишет сама команда, а не только
// ручной захват в тесте. Проверяется изнутри живого слияния: команда тестов
// снимает копию файла держателя, пока merge держит замок.
func TestLockFileNamesLiveHolder(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	devkitDir(t, root)
	branchWithFix(t, root)

	seen := filepath.Join(t.TempDir(), "seen")
	test := fmt.Sprintf("cat %s > %s", filepath.Join(root, lockPath+".owner"), seen)
	if _, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: test}); err != nil {
		t.Fatalf("merge должен пройти: %v", err)
	}
	data, err := os.ReadFile(seen)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"команда=merge XR-001", fmt.Sprintf("pid=%d", os.Getpid()), "взят="} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("под живым слиянием в файле держателя нет %q, а лежит %q", want, data)
		}
	}
	// Слияние кончилось, файла держателя за собой не осталось.
	if _, err := os.Stat(filepath.Join(root, lockPath+".owner")); !os.IsNotExist(err) {
		t.Fatalf("после слияния файл держателя должен быть убран: %v", err)
	}
	// Сам замок остаётся на месте пустым, как было до DK-122.
	rest, err := os.ReadFile(filepath.Join(root, lockPath))
	if err != nil {
		t.Fatal(err)
	}
	if len(rest) != 0 {
		t.Fatalf("файл замка должен оставаться пустым, а в нём %q", rest)
	}
}



// trackLock кладёт файл замка под git: так выглядит проект, где .devkit не
// попал в .gitignore (старая раскладка до DK-278, фикстуры тестов).
func trackLock(t *testing.T, root string) {
	t.Helper()
	devkitDir(t, root)
	write(t, root, lockPath, "")
	gitT(t, root, "add", "-f", lockPath)
	gitT(t, root, "commit", "-qm", "chore: замок под git")
}

// TestTrackedLockNotDirtyTree: пока команда держит замок, отслеживаемый git-ом
// файл замка чистоту дерева не ломает. Держателя видно в соседнем файле, а сам
// замок остаётся пустым. Первая правка DK-122 писала держателя прямо в замок,
// и ship с merge отбивали сами себя: у ship падала проверка чистоты, а merge
// не мог сделать checkout поверх изменённого файла.
func TestTrackedLockNotDirtyTree(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	trackLock(t, root)

	defer holdLock(t, root, "merge XR-009")()

	if err := requireClean(root); err != nil {
		t.Fatalf("замок с держателем не должен считаться грязью дерева: %v", err)
	}
	if err := requireCleanScoped(root, []string{lockPath, "code.txt"}); err != nil {
		t.Fatalf("замок с держателем не должен считаться грязью по файлам задачи: %v", err)
	}

	// Настоящая грязь по-прежнему отбивает: оговорка снимает один файл, а не
	// всю проверку.
	write(t, root, "code.txt", "правка без коммита\n")
	if err := requireClean(root); err == nil {
		t.Fatal("незакоммиченная правка должна отбивать")
	}
	if err := requireCleanScoped(root, []string{"code.txt"}); err == nil {
		t.Fatal("незакоммиченная правка по файлам задачи должна отбивать")
	}
}

// TestMergeSurvivesTrackedLock: слияние проходит, когда файл замка попал и под
// git, и в пути самого слияния. Ровно так сложилось в фикстурах: первый merge
// создал файл, а git add . в ветке увёз его в коммит задачи.
func TestMergeSurvivesTrackedLock(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	trackLock(t, root)

	gitT(t, root, "checkout", "-qb", "xr-001-fix")
	write(t, root, "code.txt", "new\n")
	write(t, root, "fix_test.go", "package main\n")
	write(t, root, lockPath, "мусор прошлого запуска\n")
	gitT(t, root, "add", "-A")
	gitT(t, root, "commit", "-qm", "fix: XR-001 правка")

	if _, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true"}); err != nil {
		t.Fatalf("merge не должен отбиваться собственным замком: %v", err)
	}
}
