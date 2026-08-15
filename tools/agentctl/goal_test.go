package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// goalFile кладёт файл цели в docs/tasks корня доски и возвращает путь.
func goalFile(t *testing.T, root, id, text string) string {
	t.Helper()
	dir := filepath.Join(root, "docs", "tasks")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, id+".md")
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// goalText собирает файл цели из разделов «Бюджет» и «Журнал». Соседний раздел
// идёт следом не для красоты: по нему видно, что запись витка не уезжает в
// чужой раздел и границу разбор не перешагивает.
func goalText(budget, journal string) string {
	return "# T-100: Цель: гейт бюджета\n\n## Цель\n\nПроверить гейт.\n\n" +
		"## Бюджет\n\n" + budget + "\n## Журнал\n\n" + journal + "\n## Итог\n\nПока пусто.\n"
}

func TestParseGoalBudget(t *testing.T) {
	q := specAt(t, filepath.Join(t.TempDir(), "снимок"))
	fence := "```text"
	cases := []struct {
		name    string
		text    string
		limits  []goalLimit
		tier    string
		errPart string
	}{
		{
			name:   "строки пунктами списка",
			text:   "- бюджет: week_all <= 25\n- бюджет: week_max <= 10\n- ярус: pro\n",
			limits: []goalLimit{{"week_all", 25}, {"week_max", 10}},
			tier:   tierPro,
		},
		{
			name:   "строки внутри ограды и с процентом",
			text:   fence + "\nбюджет: week_all <= 25%\nярус: base\n" + "```" + "\n",
			limits: []goalLimit{{"week_all", 25}},
			tier:   tierBase,
		},
		{
			name:   "проза раздела разбором не считается",
			text:   "Бюджет назначен пользователем.\n\nбюджет: week_all <= 5\n",
			limits: []goalLimit{{"week_all", 5}},
		},
		{
			name:   "старое имя бакета приводится к каноничному",
			text:   "бюджет: week_fable <= 3\n",
			limits: []goalLimit{{"week_max", 3}},
		},
		{
			name:   "ярус старым именем модели",
			text:   "бюджет: week_all <= 25\nярус: opus\n",
			limits: []goalLimit{{"week_all", 25}},
			tier:   tierPro,
		},
		{
			name:    "незнакомый бакет это ошибка постановки",
			text:    "бюджет: week_gpt <= 25\n",
			errPart: "незнаком",
		},
		{
			name:    "строка бюджета без сравнения это ошибка",
			text:    "бюджет: week_all 25\n",
			errPart: "жду «бакет <= число",
		},
		{
			name:    "потолок вне диапазона это ошибка",
			text:    "бюджет: week_all <= 250\n",
			errPart: "не в диапазоне",
		},
		{
			name:    "неизвестный ярус это ошибка",
			text:    "бюджет: week_all <= 25\nярус: gpt-5\n",
			errPart: "неизвестный ярус",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b, err := parseGoalBudget(goalSection(goalText(c.text, ""), goalBudgetSection), q)
			if c.errPart != "" {
				if err == nil || !strings.Contains(err.Error(), c.errPart) {
					t.Fatalf("жду ошибку про %q, получил %v", c.errPart, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("разбор: %v", err)
			}
			if len(b.Limits) != len(c.limits) {
				t.Fatalf("лимиты %v, жду %v", b.Limits, c.limits)
			}
			for i, l := range c.limits {
				if b.Limits[i] != l {
					t.Fatalf("лимит %d это %v, жду %v", i, b.Limits[i], l)
				}
			}
			if b.Tier != c.tier {
				t.Fatalf("потолок яруса %q, жду %q", b.Tier, c.tier)
			}
		})
	}
}

// TestGoalSectionBoundary: разбор берёт только свой раздел. Строка бюджета,
// заехавшая в соседний раздел файла, в лимиты попасть не должна, иначе цена
// цели считалась бы по случайному упоминанию в отчёте.
func TestGoalSectionBoundary(t *testing.T) {
	text := goalText("бюджет: week_all <= 25\n", "") + "\n## Хвост\n\nбюджет: week_all <= 90\n"
	b, err := parseGoalBudget(goalSection(text, goalBudgetSection), nil)
	if err != nil {
		t.Fatalf("разбор: %v", err)
	}
	if len(b.Limits) != 1 || b.Limits[0].Limit != 25 {
		t.Fatalf("лимиты вышли за раздел: %v", b.Limits)
	}
}

func TestParseGoalJournal(t *testing.T) {
	text := goalText("бюджет: week_all <= 25\n",
		"- виток 1: нарезка цели, continue\n"+
			"- снимок 2026-07-30T09:00: week_all 10%, week_fable 2%\n"+
			"- снимок 2026-07-30T10:00 week_all 20%\n"+
			"- снимок 2026-07-30T11:00: week_all 30%\n")
	snaps, warns := parseGoalJournal(goalSection(text, goalJournalSection))
	if len(snaps) != 2 {
		t.Fatalf("снимков %d, жду 2: %v", len(snaps), snaps)
	}
	if snaps[0].Pct["week_max"] != 2 {
		t.Fatalf("старое имя бакета не приведено к каноничному: %v", snaps[0].Pct)
	}
	if !snaps[1].Taken.Equal(time.Date(2026, 7, 30, 11, 0, 0, 0, time.Local)) {
		t.Fatalf("момент снятия %v", snaps[1].Taken)
	}
	if len(warns) != 1 || !strings.Contains(warns[0], "не разобрана") {
		t.Fatalf("про пропущенную строку не сказано: %v", warns)
	}
}

// TestSpendOf гоняет пошаговые дельты. Панель меряет от действующего лимита,
// поэтому знак дельты и обработка сброса окна тут единственная содержательная
// логика счёта.
func TestSpendOf(t *testing.T) {
	snap := func(pct ...int) []goalSnap {
		var out []goalSnap
		for _, p := range pct {
			out = append(out, goalSnap{Pct: map[string]int{"week_all": p}})
		}
		return out
	}
	cases := []struct {
		name  string
		chain []goalSnap
		want  int
	}{
		{"одна точка расхода не даёт", snap(40), 0},
		{"растущий бакет это разность", snap(10, 15, 30), 20},
		{"сброс окна берёт значение нового окна", snap(10, 60, 5, 20), 70},
		{"сброс без трат после него", snap(10, 60, 0), 50},
		{"ровное значение расхода не добавляет", snap(30, 30, 30), 0},
		{
			"снимок без бакета цепочку не рвёт",
			[]goalSnap{{Pct: map[string]int{"week_all": 10}}, {Pct: map[string]int{"week_max": 4}}, {Pct: map[string]int{"week_all": 25}}},
			15,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := spendOf(c.chain, "week_all"); got != c.want {
				t.Fatalf("расход %d, жду %d", got, c.want)
			}
		})
	}
}

