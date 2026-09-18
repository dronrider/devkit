package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// coreHubCfg это раскладка на два компонента: xr-core и xr-hub, каждый со
// своим путём и командой-заглушкой, которая оставляет маркер в root. Порядок
// ключей в файле это порядок, в котором merge и ship катят задетые компоненты.
func coreHubCfg(root string, hubFails bool) string {
	hub := "touch " + filepath.Join(root, "hub.marker")
	if hubFails {
		hub = "false"
	}
	return "deploy.xr-core = touch " + filepath.Join(root, "core.marker") + "\n" +
		"deploy.xr-core.paths = xr-core/\n" +
		"deploy.xr-hub = " + hub + "\n" +
		"deploy.xr-hub.paths = xr-hub/\n" +
		"autonomous = true\n"
}

// componentWorktree заводит linked worktree с веткой branch и коммитит в него
// files (путь -> содержимое): свой помощник вместо branchFor, у которого
// тестовый файл всегда ложится в корень репозитория мимо любой раскладки.
func componentWorktree(t *testing.T, root, branch string, files map[string]string) string {
	t.Helper()
	wt := filepath.Join(t.TempDir(), "wt-"+branch)
	gitT(t, root, "worktree", "add", "-q", "-b", branch, wt, "main")
	for path, content := range files {
		write(t, wt, path, content)
	}
	gitT(t, wt, "add", ".")
	gitT(t, wt, "commit", "-qm", "fix: правка "+branch)
	return wt
}

// TestStatusPrintsComponents: status по задаче в работе печатает задетые
// раскладкой компоненты вместе с их командой, не задетый диффом компонент не
// упоминается.
func TestStatusPrintsComponents(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	writeDeployCfg(t, root, coreHubCfg(root, false))
	componentWorktree(t, root, "xr-001-fix", map[string]string{
		"xr-core/a.go":      "package xrcore\n",
		"xr-core/a_test.go": "package xrcore\n",
	})

	msg, err := cmdStatus(root)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "компоненты: xr-core (touch "+filepath.Join(root, "core.marker")+")") {
		t.Fatalf("нет печати задетого компонента:\n%s", msg)
	}
	// "xr-hub" сам по себе встречается и в общей строке раскладки ("выкат:
	// ... (xr-core, xr-hub)"), список имён в конфиге; а вот с командой в
	// скобках, как у задетого, незадетый компонент попасть не должен.
	if strings.Contains(msg, "xr-hub (") {
		t.Fatalf("незадетый компонент не должен попадать в список задетых:\n%s", msg)
	}
}

// TestStatusComponentsMissingPath: путь диффа мимо раскладки это в status
// информационная строка, не отказ: status обязан дойти до конца и назвать
// остальные задачи тоже.
func TestStatusComponentsMissingPath(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	writeDeployCfg(t, root, coreHubCfg(root, false))
	componentWorktree(t, root, "xr-001-fix", map[string]string{
		"infra/loader.py":      "print(1)\n",
		"infra/loader_test.py": "print(1)\n",
	})

	msg, err := cmdStatus(root)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "infra/loader.py") || !strings.Contains(msg, "не задет ни одним компонентом") {
		t.Fatalf("нет отметки про путь мимо раскладки:\n%s", msg)
	}
	if !strings.Contains(msg, "deploy.<имя>") {
		t.Fatalf("нет подсказки про ключ:\n%s", msg)
	}
}

