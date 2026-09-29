package plans

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const kitRel = "../../kit/plans"

// TestBuiltinTask: встроенный task повторяет живой материал LLD DK-972. Этапов
// тринадцать, порядок тот же, ворота те же, и у вычитки ворот нет: сегодня её
// не спрашивает никто.
func TestBuiltinTask(t *testing.T) {
	tpl := loadBuiltin(t, "task")
	want := []string{"plan", "start", "work", "tests", "docs", "proofread", "stand",
		"scenario", "rehearsal", "review", "ship", "verify", "accept"}
	got := make([]string, 0, len(tpl.Stages))
	for _, s := range tpl.Stages {
		got = append(got, s.Key)
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("этапы task %v, жду %v", got, want)
	}
	gates := map[string]string{
		"tests":     "merge",
		"stand":     "merge",
		"scenario":  "check,merge",
		"rehearsal": "check",
		"review":    "merge",
		"ship":      "check",
		"verify":    "close",
		"proofread": "",
		"work":      "",
	}
	for key, want := range gates {
		s, ok := tpl.Stage(key)
		if !ok {
			t.Fatalf("этапа [%s] в task нет", key)
		}
		if strings.Join(s.Gate, ",") != want {
			t.Fatalf("ворота этапа [%s] это %v, жду %q", key, s.Gate, want)
		}
	}
	if s, _ := tpl.Stage("work"); !s.Slot {
		t.Fatal("слот своих пунктов агента стоит у разработки, а в шаблоне его нет")
	}
	if s, _ := tpl.Stage("accept"); strings.Join(s.Accept, ",") != "user,mixed" {
		t.Fatalf("приёмка человека стоит у видов user и mixed, в шаблоне %v", s.Accept)
	}
	if !contains(tpl.Types, "task") {
		t.Fatalf("тип task шаблону не достаётся: types = %v", tpl.Types)
	}
}

// TestBuiltinBugAddsRegcheck: bug это task плюс краснота со следом на слиянии.
func TestBuiltinBugAddsRegcheck(t *testing.T) {
	bug := loadBuiltin(t, "bug")
	task := loadBuiltin(t, "task")
	if len(bug.Stages) != len(task.Stages)+1 {
		t.Fatalf("этапов у bug %d, у task %d: жду ровно один лишний", len(bug.Stages), len(task.Stages))
	}
	s, ok := bug.Stage("regcheck")
	if !ok {
		t.Fatal("этапа [regcheck] у bug нет")
	}
	if s.Trace != "regcheck" || strings.Join(s.Gate, ",") != "merge" {
		t.Fatalf("regcheck описан как след %q, ворота %v", s.Trace, s.Gate)
	}
	if !contains(bug.Types, "bug") {
		t.Fatalf("тип bug шаблону не достаётся: types = %v", bug.Types)
	}
}

// TestBuiltinLLD: у проектирования нет тестов, обкатки и красноты, зато есть
// карта решений на слове агента и приёмка человека при любом виде строки.
func TestBuiltinLLD(t *testing.T) {
	tpl := loadBuiltin(t, "lld")
	for _, gone := range []string{"tests", "rehearsal", "regcheck"} {
		if _, ok := tpl.Stage(gone); ok {
			t.Fatalf("этап [%s] у lld остался", gone)
		}
	}
	for _, name := range []string{"tests", "rehearsal"} {
		why, ok := tpl.DroppedWhy(name)
		if !ok || why == "" {
			t.Fatalf("этап %s снят без причины", name)
		}
	}
	s, ok := tpl.Stage("decisions")
	if !ok {
		t.Fatal("этапа карты решений у lld нет")
	}
	if s.Trace != "слово" {
		t.Fatalf("карта решений стоит на слове агента, след в шаблоне %q", s.Trace)
	}
	a, _ := tpl.Stage("accept")
	if len(a.Accept) != 0 || a.By != "человек" {
		t.Fatalf("приёмку lld спрашивают у человека при любом виде строки, в шаблоне by = %q, accept = %v", a.By, a.Accept)
	}
}

