package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDeployConfig(t *testing.T) {
	// Файла нет: не ошибка, выкат остаётся за пользователем.
	root := t.TempDir()
	if c, err := loadDeployConfig(root); err != nil || c.Deploy != "" || c.Autonomous {
		t.Fatalf("пустой корень: %+v %v", c, err)
	}

	cfg := "# обвязка выката\ndeploy = ssh vps 'systemctl restart foo'\n\nautonomous = true\n"
	if err := os.MkdirAll(filepath.Join(root, ".devkit"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, deployConfigPath), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := loadDeployConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	// Внутренние кавычки команды не должны потеряться.
	if c.Deploy != "ssh vps 'systemctl restart foo'" {
		t.Fatalf("команда разобрана неверно: %q", c.Deploy)
	}
	if !c.Autonomous {
		t.Fatalf("autonomous не прочитан: %+v", c)
	}
}

func TestUnquote(t *testing.T) {
	cases := map[string]string{
		`"make deploy"`:         "make deploy",
		`'make deploy'`:         "make deploy",
		`make deploy`:           "make deploy",
		`ssh h 'restart foo'`:   "ssh h 'restart foo'", // внешней пары нет, не трогаем
		`"`:                     `"`,
		``:                      ``,
	}
	for in, want := range cases {
		if got := unquote(in); got != want {
			t.Errorf("unquote(%q) = %q, ждал %q", in, got, want)
		}
	}
}

func TestResolveDeploy(t *testing.T) {
	root := t.TempDir()
	writeCfg := func(s string) {
		if err := os.MkdirAll(filepath.Join(root, ".devkit"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, deployConfigPath), []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// Явный флаг сильнее конфига и выполняется всегда; автопуш он не включает,
	// пуш при явном --deploy остаётся за флагом --push.
	writeCfg("deploy = from-config\nautonomous = true\n")
	if p, _ := resolveDeploy(root, "from-flag"); p.run != "from-flag" || p.autonomous {
		t.Fatalf("флаг не перебил конфиг: %+v", p)
	}
	// autonomous=true: команду из конфига катим сами и пушим сами.
	writeCfg("deploy = from-config\nautonomous = true\n")
	if p, _ := resolveDeploy(root, ""); p.run != "from-config" || !p.autonomous {
		t.Fatalf("автономная команда не подхвачена: %+v", p)
	}
	// autonomous=false с командой: выкат за пользователем, но команда названа.
	writeCfg("deploy = from-config\nautonomous = false\n")
	if p, _ := resolveDeploy(root, ""); p.run != "" || p.manual == "" || p.autonomous {
		t.Fatalf("при autonomous=false команду катить нельзя: %+v", p)
	}
	// Команды нет вовсе: плейбук проекта, но автономия всё равно включает пуш.
	// Но это проблемное сочетание: autonomous=true без команды означает, что агент
	// полагает, что ему доверен конвейер, а выкатывать нечего.
	writeCfg("autonomous = true\n")
	if p, _ := resolveDeploy(root, ""); p.run != "" || p.manual != "" || !p.autonomous || p.warn == "" {
		t.Fatalf("без команды выката с autonomous=true ждал warn: %+v", p)
	}
	// autonomous=false с пустой командой: не ошибка, выкат по плейбуку.
	writeCfg("autonomous = false\n")
	if p, _ := resolveDeploy(root, ""); p.run != "" || p.manual != "" || p.autonomous || p.warn != "" {
		t.Fatalf("пустая команда с autonomous=false не должна давать warn: %+v", p)
	}
	// autonomous=true с командой: нормально, warn не нужен.
	writeCfg("deploy = from-config\nautonomous = true\n")
	if p, _ := resolveDeploy(root, ""); p.run != "from-config" || p.warn != "" {
		t.Fatalf("с заполненной командой warn не нужен: %+v", p)
	}
}
