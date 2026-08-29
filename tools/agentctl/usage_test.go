package main

import (
	"net/http"
	"net/http/httptest"
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
		s, err := parseUsagePanel(specAt(t, ""), readFixture(t, "usage-panel-countdown.txt"), testNow)
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
		s, err := parseUsagePanel(specAt(t, ""), readFixture(t, "usage-panel-dates.txt"), testNow)
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
		s, err := parseUsagePanel(specAt(t, ""), readFixture(t, "usage-panel-fable.txt"), testNow)
		if err != nil {
			t.Fatalf("панель не разобрана: %v", err)
		}
		if len(s.Buckets) != 2 {
			t.Fatalf("бакеты: %+v", s.Buckets)
		}
		all, _ := s.bucket("week_all")
		fable, _ := s.bucket("week_max")
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
		s, err := parseUsagePanel(specAt(t, ""), panel, testNow)
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
		s, err := parseUsagePanel(specAt(t, ""), panel, testNow)
		if err != nil {
			t.Fatalf("панель с одним недельным бакетом это не отказ: %v", err)
		}
		if len(s.Buckets) != 1 || s.Buckets[0].Name != "week_all" {
			t.Fatalf("бакеты: %+v", s.Buckets)
		}
	})

	t.Run("без общего бакета снимок не пишется", func(t *testing.T) {
		panel := "Current week (Fable)\n 70% used\n Resets in 2d\n"
		_, err := parseUsagePanel(specAt(t, ""), panel, testNow)
		if err == nil {
			t.Fatal("жду отказ: общий бакет обязателен")
		}
		if !strings.Contains(err.Error(), "week_all") {
			t.Fatalf("отказ не называет недостающий бакет: %v", err)
		}
	})

	t.Run("бакет без даты сброса это отказ", func(t *testing.T) {
		panel := "Current week (all models)\n 34% used\n Resets in 2d\n" +
			"Current week (Fable)\n 70% used\n"
		if _, err := parseUsagePanel(specAt(t, ""), panel, testNow); err == nil {
			t.Fatal("жду отказ: бакет без сброса разобрать нечем")
		}
	})

	t.Run("бакет без процента это отказ, а не нетронутый бакет", func(t *testing.T) {
		panel := "Current week (all models)\n [полоска без цифр]\n Resets in 2d\n"
		if _, err := parseUsagePanel(specAt(t, ""), panel, testNow); err == nil {
			t.Fatal("непрочитанный процент записался нулём, то есть профицитом")
		}
	})

	t.Run("нетронутый бакет не перезаписывается соседним процентом", func(t *testing.T) {
		// Честные 0% used ничем не отличаются от пустого поля, если считать
		// признаком само значение: следующий процент секции затирал бы ноль.
		panel := "Current week (all models)\n 0% used\n Resets in 2d\n Extra usage: 15% of monthly cap\n" +
			"Current week (Opus)\n 0% used\n Resets in 2d\n"
		s, err := parseUsagePanel(specAt(t, ""), panel, testNow)
		if err != nil {
			t.Fatalf("панель не разобрана: %v", err)
		}
		if all, _ := s.bucket("week_all"); all.Used != 0 {
			t.Fatalf("нетронутый бакет разобран как %.2f потраченного", all.Used)
		}
	})

	t.Run("панель без недельных лимитов это отказ", func(t *testing.T) {
		_, err := parseUsagePanel(specAt(t, ""), readFixture(t, "usage-panel-nolimits.txt"), testNow)
		if err == nil {
			t.Fatal("жду отказ: записывать в снимок нечего")
		}
		if !strings.Contains(err.Error(), "week_all") {
			t.Fatalf("отказ не называет недостающий бакет: %v", err)
		}
	})

	t.Run("цвета панели разбору не мешают", func(t *testing.T) {
		raw := readFixture(t, "usage-panel-countdown.txt")
		colored := strings.ReplaceAll(raw, "%", "\x1b[0m%\x1b[38;5;208m")
		s, err := parseUsagePanel(specAt(t, ""), colored, testNow)
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
		s, err := parseUsagePanel(specAt(t, ""), panel, testNow)
		if err != nil {
			t.Fatalf("панель не разобрана: %v", err)
		}
		if all, _ := s.bucket("week_all"); all.Used != 0.34 {
			t.Fatalf("остаток не перевёрнут: %.2f", all.Used)
		}
	})
}

