package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeConf(t *testing.T, home, text string) string {
	t.Helper()
	path := confPath(home)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadConfig(t *testing.T) {
	home := t.TempDir()
	writeConf(t, home, "# комментарий\nroot = ~/projects\nroot = /srv/work\naddr = 127.0.0.1\nport = 7200\ntoken = abc\n")
	cfg, err := LoadConfig(home)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{filepath.Join(home, "projects"), "/srv/work"}
	if strings.Join(cfg.Roots, ",") != strings.Join(want, ",") {
		t.Errorf("roots = %v, ожидал %v", cfg.Roots, want)
	}
	if cfg.Addr != "127.0.0.1" || cfg.Port != 7200 || cfg.Token != "abc" {
		t.Errorf("addr/port/token: %+v", cfg)
	}
	if cfg.ListenAddr() != "127.0.0.1:7200" {
		t.Errorf("ListenAddr = %q", cfg.ListenAddr())
	}
	if len(cfg.Errs) != 0 {
		t.Errorf("не ждал ошибок: %v", cfg.Errs)
	}
}

func TestLoadConfigDefaults(t *testing.T) {
	home := t.TempDir()
	cfg, err := LoadConfig(home)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Port != defaultPort {
		t.Errorf("порт по умолчанию %d, ожидал %d", cfg.Port, defaultPort)
	}
	if cfg.ListenAddr() != ":7112" {
		t.Errorf("ListenAddr = %q, ожидал :7112", cfg.ListenAddr())
	}
	// Пустой список корней это не молчание, а названная ошибка для /healthz.
	if len(cfg.Errs) != 1 || !strings.Contains(cfg.Errs[0], "root") {
		t.Errorf("ожидал ошибку про root, получил %v", cfg.Errs)
	}
}

// Строка без «=» это ошибка старта, но её содержимое в текст ошибки не идёт:
// ошибка ложится в журнал с правами 644, а в такой строке может лежать секрет.
func TestLoadConfigBrokenLineKeepsSecret(t *testing.T) {
	home := t.TempDir()
	writeConf(t, home, "root = /x\ntoken supersecret123\n")
	_, err := LoadConfig(home)
	if err == nil {
		t.Fatal("строка без «=» должна ронять старт")
	}
	if strings.Contains(err.Error(), "supersecret123") {
		t.Fatalf("секрет утёк в текст ошибки: %v", err)
	}
	if !strings.Contains(err.Error(), ":2:") {
		t.Fatalf("ошибка не называет номер строки: %v", err)
	}
}

func TestLoadConfigUnknownKey(t *testing.T) {
	home := t.TempDir()
	writeConf(t, home, "root = /x\nhost = 1.2.3.4\n")
	if _, err := LoadConfig(home); err == nil || !strings.Contains(err.Error(), "host") {
		t.Fatalf("незнакомый ключ должен ронять старт, получил %v", err)
	}
}

// Порог простоя читается в трёх видах: duration, сутки и недели. Битое
// значение роняет старт, как всякий непонятый ключ (DK-828).
func TestLoadConfigChatIdle(t *testing.T) {
	home := t.TempDir()
	for val, want := range map[string]time.Duration{
		"3d":  72 * time.Hour,
		"2w":  14 * 24 * time.Hour,
		"30m": 30 * time.Minute,
	} {
		writeConf(t, home, "root = /x\nchat_idle = "+val+"\n")
		cfg, err := LoadConfig(home)
		if err != nil {
			t.Fatalf("chat_idle = %s: %v", val, err)
		}
		if cfg.ChatIdle != want {
			t.Errorf("chat_idle = %s прочитался как %v, ожидал %v", val, cfg.ChatIdle, want)
		}
	}
	for _, bad := range []string{"3x", "0", "-1h", "d", ""} {
		writeConf(t, home, "root = /x\nchat_idle = "+bad+"\n")
		if _, err := LoadConfig(home); err == nil {
			t.Errorf("порог %q должен ронять старт", bad)
		}
	}
}

// Без ключа работает умолчание, ключ его перебивает; решение о том, чей порог
// звать, принимает chatIdleAfter (DK-828).
func TestChatIdleDefault(t *testing.T) {
	home := t.TempDir()
	cfg, err := LoadConfig(home)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ChatIdle != 0 {
		t.Fatalf("без ключа порог %v, ожидал ноль", cfg.ChatIdle)
	}
	s := &server{cfg: cfg}
	if got := s.chatIdleAfter(); got != chatIdleDefault {
		t.Errorf("умолчание %v, ожидал %v", got, chatIdleDefault)
	}
	cfg.ChatIdle = 30 * time.Minute
	if got := s.chatIdleAfter(); got != 30*time.Minute {
		t.Errorf("ключ не перебил умолчание: %v", got)
	}
}

// Интервал догона читается тем же разбором, что и порог простоя: кривое
// значение роняет старт, без ключа живёт умолчание догона (DK-1414).
func TestLoadConfigCatchupInterval(t *testing.T) {
	home := t.TempDir()
	writeConf(t, home, "root = /x\ncatchup_interval = 300ms\n")
	cfg, err := LoadConfig(home)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.CatchupInterval != 300*time.Millisecond {
		t.Fatalf("catchup_interval = %v, ожидал 300ms", cfg.CatchupInterval)
	}
	for _, bad := range []string{"3x", "0", "-1m", "m"} {
		writeConf(t, home, "root = /x\ncatchup_interval = "+bad+"\n")
		if _, err := LoadConfig(home); err == nil {
			t.Errorf("интервал %q должен ронять старт", bad)
		}
	}
	writeConf(t, home, "root = /x\n")
	cfg, err = LoadConfig(home)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.CatchupInterval != 0 {
		t.Fatalf("без ключа интервал %v, ожидал ноль", cfg.CatchupInterval)
	}
	s := &server{cfg: cfg}
	if got := s.cfg.catchupEvery(); got != catchupDefault {
		t.Errorf("умолчание %v, ожидал %v", got, catchupDefault)
	}
	cfg.CatchupInterval = 90 * time.Second
	if got := s.cfg.catchupEvery(); got != 90*time.Second {
		t.Errorf("ключ не перебил умолчание: %v", got)
	}
}

func TestLoadConfigBadPort(t *testing.T) {
	home := t.TempDir()
	for _, bad := range []string{"порт", "0", "70000"} {
		writeConf(t, home, "root = /x\nport = "+bad+"\n")
		if _, err := LoadConfig(home); err == nil {
			t.Errorf("порт %q должен ронять старт", bad)
		}
	}
}

func TestLoadConfigLoosePerms(t *testing.T) {
	home := t.TempDir()
	path := writeConf(t, home, "root = /x\ntoken = secret\n")
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(home); err == nil || !strings.Contains(err.Error(), "600") {
		t.Fatalf("права шире 600 должны ронять старт, получил %v", err)
	}
}

func TestSaveTokenKeepsOtherKeys(t *testing.T) {
	home := t.TempDir()
	path := writeConf(t, home, "root = /x\ntoken = old\nport = 7200\n")
	if err := saveToken(path, "new"); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(home)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Token != "new" || cfg.Port != 7200 || len(cfg.Roots) != 1 {
		t.Errorf("после saveToken: %+v", cfg)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("права файла %o, ожидал 600", fi.Mode().Perm())
	}
}

func TestCmdSecret(t *testing.T) {
	home := t.TempDir()
	first, err := cmdSecret(home, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 64 {
		t.Fatalf("токен %q не похож на 256 бит в hex", first)
	}
	again, err := cmdSecret(home, false)
	if err != nil || again != first {
		t.Fatalf("secret без --rotate обязан печатать тот же токен: %q != %q (%v)", again, first, err)
	}
	rotated, err := cmdSecret(home, true)
	if err != nil || rotated == first {
		t.Fatalf("--rotate обязан заменить секрет (%v)", err)
	}
	cfg, err := LoadConfig(home)
	if err != nil || cfg.Token != rotated {
		t.Fatalf("новый секрет не доехал до конфига: %v %v", cfg, err)
	}
}
