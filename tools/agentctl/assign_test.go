package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Тесты назначения яруса: разбор префикса в значении машинного слоя, строка via
// вердикта, запись про подписку в файле задачи и сторож на уехавшей ступени.
// Дизайн в docs/lld/DK-090-heterogeneous-ladder.md, разделы «Назначение яруса в
// машинном слое», «Вердикт: строка via и запись в файл задачи» и «Разбивка на
// задачи», строка 1.

// ladderProfiles кладёт директорию профилей с двумя харнесами и отдаёт корень,
// который понимает DEVKIT_HOME. Оба профиля настоящие, из репозитория: на
// claude-code стоят объявление квоты и делегирование субагентом, на glm-code
// делегирование подпроцессом и квота сменным съёмщиком. Подставные тут были бы
// хуже настоящих: гетерогенная лестница проверялась бы на профиле, которого ни
// на одной машине нет.
func ladderProfiles(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, profileDirGroup, profileDirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"claude-code.toml", "glm-code.toml"} {
		src, err := os.ReadFile(filepath.Join(repoRoot(t), profileDirGroup, profileDirName, name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// setupLadder уводит контур во временный HOME с двумя профилями и кладёт
// машинный конфиг с заданным маппингом.
func setupLadder(t *testing.T, machine string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("DEVKIT_HARNESS", "")
	t.Setenv("DEVKIT_HOME", ladderProfiles(t))
	setupHarness(t, machine)
	return quotaPath("claude-code")
}

const ladderMachine = `default = "claude-code"
enabled = ["claude-code", "glm-code"]

[claude-code]
mini = "haiku"
base = "glm-code:glm-5.2"
pro = "opus"
max = "fable"
`

// backMachine это лестница, у которой дорогая ступень уезжает обратно на первую
// подписку: диспетчер сидит на второй, где окно отрастает само, а недельную
// квоту тратит только то, что без неё не делается.
const backMachine = `default = "glm-code"
enabled = ["claude-code", "glm-code"]

[glm-code]
mini = "glm-5.3-flash"
base = "claude-code:opus"
pro = "glm-5.3"
max = "glm-5.3"
`

const homeMachine = `default = "claude-code"
enabled = ["claude-code"]

[claude-code]
mini = "haiku"
base = "sonnet"
pro = "opus"
max = "fable"
`

func TestParseAssignment(t *testing.T) {
	// Разделитель это первое двоеточие: слева каноничное имя харнеса, справа всё
	// остальное как имя модели. Пустая половина это опечатка, а не запись, и
	// разбирать её значило бы гадать.
	cases := []struct {
		value, harness, model string
		away                  bool
	}{
		{value: "haiku", harness: "claude-code", model: "haiku"},
		{value: "glm-code:glm-5.2", harness: "glm-code", model: "glm-5.2", away: true},
		{value: "claude-code:sonnet", harness: "claude-code", model: "sonnet"},
		{value: "claude-code:llama3:8b", harness: "claude-code", model: "llama3:8b"},
		{value: "llama3:8b", harness: "llama3", model: "8b", away: true},
	}
	for _, c := range cases {
		t.Run(c.value, func(t *testing.T) {
			a, err := parseAssignment("harness.local", "claude-code", tierBase, c.value)
			if err != nil {
				t.Fatalf("разбор %q: %v", c.value, err)
			}
			if a.Harness != c.harness || a.Model != c.model {
				t.Fatalf("разобрано %+v, жду харнес %q и модель %q", a, c.harness, c.model)
			}
			if a.away("claude-code") != c.away {
				t.Fatalf("уехавшесть ступени %v, жду %v", a.away("claude-code"), c.away)
			}
		})
	}

	for _, bad := range []string{":opus", "glm-code:"} {
		t.Run("отказ на "+bad, func(t *testing.T) {
			if _, err := parseAssignment("harness.local", "claude-code", tierBase, bad); err == nil {
				t.Fatalf("жду жёсткую ошибку на %q", bad)
			}
		})
	}
}

// TestMachineTiersAllOrNone: ярусов либо все четыре, либо ни одного. Секция без
// ярусов это харнес, который в конфиге присутствует только машинной обвязкой, а
// пропуск одного яруса из четырёх неотличим от забытого.
func TestMachineTiersAllOrNone(t *testing.T) {
	dir := filepath.Join(ladderProfiles(t), profileDirGroup, profileDirName)

	t.Run("секция без ярусов", func(t *testing.T) {
		machine := writeFile(t, t.TempDir(), "harness.local", `enabled = ["claude-code", "glm-code"]

[claude-code]
mini = "haiku"
base = "glm-code:glm-5.2"
pro = "opus"
max = "fable"

[glm-code]
bin = "/opt/claude"
`)
		l, err := mergeLayers(dir, machine, "")
		if err != nil {
			t.Fatalf("слияние: %v", err)
		}
		// DK-189: ярусов на машине нет, и лестница разворачивается предложением
		// из профиля, а не остаётся пустой.
		if s := l.Setup["glm-code"]; !s.mapped() || !s.Suggested {
			t.Fatalf("секция без ярусов ждёт предложения из профиля, вышло %+v", s)
		}
		if l.Setup["glm-code"].Bin != "/opt/claude" {
			t.Fatalf("машинная обвязка потерялась: %+v", l.Setup["glm-code"])
		}
		if !l.Setup["claude-code"].Map[tierBase].away("claude-code") {
			t.Fatalf("назначение base не уехало: %+v", l.Setup["claude-code"].Map[tierBase])
		}
	})

	t.Run("три яруса из четырёх это отказ", func(t *testing.T) {
		machine := writeFile(t, t.TempDir(), "harness.local", "enabled = [\"claude-code\"]\n\n[claude-code]\nmini = \"haiku\"\nbase = \"sonnet\"\npro = \"opus\"\n")
		_, err := mergeLayers(dir, machine, "")
		if err == nil || !strings.Contains(err.Error(), "нет ключа max") {
			t.Fatalf("жду отказ с именем ключа, получил %v", err)
		}
	})

	t.Run("назначение без профиля это предупреждение", func(t *testing.T) {
		machine := writeFile(t, t.TempDir(), "harness.local", `enabled = ["claude-code"]

[claude-code]
mini = "haiku"
base = "nowhere:m"
pro = "opus"
max = "fable"
`)
		l, err := mergeLayers(dir, machine, "")
		if err != nil {
			t.Fatalf("битое назначение уронило слияние: %v", err)
		}
		if !l.Setup["claude-code"].Map[tierBase].Broken {
			t.Fatal("назначение без профиля не помечено битым")
		}
		if len(l.Warns) != 1 || !strings.Contains(l.Warns[0], "назначение битое") {
			t.Fatalf("предупреждения %v", l.Warns)
		}
	})
}

// TestPickVia: четвёртая машинная строка печатается всегда, а не через раз, и
// несёт харнес назначения ступени. Модель при этом идёт без префикса:
// потребитель первой строки её сегодня просто читает.
func TestPickVia(t *testing.T) {
	fixNow(t, testNow)
	root := writeBoard(t)

	t.Run("однородная лестница", func(t *testing.T) {
		setupLadder(t, homeMachine)
		out, err := cmdPick(root, "T-005", false, roleExec, "")
		if err != nil {
			t.Fatalf("pick: %v", err)
		}
		if !strings.HasPrefix(out, "model: sonnet\neffort: high\ntier: base\nvia: claude-code\n") {
			t.Fatalf("на однородной лестнице via должен называть свой же харнес: %q", out)
		}
	})

	t.Run("гетерогенная лестница", func(t *testing.T) {
		setupLadder(t, ladderMachine)
		out, err := cmdPick(root, "T-005", false, roleExec, "")
		if err != nil {
			t.Fatalf("pick: %v", err)
		}
		if !strings.HasPrefix(out, "model: glm-5.2\neffort: high\ntier: base\nvia: glm-code\n") {
			t.Fatalf("уехавшая ступень не названа: %q", out)
		}
		if !strings.Contains(out, "ступень base уезжает в glm-code, остаток чужой подписки мерить нечем, вердикт без корректора") {
			t.Fatalf("вердикт на уехавшей ступени молчит про корректор: %q", out)
		}
	})

	t.Run("битое назначение не роняет pick", func(t *testing.T) {
		setupLadder(t, strings.Replace(ladderMachine, `base = "glm-code:glm-5.2"`, `base = "nowhere:m"`, 1))
		out, err := cmdPick(root, "T-005", false, roleExec, "")
		if err != nil {
			t.Fatalf("битое назначение уронило pick: %v", err)
		}
		if !strings.HasPrefix(out, "model: -\neffort: high\ntier: base\nvia: -\n") {
			t.Fatalf("жду прочерк в обеих машинных строках: %q", out)
		}
		if !strings.Contains(out, "профиля такого харнеса нет") {
			t.Fatalf("поломка прошла молча: %q", out)
		}
	})
}

// TestPickGuardAcrossSubscriptions: пока корректор считает бакеты одного
// харнеса, сдвиг через границу подписок не идёт. Мерить дефицит чужой ступени
// домашним снимком значило бы врать, а печатать её бакеты по домашнему профилю
// врать во второй раз.
func TestPickGuardAcrossSubscriptions(t *testing.T) {
	fixNow(t, testNow)
	root := writeBoard(t)

	t.Run("домашняя ступень ниже: сдвиг идёт", func(t *testing.T) {
		quota := setupLadder(t, homeMachine)
		writeQuota(t, quota, 95, 50, halfWindow)
		out, err := cmdPick(root, "T-006", false, roleExec, "")
		if err != nil {
			t.Fatalf("pick: %v", err)
		}
		if !strings.Contains(out, "корректор: дефицит week_all, pro -> base") {
			t.Fatalf("сдвиг по дефициту потерялся: %q", out)
		}
	})

	t.Run("ступенью ниже чужая подписка: сдвига нет", func(t *testing.T) {
		quota := setupLadder(t, ladderMachine)
		writeQuota(t, quota, 95, 50, halfWindow)
		out, err := cmdPick(root, "T-006", false, roleExec, "")
		if err != nil {
			t.Fatalf("pick: %v", err)
		}
		if !strings.HasPrefix(out, "model: opus\neffort: medium\ntier: pro\nvia: claude-code\n") {
			t.Fatalf("вердикт уехал через границу подписок: %q", out)
		}
		if !strings.Contains(out, "корректор: дефицит week_all, ступень base уезжает в glm-code, чужую подписку мерить нечем, сдвига нет") {
			t.Fatalf("сторож молчит про причину: %q", out)
		}
	})

	t.Run("ступенью ниже битое назначение: сдвига нет", func(t *testing.T) {
		// Сдвиг ради прочерка вместо модели это потерянный вердикт: развернуть
		// итоговую ступень нечем, а исходная работала.
		quota := setupLadder(t, strings.Replace(ladderMachine, `base = "glm-code:glm-5.2"`, `base = "nowhere:m"`, 1))
		writeQuota(t, quota, 95, 50, halfWindow)
		out, err := cmdPick(root, "T-006", false, roleExec, "")
		if err != nil {
			t.Fatalf("pick: %v", err)
		}
		if !strings.HasPrefix(out, "model: opus\neffort: medium\ntier: pro\nvia: claude-code\n") {
			t.Fatalf("вердикт съехал на неразворачиваемую ступень: %q", out)
		}
		if !strings.Contains(out, "ступень base назначена в nowhere, а профиля такого харнеса нет, сдвига нет") {
			t.Fatalf("сторож молчит про причину: %q", out)
		}
	})
}

// TestPickActiveSectionWithoutTiers: DK-189 живьём. Активен харнес, чья секция
// в машинном конфиге несёт одну обвязку, и до правки вердикт уходил с
// прочерками в model и via, хотя профиль харнеса несёт полный `map_*`.
func TestPickActiveSectionWithoutTiers(t *testing.T) {
	fixNow(t, testNow)
	root := writeBoard(t)
	home := t.TempDir()
	setupLadder(t, `default = "claude-code"
enabled = ["claude-code", "glm-code"]

[claude-code]
mini = "haiku"
base = "sonnet"
pro = "opus"
max = "fable"

[glm-code]
home = "`+home+`"
`)
	t.Setenv("DEVKIT_HARNESS", "glm-code")
	out, err := cmdPick(root, "T-005", false, roleExec, "")
	if err != nil {
		t.Fatalf("pick: %v", err)
	}
	if !strings.HasPrefix(out, "model: glm-5.3\neffort: high\ntier: base\nvia: glm-code\n") {
		t.Fatalf("секция-обвязка активного харнеса оставила вердикт без модели: %q", out)
	}

	// Источник маппинга назван: предложение это не то же самое, что настроенная
	// машина, и по выводу harness это должно быть видно.
	shown, err := cmdHarness(root, "")
	if err != nil {
		t.Fatalf("harness: %v", err)
	}
	for _, part := range []string{
		"маппинг ярусов: mini = glm-5.3-flash, base = glm-5.3, pro = glm-5.3, max = glm-5.3",
		"предложение профиля, в секции [glm-code] ярусов нет",
	} {
		if !strings.Contains(shown, part) {
			t.Fatalf("в выводе harness нет %q:\n%s", part, shown)
		}
	}
}

// TestRecordAssignment: по закрытой задаче восстанавливается, чьей подпиской она
// сделана. Домашняя ступень нативного харнеса пишется как раньше, иначе файлы
// задач пришлось бы переписывать.
func TestRecordAssignment(t *testing.T) {
	fixNow(t, testNow)
	root := writeBoard(t)
	if err := os.MkdirAll(filepath.Join(root, "docs", "tasks"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "docs", "tasks", "T-005.md")

	cases := []struct {
		name, machine, active, want string
	}{
		{"домашняя ступень", homeMachine, "claude-code", "- Разработка: субагент sonnet/high по вердикту pick"},
		{"уехавшая ступень", ladderMachine, "claude-code", "- Разработка: подпроцесс glm-code:glm-5.2/high по вердикту pick"},
		// Ступень, уехавшая в харнес со спавном внутри сессии, поднимается его
		// же командой снаружи, то есть подпроцессом: слово записи идёт по
		// дороге, которой работа уходит, а не по имени режима.
		{"ступень уехала обратно", backMachine, "glm-code", "- Разработка: подпроцесс claude-code:opus/high по вердикту pick"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			setupLadder(t, c.machine)
			// Активный харнес называется явно: прогон идёт и внутри сессии
			// агента, а она метит себя в окружении, и детект увёл бы лестницу к
			// той подписке, в которой гоняют тесты.
			t.Setenv("DEVKIT_HARNESS", c.active)
			if err := os.WriteFile(path, []byte("# T-005\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			clearStages(t, root, "T-005")
			if _, err := cmdPick(root, "T-005", true, roleExec, ""); err != nil {
				t.Fatalf("pick --record: %v", err)
			}
			text := stageText(t, root, "T-005")
			if !strings.Contains(text, c.want) {
				t.Fatalf("запись не называет исполнителя как надо:\n%s", text)
			}
		})
	}
}

// TestAltSubProfile: настоящий профиль второй подписки читается сводом
// agentctl, поднимается командой клиента и объявляет остаток сменным съёмщиком.
// Тест смотрит на
// коммитируемый файл, а не на подставной: профиль это то, чем гетерогенная
// лестница отличается от однородной, и разъедься он с ожиданиями утилиты, вся
// уехавшая ступень встала бы на живой машине, а не в прогоне.
func TestAltSubProfile(t *testing.T) {
	setupLadder(t, ladderMachine)
	dir := filepath.Join(os.Getenv("DEVKIT_HOME"), profileDirGroup, profileDirName)
	l, err := mergeLayers(dir, machineConfigPath(), "")
	if err != nil {
		t.Fatalf("слои с профилем второй подписки не сложились: %v", err)
	}
	prof := l.Profiles["glm-code"]
	if prof == nil {
		t.Fatal("профиль второй подписки не загрузился")
	}
	del := prof.section("delegate")
	if got := del.str("mode"); got != "cli" {
		t.Fatalf("режим делегирования второй подписки %q, жду cli: спавна субагента снаружи у клиента нет", got)
	}
	argv := substituteCommand(del.arr("command"), map[string]string{
		"model": "glm-5.2", "effort": "high", "prompt": "тело определения", "workdir": "/дерево",
	})
	if len(argv) == 0 || argv[0] != "claude" {
		t.Fatalf("команда подпроцесса поднимает не клиента подписки: %q", argv)
	}
	for _, want := range []string{"-p", "тело определения", "glm-5.2", "high"} {
		if !inList(argv, want) {
			t.Fatalf("в команде подпроцесса нет %q: %q", want, argv)
		}
	}
	// [quota] объявлена сменным съёмщиком: эндпоинт мониторинга подписки
	// проверен живьём, и пустая секция вернулась бы к молчащему корректору,
	// из-за которого сгоревшее пятичасовое окно заметили по отказам.
	q := quotaSpecOf(l, "glm-code")
	if q == nil {
		t.Fatal("у второй подписки не объявлен остаток: корректор для неё молчит")
	}
	if q.Snap != snapScript || q.Script != "snap/glm-code.sh" {
		t.Fatalf("остаток второй подписки снимает не съёмщик: %+v", q)
	}
	// Машинные пути считаются от каталога подписки: литерал тут увёл бы её
	// хозяйство в контур первой.
	for _, p := range [][2]string{{"rules", "global_file"}, {"hooks", "config"},
		{"skills", "dir"}, {"delegate", "agents_dir"}} {
		got := prof.section(p[0]).str(p[1])
		if !strings.HasPrefix(got, "{home}") {
			t.Fatalf("[%s] %s = %q, а считаться он обязан от {home}", p[0], p[1], got)
		}
	}
}

// TestHomeSubProfileCommand: профиль первой подписки называет обе дороги сразу.
// Тест смотрит на коммитируемый файл, а не на подставной: без команды ступень,
// уехавшая сюда со второй подписки, встала бы на живой машине, а не в прогоне,
// и диспетчеру пришлось бы возвращаться в окно первой подписки.
func TestHomeSubProfileCommand(t *testing.T) {
	setupLadder(t, backMachine)
	dir := filepath.Join(os.Getenv("DEVKIT_HOME"), profileDirGroup, profileDirName)
	l, err := mergeLayers(dir, machineConfigPath(), "")
	if err != nil {
		t.Fatalf("слои с профилем первой подписки не сложились: %v", err)
	}
	prof := l.Profiles["claude-code"]
	if prof == nil {
		t.Fatal("профиль первой подписки не загрузился")
	}
	del := prof.section("delegate")
	if got := del.str("mode"); got != "native" {
		t.Fatalf("режим делегирования первой подписки %q, жду native: внутри своей сессии субагент рождается спавном", got)
	}
	tmpl := del.arr("command")
	if len(tmpl) == 0 {
		t.Fatal("первая подписка не назвала команды: снаружи её поднять нечем, и уехавшая сюда ступень отказная")
	}
	if !hasMark(tmpl, sessionMark) {
		t.Fatalf("в команде нет %s: без имени сессии ход работы делегата не попадёт в ленту раздавшего разговора: %q", sessionMark, tmpl)
	}
	argv := substituteCommand(tmpl, map[string]string{
		"model": "opus", "effort": "medium", "prompt": "тело определения", "session": "с-1",
	})
	if argv[0] != "claude" {
		t.Fatalf("команда подпроцесса поднимает не клиента подписки: %q", argv)
	}
	for _, want := range []string{"-p", "тело определения", "opus", "medium", "с-1"} {
		if !inList(argv, want) {
			t.Fatalf("в команде подпроцесса нет %q: %q", want, argv)
		}
	}
}

// TestCmdHarnessAssignments: подписку перепутать легко, а замечается это по
// счёту, то есть поздно, поэтому команда называет и назначения, и неполную
// раскладку, и битое имя.
func TestCmdHarnessAssignments(t *testing.T) {
	root := writeBoard(t)

	t.Run("уехавшая ступень названа", func(t *testing.T) {
		setupLadder(t, ladderMachine)
		out, err := cmdHarness(root, "")
		if err != nil {
			t.Fatalf("harness: %v", err)
		}
		if !strings.Contains(out, "маппинг ярусов: mini = haiku, base = glm-code:glm-5.2, pro = opus, max = fable") {
			t.Fatalf("маппинг без назначений: %q", out)
		}
		if !strings.Contains(out, "назначение: ярус base уезжает в glm-code, поднимается командой [delegate] его профиля\n") {
			t.Fatalf("назначение не названо: %q", out)
		}
	})

	// Достижимость чужого харнеса видна до первого отказа run: команда,
	// которой его поднимают снаружи, у профиля либо есть, либо нет, и второй
	// случай называется прямо.
	t.Run("назначение в недостижимый харнес", func(t *testing.T) {
		kit := fakeKit(t)
		writeProfile(t, kit, "echocli", echoProfile)
		writeProfile(t, kit, "nativeone", nativeProfile)
		writeMachine(t, kit, "enabled = [\"echocli\"]\ndefault = \"echocli\"\n\n[echocli]\nmini = \"nativeone:чужая\"\nbase = \"cheap\"\npro = \"strong\"\nmax = \"strong\"\n")
		out, err := cmdHarness(writeBoard(t), "")
		if err != nil {
			t.Fatalf("harness: %v", err)
		}
		if !strings.Contains(out, "поднять его снаружи нечем") || !strings.Contains(out, "команды профиль не назвал") {
			t.Fatalf("недостижимое назначение прошло молча: %q", out)
		}
	})

	t.Run("назначение вне списка включённых", func(t *testing.T) {
		setupLadder(t, strings.Replace(ladderMachine, `enabled = ["claude-code", "glm-code"]`, `enabled = ["claude-code"]`, 1))
		out, err := cmdHarness(root, "")
		if err != nil {
			t.Fatalf("harness: %v", err)
		}
		if !strings.Contains(out, "а он не в списке включённых") {
			t.Fatalf("неполная раскладка прошла молча: %q", out)
		}
	})

	t.Run("битое назначение", func(t *testing.T) {
		setupLadder(t, strings.Replace(ladderMachine, `base = "glm-code:glm-5.2"`, `base = "nowhere:m"`, 1))
		out, err := cmdHarness(root, "")
		if err != nil {
			t.Fatalf("harness: %v", err)
		}
		if !strings.Contains(out, "base = -") || !strings.Contains(out, "назначение битое") {
			t.Fatalf("битое назначение не названо: %q", out)
		}
	})
}