// TestPaneReady: команду набирают только когда клиенту есть куда её принять.
// Образцы сняты живьём с клиента 2.1.220 в трёх окружениях, потому что цена
// ошибки тут не таймаут: на экране, где ввод перехвачен, Enter подтверждает
// подсвеченный пункт, то есть меняет настройки пользователя.
func TestPaneReady(t *testing.T) {
	if !paneReady(readFixture(t, "pane-ready.txt")) {
		t.Fatal("строка ввода нарисована, а клиент не сочтён готовым")
	}
	// У диалога доверия и у мастера первого запуска сплошные линейки тоже есть,
	// но строки ввода они не рисуют: по признаку «линейка есть» refresh набирал
	// бы команду прямо в них.
	for _, name := range []string{"pane-trust-dialog.txt", "pane-first-run.txt"} {
		if paneReady(readFixture(t, name)) {
			t.Fatalf("%s сочтён готовым к вводу", name)
		}
	}
	// Заставка без строки ввода: клиент ещё дорисовывается.
	splash := "\n Claude Code v2.1.220\n Opus 5 (1M context) with high effort\n ~/projects/devkit\n"
	for _, pane := range []string{"", splash} {
		if paneReady(pane) {
			t.Fatalf("недорисованный клиент сочтён готовым: %q", pane)
		}
	}
	// Подсказку под рамкой клиент меняет от запуска к запуску, признаком
	// готовности она быть не может.
	swapped := strings.ReplaceAll(readFixture(t, "pane-ready.txt"), "? for shortcuts", "install gh for PR status")
	if !paneReady(swapped) {
		t.Fatal("готовность зависит от того, какую подсказку клиент показал")
	}
}

// TestPaneBlocker: экраны, которые перехватывают ввод, узнаются поимённо и
// отказывают по-человечески, иначе refresh давит Enter в чужой диалог.
func TestPaneBlocker(t *testing.T) {
	cases := []struct {
		fixture string
		want    string
	}{
		{"pane-trust-dialog.txt", "доверие каталогу"},
		{"pane-first-run.txt", "мастер первого запуска"},
	}
	for _, c := range cases {
		why := paneBlocker(readFixture(t, c.fixture))
		if !strings.Contains(why, c.want) {
			t.Fatalf("%s: отказ %q, жду упоминание %q", c.fixture, why, c.want)
		}
	}
	if why := paneBlocker(readFixture(t, "pane-ready.txt")); why != "" {
		t.Fatalf("готовый клиент принят за перехваченный экран: %q", why)
	}
	if why := paneBlocker("Welcome to Claude Code\n Please sign in to continue\n"); !strings.Contains(why, "не залогинен") {
		t.Fatalf("экран входа не узнан: %q", why)
	}
}

// TestPanelWaiter: панель приезжает не одним кадром, и первый успешный разбор
// это ещё не повод писать снимок. Ждём не секунды, а слово самой панели.
func TestPanelWaiter(t *testing.T) {
	partialPane := readFixture(t, "usage-panel-partial.txt")
	fullPane := readFixture(t, "usage-panel-fable.txt")
	partial, err := parseUsagePanel(specAt(t, ""), partialPane, testNow)
	if err != nil {
		t.Fatalf("недорисованный кадр панели не разобран: %v", err)
	}
	if len(partial.Buckets) != 1 {
		t.Fatalf("в кадре ждали один общий бакет, вижу %+v", partial.Buckets)
	}
	full, err := parseUsagePanel(specAt(t, ""), fullPane, testNow)
	if err != nil {
		t.Fatalf("дорисованная панель не разобрана: %v", err)
	}

	t.Run("кадр с недосчитанной панелью в снимок не идёт", func(t *testing.T) {
		var w panelWaiter
		for i := 0; i < 20; i++ {
			if w.accept(partial, partialPane) {
				t.Fatalf("снимок записан по кадру, где панель ещё считает (проход %d)", i)
			}
		}
		w.accept(full, fullPane)
		if !w.accept(full, fullPane) {
			t.Fatal("дорисованная панель не принята")
		}
		if _, ok := w.snap.bucket("week_max"); !ok {
			t.Fatalf("в снимок ушёл кадр без дорогого бакета: %+v", w.snap.Buckets)
		}
	})

	t.Run("одного досчитанного кадра мало", func(t *testing.T) {
		// capture-pane снимает экран в любой момент и может застать панель на
		// середине перерисовки.
		var w panelWaiter
		if w.accept(full, fullPane) {
			t.Fatal("снимок записан по первому же кадру")
		}
		if !w.accept(full, fullPane) {
			t.Fatal("вторым кадром панель так и не принята")
		}
	})

	t.Run("недосчитанный кадр посреди досчитанных сбрасывает счёт", func(t *testing.T) {
		var w panelWaiter
		w.accept(full, fullPane)
		if w.accept(partial, partialPane) {
			t.Fatal("кадр с недосчитанной панелью принят")
		}
		if w.accept(full, fullPane) {
			t.Fatal("счёт досчитанных кадров не сброшен")
		}
	})

	t.Run("досчитанная панель без разбивки принимается", func(t *testing.T) {
		// Такая панель бывает и настоящей: свой тариф, своя версия клиента.
		// Ждать её вечно нельзя, иначе refresh отказывает там, где данные есть.
		pane := readFixture(t, "usage-panel-settled.txt")
		s, err := parseUsagePanel(specAt(t, ""), pane, testNow)
		if err != nil {
			t.Fatalf("панель не разобрана: %v", err)
		}
		var w panelWaiter
		w.accept(s, pane)
		if !w.accept(s, pane) {
			t.Fatal("панель без дорогого бакета не принята")
		}
	})
}

