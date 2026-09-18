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

// Раскладка компонентов читается парой ключей на имя, в порядке появления
// первого из них в файле, а не в алфавитном.
func TestLoadComponents(t *testing.T) {
	root := t.TempDir()
	write(t, root, "deploy.xr-hub = ./deploy-hub.sh\ndeploy.xr-hub.paths = xr-hub/\n"+
		"deploy.xr-core.paths = xr-core/, shared/\ndeploy.xr-core = ./deploy-server.sh\n"+
		"autonomous = true\n")
	c, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Components) != 2 {
		t.Fatalf("компонентов %d, ждал 2: %+v", len(c.Components), c.Components)
	}
	if c.Components[0].Name != "xr-hub" || c.Components[0].Command != "./deploy-hub.sh" ||
		len(c.Components[0].Paths) != 1 || c.Components[0].Paths[0] != "xr-hub/" {
		t.Errorf("первый компонент разобран не так: %+v", c.Components[0])
	}
	if c.Components[1].Name != "xr-core" || c.Components[1].Command != "./deploy-server.sh" ||
		len(c.Components[1].Paths) != 2 || c.Components[1].Paths[0] != "xr-core/" || c.Components[1].Paths[1] != "shared/" {
		t.Errorf("второй компонент разобран не так: %+v", c.Components[1])
	}
}

// Компонент с одной строкой из двух это ошибка загрузки, а не тихая
// половинчатая раскладка: без пути дифф его никогда не заденет, без команды
// нечем катить задетый.
func TestLoadComponentsIncomplete(t *testing.T) {
	for _, body := range []string{
		"deploy.xr-core = ./deploy-server.sh\n",
		"deploy.xr-core.paths = xr-core/\n",
	} {
		root := t.TempDir()
		write(t, root, body)
		if _, err := Load(root); err == nil {
			t.Fatalf("%q: неполный компонент должен быть ошибкой", body)
		}
	}
}

// Match отдаёт задетые компоненты в порядке конфига (не в порядке путей в
// диффе) и находит все затронутые, каждый по разу.
func TestMatch(t *testing.T) {
	root := t.TempDir()
	write(t, root, "deploy.xr-hub = ./deploy-hub.sh\ndeploy.xr-hub.paths = xr-hub/\n"+
		"deploy.xr-core = ./deploy-server.sh\ndeploy.xr-core.paths = xr-core/, shared/\n")
	c, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	matched, miss := c.Match([]string{"xr-hub/main.go", "shared/util.go", "xr-hub/README.md"})
	if miss != "" {
		t.Fatalf("неожиданный miss: %q", miss)
	}
	if len(matched) != 2 || matched[0].Name != "xr-hub" || matched[1].Name != "xr-core" {
		t.Fatalf("состав или порядок не тот: %+v", matched)
	}
	// Путь мимо всех компонентов останавливает разбор и называет себя.
	if matched, miss := c.Match([]string{"xr-hub/main.go", "infra/loader.py"}); miss != "infra/loader.py" || matched != nil {
		t.Fatalf("ждал отказ по infra/loader.py: matched=%v miss=%q", matched, miss)
	}
	// Раскладки нет вовсе: Match не судья, старый одиночный deploy остаётся в силе.
	empty := Config{}
	if matched, miss := empty.Match([]string{"any/path.go"}); matched != nil || miss != "" {
		t.Fatalf("пустая раскладка не должна судить пути: %v %q", matched, miss)
	}
}

// Автономию спрашивает дашборд, решая, поднимать ли прогон сценария после
// выката (DK-718). Ответ у него один на три случая: нет файла, стоит false,
// файл битый.
func TestAutonomousAnswers(t *testing.T) {
	cases := map[string]bool{
		"":                                    false,
		"deploy = true\n":                     false,
		"deploy = true\nautonomous = false\n": false,
		"autonomous = true\n":                 true,
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
