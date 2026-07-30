package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPickModel(t *testing.T) {
	cases := []struct {
		name   string
		r      row
		model  string
		effort string
		part   string
	}{
		{"S с неопределённостью 0 совсем атомарная", row{Type: "task", Rank: "3 (0+3+0+0+0)", Cost: "S"}, "haiku", "low", "атомарная"},
		{"S с неопределённостью 1 подход уже выбран", row{Type: "task", Rank: "6 (0+3+1+0+2)", Cost: "S"}, "sonnet", "medium", "подход уже выбран"},
		{"S с неопределённостью 2 уходит в дефолт", row{Type: "task", Rank: "8 (0+3+3+0+2)", Cost: "S"}, "opus", "high", "обычная"},
		{"M с неопределённостью 0 тоже sonnet", row{Type: "task", Rank: "33 (25+4+0+0+4)", Cost: "M"}, "sonnet", "medium", "подход уже выбран"},
		{"M с неопределённостью 1 подход уже выбран", row{Type: "task", Rank: "34 (25+4+1+0+4)", Cost: "M"}, "sonnet", "medium", "подход уже выбран"},
		{"M с неопределённостью 2 уходит в дефолт", row{Type: "task", Rank: "35 (25+4+2+0+4)", Cost: "M"}, "opus", "high", "обычная"},
		{"баг L это дефолт", row{Type: "bug", Rank: "35 (25+0+1+5+4)", Cost: "L"}, "opus", "high", "обычная"},
		{"LLD сильнее дешевизны", row{Type: "LLD", Rank: "10 (0+5+1+0+4)", Cost: "S"}, "opus", "xhigh", "дизайн"},
		{"LLD ценой L уходит в fable", row{Type: "LLD", Rank: "10 (0+5+1+0+4)", Cost: "L"}, "fable", "xhigh", "сложное проектирование"},
		{"LLD ценой XL уходит в fable", row{Type: "LLD", Rank: "20 (0+10+3+0+5)", Cost: "XL"}, "fable", "xhigh", "сложное проектирование"},
		{"LLD без оценки цены остаётся на opus", row{Type: "LLD", Rank: "10 (0+5+1+0+4)", Cost: "-"}, "opus", "xhigh", "дизайн"},
		{"неопределённость 5 это грумминг", row{Type: "task", Rank: "64 (50+6+5+0+3)", Cost: "M"}, "opus", "xhigh", "грумминг"},
		{"XL сначала разбить", row{Type: "task", Rank: "20 (0+10+3+0+5)", Cost: "XL"}, "opus", "xhigh", "разбить"},
		{"L и неопределённость 3 уходит в дефолт", row{Type: "task", Rank: "9 (0+5+3+0+1)", Cost: "L"}, "opus", "high", "обычная"},
		{"L и неопределённость 2 уходит в дефолт", row{Type: "task", Rank: "8 (0+5+2+0+1)", Cost: "L"}, "opus", "high", "обычная"},
		{"L и неопределённость 0 уходит в дефолт", row{Type: "task", Rank: "7 (0+5+0+0+1)", Cost: "L"}, "opus", "high", "обычная"},
		{"цена не оценена", row{Type: "task", Rank: "8 (0+3+1+0+4)", Cost: "-"}, "opus", "high", "не оценена"},
		{"нечитаемый ранг с ценой S уходит в дефолт", row{Type: "task", Rank: "-", Cost: "S"}, "opus", "high", "обычная"},
		{"нечитаемый ранг с ценой M уходит в дефолт", row{Type: "task", Rank: "-", Cost: "M"}, "opus", "high", "обычная"},
		{"L с нечитаемым рангом уходит в дефолт", row{Type: "task", Rank: "-", Cost: "L"}, "opus", "high", "обычная"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v := pickModel(c.r)
			if v.Model != c.model {
				t.Fatalf("модель %q, жду %q", v.Model, c.model)
			}
			if v.Effort != c.effort {
				t.Fatalf("effort %q, жду %q", v.Effort, c.effort)
			}
			if !strings.Contains(v.Reason, c.part) {
				t.Fatalf("причина %q без %q", v.Reason, c.part)
			}
		})
	}
}

