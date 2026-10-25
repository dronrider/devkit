package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dronrider/devkit/internal/checkrun"
)

// Подъём проверяющего после выката (DK-718, DK-947). Стенд тот же, что у
// остальных проверок конвейера: синтетическая доска, поддельная команда
// выката и фикстуры утилит в PATH. Живой головы тут нет, проверяется, кого и с
// чем зовёт выкат: `taskctl run <ID> --model <модель>` с заказом прогона.

// autonomousCfg это обвязка автономного выката: команда выката поддельная,
// маркер её запуска ложится в корень.
func autonomousCfg(root string) string {
	return "deploy = touch " + filepath.Join(root, "deployed.marker") + "\nautonomous = true\n"
}

// rowMixed это XR-001 со смешанным видом приёмки: агентская половина за
// проверяющим, закрытие за человеком.
const rowMixed = "| XR-001 | Починка бага [приёмка: mixed] | bug | P1 | 55 (50+0+0+5+0) | [tasks/XR-001.md](tasks/XR-001.md) |\n"

// Кейс 1 DK-947: выкат строки mixed без человека в окне сам поднимает
// проверяющего командой taskctl run, с моделью и заказом прогона и отметки
// smoke, а закрытие оставляет человеку. Раньше подъём шёл бинарём дашборда, и
// без него строка стояла в Check до захода человека.
func TestMergeAutonomousRaisesCheckRun(t *testing.T) {
	root, callLog := setup(t, rowMixed, "")
	writeDeployCfg(t, root, autonomousCfg(root))
	addRemote(t, root)
	branchWithFix(t, root)

	msg, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "deployed.marker")); err != nil {
		t.Fatalf("выкат не запускался: %q", msg)
	}
	calls := readCalls(t, callLog)
	for _, want := range []string{
		"run XR-001 -C " + root, "--model модель-base", "--hidden", "--harness стенд",
		checkrun.OrderHead + "XR-001", "shipctl smoke XR-001", "строку из Check не закрывай",
	} {
		if !strings.Contains(calls, want) {
			t.Errorf("подъём проверяющего позван без %q: %q", want, calls)
		}
	}
	if !strings.Contains(msg, "прогон сценария поднят командой taskctl run, ступень новое окно") {
		t.Fatalf("отчёт молчит про подъём прогона: %q", msg)
	}
}

// Кейс 2 DK-947: вердикт ревью дал base, а base в раскладке это модель,
// которая вела разработку. Выкат поднимает проверяющего ступенью выше, а не
// отказом «поднять другой моделью руками».
func TestMergeRaisesCheckerAboveAuthor(t *testing.T) {
	root, callLog := setup(t, rowMixed, "")
	writeDeployCfg(t, root, autonomousCfg(root))
	addRemote(t, root)
	doc, err := os.ReadFile(filepath.Join(root, "docs", "tasks", "XR-001.md"))
	if err != nil {
		t.Fatal(err)
	}
	write(t, root, "docs/tasks/XR-001.md", string(doc)+
		"\n## Ход работы\n\n- Разработка: субагент модель-base/high по вердикту pick, 2026-09-11 10:00-11:00.\n")
	branchWithFix(t, root)

	msg, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true"})
	if err != nil {
		t.Fatal(err)
	}
	calls := readCalls(t, callLog)
	if !strings.Contains(calls, "--model модель-pro") || strings.Contains(calls, "--model модель-base") {
		t.Fatalf("проверяющий поднят не ступенью выше автора: %q", calls)
	}
	for _, want := range []string{"поднят ступенью до pro", "разработку вёл модель-base"} {
		if !strings.Contains(msg, want) {
			t.Errorf("отчёт не называет %q: %q", want, msg)
		}
	}
}

// Выкат за пользователем прогон не поднимает: человек в окне, и проверяющего
// он поднимает сам (граница задачи DK-718).
func TestMergeWithoutAutonomousRaisesNothing(t *testing.T) {
	root, callLog := setup(t, rowInProg, "")
	writeDeployCfg(t, root, "deploy = true\nautonomous = false\n")
	branchWithFix(t, root)

	if _, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true"}); err != nil {
		t.Fatal(err)
	}
	if calls := readCalls(t, callLog); strings.Contains(calls, "run XR-") {
		t.Fatalf("при autonomous = false подъёма быть не должно: %q", calls)
	}
}

