package main

import (
	"strings"
	"testing"
)

// Второй круг чужого ревью (DK-759). Стенд тот же, что у подъёма прогона
// сценария: синтетическая доска, журналируемый tmux, фикстура вердикта.

// roundOne гоняет второй круг по одной строке на готовом стенде.
func roundOne(t *testing.T, e *testEnv, id string) checkRunReport {
	t.Helper()
	raw, err := e.s.projectBoard(e.proj)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := parseBoardRows(raw)
	if err != nil {
		t.Fatal(err)
	}
	return e.s.reviewRound(&Project{Name: "demo", Path: e.proj}, id, rows)
}

// Живой сессии задачи нет: второй круг поднимает свою, ту же tmux-сессию
// task-<ID>, что кнопка экрана, и заказ называет второй круг словами.
func TestRoundRaisesSession(t *testing.T) {
	e, tmuxLog := checkEnv(t, "")

	rep := roundOne(t, e, "XR-004")
	if !rep.Raised || rep.Failed {
		t.Fatalf("второй круг не начат: %+v", rep)
	}
	got := readFile(t, tmuxLog)
	if !strings.Contains(got, "new-session -d -s task-XR-004") {
		t.Fatalf("tmux позван не так: %q", got)
	}
	for _, want := range []string{"Второй круг ревью XR-004", "XR-004.review.md", "спор: "} {
		if !strings.Contains(got, want) {
			t.Errorf("в заказе второго круга нет %q: %s", want, got)
		}
	}
}

// Живая сессия задачи получает второй круг репликой в своё окно (DK-724): в
// ней лежит первый круг, и вторая сессия читала бы тот же дифф заново.
func TestRoundSendsToLiveSession(t *testing.T) {
	e, tmuxLog := checkEnv(t, "task-XR-004\\n")

	rep := roundOne(t, e, "XR-004")
	if !rep.Raised || rep.Failed {
		t.Fatalf("второй круг не подан: %+v", rep)
	}
	if !strings.Contains(rep.Line, "репликой в живую сессию task-XR-004") {
		t.Errorf("отчёт не назвал дорогу: %q", rep.Line)
	}
	got := readFile(t, tmuxLog)
	if strings.Contains(got, "new-session") {
		t.Errorf("поверх живой сессии поднята вторая: %s", got)
	}
	if !strings.Contains(got, "send-keys -t =task-XR-004:") {
		t.Fatalf("реплика не ушла клавиатурой окна: %s", got)
	}
}

// Строки нет на доске: второй круг заказывать некому, и это поломка, а не
// молчаливый пропуск.
func TestRoundRefusesUnknownRow(t *testing.T) {
	e, _ := checkEnv(t, "")

	rep := roundOne(t, e, "XR-777")
	if !rep.Failed || rep.Raised {
		t.Fatalf("второй круг по несуществующей строке: %+v", rep)
	}
	if !strings.Contains(rep.Line, "строки нет на доске") {
		t.Errorf("отчёт не назвал причину: %q", rep.Line)
	}
}
