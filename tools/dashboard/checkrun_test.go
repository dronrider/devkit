package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Подъём прогона сценария после выката (DK-718). Стенд тот же, что у запуска с
// экрана: синтетическая доска, журналируемый tmux и фикстура вердикта. Живого
// клиента тут нет и быть не должно, проверяется дорога от строки Check до
// команды tmux-сессии.

// checkTaskDoc это файл задачи с записью слияния в разделе «Выкат»: без записи
// отметка smoke не с чем сравнивается, и строка считалась бы прогнанной.
const checkTaskDoc = "# XR-003: задача на проверке\n\n" +
	"## Сценарий проверки\n\nАгентский: `git log -1`, ждём коммит правки.\n\n" +
	"## Выкат\n\n- Коммиты: abc1234, def5678\n"

// writeCheckTask кладёт файл задачи в синтетический проект, дописывая к нему
// раздел «Ход работы», когда исполнитель разработки назван.
func writeCheckTask(t *testing.T, proj, id, doc string) {
	t.Helper()
	dir := filepath.Join(proj, "docs", "tasks")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, id+".md"), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
}

// checkEnv это стенд запуска с автономной обвязкой выката: подъём заводит
// только конвейер, доверенный агенту целиком, и без обвязки любой стенд
// упирался бы в первый же барьер решения.
func checkEnv(t *testing.T, sessions string) (*testEnv, string) {
	t.Helper()
	e, _, tmuxLog := runsEnv(t, sessions)
	writeDeployLocal(t, e.proj, "deploy = true\nautonomous = true\n")
	// Перечень прав спрашивается перед каждым подъёмом, и без него стенд
	// упирался бы в отказ раньше всякого своего барьера. Фикстура отвечает
	// «права на месте», а отказ проверяется своим тестом.
	writePermsFake(t, filepath.Dir(e.proj), false)
	return e, tmuxLog
}

// permsFakeDir это каталог перечня прав в синтетическом чекауте devkit.
func permsFakeDir(root string) string {
	return filepath.Join(root, "devkit", "tools", "devkitctl")
}

