package attime

import (
	"strings"
	"testing"
	"time"
)

func at(y int, m time.Month, d, h, mi, s int) time.Time {
	return time.Date(y, m, d, h, mi, s, 0, time.Local)
}

func TestParse(t *testing.T) {
	now := at(2026, 9, 11, 15, 30, 0)
	cases := []struct {
		in   string
		want time.Time
	}{
		{"2026-09-12 02:00", at(2026, 9, 12, 2, 0, 0)},
		{"2026-09-12T02:00", at(2026, 9, 12, 2, 0, 0)},
		{"2026-09-12T02:00:30", at(2026, 9, 12, 2, 0, 30)},
		{" 2026-09-12 02:00:30 ", at(2026, 9, 12, 2, 0, 30)},
		// Короткая форма: ещё впереди сегодня, уже прошла, ровно сейчас.
		{"16:00", at(2026, 9, 11, 16, 0, 0)},
		{"02:00", at(2026, 9, 12, 2, 0, 0)},
		{"15:30", at(2026, 9, 12, 15, 30, 0)},
		{"15:30:01", at(2026, 9, 11, 15, 30, 1)},
	}
	for _, c := range cases {
		got, err := Parse(c.in, now)
		if err != nil {
			t.Fatalf("%q: %v", c.in, err)
		}
		if !got.Equal(c.want) {
			t.Errorf("%q разобран в %s, ждали %s", c.in, got, c.want)
		}
	}
}

// TestShortFormCrossesTheMonth: завтра считается календарём, а не прибавкой
// суток к числу месяца.
func TestShortFormCrossesTheMonth(t *testing.T) {
	got, err := Parse("01:00", at(2026, 9, 30, 23, 0, 0))
	if err != nil {
		t.Fatal(err)
	}
	if want := at(2026, 10, 1, 1, 0, 0); !got.Equal(want) {
		t.Fatalf("разобран в %s, ждали %s", got, want)
	}
}

func TestParseRefusal(t *testing.T) {
	for _, in := range []string{"", "завтра", "25:00", "2026-13-01 02:00", "через 6h"} {
		_, err := Parse(in, at(2026, 9, 11, 15, 30, 0))
		if err == nil {
			t.Fatalf("%q разобран без отказа", in)
		}
		if !strings.Contains(err.Error(), "02:00") {
			t.Fatalf("отказ %q не называет формы", err)
		}
	}
}
