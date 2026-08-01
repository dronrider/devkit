package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Точка отсчёта тестов: середина недельного окна берётся от неё, чтобы pace
// считался в лоб и пороги было видно глазами.
var testNow = time.Date(2026, 7, 30, 12, 0, 0, 0, time.Local)

func at(t time.Time) string { return t.Format(quotaTimeLayout) }

// bucketAt собирает бакет по проценту потраченного и остатку окна: pace тогда
// это (1 - used) / (left / окно бакета), а окно недельного это 7 суток.
func bucketAt(name string, usedPct int, left time.Duration) bucket {
	return bucket{Name: name, Used: float64(usedPct) / 100, Reset: testNow.Add(left)}
}

// specAt это объявление квоты claude-code с подставленным путём снимка. Лестница
// трат берётся из настоящего профиля, а не из копии в тестах: разъедься они, и
// корректор проверялся бы по таблице, которой на машине нет.
func specAt(t *testing.T, path string) *quotaSpec {
	t.Helper()
	l, err := mergeLayers(filepath.Join(repoRoot(t), profileDirName), filepath.Join(t.TempDir(), "нет-конфига"), "")
	if err != nil {
		t.Fatalf("слои харнесов: %v", err)
	}
	q := quotaSpecOf(l, "claude-code")
	if q == nil {
		t.Fatal("у claude-code нет объявления квоты")
	}
	q.Path, q.From = path, path
	return q
}

func snapOf(age time.Duration, buckets ...bucket) snapshot {
	return snapshot{Taken: testNow.Add(-age), Buckets: buckets}
}

const halfWindow = weekWindow / 2

// Возрасты снимка по обе стороны порога свежести: от них зависит только сдвиг
// вверх, поэтому таблицы корректора берут их отсюда, а не пишут числа руками.
const (
	freshAge = snapshotMaxAge / 2
	staleAge = snapshotMaxAge * 2
)

func TestReadSnapshot(t *testing.T) {
	// Снимок пишется и руками, и по панели, а панель у разных версий клиента
	// своя: оба набора бакетов обязаны читаться без предупреждений.
	cases := []struct {
		name string
		key  string
		val  string
	}{
		{"снимок с панели 2.1.220", "week_max", "78% сброс 2026-08-04T10:00"},
		{"снимок со старым бакетом opus", "week_opus", "78% сброс 2026-08-04T10:00"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "quota.local")
			content := "# снято руками с панели /usage\n" +
				"taken = 2026-07-30T09:00\n" +
				"week_all = 34% сброс 2026-08-04T10:00\n" +
				c.key + " = " + c.val + "\n"
			if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
			s, err := specAt(t, path).read()
			if err != nil {
				t.Fatalf("снимок не прочитан: %v", err)
			}
			if !s.Taken.Equal(time.Date(2026, 7, 30, 9, 0, 0, 0, time.Local)) {
				t.Fatalf("момент снятия %v", s.Taken)
			}
			if len(s.Buckets) != 2 {
				t.Fatalf("бакеты: %+v", s.Buckets)
			}
			b, ok := s.bucket(c.key)
			if !ok || b.Used != 0.78 {
				t.Fatalf("%s разобран как %+v", c.key, b)
			}
			if !b.Reset.Equal(time.Date(2026, 8, 4, 10, 0, 0, 0, time.Local)) {
				t.Fatalf("дата сброса %v", b.Reset)
			}
			if len(s.Warns) != 0 {
				t.Fatalf("на чистом снимке предупреждения: %v", s.Warns)
			}
		})
	}
}

