package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeDeployCfg кладёт обвязку выката в корень проекта.
func writeDeployCfg(t *testing.T, root, body string) {
	t.Helper()
	write(t, root, deployConfigPath, body)
}

func TestResolveTest(t *testing.T) {
	root := t.TempDir()
	// Ни флага, ни файла: команды нет, merge на этом отказывает.
	if cmd, fromCfg, err := resolveTest(root, ""); err != nil || cmd != "" || fromCfg {
		t.Fatalf("пустой корень: %q %v %v", cmd, fromCfg, err)
	}
	writeDeployCfg(t, root, "deploy = from-config\ntest = go test ./...\nautonomous = true\n")
	cmd, fromCfg, err := resolveTest(root, "")
	if err != nil || cmd != "go test ./..." || !fromCfg {
		t.Fatalf("ключ test не прочитан: %q %v %v", cmd, fromCfg, err)
	}
	// Явный флаг сильнее конфига, и в отчёте про конфиг тогда молчим.
	if cmd, fromCfg, _ := resolveTest(root, "make check"); cmd != "make check" || fromCfg {
		t.Fatalf("флаг не перебил конфиг: %q %v", cmd, fromCfg)
	}
	// Пустой ключ это тот же «команды нет»: отказ, а не прогон пустой строки.
	writeDeployCfg(t, root, "deploy = from-config\ntest =\n")
	if cmd, fromCfg, _ := resolveTest(root, ""); cmd != "" || fromCfg {
		t.Fatalf("пустой ключ не должен считаться командой: %q %v", cmd, fromCfg)
	}
}

// TestMergeTestFromConfig: без --test команда тестов берётся из
// .devkit/deploy.local, реально гоняется, и отчёт называет её источник.
func TestMergeTestFromConfig(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	// Путь абсолютный: тесты слияния идут в свежем дереве, и относительный
	// маркер остался бы в нём и умер вместе с уборкой.
	writeDeployCfg(t, root, "test = touch "+filepath.Join(root, "tested.marker")+"\n")
	branchWithFix(t, root)
	msg, err := cmdMerge(root, MergeParams{ID: "XR-001"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "tested.marker")); err != nil {
		t.Fatalf("команда тестов из конфига не запускалась: %q", msg)
	}
	if !strings.Contains(msg, "тесты гнались командой из "+deployConfigPath) {
		t.Fatalf("отчёт молчит про источник команды тестов: %q", msg)
	}

	// Красная команда из конфига держит слияние так же, как красный флаг.
	root, _ = setup(t, rowInProg, "")
	writeDeployCfg(t, root, "test = false\n")
	branchWithFix(t, root)
	if _, err := cmdMerge(root, MergeParams{ID: "XR-001"}); err == nil ||
		!strings.Contains(err.Error(), "красные") {
		t.Fatalf("красная команда из конфига должна держать слияние: %v", err)
	}
}

// TestMergeTestFlagBeatsConfig: явный --test сильнее ключа в конфиге.
func TestMergeTestFlagBeatsConfig(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	writeDeployCfg(t, root, "test = touch "+filepath.Join(root, "fromconfig.marker")+"\n")
	branchWithFix(t, root)
	msg, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "touch " + filepath.Join(root, "fromflag.marker")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "fromflag.marker")); err != nil {
		t.Fatalf("команда из флага не запускалась: %q", msg)
	}
	if _, err := os.Stat(filepath.Join(root, "fromconfig.marker")); err == nil {
		t.Fatal("при явном --test команда из конфига запускаться не должна")
	}
	if strings.Contains(msg, "тесты гнались командой из") {
		t.Fatalf("про конфиг при явном флаге говорить нечего: %q", msg)
	}
}

// TestMergeWithoutTestAnywhere: нет ни флага, ни ключа значит прежний отказ, и
// он называет оба способа задать команду.
func TestMergeWithoutTestAnywhere(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	branchWithFix(t, root)
	_, err := cmdMerge(root, MergeParams{ID: "XR-001"})
	if err == nil || !strings.Contains(err.Error(), "--test") ||
		!strings.Contains(err.Error(), deployConfigPath) {
		t.Fatalf("отказ без команды тестов должен называть флаг и конфиг: %v", err)
	}
	if log := gitT(t, root, "log", "--format=%s", "main"); log != "seed" {
		t.Fatalf("отказ не должен трогать main: %s", log)
	}
}

// TestRevertIgnoresTestKey: revert ключ test не читает. Пустой --test у него
// значит «без прогона»: откат чинит прод и должен быть быстрым, а прогон
// тестов на нём остаётся решением того, кто откатывает.
func TestRevertIgnoresTestKey(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	// Конфиг кладётся после коммита кода: попади он в тот же коммит, откат
	// снёс бы его и тест зеленел бы сам собой.
	codeCommit(t, root, "XR-001", "one.txt")
	writeDeployCfg(t, root, "test = touch tested.marker\n")
	if _, err := cmdRevert(root, RevertParams{ID: "XR-001"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "tested.marker")); err == nil {
		t.Fatal("revert не должен подхватывать команду тестов из конфига")
	}
}
