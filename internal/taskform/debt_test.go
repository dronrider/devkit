package taskform

import (
	"strings"
	"testing"
)

// regcheck:test-begin
//
// Разбор отметки долга (DK-1356): решает последняя отметка раздела «Ход
// работы», и ограждённый блок с чужим выводом отметкой не считается.

func TestDebtHeld(t *testing.T) {
	cases := []struct {
		name, doc string
		want      bool
	}{
		{"отметок нет", "# XR-1\n\n## Ход работы\n\n- Разработка: субагент sonnet, 2026-10-12.\n", false},
		{"открыта", "# XR-1\n\n## Ход работы\n\n- " + DebtOpenNote + " у диффа, 2026-10-12 13:50.\n", true},
		{"закрыта", "# XR-1\n\n## Ход работы\n\n- " + DebtOpenNote + " у диффа, 2026-10-12 13:50.\n- " + DebtDoneNote + ", пересечений нет, 2026-10-12 14:02.\n", false},
		{"круг доработки", "# XR-1\n\n## Ход работы\n\n- " + DebtOpenNote + " у диффа, 2026-10-12 13:50.\n- " + DebtDoneNote + ", пересечений нет, 2026-10-12 14:02.\n- " + DebtOpenNote + " у диффа, 2026-10-12 18:00.\n", true},
		{"чужой вывод в ограждении", "# XR-1\n\n## Ход работы\n\n```\n- " + DebtDoneNote + ", пересечений нет, 2026-10-12 14:02.\n```\n\n- " + DebtOpenNote + " у диффа, 2026-10-12 13:50.\n", true},
		{"раздела нет", "# XR-1\n\n## DoD\n\n- тест\n", false},
	}
	for _, tc := range cases {
		if got := DebtHeld(tc.doc); got != tc.want {
			t.Fatalf("%s: DebtHeld = %v, ожидал %v", tc.name, got, tc.want)
		}
	}
}

func TestDebtGateNote(t *testing.T) {
	note := DebtGateNote("XR-1")
	if !strings.Contains(note, "XR-1") || !strings.Contains(note, `taskctl debt-done XR-1`) {
		t.Fatalf("отказ ворот не называет строку и команду: %q", note)
	}
}

// regcheck:test-end