// TestPickModelEffortLevels: маппинг не выдаёт max, этот уровень остаётся
// ручным решением через override-строку.
func TestPickModelEffortLevels(t *testing.T) {
	rows := []row{
		{Type: "task", Rank: "3 (0+3+0+0+0)", Cost: "S"},
		{Type: "task", Rank: "6 (0+3+1+0+2)", Cost: "M"},
		{Type: "bug", Rank: "35 (25+0+1+5+4)", Cost: "L"},
		{Type: "LLD", Rank: "10 (0+5+1+0+4)", Cost: "XL"},
		{Type: "task", Rank: "64 (50+6+5+0+3)", Cost: "M"},
		{Type: "task", Rank: "-", Cost: "-"},
	}
	for _, r := range rows {
		v := pickModel(r)
		if !validEfforts[v.Effort] {
			t.Fatalf("%+v: effort %q вне допустимых", r, v.Effort)
		}
		if v.Effort == "max" {
			t.Fatalf("%+v: маппинг выдал max", r)
		}
	}
}

func TestUncertainty(t *testing.T) {
	cases := []struct {
		rank string
		want int
	}{
		{"34 (25+4+1+0+4)", 1},
		{"64 (50+6+5+0+3)", 5},
		{"-", -1},
		{"34", -1},
		{"34 (25+4+1)", -1},
		{"34 (a+b+c+d+e)", -1},
	}
	for _, c := range cases {
		if got := uncertainty(c.rank); got != c.want {
			t.Fatalf("uncertainty(%q) = %d, жду %d", c.rank, got, c.want)
		}
	}
}

const sampleBoard = `# demo: задачи (префикс T)

Проза шапки, таблицей не является.

## In progress

| ID | Задача | Тип | P | R | Цена | Ссылка |
|---|---|---|---|---|---|---|
| T-002 | фича в работе | task | P2 | 34 (25+4+1+0+4) | M | - |

## Check

Нет.

## Backlog

| ID | Задача | Тип | P | R | Цена | Ссылка |
|---|---|---|---|---|---|---|
| T-001 | мелкая правка | task | P3 | 5 (0+3+0+0+2) | S | - |
| T-003 | спайк про синхронизацию | LLD | P1 | 64 (50+6+5+0+3) | - | - |
| T-004 | неразобранная задача | task | P1 | 64 (50+6+5+0+3) | M | - |

## Blocked

Нет.
`

func writeBoard(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "docs", "TASKS.md"), []byte(sampleBoard), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestCmdPick(t *testing.T) {
	root := writeBoard(t)
	cases := []struct {
		id     string
		first  string
		second string
		part   string
	}{
		{"T-001", "model: haiku", "effort: low", "цена S"},
		{"T-002", "model: sonnet", "effort: medium", "неопределённость 1"},
		{"T-003", "model: opus", "effort: xhigh", "дизайн"},
	}
	for _, c := range cases {
		out, err := cmdPick(root, c.id, false)
		if err != nil {
			t.Fatalf("pick %s: %v", c.id, err)
		}
		lines := strings.SplitN(out, "\n", 3)
		if len(lines) != 3 {
			t.Fatalf("pick %s: жду три строки, получил %q", c.id, out)
		}
		if lines[0] != c.first {
			t.Fatalf("pick %s: первая строка %q, жду %q", c.id, lines[0], c.first)
		}
		if lines[1] != c.second {
			t.Fatalf("pick %s: вторая строка %q, жду %q", c.id, lines[1], c.second)
		}
		if !strings.Contains(lines[2], c.part) {
			t.Fatalf("pick %s: в человеческой строке нет %q: %q", c.id, c.part, out)
		}
	}
}

