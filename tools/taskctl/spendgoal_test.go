package main

// Тест бага DK-1088: признак цикла цели, переживший сам цикл. Живёт он своим
// файлом нарочно. Краснота сверяется прогоном regcheck, а перенести на старый
// код можно только тест без новых символов: соседи по spenditem_test.go зовут
// spendCarrierGoal, которой в базе ещё нет.

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dronrider/devkit/internal/sessions"
)

// sessLoop это сессия, которая вела цикл цели из чата и дожила до следующего
// дня: живой записи ~/.devkit/goals у неё уже нет.
const sessLoop = "b9a2c3d4-0000-4000-8000-000000000007"

// TestSpendCarrierGoalAfterLoop: срез за день, когда цикл цели уже кончился,
// относит ход головной сессии к статье «фон» на ID цели по носителю реестра
// чатов. До DK-1088 признак цикла жил только в записи ~/.devkit/goals, уходил
// вместе с циклом, и такой ход падал в строку «вне статей».
func TestSpendCarrierGoalAfterLoop(t *testing.T) {
	root := setup(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	old := timeNow
	defer func() { timeNow = old }()
	timeNow = func() time.Time { return spendDay(15, 0) }

	spendHead(t, home, sessLoop, map[time.Time]int{spendDay(9, 30): 4200})
	spendBind(t, home, sessLoop, sessions.Bind{Project: "synthetic", Tree: root,
		Carrier:    "цикл цели XR-200",
		Transcript: filepath.Join(home, ".claude", "projects", "slug", sessLoop+".jsonl")})

	p, err := parseSpendPeriod("2026-09-01", "2026-09-20")
	if err != nil {
		t.Fatal(err)
	}
	msg, err := cmdSpendPeriod(root, p)
	if err != nil {
		t.Fatalf("cmdSpendPeriod: %v", err)
	}
	want := []string{
		"- статья фон: ходов 1, вывод 4.2k",
		"  - XR-200: ходов 1, вывод 4.2k",
	}
	for _, w := range want {
		if !strings.Contains(msg, w) {
			t.Fatalf("в срезе нет строки %q:\n%s", w, msg)
		}
	}
	if !strings.Contains(msg, "- вне статей: ходов 0") {
		t.Fatalf("ход цикла цели ушёл вне статей:\n%s", msg)
	}
}
