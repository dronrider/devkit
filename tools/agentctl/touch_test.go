package main

import (
	"testing"

	"github.com/dronrider/devkit/internal/sessions"
)

// Взятие задачи в любом чате это действие, и правило board-task обязывает
// открыть этап `agentctl stage <ID> разработка`. Тем самым разговор и называет
// себя работой по задаче: до DK-716 такой чат вёл строку, а признака работы у
// неё не было, и кнопка «Выполнить» поднимала второго исполнителя поверх
// идущей работы.
func TestTouchStageMarksWork(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(sessions.SessionEnv, "dff98764-1111-4111-8111-111111111111")
	touchWork([]string{"stage", "DK-704", "разработка"})
	recs := sessions.LoadAll(home)["dff98764-1111-4111-8111-111111111111"]
	if len(recs) != 1 {
		t.Fatalf("записей в реестре %d: %+v", len(recs), recs)
	}
	if recs[0].Task != "DK-704" || recs[0].Source != sessions.BySrc {
		t.Fatalf("запись этапа: задача %q, источник %q", recs[0].Task, recs[0].Source)
	}
	if !sessions.WorksOn(recs, "DK-704") {
		t.Error("открывший этап разговор не считается работой по задаче")
	}
}

// Чтение записи работой не считается: спросить про этап это не работать над
// задачей, и разговор бы привязывался к каждой строке, о которой справился.
func TestTouchStageShowIsNotWork(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(sessions.SessionEnv, "dff98764-1111-4111-8111-111111111111")
	touchWork([]string{"stage", "DK-704"})
	touchWork([]string{"stage", "DK-704", "--note", "чтение"})
	if recs := sessions.LoadAll(home)["dff98764-1111-4111-8111-111111111111"]; len(recs) != 0 {
		t.Errorf("чтение записи легло работой в реестр: %+v", recs)
	}
}