func TestReadSnapshotBroken(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "quota.local")
	content := "taken = вчера\n" +
		"week_sonnet = 10% сброс 2026-08-04T10:00\n" +
		"week_all = 34% сброс не пойми когда\n" +
		"week_opus = 78% сброс 2026-08-04T10:00\n" +
		"строка без равенства\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := specAt(t, path).read()
	if err != nil {
		t.Fatalf("битые строки не должны ронять разбор: %v", err)
	}
	// Отбрасывается ровно битое, целый бакет доезжает.
	if len(s.Buckets) != 1 || s.Buckets[0].Name != "week_opus" {
		t.Fatalf("бакеты: %+v", s.Buckets)
	}
	if !s.Taken.IsZero() {
		t.Fatalf("нечитаемый момент снятия должен остаться пустым: %v", s.Taken)
	}
	if len(s.Warns) != 4 {
		t.Fatalf("жду предупреждение на каждую битую строку, вижу %v", s.Warns)
	}
	joined := strings.Join(s.Warns, "\n")
	if !strings.Contains(joined, "неизвестный ключ снимка \"week_sonnet\"") {
		t.Fatalf("незнакомый ключ прошёл молча: %v", s.Warns)
	}
}

func TestReadSnapshotMissing(t *testing.T) {
	s, err := specAt(t, filepath.Join(t.TempDir(), "нет-такого")).read()
	if err != nil {
		t.Fatalf("отсутствие файла не ошибка: %v", err)
	}
	if len(s.Buckets) != 0 || !s.Taken.IsZero() {
		t.Fatalf("жду пустой снимок, вижу %+v", s)
	}
}

func TestBucketStatus(t *testing.T) {
	// Пороги проверяются с обеих сторон: ровно на границе статус уже стоит, на
	// процент внутрь зазора уже норма.
	cases := []struct {
		name string
		b    bucket
		want string
	}{
		{"ровно на пороге профицита", bucketAt("week_all", 0, halfWindow), statusSurplus},
		{"на процент ниже порога профицита", bucketAt("week_all", 1, halfWindow), statusNormal},
		{"ровно на пороге дефицита", bucketAt("week_all", 75, halfWindow), statusDeficit},
		{"на процент выше порога дефицита", bucketAt("week_all", 74, halfWindow), statusNormal},
		{"равномерный расход это норма", bucketAt("week_all", 50, halfWindow), statusNormal},
		{"почти исчерпан в середине окна", bucketAt("week_max", 95, halfWindow), statusDeficit},
		{"нетронут за день до сброса", bucketAt("week_max", 20, 24*time.Hour), statusSurplus},
		{"сброс уже прошёл", bucketAt("week_all", 90, -time.Hour), statusExpired},
		{"сброс ровно сейчас", bucketAt("week_all", 90, 0), statusExpired},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.b.status(testNow); got != c.want {
				t.Fatalf("статус %q (pace %.2f), жду %q", got, c.b.pace(testNow), c.want)
			}
		})
	}
}

// TestQuotaSpendLadder: лестница трат это данные, из которых корректор считает
// сдвиг, и живут они теперь в профиле харнеса. Набор проверяется отдельно от
// самих сдвигов, иначе правка профиля молча меняла бы вердикты.
func TestQuotaSpendLadder(t *testing.T) {
	q := specAt(t, "")
	for _, tier := range tierNames {
		names := q.Spend[tier]
		if len(names) == 0 {
			t.Fatalf("ярус %s не тратит ни из чего", tier)
		}
		if !contains(names, q.Required) {
			t.Fatalf("ярус %s не тратит из обязательного бакета: %v", tier, names)
		}
		for _, name := range names {
			if !q.known(name) {
				t.Fatalf("ярус %s тратит из бакета %s, а профиль такого не объявляет", tier, name)
			}
		}
	}
	// Отдельный бакет панель держит один и на самой дорогой модели: у нижних
	// ярусов наборы совпадают, и различает их только взвешенная цена расхода.
	if got := q.Spend[tierMax]; len(got) != 2 || !contains(got, "week_max") {
		t.Fatalf("ярус max тратит из %v, жду пару с week_max", got)
	}
	for _, tier := range []string{tierMini, tierBase, tierPro} {
		if got := q.Spend[tier]; len(got) != 1 || got[0] != q.Required {
			t.Fatalf("ярус %s тратит из %v, а панель у него своего бакета не показывает", tier, got)
		}
	}
	// Известный бакет вне лестницы трат: снимок со старого клиента читается, но
	// сдвига по нему нет, и вывод про это говорит отдельной пометкой.
	if !q.known("week_opus") || q.spentByTier("week_opus") {
		t.Fatalf("week_opus должен быть известен и вне лестницы трат: %v", q.Buckets)
	}
}

