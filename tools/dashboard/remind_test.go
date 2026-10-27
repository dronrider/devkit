package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Доска со сроками для круга напоминаний: строка в окне, строка за
// горизонтом, просроченная и строка без срока. Фикстура отдельная от
// boardFixtureJSON: той строки доски живут без сроков, и напоминанию в ней
// взяться неоткуда.
const dueBoardFixtureJSON = `{"prefix":"XR","sections":[` +
	`{"key":"in-progress","title":"In progress","rows":[` +
	`{"id":"XR-900","title":"Срок в окне","type":"task","p":"P2","r":30,"cost":"-","link":"-","due":"2026-10-28"},` +
	`{"id":"XR-901","title":"За горизонтом","type":"task","p":"P2","r":30,"cost":"-","link":"-","due":"2026-11-15"},` +
	`{"id":"XR-902","title":"Просрочен","type":"task","p":"P2","r":30,"cost":"-","link":"-","due":"2026-10-20"}]},` +
	`{"key":"backlog","title":"Backlog","rows":[` +
	`{"id":"XR-903","title":"Без срока","type":"bug","p":"P1","r":55,"cost":"-","link":"-"}]}]}`

// Круг напоминания зовёт уведомителя по каждой строке окна, а не по доске
// целиком: событие ленты ведёт к строке доски ключом задачи, и сводная запись
// его растеряла бы. Строки за горизонтом и без срока молчат.
func TestDueRemindRoundSaysDueRows(t *testing.T) {
	e := newTestEnv(t)
	writeScript(t, e.bin, "taskctl", fmt.Sprintf("echo '%s'", dueBoardFixtureJSON))
	notifyCalls := filepath.Join(e.home, "notify.calls")
	writeNotifyFake(t, filepath.Dir(e.proj), notifyCalls)
	// Стенд собирает конфиг руками, без LoadConfig, и умолчания напоминания
	// выставляет тест: круг проверки не должен зависеть от стартовых нулей
	// синтетического конфига.
	e.s.cfg.RemindHour, e.s.cfg.RemindDays = defaultRemindHour, defaultRemindDays
	now := time.Date(2026, 10, 27, 10, 0, 0, 0, time.Local)
	e.s.now = func() time.Time { return now }

	e.s.dueRemindRound()
	got := readFile(t, notifyCalls)
	lines := strings.Split(strings.TrimSpace(got), "\n")
	if len(lines) != 2 {
		t.Fatalf("уведомитель позван %d раз, ждал две строки окна: %q", len(lines), got)
	}
	for _, want := range []string{"--reason\ttask_due", "--task\tXR-900\t", "--project\tdemo\t"} {
		if !strings.Contains(got, want) {
			t.Errorf("вызов без %q: %q", want, got)
		}
	}
	if !strings.Contains(got, "demo: XR-900 срок 2026-10-28") {
		t.Errorf("заголовок напоминания не назвал проект и срок: %q", got)
	}
	if !strings.Contains(got, "срок завтра") {
		t.Errorf("тело напоминания не сосчитало остаток днями: %q", got)
	}
	if strings.Contains(got, "XR-901") || strings.Contains(got, "XR-903") {
		t.Errorf("круг позвал строку вне окна либо без срока: %q", got)
	}
	if !strings.Contains(got, "XR-902") {
		t.Errorf("просроченная строка не попала в напоминание: %q", got)
	}
}

// Круг ходит раз в день и не раньше часа из конфига: до часа круг не зовёт
// уведомителя и не закрывает день, а после круга маркер дня держит второй
// круг того же дня молча. Наступивший день круг начинает сам.
func TestDueRemindRoundOnceADayAfterHour(t *testing.T) {
	e := newTestEnv(t)
	writeScript(t, e.bin, "taskctl", fmt.Sprintf("echo '%s'", dueBoardFixtureJSON))
	notifyCalls := filepath.Join(e.home, "notify.calls")
	writeNotifyFake(t, filepath.Dir(e.proj), notifyCalls)
	mark := filepath.Join(e.home, ".devkit", "remind.mark")
	e.s.cfg.RemindHour, e.s.cfg.RemindDays = defaultRemindHour, defaultRemindDays
	now := time.Date(2026, 10, 27, 8, 0, 0, 0, time.Local)
	e.s.now = func() time.Time { return now }

	e.s.dueRemindRound()
	if _, err := os.Stat(notifyCalls); !os.IsNotExist(err) {
		t.Fatalf("до часа напоминания уже пошли: %v", err)
	}
	if _, err := os.Stat(mark); !os.IsNotExist(err) {
		t.Fatalf("круг до часа закрыл день: %v", err)
	}

	now = now.Add(2 * time.Hour)
	e.s.dueRemindRound()
	if got, err := os.ReadFile(mark); err != nil || strings.TrimSpace(string(got)) != "2026-10-27" {
		t.Fatalf("маркер дня: %v %q", err, got)
	}
	first := readFile(t, notifyCalls)
	if strings.Count(first, "--reason\ttask_due") != 2 {
		t.Fatalf("первый круг: %q", first)
	}

	e.s.dueRemindRound()
	if got := readFile(t, notifyCalls); got != first {
		t.Fatalf("второй круг того же дня повторил напоминания:\n%s", got)
	}

	now = now.Add(24 * time.Hour)
	e.s.dueRemindRound()
	if got := readFile(t, notifyCalls); !strings.HasPrefix(got, first) {
		t.Fatalf("наступивший день не начал круг заново:\n%s", got)
	}
	if got, _ := os.ReadFile(mark); strings.TrimSpace(string(got)) != "2026-10-28" {
		t.Fatalf("маркер не переехал на новый день: %q", got)
	}
}

// Окно из конфига режет список: remind_days = 0 оставляет сам день срока и
// просроченные, а «завтра» из круга уходит.
func TestDueRemindWindowFromConfig(t *testing.T) {
	e := newTestEnv(t)
	writeConf(t, e.home, "root = "+filepath.Dir(e.proj)+"\nremind_days = 0\n")
	// Стенд собрал конфиг раньше правки файла, окно переезжает вручную: пересборка
	// сервера из теста стоит дороже, чем значение одного поля.
	e.s.cfg.RemindHour, e.s.cfg.RemindDays = defaultRemindHour, 0
	writeScript(t, e.bin, "taskctl", fmt.Sprintf("echo '%s'", dueBoardFixtureJSON))
	notifyCalls := filepath.Join(e.home, "notify.calls")
	writeNotifyFake(t, filepath.Dir(e.proj), notifyCalls)
	now := time.Date(2026, 10, 27, 10, 0, 0, 0, time.Local)
	e.s.now = func() time.Time { return now }

	e.s.dueRemindRound()
	got := readFile(t, notifyCalls)
	if strings.Contains(got, "XR-900") {
		t.Errorf("окно нуля дней взяло строку завтрашнего срока: %q", got)
	}
	if !strings.Contains(got, "XR-902") {
		t.Errorf("окно нуля дней потеряло просроченную строку: %q", got)
	}
}
