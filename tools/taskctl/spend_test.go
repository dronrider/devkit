package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dronrider/devkit/internal/stage"
)

// spendStamp это момент, которым тесты подписывают ходы синтетических
// транскриптов. Срез за период режет работу по нему, а свод статей относит по
// нему же ход головной сессии к задаче.
var spendStamp = time.Date(2026, 9, 18, 12, 0, 0, 0, time.Local)

// spendTurn это ход ассистента с расходом, как его пишет харнес claude.
func spendTurn(req string, out, in, read int) string {
	rec := map[string]any{
		"type":      "assistant",
		"requestId": req,
		"timestamp": spendStamp.UTC().Format(time.RFC3339),
		"message": map[string]any{
			"usage": map[string]any{
				"output_tokens":               out,
				"input_tokens":                in,
				"cache_read_input_tokens":     read,
				"cache_creation_input_tokens": 0,
			},
		},
	}
	data, _ := json.Marshal(rec)
	return string(data) + "\n"
}

// spendWork кладёт в подменный дом поток работы субагента и её спутника.
func spendWork(t *testing.T, home, session, id, kind, parent string, out int) {
	t.Helper()
	dir := filepath.Join(home, ".claude", "projects", "slug", session, "subagents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := spendTurn("req-"+id+"-1", out/2, 100, 1000) + spendTurn("req-"+id+"-2", out-out/2, 100, 1000)
	if err := os.WriteFile(filepath.Join(dir, "agent-"+id+".jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	meta := map[string]any{"agentType": kind, "spawnDepth": 1}
	if parent != "" {
		meta["parentAgentId"] = parent
		meta["spawnDepth"] = 2
	}
	data, _ := json.Marshal(meta)
	if err := os.WriteFile(filepath.Join(dir, "agent-"+id+".meta.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestCmdSpendByStages: команда печатает строку на каждый этап задачи с видом,
// часами, номером работы и колонками расхода, а вложенная вычитка ложится под
// тот этап, который её поднял (кейсы 1 и 3 развилки «кейсы» DK-912).
func TestCmdSpendByStages(t *testing.T) {
	root := setup(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	now := time.Date(2026, 9, 18, 15, 0, 0, 0, time.Local)
	old := timeNow
	defer func() { timeNow = old }()
	timeNow = func() time.Time { return now }

	spendWork(t, home, "sess-1", "a1b2c3d4e5f60718a", "exec-high", "", 60000)
	spendWork(t, home, "sess-1", "b2c3d4e5f60718a1c", "proofread", "a1b2c3d4e5f60718a", 4000)
	spendWork(t, home, "sess-1", "c3d4e5f60718a1b2d", "review-high", "", 14000)

	dev := stage.Stage{Kind: stage.Dev, Start: now.Add(-3 * time.Hour), End: now.Add(-time.Hour),
		Note: "субагент opus/high по вердикту pick, работа a1b2c3d4e5f60718a, week_all 25%", Session: "sess-1", Work: "a1b2c3d4e5f60718a"}
	rev := stage.Stage{Kind: stage.Review, Start: now.Add(-50 * time.Minute), End: now.Add(-40 * time.Minute),
		Note: "субагент sonnet/high, работа c3d4e5f60718a1b2d", Session: "sess-1", Work: "c3d4e5f60718a1b2d"}
	for _, s := range []stage.Stage{dev, rev} {
		if err := stage.Put(home, root, "XR-005", s); err != nil {
			t.Fatal(err)
		}
	}

	msg, err := cmdSpend(root, "XR-005")
	if err != nil {
		t.Fatalf("cmdSpend: %v", err)
	}
	want := []string{
		"токены XR-005: ходов 6, вывод 78k, свежий вход 600, чтение кэша 6.0k; этапов 2, со счётом 2, статей 0, источник живая запись этапов",
		"- разработка 2026-09-18 12:00-14:00, работа a1b2c3d4e5f60718a: ходов 4, вывод 64k, свежий вход 400, чтение кэша 4.0k, квота week_all 25%",
		"  - вложенная работа b2c3d4e5f60718a1c (proofread): ходов 2, вывод 4.0k, свежий вход 200, чтение кэша 2.0k",
		"- ревью 2026-09-18 14:10-14:20, работа c3d4e5f60718a1b2d: ходов 2, вывод 14k, свежий вход 200, чтение кэша 2.0k",
	}
	for _, w := range want {
		if !strings.Contains(msg, w) {
			t.Fatalf("в выводе нет строки %q:\n%s", w, msg)
		}
	}
}

// TestCmdSpendNoData: этап без номера работы и работа без транскрипта
// печатаются строкой «нет данных» с причиной, а не пропускаются молча (кейсы
// 2 и 7). Заход второго харнеса выглядит отсюда так же, как стёртый журнал.
func TestCmdSpendNoData(t *testing.T) {
	root := setup(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	now := time.Date(2026, 9, 18, 15, 0, 0, 0, time.Local)
	old := timeNow
	defer func() { timeNow = old }()
	timeNow = func() time.Time { return now }

	head := stage.Stage{Kind: stage.Dev, Start: now.Add(-2 * time.Hour), End: now.Add(-time.Hour),
		Note: "головная сессия", Session: "sess-1"}
	other := stage.Stage{Kind: stage.Review, Start: now.Add(-50 * time.Minute), End: now.Add(-40 * time.Minute),
		Note: "субагент второго харнеса, работа d4e5f60718a1b2c3e", Session: "sess-1", Work: "d4e5f60718a1b2c3e"}
	for _, s := range []stage.Stage{head, other} {
		if err := stage.Put(home, root, "XR-005", s); err != nil {
			t.Fatal(err)
		}
	}
	msg, err := cmdSpend(root, "XR-005")
	if err != nil {
		t.Fatalf("cmdSpend: %v", err)
	}
	if !strings.Contains(msg, "- разработка 2026-09-18 13:00-14:00: нет данных, номера работы в записи этапа нет") {
		t.Fatalf("этап без номера работы не назван причиной:\n%s", msg)
	}
	if !strings.Contains(msg, "нет данных, транскрипта работы d4e5f60718a1b2c3e нет в журналах харнеса claude: второй харнес либо журнал стёрт") {
		t.Fatalf("работа без транскрипта не названа причиной:\n%s", msg)
	}
	if !strings.Contains(msg, "со счётом 0") {
		t.Fatalf("шапка врёт про счёт:\n%s", msg)
	}
}

// TestCmdSpendFromTaskFile: у закрытой и заархивированной задачи живой записи
// нет, и этапы берутся из строк «Хода работы», где номер работы лежит в
// тексте этапа (кейс 6).
func TestCmdSpendFromTaskFile(t *testing.T) {
	root := setup(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	spendWork(t, home, "sess-1", "a1b2c3d4e5f60718a", "exec-high", "", 20000)

	dir := filepath.Join(root, "docs", "tasks", "archive", "2026")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	doc := "# XR-777\n\n## Ход работы\n\n" +
		"- Разработка: субагент opus/high по вердикту pick, работа a1b2c3d4e5f60718a, 2026-09-10 10:00-12:30.\n" +
		"- Слияние: shipctl merge, 2026-09-10 12:31-12:36.\n"
	if err := os.WriteFile(filepath.Join(dir, "XR-777.md"), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	msg, err := cmdSpend(root, "XR-777")
	if err != nil {
		t.Fatalf("cmdSpend: %v", err)
	}
	if !strings.Contains(msg, "источник «Ход работы» XR-777.md") {
		t.Fatalf("источник не назван:\n%s", msg)
	}
	if !strings.Contains(msg, "- разработка 2026-09-10 10:00-12:30, работа a1b2c3d4e5f60718a: ходов 2, вывод 20k") {
		t.Fatalf("этап из файла задачи не сведён:\n%s", msg)
	}
	if !strings.Contains(msg, "- слияние 2026-09-10 12:31-12:36: нет данных, номера работы в записи этапа нет") {
		t.Fatalf("этап без работы в файле задачи не назван причиной:\n%s", msg)
	}
}

// TestCmdSpendPeriod: срез печатает те же числа по всем задачам периода с
// разбивкой по видам, медианой и хвостом, а задача вне периода в него не
// попадает (кейс 5).
func TestCmdSpendPeriod(t *testing.T) {
	root := setup(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	now := time.Date(2026, 9, 18, 15, 0, 0, 0, time.Local)
	old := timeNow
	defer func() { timeNow = old }()
	timeNow = func() time.Time { return now }

	spendStamp = time.Date(2026, 9, 10, 11, 0, 0, 0, time.Local)
	spendWork(t, home, "sess-1", "a1b2c3d4e5f60718a", "exec-high", "", 60000)
	spendStamp = time.Date(2026, 9, 11, 10, 10, 0, 0, time.Local)
	spendWork(t, home, "sess-1", "b2c3d4e5f60718a1c", "exec-high", "", 20000)
	spendStamp = time.Date(2026, 8, 1, 11, 0, 0, 0, time.Local)
	spendWork(t, home, "sess-1", "c3d4e5f60718a1b2d", "exec-high", "", 90000)
	spendStamp = time.Date(2026, 9, 18, 12, 0, 0, 0, time.Local)

	tasks := filepath.Join(root, "docs", "tasks")
	docs := map[string]string{
		"XR-005.md": "# XR-005\n\n## Ход работы\n\n- Разработка: работа a1b2c3d4e5f60718a, 2026-09-10 10:00-12:30.\n",
		"XR-002.md": "# XR-002\n\n## Ход работы\n\n- Ревью: работа b2c3d4e5f60718a1c, 2026-09-11 10:00-10:30.\n",
		"XR-001.md": "# XR-001\n\n## Ход работы\n\n- Разработка: работа c3d4e5f60718a1b2d, 2026-08-01 10:00-12:00.\n",
	}
	for name, body := range docs {
		if err := os.WriteFile(filepath.Join(tasks, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	p, err := parseSpendPeriod("2026-09-01", "2026-09-20")
	if err != nil {
		t.Fatal(err)
	}
	msg, err := cmdSpendPeriod(root, p)
	if err != nil {
		t.Fatalf("cmdSpendPeriod: %v", err)
	}
	if strings.Contains(msg, "XR-001") {
		t.Fatalf("задача вне периода попала в срез:\n%s", msg)
	}
	want := []string{
		"токены за 2026-09-01..2026-09-20: ходов 4, вывод 80k",
		"- разработка: ходов 2, вывод 60k, свежий вход 200, чтение кэша 2.0k, работ 1",
		"- ревью: ходов 2, вывод 20k, свежий вход 200, чтение кэша 2.0k, работ 1",
		"- медиана задачи: вывод 40k",
		"- дороже прочих по выводу: XR-005 60k, XR-002 20k",
	}
	for _, w := range want {
		if !strings.Contains(msg, w) {
			t.Fatalf("в срезе нет строки %q:\n%s", w, msg)
		}
	}
}

// TestSpendTotalLineOnClose: при закрытии в файл задачи ложится одна строка
// итога, и она переживает архивацию вместе с файлом (кейс 6). Транскрипты
// харнес чистит, и другого следа расхода у закрытой задачи не остаётся.
func TestSpendTotalLineOnClose(t *testing.T) {
	root := setup(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	now := time.Date(2026, 9, 18, 15, 0, 0, 0, time.Local)
	spendWork(t, home, "sess-1", "a1b2c3d4e5f60718a", "exec-high", "", 60000)

	path := filepath.Join(root, "docs", "tasks", "XR-005.md")
	doc := "# XR-005\n\n## Ход работы\n\n- Разработка: работа a1b2c3d4e5f60718a, 2026-09-18 10:00-12:30.\n"
	if err := os.WriteFile(path, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	tail := writeSpendTotal(root, "XR-005", now)
	if tail != ", итог токенов в ход работы" {
		t.Fatalf("хвост сообщения %q", tail)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := "- Токены: ходов 2, вывод 60k, свежий вход 200, чтение кэша 2.0k, этапов со счётом 1, статей 0, 2026-09-18."
	if !strings.Contains(string(data), want) {
		t.Fatalf("строки итога нет в файле задачи:\n%s", data)
	}
}

// TestSpendTotalLineSilentWithoutData: у задачи, где ни один этап не сведён,
// строка итога была бы нулями и врала бы про расход, поэтому её нет вовсе.
func TestSpendTotalLineSilentWithoutData(t *testing.T) {
	root := setup(t)
	t.Setenv("HOME", t.TempDir())
	if tail := writeSpendTotal(root, "XR-005", time.Now()); tail != "" {
		t.Fatalf("итог записан без данных: %q", tail)
	}
}

// TestHumanTokens: числа печатаются так, как их называет человек, разбирая
// расход руками.
func TestHumanTokens(t *testing.T) {
	cases := map[int]string{0: "0", 999: "999", 1000: "1.0k", 9999: "10.0k", 10000: "10k",
		60000: "60k", 999999: "999k", 1_200_000: "1.2M"}
	for n, want := range cases {
		if got := humanTokens(n); got != want {
			t.Errorf("humanTokens(%d) = %q, ждал %q", n, got, want)
		}
	}
}

// TestParseSpendPeriodRejects: срез без границ и с битой датой это отказ с
// подсказкой, а не пустой свод.
func TestParseSpendPeriodRejects(t *testing.T) {
	if _, err := parseSpendPeriod("", ""); err == nil {
		t.Fatal("срез без границ принят")
	}
	if _, err := parseSpendPeriod("18.09.2026", ""); err == nil {
		t.Fatal("битая дата принята")
	}
}
