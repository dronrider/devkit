package gitrun

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestEnvClosesPrompt: окружение вызова закрывает все вопросы, на которые в
// сессии агента некому ответить, и не теряет родительское.
func TestEnvClosesPrompt(t *testing.T) {
	t.Setenv("GIT_ASKPASS", "/usr/bin/true")
	t.Setenv("SSH_ASKPASS", "/usr/bin/true")
	env := Env([]string{"fetch"})
	for _, want := range []string{"GIT_TERMINAL_PROMPT=0", "SSH_ASKPASS_REQUIRE=never"} {
		if !slices.Contains(env, want) {
			t.Fatalf("нет %q: %v", want, env)
		}
	}
	for _, kv := range env {
		if strings.HasPrefix(kv, "GIT_ASKPASS=") || strings.HasPrefix(kv, "SSH_ASKPASS=") {
			t.Fatalf("помощник ввода пароля остался: %q", kv)
		}
	}
	if !slices.Contains(env, "PATH="+os.Getenv("PATH")) {
		t.Fatalf("родительское окружение потерялось: %v", env)
	}
	if slices.Contains(env, "DEVKIT_PUSH_OK=1") {
		t.Fatal("разрешение на пуш выдано не пушу")
	}
	if !slices.Contains(Env([]string{"push"}), "DEVKIT_PUSH_OK=1") {
		t.Fatal("пуш без разрешения для pre-push")
	}
}

// TestEnvKeepsSSHCommand: своя команда ssh остаётся, к ней приписывается
// пакетный режим. Затирать её нельзя, ей ходят в репозитории с особым ключом.
func TestEnvKeepsSSHCommand(t *testing.T) {
	t.Setenv("GIT_SSH_COMMAND", "ssh -i /keys/id_ed25519")
	var got string
	for _, kv := range Env([]string{"push"}) {
		if strings.HasPrefix(kv, "GIT_SSH_COMMAND=") {
			got = kv
		}
	}
	if got != "GIT_SSH_COMMAND=ssh -i /keys/id_ed25519 -o BatchMode=yes" {
		t.Fatalf("команда ssh собрана не так: %q", got)
	}
}

// TestTimeout: без переменной действует умолчание, кривое значение это громкая
// ошибка, а не молчаливый возврат к умолчанию.
func TestTimeout(t *testing.T) {
	t.Setenv(TimeoutEnv, "")
	if d, err := Timeout(); err != nil || d != DefaultTimeout {
		t.Fatalf("без переменной ждал %s: %s %v", DefaultTimeout, d, err)
	}
	t.Setenv(TimeoutEnv, "2m")
	if d, err := Timeout(); err != nil || d != 2*time.Minute {
		t.Fatalf("переменная не прочитана: %s %v", d, err)
	}
	for _, bad := range []string{"скоро", "30", "0s", "-5m"} {
		t.Setenv(TimeoutEnv, bad)
		if _, err := Timeout(); err == nil {
			t.Fatalf("кривой предел %q должен быть ошибкой", bad)
		}
	}
}

// TestRunLocal: локальная команда отдаёт вывод и не задета пределом, отказ
// git приезжает обёрнутым именем команды.
func TestRunLocal(t *testing.T) {
	root := initRepo(t)
	out, err := Run(root, []string{"rev-parse", "--abbrev-ref", "HEAD"}, time.Second)
	if err != nil || out == "" {
		t.Fatalf("локальная команда: %q %v", out, err)
	}
	if _, err := Run(root, []string{"cat-file", "-p", "неттакого"}, time.Second); err == nil ||
		!strings.Contains(err.Error(), "git cat-file") {
		t.Fatalf("отказ git не назван командой: %v", err)
	}
}

// TestPushHangingCredentialHelper: главный дефект DK-697. Помощник учётки,
// который ждёт человека, вешал пуш навсегда, коммиты копились локально, а
// конвейер вставал. Теперь пуш кончается отказом по пределу, отказ называет
// причину и ход руками, а спящий помощник умирает вместе с git.
func TestPushHangingCredentialHelper(t *testing.T) {
	root := initRepo(t)
	pidFile := filepath.Join(t.TempDir(), "helper.pid")
	helper := filepath.Join(t.TempDir(), "sleepy-helper.sh")
	write(t, helper, "#!/bin/sh\necho $$ > "+pidFile+"\nsleep 60\n")
	if err := os.Chmod(helper, 0o755); err != nil {
		t.Fatal(err)
	}

	// Сервер отвечает на запрос ссылок отказом с просьбой представиться:
	// ровно на этом ответе git и зовёт помощника учётки.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("WWW-Authenticate", `Basic realm="devkit"`)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	gitT(t, root, "remote", "add", "origin", srv.URL+"/repo.git")
	gitT(t, root, "config", "credential.helper", helper)

	type result struct {
		err error
	}
	done := make(chan result, 1)
	start := time.Now()
	go func() {
		_, err := Run(root, []string{"push", "origin", "HEAD:main"}, 2*time.Second)
		done <- result{err}
	}()
	select {
	case r := <-done:
		if r.err == nil {
			t.Fatal("пуш к отказавшему серверу должен провалиться")
		}
		for _, want := range []string{"не кончился за 2s", "учётк", "связку ключей", TimeoutEnv} {
			if !strings.Contains(r.err.Error(), want) {
				t.Fatalf("в отказе нет %q:\n%v", want, r.err)
			}
		}
	case <-time.After(20 * time.Second):
		t.Fatalf("пуш не вернулся за %s: предел времени не сработал", time.Since(start))
	}

	// Помощник это потомок git, и убивать надо группу: переживший предел
	// помощник держал бы диалог связки ключей открытым дальше.
	raw, rerr := os.ReadFile(pidFile)
	if rerr != nil {
		t.Fatalf("помощник учётки не был позван: %v", rerr)
	}
	pid, cerr := strconv.Atoi(strings.TrimSpace(string(raw)))
	if cerr != nil {
		t.Fatalf("pid помощника не разобран: %q", raw)
	}
	alive := func() bool { return syscall.Kill(pid, 0) == nil }
	for i := 0; i < 50 && alive(); i++ {
		time.Sleep(20 * time.Millisecond)
	}
	if alive() {
		t.Fatalf("помощник %d пережил предел", pid)
	}
}

func initRepo(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(home, "gitconfig"))
	t.Setenv("GIT_CONFIG_SYSTEM", filepath.Join(home, "gitconfig"))
	root := t.TempDir()
	gitT(t, root, "init", "-q", "-b", "main")
	gitT(t, root, "config", "user.email", "t@example.com")
	gitT(t, root, "config", "user.name", "t")
	write(t, filepath.Join(root, "a.txt"), "a\n")
	gitT(t, root, "add", "a.txt")
	gitT(t, root, "commit", "-qm", "init")
	return root
}

func gitT(t *testing.T, root string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v (%s)", args, err, out)
	}
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(fmt.Errorf("%s: %w", path, err))
	}
}