// TestPanelConfirm: правило подтверждения одно на весь цикл, отказной экран
// принимается не с первого кадра, как и готовая панель.
func TestPanelConfirm(t *testing.T) {
	stale := readFixture(t, "usage-panel-laststale.txt")
	full := readFixture(t, "usage-panel-fable.txt")
	snap, err := parseUsagePanel(specAt(t, ""), full, testNow)
	if err != nil {
		t.Fatalf("панель не разобрана: %v", err)
	}

	t.Run("отказной экран подтверждается вторым кадром", func(t *testing.T) {
		var w panelWaiter
		if w.blocked(stale) {
			t.Fatal("отказ принят с одного кадра")
		}
		if !w.blocked(stale) {
			t.Fatal("отказ не принят и со второго кадра")
		}
	})

	t.Run("разовый отказной кадр посреди готовых счёта не даёт", func(t *testing.T) {
		var w panelWaiter
		w.accept(snap, full)
		if w.blocked(stale) {
			t.Fatal("отказ принят по кадру, который сменил исход")
		}
		if w.accept(snap, full) {
			t.Fatal("готовая панель принята, хотя счёт сбросил отказной кадр")
		}
	})
}

// TestPanelUnheard: ожидание держится на словах клиента, а слова меняются с его
// версией. Не услышав ни одного знакомого, съёмщик не знает про панель ничего,
// и снимок уходит с оговоркой вместо тихого «всё на месте».
func TestPanelUnheard(t *testing.T) {
	q := specAt(t, "")
	settled := readFixture(t, "usage-panel-settled.txt")
	drawing := readFixture(t, "usage-panel-partial.txt")
	snap, err := parseUsagePanel(q, settled, testNow)
	if err != nil {
		t.Fatalf("панель не разобрана: %v", err)
	}

	t.Run("незнакомый словарь оставляет след в снимке", func(t *testing.T) {
		var w panelWaiter
		w.accept(snap, settled)
		w.accept(snap, settled)
		why := w.gap(settled)
		if !strings.Contains(why, "знакомого слова") {
			t.Fatalf("молчание панели ничем не помечено: %q", why)
		}
		marked := w.snap.markPartial(q, why)
		if got := marked.partial("week_max"); got != why {
			t.Fatalf("след до снимка не доехал: %+v", marked.Partial)
		}
	})

	t.Run("услышанное слово оговорку снимает", func(t *testing.T) {
		var w panelWaiter
		w.accept(snap, drawing)
		w.accept(snap, settled)
		w.accept(snap, settled)
		if why := w.gap(settled); why != "" {
			t.Fatalf("панель говорила знакомо, а снимок с оговоркой: %q", why)
		}
	})

	t.Run("слова панели идут раньше оговорки", func(t *testing.T) {
		var w panelWaiter
		v2251 := readFixture(t, "usage-panel-v2251.txt")
		w.accept(snap, v2251)
		if why := w.gap(v2251); !strings.Contains(why, "из его кеша") {
			t.Fatalf("точная причина заменена оговоркой: %q", why)
		}
	})
}

