package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
)

const usageText = `agentctl: выбор исполнителя под задачу по метаданным доски (RULES.board.md)

  pick <ID> [--record]    вердикт, каким исполнителем закрывать задачу: три
       [--role exec|      машинные строки model (модель активного харнеса),
        review]           effort: low|medium|high|xhigh|max и tier:
                          mini|base|pro|max, четвёртая строка задачи и причина;
                          --record дописывает строку исполнения в раздел «Ход
                          работы» файла задачи, --role review отдаёт вердикт
                          для агента-ревьювера (ярус ниже исполнителя, пол base)
  quota [refresh]         снимок остатка лимитов: без аргумента печатает
       [--if-stale]       разобранный ~/.devkit/quota.local (бакеты, возраст,
                          pace, статус), refresh снимает панель /usage через
                          одноразовую tmux-сессию и переписывает файл;
                          --if-stale снимает только протухший снимок, на этом
                          режиме стоит хук старта сессии (hooks/README.md)
  harness [--harness      окно в резолв харнеса: активный инструмент и чем он
           <имя>]         определён, включённый список после слияния слоёв,
                          маппинг ярусов, режим делегирования, снимок квоты;
                          --harness перебивает детект, как и DEVKIT_HARNESS

Калибр исполнителя считается ярусами лестницы mini -> base -> pro -> max, а в
конкретную модель ярус разворачивается последним шагом, маппингом активного
харнеса. Харнес не определён или не настроен, значит в строке model прочерк, а
причина зовёт файл ~/.devkit/harness.local; ярус и effort при этом полноценны.

Вердикт pick корректируется остатком лимитов по снимку: дефицит бакета сдвигает
ярус на ступень вниз, профицит свежего снимка на ступень вверх. Нет снимка или
он протух, значит сдвига нет, и pick говорит об этом строкой причины: отсутствие
данных не ошибка, но и не молчание.

Свою модель сессия сменить не может, этот рычаг у пользователя, поэтому
вердикт применяется делегированием: сессия-диспетчер спавнит субагента с
моделью из вердикта, а effort приходит из определения агента (devkit/agents,
exec-* для исполнения и review-* для ревью). Правила маппинга (тип, цена,
неопределённость из разбивки ранга) описаны в agentctl/README.md.
Общий флаг -C <dir>: откуда искать корень (директорию с docs/TASKS.md),
ставится и перед командой, и после неё.
`

// globalDir вырезает -C до выбора команды, как в taskctl: справка обещает
// общий флаг, значит он обязан работать и перед подкомандой.
func globalDir(args []string) (string, []string, error) {
	dir := ""
	for len(args) > 0 {
		a := args[0]
		switch {
		case a == "-C" || a == "--C":
			if len(args) < 2 {
				return "", nil, fmt.Errorf("флагу -C нужно значение")
			}
			dir = args[1]
			args = args[2:]
		case strings.HasPrefix(a, "-C="), strings.HasPrefix(a, "--C="):
			dir = a[strings.Index(a, "=")+1:]
			args = args[1:]
		default:
			return dir, args, nil
		}
	}
	return dir, args, nil
}

func helpRequested(args []string) bool {
	for _, a := range args {
		if a == "-h" || a == "-help" || a == "--help" {
			return true
		}
	}
	return false
}

func fail(err error) {
	logRun(1)
	fmt.Fprintln(os.Stderr, "ошибка:", err)
	os.Exit(1)
}

func main() {
	gdir, args, gerr := globalDir(os.Args[1:])
	if gerr != nil {
		fail(gerr)
	}
	if gdir == "" {
		gdir = "."
	}
	if len(args) < 1 {
		fmt.Fprint(os.Stderr, usageText)
		os.Exit(2)
	}
	if helpRequested(args) {
		fmt.Print(usageText)
		return
	}
	logStart, logCmd = gdir, args[0]
	var msg string
	var err error
	switch args[0] {
	case "pick":
		if len(args) < 2 || strings.HasPrefix(args[1], "-") {
			fail(fmt.Errorf("жду: pick <ID>"))
		}
		fs := flag.NewFlagSet("pick", flag.ExitOnError)
		dir := fs.String("C", gdir, "стартовая директория")
		record := fs.Bool("record", false, "дописать строку исполнения в файл задачи")
		role := fs.String("role", roleExec, "роль субагента: exec или review")
		fs.Parse(args[2:])
		root, rerr := findRoot(*dir)
		if rerr != nil {
			fail(rerr)
		}
		msg, err = cmdPick(root, args[1], *record, *role)
	case "quota":
		// Корень с доской команде не нужен: снимок лежит на уровне машины,
		// а не проекта.
		if len(args) > 1 && args[1] == "refresh" {
			fs := flag.NewFlagSet("quota refresh", flag.ExitOnError)
			ifStale := fs.Bool("if-stale", false, "снимать панель, только если снимок протух")
			fs.Parse(args[2:])
			msg, err = cmdQuotaRefresh(quotaPath(), timeNow(), *ifStale)
			break
		}
		if len(args) > 1 {
			fail(fmt.Errorf("жду: quota [refresh]"))
		}
		msg, err = cmdQuota(quotaPath(), timeNow())
	case "harness":
		fs := flag.NewFlagSet("harness", flag.ExitOnError)
		dir := fs.String("C", gdir, "стартовая директория")
		name := fs.String("harness", "", "имя харнеса, перебивает детект")
		fs.Parse(args[1:])
		msg, err = cmdHarness(*dir, *name)
	case "help":
		fmt.Print(usageText)
		return
	default:
		fmt.Fprintf(os.Stderr, "неизвестная команда %q\n\n%s", args[0], usageText)
		logRun(2)
		os.Exit(2)
	}
	if err != nil {
		fail(err)
	}
	logRun(0)
	fmt.Println(msg)
}
