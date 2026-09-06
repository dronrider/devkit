package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const usageText = `obeycheck: правда ли правило соблюдается без своего текста в контексте

Сценарий это промпт плюс проверка командой с кодом возврата. Каждый сценарий
гоняется на двух раскладках правил по k повторов, и таблица говорит, где
послушание просело. Раскладка это директория с файлами правил, которую стенд
раскладывает во временный проект; живая машина и живая доска не участвуют, у
каждого прогона свой HOME и свой git-репозиторий (tools/obeycheck/README.md).

  obeycheck [флаги] <кандидат> <база>

Первая раскладка это кандидат, вторая база. Что за база, говорит --base:
«старый» это прежняя редакция того же текста (вопрос «не стало ли хуже»),
«пусто» это раскладка без этого текста вовсе (вопрос «есть ли польза»).

  --scenarios dir  директория сценариев (по умолчанию tools/obeycheck/scenarios из
                   чекаута devkit)
  --only a,b       гонять только эти сценарии, ID это имя файла без .md
  --for f1,f2      гонять сценарии, чей предмет покрывает эти файлы; путь от
                   корня devkit, сочетается с --only
  --base б         старый (по умолчанию) или пусто: что за вторая раскладка
  --task ID        записать след прогона в «Проверку» файла задачи
                   docs/tasks/<ID>.md; повторов при этом не меньше трёх
  -k N             повторов на раскладку, по умолчанию 5
  --tier ярус      mini, base, pro или max: в модель ярус разворачивает
                   agentctl harness маппингом активного харнеса
  --model имя      модель прямо, вместо разворота яруса
  --end конец      сессия (по умолчанию) или субагент: во втором случае промпт
                   отдаётся не сессии, а исполнителю
  --agent-def имя  определение исполнителя для субагентского конца
  --agent-cmd ...  команда прогона, промпт уходит ей на stdin, {model} в ней
                   заменяется на модель; по умолчанию headless-прогон claude
  --timeout d      потолок на один прогон и на одну команду сценария
  --home-seed dir  готовый дом, который кладётся во временный HOME до
                   раскладки; без флага временный дом получает ссылку на
                   связку ключей дома пользователя и авторизуется сам
  --no-preflight   не гонять пробную сессию перед полным проходом
  --work dir       где держать прогоны, по умолчанию временная директория
  --keep           не убирать директории прогонов: там остаются проект,
                   временный HOME и транскрипт каждого прогона
  --list           напечатать разобранные сценарии и выйти
  --devkit dir     чекаут devkit, если детект промахнулся

Вердикт строки это одно из пяти слов: польза, не хуже, зелёный на обеих,
красный на обеих, просадка. Разницу зелёных клеток считает значимой точный тест
Фишера при p не больше 0.05. С базой «старый» зачтены «польза», «не хуже» и
«зелёный на обеих», с базой «пусто» только «польза».

Код возврата: 0 все строки зачтены, 1 хотя бы одна не зачтена, 2 ошибка
прогона или аргументов.
`

const defaultAgentCmd = "claude -p --output-format stream-json --verbose --dangerously-skip-permissions --model {model}"