// TestMergeComponentsHappyPath: диффа задел оба компонента, autonomous=true
// катит их по очереди в порядке конфига, оба маркера остаются на месте, и
// задача уезжает в Check.
func TestMergeComponentsHappyPath(t *testing.T) {
	root, callLog := setup(t, rowInProg, "")
	writeDeployCfg(t, root, coreHubCfg(root, false))
	addRemote(t, root)
	componentWorktree(t, root, "xr-001-fix", map[string]string{
		"xr-core/a.go":      "package xrcore\n",
		"xr-core/a_test.go": "package xrcore\n",
		"xr-hub/b.go":       "package xrhub\n",
	})

	msg, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "выкат компонентов прошёл: xr-core, xr-hub") {
		t.Fatalf("нет отчёта о прогоне компонентов: %q", msg)
	}
	for _, marker := range []string{"core.marker", "hub.marker"} {
		if _, err := os.Stat(filepath.Join(root, marker)); err != nil {
			t.Fatalf("компонент не выкачен, маркер %s не появился", marker)
		}
	}
	// Доску двигает внешний taskctl, стенд его подменяет заглушкой, которая
	// не переписывает секции по-настоящему: перевод виден в звонке, а не в
	// перечитанном docs/TASKS.md (тот же приём, что у остальных тестов файла).
	calls, _ := os.ReadFile(callLog)
	if !strings.Contains(string(calls), "move XR-001 check") {
		t.Fatalf("задача не переведена в Check: %q", calls)
	}
}

// TestMergeComponentsStopsOnFirstFailure: xr-hub встаёт вторым по конфигу и
// падает, xr-core к этому моменту уже выкачен и не перезапускается. Отказ
// называет упавший компонент и то, что уже уехало (решение «двое», DK-894).
func TestMergeComponentsStopsOnFirstFailure(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	writeDeployCfg(t, root, coreHubCfg(root, true))
	addRemote(t, root)
	componentWorktree(t, root, "xr-001-fix", map[string]string{
		"xr-core/a.go":      "package xrcore\n",
		"xr-core/a_test.go": "package xrcore\n",
		"xr-hub/b.go":       "package xrhub\n",
	})

	_, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true"})
	if err == nil {
		t.Fatal("провал компонента xr-hub должен провалить merge")
	}
	for _, want := range []string{"xr-hub", "уже выкачены: xr-core", "In progress"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("в отказе нет %q: %v", want, err)
		}
	}
	if _, statErr := os.Stat(filepath.Join(root, "core.marker")); statErr != nil {
		t.Fatal("xr-core должен был отработать до провала xr-hub")
	}
	if _, statErr := os.Stat(filepath.Join(root, "hub.marker")); statErr == nil {
		t.Fatal("xr-hub провалился, маркера быть не должно")
	}
	b, err := loadBoard(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(b.sects["in-progress"]) != 1 || b.sects["in-progress"][0].ID != "XR-001" {
		t.Fatalf("провалившийся выкат не должен двигать задачу с доски: %+v", b.sects)
	}
}

// TestMergeRefusesUnmappedPath: диффа задел путь мимо раскладки, autonomous
// катить нечем, merge отказывает и называет путь и подсказку про ключ, до
// тестов и ребейза.
func TestMergeRefusesUnmappedPath(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	writeDeployCfg(t, root, coreHubCfg(root, false))
	addRemote(t, root)
	componentWorktree(t, root, "xr-001-fix", map[string]string{
		"infra/loader.py":      "print(1)\n",
		"infra/loader_test.py": "print(1)\n",
	})

	_, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true"})
	if err == nil {
		t.Fatal("путь мимо раскладки должен отбить merge")
	}
	for _, want := range []string{"infra/loader.py", "deploy.<имя>"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("в отказе нет %q: %v", want, err.Error())
		}
	}
	b, err := loadBoard(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(b.sects["in-progress"]) != 1 {
		t.Fatalf("отказ до слияния не должен трогать доску: %+v", b.sects)
	}
}

// TestMergeExplicitDeployBypassesComponents: явный --deploy это одна команда
// пользователя прямо сейчас, раскладка ей не указ, даже если дифф задел путь,
// для которого нет компонента.
func TestMergeExplicitDeployBypassesComponents(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	writeDeployCfg(t, root, coreHubCfg(root, false))
	addRemote(t, root)
	componentWorktree(t, root, "xr-001-fix", map[string]string{
		"infra/loader.py":      "print(1)\n",
		"infra/loader_test.py": "print(1)\n",
	})

	marker := filepath.Join(root, "explicit.marker")
	msg, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true", Deploy: "touch " + marker})
	if err != nil {
		t.Fatalf("явный --deploy не должен спрашивать раскладку: %v", err)
	}
	if _, statErr := os.Stat(marker); statErr != nil {
		t.Fatalf("явная команда выката не отработала: %q", msg)
	}
}

