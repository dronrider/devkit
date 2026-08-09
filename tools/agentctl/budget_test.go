package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeSnapshot кладёт снимок с точным моментом снятия и бакетами: то же, что
// делает q.write, но без обхода через *quotaSpec, чтобы фикстуры budget не
// зависели от резолва харнеса.
func writeSnapshot(t *testing.T, path string, taken time.Time, buckets ...bucket) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	if !taken.IsZero() {
		fmt.Fprintf(&b, "taken = %s\n", at(taken))
	}
	for _, bk := range buckets {
		fmt.Fprintf(&b, "%s = %d%% сброс %s\n", bk.Name, int(bk.Used*100), at(bk.Reset))
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestCmdBudget гоняет таблицу «Размер пачки» из DK-048 целиком через
// cmdBudget: фикстуры снимка с инжектированными часами (testNow), границы
// pace с обеих сторон, обе развилки профицита и все случаи потолка по
// умолчанию.
func TestCmdBudget(t *testing.T) {
	quota := isolateQuota(t)
	fixNow(t, testNow)
	root := t.TempDir()

	cases := []struct {
		name    string
		taken   time.Duration // возраст снимка на момент вызова, ноль значит снимка нет
		buckets []bucket
		limit   int
		parts   []string
	}{
		{
			"дефицит week_all сдвигает вниз, потолок 1",
			freshAge,
			[]bucket{bucketAt("week_all", 90, halfWindow), bucketAt("week_max", 50, halfWindow)},
			1,
			[]string{"дефицит week_all", "работать по одной"},
		},
		{
			"pace ровно на пороге дефицита это ещё дефицит",
			freshAge,
			[]bucket{bucketAt("week_all", 75, halfWindow)},
			1,
			[]string{"дефицит week_all"},
		},
		{
			"на процент выше порога дефицита это уже норма",
			freshAge,
			[]bucket{bucketAt("week_all", 74, halfWindow)},
			3,
			[]string{"корректор вердикт не двигает"},
		},
		{
			// pro жжёт week_max (замер DK-089), поэтому профицит одного week_all
			// больше не поднимает потолок до пяти: корректор не двигает, потолок 3.
			"профицит только week_all оставляет потолок 3",
			freshAge,
			[]bucket{bucketAt("week_all", 5, 24*time.Hour), bucketAt("week_max", 50, halfWindow)},
			3,
			[]string{"корректор вердикт не двигает"},
		},
		{
			"pace ровно на пороге профицита это ещё профицит",
			freshAge,
			[]bucket{bucketAt("week_all", 0, halfWindow), bucketAt("week_max", 0, halfWindow)},
			3,
			[]string{"поднимает opus до fable"},
		},
		{
			"на процент ниже порога профицита это ещё норма",
			freshAge,
			[]bucket{bucketAt("week_all", 1, halfWindow)},
			3,
			[]string{"корректор вердикт не двигает"},
		},
		{
			// Тот же снимок, на котором correctModel поднимает opus до fable:
			// потолок остаётся 3, а не уходит в 5, ради которых profицит одного
			// week_all существует отдельной строкой таблицы.
			"профицит week_all и week_max сразу поднимает opus до fable, потолок 3",
			freshAge,
			[]bucket{bucketAt("week_all", 5, 24*time.Hour), bucketAt("week_max", 5, 24*time.Hour)},
			3,
			[]string{"поднимает opus до fable", "week_all, week_max"},
		},
		{
			"протухший профицит не поднимает, потолок 3",
			staleAge,
			[]bucket{bucketAt("week_all", 5, 24*time.Hour)},
			3,
			[]string{"протух", "переснять", "agentctl quota refresh"},
		},
		{
			"бакет уже за датой сброса, потолок 3",
			freshAge,
			[]bucket{bucketAt("week_all", 90, -time.Hour)},
			3,
			[]string{"week_all уже за датой сброса"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			writeSnapshot(t, quota, testNow.Add(-c.taken), c.buckets...)
			out, err := cmdBudget(root, testNow)
			if err != nil {
				t.Fatalf("budget: %v", err)
			}
			if !strings.HasPrefix(out, fmt.Sprintf("batch: %d\n", c.limit)) {
				t.Fatalf("потолок разошёлся, вывод:\n%s", out)
			}
			for _, part := range c.parts {
				if !strings.Contains(out, part) {
					t.Fatalf("в выводе нет %q:\n%s", part, out)
				}
			}
		})
	}
}

// TestBudgetFreshSurplusHeterogeneous покрывает ветку freshSurplus бюджетного
// потолка (случай 5 в таблице DK-048). Для claude-code после замера DK-089 эта
// ветка недостижима: opus жжёт week_max, spend_pro совпал с spend_max, и
// профицит одного week_all поднимает корректор до fable, уводя потолок на 3.
// Ветка осталась для гетерогенной лестницы чужого харнеса, где pro и max
// расходуют разные наборы бакетов: здесь собран синтетический spec, у которого
// pro тратит из одного week_all, а max из week_all и week_other, и свежий
// профицит pro-бакета не сдвигает корректор, а budgetLimit поднимает потолок
// до пяти.
func TestBudgetFreshSurplusHeterogeneous(t *testing.T) {
	fixNow(t, testNow)
	q := &quotaSpec{
		Harness:  "synthetic",
		Buckets:  []string{"week_all", "week_other"},
		Required: "week_all",
		Spend: map[string][]string{
			tierMini: {"week_all"},
			tierBase: {"week_all"},
			tierPro:  {"week_all"},
			tierMax:  {"week_all", "week_other"},
		},
	}
	// week_all в профиците (остаток 95%, сутки из недели), week_other в норме
	// (половина окна): pro-набор [week_all] в профиците, max-набор
	// [week_all, week_other] в норме, значит корректор стоит на pro.
	s := snapOf(freshAge,
		bucketAt("week_all", 5, 24*time.Hour),
		bucketAt("week_other", 50, halfWindow),
	)
	c := correctModel(q, s, testNow)
	if c.Down {
		t.Fatalf("корректор сдвинулся вниз: %+v", c)
	}
	if c.shifted() {
		t.Fatalf("корректор ушёл с pro (%+v), а на гетерогенной лестнице не должен: профицит week_all не задевает week_other", c)
	}
	limit, why := budgetLimit(q, c, s, testNow)
	if limit != 5 {
		t.Fatalf("потолок %d, ждали 5 (freshSurplus на гетерогенной лестнице): %s", limit, why)
	}
	if !strings.Contains(why, "потолок 5") {
		t.Fatalf("причина не назвала потолок 5: %s", why)
	}
}

// TestCmdBudgetNoSnapshot: файла снимка ещё нет вовсе (свежая машина, quota
// refresh не запускали). Потолок не падает, а строка называет команду, которой
// снимок снимается.
func TestCmdBudgetNoSnapshot(t *testing.T) {
	isolateQuota(t)
	fixNow(t, testNow)
	root := t.TempDir()
	out, err := cmdBudget(root, testNow)
	if err != nil {
		t.Fatalf("budget без снимка: %v", err)
	}
	if !strings.HasPrefix(out, "batch: 3\n") {
		t.Fatalf("вывод:\n%s", out)
	}
	for _, part := range []string{"снимка квоты нет", "agentctl quota refresh"} {
		if !strings.Contains(out, part) {
			t.Fatalf("в выводе нет %q:\n%s", part, out)
		}
	}
}

// TestCmdBudgetBrokenLine: битая строка снимка не должна ронять потолок, но
// предупреждение обязано быть видно, как и у cmdQuota.
func TestCmdBudgetBrokenLine(t *testing.T) {
	quota := isolateQuota(t)
	fixNow(t, testNow)
	root := t.TempDir()
	content := "taken = " + at(testNow.Add(-freshAge)) + "\n" +
		"week_all = 34% сброс " + at(testNow.Add(halfWindow)) + "\n" +
		"строка без равенства\n"
	if err := os.MkdirAll(filepath.Dir(quota), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(quota, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := cmdBudget(root, testNow)
	if err != nil {
		t.Fatalf("budget с битой строкой: %v", err)
	}
	if !strings.HasPrefix(out, "batch: 3\n") {
		t.Fatalf("битая строка изменила потолок:\n%s", out)
	}
	if !strings.Contains(out, "не разобрана") {
		t.Fatalf("предупреждение о битой строке потерялось:\n%s", out)
	}
}

// TestCmdBudgetWithoutQuotaSection: у харнеса с пустой секцией [quota]
// снимать остаток нечем, но это не роняет потолок, budget остаётся при
// значении по умолчанию и называет причину, как это делает pick.
func TestCmdBudgetWithoutQuotaSection(t *testing.T) {
	isolateQuota(t)
	fixNow(t, testNow)
	root := t.TempDir()
	own := t.TempDir()
	if err := os.MkdirAll(filepath.Join(own, "kit", "harness"), 0o755); err != nil {
		t.Fatal(err)
	}
	text := "[detect]\nenv = \"CLAUDECODE\"\nvalue = \"1\"\nbin = \"claude\"\n\n[rules]\nmode = \"embed\"\n\n" +
		"[delegate]\nmode = \"native\"\nmap_mini = \"m1\"\nmap_base = \"m2\"\nmap_pro = \"m3\"\nmap_max = \"m4\"\n\n[hooks]\n\n[quota]\n"
	if err := os.WriteFile(filepath.Join(own, "kit", "harness", "claude-code.toml"), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEVKIT_HOME", own)
	out, err := cmdBudget(root, testNow)
	if err != nil {
		t.Fatalf("budget без секции quota: %v", err)
	}
	if !strings.HasPrefix(out, "batch: 3\n") {
		t.Fatalf("вывод:\n%s", out)
	}
	if !strings.Contains(out, "секция [quota] пуста") {
		t.Fatalf("причина отсутствия корректора потерялась:\n%s", out)
	}
}

// TestCmdBudgetLegacyPath: пока переезд снимка в директорию quota не сделан
// devkitctl doctor --fix, budget обязан читать старый одиночный файл и
// говорить об этом хвостом строки, как это делает cmdQuota.
func TestCmdBudgetLegacyPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("DEVKIT_HARNESS", "")
	t.Setenv("DEVKIT_HOME", repoRoot(t))
	fixNow(t, testNow)
	root := t.TempDir()

	legacy := legacyQuotaPath()
	writeSnapshot(t, legacy, testNow.Add(-freshAge), bucketAt("week_all", 50, halfWindow))
	out, err := cmdBudget(root, testNow)
	if err != nil {
		t.Fatalf("budget по старому пути: %v", err)
	}
	if !strings.HasPrefix(out, "batch: 3\n") {
		t.Fatalf("вывод:\n%s", out)
	}
	if !strings.Contains(out, legacy) || !strings.Contains(out, "devkitctl doctor --fix") {
		t.Fatalf("про старый путь снимка не сказано:\n%s", out)
	}
}

// TestBudgetCLI: команда зарегистрирована в main и печатает справку, как
// соседние подкоманды.
func TestBudgetCLI(t *testing.T) {
	home := t.TempDir()
	dk := t.TempDir()
	writeFile(t, dk, "kit/harness/claude-code.toml", "[detect]\nenv = \"CLAUDECODE\"\nvalue = \"1\"\nbin = \"claude\"\n\n"+
		"[rules]\nmode = \"embed\"\n\n[delegate]\nmap_mini = \"m1\"\nmap_base = \"m2\"\nmap_pro = \"m3\"\nmap_max = \"m4\"\n\n[hooks]\n\n[quota]\n")
	cmd := exec.Command("go", "run", ".", "budget")
	cmd.Env = append(os.Environ(), "HOME="+home, "DEVKIT_HOME="+dk, "DEVKIT_HARNESS=")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("budget: %v\n%s", err, out)
	}
	if !strings.HasPrefix(string(out), "batch: 3\n") {
		t.Fatalf("вывод:\n%s", out)
	}

	out, err = exec.Command("go", "run", ".", "budget", "--help").CombinedOutput()
	if err != nil {
		t.Fatalf("budget --help: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "потолок пачки") {
		t.Fatalf("в общей справке нет budget:\n%s", out)
	}

	if out, err := exec.Command("go", "run", ".", "budget", "лишний").CombinedOutput(); err == nil {
		t.Fatalf("лишний аргумент должен отказывать:\n%s", out)
	}
}