// writePermsFake кладёт заглушку перечня прав: она пишет в журнал дом вызова и
// отвечает нехваткой прав, когда стенд просит отказа. Живой perms.py тут не
// годится, он читает настройки машины, а стенду нужен свой ответ.
func writePermsFake(t *testing.T, root string, deny bool) string {
	t.Helper()
	dir := permsFakeDir(root)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(dir, "perms.calls")
	body := "import os, sys\n" +
		"open(" + strconv.Quote(log) + ", 'a').write(os.environ.get('HOME', '') + chr(10))\n"
	if deny {
		body += "print('в ~/.claude/settings.json не хватает прав машинного контура, " +
			"2 из 37 (Bash(node:*), Bash(dashboard:*)); разложить: python3 devkitctl.py doctor --fix')\n" +
			"sys.exit(1)\n"
	}
	if err := os.WriteFile(filepath.Join(dir, "perms.py"), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return log
}

// Прав машинного контура на машине нет: подъём не начинается вовсе, а отчёт
// называет причину словами. Раньше сессия поднималась вслепую, упиралась в
// первый же запрос разрешения, одобрить который некому, и паркой задачи
// вопросом к человеку останавливала цель на часы (DK-739).
func TestCheckRunRefusesWithoutPerms(t *testing.T) {
	e, tmuxLog := checkEnv(t, "")
	writePermsFake(t, filepath.Dir(e.proj), true)
	writeCheckTask(t, e.proj, "XR-003", checkTaskDoc)

	rep := checkRunOne(t, e, "XR-003")
	if !rep.Failed || rep.Raised {
		t.Fatalf("подъём без прав не отказал: %+v", rep)
	}
	for _, want := range []string{"прогон не поднят", "прав машинного контура", "doctor --fix"} {
		if !strings.Contains(rep.Line, want) {
			t.Errorf("в отказе нет %q: %s", want, rep.Line)
		}
	}
	if got := readFile(t, tmuxLog); strings.Contains(got, "new-session") {
		t.Errorf("сессия всё-таки поднята: %s", got)
	}
}

// Права спрашиваются под домом пользователя, а не под домом процесса: демон
// живёт под launchd с подложным HOME, и разложенных доктором настроек в нём
// нет вовсе.
func TestCheckRunAsksPermsUnderUserHome(t *testing.T) {
	e, _ := checkEnv(t, "")
	log := filepath.Join(permsFakeDir(filepath.Dir(e.proj)), "perms.calls")
	writeCheckTask(t, e.proj, "XR-003", checkTaskDoc)

	if rep := checkRunOne(t, e, "XR-003"); !rep.Raised {
		t.Fatalf("прогон не поднят: %+v", rep)
	}
	got := strings.TrimSpace(readFile(t, log))
	if got != realHome() {
		t.Errorf("перечень прав позван под домом %q, а дом пользователя %q", got, realHome())
	}
}

// writeDeployLocal кладёт обвязку выката в корень синтетического проекта.
func writeDeployLocal(t *testing.T, proj, body string) {
	t.Helper()
	dir := filepath.Join(proj, ".devkit")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "deploy.local"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// checkRunOne гоняет подъём по одной строке на готовом стенде запуска.
func checkRunOne(t *testing.T, e *testEnv, id string) checkRunReport {
	t.Helper()
	raw, err := e.s.projectBoard(e.proj)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := parseBoardRows(raw)
	if err != nil {
		t.Fatal(err)
	}
	return e.s.checkRun(&Project{Name: "demo", Path: e.proj}, id, rows)
}

// Строка Check без отметки smoke получает сессию проверяющего: та же
// tmux-сессия task-<ID>, что у кнопки экрана, с заказом прогона и отметок.
func TestCheckRunRaisesSession(t *testing.T) {
	e, tmuxLog := checkEnv(t, "")
	writeCheckTask(t, e.proj, "XR-003", checkTaskDoc)

	rep := checkRunOne(t, e, "XR-003")
	if !rep.Raised || rep.Failed {
		t.Fatalf("прогон не поднят: %+v", rep)
	}
	if !strings.Contains(rep.Line, "task-XR-003") {
		t.Errorf("отчёт не называет сессию: %q", rep.Line)
	}
	got := readFile(t, tmuxLog)
	if !strings.Contains(got, "new-session -d -s task-XR-003") {
		t.Fatalf("tmux позван не так: %q", got)
	}
	for _, want := range []string{
		"Прогони агентскую часть сценария проверки XR-003",
		"shipctl smoke XR-003",
		"agentctl stage XR-003 проверка --by",
		"taskctl close XR-003",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("в заказе прогона нет %q: %s", want, got)
		}
	}
}

// Кому прогон не отдавать, сказано в самом заказе: ворота закрытия сверяют
// прогонявшего с исполнителем разработки, и узнать имя после отказа дороже,
// чем получить его до прогона.
func TestCheckRunOrderNamesDeveloper(t *testing.T) {
	e, tmuxLog := checkEnv(t, "")
	writeCheckTask(t, e.proj, "XR-003", checkTaskDoc+
		"\n## Ход работы\n\n- Разработка: субагент модель-base/high по вердикту pick, 2026-09-02 10:00-11:00.\n")

	rep := checkRunOne(t, e, "XR-003")
	if !rep.Raised {
		t.Fatalf("прогон не поднят: %+v", rep)
	}
	if !strings.Contains(rep.Line, "разработку вёл модель-base") {
		t.Errorf("отчёт не называет исполнителя разработки: %q", rep.Line)
	}
	if got := readFile(t, tmuxLog); !strings.Contains(got, "Разработку вёл модель-base") {
		t.Errorf("в заказе прогона нет имени исполнителя разработки: %s", got)
	}
}

// Прогон исполнителю разработки не отдаётся: ярус проверяющего развернулся в ту
// же модель, что вела разработку, и подъём отказывает словами, а не жжёт квоту
// на прогон, который ворота закрытия всё равно не примут.
func TestCheckRunRefusesDeveloperModel(t *testing.T) {
	e, tmuxLog := checkEnv(t, "")
	writeCheckTask(t, e.proj, "XR-003", checkTaskDoc+
		"\n## Ход работы\n\n- Разработка: субагент модель-pro/high по вердикту pick, 2026-09-02 10:00-11:00.\n")

	rep := checkRunOne(t, e, "XR-003")
	if rep.Raised || !rep.Failed {
		t.Fatalf("подъём прогона исполнителю разработки должен быть отказан: %+v", rep)
	}
	if !strings.Contains(rep.Line, "модель-pro") || !strings.Contains(rep.Line, "вела разработку") {
		t.Errorf("отказ не называет причину: %q", rep.Line)
	}
	if got := readFile(t, tmuxLog); strings.Contains(got, "new-session") {
		t.Errorf("сессия не должна подниматься при отказе: %s", got)
	}
}

// Строки, которым прогон не нужен: отметка smoke уже стоит, приёмка за
// человеком, непогашенный провал, строка не в Check. Все четыре отвечают
// словами и без поломки, иначе тик сторожка звонил бы каждые пять минут.
func TestCheckRunSkipsWhatNeedsNoRun(t *testing.T) {
	for _, tc := range []struct {
		name, id, doc, want string
	}{
		{"отметка стоит", "XR-003",
			checkTaskDoc + "- smoke прогнан, 2026-09-02\n", "smoke прогнан"},
		{"файла нет", "XR-003", "", "файла задачи нет"},
		{"не в Check", "XR-004", checkTaskDoc, "не в Check"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, tmuxLog := checkEnv(t, "")
			if tc.doc != "" {
				writeCheckTask(t, e.proj, tc.id, tc.doc)
			}
			rep := checkRunOne(t, e, tc.id)
			if rep.Raised || rep.Failed {
				t.Fatalf("подъём тут не нужен: %+v", rep)
			}
			if !strings.Contains(rep.Line, tc.want) {
				t.Errorf("отчёт не называет причину %q: %s", tc.want, rep.Line)
			}
			if got := readFile(t, tmuxLog); strings.Contains(got, "new-session") {
				t.Errorf("сессия подниматься не должна: %s", got)
			}
		})
	}
}