// TestCmdSpendGate: обе стороны границы гейта на сквозном пути. Журнал даёт
// первую точку цепочки, живой снимок последнюю, потолок 25 проверяется ровно
// на нём и на пункт выше.
func TestCmdSpendGate(t *testing.T) {
	quota := isolateQuota(t)
	root := writeBoard(t)
	cases := []struct {
		name string
		live int
		gate string
		part string
	}{
		{"расход в рамке", 30, gateOK, "потрачено 20 из 25"},
		{"расход ровно по потолку это ещё ok", 35, gateOK, "потрачено 25 из 25"},
		{"на пункт выше потолка это уже over", 36, gateOver, "бюджет цели исчерпан по week_all"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			goal := goalFile(t, root, "T-100", goalText("бюджет: week_all <= 25\n",
				"- снимок "+at(testNow.Add(-2*time.Hour))+": week_all 10%\n"))
			writeSnapshot(t, quota, testNow.Add(-freshAge), bucketAt("week_all", c.live, halfWindow))
			out, err := cmdSpend(root, goal, false, testNow)
			if err != nil {
				t.Fatalf("spend: %v", err)
			}
			if !strings.HasPrefix(out, "gate: "+c.gate+"\n") {
				t.Fatalf("гейт разошёлся, вывод:\n%s", out)
			}
			if !strings.Contains(out, c.part) {
				t.Fatalf("в выводе нет %q:\n%s", c.part, out)
			}
		})
	}
}

// TestCmdSpendResetWindow: сброс недельного окна между витками не обнуляет
// счётчик цели. Журнал хранит 60%, живой снимок 5%: это новое окно, и расходом
// идёт оно целиком поверх набранного раньше.
func TestCmdSpendResetWindow(t *testing.T) {
	quota := isolateQuota(t)
	root := writeBoard(t)
	goal := goalFile(t, root, "T-100", goalText("бюджет: week_all <= 25\n",
		"- снимок "+at(testNow.Add(-4*time.Hour))+": week_all 10%\n"+
			"- снимок "+at(testNow.Add(-2*time.Hour))+": week_all 60%\n"))
	writeSnapshot(t, quota, testNow.Add(-freshAge), bucketAt("week_all", 5, halfWindow))
	out, err := cmdSpend(root, goal, false, testNow)
	if err != nil {
		t.Fatalf("spend: %v", err)
	}
	if !strings.Contains(out, "потрачено 55 из 25") {
		t.Fatalf("сброс окна посчитан не как расход нового окна:\n%s", out)
	}
	if !strings.HasPrefix(out, "gate: over\n") {
		t.Fatalf("исчерпанный бюджет не остановил цикл:\n%s", out)
	}
}