// TestBucketAliases: имена бакетов стали ярусными, а снимки со старым именем
// лежат на машинах и пишутся руками. Псевдоним переводит их в каноничное имя,
// иначе тот же файл читался бы как незнакомый ключ и корректор молча выключался.
func TestBucketAliases(t *testing.T) {
	q := specAt(t, "")
	if got := canonBucket("week_fable"); got != "week_max" {
		t.Fatalf("week_fable развернулся в %q", got)
	}
	if got := canonBucket("week_all"); got != "week_all" {
		t.Fatalf("каноничное имя переписано в %q", got)
	}
	// week_opus псевдонимом не стал: это другой бакет, а не week_max под старым
	// именем, и лестницу трат он не задаёт.
	if got := canonBucket("week_opus"); got != "week_opus" {
		t.Fatalf("week_opus подменён на %q", got)
	}
	s := q.parse("taken = " + at(testNow) + "\nweek_fable = 78% сброс " + at(testNow.Add(halfWindow)) + "\n")
	if len(s.Warns) != 0 {
		t.Fatalf("старое имя бакета прочиталось с предупреждением: %v", s.Warns)
	}
	if b, ok := s.bucket("week_max"); !ok || b.Used != 0.78 {
		t.Fatalf("бакет по псевдониму разобран как %+v", s.Buckets)
	}
}

// TestBucketWindow: окно берётся из префикса имени. Месячный бюджет с недельным
// окном давал бы pace всемеро больше настоящего, то есть профицит на пустом
// месте.
func TestBucketWindow(t *testing.T) {
	cases := []struct {
		name string
		want time.Duration
	}{
		{"week_all", weekWindow},
		{"week_max", weekWindow},
		{"month_all", monthWindow},
	}
	for _, c := range cases {
		if got := bucketWindow(c.name); got != c.want {
			t.Fatalf("окно %s это %v, жду %v", c.name, got, c.want)
		}
	}
	// Половина остатка за пять суток до сброса: недельному бакету это ровный
	// расход, а месячному выраженный профицит. Возьми месячный недельное окно, и
	// сдвига вверх на нём не случилось бы никогда.
	week := bucket{Name: "week_all", Used: 0.5, Reset: testNow.Add(5 * 24 * time.Hour)}
	month := bucket{Name: "month_all", Used: 0.5, Reset: testNow.Add(5 * 24 * time.Hour)}
	if week.status(testNow) != statusNormal {
		t.Fatalf("недельный бакет с половиной остатка не в норме: pace %.2f", week.pace(testNow))
	}
	if month.status(testNow) != statusSurplus {
		t.Fatalf("месячный бакет посчитан недельным окном: pace %.2f", month.pace(testNow))
	}
	if bucketPrefix("day_all") != "" {
		t.Fatal("незнакомый префикс принят за известный, окно вывелось бы молча")
	}
}

