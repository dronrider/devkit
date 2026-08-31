package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestRunShellLimit: команда в пределе отдаёт вывод как раньше, вставшая
// убивается вместе с потомками. Пределы в тесте секундные, а не минутные:
// прогон не должен зависеть от нагрузки машины и не должен её ждать.
func TestRunShellLimit(t *testing.T) {
	root := t.TempDir()
	out, timedOut, err := runShellLimit(root, "echo готово", 5*time.Second, nil)
	if err != nil || timedOut || !strings.Contains(out, "готово") {
		t.Fatalf("быстрая команда в пределе: %q %v %v", out, timedOut, err)
	}

	// Потомок оболочки должен умереть вместе с ней: в выкате это сборка или
	// ssh, и переживший предел потомок держал бы прод в неизвестном состоянии.
	pidFile := filepath.Join(root, "child.pid")
	start := time.Now()
	_, timedOut, err = runShellLimit(root, "sleep 600 & echo $! > "+pidFile+"; wait", time.Second, nil)
	if err == nil || !timedOut {
		t.Fatalf("вставшая команда должна кончиться пределом: %v %v", timedOut, err)
	}
	if el := time.Since(start); el > 30*time.Second {
		t.Fatalf("ждали %s вместо предела в секунду", el)
	}
	raw, rerr := os.ReadFile(pidFile)
	if rerr != nil {
		t.Fatalf("потомок не записал pid: %v", rerr)
	}
	pid, cerr := strconv.Atoi(strings.TrimSpace(string(raw)))
	if cerr != nil {
		t.Fatalf("pid потомка не разобран: %q", raw)
	}
	alive := func() bool { return syscall.Kill(pid, 0) == nil }
	for i := 0; i < 50 && alive(); i++ {
		time.Sleep(20 * time.Millisecond)
	}
	if alive() {
		t.Fatalf("потомок %d пережил предел", pid)
	}
}

// TestShipDeployTimeout: вставший выкат поезда кончается провалом по пределу,
// а не вечным молчанием. В тексте ошибки должно быть всё, по чему разбирают
// инцидент: что предел, какая команда висела и сколько её ждали.
func TestShipDeployTimeout(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	addRemote(t, root)
	branchFor(t, root, "XR-001", "xr-001-fix", "a.txt")
	if _, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true", Train: true}); err != nil {
		t.Fatal(err)
	}
	write(t, root, ".devkit/deploy.local",
		"deploy = sleep 600\nautonomous = true\ndeploy_timeout = 1s\n")
	start := time.Now()
	_, err := cmdShip(root, ShipParams{})
	el := time.Since(start)
	if err == nil {
		t.Fatal("вставший выкат должен провалить ship")
	}
	if el > 60*time.Second {
		t.Fatalf("ship ждал %s, предел был 1s", el)
	}
	for _, want := range []string{"предел", "sleep 600", "1s", "deploy_timeout"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("в провале нет %q:\n%v", want, err)
		}
	}
	// Задача остаётся в In progress: несостоявшийся выкат не двигает доску.
	if b, _ := loadBoard(root); len(b.sects["check"]) != 0 {
		t.Fatal("после провала выката задача не должна быть в Check")
	}
}

// TestShipDeployWithinTimeout: предел не задевает штатный выкат, слов про него
// в успешном отчёте нет.
func TestShipDeployWithinTimeout(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	addRemote(t, root)
	branchFor(t, root, "XR-001", "xr-001-fix", "a.txt")
	if _, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true", Train: true}); err != nil {
		t.Fatal(err)
	}
	write(t, root, ".devkit/deploy.local",
		"deploy = touch deployed.marker\nautonomous = true\ndeploy_timeout = 30s\n")
	msg, err := cmdShip(root, ShipParams{})
	if err != nil {
		t.Fatal(err)
	}
	if _, serr := os.Stat(filepath.Join(root, "deployed.marker")); serr != nil {
		t.Fatalf("выкат не отработал: %q", msg)
	}
	if strings.Contains(msg, "предел") {
		t.Fatalf("в штатном выкате слов про предел быть не должно: %q", msg)
	}
}

// TestDeployTimeoutConfig: без ключа действует умолчание, кривое значение это
// громкая ошибка, а не молчаливый возврат к умолчанию.
func TestDeployTimeoutConfig(t *testing.T) {
	root := t.TempDir()
	writeCfg := func(s string) {
		write(t, root, deployConfigPath, s)
	}

	writeCfg("deploy = make deploy\nautonomous = true\n")
	c, err := loadDeployConfig(root)
	if err != nil || c.Timeout != defaultDeployTimeout {
		t.Fatalf("без ключа ждал умолчание %s: %+v %v", defaultDeployTimeout, c, err)
	}
	if p, perr := resolveDeploy(root, ""); perr != nil || p.timeout != defaultDeployTimeout {
		t.Fatalf("умолчание не доехало до плана: %+v %v", p, perr)
	}
	// Файла нет вовсе: тоже умолчание, иначе нулевой предел снял бы его совсем.
	if c, err := loadDeployConfig(t.TempDir()); err != nil || c.Timeout != defaultDeployTimeout {
		t.Fatalf("без файла ждал умолчание: %+v %v", c, err)
	}

	writeCfg("deploy = make deploy\nautonomous = true\ndeploy_timeout = 90s\n")
	if c, err := loadDeployConfig(root); err != nil || c.Timeout != 90*time.Second {
		t.Fatalf("ключ не прочитан: %+v %v", c, err)
	}
	// Явный --deploy сильнее конфига по команде, но предел берёт оттуда же.
	if p, perr := resolveDeploy(root, "from-flag"); perr != nil || p.run != "from-flag" || p.timeout != 90*time.Second {
		t.Fatalf("предел не доехал до явного --deploy: %+v %v", p, perr)
	}

	for _, bad := range []string{"deploy_timeout = скоро\n", "deploy_timeout = 30\n", "deploy_timeout = 0s\n", "deploy_timeout = -5m\n"} {
		writeCfg(bad)
		if _, err := loadDeployConfig(root); err == nil {
			t.Fatalf("кривой предел (%q) должен быть ошибкой", bad)
		}
	}
}
