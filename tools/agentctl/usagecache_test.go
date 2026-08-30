package main

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// usageCacheFetchedMs это момент удачного запроса из testdata/usage-cache.json.
// Держится числом, а не датой: снимок обязан идти от него, и подмена его
// временем чтения файла тут же видна.
const usageCacheFetchedMs = 1785401400000

// cacheSpec это claude-code из настоящего профиля репозитория, которому в дом
// положен кеш. Дом у каждого стенда свой, его выдаёт specAt: съёмщик читает
// именно его, домашний каталог машины стендам не виден.
func cacheSpec(t *testing.T, cache string) *quotaSpec {
	t.Helper()
	q := specAt(t, filepath.Join(t.TempDir(), "quota.local"))
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

	// Здоровый кеш без трат по дорогой модели и кеш, часть строк которого не
	// далась разбору, снаружи выглядели бы одинаково, если пометку ставить
	// безусловно. Различает их пустая причина, ровно как на дороге панели.
	t.Run("разобранный до конца кеш пометки не ставит", func(t *testing.T) {
		q := cacheSpec(t, cacheWith(`{"kind": "weekly_all", "percent": 69, "resets_at": "2026-07-31T12:00:00+00:00"}`))
		s, notes, err := snapUsageCache(q)
		if err != nil {
			t.Fatalf("кеш не разобран: %v", err)
		}
		if len(notes) != 0 {
			t.Fatalf("знакомому кешу говорить не о чем: %v", notes)
		}
		if why := s.partial("week_max"); why != "" {
			t.Fatalf("трат по модели на неделе не было, а снимок зовёт это неполнотой: %q", why)
		}
	})

	t.Run("непонятая строка кеша помечает бакет", func(t *testing.T) {
		q := cacheSpec(t, cacheWith(`
			{"kind": "weekly_all", "percent": 69, "resets_at": "2026-07-31T12:00:00+00:00"},
			{"kind": "weekly_scoped", "percent": 12, "resets_at": "2026-07-31T12:00:00+00:00",
			 "scope": {"model": {"display_name": "Chartreuse"}}}`))
		s, _, err := snapUsageCache(q)
		if err != nil {
			t.Fatalf("кеш не разобран: %v", err)
		}
		if s.partial("week_max") == "" {
			t.Fatalf("непонятая строка лимита осталась без пометки: %+v", s)
		}
	})

	// Три проверки области стоят в одном условии, и стенд гоняет каждую: без
	// своего случая мутация двух из них прошла бы незамеченной.
	t.Run("недельный лимит без имени модели называется словами", func(t *testing.T) {
		for name, scope := range map[string]string{
			"области нет":       `null`,
			"модели нет":        `{"model": null}`,
			"имя модели пустое": `{"model": {"display_name": ""}}`,
		} {
			t.Run(name, func(t *testing.T) {
				q := cacheSpec(t, cacheWith(`
					{"kind": "weekly_all", "percent": 69, "resets_at": "2026-07-31T12:00:00+00:00"},
					{"kind": "weekly_scoped", "percent": 12, "resets_at": "2026-07-31T12:00:00+00:00",
					 "scope": `+scope+`}`))
				_, notes, err := snapUsageCache(q)
				if err != nil {
					t.Fatalf("знакомая часть кеша должна разбираться: %v", err)
				}
				if !strings.Contains(strings.Join(notes, "\n"), "без имени модели") {
					t.Fatalf("про лимит без имени модели сказано не было: %v", notes)
				}
			})
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

// cacheAt собирает кеш клиента с заданным моментом удачного запроса: возраст
// кеша тут предмет проверки, и держать его прибитым к одной дате нельзя.
func cacheAt(fetched time.Time, limits string) string {
	return fmt.Sprintf(`{"cachedUsageUtilization": {"fetchedAtMs": %d, "utilization": {"limits": [%s]}}}`,
		fetched.UnixMilli(), limits)
}

// weekAllLimit и weekMaxLimit это строки лимитов кеша: общий недельный и
// добавочный по дорогой модели.
func weekAllLimit(pct int) string {
	return fmt.Sprintf(`{"kind": "weekly_all", "percent": %d, "resets_at": "2026-07-31T12:00:00+00:00"}`, pct)
}

func weekMaxLimit(pct int) string {
	return fmt.Sprintf(`{"kind": "weekly_scoped", "percent": %d, "resets_at": "2026-07-31T12:00:00+00:00", `+
		`"scope": {"model": {"display_name": "Fable"}}}`, pct)
}

// TestSnapClaudeUsageStaleCache: кеш клиента обновляется только тогда, когда
// клиент сам ходит за расходом, и в тихий час застывает на часы. Съёмщик обязан
// смотреть на его возраст, а не на одно только наличие: снимок с меткой
// четырёхчасовой давности это тот самый застывший экран, ради которого задача и
// заводилась.
func TestSnapClaudeUsageStaleCache(t *testing.T) {
	stale := testNow.Add(-4 * time.Hour).Truncate(time.Minute)

	t.Run("протухший кеш уводит на панель", func(t *testing.T) {
		q := cacheSpec(t, cacheAt(stale, weekAllLimit(79)))
		// Ни tmux, ни клиента в стенде нет: панель отказывает, и видно, что
		// съёмщик до неё дошёл.
		t.Setenv("PATH", t.TempDir())
		s, notes, err := snapClaudeUsage(q, testNow)
		if err != nil {
			t.Fatalf("протухший кеш есть, отказывать нечему: %v", err)
		}
		said := strings.Join(notes, "\n")
		if !strings.Contains(said, usageCommand) {
			t.Fatalf("съёмщик остался на протухшем кеше и на панель не пошёл: %v", notes)
		}
		if !strings.Contains(said, "4ч 0м назад") {
			t.Fatalf("возраст кеша не назван: %v", notes)
		}
		// Панель не далась, и в файл едет кеш со своим моментом снятия: он
		// честно скажет и корректору, и человеку, что цифрам четыре часа.
		if !s.Taken.Equal(stale) {
			t.Fatalf("момент снятия %v, жду момент запроса кеша %v", s.Taken, stale)
		}
	})

	t.Run("свежий кеш панель не поднимает", func(t *testing.T) {
		fresh := testNow.Add(-10 * time.Minute).Truncate(time.Minute)
		q := cacheSpec(t, cacheAt(fresh, weekAllLimit(79)))
		t.Setenv("PATH", t.TempDir())
		s, notes, err := snapClaudeUsage(q, testNow)
		if err != nil {
			t.Fatalf("свежий кеш разобран не был: %v", err)
		}
		if len(notes) != 0 {
			t.Fatalf("свежему кешу говорить не о чем: %v", notes)
		}
		if !s.Taken.Equal(fresh) {
			t.Fatalf("момент снятия %v, жду %v", s.Taken, fresh)
		}
	})
}

// TestBorrowBreakdown: общие цифры едут с панели, а разбивку по моделям панель
// отдаёт не всегда. Занятая у кеша цифра это ровно то, что в этот момент стоит
// у человека на экране клиента, и возраст её обязан ехать пометкой рядом.
func TestBorrowBreakdown(t *testing.T) {
	q := specAt(t, filepath.Join(t.TempDir(), "quota.local"))
	stale := testNow.Add(-3 * time.Hour)
	cache := snapshot{Taken: stale, Buckets: []bucket{
		{Name: "week_all", Used: 0.70, Reset: testNow.Add(halfWindow)},
		{Name: "week_max", Used: 0.17, Reset: testNow.Add(halfWindow)},
	}}
	panel := snapshot{Taken: testNow, Buckets: []bucket{
		{Name: "week_all", Used: 0.82, Reset: testNow.Add(halfWindow)},
	}}

	t.Run("недостающий бакет занимается у кеша с пометкой возраста", func(t *testing.T) {
		s, notes := borrowBreakdown(q, panel, cache, testNow, true)
		all, ok := s.bucket("week_all")
		if !ok || all.Used != 0.82 {
			t.Fatalf("общий бакет обязан остаться панельным: %+v", s.Buckets)
		}
		max, ok := s.bucket("week_max")
		if !ok || max.Used != 0.17 {
			t.Fatalf("бакет модели у кеша не занят: %+v", s.Buckets)
		}
		if why := s.borrowed("week_max"); !strings.Contains(why, "3ч 0м") {
			t.Fatalf("возраст занятой цифры не помечен: %q", why)
		}
		// Пометка «бакета нет» на найденный бакет не годится: обе строки
		// печатаются человеку, и он читал бы под цифрой, что цифры нет.
		if why := s.partial("week_max"); why != "" {
			t.Fatalf("занятый бакет помечен как недостающий: %q", why)
		}
		if len(notes) != 1 {
			t.Fatalf("заимствование прошло молча: %v", notes)
		}
		// Порядок бакетов держит профиль, а не порядок дозаписи.
		if s.Buckets[0].Name != "week_all" {
			t.Fatalf("порядок бакетов сбился: %+v", s.Buckets)
		}
	})

	t.Run("панельный бакет кешем не подменяется", func(t *testing.T) {
		full := panel
		full.Buckets = append(append([]bucket{}, panel.Buckets...),
			bucket{Name: "week_max", Used: 0.20, Reset: testNow.Add(halfWindow)})
		s, notes := borrowBreakdown(q, full, cache, testNow, true)
		if max, _ := s.bucket("week_max"); max.Used != 0.20 {
			t.Fatalf("свежая цифра панели затёрта кешевой: %+v", s.Buckets)
		}
		if len(notes) != 0 {
			t.Fatalf("занимать было нечего, а слова есть: %v", notes)
		}
	})

	t.Run("кеша нет, занимать нечего", func(t *testing.T) {
		s, notes := borrowBreakdown(q, panel, snapshot{}, testNow, false)
		if _, ok := s.bucket("week_max"); ok {
			t.Fatalf("бакет взялся из пустого кеша: %+v", s.Buckets)
		}
		if len(notes) != 0 {
			t.Fatalf("занимать было нечего, а слова есть: %v", notes)
		}
	})
}
