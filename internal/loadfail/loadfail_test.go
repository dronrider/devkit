package loadfail

import (
	"strings"
	"testing"
)

// Живой вывод go test с провалом браузерного замера: тест признался строкой
// Mark, и краснота его чужая, как бы дифф ни пересекался с компонентом.
const quietOut = `--- FAIL: TestDashboardSmokeHiddenTabQuiet (74.12s)
    quietsmoke_test.go:390: ` + Mark + `
    quietsmoke_test.go:452: лесенкой 1 из 10, занятых окон 1 при пороге 2
FAIL
FAIL	github.com/dronrider/devkit/tools/dashboard	75.301s
`

func TestLoadTakesTestOwnConfession(t *testing.T) {
	load, names := Load(quietOut)
	if !load {
		t.Fatalf("тест назвал себя замером стенного времени, а вердикт своя краснота: %q", quietOut)
	}
	if len(names) != 1 || names[0] != "TestDashboardSmokeHiddenTabQuiet" {
		t.Errorf("нагрузочными названы %v, жду один TestDashboardSmokeHiddenTabQuiet", names)
	}
}

func TestLoadTakesWaitDelay(t *testing.T) {
	out := `--- FAIL: TestBoardRowsComeFromTaskctl (31.02s)
    board_test.go:88: tmux ls: exec: WaitDelay expired
FAIL
`
	if load, _ := Load(out); !load {
		t.Error("WaitDelay expired в блоке провала это нагрузочное падение")
	}
}

// Собственные сроки девкита говорят каждый своим глаголом («не ответил»,
// «не кончился», «не напечатал»), и все они про одно и то же: подпроцесс не
// уложился в срок. Фигура «не ... за <срок>» обязана узнавать их без
// поимённого списка, иначе новая подпись уходит в чужую красноту и съедает
// повтор слияния.
func TestLoadTakesDevkitDeadlineFamily(t *testing.T) {
	for _, out := range []string{
		`--- FAIL: TestClientLoginTrustAskedAndPassed (2.95s)
    clientlogin_test.go:368: подъём входа не прошёл вопрос доверия: 502, {"error":"клиент не напечатал ссылку авторизации за 2s: вид панели входа, видимо, сменился, разбор надо чинить"}
FAIL
`,
		`--- FAIL: TestGitRunKillsOrphan (30.01s)
    gitrun_test.go:120: git status не кончился за 2s и убит вместе с потомками
FAIL
`,
		`--- FAIL: TestSocketQuietCloses (5.02s)
    sock_test.go:44: клиент: тишина в сокете за 1.5s (read tcp 127.0.0.1)
FAIL
`,
		`--- FAIL: TestPocChromeAnswers (60.00s)
    poc_browser_test.go:20: chrome не ответил за срок
FAIL
`,
	} {
		if load, _ := Load(out); !load {
			t.Errorf("подпись срока не узналась в блоке:\n%s", out)
		}
	}
}

// Настоящая поломка признака не несёт, и вердикт обязан остаться своим:
// ложный ноль тут дороже ложной единицы.
func TestLoadRefusesPlainFailure(t *testing.T) {
	out := `--- FAIL: TestQueueOrderFollowsRank (0.01s)
    queue_test.go:40: очередь отдала DK-2 первой, жду DK-1
FAIL
`
	if load, _ := Load(out); load {
		t.Error("провал без признака срока назван нагрузочным")
	}
}

// Глагол из сроковой семьи без срока это функциональный провал, а не
// кончившееся время: «wrap не напечатал ссылку» сбоят на своей логике и под
// любой нагрузкой.
func TestLoadRefusesVerbWithoutDeadline(t *testing.T) {
	for _, out := range []string{
		`--- FAIL: TestWrapPrintsSummary (0.02s)
    wrap_test.go:15: wrap не напечатал ссылку на задачу
FAIL
`,
		`--- FAIL: TestBoardFileOpens (0.03s)
    board_test.go:22: доска не прочиталась: EOF
FAIL
`,
	} {
		if load, _ := Load(out); load {
			t.Errorf("обычный провал с глаголом из сроковой семьи назван нагрузочным:\n%s", out)
		}
	}
}