func TestCmdPickOverride(t *testing.T) {
	root := writeBoard(t)
	if err := os.MkdirAll(filepath.Join(root, "docs", "tasks"), 0o755); err != nil {
		t.Fatal(err)
	}

	t.Run("override перебивает LLD-правило", func(t *testing.T) {
		taskFile := filepath.Join(root, "docs", "tasks", "T-003.md")
		content := "# T-003\n\nОписание.\n\nМодель: fable (3D-графика)\n"
		if err := os.WriteFile(taskFile, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		out, err := cmdPick(root, "T-003", false)
		if err != nil {
			t.Fatalf("pick T-003: %v", err)
		}
		if !strings.HasPrefix(out, "model: fable") {
			t.Fatalf("жду override на fable, получил %q", out)
		}
		if !strings.Contains(out, "override-строкой") {
			t.Fatalf("в причине нет упоминания override: %q", out)
		}
		// Оси независимы: override модели не трогает effort, он остаётся от
		// обычного маппинга (LLD, значит xhigh).
		if !strings.Contains(out, "effort: xhigh") {
			t.Fatalf("effort не взят из маппинга: %q", out)
		}
	})

	t.Run("override эффорта не трогает модель", func(t *testing.T) {
		taskFile := filepath.Join(root, "docs", "tasks", "T-001.md")
		content := "# T-001\n\nЭффорт: max (домен требует)\n"
		if err := os.WriteFile(taskFile, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		out, err := cmdPick(root, "T-001", false)
		if err != nil {
			t.Fatalf("pick T-001: %v", err)
		}
		if !strings.HasPrefix(out, "model: haiku\neffort: max\n") {
			t.Fatalf("жду haiku из маппинга и max из override, получил %q", out)
		}
		if !strings.Contains(out, "effort задан override-строкой") {
			t.Fatalf("в причине нет упоминания override эффорта: %q", out)
		}
	})

	t.Run("обе override-строки сразу", func(t *testing.T) {
		taskFile := filepath.Join(root, "docs", "tasks", "T-001.md")
		content := "# T-001\n\n- Модель: fable (3D-графика)\n- Эффорт: xhigh\n"
		if err := os.WriteFile(taskFile, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		out, err := cmdPick(root, "T-001", false)
		if err != nil {
			t.Fatalf("pick T-001: %v", err)
		}
		if !strings.HasPrefix(out, "model: fable\neffort: xhigh\n") {
			t.Fatalf("жду обе оси из override, получил %q", out)
		}
	})

	t.Run("неизвестный effort это ошибка", func(t *testing.T) {
		taskFile := filepath.Join(root, "docs", "tasks", "T-001.md")
		content := "# T-001\n\nЭффорт: highest\n"
		if err := os.WriteFile(taskFile, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		_, err := cmdPick(root, "T-001", false)
		if err == nil || !strings.Contains(err.Error(), "неизвестный effort") {
			t.Fatalf("жду ошибку про неизвестный effort, получил %v", err)
		}
	})

	t.Run("вариант пунктом списка", func(t *testing.T) {
		taskFile := filepath.Join(root, "docs", "tasks", "T-002.md")
		content := "# T-002\n\n- Модель: haiku\n"
		if err := os.WriteFile(taskFile, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		out, err := cmdPick(root, "T-002", false)
		if err != nil {
			t.Fatalf("pick T-002: %v", err)
		}
		if !strings.HasPrefix(out, "model: haiku") {
			t.Fatalf("жду override на haiku, получил %q", out)
		}
	})

	t.Run("неизвестная модель это ошибка", func(t *testing.T) {
		taskFile := filepath.Join(root, "docs", "tasks", "T-001.md")
		content := "# T-001\n\nМодель: gpt5\n"
		if err := os.WriteFile(taskFile, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		_, err := cmdPick(root, "T-001", false)
		if err == nil || !strings.Contains(err.Error(), "неизвестную модель") {
			t.Fatalf("жду ошибку про неизвестную модель, получил %v", err)
		}
	})

	t.Run("без файла и без строки работает обычный маппинг", func(t *testing.T) {
		if err := os.RemoveAll(filepath.Join(root, "docs", "tasks")); err != nil {
			t.Fatal(err)
		}
		out, err := cmdPick(root, "T-001", false)
		if err != nil {
			t.Fatalf("pick T-001: %v", err)
		}
		if !strings.HasPrefix(out, "model: haiku") {
			t.Fatalf("жду обычный маппинг haiku, получил %q", out)
		}
	})
}

func TestCmdPickMissing(t *testing.T) {
	root := writeBoard(t)
	if _, err := cmdPick(root, "T-999", false); err == nil || !strings.Contains(err.Error(), "нет на доске") {
		t.Fatalf("жду ошибку про отсутствие на доске, получил %v", err)
	}
}

func TestPickOnRealBoardFormat(t *testing.T) {
	// Доска, сгенерированная taskctl init: секции с пояснением «Нет.» вместо
	// таблиц не должны ронять парсер.
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	empty := "# x\n\n## In progress\n\nНет.\n\n## Backlog\n\nНет.\n"
	if err := os.WriteFile(filepath.Join(root, "docs", "TASKS.md"), []byte(empty), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdPick(root, "T-001", false); err == nil {
		t.Fatal("жду ошибку на пустой доске")
	}
}

func TestRecordExecution(t *testing.T) {
	root := writeBoard(t)
	if err := os.MkdirAll(filepath.Join(root, "docs", "tasks"), 0o755); err != nil {
		t.Fatal(err)
	}

	t.Run("создание раздела в файле без него", func(t *testing.T) {
		taskFile := filepath.Join(root, "docs", "tasks", "T-001.md")
		content := "# T-001\n\nОписание задачи.\n"
		if err := os.WriteFile(taskFile, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}

		_, err := cmdPick(root, "T-001", true)
		if err != nil {
			t.Fatalf("pick --record: %v", err)
		}

		data, _ := os.ReadFile(taskFile)
		result := string(data)
		if !strings.Contains(result, "## Ход работы") {
			t.Fatal("раздел \"Ход работы\" не создан")
		}
		if !strings.Contains(result, "- Исполнение: субагент haiku/low") {
			t.Fatalf("строка исполнения не добавлена в файл:\n%s", result)
		}
	})

	t.Run("дозапись в существующий раздел", func(t *testing.T) {
		taskFile := filepath.Join(root, "docs", "tasks", "T-002.md")
		content := "# T-002\n\nОписание задачи.\n\n## Ход работы\n\n- Исполнение: субагент sonnet/medium по вердикту pick, 2026-07-27.\n"
		if err := os.WriteFile(taskFile, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}

		_, err := cmdPick(root, "T-002", true)
		if err != nil {
			t.Fatalf("pick --record: %v", err)
		}

		data, _ := os.ReadFile(taskFile)
		result := string(data)
		lines := strings.Split(result, "\n")
		executionLines := []string{}
		for _, line := range lines {
			if strings.HasPrefix(line, "- Исполнение:") {
				executionLines = append(executionLines, line)
			}
		}
		if len(executionLines) != 2 {
			t.Fatalf("жду две строки исполнения, получил %d:\n%s", len(executionLines), result)
		}
	})

	t.Run("ошибка без файла задачи", func(t *testing.T) {
		_, err := cmdPick(root, "T-003", true)
		if err == nil {
			t.Fatal("жду ошибку при отсутствии файла задачи")
		}
		if !strings.Contains(err.Error(), "taskctl file") {
			t.Fatalf("ошибка без подсказки про taskctl file: %v", err)
		}
	})

	t.Run("вставка в конец раздела, а не файла", func(t *testing.T) {
		// Самая содержательная граница: за «Ход работы» идёт «Ревью», строка
		// обязана лечь в конец первого раздела, не оторвавшись от записей.
		taskFile := filepath.Join(root, "docs", "tasks", "T-001.md")
		content := "# T-001\n\n## Ход работы\n\n- Начало.\n\n## Ревью\n\n- замечание: исправлено\n"
		if err := os.WriteFile(taskFile, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := cmdPick(root, "T-001", true); err != nil {
			t.Fatalf("pick --record: %v", err)
		}
		data, _ := os.ReadFile(taskFile)
		want := "- Начало.\n- Исполнение: субагент haiku/low по вердикту pick, " +
			time.Now().Format("2006-01-02") + ".\n\n## Ревью"
		if !strings.Contains(string(data), want) {
			t.Fatalf("строка не в конце раздела:\n%s", data)
		}
	})

	t.Run("пустой раздел в середине файла", func(t *testing.T) {
		taskFile := filepath.Join(root, "docs", "tasks", "T-001.md")
		content := "# T-001\n\n## Ход работы\n\n## Ревью\n\nНет.\n"
		if err := os.WriteFile(taskFile, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := cmdPick(root, "T-001", true); err != nil {
			t.Fatalf("pick --record: %v", err)
		}
		data, _ := os.ReadFile(taskFile)
		if !strings.Contains(string(data), "## Ход работы\n\n- Исполнение: субагент haiku/low") {
			t.Fatalf("запись не отбита от заголовка пустого раздела:\n%s", data)
		}
	})

	t.Run("повторный вызов дописывает вторую строку", func(t *testing.T) {
		taskFile := filepath.Join(root, "docs", "tasks", "T-001.md")
		if err := os.WriteFile(taskFile, []byte("# T-001\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 2; i++ {
			if _, err := cmdPick(root, "T-001", true); err != nil {
				t.Fatalf("pick --record, вызов %d: %v", i+1, err)
			}
		}
		data, _ := os.ReadFile(taskFile)
		if n := strings.Count(string(data), "- Исполнение: субагент haiku/low"); n != 2 {
			t.Fatalf("жду две строки исполнения после двух вызовов, вижу %d:\n%s", n, data)
		}
	})

	t.Run("файл без завершающего перевода строки", func(t *testing.T) {
		taskFile := filepath.Join(root, "docs", "tasks", "T-001.md")
		if err := os.WriteFile(taskFile, []byte("# T-001\n\n## Ход работы\n\n- Начало."), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := cmdPick(root, "T-001", true); err != nil {
			t.Fatalf("pick --record: %v", err)
		}
		data, _ := os.ReadFile(taskFile)
		if !strings.Contains(string(data), "- Начало.\n- Исполнение: субагент haiku/low") {
			t.Fatalf("строка не приклеилась к записи без перевода строки:\n%s", data)
		}
	})

	t.Run("грумминговый вердикт пишется груммингом", func(t *testing.T) {
		taskFile := filepath.Join(root, "docs", "tasks", "T-004.md")
		if err := os.WriteFile(taskFile, []byte("# T-004\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := cmdPick(root, "T-004", true); err != nil {
			t.Fatalf("pick --record: %v", err)
		}
		data, _ := os.ReadFile(taskFile)
		if !strings.Contains(string(data), "- Грумминг: субагент opus/xhigh") {
			t.Fatalf("жду строку про грумминг, а не исполнение:\n%s", data)
		}
		if strings.Contains(string(data), "- Исполнение:") {
			t.Fatalf("грумминговый вердикт записан исполнением:\n%s", data)
		}
	})

	t.Run("override снимает грумминг, пишется исполнением", func(t *testing.T) {
		// T-004 без override уходит в грумминг (неопределённость 5, см. тест
		// выше); override-строка перебивает маппинг целиком, включая Groom.
		taskFile := filepath.Join(root, "docs", "tasks", "T-004.md")
		if err := os.WriteFile(taskFile, []byte("# T-004\n\nМодель: sonnet\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := cmdPick(root, "T-004", true); err != nil {
			t.Fatalf("pick --record: %v", err)
		}
		data, _ := os.ReadFile(taskFile)
		if !strings.Contains(string(data), "- Исполнение: субагент sonnet/xhigh") {
			t.Fatalf("жду строку исполнения по override, а не грумминг:\n%s", data)
		}
		if strings.Contains(string(data), "- Грумминг:") {
			t.Fatalf("override записан как грумминг:\n%s", data)
		}
	})
}