// Проект с выкатом за пользователем прогон не поднимает. Строка сидит в Check
// без отметки smoke, все остальные признаки за подъём, но конвейер этого
// проекта человеку и принадлежит: до Check строка дошла с его рук, и сессия
// поверх его работы вставала бы каждым тиком сторожка.
func TestCheckRunSkipsUserDeployProject(t *testing.T) {
	for _, tc := range []struct{ name, cfg string }{
		{"обвязки нет", ""},
		{"автономия снята", "deploy = true\nautonomous = false\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, tmuxLog := checkEnv(t, "")
			if tc.cfg == "" {
				if err := os.Remove(filepath.Join(e.proj, ".devkit", "deploy.local")); err != nil {
					t.Fatal(err)
				}
			} else {
				writeDeployLocal(t, e.proj, tc.cfg)
			}
			writeCheckTask(t, e.proj, "XR-003", checkTaskDoc)

			rep := checkRunOne(t, e, "XR-003")
			if rep.Raised || rep.Failed {
				t.Fatalf("выкат за пользователем прогон не поднимает: %+v", rep)
			}
			if !strings.Contains(rep.Line, "autonomous = false") {
				t.Errorf("отказ не называет причину: %q", rep.Line)
			}
			if got := readFile(t, tmuxLog); strings.Contains(got, "new-session") {
				t.Errorf("сессия подниматься не должна: %s", got)
			}
		})
	}
}

// Пользовательская приёмка агентской половины не имеет: прогонять там нечего, и
// подъём такую строку не трогает. Вид приезжает полем строки доски.
func TestCheckRunSkipsUserAccept(t *testing.T) {
	e, _ := checkEnv(t, "")
	writeScript(t, e.bin, "taskctl", "echo '"+strings.Replace(runsBoardJSON,
		`{"id":"XR-003","title":"Задача на проверке"`,
		`{"id":"XR-003","title":"Задача на проверке","accept":"user"`, 1)+"'")
	writeCheckTask(t, e.proj, "XR-003", checkTaskDoc)

	rep := checkRunOne(t, e, "XR-003")
	if rep.Raised || rep.Failed || !strings.Contains(rep.Line, "приёмка за человеком") {
		t.Fatalf("строку с пользовательской приёмкой поднимать нечего: %+v", rep)
	}
}