// TestCmdSpendNoSnapshot: снимка квоты на машине ещё нет. Гейт не падает, но
// считает по журналу и вслух зовёт снять снимок: молчание тут неотличимо от
// штатной работы.
func TestCmdSpendNoSnapshot(t *testing.T) {
	isolateQuota(t)
	root := writeBoard(t)
	goal := goalFile(t, root, "T-100", goalText("бюджет: week_all <= 25\n",
		"- снимок "+at(testNow.Add(-2*time.Hour))+": week_all 10%\n"))
	out, err := cmdSpend(root, goal, false, testNow)
	if err != nil {
		t.Fatalf("spend без снимка: %v", err)
	}
	if !strings.HasPrefix(out, "gate: ok\n") {
		t.Fatalf("вывод:\n%s", out)
	}
	for _, part := range []string{"снимка квоты нет", "agentctl quota refresh", "потрачено 0 из 25"} {
		if !strings.Contains(out, part) {
			t.Fatalf("в выводе нет %q:\n%s", part, out)
		}
	}
}

// TestCmdSpendStaleSnapshot: протухший снимок гейт не роняет, но виток
// начинается с переснятия, и строка об этом говорит.
func TestCmdSpendStaleSnapshot(t *testing.T) {
	quota := isolateQuota(t)
	root := writeBoard(t)
	goal := goalFile(t, root, "T-100", goalText("бюджет: week_all <= 25\n", ""))
	writeSnapshot(t, quota, testNow.Add(-staleAge), bucketAt("week_all", 40, halfWindow))
	out, err := cmdSpend(root, goal, false, testNow)
	if err != nil {
		t.Fatalf("spend по протухшему снимку: %v", err)
	}
	if !strings.HasPrefix(out, "gate: ok\n") {
		t.Fatalf("протухший снимок уронил гейт:\n%s", out)
	}
	if !strings.Contains(out, "протух") || !strings.Contains(out, "agentctl quota refresh") {
		t.Fatalf("про возраст снимка не сказано:\n%s", out)
	}
}

// TestCmdSpendBrokenJournalLine: битая строка журнала пропускается, гейт
// работает по остальным, а предупреждение видно в выводе. Расход по неполной
// цепочке занижен, и молчать про это нельзя.
func TestCmdSpendBrokenJournalLine(t *testing.T) {
	quota := isolateQuota(t)
	root := writeBoard(t)
	goal := goalFile(t, root, "T-100", goalText("бюджет: week_all <= 25\n",
		"- снимок "+at(testNow.Add(-2*time.Hour))+": week_all мусор\n"))
	writeSnapshot(t, quota, testNow.Add(-freshAge), bucketAt("week_all", 40, halfWindow))
	out, err := cmdSpend(root, goal, false, testNow)
	if err != nil {
		t.Fatalf("spend с битой строкой: %v", err)
	}
	if !strings.HasPrefix(out, "gate: ok\n") {
		t.Fatalf("битая строка уронила гейт:\n%s", out)
	}
	if !strings.Contains(out, "не разобрана") {
		t.Fatalf("предупреждение о битой строке потерялось:\n%s", out)
	}
}

// TestCmdSpendWithoutLimits: раздел «Бюджет» без строк лимита это ошибка
// постановки. Гейт без цены пустил бы цикл на любые траты.
func TestCmdSpendWithoutLimits(t *testing.T) {
	isolateQuota(t)
	root := writeBoard(t)
	goal := goalFile(t, root, "T-100", goalText("Бюджет обсудим потом.\n", ""))
	if _, err := cmdSpend(root, goal, false, testNow); err == nil ||
		!strings.Contains(err.Error(), "нет ни одной строки") {
		t.Fatalf("жду отказ без строк бюджета, получил %v", err)
	}
}

