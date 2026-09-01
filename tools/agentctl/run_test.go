package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Контур делегирования собирается синтетический: живого клиента чужого
// инструмента в тестах нет, и профиль поднимает echo, как советует
// docs/lld/DK-033-universal-kit.md. Проверяется от этого не меньше: наружу
// видны и окружение подпроцесса, и его рабочая директория, и код возврата.

const profileHead = `[detect]

[rules]
mode = "embed"

`

// fakeKit раскладывает временный devkit: профили харнесов и определения
// агентов. Возвращает корень, из него же берётся HOME для машинного конфига.
func fakeKit(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(harnessEnv, "")
	t.Setenv(runDepthEnv, "")
	t.Setenv("DEVKIT_HOME", home)
	// Тесты гоняются и изнутри сессии агента, а она называет себя в окружении:
	// не сними её имя, и боковой журнал делегирования поехал бы в настоящий
	// транскрипт настоящего разговора.
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	// Вид сессии тоже снимается: тесты гоняет и живое окно, и делегат в
	// печатном режиме, а предупреждение про раздачу без окна проверяется
	// отдельно и от вида запустившей сессии зависеть не должно.
	t.Setenv(entrypointEnv, "cli")
	agents := filepath.Join(home, "kit", "agents")
	if err := os.MkdirAll(agents, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, role := range []string{"exec", "review"} {
		for _, effort := range []string{"low", "medium", "high", "xhigh"} {
			name := role + "-" + effort
			body := "---\nname: " + name + "\neffort: " + effort + "\n---\n\nТело определения " + name + ".\n"
			if err := os.WriteFile(filepath.Join(agents, name+".md"), []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := os.MkdirAll(filepath.Join(home, "kit", "harness"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Съём остатка в тестах не поднимается: настоящий refreshQuota запускает
	// клиента, и делать ему тут нечего. Кого позвали, проверяется отдельно.
	prev := refreshQuota
	refreshQuota = func(string) {}
	t.Cleanup(func() { refreshQuota = prev })
	return home
}

// realPath разворачивает симлинки временной директории: на macOS /var это
// ссылка на /private/var, и pwd подпроцесса пишется уже развёрнутым.
func realPath(t *testing.T, dir string) string {
	t.Helper()
	p, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func writeProfile(t *testing.T, kit, name, body string) {
	t.Helper()
	path := filepath.Join(kit, "kit", "harness", name+".toml")
	if err := os.WriteFile(path, []byte(profileHead+body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeMachine(t *testing.T, home, body string) {
	t.Helper()
	dir := filepath.Join(home, ".devkit")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, machineConfigName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// echoProfile это харнес с делегированием через команду: подпроцесс печатает
// то, что ему досталось, и кладёт промпт в файл рабочей директории.
const echoProfile = `[delegate]
mode = "cli"
command = ["/bin/sh", "-c", "echo model={model} effort={effort} depth=$DEVKIT_RUN_DEPTH harness=$DEVKIT_HARNESS pwd=$(pwd); printf %s \"$1\" > prompt.txt; exit ${EXIT_CODE:-0}", "sh", "{prompt}"]

[hooks]

[quota]
`

// signalProfile это подпроцесс, который убивает себя сигналом: кода выхода у
// такого нет вовсе.
const signalProfile = `[delegate]
mode = "cli"
command = ["/bin/sh", "-c", "kill -TERM $$"]

[hooks]

[quota]
`

const nativeProfile = `[delegate]
mode = "native"

[hooks]

[quota]
`

// nativeCmdProfile это харнес, назвавший обе дороги сразу: спавн внутри своей
// сессии для домашней ступени и команду для подъёма снаружи.
const nativeCmdProfile = `[delegate]
mode = "native"
command = ["/bin/sh", "-c", "echo model={model} effort={effort} harness=$DEVKIT_HARNESS pwd=$(pwd)"]

[hooks]

[quota]
`

const noneProfile = `[delegate]
mode = "none"

[hooks]

[quota]
`

// runOut гоняет cmdRun и отдаёт код возврата вместе со всем, что напечатано.
func runOut(t *testing.T, root, id, role, workdir string) (int, string) {
	t.Helper()
	var out, errw bytes.Buffer
	code, err := cmdRun(root, id, false, role, "", workdir, &out, &errw)
	if err != nil {
		t.Fatalf("run %s: %v", id, err)
	}
	return code, out.String() + errw.String()
}

// TestRunCLI: режим cli поднимает подпроцесс в рабочей директории, ставит ему
// ограничитель вложенности и харнес назначения, а промпт собирает из тела
// определения агента и блока задачи.
func TestRunCLI(t *testing.T) {
	kit := fakeKit(t)
	writeProfile(t, kit, "echocli", echoProfile)
	writeMachine(t, kit, "enabled = [\"echocli\"]\ndefault = \"echocli\"\n\n[echocli]\nmini = \"cheap\"\nbase = \"cheap\"\npro = \"strong\"\nmax = \"strong\"\n")
	root := writeBoard(t)
	work := realPath(t, t.TempDir())
	code, out := runOut(t, root, "T-001", roleExec, work)
	if code != 0 {
		t.Fatalf("код возврата %d, жду 0: %s", code, out)
	}
	for _, want := range []string{"model: cheap", "делегирование: cli (харнес echocli", "model=cheap", "effort=low",
		"depth=1", "harness=echocli", "pwd=" + work} {
		if !strings.Contains(out, want) {
			t.Fatalf("в выводе нет %q:\n%s", want, out)
		}
	}
	prompt, err := os.ReadFile(filepath.Join(work, "prompt.txt"))
	if err != nil {
		t.Fatalf("промпт подпроцессу не доехал: %v", err)
	}
	for _, want := range []string{"Тело определения exec-low.", "Задача T-001, роль: исполнитель",
		"Рабочая директория: " + work, "docs/tasks/T-001.md"} {
		if !strings.Contains(string(prompt), want) {
			t.Fatalf("в промпте нет %q:\n%s", want, prompt)
		}
	}
	if strings.Contains(string(prompt), "name: exec-low") {
		t.Fatalf("frontmatter определения в промпт не едет:\n%s", prompt)
	}
}

// TestRunCLIExitCode: код выхода подпроцесса проезжает наружу как есть, иначе
// скрипт поверх run не отличит сделанную работу от провала.
func TestRunCLIExitCode(t *testing.T) {
	kit := fakeKit(t)
	writeProfile(t, kit, "echocli", echoProfile)
	writeMachine(t, kit, "enabled = [\"echocli\"]\ndefault = \"echocli\"\n\n[echocli]\nmini = \"cheap\"\nbase = \"cheap\"\npro = \"strong\"\nmax = \"strong\"\n")
	root := writeBoard(t)
	t.Setenv("EXIT_CODE", "7")
	code, out := runOut(t, root, "T-001", roleExec, t.TempDir())
	if code != 7 {
		t.Fatalf("код возврата %d, жду 7: %s", code, out)
	}
}

// TestRunCLISignal: подпроцесс, убитый сигналом, кода выхода не имеет, и наружу
// идёт единица с названной причиной. Отдать тут минус единицу нельзя: она
// обернулась бы в 255 и читалась бы как чужой код возврата.
func TestRunCLISignal(t *testing.T) {
	kit := fakeKit(t)
	writeProfile(t, kit, "signalcli", signalProfile)
	writeMachine(t, kit, "enabled = [\"signalcli\"]\ndefault = \"signalcli\"\n\n[signalcli]\nmini = \"cheap\"\nbase = \"cheap\"\npro = \"strong\"\nmax = \"strong\"\n")
	root := writeBoard(t)
	code, out := runOut(t, root, "T-001", roleExec, t.TempDir())
	if code != 1 {
		t.Fatalf("код возврата %d, жду 1: %s", code, out)
	}
	if !strings.Contains(out, "подпроцесс кончился сигналом") {
		t.Fatalf("причина сигнального кода не названа:\n%s", out)
	}
}

// TestRunNative: домашняя ступень на харнесе со своим спавном это сегодняшнее
// поведение, инструкция диспетчеру и выход 0.
func TestRunNative(t *testing.T) {
	kit := fakeKit(t)
	writeProfile(t, kit, "nativeone", nativeProfile)
	writeMachine(t, kit, "enabled = [\"nativeone\"]\ndefault = \"nativeone\"\n\n[nativeone]\nmini = \"cheap\"\nbase = \"cheap\"\npro = \"strong\"\nmax = \"strong\"\n")
	root := writeBoard(t)
	code, out := runOut(t, root, "T-001", roleExec, "")
	if code != 0 {
		t.Fatalf("код возврата %d, жду 0: %s", code, out)
	}
	for _, want := range []string{"делегирование: native", "спавнить субагента: модель cheap, определение exec-low"} {
		if !strings.Contains(out, want) {
			t.Fatalf("в выводе нет %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "рабочая директория "+root) {
		t.Fatalf("без --workdir рабочая директория это корень проекта:\n%s", out)
	}
}

// TestRunNativeAway: уехавшая ступень отказная у профиля, который назвал один
// спавн: внутри сессии инструмента он есть, а снаружи дёрнуть его нечем, и
// команды подъёма профиль не дал.
func TestRunNativeAway(t *testing.T) {
	kit := fakeKit(t)
	writeProfile(t, kit, "echocli", echoProfile)
	writeProfile(t, kit, "nativeone", nativeProfile)
	writeMachine(t, kit, "enabled = [\"echocli\"]\ndefault = \"echocli\"\n\n[echocli]\nmini = \"nativeone:чужая\"\nbase = \"cheap\"\npro = \"strong\"\nmax = \"strong\"\n")
	root := writeBoard(t)
	code, out := runOut(t, root, "T-001", roleExec, "")
	if code != codeNothingToDelegate {
		t.Fatalf("код возврата %d, жду %d: %s", code, codeNothingToDelegate, out)
	}
	for _, want := range []string{"делегировать нечем", "уезжает в nativeone", "native", "профиль", "не назвал"} {
		if !strings.Contains(out, want) {
			t.Fatalf("в выводе нет %q:\n%s", want, out)
		}
	}
}

// TestRunNativeAwayCommand: ступень уезжает в харнес, который назвал и спавн, и
// команду. Режим достижимости не решает: снаружи такой харнес поднимается
// командой, и работа уходит в подпроцесс, а не в отказ.
func TestRunNativeAwayCommand(t *testing.T) {
	kit := fakeKit(t)
	writeProfile(t, kit, "echocli", echoProfile)
	writeProfile(t, kit, "nativecmd", nativeCmdProfile)
	writeMachine(t, kit, "enabled = [\"echocli\"]\ndefault = \"echocli\"\n\n[echocli]\nmini = \"nativecmd:чужая\"\nbase = \"cheap\"\npro = \"strong\"\nmax = \"strong\"\n")
	root := writeBoard(t)
	work := realPath(t, t.TempDir())
	code, out := runOut(t, root, "T-001", roleExec, work)
	if code != 0 {
		t.Fatalf("код возврата %d, жду 0: %s", code, out)
	}
	for _, want := range []string{"делегирование: команда снаружи (харнес nativecmd",
		"model=чужая", "effort=low", "harness=nativecmd", "pwd=" + work,
		"делегат вернулся: задача T-001, харнес nativecmd"} {
		if !strings.Contains(out, want) {
			t.Fatalf("в выводе нет %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "спавнить субагента") {
		t.Fatalf("уехавшая ступень уходит подпроцессом, а не инструкцией спавна:\n%s", out)
	}
}

// TestRunNativeHomeCommand: у того же профиля домашняя ступень остаётся за
// спавном. Команда это дорога наружу, и поднимать ею свою же сессию значило бы
// платить второй сессией за работу, которую диспетчер делает субагентом.
func TestRunNativeHomeCommand(t *testing.T) {
	kit := fakeKit(t)
	writeProfile(t, kit, "nativecmd", nativeCmdProfile)
	writeMachine(t, kit, "enabled = [\"nativecmd\"]\ndefault = \"nativecmd\"\n\n[nativecmd]\nmini = \"cheap\"\nbase = \"cheap\"\npro = \"strong\"\nmax = \"strong\"\n")
	root := writeBoard(t)
	code, out := runOut(t, root, "T-001", roleExec, "")
	if code != 0 {
		t.Fatalf("код возврата %d, жду 0: %s", code, out)
	}
	for _, want := range []string{"делегирование: native", "спавнить субагента: модель cheap"} {
		if !strings.Contains(out, want) {
			t.Fatalf("в выводе нет %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "подпроцесс") {
		t.Fatalf("домашняя ступень подпроцессом не поднимается:\n%s", out)
	}
}

// TestRunNone: харнес без делегирования отказывает тройкой, а не молчаливым
// нулём: скрипт поверх run не должен принять «нечем» за сделанную работу.
func TestRunNone(t *testing.T) {
	kit := fakeKit(t)
	writeProfile(t, kit, "dumb", noneProfile)
	writeMachine(t, kit, "enabled = [\"dumb\"]\ndefault = \"dumb\"\n\n[dumb]\nmini = \"cheap\"\nbase = \"cheap\"\npro = \"strong\"\nmax = \"strong\"\n")
	root := writeBoard(t)
	code, out := runOut(t, root, "T-001", roleExec, "")
	if code != codeNothingToDelegate {
		t.Fatalf("код возврата %d, жду %d: %s", code, codeNothingToDelegate, out)
	}
	if !strings.Contains(out, "делегировать нечем: харнес dumb") {
		t.Fatalf("в выводе нет причины отказа:\n%s", out)
	}
}

// TestRunUnmapped: ненастроенная машина это тот же отказ. Вердикт при этом
// печатается целиком, ярусная его половина от инструмента не зависит.
func TestRunUnmapped(t *testing.T) {
	kit := fakeKit(t)
	writeProfile(t, kit, "echocli", echoProfile)
	writeMachine(t, kit, "enabled = [\"echocli\"]\ndefault = \"echocli\"\n")
	root := writeBoard(t)
	code, out := runOut(t, root, "T-001", roleExec, "")
	if code != codeNothingToDelegate {
		t.Fatalf("код возврата %d, жду %d: %s", code, codeNothingToDelegate, out)
	}
	if !strings.Contains(out, "tier: mini") || !strings.Contains(out, "исполнителя вердикт не назвал") {
		t.Fatalf("жду полный вердикт и отказ:\n%s", out)
	}
}

// TestRunDepthGuard: ограничитель вложенности. Взведённая переменная это отказ
// до всякой работы, иначе делегирование развернулось бы в воронку сессий.
func TestRunDepthGuard(t *testing.T) {
	kit := fakeKit(t)
	writeProfile(t, kit, "echocli", echoProfile)
	writeMachine(t, kit, "enabled = [\"echocli\"]\ndefault = \"echocli\"\n\n[echocli]\nmini = \"cheap\"\nbase = \"cheap\"\npro = \"strong\"\nmax = \"strong\"\n")
	root := writeBoard(t)
	t.Setenv(runDepthEnv, "1")
	var out, errw bytes.Buffer
	if _, err := cmdRun(root, "T-001", false, roleExec, "", "", &out, &errw); err == nil {
		t.Fatalf("жду отказ на взведённом ограничителе, вывод: %s%s", out.String(), errw.String())
	} else if !strings.Contains(err.Error(), "вложенное делегирование запрещено") {
		t.Fatalf("причина отказа: %v", err)
	}
	if out.Len() > 0 {
		t.Fatalf("отказ идёт до вердикта, а напечатано: %s", out.String())
	}
}

// TestRunDepthValues: что считается взведённым ограничителем. Ноль и пустая
// строка это его отсутствие, нечисловое значение считается взведённым: молча
// пропустить непонятное хуже, чем отказать.
func TestRunDepthValues(t *testing.T) {
	cases := []struct {
		value string
		stop  bool
	}{{"", false}, {"0", false}, {" 0 ", false}, {"1", true}, {"2", true}, {"да", true}}
	for _, c := range cases {
		err := runDepthRefusal(c.value, "T-001")
		if (err != nil) != c.stop {
			t.Fatalf("значение %q: ошибка %v, жду отказ=%v", c.value, err, c.stop)
		}
	}
}

// TestRunRefreshesExecutorQuota: снимок освежается у харнеса-исполнителя, а не
// у активного: тратиться будет его подписка.
func TestRunRefreshesExecutorQuota(t *testing.T) {
	kit := fakeKit(t)
	writeProfile(t, kit, "echocli", echoProfile)
	quotaTail := `
[delegate]
mode = "cli"
command = ["/bin/sh", "-c", "exit 0"]

[hooks]

[quota]
snap = "script"
script = "snap/none.sh"
buckets = ["week_all"]
required = "week_all"
spend_mini = ["week_all"]
spend_base = ["week_all"]
spend_pro = ["week_all"]
spend_max = ["week_all"]
`
	writeProfile(t, kit, "faraway", quotaTail)
	writeMachine(t, kit, "enabled = [\"echocli\"]\ndefault = \"echocli\"\n\n[echocli]\nmini = \"faraway:чужая\"\nbase = \"cheap\"\npro = \"strong\"\nmax = \"strong\"\n")
	root := writeBoard(t)
	var asked []string
	refreshQuota = func(name string) { asked = append(asked, name) }
	if code, out := runOut(t, root, "T-001", roleExec, ""); code != 0 {
		t.Fatalf("код возврата %d, жду 0: %s", code, out)
	}
	if len(asked) != 1 || asked[0] != "faraway" {
		t.Fatalf("освежали снимок у %v, жду faraway", asked)
	}
	// Пустая [quota] у исполнителя значит, что освежать нечего, и run про это
	// молчит: сказано уже вердиктом.
	asked = nil
	writeMachine(t, kit, "enabled = [\"echocli\"]\ndefault = \"echocli\"\n\n[echocli]\nmini = \"cheap\"\nbase = \"cheap\"\npro = \"strong\"\nmax = \"strong\"\n")
	if code, out := runOut(t, root, "T-001", roleExec, ""); code != 0 {
		t.Fatalf("код возврата %d, жду 0: %s", code, out)
	}
	if len(asked) != 0 {
		t.Fatalf("у харнеса без объявления квоты освежать нечего, а звали %v", asked)
	}
}

// TestRefreshLock: замок съёма один на машину, а брошенный снимается по
// возрасту, иначе умершая сессия выключила бы освежение навсегда.
func TestRefreshLock(t *testing.T) {
	// Часы настоящие: замок сверяется с временем правки директории, а его ставит
	// файловая система.
	now := time.Now()
	lock := filepath.Join(t.TempDir(), "quota-refresh.lock")
	if !takeRefreshLock(lock, now) {
		t.Fatal("свободный замок должен браться")
	}
	if takeRefreshLock(lock, now) {
		t.Fatal("занятый замок браться не должен")
	}
	old := now.Add(-refreshLockStale - time.Minute)
	if err := os.Chtimes(lock, old, old); err != nil {
		t.Fatal(err)
	}
	if !takeRefreshLock(lock, now) {
		t.Fatal("брошенный замок снимается по возрасту")
	}
}

// TestSubstituteCommand: подстановка идёт внутри элемента, незнакомая скобка
// остаётся как есть.
func TestSubstituteCommand(t *testing.T) {
	got := substituteCommand([]string{"cli", "--model={model}", "{prompt}", "{json}"},
		map[string]string{"model": "strong", "prompt": "текст задачи"})
	want := []string{"cli", "--model=strong", "текст задачи", "{json}"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("вышло %v, жду %v", got, want)
	}
}

// TestRunReviewRole: роль ревьювера едет и в определение агента, и в блок
// задачи промпта.
func TestRunReviewRole(t *testing.T) {
	kit := fakeKit(t)
	writeProfile(t, kit, "echocli", echoProfile)
	writeMachine(t, kit, "enabled = [\"echocli\"]\ndefault = \"echocli\"\n\n[echocli]\nmini = \"cheap\"\nbase = \"cheap\"\npro = \"strong\"\nmax = \"strong\"\n")
	root := writeBoard(t)
	work := realPath(t, t.TempDir())
	if code, out := runOut(t, root, "T-001", roleReview, work); code != 0 {
		t.Fatalf("код возврата %d, жду 0: %s", code, out)
	}
	prompt, err := os.ReadFile(filepath.Join(work, "prompt.txt"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Тело определения review-", "роль: ревьювер"} {
		if !strings.Contains(string(prompt), want) {
			t.Fatalf("в промпте нет %q:\n%s", want, prompt)
		}
	}
}

// TestRunBadWorkdir: несуществующее дерево задачи это отказ до вердикта, а не
// подпроцесс, поднятый неизвестно где.
func TestRunBadWorkdir(t *testing.T) {
	kit := fakeKit(t)
	writeProfile(t, kit, "echocli", echoProfile)
	writeMachine(t, kit, "enabled = [\"echocli\"]\ndefault = \"echocli\"\n\n[echocli]\nmini = \"cheap\"\nbase = \"cheap\"\npro = \"strong\"\nmax = \"strong\"\n")
	root := writeBoard(t)
	var out, errw bytes.Buffer
	_, err := cmdRun(root, "T-001", false, roleExec, "", filepath.Join(root, "нет-такого"), &out, &errw)
	if err == nil || !strings.Contains(err.Error(), "рабочей директории") {
		t.Fatalf("жду отказ про рабочую директорию, вышло: %v", err)
	}
}

// TestRunHeadlessWarning: раздача из сессии без окна получает предупреждение, а
// из живого окна нет. Подпроцесс живёт столько, сколько живёт зовущий, и в
// сессии без окна длинный делегат обрывается на середине молча.
func TestRunHeadlessWarning(t *testing.T) {
	kit := fakeKit(t)
	writeProfile(t, kit, "echocli", echoProfile)
	writeMachine(t, kit, "enabled = [\"echocli\"]\ndefault = \"echocli\"\n\n[echocli]\nmini = \"cheap\"\nbase = \"cheap\"\npro = \"strong\"\nmax = \"strong\"\n")
	root := writeBoard(t)
	work := realPath(t, t.TempDir())
	for _, point := range sdkEntrypoints {
		t.Setenv(entrypointEnv, point)
		code, out := runOut(t, root, "T-001", roleExec, work)
		if code != 0 {
			t.Fatalf("код возврата %d, жду 0: %s", code, out)
		}
		for _, want := range []string{"делегирование из сессии без окна", entrypointEnv + "=" + point, "T-001"} {
			if !strings.Contains(out, want) {
				t.Fatalf("при %s=%s в выводе нет %q:\n%s", entrypointEnv, point, want, out)
			}
		}
	}
	for _, point := range []string{"cli", "claude-vscode", ""} {
		t.Setenv(entrypointEnv, point)
		if _, out := runOut(t, root, "T-001", roleExec, work); strings.Contains(out, "сессии без окна") {
			t.Fatalf("при %s=%q фон законен, предупреждать не о чем:\n%s", entrypointEnv, point, out)
		}
	}
}

// TestRunReportsReturn: конец делегата называется строкой. Из живого окна
// раздача уходит фоновым вызовом, и уведомление о его конце само по себе не
// говорит ни кода выхода, ни того, чья подписка заплатила.
func TestRunReportsReturn(t *testing.T) {
	kit := fakeKit(t)
	writeProfile(t, kit, "echocli", echoProfile)
	writeMachine(t, kit, "enabled = [\"echocli\"]\ndefault = \"echocli\"\n\n[echocli]\nmini = \"cheap\"\nbase = \"cheap\"\npro = \"strong\"\nmax = \"strong\"\n")
	root := writeBoard(t)
	code, out := runOut(t, root, "T-001", roleExec, realPath(t, t.TempDir()))
	if code != 0 {
		t.Fatalf("код возврата %d, жду 0: %s", code, out)
	}
	for _, want := range []string{"делегат вернулся: задача T-001", "харнес echocli", "код выхода 0"} {
		if !strings.Contains(out, want) {
			t.Fatalf("в выводе нет %q:\n%s", want, out)
		}
	}
	// Провал называется тем же местом: молчание после ненулевого кода
	// неотличимо от сделанной работы.
	t.Setenv("EXIT_CODE", "7")
	if _, out := runOut(t, root, "T-001", roleExec, realPath(t, t.TempDir())); !strings.Contains(out, "код выхода 7") {
		t.Fatalf("код выхода делегата не назван:\n%s", out)
	}
}

// TestDelegateReturnQuota: у харнеса с объявленной квотой строка возврата
// называет и снимок, по которому виден прирост расхода. Без объявления
// называть нечего, и хвоста нет.
func TestDelegateReturnQuota(t *testing.T) {
	got := delegateReturn("T-001", "glm-code", 0, &quotaSpec{Path: "/home/me/.devkit/quota/glm-code.local"})
	for _, want := range []string{"подпиской glm-code", "/home/me/.devkit/quota/glm-code.local"} {
		if !strings.Contains(got, want) {
			t.Fatalf("в строке возврата нет %q: %s", want, got)
		}
	}
	if strings.Contains(delegateReturn("T-001", "echocli", 1, nil), "снимок") {
		t.Fatalf("харнес без объявления квоты снимка не имеет: %s", delegateReturn("T-001", "echocli", 1, nil))
	}
}