// Попытки это не стенное время: «не устоялся за 3 попыток» говорит про
// конкуренцию за файл, а не про срок, и граница держится единицей измерения.
func TestLoadRefusesAttemptsNotTime(t *testing.T) {
	out := `--- FAIL: TestLockSettles (1.20s)
    lock_test.go:57: замок demo не устоялся за 3 попыток: файл на диске меняется быстрее, чем идёт проверка
FAIL
`
	if load, _ := Load(out); load {
		t.Error("«за 3 попыток» принято за срок и названо нагрузочным падением")
	}
}

// Один признавшийся тест не прощает соседа: пока в том же компоненте
// краснеет тест без признака, компонент свой.
func TestLoadRefusesMixedBlock(t *testing.T) {
	out := quietOut + `--- FAIL: TestTasksListKeepsRank (0.02s)
    tasks_test.go:12: ранг потерялся
FAIL
`
	if load, _ := Load(out); load {
		t.Error("рядом с признавшимся краснеет тест без признака, а компонент назван нагрузочным")
	}
}

// Подтест печатается с отступом под родителем, и признание родителя должно
// доходить до него: иначе вложенный провал делал бы компонент своим.
func TestLoadKeepsSubtestsUnderParent(t *testing.T) {
	out := `--- FAIL: TestDashboardSmokeChat (60.01s)
    chat_smoke_test.go:250: ` + Mark + `
    --- FAIL: TestDashboardSmokeChat/reply (12.00s)
        chat_smoke_test.go:300: ответ не дошёл за срок
FAIL
`
	load, names := Load(out)
	if !load {
		t.Error("подтест под признавшимся родителем сделал компонент своим")
	}
	if len(names) != 1 {
		t.Errorf("блоков провала %d (%v), подтест обязан лежать в родительском", len(names), names)
	}
}

// Сборка упала, раннера сняли по сроку: имён тестов в выводе нет вовсе, и
// судится весь кусок.
func TestLoadJudgesWholeOutputWithoutFailHeads(t *testing.T) {
	out := "panic: test timed out after 10m0s\n\ngoroutine 1 [running]:\n"
	if load, names := Load(out); !load || names != nil {
		t.Errorf("вывод без блоков провала: нагрузочное=%v, имена=%v; жду true без имён", load, names)
	}
	if load, _ := Load("go: cannot find module\n"); load {
		t.Error("отказ сборки без маркера срока назван нагрузочным")
	}
}

// Python unittest называет провал своей головой, и она тоже делит вывод.
func TestFailsTakesUnittestHeads(t *testing.T) {
	out := strings.Join([]string{
		"FAIL: test_tick_pours_queue (watch_test.WatchTest)",
		"Traceback (most recent call last):",
		"AssertionError: taskctl не ответил за 30s и снят по сроку",
		"ERROR: test_other (watch_test.WatchTest)",
		"AssertionError: ранг не тот",
	}, "\n")
	fails := Fails(out)
	if len(fails) != 2 {
		t.Fatalf("блоков провала %d, жду 2: %v", len(fails), fails)
	}
	if !fails[0].Load || fails[1].Load {
		t.Errorf("признак разошёлся: первый=%v, второй=%v; жду нагрузочным только первый", fails[0].Load, fails[1].Load)
	}
	if load, _ := Load(out); load {
		t.Error("компонент с одним обычным провалом назван нагрузочным")
	}
}

// Шум раннера до первой головы в блоки не идёт: приписывать его первому же
// тесту значило бы судить его по чужому выводу.
func TestFailsDropsPreamble(t *testing.T) {
	out := "=== RUN   TestOne\n" + Mark + " (строка чужого прогона)\n--- FAIL: TestOne (0.01s)\n    one_test.go:3: не то число\n"
	fails := Fails(out)
	if len(fails) != 1 {
		t.Fatalf("блоков %d, жду 1", len(fails))
	}
	if fails[0].Load {
		t.Error("шум до головы блока попал в блок и прикрыл провал")
	}
}
