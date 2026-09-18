package main

import (
	"os"
	"testing"

	"github.com/dronrider/devkit/internal/sessions"
)

// TestMain снимает с прогона ID живой сессии (DK-631). touchWork зовётся на
// pick, run, spend и budget с ключом --goal, и берётся ID сессии из
// окружения: без подмены прогон, запущенный из живого разговора, дописывал
// бы её именем строки в живой реестр сессий. Тесты, которым нужна именно
// отметка, называют ID сами через t.Setenv. Без ID отметка не пишется
// никуда: mark() в internal/sessions выходит по пустому sid раньше, чем
// тронет диск.
func TestMain(m *testing.M) {
	os.Unsetenv(sessions.SessionEnv)
	os.Exit(m.Run())
}

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

// Виток цели не двигает строку доски и мимо отметок taskctl проходит
// целиком: работой цели он называет себя сам, ключом --goal у вердикта pick
// и у замера бюджета spend/budget. ID цели берётся из имени файла (DK-112 из
// пути ".../DK-112.watch"), а не из самого пути.
func TestTouchWorkGoalKey(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(sessions.SessionEnv, "dff98764-2222-4222-8222-222222222222")
	touchWork([]string{"run", "--goal", "/дом/.devkit/goals/DK-112.watch"})
	recs := sessions.LoadAll(home)["dff98764-2222-4222-8222-222222222222"]
	if len(recs) != 1 {
		t.Fatalf("записей в реестре %d: %+v", len(recs), recs)
	}
	if recs[0].Task != "DK-112" || recs[0].Source != sessions.BySrc {
		t.Fatalf("запись работы цели: задача %q, источник %q", recs[0].Task, recs[0].Source)
	}
}

// Форма ключа через знак равенства (--goal=...) разбирается той же дорогой,
// что и раздельная.
func TestTouchWorkGoalKeyWithEquals(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(sessions.SessionEnv, "dff98764-2222-4222-8222-222222222222")
	touchWork([]string{"pick", "--goal=DK-113.watch"})
	recs := sessions.LoadAll(home)["dff98764-2222-4222-8222-222222222222"]
	if len(recs) != 1 || recs[0].Task != "DK-113" {
		t.Fatalf("ключ через = не разобран: %+v", recs)
	}
}

// Без --goal вердикт и замер бюджета работой не считаются: pick и spend
// зовутся и по задаче, а привязку задачи ведёт touchWork в taskctl, не тут.
func TestTouchWorkWithoutGoalWritesNothing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(sessions.SessionEnv, "dff98764-2222-4222-8222-222222222222")
	touchWork([]string{"run", "DK-114"})
	if all := sessions.LoadAll(home); len(all) != 0 {
		t.Errorf("без --goal запись всё равно легла: %+v", all)
	}
}

// Команда мимо touchCmds ключ --goal не отмечает: harness и list его не
// знают, и работой над целью не становятся.
func TestTouchWorkGoalIgnoresOtherCommands(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(sessions.SessionEnv, "dff98764-2222-4222-8222-222222222222")
	touchWork([]string{"harness", "--goal", "DK-115.watch"})
	if all := sessions.LoadAll(home); len(all) != 0 {
		t.Errorf("чужая команда легла работой по цели: %+v", all)
	}
}
