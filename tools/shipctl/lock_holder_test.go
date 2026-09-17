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
// строка держателя пишется литералом. Так тест обходится символами, которые
// были в коде и до DK-122, и краснота на старом коде означает молчащий отказ,
// а не несобравшуюся базу.
func holdLock(t *testing.T, root, who string) func() {
	t.Helper()
	f, err := os.OpenFile(filepath.Join(root, lockPath), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	line := fmt.Sprintf("pid=%d\tкоманда=%s\tвзят=%s\n", os.Getpid(), who, time.Now().Format(time.RFC3339))
	if _, err := f.WriteAt([]byte(line), 0); err != nil {
		t.Fatal(err)
	}
	return func() { f.Close() }
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

// TestLockFileNamesLiveHolder: держателя в файл пишет сама команда, а не
// только ручной захват в тесте. Проверяется изнутри живого слияния: команда
// тестов снимает копию файла замка, пока merge его держит.
func TestLockFileNamesLiveHolder(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	devkitDir(t, root)
	branchWithFix(t, root)

	seen := filepath.Join(t.TempDir(), "seen")
	test := fmt.Sprintf("cat %s > %s", filepath.Join(root, lockPath), seen)
	if _, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: test}); err != nil {
		t.Fatalf("merge должен пройти: %v", err)
	}
	data, err := os.ReadFile(seen)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"команда=merge XR-001", fmt.Sprintf("pid=%d", os.Getpid()), "взят="} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("под живым слиянием в файле замка нет %q, а лежит %q", want, data)
		}
	}
	// Слияние кончилось, держателя за собой замок не оставил.
	rest, err := os.ReadFile(filepath.Join(root, lockPath))
	if err != nil {
		t.Fatal(err)
	}
	if len(rest) != 0 {
		t.Fatalf("после слияния файл замка должен быть пуст, а в нём %q", rest)
	}
}


