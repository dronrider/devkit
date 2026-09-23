package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseComponentOutcomes(t *testing.T) {
	out := "бюджет параллельности: ядра=8\n" +
		"go:shipctl        3.5s ok\n" +
		"hooks              0.9s FAIL\n" +
		"мусорная строка без формата компонента\n" +
		"FAIL hooks\nтрейсбек тут не компонент\n"
	got := parseComponentOutcomes(out)
	if len(got) != 2 {
		t.Fatalf("разобрано %d строк, ждали 2: %+v", len(got), got)
	}
	if got[0].Name != "go:shipctl" || !got[0].OK || got[0].Secs != 3.5 {
		t.Fatalf("первая строка разобрана неверно: %+v", got[0])
	}
	if got[1].Name != "hooks" || got[1].OK || got[1].Secs != 0.9 {
		t.Fatalf("вторая строка разобрана неверно: %+v", got[1])
	}
}

func TestParseComponentOutcomesEmpty(t *testing.T) {
	// Одиночная команда без построчного разбора (обычный go test ./... или
	// make check) не даёт ни одной строки компонента, и вызывающий заводит
	// синтетический компонент сам.
	if got := parseComponentOutcomes("PASS\nok  	github.com/x/y	0.012s\n"); len(got) != 0 {
		t.Fatalf("одиночная команда не должна дробиться на компоненты: %+v", got)
	}
}

func TestBareNameAndTouchesDiff(t *testing.T) {
	if bareName("go:shipctl") != "shipctl" {
		t.Fatalf("bareName не срезал префикс: %q", bareName("go:shipctl"))
	}
	if bareName("hooks") != "hooks" {
		t.Fatalf("bareName без двоеточия не должен меняться: %q", bareName("hooks"))
	}
	diff := []string{"tools/shipctl/ops.go", "docs/tasks/DK-1125.md"}
	if !touchesDiff("go:shipctl", diff) {
		t.Fatal("компонент tools/shipctl обязан считаться задетым своим диффом")
	}
	if touchesDiff("go:taskctl", diff) {
		t.Fatal("компонент tools/taskctl дифф не задевал, а посчитан задетым")
	}
	if touchesDiff("hooks", diff) {
		t.Fatal("компонент hooks дифф не задевал, а посчитан задетым")
	}
}

func TestParseSince(t *testing.T) {
	cases := []struct {
		in   string
		want time.Duration
	}{
		{"24h", 24 * time.Hour},
		{"30m", 30 * time.Minute},
		{"7d", 7 * 24 * time.Hour},
		{"2w", 14 * 24 * time.Hour},
	}
	for _, c := range cases {
		got, err := parseSince(c.in)
		if err != nil || got != c.want {
			t.Errorf("parseSince(%q) = %v, %v; ждали %v", c.in, got, err, c.want)
		}
	}
	bad := []string{"", "0d", "-1h", "неделя", "d"}
	for _, in := range bad {
		if _, err := parseSince(in); err == nil {
			t.Errorf("parseSince(%q) должен отказать", in)
		}
	}
}

func TestWriteTestLogSkipsWithoutDevkitDir(t *testing.T) {
	root := t.TempDir()
	// .devkit не заведён: писать журналу некуда, как у logRun.
	writeTestLog(root, "XR-001", []string{"a.go"}, "ok\n", true, time.Second)
	if _, err := os.Stat(filepath.Join(root, testLogPath)); err == nil {
		t.Fatal("журнал не должен появляться без каталога .devkit")
	}
}

func TestReadTestLogWindowAndBrokenLines(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".devkit"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, testLogPath)
	old := `{"time":"2020-01-01T00:00:00Z","id":"XR-001","ok":false,"diff":["a.go"],"components":[{"name":"a","ok":false,"secs":1}]}`
	fresh := `{"time":"` + time.Now().Format(time.RFC3339) + `","id":"XR-002","ok":true,"diff":["b.go"],"components":[{"name":"b","ok":true,"secs":1}]}`
	body := old + "\n" + "не json совсем\n" + fresh + "\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	recs, err := readTestLog(root, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 || recs[0].ID != "XR-002" {
		t.Fatalf("окно и порченая строка разобраны неверно: %+v", recs)
	}
}

