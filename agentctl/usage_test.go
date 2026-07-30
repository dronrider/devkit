package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func readFixture(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestParseUsagePanel(t *testing.T) {
	// Панели старых клиентов с бакетом Opus разбираются по-прежнему: у разных
	// версий клиента и тарифов панель своя, и новая не отменяет старую.
	t.Run("обратный отсчёт до сброса", func(t *testing.T) {
		s, err := parseUsagePanel(readFixture(t, "usage-panel-countdown.txt"), testNow)
		if err != nil {
			t.Fatalf("панель не разобрана: %v", err)
		}
		if len(s.Buckets) != 2 {
			t.Fatalf("бакеты: %+v", s.Buckets)
		}
		all, _ := s.bucket("week_all")
		opus, _ := s.bucket("week_opus")
		if all.Used != 0.34 || opus.Used != 0.78 {
			t.Fatalf("проценты разобраны как %.2f и %.2f", all.Used, opus.Used)
		}
		// Пятичасовая сессия в снимок не идёт, её процент не должен утечь в
		// первый недельный бакет.
		want := testNow.Add(4*24*time.Hour + 19*time.Hour)
		if !all.Reset.Equal(want) || !opus.Reset.Equal(want) {
			t.Fatalf("сброс %v и %v, жду %v", all.Reset, opus.Reset, want)
		}
		if !s.Taken.Equal(testNow) {
			t.Fatalf("момент снятия %v", s.Taken)
		}
	})

	t.Run("календарная дата сброса", func(t *testing.T) {
		s, err := parseUsagePanel(readFixture(t, "usage-panel-dates.txt"), testNow)
		if err != nil {
			t.Fatalf("панель не разобрана: %v", err)
		}
		all, _ := s.bucket("week_all")
		opus, _ := s.bucket("week_opus")
		if all.Used != 0.91 || opus.Used != 0.62 {
			t.Fatalf("проценты разобраны как %.2f и %.2f", all.Used, opus.Used)
		}
		// Панель показывает пояс аккаунта, и момент сброса от пояса машины не
		// зависит: 10:00 в Лос-Анджелесе это не местные 10:00.
		want := time.Date(2026, 8, 4, 10, 0, 0, 0, mustLoad(t, "America/Los_Angeles"))
		if !all.Reset.Equal(want) {
			t.Fatalf("сброс %v, жду %v", all.Reset, want)
		}
	})

	t.Run("панель клиента 2.1.220: вместо opus бакет fable", func(t *testing.T) {
		s, err := parseUsagePanel(readFixture(t, "usage-panel-fable.txt"), testNow)
		if err != nil {
			t.Fatalf("панель не разобрана: %v", err)
		}
		if len(s.Buckets) != 2 {
			t.Fatalf("бакеты: %+v", s.Buckets)
		}
		all, _ := s.bucket("week_all")
		fable, _ := s.bucket("week_fable")
		// Промо-строка «+50% weekly limits promo» стоит внутри секции всех
		// моделей, и её процент не должен подменить настоящий.
		if all.Used != 0.41 || fable.Used != 0.70 {
			t.Fatalf("проценты разобраны как %.2f и %.2f", all.Used, fable.Used)
		}
		if _, ok := s.bucket("week_opus"); ok {
			t.Fatal("бакет opus выдуман на панели, где его нет")
		}
		want := time.Date(2026, 8, 3, 15, 0, 0, 0, mustLoad(t, "Europe/Moscow"))
		if !all.Reset.Equal(want) || !fable.Reset.Equal(want) {
			t.Fatalf("сброс %v и %v, жду %v", all.Reset, fable.Reset, want)
		}
	})

	t.Run("промо-строка секции не притворяется заголовком", func(t *testing.T) {
		// Слово «weekly» есть и в промо, и в подсказке внизу панели: пройди они
		// за заголовок, в бакет уехал бы процент промо.
		panel := "Current week (all models)\n +50% weekly limits promo through Aug 19\n 41% used\n Resets in 2d\n" +
			" d to day   w to week\n"
		s, err := parseUsagePanel(panel, testNow)
		if err != nil {
			t.Fatalf("панель не разобрана: %v", err)
		}
		if all, _ := s.bucket("week_all"); all.Used != 0.41 {
			t.Fatalf("в бакет уехал %.2f вместо 0.41", all.Used)
		}
	})

	t.Run("дорогого бакета в панели может не быть", func(t *testing.T) {
		panel := "Current session\n 48% used\n Resets in 1h 43m\n" +
			"Current week (all models)\n 34% used\n Resets in 2d\n"
		s, err := parseUsagePanel(panel, testNow)
		if err != nil {
			t.Fatalf("панель с одним недельным бакетом это не отказ: %v", err)
		}
		if len(s.Buckets) != 1 || s.Buckets[0].Name != "week_all" {
			t.Fatalf("бакеты: %+v", s.Buckets)
		}
	})

	t.Run("без общего бакета снимок не пишется", func(t *testing.T) {
		panel := "Current week (Fable)\n 70% used\n Resets in 2d\n"
		_, err := parseUsagePanel(panel, testNow)
		if err == nil {
			t.Fatal("жду отказ: общий бакет обязателен")
		}
		if !strings.Contains(err.Error(), "не тронут") {
			t.Fatalf("отказ без обещания не трогать снимок: %v", err)
		}
	})

	t.Run("бакет без даты сброса это отказ", func(t *testing.T) {
		panel := "Current week (all models)\n 34% used\n Resets in 2d\n" +
			"Current week (Fable)\n 70% used\n"
		if _, err := parseUsagePanel(panel, testNow); err == nil {
			t.Fatal("жду отказ: бакет без сброса разобрать нечем")
		}
	})

	t.Run("бакет без процента это отказ, а не нетронутый бакет", func(t *testing.T) {
		panel := "Current week (all models)\n [полоска без цифр]\n Resets in 2d\n"
		if _, err := parseUsagePanel(panel, testNow); err == nil {
			t.Fatal("непрочитанный процент записался нулём, то есть профицитом")
		}
	})

	t.Run("нетронутый бакет не перезаписывается соседним процентом", func(t *testing.T) {
		// Честные 0% used ничем не отличаются от пустого поля, если считать
		// признаком само значение: следующий процент секции затирал бы ноль.
		panel := "Current week (all models)\n 0% used\n Resets in 2d\n Extra usage: 15% of monthly cap\n" +
			"Current week (Opus)\n 0% used\n Resets in 2d\n"
		s, err := parseUsagePanel(panel, testNow)
		if err != nil {
			t.Fatalf("панель не разобрана: %v", err)
		}
		if all, _ := s.bucket("week_all"); all.Used != 0 {
			t.Fatalf("нетронутый бакет разобран как %.2f потраченного", all.Used)
		}
	})

	t.Run("панель без недельных лимитов это отказ", func(t *testing.T) {
		_, err := parseUsagePanel(readFixture(t, "usage-panel-nolimits.txt"), testNow)
		if err == nil {
			t.Fatal("жду отказ: записывать в снимок нечего")
		}
		if !strings.Contains(err.Error(), "не тронут") {
			t.Fatalf("отказ без обещания не трогать снимок: %v", err)
		}
	})

	t.Run("цвета панели разбору не мешают", func(t *testing.T) {
		raw := readFixture(t, "usage-panel-countdown.txt")
		colored := strings.ReplaceAll(raw, "%", "\x1b[0m%\x1b[38;5;208m")
		s, err := parseUsagePanel(colored, testNow)
		if err != nil {
			t.Fatalf("панель с управляющими последовательностями не разобрана: %v", err)
		}
		if all, _ := s.bucket("week_all"); all.Used != 0.34 {
			t.Fatalf("процент из раскрашенной панели %.2f", all.Used)
		}
	})

	t.Run("остаток вместо потраченного переворачивается", func(t *testing.T) {
		panel := "Current week (all models)\n 66% left\n Resets in 2d\n" +
			"Current week (Opus)\n 10% left\n Resets in 2d\n"
		s, err := parseUsagePanel(panel, testNow)
		if err != nil {
			t.Fatalf("панель не разобрана: %v", err)
		}
		if all, _ := s.bucket("week_all"); all.Used != 0.34 {
			t.Fatalf("остаток не перевёрнут: %.2f", all.Used)
		}
	})
}

func TestParseResetTime(t *testing.T) {
	cases := []struct {
		line string
		want time.Time
	}{
		{"Resets Tue Aug 4 at 10:00am", time.Date(2026, 8, 4, 10, 0, 0, 0, time.Local)},
		{"Resets Aug 4, 10:00 AM", time.Date(2026, 8, 4, 10, 0, 0, 0, time.Local)},
		{"Resets August 4 at 10:00", time.Date(2026, 8, 4, 10, 0, 0, 0, time.Local)},
		{"Resets Aug 4 at 7pm", time.Date(2026, 8, 4, 19, 0, 0, 0, time.Local)},
		{"Resets Aug 4 at 12am", time.Date(2026, 8, 4, 0, 0, 0, 0, time.Local)},
		{"Resets in 4d 19h", testNow.Add(4*24*time.Hour + 19*time.Hour)},
		{"Resets in 6 days", testNow.Add(6 * 24 * time.Hour)},
		{"Resets in 1h 43m", testNow.Add(time.Hour + 43*time.Minute)},
	}
	for _, c := range cases {
		got, err := parseResetTime(c.line, testNow)
		if err != nil {
			t.Fatalf("%q: %v", c.line, err)
		}
		if !got.Equal(c.want) {
			t.Fatalf("%q разобрано как %v, жду %v", c.line, got, c.want)
		}
	}
	for _, line := range []string{"Resets soon", "Resets", "Resets Aug"} {
		if _, err := parseResetTime(line, testNow); err == nil {
			t.Fatalf("%q: жду отказ, а не выдуманную дату", line)
		}
	}
}

func mustLoad(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Skipf("на машине нет базы часовых поясов: %v", err)
	}
	return loc
}

