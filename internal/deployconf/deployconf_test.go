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
	write(t, root, "deploy.worker = ./deploy-worker.sh\ndeploy.worker.paths = worker/\n"+
		"deploy.web.paths = web/, shared/\ndeploy.web = ./deploy-web.sh\n"+
		"autonomous = true\n")
	c, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Components) != 2 {
		t.Fatalf("компонентов %d, ждал 2: %+v", len(c.Components), c.Components)
	}
	if c.Components[0].Name != "worker" || c.Components[0].Command != "./deploy-worker.sh" ||
		len(c.Components[0].Paths) != 1 || c.Components[0].Paths[0] != "worker/" {
		t.Errorf("первый компонент разобран не так: %+v", c.Components[0])
	}
	if c.Components[1].Name != "web" || c.Components[1].Command != "./deploy-web.sh" ||
		len(c.Components[1].Paths) != 2 || c.Components[1].Paths[0] != "web/" || c.Components[1].Paths[1] != "shared/" {
		t.Errorf("второй компонент разобран не так: %+v", c.Components[1])
	}
}

// Компонент с одной строкой из двух это ошибка загрузки, а не тихая
// половинчатая раскладка: без пути дифф его никогда не заденет, без команды
// нечем катить задетый.
func TestLoadComponentsIncomplete(t *testing.T) {
	for _, body := range []string{
		"deploy.web = ./deploy-web.sh\n",
		"deploy.web.paths = web/\n",
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
	write(t, root, "deploy.worker = ./deploy-worker.sh\ndeploy.worker.paths = worker/\n"+
		"deploy.web = ./deploy-web.sh\ndeploy.web.paths = web/, shared/\n")
	c, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	matched, miss := c.Match([]string{"worker/main.go", "shared/util.go", "worker/README.md"})
	if miss != "" {
		t.Fatalf("неожиданный miss: %q", miss)
	}
	if len(matched) != 2 || matched[0].Name != "worker" || matched[1].Name != "web" {
		t.Fatalf("состав или порядок не тот: %+v", matched)
	}
	// Путь мимо всех компонентов останавливает разбор и называет себя.
	if matched, miss := c.Match([]string{"worker/main.go", "infra/loader.py"}); miss != "infra/loader.py" || matched != nil {
		t.Fatalf("ждал отказ по infra/loader.py: matched=%v miss=%q", matched, miss)
	}
	// Раскладки нет вовсе: Match не судья, старый одиночный deploy остаётся в силе.
	empty := Config{}
	if matched, miss := empty.Match([]string{"any/path.go"}); matched != nil || miss != "" {
		t.Fatalf("пустая раскладка не должна судить пути: %v %q", matched, miss)
	}
}

// Пути двух компонентов бывают вложены один в другой (общий подкаталог
// раскладки на весь модуль плюс отдельный компонент под его частью). Путь,
// который подходит обоим, достаётся тому, что стоит в конфиге первым: разбор
// компонента для одного пути не выбирает более узкий, он берёт первый
// подходящий по порядку (находка ревью DK-894, №2).
func TestMatchOverlappingPathsPicksFirstByConfig(t *testing.T) {
	root := t.TempDir()
	write(t, root, "deploy.app = ./deploy-app.sh\ndeploy.app.paths = app/\n"+
		"deploy.app-static = ./deploy-app-static.sh\ndeploy.app-static.paths = app/static/\n")
	c, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	matched, miss := c.Match([]string{"app/static/x.css"})
	if miss != "" {
		t.Fatalf("неожиданный miss: %q", miss)
	}
	if len(matched) != 1 || matched[0].Name != "app" {
		t.Fatalf("путь под обоими компонентами должен достаться первому по конфигу (app): %+v", matched)
	}
}

// pathUnder судит по границе `/`, а не по голому HasPrefix: сосед с более
// длинным именем (tools/shipctl) не должен попадать под более короткий путь
// компонента (tools/ship), и наоборот, свой путь остаётся своим (находка
// ревью DK-894, №3).
func TestMatchPathBoundaryNotPrefixCollision(t *testing.T) {
	root := t.TempDir()
	write(t, root, "deploy.ship = ./deploy-ship.sh\ndeploy.ship.paths = tools/ship\n")
	c, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if matched, miss := c.Match([]string{"tools/ship/main.go"}); miss != "" || len(matched) != 1 || matched[0].Name != "ship" {
		t.Fatalf("свой путь компонента должен матчиться: matched=%v miss=%q", matched, miss)
	}
	if matched, miss := c.Match([]string{"tools/shipctl/main.go"}); miss != "tools/shipctl/main.go" || matched != nil {
		t.Fatalf("соседний каталог с тем же префиксом не должен матчиться: matched=%v miss=%q", matched, miss)
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
