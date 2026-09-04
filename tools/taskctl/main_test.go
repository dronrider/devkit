package main

import (
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestGlobalDirParse(t *testing.T) {
	cases := []struct {
		in   []string
		dir  string
		rest []string
	}{
		{[]string{"-C", "/x", "lint"}, "/x", []string{"lint"}},
		{[]string{"--C", "/x", "close", "XR-1"}, "/x", []string{"close", "XR-1"}},
		{[]string{"-C=/x", "sort"}, "/x", []string{"sort"}},
		{[]string{"--C=/x", "id"}, "/x", []string{"id"}},
		{[]string{"lint"}, "", []string{"lint"}},
		{[]string{"close", "-C", "/x", "XR-1"}, "", []string{"close", "-C", "/x", "XR-1"}},
	}
	for _, c := range cases {
		dir, rest, err := globalDir(c.in)
		if err != nil {
			t.Fatalf("%v: %v", c.in, err)
		}
		if dir != c.dir || strings.Join(rest, " ") != strings.Join(c.rest, " ") {
			t.Errorf("%v: получил dir=%q rest=%v", c.in, dir, rest)
		}
	}
	if _, _, err := globalDir([]string{"-C"}); err == nil {
		t.Error("-C без значения должен падать с ошибкой")
	}
}

// TestRunLog: запуск оставляет строку в журнале .devkit/log (успех и ошибка с
// кодом выхода), а без директории .devkit журнал не заводится.
func TestRunLog(t *testing.T) {
	root := setup(t)
	if err := os.Mkdir(filepath.Join(root, ".devkit"), 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("go", "run", ".", "-C", root, "lint").CombinedOutput(); err != nil {
		t.Fatalf("lint: %v\n%s", err, out)
	}
	exec.Command("go", "run", ".", "-C", root, "show", "XR-404").Run()
	data, err := os.ReadFile(filepath.Join(root, ".devkit", "log"))
	if err != nil {
		t.Fatalf("журнал не записан: %v", err)
	}
	if !strings.Contains(string(data), "\ttaskctl\tlint\t0\n") ||
		!strings.Contains(string(data), "\ttaskctl\tshow\t1\n") {
		t.Fatalf("строки журнала: %q", data)
	}

	root2 := setup(t)
	if out, err := exec.Command("go", "run", ".", "-C", root2, "lint").CombinedOutput(); err != nil {
		t.Fatalf("lint без .devkit: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(root2, ".devkit", "log")); err == nil {
		t.Fatal("журнал не должен заводиться без .devkit")
	}
}

// TestHelpAfterSubcommand: -h/--help работает у любой подкоманды, включая
// команды с позиционными аргументами, где флаг раньше принимался за ID.
func TestHelpAfterSubcommand(t *testing.T) {
	root := setup(t)
	for _, args := range [][]string{
		{"show", "--help"},
		{"move", "XR-005", "-h"},
		{"review", "add", "--help"},
		{"add", "--help"},
	} {
		out, err := exec.Command("go", append([]string{"run", ".", "-C", root}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("%v: справка по подкоманде упала: %v\n%s", args, err, out)
		}
		if !strings.Contains(string(out), "taskctl: механика канбан-доски") {
			t.Fatalf("%v: вместо справки: %s", args, out)
		}
	}
}

// TestParseArgsMixesFlagsAndPositionals переехал в internal/frame: разбор
// аргументов стал общим каркасом (LLD DK-237, волна из DK-236), и его регрессия
// держится там, где живёт код.

// TestFlagBeforePositional: флаг перед позиционным аргументом уносил сам
// аргумент, потому что fs.Parse останавливается на первом не-флаге, а хвост
// fs.Args() команды не смотрели вовсе (DK-099). Проверяются обе формы из
// разбора и рабочий порядок «флаг после позиционного», чтобы починка его не
// увела.
func TestFlagBeforePositional(t *testing.T) {
	root := setup(t)
	gitSetup(t, root)

	out, err := runCLI(t, "draft", "-C", root, "--prio", "mid", "текст черновика после флага")
	if err != nil {
		t.Fatalf("draft -C <dir> \"текст\": %v\n%s", err, out)
	}
	data, rerr := os.ReadFile(filepath.Join(root, "docs", "tasks", "drafts", "XR-008.md"))
	if rerr != nil {
		t.Fatalf("черновик не заведён: %v\n%s", rerr, out)
	}
	if !strings.Contains(string(data), "текст черновика после флага") {
		t.Fatalf("текст черновика потерян:\n%s", data)
	}

	out, err = runCLI(t, "-C", root, "review", "add", "XR-005",
		"-m", "docs(tasks): XR-005 замечание", "настоящий текст замечания")
	if err != nil {
		t.Fatalf("review add <ID> -m \"...\" \"текст\": %v\n%s", err, out)
	}
	shown, err := cmdReviewShow(root, "XR-005")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(shown, "1. [открыто] настоящий текст замечания") {
		t.Fatalf("замечанием записано не то:\n%s", shown)
	}
	if subj := gitOut(t, root, "log", "-1", "--pretty=%s"); subj != "docs(tasks): XR-005 замечание" {
		t.Fatalf("сообщение коммита: %q", subj)
	}

	out, err = runCLI(t, "-C", root, "review", "add", "XR-002",
		"замечание с флагом в конце", "-m", "docs(tasks): XR-002 замечание")
	if err != nil {
		t.Fatalf("рабочий порядок аргументов сломан: %v\n%s", err, out)
	}
	shown, err = cmdReviewShow(root, "XR-002")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(shown, "1. [открыто] замечание с флагом в конце") {
		t.Fatalf("замечание при флаге в конце:\n%s", shown)
	}
}

// TestExtraPositionalRefused: лишний позиционный аргумент это чаще всего
// потерянные кавычки, и молча выбросить его значит потерять данные тем же
// способом, каким их терял флаг перед текстом.
func TestExtraPositionalRefused(t *testing.T) {
	root := setup(t)
	out, err := runCLI(t, "-C", root, "move", "XR-004", "in-progress", "лишнее")
	if err == nil {
		t.Fatalf("лишний аргумент принят молча:\n%s", out)
	}
	if !strings.Contains(out, "лишний аргумент \"лишнее\"") {
		t.Fatalf("вместо отказа: %s", out)
	}
}

// TestDraftRefusesSubcommandWord: неизвестное первое слово падало в ветку
// записи и само становилось текстом черновика, с кодом возврата 0 и обычным
// сообщением на экране; настоящий текст при этом пропадал (DK-099).
func TestDraftRefusesSubcommandWord(t *testing.T) {
	root := setup(t)
	for _, args := range [][]string{
		{"draft", "show", "XR-001"},
		{"draft", "add", "текст, который выбрасывался"},
	} {
		out, err := runCLI(t, append([]string{"-C", root}, args...)...)
		if err == nil {
			t.Fatalf("%v: черновик записан молча:\n%s", args, out)
		}
		if !strings.Contains(out, "list, defer, prio, attach, drop") ||
			!strings.Contains(out, "taskctl show <ID>") {
			t.Fatalf("%v: отказ без подсказки:\n%s", args, out)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "docs", "tasks", "drafts")); !os.IsNotExist(err) {
		t.Fatalf("отказ всё равно завёл черновик: %v", err)
	}
}

func runCLI(t *testing.T, args ...string) (string, error) {
	t.Helper()
	out, err := exec.Command("go", append([]string{"run", "."}, args...)...).CombinedOutput()
	return string(out), err
}

func TestGlobalDirBeforeCommand(t *testing.T) {
	root := setup(t)
	out, err := exec.Command("go", "run", ".", "-C", root, "lint").CombinedOutput()
	if err != nil {
		t.Fatalf("-C перед командой отбит: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "доска и архив в порядке") {
		t.Fatalf("неожиданный вывод: %s", out)
	}
}

// TestUsageTextDraftForm: шапка draft называет порог первой строки и страницу
// формы. Порог виден пользователю только оттуда и из отказа команды, а
// молчаливая подсказка снова разводит черновики простынями.
func TestUsageTextDraftForm(t *testing.T) {
	for _, want := range []string{"72 символа", formDoc} {
		if !strings.Contains(usageText, want) {
			t.Errorf("в шапке draft нет %q", want)
		}
	}
}

// TestUsageTextAcceptBarrier: регрессионный тест на то, что шапка содержит
// флаги --accept и --barrier с перечислением ключей барьера.
func TestUsageTextAcceptBarrier(t *testing.T) {
	cases := []struct {
		name     string
		subcmd   string
		contains []string
	}{
		{
			name:   "add",
			subcmd: "add",
			contains: []string{
				"--accept agent|mixed|user",
				"--barrier глаза|доступ|необратимость|секрет|согласие|событие",
			},
		},
		{
			name:   "set",
			subcmd: "set",
			contains: []string{
				"--accept agent|mixed|user",
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			for _, expected := range c.contains {
				if !strings.Contains(usageText, expected) {
					t.Errorf("%s: usageText не содержит %q", c.subcmd, expected)
				}
			}
		})
	}
}

// usageEntry вырезает из шапки блок одной подкоманды: от строки, начатой
// её именем с двойным отступом, до следующей строки с таким же отступом и
// именем другой команды. Продолжения блока начинаются глубже.
func usageEntry(t *testing.T, name string) string {
	t.Helper()
	lines := strings.Split(usageText, "\n")
	start := -1
	for i, l := range lines {
		if strings.HasPrefix(l, "  "+name+" ") {
			start = i
			break
		}
	}
	if start < 0 {
		t.Fatalf("в usageText нет строки подкоманды %s", name)
	}
	end := start + 1
	for end < len(lines) && (strings.HasPrefix(lines[end], "   ") || lines[end] == "") {
		end++
	}
	return strings.Join(lines[start:end], "\n")
}

// usageFlagToken ищет упоминания флагов в блоке шапки (--имя), чтобы поймать
// не только пропущенный флаг, но и обратное: справка называет флаг, которого
// у команды больше нет (DK-715, --wait ушёл из askFlags, справка отстала).
var usageFlagToken = regexp.MustCompile(`--([a-zA-Z][a-zA-Z0-9-]*)`)

// TestUsageMatchesFlagSet: каждый флаг, объявленный у подкоманды, назван в её
// блоке шапки. Общие -C, -m и --push описаны абзацем ниже, в блок команды они
// не входят. У ask и draft ask сверка ещё и обратная: блок шапки не должен
// называть флаг, которого нет в askFlags, иначе устаревшая справка (как
// [--wait N] после ухода флага) не поймается.
func TestUsageMatchesFlagSet(t *testing.T) {
	common := map[string]bool{"C": true, "m": true, "push": true}
	sets := map[string]struct {
		declare func(*flag.FlagSet)
		strict  bool
	}{
		"add":       {declare: func(fs *flag.FlagSet) { addFlags(fs, &AddParams{}) }},
		"set":       {declare: func(fs *flag.FlagSet) { setFlags(fs, &SetParams{}) }},
		"ask":       {declare: func(fs *flag.FlagSet) { askFlags(fs, &AskParams{}) }, strict: true},
		"draft ask": {declare: func(fs *flag.FlagSet) { askFlags(fs, &AskParams{}) }, strict: true},
	}
	for name, set := range sets {
		t.Run(name, func(t *testing.T) {
			fs := flag.NewFlagSet(name, flag.ContinueOnError)
			set.declare(fs)
			entry := usageEntry(t, name)
			declared := map[string]bool{}
			fs.VisitAll(func(f *flag.Flag) {
				declared[f.Name] = true
				if common[f.Name] {
					return
				}
				if !strings.Contains(entry, "--"+f.Name) {
					t.Errorf("%s: флаг --%s объявлен, но в блоке шапки его нет:\n%s", name, f.Name, entry)
				}
			})
			if !set.strict {
				return
			}
			for _, m := range usageFlagToken.FindAllStringSubmatch(entry, -1) {
				if common[m[1]] || declared[m[1]] {
					continue
				}
				t.Errorf("%s: блок шапки называет --%s, а такого флага у команды нет:\n%s", name, m[1], entry)
			}
		})
	}
}
