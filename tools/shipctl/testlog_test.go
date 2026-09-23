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

// TestParseComponentOutcomesWithRoot: замечание ревью круга 2. Команда test
// вправе назвать корень компонента прямо в строке итога, в скобках между
// именем и длительностью, тем же приёмом, каким его уже печатает --list
// parallel.py. Строка без скобок разбирается как раньше, Root пустой.
func TestParseComponentOutcomesWithRoot(t *testing.T) {
	out := "skills          (kit/skills)  4.2s FAIL\n" +
		"go:shipctl        3.5s ok\n"
	got := parseComponentOutcomes(out)
	if len(got) != 2 {
		t.Fatalf("разобрано %d строк, ждали 2: %+v", len(got), got)
	}
	if got[0].Name != "skills" || got[0].Root != "kit/skills" || got[0].OK || got[0].Secs != 4.2 {
		t.Fatalf("строка с корнем в скобках разобрана неверно: %+v", got[0])
	}
	if got[1].Name != "go:shipctl" || got[1].Root != "" || !got[1].OK {
		t.Fatalf("строка без скобок не должна получать Root: %+v", got[1])
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

// TestResolveOwnershipUnresolvedWithoutLayout: замечание ревью круга 1. Без
// заведённой раскладки deploy.<имя>.paths и без Root в строке итога шипctl не
// угадывает границу компонента по имени: компонент неопознан, даже когда
// диффа касается путь, который совпал бы с именем по старой (снятой)
// эвристике сегмента.
func TestResolveOwnershipUnresolvedWithoutLayout(t *testing.T) {
	cfg := deployConfig{}
	diff := []string{"compA/thing.txt"}
	resolved, own := resolveOwnership(cfg, "compA", "", diff)
	if resolved || own {
		t.Fatalf("без раскладки и без Root компонент не должен считаться ни опознанным, ни своим: resolved=%v own=%v", resolved, own)
	}
}

// TestResolveOwnershipRootWithoutLayout: замечание ревью круга 2. Раскладки
// deploy.<имя>.paths у проекта нет вовсе (как сегодня у самого devkit), но
// команда test назвала корень компонента прямо в строке итога. Own
// определяется этим корнем, а не молчаливым отказом.
func TestResolveOwnershipRootWithoutLayout(t *testing.T) {
	cfg := deployConfig{}
	own := []string{"compA/thing.txt"}
	if resolved, isOwn := resolveOwnership(cfg, "compA", "compA", own); !resolved || !isOwn {
		t.Fatalf("свой диффу корень без раскладки должен резолвиться своим: resolved=%v own=%v", resolved, isOwn)
	}
	other := []string{"compB/thing.txt"}
	if resolved, isOwn := resolveOwnership(cfg, "compA", "compA", other); !resolved || isOwn {
		t.Fatalf("чужой диффу корень без раскладки должен резолвиться чужим, а не оставаться неопознанным: resolved=%v own=%v", resolved, isOwn)
	}
}

// TestResolveOwnershipLayoutOverridesRoot: раскладка deploy.<имя>.paths, когда
// заведена, главнее Root. Root тут шире (весь бакет kit/skills), раскладка
// уже (один файл скрипта), и диффа касается только бакет, а не файл из
// раскладки: своим признаётся ответ раскладки, а не более широкого Root.
func TestResolveOwnershipLayoutOverridesRoot(t *testing.T) {
	cfg := deployConfig{Components: []deployComponent{
		{Name: "check-skills", Paths: []string{"kit/skills/check-skills.py"}},
	}}
	diff := []string{"kit/skills/some-other-skill/foo.py"}
	resolved, own := resolveOwnership(cfg, "check-skills", "kit/skills", diff)
	if !resolved || own {
		t.Fatalf("раскладка должна главенствовать над более широким Root: resolved=%v own=%v", resolved, own)
	}
}

// TestResolveOwnershipWholeRepoRootAlwaysOwn: замечание ревью круга 3,
// запись 3. Компонент doctor (tools/devkitctl/parallel.py, cwd=".") держит
// корень «.», весь репозиторий. Любой путь диффа лежит внутри такой
// области, и own обязан быть true, а не застревать в false навсегда из-за
// того, что реальный путь никогда не совпадает с голой точкой буквально.
func TestResolveOwnershipWholeRepoRootAlwaysOwn(t *testing.T) {
	cfg := deployConfig{}
	diff := []string{"tools/devkitctl/devkitctl.py"}
	resolved, own := resolveOwnership(cfg, "doctor", ".", diff)
	if !resolved || !own {
		t.Fatalf("корень-точка обязан резолвиться своим для любого пути диффа: resolved=%v own=%v", resolved, own)
	}
}

// TestResolveOwnershipLayoutWithoutPathsFallsBackToRoot: замечание ревью
// круга 3, запись 4. deploy.<имя> заведён без единой deploy.<имя>.paths
// строки (deployconf.Load отдаёт Component с пустым Paths вместе с
// ошибкой, которую вызывающий молча роняет). Раньше это застревало в
// own=false навсегда: цикл по пустым Paths ничего не находит, а return
// срабатывает раньше Root. Теперь недоделанная раскладка приравнена к
// отсутствующей, и резолв падает дальше к Root.
func TestResolveOwnershipLayoutWithoutPathsFallsBackToRoot(t *testing.T) {
	cfg := deployConfig{Components: []deployComponent{
		{Name: "partial", Command: "true"},
	}}
	diff := []string{"partial/thing.go"}
	if resolved, own := resolveOwnership(cfg, "partial", "partial", diff); !resolved || !own {
		t.Fatalf("раскладка без .paths должна уступать Root, а не застревать в own=false: resolved=%v own=%v", resolved, own)
	}
	if resolved, own := resolveOwnership(cfg, "partial", "", diff); resolved || own {
		t.Fatalf("без Root та же недоделанная раскладка остаётся неопознанной, как и отсутствующая: resolved=%v own=%v", resolved, own)
	}
}

// TestResolveOwnershipExactPathMatch: заведённая раскладка распознаёт
// точное совпадение независимо от расширения. Второй кейс это замечание
// ревью про компонент-скрипт (kit/skills/check-skills.py против имени
// компонента «check-skills» без расширения): путь matching, а не имя,
// поэтому расширение больше не мешает.
func TestResolveOwnershipExactPathMatch(t *testing.T) {
	cfg := deployConfig{Components: []deployComponent{
		{Name: "check-skills", Paths: []string{"kit/skills/check-skills.py"}},
	}}
	own := []string{"kit/skills/check-skills.py"}
	if resolved, isOwn := resolveOwnership(cfg, "check-skills", "", own); !resolved || !isOwn {
		t.Fatalf("точный путь компонента-скрипта должен резолвиться своим: resolved=%v own=%v", resolved, isOwn)
	}
	other := []string{"kit/skills/some-other-skill/foo.py"}
	if resolved, isOwn := resolveOwnership(cfg, "check-skills", "", other); !resolved || isOwn {
		t.Fatalf("файл другого скилла не должен резолвиться своим для check-skills: resolved=%v own=%v", resolved, isOwn)
	}
}

// TestResolveOwnershipBucketLayoutTrustsDeclaredBoundary: замечание ревью
// про бакет parallel.py (skills, hooks, unittest discover по всему
// каталогу). Заведённая раскладка на весь каталог считается доверенной как
// есть: вложенный файл внутри объявленной границы признаётся своим. Точность
// тут решает раскладка проекта, а не шипctl: заведи её проект уже (paths на
// каждый скилл отдельно), точнее станет и разбор.
func TestResolveOwnershipBucketLayoutTrustsDeclaredBoundary(t *testing.T) {
	cfg := deployConfig{Components: []deployComponent{
		{Name: "skills", Paths: []string{"kit/skills/"}},
	}}
	diff := []string{"kit/skills/some-other-skill/foo.py"}
	resolved, own := resolveOwnership(cfg, "skills", "", diff)
	if !resolved || !own {
		t.Fatalf("объявленная граница бакета должна доверяться как есть: resolved=%v own=%v", resolved, own)
	}
}

// TestResolveOwnershipDeclaredButNotTouched: раскладка заведена, но дифф
// её область не касается. Компонент опознан и не свой, чужая краснота.
func TestResolveOwnershipDeclaredButNotTouched(t *testing.T) {
	cfg := deployConfig{Components: []deployComponent{
		{Name: "compB", Paths: []string{"compB/"}},
	}}
	diff := []string{"compA/thing.txt"}
	resolved, own := resolveOwnership(cfg, "compB", "", diff)
	if !resolved || own {
		t.Fatalf("заведённый, но не задетый компонент должен быть чужим: resolved=%v own=%v", resolved, own)
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
	// Раскладка нужна не выкату (autonomous не поднят, деплой не зовётся), а
	// разбору принадлежности компонента: без неё оба компонента остались бы
	// неопознанными, и своя краснота ничем не отличалась бы от чужой.
	writeDeployCfg(t, root, "deploy.compA = true\ndeploy.compA.paths = compA/\n"+
		"deploy.compB = true\ndeploy.compB.paths = compB/\n")
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
	writeDeployCfg(t, root, "deploy.compA = true\ndeploy.compA.paths = compA/\n"+
		"deploy.compB = true\ndeploy.compB.paths = compB/\n")
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

// TestMergeForeignFailsUnresolvedComponentCounted: регрессия ревью круга 1.
// Раскладки нет вовсе (как сегодня у самого devkit), а красный компонент
// назван так же, как каталог, который дифф задачи трогает. По старой (снятой)
// эвристике сегмента пути это читалось бы своей краснотой и держало бы
// foreign-fails в ложном нуле. Без заведённой границы компонент неопознан, и
// его краснота идёт в счёт: ложный ноль опаснее ложной единицы.
func TestMergeForeignFailsUnresolvedComponentCounted(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	if err := os.MkdirAll(filepath.Join(root, ".devkit"), 0o755); err != nil {
		t.Fatal(err)
	}
	gitT(t, root, "checkout", "-qb", "xr-001-fix")
	write(t, root, "compA/thing.txt", "own\n")
	write(t, root, "fix_test.go", "package main\n")
	gitT(t, root, "add", ".")
	gitT(t, root, "commit", "-qm", "fix: XR-001 правка")
	test := `printf 'compA   0.1s FAIL\n'; exit 1`
	if _, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: test}); err == nil {
		t.Fatal("красный компонент должен держать слияние")
	}
	n, err := foreignFails(root, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("неопознанный компонент без раскладки не должен молча прощаться: %d", n)
	}
}

// TestMergeForeignFailsOwnRedFromRootWithoutLayoutNotCounted: регрессия
// ревью круга 2. Раскладки deploy.<имя>.paths нет вовсе (живой пример - сам
// devkit), но команда test называет корень красного компонента прямо в
// строке итога (тем же приёмом, каким его теперь печатает parallel.py).
// Красный компонент честно свой (корень внутри диффа задачи), и такое
// слияние в foreign-fails попадать не должно: без разбора по Root каждое
// красное слияние проекта без раскладки садилось бы в счёт как чужое,
// включая своё.
func TestMergeForeignFailsOwnRedFromRootWithoutLayoutNotCounted(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	if err := os.MkdirAll(filepath.Join(root, ".devkit"), 0o755); err != nil {
		t.Fatal(err)
	}
	gitT(t, root, "checkout", "-qb", "xr-001-fix")
	write(t, root, "compA/thing.txt", "own\n")
	write(t, root, "fix_test.go", "package main\n")
	gitT(t, root, "add", ".")
	gitT(t, root, "commit", "-qm", "fix: XR-001 правка")
	test := `printf 'compA (compA) 0.1s FAIL\n'; exit 1`
	if _, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: test}); err == nil {
		t.Fatal("красный компонент должен держать слияние")
	}
	n, err := foreignFails(root, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("своя краснота по корню из строки итога не должна идти в счёт даже без раскладки: %d", n)
	}
}