// Разлив поезда тиком сторожка это второй вход: тик зовёт `ship --drain`, и
// прогон поднимается на весь состав, а не на головную задачу.
func TestShipDrainRaisesCheckRunForTrain(t *testing.T) {
	root, callLog := setup(t, rowInProg+rowInProg3, "")
	writeDeployCfg(t, root, autonomousCfg(root))
	addRemote(t, root)
	taskWithScenario(t, root, "XR-003")
	branchFor(t, root, "XR-001", "xr-001-fix", "a.txt")
	if _, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true", Train: true}); err != nil {
		t.Fatal(err)
	}
	branchFor(t, root, "XR-003", "xr-003-fix", "b.txt")
	if _, err := cmdMerge(root, MergeParams{ID: "XR-003", Test: "true", Train: true}); err != nil {
		t.Fatal(err)
	}

	msg, err := cmdShip(root, ShipParams{Drain: true})
	if err != nil {
		t.Fatal(err)
	}
	calls := readCalls(t, callLog)
	for _, id := range []string{"XR-001", "XR-003"} {
		if !strings.Contains(calls, "run "+id+" -C "+root) {
			t.Errorf("подъём прогона по составу пропустил %s: %q", id, calls)
		}
	}
	if !strings.Contains(msg, "прогон сценария поднят") {
		t.Fatalf("отчёт разлива молчит про подъём: %q", msg)
	}
}

// Ручной ship без флагов прогон не поднимает даже на автономном проекте:
// границу задачи держит признак вызова, а не флаг конфига. Разлив зовёт тик, у
// него окна нет; голый ship зовёт тот, кто сидит в окне и проверяющего
// поднимает сам. Оставшееся без отметки доберёт следующий тик.
func TestShipByHandRaisesNothing(t *testing.T) {
	root, callLog := setup(t, rowInProg, "")
	writeDeployCfg(t, root, autonomousCfg(root))
	addRemote(t, root)
	branchFor(t, root, "XR-001", "xr-001-fix", "a.txt")
	if _, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true", Train: true}); err != nil {
		t.Fatal(err)
	}
	msg, err := cmdShip(root, ShipParams{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "deployed.marker")); err != nil {
		t.Fatalf("выкат не запускался: %q", msg)
	}
	if calls := readCalls(t, callLog); strings.Contains(calls, "run XR-") {
		t.Fatalf("ручной ship прогон не поднимает: %q", calls)
	}
	if strings.Contains(msg, "прогон сценария поднят") {
		t.Fatalf("отчёт ручного ship обещает подъём: %q", msg)
	}
}

// Ручной выкат поезда командой человека прогон не поднимает: явный --deploy это
// указание прямо сейчас, и окно у него есть.
func TestShipManualDeployRaisesNothing(t *testing.T) {
	root, callLog := setup(t, rowInProg, "")
	branchFor(t, root, "XR-001", "xr-001-fix", "a.txt")
	if _, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true", Train: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdShip(root, ShipParams{Deploy: "true"}); err != nil {
		t.Fatal(err)
	}
	if calls := readCalls(t, callLog); strings.Contains(calls, "run XR-") {
		t.Fatalf("при явном --deploy подъёма быть не должно: %q", calls)
	}
}

// Ветка, не тронувшая ничего вне docs/, уходит в Check без выката, и прогон ей
// нужен тот же: сценарий у неё свой, а отметки smoke на ней нет.
func TestMergeTrainDocsOnlyRaisesCheckRun(t *testing.T) {
	root, callLog := setup(t, rowInProg, "")
	writeDeployCfg(t, root, autonomousCfg(root))
	addRemote(t, root)
	gitT(t, root, "checkout", "-qb", "xr-001-docs", "main")
	write(t, root, "docs/lld/note.md", "# LLD\n")
	// Добавляется только docs/: обвязка выката лежит в рабочем дереве
	// незакоммиченной, и попади она в коммит, ветка перестала бы быть
	// бескодовой.
	gitT(t, root, "add", "docs")
	gitT(t, root, "commit", "-qm", "docs: XR-001 правка LLD")

	if _, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true", Train: true}); err != nil {
		t.Fatal(err)
	}
	if calls := readCalls(t, callLog); !strings.Contains(calls, "run XR-001 -C "+root) {
		t.Fatalf("бескодовой задаче прогон нужен тот же: %q", calls)
	}
}

