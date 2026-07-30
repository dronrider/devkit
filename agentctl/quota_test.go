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
// это (1 - used) / (left / 7 суток).
func bucketAt(name string, usedPct int, left time.Duration) bucket {
	return bucket{Name: name, Used: float64(usedPct) / 100, Reset: testNow.Add(left)}
}

func snapOf(age time.Duration, buckets ...bucket) snapshot {
	return snapshot{Taken: testNow.Add(-age), Buckets: buckets}
}

const halfWindow = quotaWindow / 2

func TestReadSnapshot(t *testing.T) {
	// Снимок пишется и руками, и по панели, а панель у разных версий клиента
	// своя: оба набора бакетов обязаны читаться без предупреждений.
	cases := []struct {
		name string
		dear string
		val  string
	}{
		{"снимок с панели 2.1.220", "week_fable", "78% сброс 2026-08-04T10:00"},
		{"снимок со старым бакетом opus", "week_opus", "78% сброс 2026-08-04T10:00"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "quota.local")
			content := "# снято руками с панели /usage\n" +
				"taken = 2026-07-30T09:00\n" +
				"week_all = 34% сброс 2026-08-04T10:00\n" +
				c.dear + " = " + c.val + "\n"
			if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
			s, err := readSnapshot(path)
			if err != nil {
				t.Fatalf("снимок не прочитан: %v", err)
			}
			if !s.Taken.Equal(time.Date(2026, 7, 30, 9, 0, 0, 0, time.Local)) {
				t.Fatalf("момент снятия %v", s.Taken)
			}
			if len(s.Buckets) != 2 {
				t.Fatalf("бакеты: %+v", s.Buckets)
			}
			b, ok := s.bucket(c.dear)
			if !ok || b.Used != 0.78 {
				t.Fatalf("%s разобран как %+v", c.dear, b)
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
	s, err := readSnapshot(path)
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
	s, err := readSnapshot(filepath.Join(t.TempDir(), "нет-такого"))
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
		{"почти исчерпан в середине окна", bucketAt("week_fable", 95, halfWindow), statusDeficit},
		{"нетронут за день до сброса", bucketAt("week_fable", 20, 24*time.Hour), statusSurplus},
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

// TestTierBuckets: лестница трат это данные, из которых корректор считает
// сдвиг, поэтому её набор проверяется отдельно от самих сдвигов.
func TestTierBuckets(t *testing.T) {
	for _, tier := range tiers {
		names := tierBuckets[tier]
		if len(names) == 0 {
			t.Fatalf("ярус %s не тратит ни из чего", tier)
		}
		if !contains(names, requiredBucket) {
			t.Fatalf("ярус %s не тратит из общего бакета: %v", tier, names)
		}
		for _, name := range names {
			if !known(name) {
				t.Fatalf("ярус %s тратит из бакета %s, а в снимке такого нет", tier, name)
			}
		}
	}
	// Отдельный бакет панель держит один и на самой дорогой модели, поэтому
	// добавочный бакет есть только у верхней ступени.
	if got := extraBuckets("fable", "opus"); len(got) != 1 || got[0] != "week_fable" {
		t.Fatalf("opus -> fable добирает %v, жду week_fable", got)
	}
	for _, pair := range [][2]string{{"sonnet", "haiku"}, {"opus", "sonnet"}} {
		if got := extraBuckets(pair[0], pair[1]); len(got) != 0 {
			t.Fatalf("%s добирает к тратам %s бакет %v, которого панель не показывает", pair[0], pair[1], got)
		}
	}
}

func TestCorrectModel(t *testing.T) {
	deficitAll := bucketAt("week_all", 90, halfWindow)
	deficitFable := bucketAt("week_fable", 90, halfWindow)
	// Профицит это выраженный перекос: почти нетронутый бакет за сутки до
	// сброса. В середине окна нетронутый бакет даёт pace ровно 2, впритык.
	surplusAll := bucketAt("week_all", 5, 24*time.Hour)
	surplusFable := bucketAt("week_fable", 5, 24*time.Hour)
	normalAll := bucketAt("week_all", 50, halfWindow)
	normalFable := bucketAt("week_fable", 50, halfWindow)
	expiredFable := bucketAt("week_fable", 5, -time.Hour)
	deficitOpus := bucketAt("week_opus", 90, halfWindow)

	cases := []struct {
		name  string
		model string
		groom bool
		snap  snapshot
		want  string
		note  string
	}{
		{"дефицит общего бакета снимает opus на ярус вниз", "opus", false,
			snapOf(time.Hour, deficitAll, normalFable), "sonnet", "дефицит week_all"},
		{"своего бакета у opus больше нет, дефицит fable его не трогает", "opus", false,
			snapOf(time.Hour, normalAll, deficitFable), "opus", ""},
		{"sonnet при дефиците уходит на haiku", "sonnet", false,
			snapOf(time.Hour, deficitAll), "haiku", "дефицит week_all"},
		{"ниже haiku двигать некуда", "haiku", false,
			snapOf(time.Hour, deficitAll), "haiku", "дефицит week_all"},
		{"fable при дефиците своего бакета уходит на opus", "fable", false,
			snapOf(time.Hour, normalAll, deficitFable), "opus", "дефицит week_fable"},
		{"fable снимает вниз и дефицит общего бакета", "fable", false,
			snapOf(time.Hour, deficitAll, normalFable), "opus", "дефицит week_all"},
		{"профицит добавочного бакета поднимает opus", "opus", false,
			snapOf(time.Hour, normalAll, surplusFable), "fable", "профицит week_fable"},
		{"haiku поднимает профицит общего бакета", "haiku", false,
			snapOf(time.Hour, surplusAll), "sonnet", "профицит week_all"},
		{"sonnet поднимает тот же общий бакет", "sonnet", false,
			snapOf(time.Hour, surplusAll, normalFable), "opus", "профицит week_all"},
		{"профицита общего бакета для opus мало", "opus", false,
			snapOf(time.Hour, surplusAll, normalFable), "opus", ""},
		{"выше fable ярусов нет", "fable", false,
			snapOf(time.Hour, surplusAll, surplusFable), "fable", ""},
		{"дефицит валиден и по старому снимку", "opus", false,
			snapOf(3*24*time.Hour, deficitAll, normalFable), "sonnet", "дефицит week_all"},
		{"профицит по старому снимку не поднимает", "sonnet", false,
			snapOf(3*24*time.Hour, surplusAll, normalFable), "sonnet", ""},
		{"снимок без момента снятия вверх не двигает", "sonnet", false,
			snapshot{Buckets: []bucket{surplusAll, normalFable}}, "sonnet", ""},
		{"дефицит сильнее профицита", "fable", false,
			snapOf(time.Hour, deficitAll, surplusFable), "opus", "дефицит week_all"},
		{"грумминговый вердикт не корректируется", "opus", true,
			snapOf(time.Hour, deficitAll, deficitFable), "opus", ""},
		{"протухший бакет не двигает", "opus", false,
			snapOf(time.Hour, normalAll, expiredFable), "opus", ""},
		{"старый бакет opus лестницу трат больше не задаёт", "opus", false,
			snapOf(time.Hour, normalAll, deficitOpus), "opus", ""},
		{"пустой снимок оставляет вердикт как есть", "opus", false,
			snapshot{}, "opus", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := correctModel(c.model, c.groom, c.snap, testNow)
			if got.Model != c.want {
				t.Fatalf("модель %q, жду %q (причина %q)", got.Model, c.want, got.Note)
			}
			if got.Note != c.note {
				t.Fatalf("причина %q, жду %q", got.Note, c.note)
			}
		})
	}
}

func TestCorrectModelBottomWarning(t *testing.T) {
	// На дне лестницы причина остаётся, а сдвига нет: предупреждение честнее
	// молчания, дешевле haiku исполнителя всё равно нет.
	c := correctModel("haiku", false, snapOf(time.Hour, bucketAt("week_all", 95, halfWindow)), testNow)
	if c.shifted() {
		t.Fatalf("сдвиг ниже haiku: %+v", c)
	}
	if !strings.Contains(c.tail(), "ниже haiku ярусов нет") {
		t.Fatalf("нет предупреждения в хвосте: %q", c.tail())
	}
}

func TestCorrectionTail(t *testing.T) {
	c := correctModel("opus", false, snapOf(time.Hour, bucketAt("week_all", 95, halfWindow),
		bucketAt("week_fable", 50, halfWindow)), testNow)
	if got := c.tail(); got != "корректор: дефицит week_all, opus -> sonnet" {
		t.Fatalf("хвост %q", got)
	}
	if tail := (correction{Model: "opus", From: "opus"}).tail(); tail != "" {
		t.Fatalf("молчащий корректор занял место в выводе: %q", tail)
	}
}

func TestCmdQuota(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "quota.local")

	t.Run("без файла подсказка вместо ошибки", func(t *testing.T) {
		out, err := cmdQuota(path, testNow)
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
			"week_fable = 95% сброс " + at(testNow.Add(halfWindow)) + "\n" +
			"week_haiku = 5% сброс " + at(testNow.Add(halfWindow)) + "\n"
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		out, err := cmdQuota(path, testNow)
		if err != nil {
			t.Fatalf("quota: %v", err)
		}
		for _, want := range []string{"возраст 3ч 0м", "week_all", "норма", "week_fable", "дефицит", "неизвестный ключ"} {
			if !strings.Contains(out, want) {
				t.Fatalf("в выводе нет %q:\n%s", want, out)
			}
		}
	})

	t.Run("снимок из будущего", func(t *testing.T) {
		content := "taken = " + at(testNow.Add(2*time.Hour)) + "\n" +
			"week_all = 50% сброс " + at(testNow.Add(halfWindow)) + "\n"
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		out, err := cmdQuota(path, testNow)
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
		content := "taken = " + at(testNow.Add(-3*24*time.Hour)) + "\n" +
			"week_all = 5% сброс " + at(testNow.Add(24*time.Hour)) + "\n"
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		out, err := cmdQuota(path, testNow)
		if err != nil {
			t.Fatalf("quota: %v", err)
		}
		if !strings.Contains(out, "профицит (снимок старше суток") {
			t.Fatalf("непомеченный профицит по старому снимку:\n%s", out)
		}
	})
}

func TestWriteSnapshotRoundTrip(t *testing.T) {
	// refresh пишет тот же формат, каким снимок заполняют руками: записанное
	// обязано читаться собственным парсером.
	path := filepath.Join(t.TempDir(), ".devkit", "quota.local")
	s := snapOf(0, bucketAt("week_all", 34, halfWindow), bucketAt("week_fable", 78, halfWindow))
	if err := writeSnapshot(path, s); err != nil {
		t.Fatalf("запись снимка: %v", err)
	}
	back, err := readSnapshot(path)
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