// TestPanelSettled: признак ожидания это слово панели, а не счёт секунд.
func TestPanelSettled(t *testing.T) {
	if panelSettled(readFixture(t, "usage-panel-partial.txt")) {
		t.Fatal("кадр со строкой Refreshing принят за досчитанный")
	}
	if panelSettled("   Loading usage data\u2026\n   Esc to cancel\n") {
		t.Fatal("кадр без цифр принят за досчитанный")
	}
	for _, name := range []string{"usage-panel-fable.txt", "usage-panel-v2251.txt"} {
		if !panelSettled(readFixture(t, name)) {
			t.Fatalf("досчитанная панель %s принята за недосчитанную", name)
		}
	}
}

// TestPanelNoBreakdown: неполный кадр называет себя неполным словами панели, и
// это отличимо от подписки, у которой дорогого бакета нет вовсе.
func TestPanelNoBreakdown(t *testing.T) {
	why := panelNoBreakdown(readFixture(t, "usage-panel-v2251.txt"))
	if !strings.Contains(why, "из его кеша") {
		t.Fatalf("причина неполноты не названа: %q", why)
	}
	if why := panelNoBreakdown(readFixture(t, "usage-panel-fable.txt")); why != "" {
		t.Fatalf("целая панель помечена неполной: %q", why)
	}
	if why := panelNoBreakdown(readFixture(t, "usage-panel-settled.txt")); why != "" {
		t.Fatalf("панель без дорогого бакета помечена неполной: %q", why)
	}
	if why := panelNoBreakdown("   Could not refresh usage data\n"); !strings.Contains(why, "не обновила") {
		t.Fatalf("отказ обновления не узнан: %q", why)
	}
}

// TestMarkPartial: пометка ложится на бакеты лестницы трат и только тогда,
// когда панель сама сказала, что разбивки не будет.
func TestMarkPartial(t *testing.T) {
	q := specAt(t, "")
	s, err := parseUsagePanel(q, readFixture(t, "usage-panel-v2251.txt"), testNow)
	if err != nil {
		t.Fatalf("панель не разобрана: %v", err)
	}
	marked := s.markPartial(q, panelNoBreakdown(readFixture(t, "usage-panel-v2251.txt")))
	if why := marked.partial("week_max"); !strings.Contains(why, "из его кеша") {
		t.Fatalf("week_max не помечен: %+v", marked.Partial)
	}
	if why := marked.partial("week_all"); why != "" {
		t.Fatalf("помечен бакет, который в снимке есть: %q", why)
	}
	if why := marked.partial("week_opus"); why != "" {
		t.Fatalf("помечен бакет вне лестницы трат: %q", why)
	}
	if len(s.markPartial(q, "").Partial) != 0 {
		t.Fatal("пометка легла без слов панели")
	}
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

// TestCmdQuotaRefreshIfStale: на этом режиме стоит хук старта сессии, и он
// зовётся на каждой сессии. Свежий снимок он трогать не должен, иначе клиент в
// tmux поднимался бы по десятку раз в день впустую.
func TestCmdQuotaRefreshIfStale(t *testing.T) {
	q := specAt(t, filepath.Join(t.TempDir(), ".devkit", "quota", "claude-code.local"))
	fresh := snapOf(freshAge, bucketAt("week_all", 40, halfWindow))
	if err := q.write(fresh); err != nil {
		t.Fatal(err)
	}
	out, err := cmdQuotaRefresh(q, testNow, true)
	if err != nil {
		t.Fatalf("свежий снимок не должен ронять refresh: %v", err)
	}
	if !strings.Contains(out, "не снимаем") {
		t.Fatalf("на свежем снимке refresh полез за панелью: %q", out)
	}
	// Порог живёт в agentctl, а не в хуке: за порогом тот же вызов уходит
	// снимать панель и упирается уже в окружение (tmux, claude), а не в возраст.
	stale := snapOf(snapshotMaxAge+time.Minute, bucketAt("week_all", 40, halfWindow))
	if err := q.write(stale); err != nil {
		t.Fatal(err)
	}
	out, err = cmdQuotaRefresh(q, testNow, true)
	if err == nil && strings.Contains(out, "не снимаем") {
		t.Fatalf("протухший снимок не пошёл на съём панели: %q", out)
	}
}

// Съёмщик проверяется подложными скриптами: живого инструмента с [quota] snap =
// "script" на машине нет, а контракт (stdin не даётся, окружение с именем
// харнеса и бюджетом, stdout это текст снимка, отказ это ненулевой код с
// причиной в stderr) от инструмента не зависит.
func scriptSpec(t *testing.T, body string) *quotaSpec {
	t.Helper()
	home := t.TempDir()
	q := specAt(t, filepath.Join(home, ".devkit", "quota", "sometool.local"))
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "snap"), 0o755); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join("snap", "sometool.sh")
	if err := os.WriteFile(filepath.Join(dir, script), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	q.Harness, q.Dir, q.Snap, q.Script = "sometool", dir, snapScript, script
	return q
}