// TestBuiltinPOC: режим POC едет доставкой до пользователя, а тесты, ревью,
// вычитка, стенд и обкатка сняты записями с причиной, а не молчанием.
func TestBuiltinPOC(t *testing.T) {
	tpl := loadBuiltin(t, "poc")
	for _, name := range []string{"tests", "docs", "review", "proofread", "stand", "rehearsal"} {
		if _, ok := tpl.Stage(name); ok {
			t.Fatalf("этап [%s] у poc остался", name)
		}
		why, ok := tpl.DroppedWhy(name)
		if !ok || why == "" {
			t.Fatalf("этап %s снят без причины", name)
		}
	}
	s, ok := tpl.Stage("show")
	if !ok || s.By != "человек" {
		t.Fatalf("показ пользователю ведёт человек, в шаблоне [show] by = %q", s.By)
	}
	if len(tpl.Types) != 0 {
		t.Fatalf("poc не достаётся по типу строки, в шаблоне types = %v", tpl.Types)
	}
}

// TestParseRejects: границы формата. Каждый вход это отдельная поломка, которую
// иначе увидели бы уже на собранном плане, где этап пропал молча.
func TestParseRejects(t *testing.T) {
	cases := []struct {
		name string
		text string
		want string
	}{
		{"чужое имя", "name = \"other\"\ntitle = \"x\"\n[work]\ntitle = \"a\"\nby = \"сам\"\ntrace = \"слово\"\n", "имя шаблона это имя файла"},
		{"без title", "[work]\ntitle = \"a\"\nby = \"сам\"\ntrace = \"слово\"\n", "нет title"},
		{"без этапов", "title = \"x\"\n", "этапов нет"},
		{"незнакомый by", "title = \"x\"\n[work]\ntitle = \"a\"\nby = \"робот\"\ntrace = \"слово\"\n", "by = \"робот\""},
		{"этап без следа", "title = \"x\"\n[work]\ntitle = \"a\"\nby = \"сам\"\n", "нет trace"},
		{"незнакомые ворота", "title = \"x\"\n[work]\ntitle = \"a\"\nby = \"сам\"\ntrace = \"tests\"\ngate = [\"slияние\"]\n", "ворота известны эти"},
		{"незнакомая приёмка", "title = \"x\"\n[work]\ntitle = \"a\"\nby = \"сам\"\ntrace = \"слово\"\naccept = [\"робот\"]\n", "виды приёмки эти"},
		{"снятие без причины", "title = \"x\"\ndropped = [\"tests\"]\n[work]\ntitle = \"a\"\nby = \"сам\"\ntrace = \"слово\"\n", "без причины"},
		{"два слота", "title = \"x\"\n[work]\ntitle = \"a\"\nby = \"сам\"\ntrace = \"слово\"\nslot = true\n[more]\ntitle = \"b\"\nby = \"сам\"\ntrace = \"слово\"\nslot = true\n", "ложатся в один"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Parse("plan.toml", c.text)
			if err == nil {
				t.Fatalf("разбор прошёл, а жду отказ про %q", c.want)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Fatalf("отказ %q, а жду про %q", err, c.want)
			}
		})
	}
}

// TestUnknownKeyWarns: опечатка в ключе не валит разбор, но и не молчит: такой
// ключ не читает никто, и настройка выглядит поставленной.
func TestUnknownKeyWarns(t *testing.T) {
	tpl, err := Parse("plan.toml", "title = \"x\"\n[work]\ntitle = \"a\"\nby = \"сам\"\ntrace = \"слово\"\ngates = [\"merge\"]\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(tpl.Warns) != 1 || !strings.Contains(tpl.Warns[0], "gates") {
		t.Fatalf("предупреждения %v, жду одно про ключ gates", tpl.Warns)
	}
}

// TestLoadReplacesWholeFile: проектный файл замещает встроенный целиком, а свой
// шаблон проекта добавляется к набору. Пословного слияния секций нет.
func TestLoadReplacesWholeFile(t *testing.T) {
	kit := t.TempDir()
	proj := t.TempDir()
	write(t, filepath.Join(kit, "task.toml"), "title = \"встроенный\"\n[work]\ntitle = \"разработка\"\nby = \"сам\"\ntrace = \"слово\"\n[tests]\ntitle = \"тесты\"\nby = \"сам\"\ntrace = \"tests\"\n")
	write(t, filepath.Join(proj, "task.toml"), "title = \"проектный\"\ndropped = [\"tests: у проекта нет кода\"]\n[work]\ntitle = \"разработка\"\nby = \"сам\"\ntrace = \"слово\"\n")
	write(t, filepath.Join(proj, "security.toml"), "title = \"разбор безопасности\"\n[audit]\ntitle = \"аудит\"\nby = \"субагент\"\ntrace = \"слово\"\n")
	set, err := Load(kit, proj)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(set.Names, ",") != "security,task" {
		t.Fatalf("набор это %v, жду security и task", set.Names)
	}
	tpl, _ := set.Get("task")
	if tpl.Title != "проектный" || !tpl.Project {
		t.Fatalf("встроенный task не замещён: title %q, слой проекта %v", tpl.Title, tpl.Project)
	}
	if _, ok := tpl.Stage("tests"); ok {
		t.Fatal("секция [tests] встроенного файла осталась: слияние слоёв идёт по файлу, а не по секциям")
	}
}