// TestMergeForeignFailsOwnRedFromWholeRepoRootNotCounted: регрессия ревью
// круга 3, запись 3. Живой пример - компонент doctor (tools/devkitctl/
// parallel.py, cwd="."), корень которого это весь репозиторий. Красный
// такой компонент честно свой при любом диффе задачи, и в foreign-fails
// попадать не должен.
func TestMergeForeignFailsOwnRedFromWholeRepoRootNotCounted(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	if err := os.MkdirAll(filepath.Join(root, ".devkit"), 0o755); err != nil {
		t.Fatal(err)
	}
	gitT(t, root, "checkout", "-qb", "xr-001-fix")
	write(t, root, "compA/thing.txt", "own\n")
	write(t, root, "fix_test.go", "package main\n")
	gitT(t, root, "add", ".")
	gitT(t, root, "commit", "-qm", "fix: XR-001 правка")
	test := `printf 'doctor (.) 0.1s FAIL\n'; exit 1`
	if _, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: test}); err == nil {
		t.Fatal("красный компонент должен держать слияние")
	}
	n, err := foreignFails(root, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("компонент с корнем-точкой честно свой на любом диффе, в счёт не идёт: %d", n)
	}
}

// TestMergeForeignFailsOwnRedFromPartialLayoutFallsBackToRootNotCounted:
// регрессия ревью круга 3, запись 4. deploy.compA заведён без единой
// deploy.compA.paths строки (недоделанная раскладка), и own обязан
// решаться через Root, а не застревать в false навсегда.
func TestMergeForeignFailsOwnRedFromPartialLayoutFallsBackToRootNotCounted(t *testing.T) {
	root, _ := setup(t, rowInProg, "")
	if err := os.MkdirAll(filepath.Join(root, ".devkit"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeDeployCfg(t, root, "deploy.compA = true\n")
	gitT(t, root, "checkout", "-qb", "xr-001-fix")
	write(t, root, "compA/thing.txt", "own\n")
	write(t, root, "fix_test.go", "package main\n")
	gitT(t, root, "add", ".")
	gitT(t, root, "commit", "-qm", "fix: XR-001 правка")
	test := `printf 'compA (compA) 0.1s FAIL\n'; exit 1`
	if _, err := cmdMerge(root, MergeParams{ID: "XR-001", Test: test}); err == nil {
		t.Fatal("красный компонент должен держать слияние")
	}
	n, err := foreignFails(root, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("недоделанная раскладка должна уступать Root, а не топить own в false: %d", n)
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