func TestQuotaRefreshScript(t *testing.T) {
	snapText := "taken = " + at(testNow) + "\nweek_all = 40% сброс " + at(testNow.Add(halfWindow)) + "\n"

	t.Run("валидный вывод ложится в файл", func(t *testing.T) {
		q := scriptSpec(t, "#!/bin/sh\nprintf '%s' \""+snapText+"\"\n")
		out, err := cmdQuotaRefresh(q, testNow, false)
		if err != nil {
			t.Fatalf("refresh: %v", err)
		}
		if !strings.Contains(out, "week_all: потрачено 40%") {
			t.Fatalf("снятое не показано: %q", out)
		}
		s, err := q.read()
		if err != nil {
			t.Fatalf("снимок не прочитан: %v", err)
		}
		if b, ok := s.bucket("week_all"); !ok || b.Used != 0.4 {
			t.Fatalf("в файле не то, что напечатал съёмщик: %+v", s.Buckets)
		}
	})

	t.Run("окружение съёмщика", func(t *testing.T) {
		// Имя харнеса и бюджет съёмщик получает переменными: свой конфиг он не
		// читает, иначе машинный конфиг пришлось бы разбирать каждому скрипту.
		q := scriptSpec(t, "#!/bin/sh\n[ \"$DEVKIT_HARNESS\" = sometool ] || { echo \"чужое имя харнеса: $DEVKIT_HARNESS\" >&2; exit 1; }\n"+
			"printf 'taken = %s\\n' \""+at(testNow)+"\"\n"+
			"printf 'week_all = %s%% сброс %s\\n' \"$DEVKIT_QUOTA_BUDGET\" \""+at(testNow.Add(halfWindow))+"\"\n")
		q.BudgetBased, q.Budget = true, 20
		if _, err := cmdQuotaRefresh(q, testNow, false); err != nil {
			t.Fatalf("refresh: %v", err)
		}
		data, err := os.ReadFile(q.Path)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), "week_all = 20%") {
			t.Fatalf("бюджет до съёмщика не доехал:\n%s", data)
		}
	})

	t.Run("каталог харнеса доезжает до съёмщика", func(t *testing.T) {
		// Токен подписки лежит в настройках клиента внутри каталога машинного
		// хозяйства, и съёмщик получает каталог переменной: своего пути к токену
		// у него нет, иначе машинный конфиг разбирал бы каждый скрипт сам.
		home := t.TempDir()
		q := scriptSpec(t, "#!/bin/sh\n[ \"$DEVKIT_HARNESS_HOME\" = \""+home+"\" ] || { echo \"каталог харнеса не доехал: $DEVKIT_HARNESS_HOME\" >&2; exit 1; }\n"+
			"printf '%s' \""+snapText+"\"\n")
		q.Home = home
		if _, err := cmdQuotaRefresh(q, testNow, false); err != nil {
			t.Fatalf("refresh: %v", err)
		}
	})

	t.Run("бюджет не задан", func(t *testing.T) {
		q := scriptSpec(t, "#!/bin/sh\nprintf '%s' \""+snapText+"\"\n")
		q.BudgetBased = true
		if _, err := cmdQuotaRefresh(q, testNow, false); err == nil ||
			!strings.Contains(err.Error(), "бюджета в машинном конфиге нет") {
			t.Fatalf("расход в деньгах без бюджета прошёл молча: %v", err)
		}
	})

	t.Run("мусор на stdout файл не трогает", func(t *testing.T) {
		q := scriptSpec(t, "#!/bin/sh\necho 'Traceback (most recent call last):'\n")
		before := seedSnapshot(t, q, snapText)
		_, err := cmdQuotaRefresh(q, testNow, false)
		if err == nil || !strings.Contains(err.Error(), "не разобран") {
			t.Fatalf("мусор принят за снимок: %v", err)
		}
		sameFile(t, q.Path, before)
	})

	t.Run("вывод без обязательного бакета файл не трогает", func(t *testing.T) {
		q := scriptSpec(t, "#!/bin/sh\nprintf 'taken = %s\\n' \""+at(testNow)+"\"\n")
		before := seedSnapshot(t, q, snapText)
		_, err := cmdQuotaRefresh(q, testNow, false)
		if err == nil || !strings.Contains(err.Error(), "нет обязательного бакета week_all") {
			t.Fatalf("снимок без общего бакета принят: %v", err)
		}
		sameFile(t, q.Path, before)
	})

	t.Run("вывод без момента снятия файл не трогает", func(t *testing.T) {
		// Снимок без taken читается, но вверх по нему корректор не двигает:
		// принять такой от съёмщика значит молча потерять половину его работы.
		q := scriptSpec(t, "#!/bin/sh\nprintf 'week_all = 40%% сброс %s\\n' \""+at(testNow.Add(halfWindow))+"\"\n")
		before := seedSnapshot(t, q, snapText)
		_, err := cmdQuotaRefresh(q, testNow, false)
		if err == nil || !strings.Contains(err.Error(), "нет момента снятия") {
			t.Fatalf("снимок без момента снятия принят: %v", err)
		}
		sameFile(t, q.Path, before)
	})

	t.Run("ненулевой выход: причина видна, файл не тронут", func(t *testing.T) {
		q := scriptSpec(t, "#!/bin/sh\necho 'токен протух, обновить: sometool login' >&2\n"+
			"printf '%s'\nexit 3\n")
		before := seedSnapshot(t, q, snapText)
		_, err := cmdQuotaRefresh(q, testNow, false)
		if err == nil {
			t.Fatal("отказ съёмщика прошёл как успех")
		}
		if !strings.Contains(err.Error(), "токен протух") {
			t.Fatalf("причина из stderr потерялась: %v", err)
		}
		sameFile(t, q.Path, before)
	})

	t.Run("stdin съёмщику не даётся", func(t *testing.T) {
		// Съёмщик ничего не спрашивает: сессия зовёт refresh хуком старта, и
		// скрипт, ждущий ввода, повесил бы её.
		q := scriptSpec(t, "#!/bin/sh\nif [ -n \"$(cat)\" ]; then echo 'мне дали stdin' >&2; exit 1; fi\n"+
			"printf '%s' \""+snapText+"\"\n")
		if _, err := cmdQuotaRefresh(q, testNow, false); err != nil {
			t.Fatalf("refresh: %v", err)
		}
	})

	t.Run("съёмщика нет на месте", func(t *testing.T) {
		q := scriptSpec(t, "#!/bin/sh\nexit 0\n")
		q.Script = filepath.Join("snap", "нет-такого.sh")
		if _, err := cmdQuotaRefresh(q, testNow, false); err == nil ||
			!strings.Contains(err.Error(), "снимок не тронут") {
			t.Fatalf("пропавший съёмщик прошёл молча: %v", err)
		}
	})
}

