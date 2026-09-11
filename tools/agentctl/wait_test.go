package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// waitEnv собирает подставное окружение: команда спрашивает переменные через
// переданную функцию, и живой os.Getenv в тестах не участвует.
func waitEnv(pairs map[string]string) func(string) string {
	return func(name string) string { return pairs[name] }
}

// waitRead читает отметку из подставного каталога тем же разбором, каким её
// читает оболочка конвейера: полями JSON, а не структурой пакета.
func waitRead(t *testing.T, dir, id string) map[string]string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, id+".json"))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("отметка не разобрана: %v", err)
	}
	return out
}

func waitNow() time.Time {
	return time.Date(2026, 9, 10, 9, 42, 0, 0, time.Local)
}

// TestWaitMarkFields: имена полей записи это договор с оболочкой
// (task-run.py, WAIT_KEYS). Переименование поля тут ломает чтение там, и
// молчаливым такое переименование быть не должно.
func TestWaitMarkFields(t *testing.T) {
	root := writeBoard(t)
	dir := t.TempDir()
	env := waitEnv(map[string]string{waitDirEnv: dir, waitSessEnv: "88a28c88-1"})

	said, err := cmdWait(root, "T-001", waitClean, root, "10m", "жду чистой доски у соседа", env, waitNow())
	if err != nil {
		t.Fatal(err)
	}
	got := waitRead(t, dir, "T-001")
	want := map[string]string{
		"task": "T-001", "session": "88a28c88-1", "kind": "чисто", "target": root,
		"since": "2026-09-10T09:42:00", "until": "2026-09-10T09:52:00",
		"note": "жду чистой доски у соседа",
	}
	for key, val := range want {
		if got[key] != val {
			t.Fatalf("поле %s это %q, ждали %q (запись %v)", key, got[key], val, got)
		}
	}
	if len(got) != len(want) {
		t.Fatalf("полей в записи %d, ждали %d: %v", len(got), len(want), got)
	}
	if !strings.Contains(said, "срок до 2026-09-10T09:52:00") || !strings.Contains(said, "воронку") {
		t.Fatalf("строка человеку не называет ни срока, ни судьбы прохода: %q", said)
	}
}

// TestWaitTimerNeedsNoTarget: голый срок это законное ожидание. Фоновое дело
// сессии своим файлом не отмечается (DK-510: слияние ушло в фон, и ждать
// оболочке было нечего, кроме времени).
func TestWaitTimerNeedsNoTarget(t *testing.T) {
	root := writeBoard(t)
	dir := t.TempDir()
	env := waitEnv(map[string]string{waitDirEnv: dir})

	if _, err := cmdWait(root, "T-001", waitTimer, "", "5m", "фоновое слияние идёт", env, waitNow()); err != nil {
		t.Fatal(err)
	}
	got := waitRead(t, dir, "T-001")
	if got["kind"] != waitTimer || got["target"] != "" {
		t.Fatalf("срок записан целью: %v", got)
	}
	if got["until"] != "2026-09-10T09:47:00" {
		t.Fatalf("срок %q, ждали 09:47:00", got["until"])
	}
}

// TestWaitTargetIsAbsolute: оболочка живёт в своей директории, а не в
// директории сессии, и относительный путь она проверяла бы не там.
func TestWaitTargetIsAbsolute(t *testing.T) {
	root := writeBoard(t)
	dir := t.TempDir()
	env := waitEnv(map[string]string{waitDirEnv: dir})

	if _, err := cmdWait(root, "T-001", waitHere, "docs/TASKS.md", "1m", "", env, waitNow()); err != nil {
		t.Fatal(err)
	}
	got := waitRead(t, dir, "T-001")
	if !filepath.IsAbs(got["target"]) {
		t.Fatalf("цель %q осталась относительной", got["target"])
	}
}

// TestWaitRefusals: отметка, которую оболочка не поймёт, не должна лечь молча.
// Ход тогда кончится ожиданием, которого никто не ждёт, и воронка снимет окно
// ровно так же, как снимала до DK-899.
func TestWaitRefusals(t *testing.T) {
	root := writeBoard(t)
	dir := t.TempDir()
	env := waitEnv(map[string]string{waitDirEnv: dir})
	cases := []struct {
		name, id, kind, target, until, want string
	}{
		{"чужой вид", "T-001", "жду", "", "10m", "условие"},
		{"условие без цели", "T-001", waitHere, "", "10m", "нужна цель"},
		{"срок с целью", "T-001", waitTimer, "/tmp", "10m", "цель не нужна"},
		{"без срока", "T-001", waitTimer, "", "", "срок ожидания"},
		{"кривой срок", "T-001", waitTimer, "", "десять минут", "не разобран"},
		{"нулевой срок", "T-001", waitTimer, "", "0s", "не больше нуля"},
		{"срок за потолком", "T-001", waitTimer, "", "3h", "больше потолка"},
		{"чужой ID", "мусор", waitTimer, "", "10m", "ID задачи"},
		{"строки нет на доске", "T-999", waitTimer, "", "10m", "на доске нет"},
		{"слита без ID", "T-001", waitMerged, "мусор", "10m", "ID задачи"},
		{"закрыта без цели", "T-001", waitClosed, "", "10m", "нужна цель"},
		{"процесс не числом", "T-001", waitProc, "sleep", "10m", "pid числом"},
		{"процесс без срока", "T-001", waitProc, "1", "", "срок ожидания"},
		{"час кривой", "T-001", waitHour, "в обед", "", "не разобран"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := cmdWait(root, c.id, c.kind, c.target, c.until, "", env, waitNow())
			if err == nil {
				t.Fatalf("отметка легла без отказа")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Fatalf("отказ %q не называет причины %q", err, c.want)
			}
			if _, serr := os.Stat(filepath.Join(dir, c.id+".json")); serr == nil {
				t.Fatalf("отказ оставил после себя файл отметки")
			}
		})
	}
}

// TestWaitCleanNeedsATree: условие «чисто» проверяется в рабочем дереве, и
// путь, которого нет, оболочка проверяла бы до самого срока впустую.
func TestWaitCleanNeedsATree(t *testing.T) {
	root := writeBoard(t)
	dir := t.TempDir()
	env := waitEnv(map[string]string{waitDirEnv: dir})

	_, err := cmdWait(root, "T-001", waitClean, filepath.Join(root, "нет-такого"), "10m", "", env, waitNow())
	if err == nil || !strings.Contains(err.Error(), "нужна директория") {
		t.Fatalf("ожидание чистоты несуществующего дерева прошло: %v", err)
	}
}

// TestWaitOverwritesTheOldMark: ожиданий у задачи много, а запись одна.
// Свежая отметка обязана перебивать прошлую целиком, иначе оболочка читала бы
// вчерашнее условие.
func TestWaitOverwritesTheOldMark(t *testing.T) {
	root := writeBoard(t)
	dir := t.TempDir()
	env := waitEnv(map[string]string{waitDirEnv: dir})

	if _, err := cmdWait(root, "T-001", waitClean, root, "10m", "первое", env, waitNow()); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdWait(root, "T-001", waitTimer, "", "1m", "", env, waitNow().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	got := waitRead(t, dir, "T-001")
	if got["kind"] != waitTimer || got["target"] != "" || got["note"] != "" {
		t.Fatalf("прошлая отметка проступила сквозь свежую: %v", got)
	}
	if got["since"] != "2026-09-10T09:43:00" {
		t.Fatalf("время отметки %q, ждали 09:43:00", got["since"])
	}
}