// TestLoadFallsBackToKitOnBrokenProjectFile: файл проектного слоя, не читаемый
// как шаблон (пустой на полпути записи, например shell truncate-ит цель
// `--dump ... > .devkit/plans/x.toml` раньше запуска agentctl), не должен
// ронять весь набор. Встроенный шаблон того же имени остаётся в наборе, а про
// битый файл идёт предупреждение.
func TestLoadFallsBackToKitOnBrokenProjectFile(t *testing.T) {
	kit := t.TempDir()
	proj := t.TempDir()
	write(t, filepath.Join(kit, "task.toml"), "title = \"встроенный\"\n[work]\ntitle = \"разработка\"\nby = \"сам\"\ntrace = \"слово\"\n")
	write(t, filepath.Join(proj, "task.toml"), "")
	set, err := Load(kit, proj)
	if err != nil {
		t.Fatalf("Load упал на битом файле проектного слоя: %v", err)
	}
	tpl, ok := set.Get("task")
	if !ok || tpl.Title != "встроенный" || tpl.Project {
		t.Fatalf("встроенный task не отдан запасным вариантом: %v, title %q, слой проекта %v", ok, tpl.Title, tpl.Project)
	}
	if len(set.Warns) != 1 || !strings.Contains(set.Warns[0], "task.toml") {
		t.Fatalf("предупреждения %v, жду одно про task.toml", set.Warns)
	}
}

// TestLoadKeepsRejectingTypoInNonEmptyProjectFile: непустой файл проектного
// слоя с опечаткой в известном ключе (by, gate, accept) остаётся отказом, а не
// падает на тот же запасной путь, что пустой файл (ревью DK-1142). Иначе
// развилка задачи «строгость валидатора шаблона» перестаёт работать именно
// там, где живёт настоящая опечатка организации.
func TestLoadKeepsRejectingTypoInNonEmptyProjectFile(t *testing.T) {
	kit := t.TempDir()
	proj := t.TempDir()
	write(t, filepath.Join(kit, "task.toml"), "title = \"встроенный\"\n[work]\ntitle = \"разработка\"\nby = \"сам\"\ntrace = \"слово\"\n")
	write(t, filepath.Join(proj, "task.toml"), "title = \"проектный\"\n[work]\ntitle = \"разработка\"\nby = \"исполнитель\"\ntrace = \"слово\"\n")
	if _, err := Load(kit, proj); err == nil {
		t.Fatal("Load молча подменил встроенным шаблон с опечаткой в by, а должен был отказать")
	} else if !strings.Contains(err.Error(), "by = ") {
		t.Fatalf("ошибка не называет причину (опечатка by): %v", err)
	}
}

// TestByTypeFromLayers: тип строки доски достаётся шаблону, который его назвал,
// и проектная копия перебивает встроенный тем же типом.
func TestByTypeFromLayers(t *testing.T) {
	kit := t.TempDir()
	write(t, filepath.Join(kit, "task.toml"), "title = \"встроенный\"\ntypes = [\"task\"]\n[work]\ntitle = \"разработка\"\nby = \"сам\"\ntrace = \"слово\"\n")
	set, err := Load(kit, "")
	if err != nil {
		t.Fatal(err)
	}
	if tpl, ok := set.ByType("task"); !ok || tpl.Name != "task" {
		t.Fatalf("по типу task шаблон не нашёлся: %v", ok)
	}
	if _, ok := set.ByType("bug"); ok {
		t.Fatal("по типу bug нашёлся шаблон, которого в наборе нет")
	}
}