// TestCmdSpendUnknownBucket: незнакомый харнесу бакет это ошибка постановки, а
// не молчаливый пропуск: посчитанный без него расход выглядел бы штатным.
func TestCmdSpendUnknownBucket(t *testing.T) {
	isolateQuota(t)
	root := writeBoard(t)
	goal := goalFile(t, root, "T-100", goalText("бюджет: week_gpt <= 25\n", ""))
	if _, err := cmdSpend(root, goal, false, testNow); err == nil ||
		!strings.Contains(err.Error(), "week_gpt") {
		t.Fatalf("жду отказ по незнакомому бакету, получил %v", err)
	}
}

// TestCmdSpendRecord: строка снимка дописывается в конец «Журнала», соседние
// разделы остаются как были, а повторный прогон читает записанное как
// предыдущую точку цепочки и расход не удваивает.
func TestCmdSpendRecord(t *testing.T) {
	quota := isolateQuota(t)
	root := writeBoard(t)
	goal := goalFile(t, root, "T-100", goalText("бюджет: week_all <= 25\n",
		"- снимок "+at(testNow.Add(-2*time.Hour))+": week_all 10%\n"))
	writeSnapshot(t, quota, testNow.Add(-freshAge), bucketAt("week_all", 20, halfWindow),
		bucketAt("week_max", 4, halfWindow))
	if _, err := cmdSpend(root, goal, true, testNow); err != nil {
		t.Fatalf("spend --record: %v", err)
	}
	data, err := os.ReadFile(goal)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	line := "- снимок " + at(testNow.Add(-freshAge)) + ": week_all 20%, week_max 4%"
	if !strings.Contains(text, line) {
		t.Fatalf("строки снимка нет в файле цели:\n%s", text)
	}
	journal := strings.Index(text, goalJournalSection)
	total := strings.Index(text, "## Итог")
	if at := strings.Index(text, line); at < journal || at > total {
		t.Fatalf("запись уехала из «Журнала»:\n%s", text)
	}
	if !strings.Contains(text, "## Итог\n\nПока пусто.\n") {
		t.Fatalf("соседний раздел задет:\n%s", text)
	}

	out, err := cmdSpend(root, goal, false, testNow)
	if err != nil {
		t.Fatalf("повторный spend: %v", err)
	}
	if !strings.Contains(out, "потрачено 10 из 25") {
		t.Fatalf("записанный снимок посчитан дважды:\n%s", out)
	}
}

// TestCmdSpendRecordWithoutSnapshot: записывать в журнал нечего, и гейт об
// этом говорит вслух, а не делает вид, что виток записан.
func TestCmdSpendRecordWithoutSnapshot(t *testing.T) {
	isolateQuota(t)
	root := writeBoard(t)
	goal := goalFile(t, root, "T-100", goalText("бюджет: week_all <= 25\n", ""))
	out, err := cmdSpend(root, goal, true, testNow)
	if err != nil {
		t.Fatalf("spend --record без снимка: %v", err)
	}
	if !strings.Contains(out, "записывать в журнал нечего") {
		t.Fatalf("молчаливая запись:\n%s", out)
	}
	data, err := os.ReadFile(goal)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), goalSnapPrefix) {
		t.Fatalf("в журнал попала строка без снимка:\n%s", data)
	}
}

// TestCmdSpendMissingGoal: файла цели нет, значит отказ с названным путём, а не
// гейт по пустому бюджету.
func TestCmdSpendMissingGoal(t *testing.T) {
	isolateQuota(t)
	root := writeBoard(t)
	if _, err := cmdSpend(root, "docs/tasks/T-999.md", false, testNow); err == nil ||
		!strings.Contains(err.Error(), "файла цели нет") {
		t.Fatalf("жду отказ по отсутствующему файлу, получил %v", err)
	}
}

// TestSpendCLI: команда зарегистрирована в main, путь цели берётся
// относительно корня доски, а без --goal она отказывает.
func TestSpendCLI(t *testing.T) {
	home := t.TempDir()
	root := writeBoard(t)
	goalFile(t, root, "T-100", goalText("бюджет: week_all <= 25\n", ""))
	cmd := exec.Command("go", "run", ".", "-C", root, "spend", "--goal", "docs/tasks/T-100.md")
	cmd.Env = append(os.Environ(), "HOME="+home, "DEVKIT_HOME="+repoRoot(t), "DEVKIT_HARNESS=")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("spend: %v\n%s", err, out)
	}
	if !strings.HasPrefix(string(out), "gate: ok\n") {
		t.Fatalf("вывод:\n%s", out)
	}

	cmd = exec.Command("go", "run", ".", "-C", root, "spend")
	cmd.Env = append(os.Environ(), "HOME="+home, "DEVKIT_HOME="+repoRoot(t), "DEVKIT_HARNESS=")
	if out, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("spend без --goal должен отказывать:\n%s", out)
	}

	out, err = exec.Command("go", "run", ".", "--help").CombinedOutput()
	if err != nil {
		t.Fatalf("справка: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "spend --goal") {
		t.Fatalf("в общей справке нет spend:\n%s", out)
	}
}

