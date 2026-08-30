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

  obeycheck [флаги] <раскладка1> <раскладка2>

  --scenarios dir  директория сценариев (по умолчанию tools/obeycheck/scenarios из
                   чекаута devkit)
  --only a,b       гонять только эти сценарии, ID это имя файла без .md
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

Код возврата: 0 регрессий нет, 1 регрессия хотя бы в одном сценарии, 2 ошибка
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
	scen, err := loadScenarios(dir, splitList(*only))
	if err != nil {
		fail(err)
	}
	if *list {
		for _, s := range scen {
			fmt.Printf("%-20s %s (конец: %s)\n", s.ID, s.Title, s.End)
		}
		logRun(".", 0)
		return
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

	report, regression, err := Run(Params{
		Scenarios: scen,
		Layouts:   fs.Args(),
		Repeats:   *repeats,
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
	fmt.Println(report)
	if regression {
		logRun(".", 1)
		os.Exit(1)
	}
	logRun(".", 0)
}