// Смешанный вид приёмки прогоняется наполовину: агентская часть идёт сессией, а
// закрывать строку заказ не велит, последний шаг остаётся человеку.
func TestCheckRunMixedLeavesCloseToHuman(t *testing.T) {
	e, tmuxLog := checkEnv(t, "")
	writeScript(t, e.bin, "taskctl", "echo '"+strings.Replace(runsBoardJSON,
		`{"id":"XR-003","title":"Задача на проверке"`,
		`{"id":"XR-003","title":"Задача на проверке","accept":"mixed"`, 1)+"'")
	writeCheckTask(t, e.proj, "XR-003", checkTaskDoc)

	if rep := checkRunOne(t, e, "XR-003"); !rep.Raised {
		t.Fatalf("агентская половина смешанного вида прогоняется: %+v", rep)
	}
	got := readFile(t, tmuxLog)
	if !strings.Contains(got, "строку из Check не закрывай") {
		t.Errorf("заказ смешанного вида должен оставить закрытие человеку: %s", got)
	}
	if strings.Contains(got, "taskctl close XR-003") {
		t.Errorf("закрытие смешанной строки заказывать нельзя: %s", got)
	}
}

// Поверх живой работы сессия не поднимается: у задачи уже идёт свой конвейер,
// и второй разбирался бы с той же строкой.
func TestCheckRunSkipsLiveSession(t *testing.T) {
	e, _ := checkEnv(t, "task-XR-003\n")
	writeCheckTask(t, e.proj, "XR-003", checkTaskDoc)

	rep := checkRunOne(t, e, "XR-003")
	if rep.Raised || rep.Failed || !strings.Contains(rep.Line, "уже идёт") {
		t.Fatalf("поверх живой сессии подъёма быть не должно: %+v", rep)
	}
}

// Без имён берётся вся секция Check, и на каждую строку приходится своя строка
// отчёта: зовущий уносит слова в журнал, молчащего исхода нет.
func TestCheckRunsWholeCheckSection(t *testing.T) {
	e, _ := checkEnv(t, "")
	writeCheckTask(t, e.proj, "XR-003", checkTaskDoc)

	lines, raised, failed := e.s.checkRuns(&Project{Name: "demo", Path: e.proj}, nil)
	if raised != 1 || failed {
		t.Fatalf("ждал один поднятый прогон без поломок: %d %v %q", raised, failed, lines)
	}
	if len(lines) != 1 || !strings.Contains(lines[0], "XR-003") {
		t.Fatalf("отчёт по секции Check: %q", lines)
	}
}

// Команда печатает строки отчёта и отвечает кодом: провал нужного подъёма это
// выход 1, а «поднимать нечего» это ноль.
func TestCmdCheckPrintsAndCodes(t *testing.T) {
	e, _ := checkEnv(t, "")
	writeCheckTask(t, e.proj, "XR-003", checkTaskDoc+
		"\n## Ход работы\n\n- Разработка: субагент модель-pro/high по вердикту pick, 2026-09-02 10:00-11:00.\n")

	var out bytes.Buffer
	err := cmdCheck(e.home, e.proj, nil, &out)
	if !errors.Is(err, errCheckRunFailed) {
		t.Fatalf("отказ подъёма это ненулевой выход: %v", err)
	}
	if !strings.Contains(out.String(), "XR-003") {
		t.Fatalf("команда молчит про строку: %q", out.String())
	}

	out.Reset()
	writeCheckTask(t, e.proj, "XR-003", checkTaskDoc+"- smoke прогнан, 2026-09-02\n")
	if err := cmdCheck(e.home, e.proj, nil, &out); err != nil {
		t.Fatalf("прогнанная строка это штатный ноль: %v", err)
	}
	if !strings.Contains(out.String(), "подъём не нужен") {
		t.Fatalf("команда молчит про причину: %q", out.String())
	}
}

// Корень без доски это отказ с причиной: подъём идёт по строке доски, и
// молчаливый ноль тут выглядел бы отработавшей командой.
func TestCmdCheckWithoutBoard(t *testing.T) {
	var out bytes.Buffer
	err := cmdCheck(t.TempDir(), t.TempDir(), nil, &out)
	if err == nil || !strings.Contains(err.Error(), boardRel) {
		t.Fatalf("корень без доски: %v", err)
	}
}