// TestGlmCodeSnapScript: съёмщик glm-code проверяется целиком, от settings.json
// до текста снимка. Живой z.ai не нужен: локальный сервер отвечает образцом,
// снятым с подписки живьём, и разбор едет по нему, а не по представлению о
// чужой разметке.
func TestGlmCodeSnapScript(t *testing.T) {
	const token = "подписной-токен"
	body := readFixture(t, "glm-quota.json")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path != "/api/monitor/usage/quota/limit":
			http.NotFound(w, r)
		case r.Header.Get("Authorization") != "Bearer "+token:
			w.WriteHeader(http.StatusUnauthorized)
		default:
			_, _ = w.Write([]byte(body))
		}
	}))
	defer srv.Close()

	// Эндпоинт мониторинга чужой для API сообщений: base URL клиента кончается
	// на /api/anthropic, а съёмщик обязан дернуть origin, а не склеивать путь.
	settings := func(tok string) string {
		return `{"env": {"ANTHROPIC_BASE_URL": "` + srv.URL + `/api/anthropic", "ANTHROPIC_AUTH_TOKEN": "` + tok + `"}}`
	}
	quotaHome := func(t *testing.T, tok string) *quotaSpec {
		t.Helper()
		home := t.TempDir()
		writeFile(t, home, "settings.json", settings(tok))
		q := glmSpec(t, filepath.Join(t.TempDir(), "quota", "glm-code.local"))
		q.Home = home
		return q
	}

	t.Run("живой ответ ложится в снимок обеими шкалами", func(t *testing.T) {
		snap, err := snapByScript(quotaHome(t, token), testNow)
		if err != nil {
			t.Fatalf("съёмщик отказал: %v", err)
		}
		if len(snap.Warns) != 0 {
			t.Fatalf("вывод съёмщика не разобран начисто: %v", snap.Warns)
		}
		five, ok := snap.bucket("window5h_all")
		if !ok {
			t.Fatalf("пятичасового окна в снимке нет: %+v", snap.Buckets)
		}
		week, _ := snap.bucket("week_all")
		// Проценты берутся из сырых чисел, а не из поля percentage: панель z.ai
		// обрезает вниз (198 из 12000 это 1), снимок округляет до ближайшего.
		if five.Used != 0.02 || week.Used != 0.09 {
			t.Fatalf("проценты %.2f и %.2f, жду 0.02 и 0.09", five.Used, week.Used)
		}
	})

	t.Run("чужой токен это отказ, файл не тронут", func(t *testing.T) {
		q := quotaHome(t, "не-тот-токен")
		before := seedSnapshot(t, q, "taken = 2026-08-01T10:00\n")
		if _, err := cmdQuotaRefresh(q, testNow, false); err == nil ||
			!strings.Contains(err.Error(), "не прошёл") {
			t.Fatalf("отказ эндпоинта прошёл как снимок: %v", err)
		}
		sameFile(t, q.Path, before)
	})

	t.Run("незнакомое окно это отказ, а не догадка", func(t *testing.T) {
		// Разметка z.ai не контракт: сменились коды окон, съёмщик обязан
		// отказаться, потому что снимок с перепутанными окнами двигал бы
		// вердикты в обратную сторону.
		kept := body
		body = strings.Replace(kept, `"unit":3`, `"unit":9`, 1)
		defer func() { body = kept }()
		q := quotaHome(t, token)
		before := seedSnapshot(t, q, "taken = 2026-08-01T10:00\n")
		if _, err := cmdQuotaRefresh(q, testNow, false); err == nil ||
			!strings.Contains(err.Error(), "незнакомое окно") {
			t.Fatalf("незнакомая разметка принята за снимок: %v", err)
		}
		sameFile(t, q.Path, before)
	})

	t.Run("окно дважды это отказ, а не две строки", func(t *testing.T) {
		// Повторная запись того же окна молча дала бы вторую строку бакета,
		// и парсер снимка взял бы первую без единого предупреждения.
		kept := body
		body = strings.Replace(kept, `"unit":6,"number":1`, `"unit":3,"number":5`, 1)
		defer func() { body = kept }()
		q := quotaHome(t, token)
		before := seedSnapshot(t, q, "taken = 2026-08-01T10:00\n")
		if _, err := cmdQuotaRefresh(q, testNow, false); err == nil ||
			!strings.Contains(err.Error(), "дважды") {
			t.Fatalf("повтор окна принят за снимок: %v", err)
		}
		sameFile(t, q.Path, before)
	})
}

