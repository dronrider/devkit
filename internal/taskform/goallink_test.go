package taskform

import (
	"os"
	"path/filepath"
	"testing"
)

// Корпус форм связи с целью: разбор один на taskctl и дашборд, и корпус
// держит все живые формы записи одним списком.
func TestGoalKey(t *testing.T) {
	for _, c := range []struct{ link, want string }{
		{"Цель: [tasks/DK-565.md](tasks/DK-565.md)", "DK-565"},
		{"Цель: DK-565", "DK-565"},
		{"Цель: docs/tasks/DK-565.md", "DK-565"},
		{"Цель: DK-565.md", "DK-565"},
		{"Цель: [tasks/DK-565.md]", "DK-565"},
		// Текст ссылки первичен, адрес в скобках разбор не читает: в живых
		// файлах текст и адрес совпадают, и разбор держит одну дорогу.
		{"Цель: [Запись](DK-565.md)", "Запись"},
		{"Цель:", ""},
		{"  Цель:  [x](y)  ", "x"},
		{"", ""},
	} {
		if got := GoalKey(c.link); got != c.want {
			t.Errorf("GoalKey(%q) = %q, ждал %q", c.link, got, c.want)
		}
	}
}

// Цель записи читается из шапки файла задачи и подтверждается файлом цели.
func TestTaskGoalKey(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "docs", "tasks")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("XR-100.md", "# XR-100: пробный цикл\n\n## Цель\n")
	write("XR-4.md", "# XR-4: задача\n\nЦель: [tasks/XR-100.md](tasks/XR-100.md)\n\n## Ход работы\n")
	write("XR-5.md", "# XR-5: без цели в шапке\n\n## Ход работы\nЦель: [tasks/XR-100.md](tasks/XR-100.md)\n")
	write("XR-6.md", "# XR-6: цель уехала\n\nЦель: [tasks/XR-777.md](tasks/XR-777.md)\n")
	if got := TaskGoalKey(root, "XR-4"); got != "XR-100" {
		t.Errorf("цель задачи не прочиталась: %q", got)
	}
	if got := TaskGoalKey(root, "XR-5"); got != "" {
		t.Errorf("цель ниже шапки прочиталась: %q", got)
	}
	if got := TaskGoalKey(root, "XR-6"); got != "" {
		t.Errorf("цель без файла на месте прочиталась: %q", got)
	}
	if got := TaskGoalKey(root, "XR-нет"); got != "" {
		t.Errorf("несуществующая запись отдала цель: %q", got)
	}
}