// checkStand это строка XR-001 в Check на автономном проекте: страховка тика
// (`shipctl check-run`) видит её по доске, без выката.
func checkStand(t *testing.T) (string, string) {
	t.Helper()
	root, callLog := setup(t, "", rowInProg)
	writeDeployCfg(t, root, "deploy = true\nautonomous = true\n")
	return root, callLog
}

// Страховка тика поднимает строку Check тем же путём, что выкат, а машинный
// вид отдаёт исход по строкам: по нему тик помнит повтор отказа.
func TestCheckRunJSONForTheTick(t *testing.T) {
	root, callLog := checkStand(t)
	out, failed, err := cmdCheckRun(root, nil, true)
	if err != nil || failed {
		t.Fatalf("подъём по строке Check: %v %v %s", err, failed, out)
	}
	var reps []checkrun.Report
	if err := json.Unmarshal([]byte(out), &reps); err != nil {
		t.Fatalf("машинный вид не разобрался (%v): %s", err, out)
	}
	if len(reps) != 1 || reps[0].ID != "XR-001" || !reps[0].Raised {
		t.Fatalf("ждал один поднятый прогон: %+v", reps)
	}
	if calls := readCalls(t, callLog); !strings.Contains(calls, "run XR-001 -C "+root) {
		t.Fatalf("страховка позвала не taskctl run: %q", calls)
	}
}

// Занятый замок это живая голова задачи, чаще всего та, что сама катила выкат:
// поломкой он не считается, а отчёт говорит, кто прогонит сценарий.
func TestCheckRunBusyHeadIsNotFailure(t *testing.T) {
	root, _ := checkStand(t)
	t.Setenv("TASKCTL_RUN_CODE", "3")
	out, failed, err := cmdCheckRun(root, nil, false)
	if err != nil || failed {
		t.Fatalf("занятый замок это не поломка: %v %v %s", err, failed, out)
	}
	if !strings.Contains(out, "работа уже идёт") || !strings.Contains(out, "поднимет тик после её выхода") {
		t.Fatalf("отчёт не говорит, кто прогонит сценарий: %s", out)
	}
}

// Лестнице нечем поднять голову, позван человек: это поломка подъёма.
func TestCheckRunCalledHumanIsFailure(t *testing.T) {
	root, _ := checkStand(t)
	t.Setenv("TASKCTL_RUN_CODE", "1")
	out, failed, _ := cmdCheckRun(root, nil, false)
	if !failed || !strings.Contains(out, "позван человек") {
		t.Fatalf("зов человеку это отказ подъёма: %v %s", failed, out)
	}
}

// Без taskctl поднимать нечем: отказ называет, что делать руками.
func TestCheckRunWithoutTaskctl(t *testing.T) {
	root, _ := checkStand(t)
	t.Setenv("PATH", t.TempDir())
	out, failed, _ := cmdCheckRun(root, nil, false)
	for _, want := range []string{"taskctl не нашёлся", "shipctl smoke XR-001"} {
		if !failed || !strings.Contains(out, want) {
			t.Fatalf("отказ без taskctl не называет %q: %v %s", want, failed, out)
		}
	}
}

// Прав машинного контура нет: голову не поднимают вовсе, иначе она встала бы
// на первом же запросе разрешения, одобрить который некому (DK-739).
func TestCheckRunRefusesWithoutPerms(t *testing.T) {
	root, callLog := checkStand(t)
	t.Setenv("DEVKIT_HOME", fakeDevkit(t, true))
	out, failed, _ := cmdCheckRun(root, nil, false)
	if !failed || !strings.Contains(out, "не хватает прав") {
		t.Fatalf("отказ без прав: %v %s", failed, out)
	}
	if calls := readCalls(t, callLog); strings.Contains(calls, "run XR-") {
		t.Fatalf("без прав голова подниматься не должна: %q", calls)
	}
}