// TestCapTier: сама таблица потолка, без обхода через файлы. Потолок только
// опускает, грумминг ниже pro не режет и effort не трогает.
func TestCapTier(t *testing.T) {
	cases := []struct {
		name string
		v    verdict
		cap  string
		tier string
		part string
	}{
		{"вердикт выше потолка режется", verdict{Tier: tierMax, Effort: "xhigh"}, tierPro, tierPro, "max -> pro"},
		{"вердикт ровно по потолку остаётся", verdict{Tier: tierPro, Effort: "medium"}, tierPro, tierPro, "и так не выше"},
		{"вердикт ниже потолка вверх не подтягивается", verdict{Tier: tierMini, Effort: "low"}, tierPro, tierMini, "и так не выше"},
		{"грумминг ниже pro не режется", verdict{Tier: tierPro, Effort: "xhigh", Groom: true}, tierMini, tierPro, "ниже pro не режется"},
		{"грумминг режется до pro, но не ниже", verdict{Tier: tierMax, Effort: "xhigh", Groom: true}, tierBase, tierPro, "max -> pro"},
		{"потолок max ничего не режет", verdict{Tier: tierPro, Effort: "medium"}, tierMax, tierPro, "и так не выше"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v := c.v
			note := capTier(&v, c.cap)
			if v.Tier != c.tier {
				t.Fatalf("ярус %q, жду %q", v.Tier, c.tier)
			}
			if v.Effort != c.v.Effort {
				t.Fatalf("потолок сдвинул effort: %q вместо %q", v.Effort, c.v.Effort)
			}
			if !strings.Contains(note, c.part) {
				t.Fatalf("причина %q без %q", note, c.part)
			}
		})
	}
}

