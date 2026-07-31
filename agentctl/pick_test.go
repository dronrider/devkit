package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestPick(t *testing.T) {
	// Пары вердикта целиком. Таблица гоняет тройку pickModel, pickEffort и
	// floorSonnetEffort напрямую: pickModel и pickEffort считаются порознь,
	// floorSonnetEffort подтягивает effort для sonnet. Рабочий путь (cmdPick)
	// собирает вердикт той же тройкой, но с поправкой на override и корректор.
	// Оси независимые, но не совсем: у sonnet есть пол effort, поэтому в
	// таблице видно, как low и medium из маппинга подтягиваются до high, а
	// остальные модели идут ровно по расчёту.
	cases := []struct {
		name   string
		r      row
		model  string
		effort string
		part   string
	}{
		{"S с неопределённостью 0 совсем атомарная", row{Type: "task", Rank: "3 (0+3+0+0+0)", Cost: "S"}, "haiku", "low", "атомарная"},
		{"S с неопределённостью 1 это sonnet, effort подтянут полом", row{Type: "task", Rank: "6 (0+3+1+0+2)", Cost: "S"}, "sonnet", "high", "экономить глубину смысла нет"},
		{"S с неопределённостью 3 верхняя граница диапазона", row{Type: "task", Rank: "10 (0+3+3+0+4)", Cost: "S"}, "opus", "high", "сильной"},
		{"M с неопределённостью 0 уходит в дефолт", row{Type: "task", Rank: "33 (25+4+0+0+4)", Cost: "M"}, "opus", "low", "сильной"},
		{"M с неопределённостью 1 уходит в дефолт", row{Type: "task", Rank: "34 (25+4+1+0+4)", Cost: "M"}, "opus", "medium", "сильной"},
		{"S с неопределённостью 2 уходит в дефолт", row{Type: "task", Rank: "8 (0+3+2+0+2)", Cost: "S"}, "opus", "medium", "сильной"},
		{"M с неопределённостью 2 уходит в дефолт", row{Type: "task", Rank: "35 (25+4+2+0+4)", Cost: "M"}, "opus", "medium", "сильной"},
		{"M с неопределённостью 3 уходит в дефолт", row{Type: "task", Rank: "36 (25+4+3+0+4)", Cost: "M"}, "opus", "high", "сильной"},
		{"баг L с неопределённостью 1 это дефолт", row{Type: "bug", Rank: "35 (25+0+1+5+4)", Cost: "L"}, "opus", "medium", "сильной"},
		{"LLD сильнее дешевизны", row{Type: "LLD", Rank: "10 (0+5+1+0+4)", Cost: "S"}, "opus", "xhigh", "дизайн"},
		{"LLD ценой L уходит в fable", row{Type: "LLD", Rank: "10 (0+5+1+0+4)", Cost: "L"}, "fable", "xhigh", "сложное проектирование"},
		{"LLD ценой XL уходит в fable", row{Type: "LLD", Rank: "20 (0+10+3+0+5)", Cost: "XL"}, "fable", "xhigh", "сложное проектирование"},
		{"LLD без оценки цены остаётся на opus", row{Type: "LLD", Rank: "10 (0+5+1+0+4)", Cost: "-"}, "opus", "xhigh", "дизайн"},
		{"LLD без неопределённости всё равно xhigh", row{Type: "LLD", Rank: "10 (0+5+0+0+5)", Cost: "S"}, "opus", "xhigh", "дизайн"},
		{"неопределённость 5 это грумминг", row{Type: "task", Rank: "64 (50+6+5+0+3)", Cost: "M"}, "opus", "xhigh", "грумминг"},
		{"XL сначала разбить", row{Type: "task", Rank: "20 (0+10+3+0+5)", Cost: "XL"}, "opus", "xhigh", "разбить"},
		{"XL без неопределённости всё равно разбить", row{Type: "task", Rank: "20 (0+10+0+0+5)", Cost: "XL"}, "opus", "xhigh", "разбить"},
		{"L и неопределённость 3 уходит в дефолт", row{Type: "task", Rank: "9 (0+5+3+0+1)", Cost: "L"}, "opus", "high", "сильной"},
		{"L и неопределённость 2 уходит в дефолт", row{Type: "task", Rank: "8 (0+5+2+0+1)", Cost: "L"}, "opus", "medium", "сильной"},
		{"L и неопределённость 0 уходит в дефолт", row{Type: "task", Rank: "7 (0+5+0+0+1)", Cost: "L"}, "opus", "low", "сильной"},
		{"цена не оценена", row{Type: "task", Rank: "8 (0+3+1+0+4)", Cost: "-"}, "opus", "high", "не оценена"},
		{"цена не оценена при нулевой неопределённости", row{Type: "task", Rank: "7 (0+3+0+0+4)", Cost: "-"}, "opus", "high", "не оценена"},
		{"нечитаемый ранг с ценой S уходит в дефолт", row{Type: "task", Rank: "-", Cost: "S"}, "opus", "high", "сильной"},
		{"нечитаемый ранг с ценой M уходит в дефолт", row{Type: "task", Rank: "-", Cost: "M"}, "opus", "high", "сильной"},
		{"L с нечитаемым рангом уходит в дефолт", row{Type: "task", Rank: "-", Cost: "L"}, "opus", "high", "сильной"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v := pickModel(c.r)
			v.Effort = pickEffort(c.r)
			floorSonnetEffort(&v)
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

func TestPickEffort(t *testing.T) {
	// Ось effort отдельно: при одной и той же цене уровень ходит за
	// неопределённостью, а цена входит только там, где метаданным верить рано.
	cases := []struct {
		name string
		r    row
		want string
	}{
		{"рутина", row{Type: "task", Rank: "7 (0+5+0+0+1)", Cost: "M"}, "low"},
		{"всё ясно, осталось сделать", row{Type: "task", Rank: "8 (0+5+1+0+1)", Cost: "M"}, "medium"},
		{"почти ясно", row{Type: "task", Rank: "9 (0+5+2+0+1)", Cost: "M"}, "medium"},
		{"развилки в деталях", row{Type: "task", Rank: "10 (0+5+3+0+1)", Cost: "M"}, "high"},
		{"порог готовности снизу", row{Type: "task", Rank: "11 (0+5+4+0+1)", Cost: "M"}, "xhigh"},
		{"совсем не разобрано", row{Type: "task", Rank: "12 (0+5+5+0+1)", Cost: "M"}, "xhigh"},
		{"цена не двигает уровень сама по себе", row{Type: "task", Rank: "8 (0+5+1+0+1)", Cost: "L"}, "medium"},
		{"XL это разбивка", row{Type: "task", Rank: "8 (0+5+1+0+1)", Cost: "XL"}, "xhigh"},
		{"LLD это проектирование", row{Type: "LLD", Rank: "7 (0+5+0+0+1)", Cost: "S"}, "xhigh"},
		{"неоценённая цена прочерком", row{Type: "task", Rank: "7 (0+5+0+0+1)", Cost: "-"}, "high"},
		{"неоценённая цена пустой ячейкой", row{Type: "task", Rank: "7 (0+5+0+0+1)", Cost: ""}, "high"},
		{"нечитаемая разбивка", row{Type: "task", Rank: "7", Cost: "S"}, "high"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := pickEffort(c.r); got != c.want {
				t.Fatalf("effort %q, жду %q", got, c.want)
			}
		})
	}
}

func TestCostAtLeastM(t *testing.T) {
	// Прямая таблица на классификатор: интеграционный путь через cmdPick не
	// достаёт до XL, у не-LLD она грумминговая, а у LLD её перебивает
	// LLD-ветка advice раньше, чем дело доходит до цены.
	cases := []struct {
		cost string
		want bool
	}{
		{"S", false},
		{"M", true},
		{"L", true},
		{"XL", true},
		{"-", false},
		{"", false},
	}
	for _, c := range cases {
		t.Run("цена "+c.cost, func(t *testing.T) {
			if got := costAtLeastM(c.cost); got != c.want {
				t.Fatalf("costAtLeastM(%q) = %v, жду %v", c.cost, got, c.want)
			}
		})
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
| T-005 | задача поменьше | task | P3 | 6 (0+3+1+0+2) | S | - |
| T-006 | задача ценой L | task | P3 | 7 (0+5+1+0+1) | L | - |

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

// isolateQuota уводит снимок квоты во временный HOME и возвращает путь к нему.
// Без этого вердикт зависел бы от того, что лежит в снимке на машине.
func isolateQuota(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	return filepath.Join(home, ".devkit", quotaFileName)
}

// fixNow останавливает часы утилиты: и формула корректора, и дата в строке
// --record считаются от одного момента.
func fixNow(t *testing.T, now time.Time) {
	t.Helper()
	prev := timeNow
	timeNow = func() time.Time { return now }
	t.Cleanup(func() { timeNow = prev })
}

func TestCmdPick(t *testing.T) {
	isolateQuota(t)
	root := writeBoard(t)
	cases := []struct {
		id     string
		first  string
		second string
		part   string
	}{
		{"T-001", "model: haiku", "effort: low", "цена S"},
		{"T-002", "model: opus", "effort: medium", "неопределённость 1"},
		{"T-003", "model: opus", "effort: xhigh", "дизайн"},
		// Сквозной путь без override и без снимка квоты: T-005 маппингом
		// sonnet (S/1), effort из пола видно прямо в человеческой строке.
		{"T-005", "model: sonnet", "effort: high", "экономить глубину смысла нет"},
	}
	for _, c := range cases {
		out, err := cmdPick(root, c.id, false, roleExec)
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
	isolateQuota(t)
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
		out, err := cmdPick(root, "T-003", false, roleExec)
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

	t.Run("override sonnet на LLD полом не опускает xhigh", func(t *testing.T) {
		// T-003 маппингом LLD, pickEffort для него всегда xhigh; override
		// модели на sonnet меняет только модель, а пол трогает лишь low и
		// medium, xhigh уже выше и остаётся как есть.
		taskFile := filepath.Join(root, "docs", "tasks", "T-003.md")
		content := "# T-003\n\nМодель: sonnet\n"
		if err := os.WriteFile(taskFile, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		out, err := cmdPick(root, "T-003", false, roleExec)
		if err != nil {
			t.Fatalf("pick T-003: %v", err)
		}
		if !strings.HasPrefix(out, "model: sonnet\neffort: xhigh\n") {
			t.Fatalf("жду sonnet с effort xhigh нетронутым полом, получил %q", out)
		}
		if strings.Contains(out, "effort поднят до high") {
			t.Fatalf("пол сработал там, где не должен: %q", out)
		}
	})

	t.Run("override эффорта не трогает модель", func(t *testing.T) {
		taskFile := filepath.Join(root, "docs", "tasks", "T-001.md")
		content := "# T-001\n\nЭффорт: max (домен требует)\n"
		if err := os.WriteFile(taskFile, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		out, err := cmdPick(root, "T-001", false, roleExec)
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
		out, err := cmdPick(root, "T-001", false, roleExec)
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
		_, err := cmdPick(root, "T-001", false, roleExec)
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
		out, err := cmdPick(root, "T-002", false, roleExec)
		if err != nil {
			t.Fatalf("pick T-002: %v", err)
		}
		// Заодно видно независимость осей на обычной задаче: модель пришла из
		// override, effort посчитан по неопределённости строки доски.
		if !strings.HasPrefix(out, "model: haiku\neffort: medium\n") {
			t.Fatalf("жду override на haiku и effort из маппинга, получил %q", out)
		}
	})

	t.Run("неизвестная модель это ошибка", func(t *testing.T) {
		taskFile := filepath.Join(root, "docs", "tasks", "T-001.md")
		content := "# T-001\n\nМодель: gpt5\n"
		if err := os.WriteFile(taskFile, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		_, err := cmdPick(root, "T-001", false, roleExec)
		if err == nil || !strings.Contains(err.Error(), "неизвестную модель") {
			t.Fatalf("жду ошибку про неизвестную модель, получил %v", err)
		}
	})

	t.Run("override модели на sonnet поднимает effort из маппинга", func(t *testing.T) {
		// T-001 сам по себе S/0, маппинг даёт haiku/low; override модели меняет
		// только модель, а effort остаётся low, пока его не подтянет пол.
		taskFile := filepath.Join(root, "docs", "tasks", "T-001.md")
		content := "# T-001\n\nМодель: sonnet\n"
		if err := os.WriteFile(taskFile, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		out, err := cmdPick(root, "T-001", false, roleExec)
		if err != nil {
			t.Fatalf("pick T-001: %v", err)
		}
		if !strings.HasPrefix(out, "model: sonnet\neffort: high\n") {
			t.Fatalf("жду override на sonnet с эффортом, подтянутым до high, получил %q", out)
		}
		if !strings.Contains(out, "экономить глубину смысла нет") {
			t.Fatalf("в причине не видно, что effort подтянут полом: %q", out)
		}
	})

	t.Run("явный override effort у sonnet перебивает пол", func(t *testing.T) {
		// T-005 маппингом уже sonnet (S/1), пол сам поднял бы effort до high,
		// но явная override-строка должна пройти как есть, хоть и low.
		taskFile := filepath.Join(root, "docs", "tasks", "T-005.md")
		content := "# T-005\n\nЭффорт: low\n"
		if err := os.WriteFile(taskFile, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		out, err := cmdPick(root, "T-005", false, roleExec)
		if err != nil {
			t.Fatalf("pick T-005: %v", err)
		}
		if !strings.HasPrefix(out, "model: sonnet\neffort: low\n") {
			t.Fatalf("жду sonnet из маппинга и low из override без подтяжки полом, получил %q", out)
		}
		if strings.Contains(out, "экономить глубину смысла нет") {
			t.Fatalf("пол не должен был сработать при явном override effort: %q", out)
		}
	})

	t.Run("override модели с sonnet на opus снимает пол", func(t *testing.T) {
		// T-005 маппингом sonnet (S/1), effort из маппинга medium; override
		// модели на opus меняет модель, а пол на неё уже не действует, effort
		// остаётся medium, а не утаскивается за прежней моделью в high.
		taskFile := filepath.Join(root, "docs", "tasks", "T-005.md")
		content := "# T-005\n\nМодель: opus\n"
		if err := os.WriteFile(taskFile, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		out, err := cmdPick(root, "T-005", false, roleExec)
		if err != nil {
			t.Fatalf("pick T-005: %v", err)
		}
		if !strings.HasPrefix(out, "model: opus\neffort: medium\n") {
			t.Fatalf("жду override на opus без пола sonnet, получил %q", out)
		}
	})

	t.Run("без файла и без строки работает обычный маппинг", func(t *testing.T) {
		if err := os.RemoveAll(filepath.Join(root, "docs", "tasks")); err != nil {
			t.Fatal(err)
		}
		out, err := cmdPick(root, "T-001", false, roleExec)
		if err != nil {
			t.Fatalf("pick T-001: %v", err)
		}
		if !strings.HasPrefix(out, "model: haiku") {
			t.Fatalf("жду обычный маппинг haiku, получил %q", out)
		}
	})
}

// writeQuota кладёт свежий снимок во временный HOME: проценты потраченного и
// остаток окна, из которых считается pace.
func writeQuota(t *testing.T, path string, allPct, fablePct int, left time.Duration) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	content := "taken = " + at(testNow.Add(-freshAge)) + "\n" +
		"week_all = " + strconv.Itoa(allPct) + "% сброс " + at(testNow.Add(left)) + "\n" +
		"week_fable = " + strconv.Itoa(fablePct) + "% сброс " + at(testNow.Add(left)) + "\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCmdPickQuota(t *testing.T) {
	quota := isolateQuota(t)
	fixNow(t, testNow)
	root := writeBoard(t)
	if err := os.MkdirAll(filepath.Join(root, "docs", "tasks"), 0o755); err != nil {
		t.Fatal(err)
	}

	t.Run("дефицит опускает вердикт и виден в строке записи", func(t *testing.T) {
		writeQuota(t, quota, 95, 50, halfWindow)
		taskFile := filepath.Join(root, "docs", "tasks", "T-002.md")
		if err := os.WriteFile(taskFile, []byte("# T-002\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		out, err := cmdPick(root, "T-002", true, roleExec)
		if err != nil {
			t.Fatalf("pick: %v", err)
		}
		// T-002 маппингом opus/medium; после сдвига на sonnet effort ещё и
		// подтягивается полом, пол считается по итоговой модели.
		if !strings.HasPrefix(out, "model: sonnet\neffort: high\n") {
			t.Fatalf("жду сдвинутый вердикт, получил %q", out)
		}
		if !strings.Contains(out, "корректор: дефицит week_all, opus -> sonnet") {
			t.Fatalf("в человеческой строке нет хвоста корректора: %q", out)
		}
		data, _ := os.ReadFile(taskFile)
		want := "- Исполнение: субагент sonnet/high по вердикту pick (маппинг opus, корректор: дефицит week_all), " +
			testNow.Format("2006-01-02") + "."
		if !strings.Contains(string(data), want) {
			t.Fatalf("строка записи разошлась с ожидаемой:\n%s", data)
		}
	})

	t.Run("профицит поднимает вердикт", func(t *testing.T) {
		writeQuota(t, quota, 5, 5, 24*time.Hour)
		out, err := cmdPick(root, "T-005", false, roleExec)
		if err != nil {
			t.Fatalf("pick: %v", err)
		}
		// T-005 маппингом sonnet (S с неопределённостью 1); отдельного бакета
		// у opus больше нет, и вверх её двигает профицит общего week_all.
		if !strings.HasPrefix(out, "model: opus") {
			t.Fatalf("жду подъём до opus, получил %q", out)
		}
		if !strings.Contains(out, "корректор: профицит week_all, sonnet -> opus") {
			t.Fatalf("нет хвоста корректора: %q", out)
		}
	})

	t.Run("сдвинутому вниз LLD советуют отложить дизайн", func(t *testing.T) {
		writeQuota(t, quota, 95, 50, halfWindow)
		out, err := cmdPick(root, "T-003", false, roleExec)
		if err != nil {
			t.Fatalf("pick: %v", err)
		}
		if !strings.HasPrefix(out, "model: sonnet") {
			t.Fatalf("жду сдвиг LLD-вердикта вниз, получил %q", out)
		}
		if !strings.Contains(out, "отложить") {
			t.Fatalf("нет совета отложить дизайн: %q", out)
		}
	})

	t.Run("сдвинутому вниз вердикту ценой M советуют отложить исполнение", func(t *testing.T) {
		writeQuota(t, quota, 95, 50, halfWindow)
		out, err := cmdPick(root, "T-002", false, roleExec)
		if err != nil {
			t.Fatalf("pick: %v", err)
		}
		if !strings.HasPrefix(out, "model: sonnet") {
			t.Fatalf("жду сдвиг вердикта T-002 вниз, получил %q", out)
		}
		if !strings.Contains(out, "отложить") {
			t.Fatalf("нет совета отложить для цены M: %q", out)
		}
	})

	t.Run("сдвинутому вниз вердикту ценой L тоже советуют отложить исполнение", func(t *testing.T) {
		writeQuota(t, quota, 95, 50, halfWindow)
		out, err := cmdPick(root, "T-006", false, roleExec)
		if err != nil {
			t.Fatalf("pick: %v", err)
		}
		if !strings.HasPrefix(out, "model: sonnet") {
			t.Fatalf("жду сдвиг вердикта T-006 вниз, получил %q", out)
		}
		if !strings.Contains(out, "отложить") {
			t.Fatalf("нет совета отложить для цены L: %q", out)
		}
	})

	t.Run("сдвинутому вниз вердикту ценой S совета не дают", func(t *testing.T) {
		writeQuota(t, quota, 90, 50, halfWindow)
		out, err := cmdPick(root, "T-005", false, roleExec)
		if err != nil {
			t.Fatalf("pick: %v", err)
		}
		if !strings.HasPrefix(out, "model: haiku") {
			t.Fatalf("жду сдвиг вердикта T-005 вниз, получил %q", out)
		}
		if strings.Contains(out, "отложить") {
			t.Fatalf("S-задаче не положен совет отложить: %q", out)
		}
	})

	t.Run("грумминговый вердикт корректор не трогает", func(t *testing.T) {
		writeQuota(t, quota, 95, 50, halfWindow)
		out, err := cmdPick(root, "T-004", false, roleExec)
		if err != nil {
			t.Fatalf("pick: %v", err)
		}
		if !strings.HasPrefix(out, "model: opus") || strings.Contains(out, "корректор") {
			t.Fatalf("грумминговый вердикт скорректирован: %q", out)
		}
	})

	t.Run("override модели корректор не двигает", func(t *testing.T) {
		writeQuota(t, quota, 95, 50, halfWindow)
		taskFile := filepath.Join(root, "docs", "tasks", "T-002.md")
		if err := os.WriteFile(taskFile, []byte("# T-002\n\nМодель: opus\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		out, err := cmdPick(root, "T-002", false, roleExec)
		if err != nil {
			t.Fatalf("pick: %v", err)
		}
		if !strings.HasPrefix(out, "model: opus") || strings.Contains(out, "корректор") {
			t.Fatalf("ручное решение сдвинуто корректором: %q", out)
		}
	})

	t.Run("незнакомый ключ снимка виден предупреждением", func(t *testing.T) {
		content := "taken = " + at(testNow) + "\nweek_sonnet = 5% сброс " + at(testNow.Add(halfWindow)) + "\n"
		if err := os.WriteFile(quota, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		out, err := cmdPick(root, "T-001", false, roleExec)
		if err != nil {
			t.Fatalf("pick: %v", err)
		}
		if !strings.HasPrefix(out, "model: haiku") {
			t.Fatalf("незнакомый ключ сдвинул вердикт: %q", out)
		}
		if !strings.Contains(out, "неизвестный ключ снимка") {
			t.Fatalf("нет предупреждения про ключ: %q", out)
		}
	})

	t.Run("нечитаемый снимок предупреждает с причиной", func(t *testing.T) {
		if err := os.RemoveAll(quota); err != nil {
			t.Fatal(err)
		}
		// Директория вместо файла: снимок не читается, но вердикт обязан
		// доехать, а причина отказа попасть в предупреждение.
		if err := os.MkdirAll(quota, 0o755); err != nil {
			t.Fatal(err)
		}
		out, err := cmdPick(root, "T-001", false, roleExec)
		if err != nil {
			t.Fatalf("нечитаемый снимок не должен ронять pick: %v", err)
		}
		if !strings.HasPrefix(out, "model: haiku") {
			t.Fatalf("вердикт изменился: %q", out)
		}
		if !strings.Contains(out, "снимок квоты не прочитан (") {
			t.Fatalf("нет предупреждения с причиной: %q", out)
		}
	})

	t.Run("без снимка вердикт прежний, но молчания нет", func(t *testing.T) {
		if err := os.Remove(quota); err != nil {
			t.Fatal(err)
		}
		// T-006 берётся ради чистоты: у T-002 выше по тесту уже лежит
		// override-строка, а при override корректор до снимка не доходит.
		out, err := cmdPick(root, "T-006", false, roleExec)
		if err != nil {
			t.Fatalf("pick: %v", err)
		}
		if !strings.HasPrefix(out, "model: opus\neffort: medium\n") || strings.Contains(out, "корректор:") {
			t.Fatalf("отсутствие снимка изменило вердикт: %q", out)
		}
		// Выключенный корректор обязан быть виден: без этой строки вердикт без
		// снимка неотличим от вердикта, который снимок посмотрел и сдвигать не
		// стал, и про то, что квота не читается, никто не узнает.
		if !strings.Contains(out, "снимка квоты нет") {
			t.Fatalf("отсутствие снимка прошло молча: %q", out)
		}
		if !strings.Contains(out, "agentctl quota refresh") {
			t.Fatalf("в предупреждении нет команды, которой снимок заводится: %q", out)
		}
	})

	t.Run("протухший снимок предупреждает и вверх не двигает", func(t *testing.T) {
		// Профицит по возрасту как раз за порогом: сдвиг вверх пропадает, и
		// сказать об этом надо, иначе вердикт выглядит обычным дефолтом.
		content := "taken = " + at(testNow.Add(-snapshotMaxAge-time.Minute)) + "\n" +
			"week_all = 5% сброс " + at(testNow.Add(24*time.Hour)) + "\n"
		if err := os.WriteFile(quota, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		out, err := cmdPick(root, "T-005", false, roleExec)
		if err != nil {
			t.Fatalf("pick: %v", err)
		}
		if !strings.HasPrefix(out, "model: sonnet") || strings.Contains(out, "корректор:") {
			t.Fatalf("протухший снимок поднял вердикт: %q", out)
		}
		if !strings.Contains(out, "снимок квоты снят") || !strings.Contains(out, "переснять") {
			t.Fatalf("протухший снимок прошёл молча: %q", out)
		}
	})

	t.Run("свежий снимок предупреждения не печатает", func(t *testing.T) {
		content := "taken = " + at(testNow.Add(-time.Minute)) + "\n" +
			"week_all = 50% сброс " + at(testNow.Add(halfWindow)) + "\n"
		if err := os.WriteFile(quota, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		out, err := cmdPick(root, "T-006", false, roleExec)
		if err != nil {
			t.Fatalf("pick: %v", err)
		}
		if strings.Contains(out, "снимок квоты") || strings.Contains(out, "снимка квоты") {
			t.Fatalf("рабочий снимок занял место в выводе: %q", out)
		}
	})

	t.Run("снимок без момента снятия предупреждает про возраст", func(t *testing.T) {
		content := "week_all = 5% сброс " + at(testNow.Add(24*time.Hour)) + "\n"
		if err := os.WriteFile(quota, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		out, err := cmdPick(root, "T-005", false, roleExec)
		if err != nil {
			t.Fatalf("pick: %v", err)
		}
		if !strings.Contains(out, "нет момента снятия") {
			t.Fatalf("снимок без taken прошёл молча: %q", out)
		}
	})
}

func TestReviewShift(t *testing.T) {
	// Модельная ось для роли ревью: ярус вниз, пол sonnet, и два случая без
	// спуска (дизайн и грумминг). Effort роль не считает, он приходит готовым.
	cases := []struct {
		name  string
		v     verdict
		r     row
		model string
		part  string
	}{
		{"дефолтный opus опускается до sonnet", verdict{Model: "opus"}, row{Type: "task", Cost: "M"}, "sonnet", "внимательность на диффе"},
		{"fable опускается до opus", verdict{Model: "fable"}, row{Type: "task", Cost: "L"}, "opus", "внимательность на диффе"},
		{"sonnet это пол, ниже не идём", verdict{Model: "sonnet"}, row{Type: "task", Cost: "S"}, "sonnet", "пол ревьювера"},
		{"haiku подтягивается до пола", verdict{Model: "haiku"}, row{Type: "task", Cost: "S"}, "sonnet", "ниже sonnet ревью не опускаем"},
		{"дизайн читается тем же калибром", verdict{Model: "opus"}, row{Type: "LLD", Cost: "S"}, "opus", "спуска нет"},
		{"дизайн ценой L остаётся на fable", verdict{Model: "fable"}, row{Type: "LLD", Cost: "L"}, "fable", "спуска нет"},
		{"по грумминговому вердикту ревьюить нечего", verdict{Model: "opus", Groom: true}, row{Type: "task", Cost: "XL"}, "opus", "ревьюить пока нечего"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v := c.v
			reviewShift(&v, c.r)
			if v.Model != c.model {
				t.Fatalf("модель %q, жду %q", v.Model, c.model)
			}
			if !strings.Contains(v.Reason, c.part) {
				t.Fatalf("причина %q без %q", v.Reason, c.part)
			}
		})
	}
}

func TestCmdPickReview(t *testing.T) {
	isolateQuota(t)
	fixNow(t, testNow)
	root := writeBoard(t)
	if err := os.MkdirAll(filepath.Join(root, "docs", "tasks"), 0o755); err != nil {
		t.Fatal(err)
	}

	t.Run("исполнитель opus, ревьювер sonnet с подтянутым effort", func(t *testing.T) {
		// T-002 маппингом opus/medium; ревьювер идёт ярусом ниже, а пол sonnet
		// поднимает ему глубину до high.
		out, err := cmdPick(root, "T-002", false, roleReview)
		if err != nil {
			t.Fatalf("pick --role review: %v", err)
		}
		if !strings.HasPrefix(out, "model: sonnet\neffort: high\n") {
			t.Fatalf("жду вердикт ревьювера sonnet/high, получил %q", out)
		}
		if !strings.Contains(out, "роль ревью: opus -> sonnet") {
			t.Fatalf("в причине не видно спуска на роль: %q", out)
		}
	})

	t.Run("исполнителю haiku ревьювер не достаётся дешевле пола", func(t *testing.T) {
		out, err := cmdPick(root, "T-001", false, roleReview)
		if err != nil {
			t.Fatalf("pick --role review: %v", err)
		}
		if !strings.HasPrefix(out, "model: sonnet\neffort: high\n") {
			t.Fatalf("жду пол sonnet, получил %q", out)
		}
	})

	t.Run("роль exec вердикт не трогает", func(t *testing.T) {
		out, err := cmdPick(root, "T-002", false, roleExec)
		if err != nil {
			t.Fatalf("pick: %v", err)
		}
		if !strings.HasPrefix(out, "model: opus\neffort: medium\n") {
			t.Fatalf("исполнительский вердикт поехал: %q", out)
		}
		if strings.Contains(out, "роль ревью") {
			t.Fatalf("в исполнительском вердикте появился хвост роли: %q", out)
		}
	})

	t.Run("дизайн ревьюится тем же калибром", func(t *testing.T) {
		out, err := cmdPick(root, "T-003", false, roleReview)
		if err != nil {
			t.Fatalf("pick --role review: %v", err)
		}
		if !strings.HasPrefix(out, "model: opus\neffort: xhigh\n") {
			t.Fatalf("вердикт ревью на LLD опущен: %q", out)
		}
	})

	t.Run("override модели ревьювер тоже понимает", func(t *testing.T) {
		taskFile := filepath.Join(root, "docs", "tasks", "T-005.md")
		if err := os.WriteFile(taskFile, []byte("# T-005\n\nМодель: fable (3D-графика)\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		out, err := cmdPick(root, "T-005", false, roleReview)
		if err != nil {
			t.Fatalf("pick --role review: %v", err)
		}
		if !strings.HasPrefix(out, "model: opus") {
			t.Fatalf("жду ярус ниже заданной override модели, получил %q", out)
		}
	})

	t.Run("неизвестная роль это ошибка", func(t *testing.T) {
		if _, err := cmdPick(root, "T-001", false, "тестировщик"); err == nil ||
			!strings.Contains(err.Error(), "неизвестная роль") {
			t.Fatalf("жду ошибку про роль, получил %v", err)
		}
	})

	t.Run("запись ревью в файл задачи", func(t *testing.T) {
		taskFile := filepath.Join(root, "docs", "tasks", "T-002.md")
		content := "# T-002\n\n## Ход работы\n\n- Исполнение: субагент opus/medium по вердикту pick, 2026-07-30.\n"
		if err := os.WriteFile(taskFile, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := cmdPick(root, "T-002", true, roleReview); err != nil {
			t.Fatalf("pick --role review --record: %v", err)
		}
		data, _ := os.ReadFile(taskFile)
		want := "- Ревью: субагент sonnet/high по вердикту pick, " + testNow.Format("2006-01-02") + "."
		if !strings.Contains(string(data), want) {
			t.Fatalf("строка ревью разошлась с ожидаемой:\n%s", data)
		}
	})

	// Снимок квоты кладётся последним: он меняет вердикт для всех вызовов
	// ниже по тесту.
	t.Run("корректор не уводит ревьювера ниже пола", func(t *testing.T) {
		writeQuota(t, filepath.Join(os.Getenv("HOME"), ".devkit", quotaFileName), 95, 50, halfWindow)
		out, err := cmdPick(root, "T-002", false, roleReview)
		if err != nil {
			t.Fatalf("pick --role review: %v", err)
		}
		// Корректор уже опустил исполнительский вердикт до sonnet, роль ревью
		// добавила бы второй ярус, но ниже пола не уходит.
		if !strings.HasPrefix(out, "model: sonnet") {
			t.Fatalf("ревьювер уехал ниже пола: %q", out)
		}
	})

	// Совет отложить работу разбирается отдельно от модели: он живёт в тексте
	// причины, и сверка одной первой строки его пропускала.
	t.Run("ревьюверу не советуют отложить сделанную работу", func(t *testing.T) {
		writeQuota(t, filepath.Join(os.Getenv("HOME"), ".devkit", quotaFileName), 95, 50, halfWindow)
		for _, id := range []string{"T-002", "T-006", "T-003"} {
			out, err := cmdPick(root, id, false, roleReview)
			if err != nil {
				t.Fatalf("pick %s --role review: %v", id, err)
			}
			if strings.Contains(out, "отложить") {
				t.Fatalf("в вердикте ревьювера %s остался совет отложить: %q", id, out)
			}
			// Хвост корректора при этом на месте: сдвиг объяснять всё равно надо.
			if !strings.Contains(out, "корректор: дефицит week_all") {
				t.Fatalf("вместе с советом ушёл и хвост корректора для %s: %q", id, out)
			}
			exec, err := cmdPick(root, id, false, roleExec)
			if err != nil {
				t.Fatalf("pick %s: %v", id, err)
			}
			if !strings.Contains(exec, "отложить") {
				t.Fatalf("исполнительский вердикт %s потерял совет отложить: %q", id, exec)
			}
		}
	})
}

func TestCmdPickMissing(t *testing.T) {
	root := writeBoard(t)
	if _, err := cmdPick(root, "T-999", false, roleExec); err == nil || !strings.Contains(err.Error(), "нет на доске") {
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
	if _, err := cmdPick(root, "T-001", false, roleExec); err == nil {
		t.Fatal("жду ошибку на пустой доске")
	}
}

func TestRecordExecution(t *testing.T) {
	isolateQuota(t)
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

		_, err := cmdPick(root, "T-001", true, roleExec)
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

		_, err := cmdPick(root, "T-002", true, roleExec)
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
		_, err := cmdPick(root, "T-003", true, roleExec)
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
		if _, err := cmdPick(root, "T-001", true, roleExec); err != nil {
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
		if _, err := cmdPick(root, "T-001", true, roleExec); err != nil {
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
			if _, err := cmdPick(root, "T-001", true, roleExec); err != nil {
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
		if _, err := cmdPick(root, "T-001", true, roleExec); err != nil {
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
		if _, err := cmdPick(root, "T-004", true, roleExec); err != nil {
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
		if _, err := cmdPick(root, "T-004", true, roleExec); err != nil {
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