// Вверху лестницы модель автора: поднять некем, и это отказ со словами, а не
// прогон, который ворота закрытия всё равно не примут.
func TestCheckRunRefusesAtTopOfLadder(t *testing.T) {
	root, callLog := checkStand(t)
	bin := t.TempDir()
	writeAgentctlFake(t, bin, "max")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	write(t, root, "docs/tasks/XR-001.md", "# XR-001\n\n## Ход работы\n\n- Разработка: субагент модель-max/high по вердикту pick, 2026-09-11.\n")
	out, failed, _ := cmdCheckRun(root, nil, false)
	if !failed || !strings.Contains(out, "вела разработку") {
		t.Fatalf("столкновение наверху лестницы: %v %s", failed, out)
	}
	if calls := readCalls(t, callLog); strings.Contains(calls, "run XR-") {
		t.Fatalf("при отказе голова подниматься не должна: %q", calls)
	}
}

func TestCheckRunPassesSelectedIndependentHarnessToTaskctl(t *testing.T) {
	root, callLog := checkStand(t)
	bin := t.TempDir()
	writeAgentctlFake(t, bin, "pro")
	path := filepath.Join(bin, "agentctl")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	raw := `{"harnesses":[
	{"name":"glm","enabled":true,"auto":true,"models":[{"tier":"pro","model":"glm-5.3"},{"tier":"max","model":"glm-5.3"}]},
	{"name":"router","enabled":true,"default":true,"models":[{"tier":"pro","model":"sonnet"}]}]}`
	if err := os.WriteFile(path, []byte(strings.Replace(string(body), ladderFixture, raw, 1)), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("DEVKIT_HARNESS", "glm")
	write(t, root, "docs/tasks/XR-001.md", "# XR-001\n\n## Ход работы\n\n- Разработка: субагент glm-5.3/high по вердикту pick, 2026-09-11.\n")
	out, failed, err := cmdCheckRun(root, nil, false)
	if err != nil || failed {
		t.Fatalf("%s: %v", out, err)
	}
	calls := readCalls(t, callLog)
	for _, want := range []string{"--harness router", "--model sonnet"} {
		if !strings.Contains(calls, want) {
			t.Fatalf("носитель не получил %s: %s", want, calls)
		}
	}
	if !strings.Contains(out, "подписка router") {
		t.Fatalf("выбор не виден: %s", out)
	}
}

// Поднимать нечего значит и говорить нечего: пустой состав утилит не зовёт.
func TestCheckRunNoteWithoutTasks(t *testing.T) {
	if note := checkRunNote(t.TempDir(), nil); note != "" {
		t.Fatalf("на пустом составе приписки быть не должно: %q", note)
	}
}

// TestTaskHarnessPolicy это допуск подписок к автоматическим назначениям
// (DK-1453): подъём проверяющего после выката не берёт включённую подписку
// без разрешения, даже когда независимая модель есть только у неё, и отказ
// зовёт человека словами, а не головой на чужой квоте.
func TestTaskHarnessPolicy(t *testing.T) {
	layout := func(auto string) string {
		return `{"harnesses":[
	{"name":"стенд","enabled":true,"default":true,"bin":"клиент",
	 "models":[{"tier":"base","model":"модель-base"},{"tier":"pro","model":"модель-pro"},{"tier":"max","model":"модель-max"}]},
	{"name":"router","enabled":true,` + auto + `"models":[{"tier":"max","model":"route-max"}]}]}`
	}
	stand := func(t *testing.T, auto string) (string, string) {
		t.Helper()
		root, callLog := checkStand(t)
		bin := t.TempDir()
		writeAgentctlFake(t, bin, "max")
		path := filepath.Join(bin, "agentctl")
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(strings.Replace(string(body), ladderFixture, layout(auto), 1)), 0o755); err != nil {
			t.Fatal(err)
		}
		t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
		write(t, root, "docs/tasks/XR-001.md", "# XR-001\n\n## Ход работы\n\n- Разработка: субагент модель-max/high по вердикту pick, 2026-09-11.\n")
		return root, callLog
	}

	t.Run("запрещённая не поднимается ради независимости", func(t *testing.T) {
		root, callLog := stand(t, ``)
		out, failed, _ := cmdCheckRun(root, nil, false)
		if !failed || !strings.Contains(out, "вела разработку") {
			t.Fatalf("запрет обязан кончаться отказом: %v %s", failed, out)
		}
		if calls := readCalls(t, callLog); strings.Contains(calls, "run XR-") || strings.Contains(calls, "router") {
			t.Fatalf("запрещённая подписка поднята: %q", calls)
		}
	})
	t.Run("после явного разрешения участвует", func(t *testing.T) {
		root, callLog := stand(t, `"auto":true,`)
		out, failed, err := cmdCheckRun(root, nil, false)
		if err != nil || failed {
			t.Fatalf("%s: %v", out, err)
		}
		calls := readCalls(t, callLog)
		for _, want := range []string{"--harness router", "--model route-max"} {
			if !strings.Contains(calls, want) {
				t.Fatalf("носитель не получил %s: %s", want, calls)
			}
		}
		if !strings.Contains(out, "подписка router") {
			t.Fatalf("выбор не виден: %s", out)
		}
	})

	t.Run("ступень запрещённого via без модели отдаёт отказ", func(t *testing.T) {
		root, callLog := checkStand(t)
		bin := t.TempDir()
		writeAgentctlFake(t, bin, "max")
		path := filepath.Join(bin, "agentctl")
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		raw := `{"harnesses":[
		{"name":"стенд","enabled":true,"default":true,"bin":"клиент",
		 "models":[{"tier":"max","model":"route-max","via":"router"}]},
		{"name":"router","enabled":true,"auto":false,"bin":"клиент",
		 "models":[{"tier":"max","model":"route-max"}]}]}`
		if err := os.WriteFile(path, []byte(strings.Replace(string(body), ladderFixture, raw, 1)), 0o755); err != nil {
			t.Fatal(err)
		}
		t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
		// Записи разработки нет: отбор держит только допуск, и ярус не
		// разворачивает ни одна допущенная ступень.
		out, failed, _ := cmdCheckRun(root, nil, false)
		if !failed {
			t.Fatalf("отсутствие разрешённой модели обязано отказать: %s", out)
		}
		for _, want := range []string{"прогон не поднят", "автоматическим назначениям"} {
			if !strings.Contains(out, want) {
				t.Fatalf("в отказе нет %q: %s", want, out)
			}
		}
		if calls := readCalls(t, callLog); strings.Contains(calls, "run XR-") || strings.Contains(calls, "router") {
			t.Fatalf("запрещённая подписка поднята: %q", calls)
		}
	})
}

