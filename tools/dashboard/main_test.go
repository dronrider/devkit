package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPrintVersion(t *testing.T) {
	var sb strings.Builder
	if !printVersion([]string{"serve", "--version"}, &sb) {
		t.Fatal("--version не распознан")
	}
	if got := sb.String(); got != "dashboard dev (unknown)\n" {
		t.Fatalf("строка версии %q не в формате утилит devkit", got)
	}
	if printVersion([]string{"serve"}, &sb) {
		t.Fatal("--version увиделся там, где его нет")
	}
	if printVersion([]string{"--", "--version"}, &sb) {
		t.Fatal("--version за «--» принадлежит чужой нагрузке")
	}
}

// Замена файла на месте меняет иноду, перезапись содержимого нет: на этой
// разнице держится самонаблюдение под выкат (go build кладёт бинарь заново).
func TestInodeOf(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bin")
	if err := os.WriteFile(path, []byte("v1"), 0o755); err != nil {
		t.Fatal(err)
	}
	first, ok := inodeOf(path)
	if !ok {
		t.Fatal("инода не прочиталась")
	}
	if err := os.WriteFile(path, []byte("v2"), 0o755); err != nil {
		t.Fatal(err)
	}
	same, ok := inodeOf(path)
	if !ok || same != first {
		t.Fatalf("перезапись содержимого не должна менять иноду: %d -> %d", first, same)
	}
	staged := filepath.Join(dir, "bin.new")
	if err := os.WriteFile(staged, []byte("v3"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(staged, path); err != nil {
		t.Fatal(err)
	}
	replaced, ok := inodeOf(path)
	if !ok || replaced == first {
		t.Fatal("замена файла на месте обязана дать другую иноду")
	}
	if _, ok := inodeOf(filepath.Join(dir, "нет-такого")); ok {
		t.Fatal("пропавший файл не должен отдавать иноду")
	}
}

// Сервер собирается с пределом на чтение заголовков: без него молчащие
// соединения висят вечно, а слушает демон все интерфейсы.
func TestHTTPServerLimits(t *testing.T) {
	s := httpServer(nil)
	if s.ReadHeaderTimeout <= 0 {
		t.Fatal("ReadHeaderTimeout не поставлен: молчащее соединение повиснет навсегда")
	}
	if s.WriteTimeout != 0 {
		t.Fatal("WriteTimeout должен остаться нулевым: дальше по серии едут SSE-потоки")
	}
}

// watchBinary не срабатывает, пока инода на месте, и останавливается по stop.
func TestWatchBinaryQuietStop(t *testing.T) {
	fired := make(chan struct{}, 1)
	stop := watchBinary(10*time.Millisecond, func() { fired <- struct{}{} })
	select {
	case <-fired:
		t.Fatal("бинарь никто не менял, а наблюдатель сработал")
	case <-time.After(50 * time.Millisecond):
	}
	stop()
}
