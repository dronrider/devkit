package main

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Стенд каталога подъёма клиента (DK-1163). Клиент на старте обходит дерево
// своего рабочего каталога: поднятый в доме, он заходит в Рабочий стол,
// Документы и медиатеку, и macOS спрашивает разрешение на каждую такую папку у
// ответственного процесса, а под launchd это дашборд. Поднятый без каталога
// вовсе, он наследует рабочий каталог службы, то есть корень файловой системы.
// Каталог поэтому называется всегда, и он не дом.

// serviceDirOf это путь служебного каталога, посчитанный стендом самостоятельно.
// Звать тут internal/clientdir нельзя: правка и её мерка съезжали бы вместе.
func serviceDirOf(home string) string {
	return filepath.Join(home, ".devkit", "client")
}

// swapClientHome подставляет названный дом под служебный каталог подъёма
// клиента и возвращает прежний шов по концу теста. Зовёт его и общий стенд
// (newTestEnv), поэтому настоящий `~/.devkit/client` не заводит ни один прогон.
func swapClientHome(t *testing.T, home string) {
	t.Helper()
	old := clientHomeFn
	clientHomeFn = func(string) string { return home }
	t.Cleanup(func() { clientHomeFn = old })
}

// tempClientHome это то же на временном доме, для тестов без общего стенда.
func tempClientHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	swapClientHome(t, home)
	return home
}

// raiseDirCheck это мерка каталога подъёма: назван и не дом.
func raiseDirCheck(dir, home string) error {
	if strings.TrimSpace(dir) == "" {
		return errors.New("каталог подъёма клиента не назван")
	}
	if filepath.Clean(dir) == filepath.Clean(home) {
		return errors.New("каталог подъёма клиента это дом " + home)
	}
	return nil
}

// Вход поднимается в служебном пустом каталоге под домом, а не в самом доме.
// Живой случай DK-1163: клиент входа обходил дерево дома, заходя в Рабочий
// стол, Документы и медиатеку, macOS спрашивала разрешение на каждую папку у
// ответственного процесса (под launchd это дашборд), и очередь этих окон
// подозревается в падении WindowServer.
func TestClientLoginRaisesOutsideHome(t *testing.T) {
	e := newTestEnv(t)
	home := e.home
	d := fakeTmuxLogin(t, e)
	fastLoginWait(t, 2*time.Second)
	c := e.loggedClient(t)
	resp, text := loginPost(t, c, e.srv.URL, "/api/projects/demo/chats/login", "{}")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("подъём входа: %d, %s", resp.StatusCode, text)
	}
	raw, err := os.ReadFile(filepath.Join(d, "raise"))
	if err != nil {
		t.Fatalf("доводы подъёма не записались: %v", err)
	}
	args := strings.Fields(strings.TrimSpace(string(raw)))
	dir := ""
	for i, a := range args {
		if a == "-c" && i+1 < len(args) {
			dir = args[i+1]
		}
	}
	if err := raiseDirCheck(dir, home); err != nil {
		t.Fatalf("каталог подъёма входа не годится: %v (доводы %v)", err, args)
	}
	if want := serviceDirOf(home); dir != want {
		t.Fatalf("вход поднят в %q, ждали служебный каталог %q", dir, want)
	}
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		t.Fatalf("служебный каталог не заведён: %v", err)
	}
}

// Служебный запуск без каталога это отказ с причиной, а не наследование
// рабочего каталога демона. Под launchd у службы он равен корню файловой
// системы, и поднятый оттуда клиент обходил весь диск, упираясь в защищённые
// папки macOS диалогами доступа (DK-1163).
func TestRunProcQuietRefusesEmptyDir(t *testing.T) {
	_, err := runProcQuietAt(t.TempDir(), "", true, "echo", "привет")
	if err == nil {
		t.Fatal("служебный запуск без каталога прошёл")
	}
	if !strings.Contains(err.Error(), "не назван") {
		t.Fatalf("причина отказа не названа: %v", err)
	}
}

// Названный каталог остаётся рабочим у подпроцесса: транскрипт служебного
// вызова ложится туда же, куда его и заказали.
func TestRunProcQuietKeepsNamedDir(t *testing.T) {
	dir := t.TempDir()
	out, err := runProcQuietAt(t.TempDir(), dir, true, "pwd")
	if err != nil {
		t.Fatalf("служебный запуск отказал: %v", err)
	}
	got, err := filepath.EvalSymlinks(strings.TrimSpace(string(out)))
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("подпроцесс поднят в %q, заказывали %q", got, want)
	}
}

// Дороги, у которых каталог не заказывают, берут служебный: он заводится сам и
// пуст, обходить клиенту там нечего.
func TestRunProcHomeUsesServiceDir(t *testing.T) {
	home := tempClientHome(t)
	out, err := runProcHome("pwd")
	if err != nil {
		t.Fatalf("запуск под домом отказал: %v", err)
	}
	got, err := filepath.EvalSymlinks(strings.TrimSpace(string(out)))
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(serviceDirOf(home))
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("подпроцесс поднят в %q, ждали служебный каталог %q", got, want)
	}
	if err := raiseDirCheck(got, home); err != nil {
		t.Fatalf("каталог подъёма не годится: %v", err)
	}
}