func fail(err error) {
	fmt.Fprintln(os.Stderr, "ошибка:", err)
	logRun(".", 2)
	os.Exit(2)
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func main() {
	if versionRequested() {
		return
	}
	fs := flag.NewFlagSet("obeycheck", flag.ExitOnError)
	fs.Usage = func() { fmt.Fprint(os.Stderr, usageText) }
	scenarios := fs.String("scenarios", "", "директория сценариев")
	only := fs.String("only", "", "гонять только эти сценарии, через запятую")
	forFiles := fs.String("for", "", "гонять сценарии, чей предмет покрывает эти файлы")
	base := fs.String("base", baseOld, "что за вторая раскладка: старый или пусто")
	task := fs.String("task", "", "записать след прогона в файл задачи")
	repeats := fs.Int("k", 5, "повторов на раскладку")
	tier := fs.String("tier", "mini", "ярус модели")
	model := fs.String("model", "", "модель прямо, вместо разворота яруса")
	end := fs.String("end", endSession, "конец прогона: сессия или субагент")
	agentDef := fs.String("agent-def", "exec-medium", "определение исполнителя для субагентского конца")
	agentCmd := fs.String("agent-cmd", defaultAgentCmd, "команда прогона")
	timeout := fs.Duration("timeout", 15*time.Minute, "потолок на один прогон")
	homeSeed := fs.String("home-seed", "", "готовый дом, который кладётся во временный HOME до раскладки")
	noPreflight := fs.Bool("no-preflight", false, "не гонять пробную сессию перед полным проходом")
	work := fs.String("work", "", "где держать прогоны")
	keep := fs.Bool("keep", false, "не убирать директории прогонов")
	list := fs.Bool("list", false, "напечатать сценарии и выйти")
	devkit := fs.String("devkit", "", "чекаут devkit")
	fs.Parse(os.Args[1:])

	root := *devkit
	if root == "" {
		d, err := findDevkit(".")
		if err != nil {
			fail(err)
		}
		root = d
	}
	dir := *scenarios
	if dir == "" {
		dir = filepath.Join(root, "tools", "obeycheck", "scenarios")
	}
	var only2 []string
	for _, f := range splitList(*forFiles) {
		only2 = append(only2, relToDevkit(root, f))
	}
	scen, err := loadScenarios(dir, root, splitList(*only), only2)
	if err != nil {
		fail(err)
	}
	if *list {
		for _, s := range scen {
			fmt.Printf("%-20s %-52s %s (конец: %s)\n", s.ID, s.Title, s.Subject(), s.End)
		}
		logRun(".", 0)
		return
	}
	if err := checkBase(*base); err != nil {
		fail(err)
	}
	// Файл задачи и число повторов проверяются до прогона: узнать о разведке
	// после двухсот сессий значит потерять их зря.
	taskPath := ""
	if *task != "" {
		if *repeats < minTaskRepeats {
			fail(fmt.Errorf("--task %s при -k %d это разведка, а не замер: следу в файле задачи нужно "+
				"хотя бы %d повтора на раскладку", *task, *repeats, minTaskRepeats))
		}
		if taskPath, err = taskFile(".", *task); err != nil {
			fail(err)
		}
	}

	argv := strings.Fields(*agentCmd)
	if len(argv) == 0 {
		fail(fmt.Errorf("пустая команда прогона"))
	}
	if strings.Contains(*agentCmd, "{model}") {
		m := *model
		if m == "" {
			m, err = resolveModel(*tier)
			if err != nil {
				fail(err)
			}
		}
		for i := range argv {
			argv[i] = strings.ReplaceAll(argv[i], "{model}", m)
		}
	}

	res, err := Run(Params{
		Scenarios: scen,
		Layouts:   fs.Args(),
		Repeats:   *repeats,
		Base:      *base,
		Agent:     argv,
		End:       *end,
		AgentDef:  *agentDef,
		Devkit:    root,
		HomeSeed:  *homeSeed,
		Work:      *work,
		Keep:      *keep,
		Preflight: !*noPreflight,
		Timeout:   *timeout,
		Progress:  os.Stderr,
	})
	if err != nil {
		fail(err)
	}
	fmt.Println(res.Report)
	if taskPath != "" {
		tier := *tier
		if *model != "" {
			tier = *model
		}
		n := note{
			Task:      *task,
			Devkit:    root,
			Tier:      tier,
			Base:      *base,
			Repeats:   *repeats,
			Command:   strings.Join(os.Args, " "),
			Table:     res.Report,
			Scenarios: liveScenarios(res.Rows),
			Failed:    res.Failed,
			Warnings:  missingInLayout(fs.Args()[0], root, runSubjects(scen)),
			Now:       time.Now(),
		}
		for _, w := range n.Warnings {
			fmt.Fprintln(os.Stderr, "предупреждение:", w)
		}
		if err := n.write(taskPath); err != nil {
			fail(err)
		}
		fmt.Printf("след прогона записан в %s\n", taskPath)
	}
	if res.Failed {
		logRun(".", 1)
		os.Exit(1)
	}
	logRun(".", 0)
}
