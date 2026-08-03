package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Тесты ярусной половины вердикта: псевдонимы в override, разворачивание яруса
// в модель маппингом активного харнеса и то, что вердикт не молчит, когда
// разворачивать нечем. Дизайн в docs/lld/DK-033-universal-kit.md, раздел
// «Ярусы и вердикт pick».

// setupHarness кладёт машинный конфиг во временный HOME и возвращает его путь.
// Профили при этом свои, из репозитория: правится только маппинг.
func setupHarness(t *testing.T, text string) string {
	t.Helper()
	path := filepath.Join(os.Getenv("HOME"), ".devkit", machineConfigName)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestOverrideTierAliases(t *testing.T) {
	// Строки «Модель:» в уже написанных файлах задач сделаны старыми именами,
	// и ломать их об жёсткую ошибку нельзя: псевдоним переводит имя в ярус, а
	// имя яруса принимается наравне с ним.
	isolateQuota(t)
	root := writeBoard(t)
	if err := os.MkdirAll(filepath.Join(root, "docs", "tasks"), 0o755); err != nil {
		t.Fatal(err)
	}
	cases := []struct{ value, tier, model string }{
		{"mini", tierMini, "haiku"},
		{"base", tierBase, "sonnet"},
		{"pro", tierPro, "opus"},
		{"max", tierMax, "fable"},
		{"haiku", tierMini, "haiku"},
		{"sonnet", tierBase, "sonnet"},
		{"opus", tierPro, "opus"},
		{"fable", tierMax, "fable"},
	}
	for _, c := range cases {
		t.Run("override "+c.value, func(t *testing.T) {
			taskFile := filepath.Join(root, "docs", "tasks", "T-002.md")
			if err := os.WriteFile(taskFile, []byte("# T-002\n\nМодель: "+c.value+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			out, err := cmdPick(root, "T-002", false, roleExec, "")
			if err != nil {
				t.Fatalf("pick: %v", err)
			}
			if !strings.HasPrefix(out, "model: "+c.model+"\n") {
				t.Fatalf("модель разошлась с ожидаемой: %q", out)
			}
			if !strings.Contains(out, "\ntier: "+c.tier+"\n") {
				t.Fatalf("ярус разошёлся с ожидаемым: %q", out)
			}
		})
	}

	t.Run("конкретная модель инструмента в override не принимается", func(t *testing.T) {
		// Строка файла задачи переживает смену харнеса, поэтому имя модели
		// инструмента тут запрещено так же, как опечатка.
		taskFile := filepath.Join(root, "docs", "tasks", "T-002.md")
		if err := os.WriteFile(taskFile, []byte("# T-002\n\nМодель: claude-opus-4-6\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		_, err := cmdPick(root, "T-002", false, roleExec, "")
		if err == nil || !strings.Contains(err.Error(), "неизвестный ярус") {
			t.Fatalf("жду отказ на имя модели инструмента, получил %v", err)
		}
	})
}

func TestCmdPickTierLine(t *testing.T) {
	// Третья машинная строка стоит третьей, а первые две не двигаются с мест:
	// потребители первой строки вердикта переучиваться не должны.
	isolateQuota(t)
	root := writeBoard(t)
	out, err := cmdPick(root, "T-002", false, roleExec, "")
	if err != nil {
		t.Fatalf("pick: %v", err)
	}
	lines := strings.Split(out, "\n")
	if len(lines) < 4 {
		t.Fatalf("жду четыре строки вердикта, получил %q", out)
	}
	want := []string{"model: opus", "effort: medium", "tier: pro"}
	for i, w := range want {
		if lines[i] != w {
			t.Fatalf("строка %d это %q, жду %q", i+1, lines[i], w)
		}
	}
	if !strings.HasPrefix(lines[3], "T-002 (task") {
		t.Fatalf("человеческая строка съехала: %q", lines[3])
	}
}

func TestCmdPickUnmappedHarness(t *testing.T) {
	// Ярусная половина вердикта от инструмента не зависит, поэтому
	// ненастроенный контур это не отказ, а прочерк в строке model и причина
	// хвостом.
	isolateQuota(t)
	fixNow(t, testNow)
	root := writeBoard(t)
	if err := os.MkdirAll(filepath.Join(root, "docs", "tasks"), 0o755); err != nil {
		t.Fatal(err)
	}

	t.Run("харнес включён, а маппинга нет", func(t *testing.T) {
		machine := setupHarness(t, "default = \"claude-code\"\nenabled = [\"claude-code\"]\n")
		out, err := cmdPick(root, "T-002", false, roleExec, "")
		if err != nil {
			t.Fatalf("pick: %v", err)
		}
		if !strings.HasPrefix(out, "model: -\neffort: medium\ntier: pro\n") {
			t.Fatalf("жду прочерк модели при полном ярусе, получил %q", out)
		}
		if !strings.Contains(out, "харнес claude-code не настроен") || !strings.Contains(out, machine) {
			t.Fatalf("в причине нет ни находки, ни файла, которым она чинится: %q", out)
		}
	})

	t.Run("строка записи несёт ярус, когда модели нет", func(t *testing.T) {
		setupHarness(t, "default = \"claude-code\"\nenabled = [\"claude-code\"]\n")
		taskFile := filepath.Join(root, "docs", "tasks", "T-002.md")
		if err := os.WriteFile(taskFile, []byte("# T-002\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := cmdPick(root, "T-002", true, roleExec, ""); err != nil {
			t.Fatalf("pick --record: %v", err)
		}
		data, _ := os.ReadFile(taskFile)
		if !strings.Contains(string(data), "- Исполнение: субагент pro/medium по вердикту pick") {
			t.Fatalf("в записи не осталось исполнителя:\n%s", data)
		}
	})

	t.Run("сдвиг без маппинга про модель не сочиняет", func(t *testing.T) {
		// Развернуть нечем оба конца сдвига, и хвост про холостой ход был бы
		// обещанием модели, которой в вердикте нет.
		setupHarness(t, "default = \"claude-code\"\nenabled = [\"claude-code\"]\n")
		writeQuota(t, quotaPath("claude-code"), 95, 50, halfWindow)
		out, err := cmdPick(root, "T-006", false, roleExec, "")
		if err != nil {
			t.Fatalf("pick: %v", err)
		}
		if !strings.Contains(out, "корректор: дефицит week_all, pro -> base") {
			t.Fatalf("сдвиг корректора потерялся: %q", out)
		}
		if strings.Contains(out, "модель та же") {
			t.Fatalf("хвост обещает модель, которой в вердикте нет: %q", out)
		}
		if err := os.Remove(quotaPath("claude-code")); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("харнес не определён вовсе", func(t *testing.T) {
		machine := setupHarness(t, "enabled = []\n")
		out, err := cmdPick(root, "T-001", false, roleExec, "")
		if err != nil {
			t.Fatalf("pick: %v", err)
		}
		if !strings.HasPrefix(out, "model: -\neffort: low\ntier: mini\n") {
			t.Fatalf("жду прочерк модели при полном ярусе, получил %q", out)
		}
		if !strings.Contains(out, "харнес не определён") || !strings.Contains(out, machine) {
			t.Fatalf("в причине нет ни находки, ни файла, которым она чинится: %q", out)
		}
	})
}

func TestCmdPickFoldedMapping(t *testing.T) {
	// Соседние ярусы, сложенные в одну модель, это законный маппинг, и сдвиг по
	// такой паре модель не меняет. Молчаливый холостой ход неотличим от
	// отсутствия сдвига, поэтому корректор говорит о нём хвостом.
	quota := isolateQuota(t)
	fixNow(t, testNow)
	root := writeBoard(t)
	setupHarness(t, `default = "claude-code"
enabled = ["claude-code"]

[claude-code]
mini = "small"
base = "workhorse"
pro = "workhorse"
max = "big"
`)
	writeQuota(t, quota, 95, 50, halfWindow)
	out, err := cmdPick(root, "T-002", false, roleExec, "")
	if err != nil {
		t.Fatalf("pick: %v", err)
	}
	if !strings.HasPrefix(out, "model: workhorse\neffort: high\ntier: base\n") {
		t.Fatalf("жду сдвинутый ярус при той же модели, получил %q", out)
	}
	if !strings.Contains(out, "корректор: дефицит week_all, сдвиг pro -> base, модель та же") {
		t.Fatalf("холостой сдвиг прошёл молча: %q", out)
	}
}