// TestParseResetTimeZone: пояс в скобках считается явным указанием, а не
// украшением. Тест не зависит от пояса машины: сравниваются моменты, а не
// показания часов.
func TestParseResetTimeZone(t *testing.T) {
	la := mustLoad(t, "America/Los_Angeles")
	got, err := parseResetTime("Resets Tue Aug 4 at 10:00am (America/Los_Angeles)", testNow)
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 8, 4, 10, 0, 0, 0, la)
	if !got.Equal(want) {
		t.Fatalf("сброс %v, жду %v", got, want)
	}
	// Незнакомая зона это не повод терять бакет: считаем местным временем.
	got, err = parseResetTime("Resets Tue Aug 4 at 10:00am (Nowhere/Middle_Of)", testNow)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Equal(time.Date(2026, 8, 4, 10, 0, 0, 0, time.Local)) {
		t.Fatalf("незнакомая зона разобрана как %v", got)
	}
}

// TestParseResetYearRollover: года панель не показывает, и на стыке лет он
// обязан достроиться следующим, а не текущим.
func TestParseResetYearRollover(t *testing.T) {
	now := time.Date(2026, 12, 30, 12, 0, 0, 0, time.Local)
	got, err := parseResetTime("Resets Jan 3 at 10:00am", now)
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2027, 1, 3, 10, 0, 0, 0, time.Local)
	if !got.Equal(want) {
		t.Fatalf("сброс %v, жду %v", got, want)
	}
}