// TestMergeForeignFailsOwnRedNotCounted: DoD «Прогон на стенде показывает оба
// исхода», первая половина. Компонент, чьё имя лежит в диффе задачи, красный
// сам по себе, и это своя краснота: слияние отбито честно, а команда счёта
// такое слияние не берёт.
func TestMergeForeignFailsOwnRedNotCounted(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	if err := os.MkdirAll(filepath.Join(root, ".devkit"), 0o755); err != nil {
		t.Fatal(err)
	}
	gitT(t, root, "checkout", "-qb", "xr-001-fix")
	write(t, root, "compA/thing.txt", "own\n")
	write(t, root, "fix_test.go", "package main\n")
	gitT(t, root, "add", ".")
	gitT(t, root, "commit", "-qm", "fix: XR-001 правка")
	test := `printf 'compA   0.1s FAIL\ncompB   0.1s ok\n'; exit 1`
	if _, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: test}); err == nil {
		t.Fatal("красный компонент должен держать слияние")
	}
	n, err := foreignFails(root, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("своя краснота (compA в диффе) не должна идти в счёт: %d", n)
	}
}

// TestMergeForeignFailsForeignRedCounted: вторая половина того же прогона на
// стенде. Красный компонент, чьё имя дифф задачи не задевает: слияние тоже
// отбито, но команда счёта должна взять его в число.
func TestMergeForeignFailsForeignRedCounted(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	if err := os.MkdirAll(filepath.Join(root, ".devkit"), 0o755); err != nil {
		t.Fatal(err)
	}
	gitT(t, root, "checkout", "-qb", "xr-001-fix")
	write(t, root, "compA/thing.txt", "own\n")
	write(t, root, "fix_test.go", "package main\n")
	gitT(t, root, "add", ".")
	gitT(t, root, "commit", "-qm", "fix: XR-001 правка")
	test := `printf 'compA   0.1s ok\ncompB   0.1s FAIL\n'; exit 1`
	if _, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: test}); err == nil {
		t.Fatal("красный компонент должен держать слияние")
	}
	n, err := foreignFails(root, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("чужая краснота (compB вне диффа) должна идти в счёт: %d", n)
	}
	msg, err := cmdForeignFails(root, "1h")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, ": 1") {
		t.Fatalf("отчёт команды должен называть число 1: %q", msg)
	}
}

// TestJournalSurvivesRevertAndRemerge: DoD «Журнал переживает откат и
// повторное слияние той же задачи». Запись первого слияния остаётся в файле
// после отката (он вне git и revert её не видит), а повторное слияние
// дописывает вторую запись рядом, не затирая первую.
func TestJournalSurvivesRevertAndRemerge(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	if err := os.MkdirAll(filepath.Join(root, ".devkit"), 0o755); err != nil {
		t.Fatal(err)
	}
	branchWithFix(t, root)
	if _, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true"}); err != nil {
		t.Fatal(err)
	}
	recs, err := readTestLog(root, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 {
		t.Fatalf("после первого слияния ждали одну запись: %d", len(recs))
	}
	if _, err := cmdRevert(root, RevertParams{ID: "XR-001"}); err != nil {
		t.Fatal(err)
	}
	recs, err = readTestLog(root, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 {
		t.Fatalf("откат не должен трогать журнал: %d записей", len(recs))
	}
	gitT(t, root, "checkout", "-qb", "xr-001-fix")
	write(t, root, "code.txt", "newer\n")
	write(t, root, "second_test.go", "package main\n")
	gitT(t, root, "add", ".")
	gitT(t, root, "commit", "-qm", "fix: XR-001 второй круг")
	if _, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: "true"}); err != nil {
		t.Fatal(err)
	}
	recs, err = readTestLog(root, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 2 {
		t.Fatalf("повторное слияние должно дописать вторую запись рядом с первой: %d", len(recs))
	}
	for _, r := range recs {
		if r.ID != "XR-001" || !r.OK {
			t.Fatalf("обе записи должны называть XR-001 зелёным прогоном: %+v", r)
		}
	}
}