func readCalls(t *testing.T, callLog string) string {
	t.Helper()
	data, err := os.ReadFile(callLog)
	if err != nil {
		return ""
	}
	return string(data)
}

func TestCheckRunPassesActualViaToTaskctl(t *testing.T) {
	root, callLog := checkStand(t)
	bin := t.TempDir()
	writeAgentctlFake(t, bin, "pro")
	path := filepath.Join(bin, "agentctl")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	raw := `{"harnesses":[
	{"name":"glm","enabled":true,"auto":true,"models":[{"tier":"pro","model":"glm-5.3"},{"tier":"max","model":"glm-5.3"}]},
	{"name":"router","enabled":true,"default":true,"models":[{"tier":"pro","model":"sonnet","via":"owner"}]},
	{"name":"owner","enabled":true,"auto":true,"models":[]}]}`
	if err := os.WriteFile(path, []byte(strings.Replace(string(body), ladderFixture, raw, 1)), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("DEVKIT_HARNESS", "glm")
	write(t, root, "docs/tasks/XR-001.md", "# XR-001\n\n## Ход работы\n\n- Разработка: субагент glm-5.3/high по вердикту pick, 2026-09-11.\n")
	out, failed, err := cmdCheckRun(root, nil, false)
	if err != nil || failed {
		t.Fatalf("%s: %v", out, err)
	}
	calls := readCalls(t, callLog)
	for _, want := range []string{"--harness owner", "--model sonnet"} {
		if !strings.Contains(calls, want) {
			t.Fatalf("носитель не получил %s: %s", want, calls)
		}
	}
	if !strings.Contains(out, "подписка owner") {
		t.Fatalf("выбор не виден: %s", out)
	}
}
