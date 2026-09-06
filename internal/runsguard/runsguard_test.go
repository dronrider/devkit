package runsguard

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGuardCleanRunKeepsCode(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "old.run"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	code := guard(dir, func() int { return 0 }, &out)
	if code != 0 {
		t.Fatalf("guard() при чистом прогоне вернул %d, ждал 0", code)
	}
	if out.Len() != 0 {
		t.Fatalf("guard() без новых файлов написал в вывод: %q", out.String())
	}
}

// TestGuardMissingDirIsClean: свежая машина без единого прогона .run-записей
// ~/.devkit/runs не заводила, и это не повод для находки.
func TestGuardMissingDirIsClean(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "нет-такого")
	var out bytes.Buffer
	code := guard(dir, func() int { return 0 }, &out)
	if code != 0 || out.Len() != 0 {
		t.Fatalf("guard() на отсутствующем каталоге: код %d, вывод %q", code, out.String())
	}
}

// TestGuardNewFileFailsGreenRun: регрессия DK-818 в миниатюре. Прогон сам по
// себе зелёный (run возвращает 0), но пишет файл мимо подмены HOME, и это
// должно завалить Guard, а не проехать вместе с зелёным кодом.
func TestGuardNewFileFailsGreenRun(t *testing.T) {
	dir := t.TempDir()
	var out bytes.Buffer
	code := guard(dir, func() int {
		if err := os.WriteFile(filepath.Join(dir, "XR-004-var-folders.run"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		return 0
	}, &out)
	if code != 1 {
		t.Fatalf("guard() при новом файле вернул %d, ждал 1", code)
	}
	if !strings.Contains(out.String(), "XR-004-var-folders.run") {
		t.Fatalf("guard() не назвал новый файл в сообщении: %q", out.String())
	}
	if !strings.Contains(out.String(), dir) {
		t.Fatalf("guard() не назвал каталог в сообщении: %q", out.String())
	}
}

// TestGuardNewFileKeepsFailingCode: прогон уже красный сам по себе (упавший
// тест), и утечка в реальный дом не должна маскировать код провала числом 1
// вместо настоящего кода ошибки теста.
func TestGuardNewFileKeepsFailingCode(t *testing.T) {
	dir := t.TempDir()
	var out bytes.Buffer
	code := guard(dir, func() int {
		if err := os.WriteFile(filepath.Join(dir, "leak.run"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		return 2
	}, &out)
	if code != 2 {
		t.Fatalf("guard() при уже красном прогоне вернул %d, ждал сохранить 2", code)
	}
	if out.Len() == 0 {
		t.Fatal("guard() не сообщил об утечке при уже красном прогоне")
	}
}

// TestGuardOnlyCountsNewNames: файлы, лежавшие в каталоге до прогона, не
// новость, даже если их много.
func TestGuardOnlyCountsNewNames(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"a.run", "b.run", "c.run"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var out bytes.Buffer
	code := guard(dir, func() int { return 0 }, &out)
	if code != 0 || out.Len() != 0 {
		t.Fatalf("guard() пометил старые файлы новыми: код %d, вывод %q", code, out.String())
	}
}
