package control

import "testing"

// TestOfAndSuffix: разбор и сбор суффикса сходятся в одну форму, срок рядом с
// контролем не мешает ни тому, ни другому. Хвосты, стоящие после контроля
// (приёмка, провал, блок), снимаются до поиска, как у splitTitle.
func TestOfAndSuffix(t *testing.T) {
	for _, tc := range []struct {
		title string
		days  int
		ok    bool
	}{
		{"Хвост [срок: 2026-11-05] [контроль: 3]", 3, true},
		{"Хвост [срок: 2026-11-05] [контроль: 0]", 0, true},
		{"Хвост [контроль: 3] [приёмка: mixed]", 3, true},
		{"Хвост [срок: 2026-11-05] [контроль: 3] [приёмка: agent] [блок: ждёт]", 3, true},
		{"Хвост [долг] [контроль: 3]", 3, true},
		{"Хвост [контроль: 3] [долг]", 0, false},
		{"Хвост [срок: 2026-11-05]", 0, false},
	} {
		days, ok := Of(tc.title)
		if ok != tc.ok || days != tc.days {
			t.Fatalf("%q: ждал (%d, %v), пришло (%d, %v)", tc.title, tc.days, tc.ok, days, ok)
		}
	}
	if got := Suffix(3); got != " [контроль: 3]" {
		t.Fatalf("суффикс: %q", got)
	}
}

// TestDueOf: срок читается и без контроля, и под ним. Читатель окна зовёт
// DueOf по заголовку целиком с хвостами, поэтому контроль после срока обязан
// сниматься самим читателем.
func TestDueOf(t *testing.T) {
	for _, tc := range []struct{ title, due string }{
		{"Хвост [срок: 2026-11-05] [контроль: 3]", "2026-11-05"},
		{"Хвост [срок: 2026-11-05] [контроль: 3] [приёмка: mixed]", "2026-11-05"},
		{"Хвост [срок: 2026-11-05]", "2026-11-05"},
		{"Хвост [контроль: 3]", ""},
		{"Хвост", ""},
	} {
		if got := DueOf(tc.title); got != tc.due {
			t.Fatalf("%q: ждал %q, пришло %q", tc.title, tc.due, got)
		}
	}
}

// TestWindow: окно открывается за N дней до срока и остаётся открытым после
// него, а не закрывается в день срока. Граница месяцев проверяется нарочно:
// вычитание чисел YYYYMMDD ломается именно на ней.
func TestWindow(t *testing.T) {
	for _, tc := range []struct {
		due, today string
		days       int
		want       bool
	}{
		{"2026-11-05", "2026-11-05", 0, true},
		{"2026-11-05", "2026-11-04", 0, false},
		{"2026-11-05", "2026-11-02", 3, true},
		{"2026-11-05", "2026-11-01", 3, false},
		{"2026-11-05", "2026-11-09", 0, true},
		{"2026-11-01", "2026-10-31", 1, true},
		{"кривая", "2026-11-05", 3, false},
		{"2026-11-05", "кривая", 3, false},
	} {
		if got := Window(tc.due, tc.days, tc.today); got != tc.want {
			t.Fatalf("окно %s за %d до %s: ждал %v, пришло %v",
				tc.today, tc.days, tc.due, tc.want, got)
		}
	}
}

// TestOverdue: просрочка считается днями, ноль и меньше это «не просрочен».
func TestOverdue(t *testing.T) {
	for _, tc := range []struct {
		due, today string
		want       int
	}{
		{"2026-11-05", "2026-11-05", 0},
		{"2026-11-05", "2026-11-01", -4},
		{"2026-11-05", "2026-11-08", 3},
		{"кривая", "2026-11-05", 0},
	} {
		if got := Overdue(tc.due, tc.today); got != tc.want {
			t.Fatalf("просрочка %s на %s: ждал %d, пришло %d", tc.due, tc.today, tc.want, got)
		}
	}
}