// seedSnapshot кладёт прежний снимок и возвращает его содержимое: отказ съёмщика
// обязан оставить файл ровно таким, каким он был.
func seedSnapshot(t *testing.T, q *quotaSpec, text string) []byte {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(q.Path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(q.Path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	return []byte(text)
}

func sameFile(t *testing.T, path string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("прежний снимок пропал: %v", err)
	}
	if string(got) != string(want) {
		t.Fatalf("снимок тронут отказавшим съёмщиком:\n%s", got)
	}
	dir := filepath.Dir(path)
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) != 1 {
		t.Fatalf("в %s остался мусор от записи: %v", dir, ents)
	}
}

// Панель клиента 2.1.251: недельный бакет на месте, а разбивки по моделям на
// экране нет, её клиент подтягивает отдельно и в этом кадре не дождался.
func TestParseUsagePanelV2251(t *testing.T) {
	s, err := parseUsagePanel(specAt(t, ""), readFixture(t, "usage-panel-v2251.txt"), testNow)
	if err != nil {
		t.Fatalf("панель не разобрана: %v", err)
	}
	if len(s.Buckets) != 1 {
		t.Fatalf("бакетов %d, ждали один: %+v", len(s.Buckets), s.Buckets)
	}
	b := s.Buckets[0]
	if b.Name != "week_all" || b.Used != 0.61 {
		t.Fatalf("бакет разобран неверно: %+v", b)
	}
	if got := b.Reset.Format("2006-01-02T15:04"); got != "2026-08-31T15:00" {
		t.Fatalf("сброс %s, ждали 2026-08-31T15:00", got)
	}
}

