package main

import (
	"os"
	"os/user"
	"path/filepath"
	"testing"

	"github.com/dronrider/devkit/internal/sessions"
)

// Прогон тестов не пишет в живой реестр сессий. Команды доски отмечают работу
// сессии строкой в реестре, и берётся ID сессии из окружения: прогон,
// запущенный из живого разговора, дописывал её именем десятки записей в живой
// ~/.devkit/sessions.log деревьями /var/folders/..., реестр забывал имя tmux
// этой сессии, и дашборд переставал видеть, чем её снимать (живой случай
// chat-DK-397-1, замечание пользователя). ID снимает TestMain на весь бинарь
// прогона, и стенд сторожит именно это: без ID отметка работы не пишется
// никуда, а живой реестр остаётся строка в строку прежним.
func TestRunLeavesLiveRegistryAlone(t *testing.T) {
	if sid := os.Getenv(sessions.SessionEnv); sid != "" {
		t.Errorf("прогон унаследовал ID живой сессии %q: её записи станут записями прогона", sid)
	}
	live := liveRegistry(t)
	was := lines(live)
	// Отметка работы во время прогона: живого реестра она не касается.
	touchWork([]string{"move", "XR-004", "in-progress"}, nil)
	touchWork([]string{"close", "XR-005"}, nil)
	if got := lines(live); got != was {
		t.Errorf("прогон дописал живой реестр %s: было %d строк, стало %d", live, was, got)
	}
}

// liveRegistry это реестр живого дома человека, а не дома прогона: сторожим мы
// именно его.
func liveRegistry(t *testing.T) string {
	t.Helper()
	home := ""
	if u, err := user.Current(); err == nil {
		home = u.HomeDir
	}
	if home == "" {
		home = os.Getenv("HOME")
	}
	if home == "" {
		t.Skip("дома человека не видно: сторожить нечего")
	}
	return filepath.Join(home, ".devkit", sessions.Rel)
}

func lines(path string) int {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	n := 0
	for _, b := range data {
		if b == '\n' {
			n++
		}
	}
	return n
}

// Конец работы над задачей отмечается в реестре так же, как её начало
// (DK-716): закрытие строки и перевод её из In progress кладут «снята», и
// строка отдаёт «Стоп» обратно кнопке запуска. Возврат в In progress работой
// и остаётся: строку двигает тот, кто её берёт.
func TestTouchDoneOnCloseAndMoveOut(t *testing.T) {
	cases := []struct {
		args []string
		done bool
	}{
		{[]string{"close", "XR-005"}, true},
		{[]string{"move", "XR-005", "check"}, true},
		{[]string{"move", "XR-005", "Backlog"}, true},
		{[]string{"move", "XR-005", "in-progress"}, false},
		{[]string{"move", "XR-005", "In progress"}, false},
		// Ключи встают где угодно, и статусом читается первое слово за ID, а
		// не третий аргумент подряд.
		{[]string{"move", "XR-005", "--push", "check"}, true},
		{[]string{"move", "--push", "XR-005", "in-progress"}, false},
		{[]string{"ask", "XR-005"}, false},
	}
	for _, c := range cases {
		at := 1
		for i, a := range c.args[1:] {
			if touchIDRe.MatchString(a) {
				at = i + 1
				break
			}
		}
		if got := touchDone(c.args, at); got != c.done {
			t.Errorf("%v: конец работы %v, ожидал %v", c.args, got, c.done)
		}
	}
}

// Отметка едет в реестр целиком: сессия, закрывшая задачу, кладёт именную
// «снята», а не отвязывает себя от всех своих задач разом.
func TestTouchWorkWritesRelease(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(sessions.SessionEnv, "aaaa1111-1111-4111-8111-111111111111")
	touchWork([]string{"move", "XR-005", "in-progress"}, nil)
	touchWork([]string{"close", "XR-005"}, nil)
	recs := sessions.LoadAll(home)["aaaa1111-1111-4111-8111-111111111111"]
	if len(recs) != 2 {
		t.Fatalf("записей в реестре %d: %+v", len(recs), recs)
	}
	if recs[0].Source != sessions.BySrc || recs[1].Source != sessions.ByOff {
		t.Fatalf("источники записей: %q и %q", recs[0].Source, recs[1].Source)
	}
	if recs[1].Task != "XR-005" {
		t.Errorf("отвязка не назвала задачу: %q, и сняла бы всю сессию", recs[1].Task)
	}
	if len(sessions.Works(recs)) != 0 {
		t.Errorf("после закрытия строка осталась рабочей: %+v", sessions.Works(recs))
	}
}

// Холостой прогон реестра не касается ни в какую сторону (DK-1007). Ту же
// команду `move <ID> check --dry-run` вызывают и руками, и подпроцессом из
// shipctl по всему составу поезда. Доску такой прогон не двигает, и снимать
// сессию со строки ему тем более нечем. Обратная сторона та же. Холостой
// перевод в In progress привязки не заводит.
func TestDryRunLeavesRegistryAlone(t *testing.T) {
	const sid = "aaaa1111-1111-4111-8111-111111111111"
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(sessions.SessionEnv, sid)
	touchWork([]string{"move", "XR-005", "in-progress"}, nil)
	dry := [][]string{
		{"move", "XR-005", "check", "--dry-run"},
		{"move", "XR-005", "--dry-run", "check"},
		{"move", "--dry-run", "XR-006", "in-progress"},
		{"move", "XR-006", "in-progress", "-dry-run"},
		{"move", "XR-006", "in-progress", "--dry-run=true"},
	}
	for _, args := range dry {
		touchWork(args, nil)
	}
	recs := sessions.LoadAll(home)[sid]
	if len(recs) != 1 {
		t.Fatalf("холостой прогон дописал реестр, записей %d: %+v", len(recs), recs)
	}
	works := sessions.Works(recs)
	if len(works) != 1 || works[0] != "XR-005" {
		t.Fatalf("после холостого прогона сессия работает над %v, ожидал [XR-005]", works)
	}
}