// TestCmdPickGoalCap: потолок цели на сквозном пути pick. Он идёт последним
// шагом ярусной оси, поэтому проверяется вместе с override и корректором, а не
// сам по себе.
func TestCmdPickGoalCap(t *testing.T) {
	quota := isolateQuota(t)
	fixNow(t, testNow)
	root := writeBoard(t)
	goal := "docs/tasks/T-100.md"
	setGoal := func(t *testing.T, tier string) {
		t.Helper()
		line := ""
		if tier != "" {
			line = "ярус: " + tier + "\n"
		}
		goalFile(t, root, "T-100", goalText("бюджет: week_all <= 25\n"+line, ""))
	}

	t.Run("вердикт выше потолка режется с причиной", func(t *testing.T) {
		setGoal(t, tierBase)
		writeSnapshot(t, quota, testNow.Add(-freshAge), bucketAt("week_all", 50, halfWindow))
		out, err := cmdPick(root, "T-002", false, roleExec, goal)
		if err != nil {
			t.Fatalf("pick: %v", err)
		}
		if !strings.HasPrefix(out, "model: sonnet") || !strings.Contains(out, "tier: base") {
			t.Fatalf("вердикт не срезан потолком:\n%s", out)
		}
		if !strings.Contains(out, "потолок цели: base, pro -> base") {
			t.Fatalf("причина среза не названа:\n%s", out)
		}
	})

	t.Run("вердикт ниже потолка остаётся как был", func(t *testing.T) {
		setGoal(t, tierPro)
		writeSnapshot(t, quota, testNow.Add(-freshAge), bucketAt("week_all", 50, halfWindow))
		out, err := cmdPick(root, "T-001", false, roleExec, goal)
		if err != nil {
			t.Fatalf("pick: %v", err)
		}
		if !strings.HasPrefix(out, "model: haiku") || !strings.Contains(out, "tier: mini") {
			t.Fatalf("потолок подтянул вердикт вверх:\n%s", out)
		}
		if !strings.Contains(out, "effort: low") {
			t.Fatalf("потолок сдвинул effort:\n%s", out)
		}
	})

	t.Run("потолок режет подъём корректора, а не наоборот", func(t *testing.T) {
		// Свежий профицит week_all и week_max поднимает вердикт T-002 до max.
		// Потолок стоит после корректора, поэтому итог это pro; примени его
		// раньше, вердикт уехал бы обратно на max.
		setGoal(t, tierPro)
		writeSnapshot(t, quota, testNow.Add(-freshAge),
			bucketAt("week_all", 5, 24*time.Hour), bucketAt("week_max", 5, 24*time.Hour))
		out, err := cmdPick(root, "T-002", false, roleExec, goal)
		if err != nil {
			t.Fatalf("pick: %v", err)
		}
		if !strings.Contains(out, "tier: pro") {
			t.Fatalf("потолок не срезал подъём корректора:\n%s", out)
		}
		if !strings.Contains(out, "профицит") || !strings.Contains(out, "потолок цели: pro, max -> pro") {
			t.Fatalf("в причине нет обоих шагов ярусной оси:\n%s", out)
		}
	})

	t.Run("override не пробивает потолок", func(t *testing.T) {
		setGoal(t, tierBase)
		writeSnapshot(t, quota, testNow.Add(-freshAge), bucketAt("week_all", 50, halfWindow))
		taskFile := filepath.Join(root, "docs", "tasks", "T-006.md")
		if err := os.WriteFile(taskFile, []byte("# T-006\n\nМодель: max (3D-графика)\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		out, err := cmdPick(root, "T-006", false, roleExec, goal)
		if err != nil {
			t.Fatalf("pick: %v", err)
		}
		if !strings.Contains(out, "tier: base") {
			t.Fatalf("override пробил бюджетную рамку цели:\n%s", out)
		}
		if !strings.Contains(out, "потолок цели: base, max -> base") {
			t.Fatalf("причина среза не названа:\n%s", out)
		}
	})

	t.Run("грумминговый вердикт ниже pro не режется", func(t *testing.T) {
		setGoal(t, tierMini)
		writeSnapshot(t, quota, testNow.Add(-freshAge), bucketAt("week_all", 50, halfWindow))
		out, err := cmdPick(root, "T-004", false, roleExec, goal)
		if err != nil {
			t.Fatalf("pick: %v", err)
		}
		if !strings.Contains(out, "tier: pro") {
			t.Fatalf("нарезка цели ушла дешёвой модели:\n%s", out)
		}
		if !strings.Contains(out, "ниже pro не режется") {
			t.Fatalf("причина не названа:\n%s", out)
		}
	})

	t.Run("без строки яруса вердикт работает как обычно", func(t *testing.T) {
		setGoal(t, "")
		writeSnapshot(t, quota, testNow.Add(-freshAge), bucketAt("week_all", 50, halfWindow))
		out, err := cmdPick(root, "T-002", false, roleExec, goal)
		if err != nil {
			t.Fatalf("pick: %v", err)
		}
		if !strings.Contains(out, "tier: pro") || strings.Contains(out, "потолок цели") {
			t.Fatalf("потолка в файле нет, а вердикт про него говорит:\n%s", out)
		}
	})

	t.Run("файла цели нет, значит отказ", func(t *testing.T) {
		if _, err := cmdPick(root, "T-002", false, roleExec, "docs/tasks/T-999.md"); err == nil ||
			!strings.Contains(err.Error(), "файла цели нет") {
			t.Fatalf("жду отказ, получил %v", err)
		}
	})
}

// TestCmdPickGoalCapRecord: строка в файле задачи несёт исходный маппинг и
// причину среза. Без этого по «Ходу работы» не понять, почему исполнитель
// разошёлся с таблицей.
func TestCmdPickGoalCapRecord(t *testing.T) {
	quota := isolateQuota(t)
	fixNow(t, testNow)
	root := writeBoard(t)
	goalFile(t, root, "T-100", goalText("бюджет: week_all <= 25\nярус: base\n", ""))
	writeSnapshot(t, quota, testNow.Add(-freshAge), bucketAt("week_all", 50, halfWindow))
	taskFile := filepath.Join(root, "docs", "tasks", "T-002.md")
	if err := os.WriteFile(taskFile, []byte("# T-002\n\n## Ход работы\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := cmdPick(root, "T-002", true, roleExec, "docs/tasks/T-100.md"); err != nil {
		t.Fatalf("pick --record: %v", err)
	}
	text := stageText(t, root, "T-002")
	if !strings.Contains(text, "субагент sonnet/high") {
		t.Fatalf("в записи не тот исполнитель:\n%s", text)
	}
	if !strings.Contains(text, "маппинг opus, потолок цели: base, pro -> base") {
		t.Fatalf("в записи нет исходного маппинга и причины среза:\n%s", text)
	}
}
