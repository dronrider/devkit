package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestGoalTurnGoalNeedsHidden: виток узнаётся только по скрытой записи с
// заказом оболочки целиком. Человек, набравший ту же фразу в своём чате, ведёт
// цель этим разговором, и реплика ему идёт в сессию.
func TestGoalTurnGoalNeedsHidden(t *testing.T) {
	order := "продолжай цель XR-100 по скиллу goal-loop"
	if got := goalTurnGoal(true, order); got != "XR-100" {
		t.Errorf("скрытый виток с заказом оболочки не узнан: %q", got)
	}
	if got := goalTurnGoal(false, order); got != "" {
		t.Errorf("чат человека с той же фразой принят за виток цели %q", got)
	}
	if got := goalTurnGoal(true, "продолжай цель XR-100, только сначала глянь журнал"); got != "" {
		t.Errorf("вольная реплика принята за заказ оболочки: %q", got)
	}
}

// TestGoalTurnContractWithShell: заказ витка, признак скрытия и файл имени
// витка дашборд узнаёт по тому, как их пишет goal-run.py. Разойдись они, и
// строка цели снова осталась бы без адреса, а реплика ушла бы в сессию витка.
func TestGoalTurnContractWithShell(t *testing.T) {
	src := readFile(t, filepath.Join("..", "..", "kit", "skills", "goal-loop", "goal-run.py"))
	for _, want := range []string{
		`self.prompt = "продолжай цель %s по скиллу goal-loop" % goal_id`,
		`self.devdir = os.path.join(proj, ".devkit")`,
		`self.lock = os.path.join(self.devdir, "goal-%s.lock" % goal_id)`,
		`self.sessfile = os.path.join(self.lock, "session")`,
		`cmd = "DEVKIT_HIDDEN=1 "`,
	} {
		if !strings.Contains(src, want) {
			t.Errorf("goal-run.py разошёлся с дашбордом: нет строки %q", want)
		}
	}
	if got := goalTurnGoal(true, fmt.Sprintf("продолжай цель %s по скиллу goal-loop", "XR-100")); got != "XR-100" {
		t.Errorf("заказ оболочки не узнаётся разбором дашборда: %q", got)
	}
}

// TestGoalTurnIgnoresForeignSessionFile: имя витка берётся только из замка,
// который держит живая оболочка, и только в форме uuid: мусор в файле не
// становится адресом кнопки.
func TestGoalTurnIgnoresForeignSessionFile(t *testing.T) {
	e := newTestEnv(t)
	goalLock(t, e.proj, "XR-100", deadPID(t), goalTurnSID)
	if got := goalTurn(e.proj, "XR-100"); got != "" {
		t.Errorf("брошенный замок назвал виток %q", got)
	}
	goalLock(t, e.proj, "XR-100", os.Getpid(), "../../etc/passwd")
	if got := goalTurn(e.proj, "XR-100"); got != "" {
		t.Errorf("мусор в файле имени стал адресом витка: %q", got)
	}
	goalLock(t, e.proj, "XR-100", os.Getpid(), goalTurnSID)
	if got := goalTurn(e.proj, "XR-100"); got != goalTurnSID {
		t.Errorf("живой замок не назвал виток: %q", got)
	}
}
