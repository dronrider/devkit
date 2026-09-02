package deployconf

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func write(t *testing.T, root, body string) {
	t.Helper()
	dir := filepath.Join(root, ".devkit")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "deploy.local"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Ключи обвязки читаются оба, а кавычки снимаются только внешние: внутри
// команды выката они часть самой команды.
func TestLoadKeys(t *testing.T) {
	root := t.TempDir()
	write(t, root, "# обвязка\ndeploy = \"ssh vps 'systemctl restart foo'\"\ntest = go test ./...\nautonomous = true\ndeploy_timeout = 90s\n")
	c, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if c.Deploy != "ssh vps 'systemctl restart foo'" {
		t.Errorf("команда выката: %q", c.Deploy)
	}
	if c.Test != "go test ./..." || !c.Autonomous || c.Timeout != 90*time.Second {
		t.Errorf("обвязка прочиталась не так: %+v", c)
	}
}

// Файла нет значит выкат за пользователем, и это не ошибка: так живёт проект
// без обвязки вовсе.
func TestLoadMissingIsUserDeploy(t *testing.T) {
	c, err := Load(t.TempDir())
	if err != nil || c.Autonomous || c.Timeout != DefaultTimeout {
		t.Fatalf("корень без обвязки: %+v, %v", c, err)
	}
}

// Автономию спрашивает дашборд, решая, поднимать ли прогон сценария после
// выката (DK-718). Ответ у него один на три случая: нет файла, стоит false,
// файл битый.
func TestAutonomousAnswers(t *testing.T) {
	cases := map[string]bool{
		"":                                            false,
		"deploy = true\n":                             false,
		"deploy = true\nautonomous = false\n":         false,
		"autonomous = true\n":                         true,
		"autonomous = true\ndeploy_timeout = позже\n": false,
	}
	for body, want := range cases {
		root := t.TempDir()
		if body != "" {
			write(t, root, body)
		}
		if got := Autonomous(root); got != want {
			t.Errorf("обвязка %q: автономия %v, ожидал %v", body, got, want)
		}
	}
}