// TestShipRunsComponentsForTrain: два поездных слияния задевают разные
// компоненты, ship считает раскладку по диапазону deployed..main и катит
// задетые компоненты по очереди, разом переводя обе задачи в Check.
func TestShipRunsComponentsForTrain(t *testing.T) {
	root, callLog := setup(t, rowInProg+rowInProg3, "")
	writeDeployCfg(t, root, coreHubCfg(root, false))
	addRemote(t, root)

	componentWorktree(t, root, "xr-001-fix", map[string]string{
		"xr-core/a.go":      "package xrcore\n",
		"xr-core/a_test.go": "package xrcore\n",
	})
	if _, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true", Train: true}); err != nil {
		t.Fatal(err)
	}
	// XR-003 нужен сценарий проверки в файле задачи, иначе ворот перевода в
	// Check отобьёт её на стороне ship: файл ложится на main до ответвления
	// XR-003, тем же приёмом, что в TestTrainMergeAndShip.
	taskWithScenario(t, root, "XR-003")
	componentWorktree(t, root, "xr-003-fix", map[string]string{
		"xr-hub/b.go":      "package xrhub\n",
		"xr-hub/b_test.go": "package xrhub\n",
	})
	if _, err := cmdMerge(root, MergeParams{ID: "XR-003", Test: "true", Train: true}); err != nil {
		t.Fatal(err)
	}

	msg, err := cmdShip(root, ShipParams{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "поезд выкачен по компонентам") || !strings.Contains(msg, "xr-core") || !strings.Contains(msg, "xr-hub") {
		t.Fatalf("нет отчёта о прогоне компонентов поезда: %q", msg)
	}
	for _, marker := range []string{"core.marker", "hub.marker"} {
		if _, err := os.Stat(filepath.Join(root, marker)); err != nil {
			t.Fatalf("компонент поезда не выкачен, маркер %s не появился", marker)
		}
	}
	calls, _ := os.ReadFile(callLog)
	if !strings.Contains(string(calls), "move XR-001 check") || !strings.Contains(string(calls), "move XR-003 check") {
		t.Fatalf("обе задачи поезда должны уйти в Check: %q", calls)
	}
}

// TestShipDrainSwallowsUnmappedPath: раскладку заводят уже после того, как
// код лёг train-слиянием без автономии (обычный порядок на живом проекте:
// раскладка приезжает следом за кодом), и на ship путь мимо неё это то же
// предусловие, что занятая очередь или пустой поезд, --drain отступает молча,
// а без него называет путь и подсказку.
func TestShipDrainSwallowsUnmappedPath(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	addRemote(t, root)
	componentWorktree(t, root, "xr-001-fix", map[string]string{
		"infra/loader.py":      "print(1)\n",
		"infra/loader_test.py": "print(1)\n",
	})
	if _, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true", Train: true}); err != nil {
		t.Fatal(err)
	}
	writeDeployCfg(t, root, coreHubCfg(root, false))

	msg, err := cmdShip(root, ShipParams{Drain: true})
	if err != nil {
		t.Fatalf("под --drain путь мимо раскладки не должен быть громкой ошибкой: %v", err)
	}
	if !strings.Contains(msg, "infra/loader.py") {
		t.Fatalf("разлив должен назвать путь мимо раскладки: %q", msg)
	}
	// Ворота перевода (taskMoveGate) в стенде зовут заглушку taskctl, а та
	// правит docs/TASKS.md на диске при любом вызове без коммита: артефакт
	// стенда, не настоящего --dry-run. Второй вызов ship той же командой на
	// том же корне иначе упёрся бы в грязное дерево, которого в жизни нет.
	gitT(t, root, "checkout", "--", "docs/TASKS.md")

	if _, err := cmdShip(root, ShipParams{}); err == nil {
		t.Fatal("без --drain путь мимо раскладки должен быть громким отказом")
	} else if !strings.Contains(err.Error(), "infra/loader.py") || !strings.Contains(err.Error(), "deploy.<имя>") {
		t.Fatalf("в отказе нет пути или подсказки: %v", err)
	}
}
