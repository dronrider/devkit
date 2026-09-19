package main

import (
	"os"
	"os/exec"
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

// buildSecretCLI собирает настоящий бинарь `dashboard` во временный каталог
// теста и гоняет его отдельным процессом. Проверка идёт по стандартному
// выводу самой команды, а не по внутренней функции: так тестовый файл не
// зовёт новых символов пакета, старый бинарь собирается им же самим, и
// regcheck доказывает регрессию на настоящем CLI, а не на своей же обвязке.
func buildSecretCLI(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "dashboard")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	cmd.Env = append(os.Environ(), "GOWORK=off")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build dashboard: %v\n%s", err, out)
	}
	return bin
}

// runSecretCLI зовёт `dashboard secret` с явным окружением: унаследованный
// CLAUDECODE вычищается, чтобы прогон из настоящей агентской сессии не
// подделал ветку теста сам собой, а HOME подменяется синтетическим конфигом.
// Пустой claudecode оставляет переменную не выставленной вовсе.
func runSecretCLI(t *testing.T, bin, home, claudecode string) (out string, code int) {
	t.Helper()
	var env []string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "CLAUDECODE=") {
			continue
		}
		env = append(env, kv)
	}
	env = append(env, "HOME="+home)
	if claudecode != "" {
		env = append(env, "CLAUDECODE="+claudecode)
	}
	cmd := exec.Command(bin, "secret")
	cmd.Env = env
	data, err := cmd.CombinedOutput()
	if err != nil {
		exitErr, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("dashboard secret не запустился: %v", err)
		}
		code = exitErr.ExitCode()
	}
	return string(data), code
}

// В агентской сессии (DK-1052) команда `secret` не должна отдать значение
// токена: оно едет в контекст модели и в транскрипт, а путь утечки закрывает
// именно отказ печати. Признак тот же, что у devkitctl doctor --fix
// (CLAUDECODE=1), isatty тут не годится: шаги человека зовут команду
// подстановкой `$(dashboard secret)`, и стандартный вывод там не терминал.
func TestSecretCLIAgenticSessionRefuses(t *testing.T) {
	bin := buildSecretCLI(t)
	home := t.TempDir()
	writeConf(t, home, "root = /x\ntoken = синтетический-токен-агента\n")
	out, code := runSecretCLI(t, bin, home, "1")
	if code == 0 {
		t.Fatalf("агентская сессия получила код 0, ждала отказ: %q", out)
	}
	if strings.Contains(out, "синтетический-токен-агента") {
		t.Fatalf("вывод агентской сессии несёт значение токена: %q", out)
	}
	if !strings.Contains(out, confPath(home)) {
		t.Fatalf("вывод не назвал файл конфига: %q", out)
	}
	if !strings.Contains(out, "token") {
		t.Fatalf("вывод не назвал ключ token: %q", out)
	}
	if !strings.Contains(out, "человек") {
		t.Fatalf("вывод не сказал, что команду зовёт человек: %q", out)
	}
}

// Вызов человека, в том числе через подстановку команды и конвейер, работает
// как раньше: без признака агентской сессии значение печатается.
func TestSecretCLIHumanPrintsToken(t *testing.T) {
	bin := buildSecretCLI(t)
	home := t.TempDir()
	writeConf(t, home, "root = /x\ntoken = синтетический-токен-человека\n")
	out, code := runSecretCLI(t, bin, home, "")
	if code != 0 {
		t.Fatalf("человеческий вызов получил код %d, ждал 0: %q", code, out)
	}
	if strings.TrimSpace(out) != "синтетический-токен-человека" {
		t.Fatalf("вывод %q не равен токену конфига", out)
	}
}

// CLAUDECODE со значением, отличным от "1", не должен считаться агентской
// сессией: признак строгий, как и у devkitctl doctor --fix.
func TestSecretCLIExactValue(t *testing.T) {
	bin := buildSecretCLI(t)
	home := t.TempDir()
	writeConf(t, home, "root = /x\ntoken = синтетический-токен-ноль\n")
	out, code := runSecretCLI(t, bin, home, "0")
	if code != 0 {
		t.Fatalf("CLAUDECODE=0 получил код %d, ждал 0 (не агентская сессия): %q", code, out)
	}
	if strings.TrimSpace(out) != "синтетический-токен-ноль" {
		t.Fatalf("вывод %q не равен токену конфига", out)
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