func TestCorrectTier(t *testing.T) {
	deficitAll := bucketAt("week_all", 90, halfWindow)
	deficitFable := bucketAt("week_max", 90, halfWindow)
	// Профицит это выраженный перекос: почти нетронутый бакет за сутки до
	// сброса. В середине окна нетронутый бакет даёт pace ровно 2, впритык.
	surplusAll := bucketAt("week_all", 5, 24*time.Hour)
	surplusFable := bucketAt("week_max", 5, 24*time.Hour)
	normalAll := bucketAt("week_all", 50, halfWindow)
	normalFable := bucketAt("week_max", 50, halfWindow)
	expiredFable := bucketAt("week_max", 5, -time.Hour)
	deficitOpus := bucketAt("week_opus", 90, halfWindow)

	cases := []struct {
		name  string
		tier  string
		groom bool
		snap  snapshot
		want  string
		note  string
	}{
		{"дефицит общего бакета снимает pro на ярус вниз", "pro", false,
			snapOf(freshAge, deficitAll, normalFable), "base", "дефицит week_all"},
		{"своего бакета у pro больше нет, дефицит week_max его не трогает", "pro", false,
			snapOf(freshAge, normalAll, deficitFable), "pro", ""},
		{"base при дефиците уходит на mini", "base", false,
			snapOf(freshAge, deficitAll), "mini", "дефицит week_all"},
		{"ниже mini двигать некуда", "mini", false,
			snapOf(freshAge, deficitAll), "mini", "дефицит week_all"},
		{"max при дефиците своего бакета уходит на pro", "max", false,
			snapOf(freshAge, normalAll, deficitFable), "pro", "дефицит week_max"},
		{"max снимает вниз и дефицит общего бакета", "max", false,
			snapOf(freshAge, deficitAll, normalFable), "pro", "дефицит week_all"},
		{"профицит обоих бакетов поднимает pro", "pro", false,
			snapOf(freshAge, surplusAll, surplusFable), "max", "профицит week_all, week_max"},
		{"одного профицита week_max для подъёма мало", "pro", false,
			snapOf(freshAge, normalAll, surplusFable), "pro", ""},
		{"общий бакет у границы дефицита подъём не пускает", "pro", false,
			snapOf(freshAge, bucketAt("week_all", 74, halfWindow), surplusFable), "pro", ""},
		{"mini поднимает профицит общего бакета", "mini", false,
			snapOf(freshAge, surplusAll), "base", "профицит week_all"},
		{"base поднимает тот же общий бакет", "base", false,
			snapOf(freshAge, surplusAll, normalFable), "pro", "профицит week_all"},
		{"профицита общего бакета для pro мало", "pro", false,
			snapOf(freshAge, surplusAll, normalFable), "pro", ""},
		{"выше max ярусов нет", "max", false,
			snapOf(freshAge, surplusAll, surplusFable), "max", ""},
		{"дефицит валиден и по старому снимку", "pro", false,
			snapOf(staleAge, deficitAll, normalFable), "base", "дефицит week_all"},
		{"профицит по старому снимку не поднимает", "base", false,
			snapOf(staleAge, surplusAll, normalFable), "base", ""},
		{"снимок без момента снятия вверх не двигает", "base", false,
			snapshot{Buckets: []bucket{surplusAll, normalFable}}, "base", ""},
		{"дефицит сильнее профицита", "max", false,
			snapOf(freshAge, deficitAll, surplusFable), "pro", "дефицит week_all"},
		{"грумминговый вердикт не корректируется", "pro", true,
			snapOf(freshAge, deficitAll, deficitFable), "pro", ""},
		{"протухший бакет не двигает", "pro", false,
			snapOf(freshAge, normalAll, expiredFable), "pro", ""},
		{"старый бакет week_opus лестницу трат больше не задаёт", "pro", false,
			snapOf(freshAge, normalAll, deficitOpus), "pro", ""},
		{"пустой снимок оставляет вердикт как есть", "pro", false,
			snapshot{}, "pro", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := correctTier(specAt(t, ""), c.tier, c.groom, c.snap, testNow)
			if got.Tier != c.want {
				t.Fatalf("ярус %q, жду %q (причина %q)", got.Tier, c.want, got.Note)
			}
			if got.Note != c.note {
				t.Fatalf("причина %q, жду %q", got.Note, c.note)
			}
		})
	}
}

func TestCorrectTierBottomWarning(t *testing.T) {
	// На дне лестницы причина остаётся, а сдвига нет: предупреждение честнее
	// молчания, дешевле нижней ступени исполнителя всё равно нет.
	c := correctTier(specAt(t, ""), "mini", false, snapOf(freshAge, bucketAt("week_all", 95, halfWindow)), testNow)
	if c.shifted() {
		t.Fatalf("сдвиг ниже mini: %+v", c)
	}
	if !strings.Contains(c.tail(), "ниже mini ярусов нет") {
		t.Fatalf("нет предупреждения в хвосте: %q", c.tail())
	}
}