// TestFindingsStaleCopy: копия проекта, отставшая от встроенного шаблона, это
// находка доктора; снятие с причиной проходит молча.
func TestFindingsStaleCopy(t *testing.T) {
	kit := t.TempDir()
	proj := t.TempDir()
	write(t, filepath.Join(kit, "task.toml"), "title = \"встроенный\"\n[work]\ntitle = \"разработка\"\nby = \"сам\"\ntrace = \"слово\"\n[tests]\ntitle = \"тесты\"\nby = \"сам\"\ntrace = \"tests\"\n")
	write(t, filepath.Join(proj, "task.toml"), "title = \"копия\"\n[work]\ntitle = \"разработка\"\nby = \"сам\"\ntrace = \"слово\"\n")
	got := Findings(kit, proj)
	if len(got) != 1 || !strings.Contains(got[0], "[tests]") {
		t.Fatalf("находки %v, жду одну про пропавший этап tests", got)
	}
	write(t, filepath.Join(proj, "task.toml"), "title = \"копия\"\ndropped = [\"tests: у проекта нет кода, документация правится ревью\"]\n[work]\ntitle = \"разработка\"\nby = \"сам\"\ntrace = \"слово\"\n")
	if got := Findings(kit, proj); len(got) != 0 {
		t.Fatalf("снятие с причиной дало находки %v", got)
	}
}

// TestFindingsBrokenDrop: снятие без причины ловится разбором, и доктор видит
// его находкой, а не падением.
func TestFindingsBrokenDrop(t *testing.T) {
	kit := t.TempDir()
	proj := t.TempDir()
	write(t, filepath.Join(kit, "task.toml"), "title = \"встроенный\"\n[work]\ntitle = \"разработка\"\nby = \"сам\"\ntrace = \"слово\"\n")
	write(t, filepath.Join(proj, "task.toml"), "title = \"копия\"\ndropped = [\"work\"]\n[tests]\ntitle = \"тесты\"\nby = \"сам\"\ntrace = \"tests\"\n")
	got := Findings(kit, proj)
	if len(got) != 1 || !strings.Contains(got[0], "без причины") {
		t.Fatalf("находки %v, жду одну про снятие без причины", got)
	}
}

// TestFindingsWithoutProjectLayer: проект без своего слоя живёт на встроенном
// наборе, и это штатное состояние, а не находка.
func TestFindingsWithoutProjectLayer(t *testing.T) {
	if got := Findings(kitDir(t), filepath.Join(t.TempDir(), "нет")); len(got) != 0 {
		t.Fatalf("находки на проекте без своего слоя: %v", got)
	}
}

// TestKitDirLadder: лестница поиска встроенного слоя. Переменная старше дерева,
// симлинк проекта старше соседнего клона.
func TestKitDirLadder(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, "projects", "app")
	mk(t, filepath.Join(root, ".devkit", "devkit", "kit", DirName))
	mk(t, filepath.Join(home, "projects", "devkit", "kit", DirName))
	if got := KitDir(root, home); got != filepath.Join(root, ".devkit", "devkit", "kit", DirName) {
		t.Fatalf("симлинк проекта не выбран, взят %s", got)
	}
	env := t.TempDir()
	mk(t, filepath.Join(env, "kit", DirName))
	t.Setenv("DEVKIT_HOME", env)
	if got := KitDir(root, home); got != filepath.Join(env, "kit", DirName) {
		t.Fatalf("DEVKIT_HOME не старше дерева, взят %s", got)
	}
	t.Setenv("DEVKIT_HOME", filepath.Join(env, "нет"))
	if got := KitDir("", ""); got != "" {
		t.Fatalf("без дерева и дома нашёлся каталог %s", got)
	}
}

func loadBuiltin(t *testing.T, name string) *Template {
	t.Helper()
	set, err := Load(kitDir(t), "")
	if err != nil {
		t.Fatal(err)
	}
	tpl, ok := set.Get(name)
	if !ok {
		t.Fatalf("встроенного шаблона %s нет, набор это %v", name, set.Names)
	}
	if len(set.Warns) != 0 {
		t.Fatalf("встроенный набор читается с предупреждениями: %v", set.Warns)
	}
	return tpl
}

func kitDir(t *testing.T) string {
	t.Helper()
	abs, err := filepath.Abs(kitRel)
	if err != nil {
		t.Fatal(err)
	}
	return abs
}

func write(t *testing.T, path, text string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mk(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
}

func contains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}
