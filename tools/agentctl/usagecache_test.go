package main

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// usageCacheFetchedMs это момент удачного запроса из testdata/usage-cache.json.
// Держится числом, а не датой: снимок обязан идти от него, и подмена его
// временем чтения файла тут же видна.
const usageCacheFetchedMs = 1785401400000

// cacheSpec это claude-code из настоящего профиля репозитория, которому дом
// подменён каталогом с кешем. Дом отдельный на каждый стенд: съёмщик читает
// именно его, а не домашний каталог машины.
func cacheSpec(t *testing.T, cache string) *quotaSpec {
	t.Helper()
	q := specAt(t, filepath.Join(t.TempDir(), "quota.local"))
	q.Home = t.TempDir()
	if cache != "" {
		writeFile(t, q.Home, usageCacheFile, cache)
	}
	return q
}

func TestSnapUsageCache(t *testing.T) {
	t.Run("кеш клиента даёт бакеты и момент запроса", func(t *testing.T) {
		q := cacheSpec(t, readFixture(t, "usage-cache.json"))
		s, notes, err := snapUsageCache(q)
		if err != nil {
			t.Fatalf("кеш не разобран: %v", err)
		}
		if len(notes) != 0 {
			t.Fatalf("знакомый кеш говорить не о чем: %v", notes)
		}
		all, ok := s.bucket("week_all")
		if !ok || all.Used != 0.69 {
			t.Fatalf("общий бакет: %+v", s.Buckets)
		}
		// Ровно та разбивка, которую человек видит на экране клиента и которой
		// в разобранной панели не было.
		max, ok := s.bucket("week_max")
		if !ok || max.Used != 0.10 {
			t.Fatalf("бакет модели: %+v", s.Buckets)
		}
		if len(s.Buckets) != 2 {
			t.Fatalf("пятичасовая сессия в снимок не пишется: %+v", s.Buckets)
		}
		want, err := time.Parse(time.RFC3339, "2026-07-31T12:00:00+00:00")
		if err != nil {
			t.Fatal(err)
		}
		if !all.Reset.Equal(want) {
			t.Fatalf("сброс %v, жду %v", all.Reset, want)
		}
		// Момент снятия идёт от fetchedAtMs. Время записи файла тут ни при чём:
		// свежая метка над старыми цифрами это та же ложь, от которой уходила
		// DK-584.
		if !s.Taken.Equal(time.UnixMilli(usageCacheFetchedMs)) {
			t.Fatalf("момент снятия %v, жду %v", s.Taken, time.UnixMilli(usageCacheFetchedMs))
		}
	})

	t.Run("порядок бакетов от профиля, а не от файла", func(t *testing.T) {
		q := cacheSpec(t, cacheWith(`
			{"kind": "weekly_scoped", "percent": 10, "resets_at": "2026-07-31T12:00:00+00:00",
			 "scope": {"model": {"display_name": "Fable"}}},
			{"kind": "weekly_all", "percent": 69, "resets_at": "2026-07-31T12:00:00+00:00"}`))
		s, _, err := snapUsageCache(q)
		if err != nil {
			t.Fatalf("кеш не разобран: %v", err)
		}
		if s.Buckets[0].Name != "week_all" || s.Buckets[1].Name != "week_max" {
			t.Fatalf("порядок бакетов: %+v", s.Buckets)
		}
	})

	t.Run("кеш без разбивки помечает бакет", func(t *testing.T) {
		q := cacheSpec(t, cacheWith(`{"kind": "weekly_all", "percent": 69, "resets_at": "2026-07-31T12:00:00+00:00"}`))
		s, _, err := snapUsageCache(q)
		if err != nil {
			t.Fatalf("кеш не разобран: %v", err)
		}
		if s.partial("week_max") == "" {
			t.Fatalf("пропажа дорогого бакета осталась без пометки: %+v", s)
		}
	})

	t.Run("незнакомый вид лимита называется словами", func(t *testing.T) {
		q := cacheSpec(t, cacheWith(`
			{"kind": "weekly_all", "percent": 69, "resets_at": "2026-07-31T12:00:00+00:00"},
			{"kind": "fortnightly_all", "percent": 12, "resets_at": "2026-07-31T12:00:00+00:00"}`))
		s, notes, err := snapUsageCache(q)
		if err != nil {
			t.Fatalf("знакомая часть кеша должна разбираться: %v", err)
		}
		if _, ok := s.bucket("week_all"); !ok {
			t.Fatalf("общий бакет потерян: %+v", s.Buckets)
		}
		if !strings.Contains(strings.Join(notes, "\n"), "fortnightly_all") {
			t.Fatalf("про незнакомый вид лимита сказано не было: %v", notes)
		}
	})

	t.Run("незнакомая модель называется словами", func(t *testing.T) {
		q := cacheSpec(t, cacheWith(`
			{"kind": "weekly_all", "percent": 69, "resets_at": "2026-07-31T12:00:00+00:00"},
			{"kind": "weekly_scoped", "percent": 12, "resets_at": "2026-07-31T12:00:00+00:00",
			 "scope": {"model": {"display_name": "Chartreuse"}}}`))
		_, notes, err := snapUsageCache(q)
		if err != nil {
			t.Fatalf("знакомая часть кеша должна разбираться: %v", err)
		}
		if !strings.Contains(strings.Join(notes, "\n"), "Chartreuse") {
			t.Fatalf("про незнакомую модель сказано не было: %v", notes)
		}
	})

	t.Run("кеш без момента запроса отказывает", func(t *testing.T) {
		cache := strings.Replace(readFixture(t, "usage-cache.json"), `"fetchedAtMs": 1785401400000,`, "", 1)
		q := cacheSpec(t, cache)
		_, _, err := snapUsageCache(q)
		if err == nil || !strings.Contains(err.Error(), "fetchedAtMs") {
			t.Fatalf("кеш неизвестного возраста должен отказывать словами, вышло %v", err)
		}
	})

	t.Run("кеш без обязательного бакета отказывает", func(t *testing.T) {
		q := cacheSpec(t, cacheWith(`{"kind": "session", "percent": 9, "resets_at": "2026-07-30T16:10:00+00:00"}`))
		_, _, err := snapUsageCache(q)
		if err == nil || !strings.Contains(err.Error(), "week_all") {
			t.Fatalf("кеш без общего бакета должен отказывать словами, вышло %v", err)
		}
	})

	t.Run("сменившийся формат называется словами", func(t *testing.T) {
		q := cacheSpec(t, `{"numStartups": 412}`)
		_, _, err := snapUsageCache(q)
		if err == nil || !strings.Contains(err.Error(), usageCacheKey) {
			t.Fatalf("пропажу ключа надо называть, вышло %v", err)
		}
	})

	t.Run("кеша нет, назван путь", func(t *testing.T) {
		q := cacheSpec(t, "")
		_, _, err := snapUsageCache(q)
		if err == nil || !strings.Contains(err.Error(), filepath.Join(q.Home, usageCacheFile)) {
			t.Fatalf("отказ должен называть файл, вышло %v", err)
		}
	})

	t.Run("дом харнеса выбирает кеш", func(t *testing.T) {
		q := cacheSpec(t, cacheWith(`{"kind": "weekly_all", "percent": 42, "resets_at": "2026-07-31T12:00:00+00:00"}`))
		other := cacheSpec(t, cacheWith(`{"kind": "weekly_all", "percent": 7, "resets_at": "2026-07-31T12:00:00+00:00"}`))
		s, _, err := snapUsageCache(other)
		if err != nil {
			t.Fatalf("кеш не разобран: %v", err)
		}
		all, _ := s.bucket("week_all")
		if all.Used != 0.07 {
			t.Fatalf("прочитан кеш чужого дома: %+v, дом %s", s.Buckets, q.Home)
		}
	})
}

// TestSnapClaudeUsageFallsBack: кеша нет, tmux и клиента в стенде тоже нет, и
// съёмщик обязан сказать обе причины сразу. Молчащий отказ отсюда неотличим от
// поломки разбора панели.
func TestSnapClaudeUsageFallsBack(t *testing.T) {
	q := cacheSpec(t, "")
	t.Setenv("PATH", t.TempDir())
	_, notes, err := snapClaudeUsage(q, testNow)
	if err == nil {
		t.Fatal("без кеша и без клиента снимать нечем, а отказа нет")
	}
	if !strings.Contains(err.Error(), usageCacheFile) {
		t.Fatalf("в отказе нет причины по кешу: %v", err)
	}
	if !strings.Contains(strings.Join(notes, "\n"), usageCommand) {
		t.Fatalf("смена дороги прошла молча: %v", notes)
	}
}

// cacheWith собирает кеш вокруг заданных строк лимитов: стенды отличаются
// только ими, и полный файл в каждом был бы шумом.
func cacheWith(limits string) string {
	return `{"cachedUsageUtilization": {"fetchedAtMs": 1785401400000, "utilization": {"limits": [` + limits + `]}}}`
}