// Отказ съёмщика показывают человеку, и по нему должно быть видно, что делать.
func TestPanelFailure(t *testing.T) {
	snapPath := filepath.Join(t.TempDir(), "claude-code.local")
	framePath := filepath.Join(filepath.Dir(snapPath), "claude-code.pane.txt")

	t.Run("верх панели уехал за край окна", func(t *testing.T) {
		pane := readFixture(t, "usage-panel-cropped.txt")
		q := specAt(t, snapPath)
		_, err := parseUsagePanel(q, pane, testNow)
		if err == nil {
			t.Fatal("срезанная панель разобралась, тест бесполезен")
		}
		msg := panelFailure(q, pane, err).Error()
		for _, want := range []string{"week_all", "не поместился в окно", framePath, "Снимок не тронут"} {
			if !strings.Contains(msg, want) {
				t.Fatalf("в отказе нет %q: %s", want, msg)
			}
		}
		frame, ferr := os.ReadFile(framePath)
		if ferr != nil {
			t.Fatalf("кадр панели не сохранён: %v", ferr)
		}
		if !strings.Contains(string(frame), "your limits usage") {
			t.Fatalf("в кадре не тот экран: %.80s", frame)
		}
	})

	t.Run("клиент упёрся в частоту обращений", func(t *testing.T) {
		pane := readFixture(t, "usage-panel-ratelimit.txt")
		msg := panelFailure(specAt(t, snapPath), pane, nil).Error()
		if !strings.Contains(msg, "частоту обращений") {
			t.Fatalf("отказ не называет причину: %s", msg)
		}
		if !strings.Contains(msg, framePath) {
			t.Fatalf("отказ не ведёт к кадру панели: %s", msg)
		}
	})

	t.Run("целая панель за отказ клиента не считается", func(t *testing.T) {
		// Про частоту обращений панель пишет и внутри рабочего экрана, когда не
		// дождалась одной только разбивки по моделям.
		if why := panelBlocked(readFixture(t, "usage-panel-v2251.txt")); why != "" {
			t.Fatalf("рабочая панель принята за отказ: %s", why)
		}
	})

	t.Run("панель с цифрами прошлого раза в снимок не идёт", func(t *testing.T) {
		// Кадр настоящий: клиент 2.1.251 с недоступной сетью нарисовал бакеты
		// из своей памяти и подписал их возрастом. Разбивка по моделям в кадре
		// есть, и тем опаснее записать его снимком: свежий момент снятия над
		// цифрами получасовой давности это молчащая ложь, а прежний снимок
		// хотя бы честно показывает свой возраст.
		pane := readFixture(t, "usage-panel-laststale.txt")
		if !strings.Contains(pane, "Current week (Fable)") {
			t.Fatal("в образце нет разбивки по моделям, случай не тот")
		}
		why := panelBlocked(pane)
		if !strings.Contains(why, "прошлого раза") {
			t.Fatalf("панель с прошлыми цифрами принята за рабочую: %q", why)
		}
		if panelBlocked(readFixture(t, "usage-panel-v2251.txt")) != "" {
			t.Fatal("свежая панель без разбивки принята за панель с прошлыми цифрами")
		}
	})

	t.Run("панель не открылась вовсе", func(t *testing.T) {
		msg := panelFailure(specAt(t, snapPath), readFixture(t, "pane-ready.txt"), nil).Error()
		if !strings.Contains(msg, "не нарисовал панель") {
			t.Fatalf("отказ не про пустой экран: %s", msg)
		}
		if strings.Contains(msg, "не поместился в окно") {
			t.Fatalf("отказ выдумал срезанный верх: %s", msg)
		}
	})

	t.Run("целая панель за срезанную не считается", func(t *testing.T) {
		if panelCropped(readFixture(t, "usage-panel-v2251.txt")) {
			t.Fatal("целая панель принята за срезанную")
		}
	})
}

// Окно съёмщика должно вмещать панель целиком: на шестидесяти строках недельный
// бакет уезжал выше видимой части, и снимок переставал вставать.
func TestUsagePaneRows(t *testing.T) {
	panel := strings.Count(readFixture(t, "usage-panel-v2251.txt"), "\n")
	if usagePaneRows < panel*2 {
		t.Fatalf("окно съёмщика %d строк при панели в %d, запаса на рост нет", usagePaneRows, panel)
	}
}