func TestCorrectionTail(t *testing.T) {
	c := correctTier(specAt(t, ""), "pro", false, snapOf(freshAge, bucketAt("week_all", 95, halfWindow),
		bucketAt("week_max", 50, halfWindow)), testNow)
	if got := c.tail(); got != "корректор: дефицит week_all, pro -> base" {
		t.Fatalf("хвост %q", got)
	}
	if tail := (correction{Tier: "pro", From: "pro"}).tail(); tail != "" {
		t.Fatalf("молчащий корректор занял место в выводе: %q", tail)
	}
}

func TestCmdQuota(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "quota.local")

	t.Run("без файла подсказка вместо ошибки", func(t *testing.T) {
		out, err := cmdQuota(specAt(t, path), testNow)
		if err != nil {
			t.Fatalf("отсутствие снимка не ошибка: %v", err)
		}
		if !strings.Contains(out, "снимка нет") {
			t.Fatalf("вывод %q", out)
		}
	})

	t.Run("бакеты со статусами", func(t *testing.T) {
		content := "taken = " + at(testNow.Add(-3*time.Hour)) + "\n" +
			"week_all = 50% сброс " + at(testNow.Add(halfWindow)) + "\n" +
			"week_max = 95% сброс " + at(testNow.Add(halfWindow)) + "\n" +
			"week_haiku = 5% сброс " + at(testNow.Add(halfWindow)) + "\n"
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		out, err := cmdQuota(specAt(t, path), testNow)
		if err != nil {
			t.Fatalf("quota: %v", err)
		}
		for _, want := range []string{"возраст 3ч 0м", "week_all", "норма", "week_max", "дефицит", "неизвестный ключ"} {
			if !strings.Contains(out, want) {
				t.Fatalf("в выводе нет %q:\n%s", want, out)
			}
		}
	})

	t.Run("бакет вне лестницы трат идёт с пометкой", func(t *testing.T) {
		// Снимок со старого клиента доживает до новой версии, и «week_opus:
		// дефицит» без пояснения обещает сдвиг, которого не будет.
		content := "taken = " + at(testNow) + "\n" +
			"week_all = 50% сброс " + at(testNow.Add(halfWindow)) + "\n" +
			"week_opus = 95% сброс " + at(testNow.Add(halfWindow)) + "\n"
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		out, err := cmdQuota(specAt(t, path), testNow)
		if err != nil {
			t.Fatalf("quota: %v", err)
		}
		if !strings.Contains(out, "week_opus: потрачено 95%") {
			t.Fatalf("бакет со старого снимка потерялся:\n%s", out)
		}
		if !strings.Contains(out, "лестницу трат не задаёт") {
			t.Fatalf("бакет вне лестницы напечатан наравне с рабочими:\n%s", out)
		}
		for _, line := range strings.Split(out, "\n") {
			if strings.HasPrefix(line, "week_all") && strings.Contains(line, "лестницу трат не задаёт") {
				t.Fatalf("пометка уехала на рабочий бакет: %q", line)
			}
		}
	})

	t.Run("снимок из будущего", func(t *testing.T) {
		content := "taken = " + at(testNow.Add(2*time.Hour)) + "\n" +
			"week_all = 50% сброс " + at(testNow.Add(halfWindow)) + "\n"
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		out, err := cmdQuota(specAt(t, path), testNow)
		if err != nil {
			t.Fatalf("quota: %v", err)
		}
		if !strings.Contains(out, "это позже текущего времени: часы разошлись") {
			t.Fatalf("несогласованная фраза про возраст:\n%s", out)
		}
		if strings.Contains(out, "возраст") {
			t.Fatalf("возраст у снимка из будущего не считается:\n%s", out)
		}
	})

	t.Run("протухший профицит помечен", func(t *testing.T) {
		content := "taken = " + at(testNow.Add(-staleAge)) + "\n" +
			"week_all = 5% сброс " + at(testNow.Add(24*time.Hour)) + "\n"
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		out, err := cmdQuota(specAt(t, path), testNow)
		if err != nil {
			t.Fatalf("quota: %v", err)
		}
		if !strings.Contains(out, "профицит (снимок протух, вверх не двигает)") {
			t.Fatalf("непомеченный профицит по протухшему снимку:\n%s", out)
		}
		if !strings.Contains(out, "протух, вверх корректор не двинет") {
			t.Fatalf("возраст снимка напечатан без слова про протухание:\n%s", out)
		}
	})
}

