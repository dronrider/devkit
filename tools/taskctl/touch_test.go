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
	touchWork([]string{"move", "XR-004", "in-progress"})
	touchWork([]string{"close", "XR-005"})
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
