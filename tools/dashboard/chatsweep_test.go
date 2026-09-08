package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeDashboardConfig кладёт на диск ~/.devkit/dashboard.local: cmdChatSweep
// зовёт LoadConfig заново, своего сервера у стенда для него нет, и без файла
// поиск проектов машины (s.projects) не найдёт ни одного корня.
func writeDashboardConfig(t *testing.T, home, root string) {
	t.Helper()
	dir := filepath.Join(home, ".devkit")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "dashboard.local"),
		[]byte("root = "+root+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// Уборка накопившегося (DK-847, решено человеком 2026-09-07): разбор первой
// реплики печатает список кандидатов, человек его смотрит и соглашается, а
// признак hidden эти записи не получают, потому что подняты они были ещё до
// него. Стенд собирает синтетический дом с несколькими транскриптами и зовёт
// саму команду `dashboard chatsweep`, как её зовёт человек.

func TestChatSweepListsKnownMarkers(t *testing.T) {
	e, _ := chatEnv(t)
	writeDashboardConfig(t, e.home, filepath.Dir(e.proj))
	check := "ffff1111-1111-4111-8111-111111111111"
	round := "ffff2222-2222-4222-8222-222222222222"
	goal := "ffff3333-3333-4333-8333-333333333333"
	human := "ffff4444-4444-4444-8444-444444444444"
	writeSession(t, e.home, e.proj, "", check,
		saidLine("Прогони агентскую часть сценария проверки XR-4 на выкаченном коде", time.Now()), time.Now())
	writeSession(t, e.home, e.proj, "", round,
		saidLine("Второй круг ревью XR-4: автор ответил в тредах MR.", time.Now()), time.Now())
	writeSession(t, e.home, e.proj, "", goal,
		saidLine("продолжай цель XR-100 по скиллу goal-loop", time.Now()), time.Now())
	writeSession(t, e.home, e.proj, "", human,
		saidLine("Выполни XR-4", time.Now()), time.Now())

	var out bytes.Buffer
	if err := cmdChatSweep(e.home, nil, &out); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{check, round, goal} {
		if !strings.Contains(got, want) {
			t.Errorf("кандидат %s не назван в разборе:\n%s", want, got)
		}
	}
	if strings.Contains(got, human) {
		t.Errorf("человеческий разговор попал в кандидаты:\n%s", got)
	}
}

func TestChatSweepApplyArchives(t *testing.T) {
	e, c := chatEnv(t)
	sid := "ffff5555-5555-4555-8555-555555555555"
	writeSession(t, e.home, e.proj, "", sid,
		saidLine("Второй круг ревью XR-4: автор ответил в тредах MR.", time.Now()), time.Now())

	var out bytes.Buffer
	if err := cmdChatSweep(e.home, []string{sid}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), sid+": в архиве") {
		t.Fatalf("подтверждения уборки нет: %s", out.String())
	}
	list, _ := chatsWindow(t, e, c, "&days=0")
	got := chatOne(list, sid)
	if got == nil || !got.Archived {
		t.Fatalf("запись не ушла в архив: %+v", got)
	}
}

func TestChatSweepEmptyListSaysSo(t *testing.T) {
	e, _ := chatEnv(t)
	writeDashboardConfig(t, e.home, filepath.Dir(e.proj))
	var out bytes.Buffer
	if err := cmdChatSweep(e.home, nil, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "кандидатов на уборку не нашлось") {
		t.Fatalf("пустой разбор промолчал: %s", out.String())
	}
}