func TestSnapshotFresh(t *testing.T) {
	// Порог свежести решает только судьбу сдвига вверх, поэтому проверяется с
	// обеих сторон: ровно на пороге снимок ещё рабочий, минутой позже уже нет.
	cases := []struct {
		name string
		s    snapshot
		want bool
	}{
		{"ровно на пороге", snapOf(snapshotMaxAge), true},
		{"минутой позже порога", snapOf(snapshotMaxAge + time.Minute), false},
		{"только что снятый", snapOf(0), true},
		{"без момента снятия", snapshot{Buckets: []bucket{bucketAt("week_all", 5, halfWindow)}}, false},
		{"снят позже текущего времени", snapOf(-time.Hour), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.s.fresh(testNow); got != c.want {
				t.Fatalf("fresh = %v, жду %v", got, c.want)
			}
		})
	}
}

func TestSnapshotAgeWarn(t *testing.T) {
	// Три немых случая, из-за которых корректор молча выключался: файла нет,
	// момента снятия нет, снимок протух. Рабочий снимок предупреждения не
	// получает, иначе строка вердикта зарастает шумом.
	cases := []struct {
		name string
		s    snapshot
		part string
	}{
		{"снимка нет вовсе", snapshot{}, "снимка квоты нет"},
		{"снимок без момента снятия", snapshot{Buckets: []bucket{bucketAt("week_all", 5, halfWindow)}}, "нет момента снятия"},
		{"снимок протух", snapOf(snapshotMaxAge+time.Minute, bucketAt("week_all", 5, halfWindow)), "снимок квоты снят"},
		{"снимок из будущего", snapOf(-time.Hour, bucketAt("week_all", 5, halfWindow)), "часы разошлись"},
		{"снимок рабочий", snapOf(freshAge, bucketAt("week_all", 5, halfWindow)), ""},
		{"снимок ровно на пороге", snapOf(snapshotMaxAge, bucketAt("week_all", 5, halfWindow)), ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := c.s.ageWarn("/tmp/quota.local", testNow)
			if c.part == "" {
				if got != "" {
					t.Fatalf("рабочий снимок дал предупреждение %q", got)
				}
				return
			}
			if !strings.Contains(got, c.part) {
				t.Fatalf("предупреждение %q без %q", got, c.part)
			}
			if !strings.Contains(got, "agentctl quota refresh") {
				t.Fatalf("предупреждение %q без команды, которой снимок чинится", got)
			}
		})
	}
}

func TestWriteSnapshotRoundTrip(t *testing.T) {
	// refresh пишет тот же формат, каким снимок заполняют руками: записанное
	// обязано читаться собственным парсером.
	path := filepath.Join(t.TempDir(), ".devkit", "quota.local")
	q := specAt(t, path)
	s := snapOf(0, bucketAt("week_all", 34, halfWindow), bucketAt("week_max", 78, halfWindow))
	if err := q.write(s); err != nil {
		t.Fatalf("запись снимка: %v", err)
	}
	back, err := q.read()
	if err != nil {
		t.Fatalf("чтение снимка: %v", err)
	}
	if len(back.Warns) != 0 {
		t.Fatalf("свой же формат не разобрался: %v", back.Warns)
	}
	if !back.Taken.Equal(s.Taken) || len(back.Buckets) != 2 {
		t.Fatalf("после записи и чтения %+v", back)
	}
	for i, b := range back.Buckets {
		if b != s.Buckets[i] {
			t.Fatalf("бакет %s разошёлся: %+v против %+v", b.Name, b, s.Buckets[i])
		}
	}
}

