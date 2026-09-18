package main

import (
	"testing"

	"github.com/dronrider/devkit/internal/sessions"
)

// Движение кода задачи это работа над ней ровно так же, как движение строки
// доски (POC ветки poc-chat): сессия, заведшая ветку, слившая её или
// откатившая выкат, называет себя в реестре сама. Тестов на touchWork у
// shipctl не было вовсе (DK-631), хотя зовётся она из main на каждую
// команду.
func TestTouchWorkMarksTask(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(sessions.SessionEnv, "aaaa1111-1111-4111-8111-111111111111")
	touchWork([]string{"start", "XR-005", "--slug", "хвост"})
	recs := sessions.LoadAll(home)["aaaa1111-1111-4111-8111-111111111111"]
	if len(recs) != 1 {
		t.Fatalf("записей в реестре %d: %+v", len(recs), recs)
	}
	if recs[0].Task != "XR-005" || recs[0].Source != sessions.BySrc {
		t.Fatalf("запись работы: задача %q, источник %q", recs[0].Task, recs[0].Source)
	}
}

// ID стоит первым похожим словом после команды: флаг перед ним (форма
// «start --slug хвост XR-005», починка DK-236) не должен читаться как задача,
// а сама задача находится, где бы она ни встала.
func TestTouchWorkFindsIDPastFlags(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(sessions.SessionEnv, "aaaa1111-1111-4111-8111-111111111111")
	touchWork([]string{"start", "--slug", "хвост", "XR-006"})
	recs := sessions.LoadAll(home)["aaaa1111-1111-4111-8111-111111111111"]
	if len(recs) != 1 || recs[0].Task != "XR-006" {
		t.Fatalf("работа не нашла задачу за флагом: %+v", recs)
	}
}

// Команды чтения (status и всё, чего нет в touchCmds) работой не считаются:
// справиться о состоянии это не работать над задачей.
func TestTouchWorkIgnoresReadCommands(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(sessions.SessionEnv, "aaaa1111-1111-4111-8111-111111111111")
	touchWork([]string{"status", "XR-005"})
	if recs := sessions.LoadAll(home)["aaaa1111-1111-4111-8111-111111111111"]; len(recs) != 0 {
		t.Errorf("чтение статуса легло работой в реестр: %+v", recs)
	}
}

// Без ID живой сессии в окружении отметка не пишется никуда: это и есть
// рубеж, которым гигиена прогона держит живой реестр в стороне от тестов
// (DK-631).
func TestTouchWorkWithoutSessionIDWritesNothing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	touchWork([]string{"merge", "XR-005"})
	if all := sessions.LoadAll(home); len(all) != 0 {
		t.Errorf("без ID сессии запись всё равно легла: %+v", all)
	}
}
