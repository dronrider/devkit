package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/dronrider/devkit/internal/stage"
)

func TestPick(t *testing.T) {
	// Пары вердикта целиком. Таблица гоняет тройку pickTier, pickEffort и
	// floorBaseEffort напрямую: pickTier и pickEffort считаются порознь,
	// floorBaseEffort подтягивает effort яруса base. Рабочий путь (cmdPick)
	// собирает вердикт той же тройкой, но с поправкой на override и корректор.
	// Оси независимые, но не совсем: у base есть пол effort, поэтому в таблице
	// видно, как low и medium из маппинга подтягиваются до high, а остальные
	// ярусы идут ровно по расчёту.
	cases := []struct {
		name   string
		r      row
		tier   string
		effort string
		part   string
	}{
		{"S с неопределённостью 0 совсем атомарная", row{Type: "task", Rank: "3 (0+3+0+0+0)", Cost: "S"}, tierMini, "low", "атомарная"},
		{"S с неопределённостью 1 это base, effort подтянут полом", row{Type: "task", Rank: "6 (0+3+1+0+2)", Cost: "S"}, tierBase, "high", "экономить глубину смысла нет"},
		{"S с неопределённостью 3 верхняя граница диапазона", row{Type: "task", Rank: "10 (0+3+3+0+4)", Cost: "S"}, tierPro, "high", "сильной"},
		{"M с неопределённостью 0 уходит в дефолт", row{Type: "task", Rank: "33 (25+4+0+0+4)", Cost: "M"}, tierPro, "low", "сильной"},
		{"M с неопределённостью 1 уходит в дефолт", row{Type: "task", Rank: "34 (25+4+1+0+4)", Cost: "M"}, tierPro, "medium", "сильной"},
		{"S с неопределённостью 2 уходит в дефолт", row{Type: "task", Rank: "8 (0+3+2+0+2)", Cost: "S"}, tierPro, "medium", "сильной"},
		{"M с неопределённостью 2 уходит в дефолт", row{Type: "task", Rank: "35 (25+4+2+0+4)", Cost: "M"}, tierPro, "medium", "сильной"},
		{"M с неопределённостью 3 уходит в дефолт", row{Type: "task", Rank: "36 (25+4+3+0+4)", Cost: "M"}, tierPro, "high", "сильной"},
		{"баг L с неопределённостью 1 это дефолт", row{Type: "bug", Rank: "35 (25+0+1+5+4)", Cost: "L"}, tierPro, "medium", "сильной"},
		{"LLD сильнее дешевизны", row{Type: "LLD", Rank: "10 (0+5+1+0+4)", Cost: "S"}, tierPro, "xhigh", "дизайн"},
		{"LLD ценой L уходит в max", row{Type: "LLD", Rank: "10 (0+5+1+0+4)", Cost: "L"}, tierMax, "xhigh", "сложное проектирование"},
		{"LLD ценой XL уходит в max", row{Type: "LLD", Rank: "20 (0+10+3+0+5)", Cost: "XL"}, tierMax, "xhigh", "сложное проектирование"},
		{"LLD без оценки цены остаётся на pro", row{Type: "LLD", Rank: "10 (0+5+1+0+4)", Cost: "-"}, tierPro, "xhigh", "дизайн"},
		{"LLD без неопределённости всё равно xhigh", row{Type: "LLD", Rank: "10 (0+5+0+0+5)", Cost: "S"}, tierPro, "xhigh", "дизайн"},
		// Порог готовности из RANKING.md это 4, и нижняя граница проверяется
		// отдельно: на ней вердикт обязан стать грумминговым.
		{"неопределённость 4 это уже грумминг", row{Type: "task", Rank: "63 (50+6+4+0+3)", Cost: "M"}, tierPro, "xhigh", "неопределённость 4: сначала грумминг"},
		{"неопределённость 5 это грумминг", row{Type: "task", Rank: "64 (50+6+5+0+3)", Cost: "M"}, tierPro, "xhigh", "грумминг"},
		{"XL сначала разбить", row{Type: "task", Rank: "20 (0+10+3+0+5)", Cost: "XL"}, tierPro, "xhigh", "разбить"},
		{"XL без неопределённости всё равно разбить", row{Type: "task", Rank: "20 (0+10+0+0+5)", Cost: "XL"}, tierPro, "xhigh", "разбить"},
		{"L и неопределённость 3 уходит в дефолт", row{Type: "task", Rank: "9 (0+5+3+0+1)", Cost: "L"}, tierPro, "high", "сильной"},
		{"L и неопределённость 2 уходит в дефолт", row{Type: "task", Rank: "8 (0+5+2+0+1)", Cost: "L"}, tierPro, "medium", "сильной"},
		{"L и неопределённость 0 уходит в дефолт", row{Type: "task", Rank: "7 (0+5+0+0+1)", Cost: "L"}, tierPro, "low", "сильной"},
		{"цена не оценена", row{Type: "task", Rank: "8 (0+3+1+0+4)", Cost: "-"}, tierPro, "high", "не оценена"},
		{"цена не оценена при нулевой неопределённости", row{Type: "task", Rank: "7 (0+3+0+0+4)", Cost: "-"}, tierPro, "high", "не оценена"},
		{"нечитаемый ранг с ценой S уходит в дефолт", row{Type: "task", Rank: "-", Cost: "S"}, tierPro, "high", "сильной"},
		{"нечитаемый ранг с ценой M уходит в дефолт", row{Type: "task", Rank: "-", Cost: "M"}, tierPro, "high", "сильной"},
		{"L с нечитаемым рангом уходит в дефолт", row{Type: "task", Rank: "-", Cost: "L"}, tierPro, "high", "сильной"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v := pickTier(c.r)
			v.Effort = pickEffort(c.r)
			floorBaseEffort(&v)
			if v.Tier != c.tier {
				t.Fatalf("ярус %q, жду %q", v.Tier, c.tier)
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
		// Хвост поправок после пяти слагаемых (DK-428): разбор идёт до первой
		// запятой, иначе вердикт pick встал бы на неизвестности у каждой
		// строки с бонусом или наследованием.
		{"36 (25+4+1+0+4, S+2)", 1},
		{"62 (25+5+3+0+4, S+2, от DK-473)", 3},
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

// isolateQuota уводит снимок квоты и контур харнесов во временный HOME и
// возвращает путь к снимку. Без этого вердикт зависел бы и от того, что лежит в
// снимке на машине, и от машинного маппинга ярусов: ярус разворачивается в
// модель последним шагом, и живой ~/.devkit/harness.local сдвинул бы строку
// model у всех тестов сразу. Профили при этом берутся свои, из репозитория,
// то есть маппинг выходит предложением claude-code.
func isolateQuota(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("DEVKIT_HARNESS", "")
	t.Setenv("DEVKIT_HOME", repoRoot(t))
	return quotaPath("claude-code")
}

// stageText отдаёт отмеченные этапы задачи теми же строками, какими пакет уедет
// в раздел «Ход работы» файла задачи: pick рабочего дерева больше не касается
// (DK-338), и проверять запись надо там, где она теперь лежит.
func stageText(t *testing.T, root, id string) string {
	t.Helper()
	rec, err := stage.Load(stage.Path(stage.Home(), stage.MainRoot(root), id))
	if err != nil {
		t.Fatal(err)
	}
	return strings.Join(stage.Lines(rec.Stages, timeNow()), "\n")
}

// clearStages убирает запись этапов задачи. Подтесты одного теста делят
// временный HOME, и накопленное соседом иначе доезжало бы до следующей проверки.
func clearStages(t *testing.T, root, id string) {
	t.Helper()
	if _, err := stage.Flush(stage.Home(), stage.MainRoot(root), id); err != nil {
		t.Fatal(err)
	}
}

// stageStamp это хвост строки пакета при замороженных часах: этап начат и закрыт
// одним моментом, и длительности в строке тогда нет.
func stageStamp() string { return timeNow().Format("2006-01-02 15:04") }

// repoRoot это корень devkit: тесты гоняются в agentctl, профили лежат этажом
// выше.
func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
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
		out, err := cmdPick(root, c.id, false, roleExec, "")
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
		out, err := cmdPick(root, "T-003", false, roleExec, "")
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
		out, err := cmdPick(root, "T-003", false, roleExec, "")
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
		out, err := cmdPick(root, "T-001", false, roleExec, "")
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
		out, err := cmdPick(root, "T-001", false, roleExec, "")
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
		_, err := cmdPick(root, "T-001", false, roleExec, "")
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
		out, err := cmdPick(root, "T-002", false, roleExec, "")
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
		_, err := cmdPick(root, "T-001", false, roleExec, "")
		if err == nil || !strings.Contains(err.Error(), "неизвестный ярус") {
			t.Fatalf("жду ошибку про неизвестный ярус, получил %v", err)
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
		out, err := cmdPick(root, "T-001", false, roleExec, "")
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
		out, err := cmdPick(root, "T-005", false, roleExec, "")
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
		out, err := cmdPick(root, "T-005", false, roleExec, "")
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
		out, err := cmdPick(root, "T-001", false, roleExec, "")
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
		"week_max = " + strconv.Itoa(fablePct) + "% сброс " + at(testNow.Add(left)) + "\n"
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
		clearStages(t, root, "T-002")
		out, err := cmdPick(root, "T-002", true, roleExec, "")
		if err != nil {
			t.Fatalf("pick: %v", err)
		}
		// T-002 маппингом opus/medium; после сдвига на sonnet effort ещё и
		// подтягивается полом, пол считается по итоговой модели.
		if !strings.HasPrefix(out, "model: sonnet\neffort: high\n") {
			t.Fatalf("жду сдвинутый вердикт, получил %q", out)
		}
		if !strings.Contains(out, "корректор: дефицит week_all, pro -> base") {
			t.Fatalf("в человеческой строке нет хвоста корректора: %q", out)
		}
		text := stageText(t, root, "T-002")
		want := "- Разработка: субагент sonnet/high по вердикту pick (маппинг opus, корректор: дефицит week_all; " +
			"квота: week_all 95% дефицит, week_max 50%, снимок 23м назад), " + stageStamp() + "."
		if !strings.Contains(text, want) {
			t.Fatalf("строка записи разошлась с ожидаемой:\n%s", text)
		}
	})

	t.Run("профицит поднимает вердикт", func(t *testing.T) {
		writeQuota(t, quota, 5, 5, 24*time.Hour)
		out, err := cmdPick(root, "T-005", false, roleExec, "")
		if err != nil {
			t.Fatalf("pick: %v", err)
		}
		// T-005 маппингом sonnet (S с неопределённостью 1); pro жжёт и week_max,
		// поэтому подъём base -> pro требует профицита обоих бакетов сразу.
		if !strings.HasPrefix(out, "model: opus") {
			t.Fatalf("жду подъём до opus, получил %q", out)
		}
		if !strings.Contains(out, "корректор: профицит week_all, week_max, base -> pro") {
			t.Fatalf("нет хвоста корректора: %q", out)
		}
	})

	t.Run("сдвинутому вниз LLD советуют отложить дизайн", func(t *testing.T) {
		writeQuota(t, quota, 95, 50, halfWindow)
		out, err := cmdPick(root, "T-003", false, roleExec, "")
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
		out, err := cmdPick(root, "T-002", false, roleExec, "")
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
		out, err := cmdPick(root, "T-006", false, roleExec, "")
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
		out, err := cmdPick(root, "T-005", false, roleExec, "")
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
		out, err := cmdPick(root, "T-004", false, roleExec, "")
		if err != nil {
			t.Fatalf("pick: %v", err)
		}
		if !strings.HasPrefix(out, "model: opus") || strings.Contains(out, "корректор:") {
			t.Fatalf("грумминговый вердикт скорректирован: %q", out)
		}
		// Состояние квоты при этом видно: корректор к груммингу не применяется, и
		// сказать об этом надо, иначе дефицит в снимке выглядит незамеченным.
		if !strings.Contains(out, "квота: week_all 95% дефицит") ||
			!strings.Contains(out, "корректор его не двигает") {
			t.Fatalf("грумминговый вердикт молчит про квоту: %q", out)
		}
	})

	t.Run("override модели корректор не двигает", func(t *testing.T) {
		writeQuota(t, quota, 95, 50, halfWindow)
		taskFile := filepath.Join(root, "docs", "tasks", "T-002.md")
		if err := os.WriteFile(taskFile, []byte("# T-002\n\nМодель: opus\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		out, err := cmdPick(root, "T-002", false, roleExec, "")
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
		out, err := cmdPick(root, "T-001", false, roleExec, "")
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
		out, err := cmdPick(root, "T-001", false, roleExec, "")
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
		out, err := cmdPick(root, "T-006", false, roleExec, "")
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
		out, err := cmdPick(root, "T-005", false, roleExec, "")
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

	t.Run("свежий снимок виден состоянием, а не предупреждением", func(t *testing.T) {
		content := "taken = " + at(testNow.Add(-time.Minute)) + "\n" +
			"week_all = 50% сброс " + at(testNow.Add(halfWindow)) + "\n" +
			"week_max = 50% сброс " + at(testNow.Add(halfWindow)) + "\n"
		if err := os.WriteFile(quota, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		out, err := cmdPick(root, "T-006", false, roleExec, "")
		if err != nil {
			t.Fatalf("pick: %v", err)
		}
		if strings.Contains(out, "снимок квоты") || strings.Contains(out, "снимка квоты") {
			t.Fatalf("рабочий снимок занял место в выводе предупреждением: %q", out)
		}
		// Сдвига нет, и без этой строки вердикт по снимку в норме читался бы так
		// же, как вердикт, для которого снимка не нашлось вовсе.
		if !strings.Contains(out, "квота: week_all 50%, week_max 50%, снимок 1м назад, сдвига нет") {
			t.Fatalf("состояние квоты в вердикте не названо: %q", out)
		}
	})

	t.Run("снимок из будущего вверх не двигает и предупреждает", func(t *testing.T) {
		// Часы разошлись назад, и возраст снимка вышел отрицательным. Такой
		// снимок молча проходил за свежий, а профицит по нему поднимал вердикт:
		// «снят только что» тут ничего не значит, снять его могли когда угодно.
		content := "taken = " + at(testNow.Add(time.Hour)) + "\n" +
			"week_all = 5% сброс " + at(testNow.Add(24*time.Hour)) + "\n"
		if err := os.WriteFile(quota, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		out, err := cmdPick(root, "T-001", false, roleExec, "")
		if err != nil {
			t.Fatalf("pick: %v", err)
		}
		if !strings.HasPrefix(out, "model: haiku") || strings.Contains(out, "корректор:") {
			t.Fatalf("снимок из будущего поднял вердикт: %q", out)
		}
		if !strings.Contains(out, "часы разошлись") || !strings.Contains(out, "переснять") {
			t.Fatalf("снимок из будущего прошёл молча: %q", out)
		}
	})

	t.Run("снимок без момента снятия предупреждает про возраст", func(t *testing.T) {
		content := "week_all = 5% сброс " + at(testNow.Add(24*time.Hour)) + "\n"
		if err := os.WriteFile(quota, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		out, err := cmdPick(root, "T-005", false, roleExec, "")
		if err != nil {
			t.Fatalf("pick: %v", err)
		}
		if !strings.Contains(out, "нет момента снятия") {
			t.Fatalf("снимок без taken прошёл молча: %q", out)
		}
	})
}

func TestReviewShift(t *testing.T) {
	// Ярусная ось для роли ревью: ступень вниз, пол base, и два случая без
	// спуска (дизайн и грумминг). Effort роль не считает, он приходит готовым.
	cases := []struct {
		name string
		v    verdict
		r    row
		tier string
		part string
	}{
		{"дефолтный pro опускается до base", verdict{Tier: tierPro}, row{Type: "task", Cost: "M"}, tierBase, "внимательность на диффе"},
		{"max опускается до pro", verdict{Tier: tierMax}, row{Type: "task", Cost: "L"}, tierPro, "внимательность на диффе"},
		{"base это пол, ниже не идём", verdict{Tier: tierBase}, row{Type: "task", Cost: "S"}, tierBase, "пол ревьювера"},
		{"mini подтягивается до пола", verdict{Tier: tierMini}, row{Type: "task", Cost: "S"}, tierBase, "ниже base ревью не опускаем"},
		{"дизайн читается тем же калибром", verdict{Tier: tierPro}, row{Type: "LLD", Cost: "S"}, tierPro, "спуска нет"},
		{"дизайн ценой L остаётся на max", verdict{Tier: tierMax}, row{Type: "LLD", Cost: "L"}, tierMax, "спуска нет"},
		{"по грумминговому вердикту ревьюить нечего", verdict{Tier: tierPro, Groom: true}, row{Type: "task", Cost: "XL"}, tierPro, "ревьюить пока нечего"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v := c.v
			reviewShift(&v, c.r)
			if v.Tier != c.tier {
				t.Fatalf("ярус %q, жду %q", v.Tier, c.tier)
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
		out, err := cmdPick(root, "T-002", false, roleReview, "")
		if err != nil {
			t.Fatalf("pick --role review: %v", err)
		}
		if !strings.HasPrefix(out, "model: sonnet\neffort: high\n") {
			t.Fatalf("жду вердикт ревьювера sonnet/high, получил %q", out)
		}
		if !strings.Contains(out, "роль ревью: pro -> base") {
			t.Fatalf("в причине не видно спуска на роль: %q", out)
		}
	})

	t.Run("исполнителю haiku ревьювер не достаётся дешевле пола", func(t *testing.T) {
		out, err := cmdPick(root, "T-001", false, roleReview, "")
		if err != nil {
			t.Fatalf("pick --role review: %v", err)
		}
		if !strings.HasPrefix(out, "model: sonnet\neffort: high\n") {
			t.Fatalf("жду пол sonnet, получил %q", out)
		}
	})

	t.Run("роль exec вердикт не трогает", func(t *testing.T) {
		out, err := cmdPick(root, "T-002", false, roleExec, "")
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
		out, err := cmdPick(root, "T-003", false, roleReview, "")
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
		out, err := cmdPick(root, "T-005", false, roleReview, "")
		if err != nil {
			t.Fatalf("pick --role review: %v", err)
		}
		if !strings.HasPrefix(out, "model: opus") {
			t.Fatalf("жду ярус ниже заданной override модели, получил %q", out)
		}
	})

	t.Run("неизвестная роль это ошибка", func(t *testing.T) {
		if _, err := cmdPick(root, "T-001", false, "тестировщик", ""); err == nil ||
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
		clearStages(t, root, "T-002")
		if _, err := cmdPick(root, "T-002", true, roleReview, ""); err != nil {
			t.Fatalf("pick --role review --record: %v", err)
		}
		text := stageText(t, root, "T-002")
		want := "- Ревью: субагент sonnet/high по вердикту pick" + noQuotaNote + ", " + stageStamp() + "."
		if !strings.Contains(text, want) {
			t.Fatalf("строка ревью разошлась с ожидаемой:\n%s", text)
		}
		data, _ := os.ReadFile(taskFile)
		if strings.Contains(string(data), "Ревью: субагент") {
			t.Fatalf("вердикт ревью правил файл задачи, а этого больше не бывает:\n%s", data)
		}
	})

	// Снимок квоты кладётся последним: он меняет вердикт для всех вызовов
	// ниже по тесту.
	t.Run("корректор не уводит ревьювера ниже пола", func(t *testing.T) {
		writeQuota(t, quotaPath("claude-code"), 95, 50, halfWindow)
		out, err := cmdPick(root, "T-002", false, roleReview, "")
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
		writeQuota(t, quotaPath("claude-code"), 95, 50, halfWindow)
		for _, id := range []string{"T-002", "T-006", "T-003"} {
			out, err := cmdPick(root, id, false, roleReview, "")
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
			exec, err := cmdPick(root, id, false, roleExec, "")
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
	if _, err := cmdPick(root, "T-999", false, roleExec, ""); err == nil || !strings.Contains(err.Error(), "нет на доске") {
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
	if _, err := cmdPick(root, "T-001", false, roleExec, ""); err == nil {
		t.Fatal("жду ошибку на пустой доске")
	}
}

// noQuotaNote это хвост строки записи там, где снимка нет: временный HOME
// тестов пуст, и такой хвост получает всякая запись без своего снимка.
const noQuotaNote = " (квота: снимка нет, корректор выключен)"

// TestRecordQuotaState проверяет главное требование DK-018: по строке в файле
// задачи видно, на каких данных выбрана модель. Без состояния квоты три разных
// случая (корректора нет, снимок в норме, снимок протух) дают одну и ту же
// запись, и разобрать по ней закрытую задачу нельзя.
func TestRecordQuotaState(t *testing.T) {
	quota := isolateQuota(t)
	fixNow(t, testNow)
	root := writeBoard(t)
	if err := os.MkdirAll(filepath.Join(root, "docs", "tasks"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(quota), 0o755); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		snap string
		id   string
		want string
	}{
		{
			name: "снимка нет",
			id:   "T-002",
			want: "- Разработка: субагент opus/medium по вердикту pick (квота: снимка нет, корректор выключен), ",
		},
		{
			name: "снимок в норме, сдвига нет",
			snap: "taken = " + at(testNow.Add(-freshAge)) + "\n" +
				"week_all = 50% сброс " + at(testNow.Add(halfWindow)) + "\n" +
				"week_max = 50% сброс " + at(testNow.Add(halfWindow)) + "\n",
			id:   "T-002",
			want: "- Разработка: субагент opus/medium по вердикту pick (квота: week_all 50%, week_max 50%, снимок 23м назад, сдвига нет), ",
		},
		{
			name: "дефицит сдвинул вердикт вниз",
			snap: "taken = " + at(testNow.Add(-time.Minute)) + "\n" +
				"week_all = 95% сброс " + at(testNow.Add(halfWindow)) + "\n" +
				"week_max = 50% сброс " + at(testNow.Add(halfWindow)) + "\n",
			id: "T-002",
			want: "- Разработка: субагент sonnet/high по вердикту pick (маппинг opus, корректор: дефицит week_all; " +
				"квота: week_all 95% дефицит, week_max 50%, снимок 1м назад), ",
		},
		{
			// Профицит по протухшему снимку вверх не двигает, и запись обязана
			// отличаться от записи по свежему снимку в норме: решение тут принято
			// вслепую, а не по остатку.
			name: "протухший снимок назван протухшим",
			snap: "taken = " + at(testNow.Add(-3*time.Hour)) + "\n" +
				"week_all = 5% сброс " + at(testNow.Add(24*time.Hour)) + "\n",
			id:   "T-005",
			want: "- Разработка: субагент sonnet/high по вердикту pick (квота: week_all 5% профицит, снимок 3ч 0м назад, протух, сдвига нет), ",
		},
		{
			// Подъём на max платит уже из двух бакетов, и в записи обязаны быть оба:
			// по одному week_all не восстановить, чем вердикт будет платить.
			name: "подъём на max несёт оба бакета",
			snap: "taken = " + at(testNow.Add(-time.Minute)) + "\n" +
				"week_all = 5% сброс " + at(testNow.Add(24*time.Hour)) + "\n" +
				"week_max = 5% сброс " + at(testNow.Add(24*time.Hour)) + "\n",
			id: "T-003",
			want: "- Разработка: субагент fable/xhigh по вердикту pick (маппинг opus, корректор: профицит week_all, week_max; " +
				"квота: week_all 5% профицит, week_max 5% профицит, снимок 1м назад), ",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.snap == "" {
				if err := os.RemoveAll(quota); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(quota, []byte(c.snap), 0o644); err != nil {
				t.Fatal(err)
			}
			taskFile := filepath.Join(root, "docs", "tasks", c.id+".md")
			if err := os.WriteFile(taskFile, []byte("# "+c.id+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			clearStages(t, root, c.id)
			if _, err := cmdPick(root, c.id, true, roleExec, ""); err != nil {
				t.Fatalf("pick --record: %v", err)
			}
			text := stageText(t, root, c.id)
			want := c.want + stageStamp() + "."
			if !strings.Contains(text, want) {
				t.Fatalf("строка записи разошлась с ожидаемой\nжду: %s\nвижу:\n%s", want, text)
			}
		})
	}

	t.Run("повторный record не задваивает состояние", func(t *testing.T) {
		if err := os.WriteFile(quota, []byte("taken = "+at(testNow.Add(-time.Minute))+"\n"+
			"week_all = 50% сброс "+at(testNow.Add(halfWindow))+"\n"+
			"week_max = 50% сброс "+at(testNow.Add(halfWindow))+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		taskFile := filepath.Join(root, "docs", "tasks", "T-002.md")
		if err := os.WriteFile(taskFile, []byte("# T-002\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		clearStages(t, root, "T-002")
		for i := 0; i < 2; i++ {
			if _, err := cmdPick(root, "T-002", true, roleExec, ""); err != nil {
				t.Fatalf("pick --record, вызов %d: %v", i+1, err)
			}
		}
		text := stageText(t, root, "T-002")
		for _, line := range strings.Split(text, "\n") {
			if n := strings.Count(line, "квота:"); n > 1 {
				t.Fatalf("состояние квоты в строке названо %d раза: %q", n, line)
			}
		}
		if n := strings.Count(text, "квота: week_all 50%, week_max 50%, снимок 1м назад, сдвига нет"); n != 2 {
			t.Fatalf("жду по одному состоянию на каждую из двух записей, вижу %d:\n%s", n, text)
		}
	})
}

func TestRecordStage(t *testing.T) {
	isolateQuota(t)
	fixNow(t, testNow)
	root := writeBoard(t)
	if err := os.MkdirAll(filepath.Join(root, "docs", "tasks"), 0o755); err != nil {
		t.Fatal(err)
	}

	t.Run("этап разработки отмечен видом и временем", func(t *testing.T) {
		taskFile := filepath.Join(root, "docs", "tasks", "T-001.md")
		content := "# T-001\n\nОписание задачи.\n"
		if err := os.WriteFile(taskFile, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		clearStages(t, root, "T-001")
		if _, err := cmdPick(root, "T-001", true, roleExec, ""); err != nil {
			t.Fatalf("pick --record: %v", err)
		}
		rec, err := stage.Load(stage.Path(stage.Home(), stage.MainRoot(root), "T-001"))
		if err != nil {
			t.Fatal(err)
		}
		live, ok := rec.Live()
		if !ok {
			t.Fatal("этап не отмечен вовсе")
		}
		if live.Kind != stage.Dev {
			t.Fatalf("вид деятельности %q, жду %q", live.Kind, stage.Dev)
		}
		if !live.Start.Equal(testNow) {
			t.Fatalf("время начала %s, жду %s", live.Start, testNow)
		}
		if !strings.Contains(live.Note, "субагент haiku/low по вердикту pick") {
			t.Fatalf("в тексте записи нет исполнителя: %q", live.Note)
		}
	})

	t.Run("файл задачи вердикт не трогает", func(t *testing.T) {
		// Исходный баг DK-120: строка вердикта ложилась правкой рабочего дерева,
		// ревьювер её не коммитил, и merge отказывал на незакоммиченном. Правки
		// больше нет вовсе, поэтому и коммитить нечего.
		taskFile := filepath.Join(root, "docs", "tasks", "T-002.md")
		content := "# T-002\n\n## Ход работы\n\n- Разработка: было.\n"
		if err := os.WriteFile(taskFile, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		clearStages(t, root, "T-002")
		if _, err := cmdPick(root, "T-002", true, roleExec, ""); err != nil {
			t.Fatalf("pick --record: %v", err)
		}
		data, err := os.ReadFile(taskFile)
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != content {
			t.Fatalf("вердикт правил файл задачи:\n%s", data)
		}
	})

	t.Run("ошибка без файла задачи", func(t *testing.T) {
		_, err := cmdPick(root, "T-003", true, roleExec, "")
		if err == nil {
			t.Fatal("жду ошибку при отсутствии файла задачи")
		}
		if !strings.Contains(err.Error(), "taskctl file") {
			t.Fatalf("ошибка без подсказки про taskctl file: %v", err)
		}
	})

	t.Run("повторный вызов копит этапы в пакете", func(t *testing.T) {
		taskFile := filepath.Join(root, "docs", "tasks", "T-001.md")
		if err := os.WriteFile(taskFile, []byte("# T-001\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		clearStages(t, root, "T-001")
		if _, err := cmdPick(root, "T-001", true, roleExec, ""); err != nil {
			t.Fatalf("вердикт исполнителя: %v", err)
		}
		if _, err := cmdPick(root, "T-001", true, roleReview, ""); err != nil {
			t.Fatalf("вердикт ревьювера: %v", err)
		}
		rec, err := stage.Load(stage.Path(stage.Home(), stage.MainRoot(root), "T-001"))
		if err != nil {
			t.Fatal(err)
		}
		if len(rec.Stages) != 2 {
			t.Fatalf("жду два этапа в пакете, вижу %d: %+v", len(rec.Stages), rec.Stages)
		}
		if rec.Stages[0].Kind != stage.Dev || rec.Stages[1].Kind != stage.Review {
			t.Fatalf("порядок этапов разошёлся с порядком вердиктов: %+v", rec.Stages)
		}
	})

	t.Run("грумминговый вердикт это разработка со словом в тексте", func(t *testing.T) {
		taskFile := filepath.Join(root, "docs", "tasks", "T-004.md")
		if err := os.WriteFile(taskFile, []byte("# T-004\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		clearStages(t, root, "T-004")
		if _, err := cmdPick(root, "T-004", true, roleExec, ""); err != nil {
			t.Fatalf("pick --record: %v", err)
		}
		text := stageText(t, root, "T-004")
		if !strings.Contains(text, "- Разработка: грумминговый вердикт, субагент opus/xhigh") {
			t.Fatalf("жду разработку со словом про грумминг:\n%s", text)
		}
	})

	t.Run("override снимает грумминг, и слова про него в записи нет", func(t *testing.T) {
		// T-004 без override уходит в грумминг (неопределённость 5, см. тест
		// выше); override-строка перебивает маппинг целиком, включая Groom.
		taskFile := filepath.Join(root, "docs", "tasks", "T-004.md")
		if err := os.WriteFile(taskFile, []byte("# T-004\n\nМодель: sonnet\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		clearStages(t, root, "T-004")
		if _, err := cmdPick(root, "T-004", true, roleExec, ""); err != nil {
			t.Fatalf("pick --record: %v", err)
		}
		text := stageText(t, root, "T-004")
		if !strings.Contains(text, "- Разработка: субагент sonnet/xhigh") {
			t.Fatalf("жду запись по override:\n%s", text)
		}
		if strings.Contains(text, "грумминговый вердикт") {
			t.Fatalf("override записан груммингом:\n%s", text)
		}
	})
}

// dropWarn выбрасывает из вердикта хвост с заданными словами: хвосты идут через
// «; », и вырезать один целиком проще, чем сверять вердикты по кускам.
func dropWarn(verdict, part string) string {
	var kept []string
	for _, seg := range strings.Split(verdict, "; ") {
		if !strings.Contains(seg, part) {
			kept = append(kept, seg)
		}
	}
	return strings.Join(kept, "; ")
}

// TestCmdPickSnapshotMigration это проверка шага 3 миграции DK-033: снимок стал
// директорией по файлу на харнес, и до переезда читается старый одиночный файл.
// Сдвиги корректора обязаны совпасть по обе стороны переезда, иначе он молча
// поменял бы вердикты; отличается только хвост про старый путь, и молчать про
// него нельзя, ведь после переезда читаться будет другой файл.
func TestCmdPickSnapshotMigration(t *testing.T) {
	moved := isolateQuota(t)
	fixNow(t, testNow)
	root := writeBoard(t)

	legacy := legacyQuotaPath()
	writeQuota(t, legacy, 95, 50, halfWindow)
	before, err := cmdPick(root, "T-002", false, roleExec, "")
	if err != nil {
		t.Fatalf("pick по старому снимку: %v", err)
	}
	if !strings.HasPrefix(before, "model: sonnet\neffort: high\ntier: base\n") {
		t.Fatalf("старый снимок перестал двигать вердикт: %q", before)
	}
	if !strings.Contains(before, "корректор: дефицит week_all, pro -> base") {
		t.Fatalf("сдвига корректора по старому снимку нет: %q", before)
	}
	if !strings.Contains(before, legacy) || !strings.Contains(before, "по старому пути") {
		t.Fatalf("чтение старого пути прошло молча: %q", before)
	}

	if err := os.MkdirAll(filepath.Dir(moved), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(legacy, moved); err != nil {
		t.Fatal(err)
	}
	after, err := cmdPick(root, "T-002", false, roleExec, "")
	if err != nil {
		t.Fatalf("pick после переезда: %v", err)
	}
	if got := dropWarn(before, "по старому пути"); got != after {
		t.Fatalf("переезд снимка сдвинул вердикт\nбыло:\n%s\nстало:\n%s", got, after)
	}
	if strings.Contains(after, "по старому пути") {
		t.Fatalf("после переезда хвост про старый путь остался: %q", after)
	}
}

// TestCmdPickNewSnapshotWins: рядом лежат оба файла, читается новый. Иначе
// переезд, сделанный доктором, ничего бы не менял, а старый файл тихо решал бы
// вердикт.
func TestCmdPickNewSnapshotWins(t *testing.T) {
	quota := isolateQuota(t)
	fixNow(t, testNow)
	root := writeBoard(t)
	writeQuota(t, legacyQuotaPath(), 95, 50, halfWindow)
	writeQuota(t, quota, 5, 5, 24*time.Hour)
	out, err := cmdPick(root, "T-005", false, roleExec, "")
	if err != nil {
		t.Fatalf("pick: %v", err)
	}
	if !strings.HasPrefix(out, "model: opus") || !strings.Contains(out, "профицит week_all") {
		t.Fatalf("вердикт посчитан по старому файлу: %q", out)
	}
}