// TestCmdPickWithoutQuotaSection: у харнеса с пустой секцией [quota] снимать
// остаток нечем, и корректор для него выключен штатно. Молчать про это нельзя:
// вердикт без корректора выглядит совершенно обычным, на этом однажды и
// погорели (DK-019).
func TestCmdPickWithoutQuotaSection(t *testing.T) {
	isolateQuota(t)
	fixNow(t, testNow)
	root := writeBoard(t)
	// Профиль без объявления квоты кладётся под именем сегодняшнего харнеса:
	// резолв тогда прежний, а квоты у него нет.
	own := t.TempDir()
	if err := os.MkdirAll(filepath.Join(own, "harness"), 0o755); err != nil {
		t.Fatal(err)
	}
	text := "[detect]\nenv = \"CLAUDECODE\"\nvalue = \"1\"\nbin = \"claude\"\n\n[rules]\nmode = \"embed\"\n\n" +
		"[delegate]\nmode = \"native\"\nmap_mini = \"m1\"\nmap_base = \"m2\"\nmap_pro = \"m3\"\nmap_max = \"m4\"\n\n[hooks]\n\n[quota]\n"
	if err := os.WriteFile(filepath.Join(own, "harness", "claude-code.toml"), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEVKIT_HOME", own)
	// Снимок на месте и с дефицитом: сдвига всё равно нет, тратить из бакетов
	// объявляет профиль, а он молчит.
	writeQuota(t, quotaPath("claude-code"), 95, 50, halfWindow)
	out, err := cmdPick(root, "T-002", false, roleExec)
	if err != nil {
		t.Fatalf("pick: %v", err)
	}
	if !strings.HasPrefix(out, "model: m3\neffort: medium\ntier: pro\n") {
		t.Fatalf("вердикт двинулся без объявления квоты: %q", out)
	}
	if !strings.Contains(out, "секция [quota] пуста") || !strings.Contains(out, "без корректора") {
		t.Fatalf("выключенный корректор прошёл молча: %q", out)
	}
}

// TestCmdQuotaLegacyPath: команда quota это окно в решения корректора, и она
// обязана называть тот файл, который читает. Пока переезд не сделан, это старый
// одиночный снимок, и заметка зовёт того, кто его переложит.
func TestCmdQuotaLegacyPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	q := specAt(t, "")
	legacy := legacyQuotaPath()
	if err := os.MkdirAll(filepath.Dir(legacy), 0o755); err != nil {
		t.Fatal(err)
	}
	content := "taken = " + at(testNow) + "\nweek_all = 40% сброс " + at(testNow.Add(halfWindow)) + "\n"
	if err := os.WriteFile(legacy, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	q.Path, q.From = snapshotSource("claude-code")
	if q.From != legacy {
		t.Fatalf("читатель не увидел старый снимок: %q", q.From)
	}
	out, err := cmdQuota(q, testNow)
	if err != nil {
		t.Fatalf("quota: %v", err)
	}
	if !strings.Contains(out, legacy) || !strings.Contains(out, "devkitctl doctor --fix") {
		t.Fatalf("про старый путь не сказано:\n%s", out)
	}
	// Пишет refresh всегда в новый путь: переезд идёт в одну сторону.
	if err := q.write(snapOf(0, bucketAt("week_all", 40, halfWindow))); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(quotaPath("claude-code")); err != nil {
		t.Fatalf("снимок записан мимо нового пути: %v", err)
	}
	out, err = cmdQuota(q, testNow)
	if err != nil {
		t.Fatalf("quota: %v", err)
	}
	if strings.Contains(out, "старому пути") {
		t.Fatalf("после записи заметка про старый путь осталась:\n%s", out)
	}
}
