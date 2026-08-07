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
       [--goal <файл>]    mini|base|pro|max, четвёртая строка задачи и причина;
                          --record дописывает строку исполнения в раздел «Ход
                          работы» файла задачи, --role review отдаёт вердикт
                          для агента-ревьювера (ярус ниже исполнителя, пол base),
                          --goal режет вердикт потолком яруса из раздела
                          «Бюджет» файла цели
  spend --goal <файл>     гейт бюджета цели: первая строка машинная (gate: ok
       [--record]         либо gate: over), вторая называет потраченное по
                          каждому бакету против потолка из раздела «Бюджет»
                          файла цели. Расход считается суммой пошаговых дельт
                          между снимками квоты из раздела «Журнал» и текущим
                          снимком; --record дописывает текущий снимок в «Журнал»
  quota [refresh]         снимок остатка лимитов активного харнеса: без
       [--if-stale]       аргумента печатает разобранный
                          ~/.devkit/quota/<харнес>.local (бакеты, возраст, pace,
                          статус), refresh снимает остаток тем способом, что
                          объявил профиль (панель /usage в одноразовой
                          tmux-сессии либо съёмщик из kit/harness/snap), и
                          переписывает файл; --if-stale снимает только протухший
                          снимок, на этом режиме стоит хук старта сессии
                          (hooks/README.md)
  harness [--harness      окно в резолв харнеса: активный инструмент и чем он
           <имя>]         определён, включённый список после слияния слоёв,
                          маппинг ярусов, режим делегирования, снимок квоты;
                          --harness перебивает детект, как и DEVKIT_HARNESS
  budget                  потолок пачки для taskctl batch --limit: первая
                          строка машинная (batch: N), вторая называет бакет,
                          темп и причину потолка. Потолок не выводится своим
                          рассуждением про проценты, а спрашивается у
                          корректора на модели opus (том же, что тратит
                          вердикт pick); нет снимка или он протух, значит
                          потолок 3 по умолчанию, а строка зовёт agentctl
                          quota refresh

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
моделью из вердикта, а effort приходит из определения агента (devkit/kit/agents,
exec-* для исполнения и review-* для ревью). Правила маппинга (тип, цена,
неопределённость из разбивки ранга) описаны в tools/agentctl/README.md.
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
	if versionRequested() {
		return
	}
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
		goal := fs.String("goal", "", "файл цели, из него берётся потолок яруса")
		fs.Parse(args[2:])
		root, rerr := findRoot(*dir)
		if rerr != nil {
			fail(rerr)
		}
		msg, err = cmdPick(root, args[1], *record, *role, *goal)
	case "spend":
		fs := flag.NewFlagSet("spend", flag.ExitOnError)
		dir := fs.String("C", gdir, "стартовая директория")
		goal := fs.String("goal", "", "файл цели с разделами «Бюджет» и «Журнал»")
		record := fs.Bool("record", false, "дописать текущий снимок квоты в «Журнал» файла цели")
		fs.Parse(args[1:])
		if *goal == "" {
			fail(fmt.Errorf("жду: spend --goal <файл цели>"))
		}
		root, rerr := findRoot(*dir)
		if rerr != nil {
			fail(rerr)
		}
		// Гейт стоит в начале каждого витка, и он же отмечает цель в реестре
		// сторожка: отметка идёт до счёта, потому что вставший цикл надо
		// заметить и тогда, когда сам гейт кончился ошибкой.
		watchRegister(root, *goal, timeNow())
		msg, err = cmdSpend(root, *goal, *record, timeNow())
	case "quota":
		// Корень с доской команде не нужен: снимок лежит на уровне машины, а не
		// проекта. Профиль харнеса при этом нужен: он говорит, чем снимать,
		// какие бакеты бывают и в какой файл директории они ложатся.
		q, qerr := quotaSpecFor(gdir)
		if qerr != nil {
			fail(qerr)
		}
		if len(args) > 1 && args[1] == "refresh" {
			fs := flag.NewFlagSet("quota refresh", flag.ExitOnError)
			ifStale := fs.Bool("if-stale", false, "снимать панель, только если снимок протух")
			fs.Parse(args[2:])
			msg, err = cmdQuotaRefresh(q, timeNow(), *ifStale)
			break
		}
		if len(args) > 1 {
			fail(fmt.Errorf("жду: quota [refresh]"))
		}
		msg, err = cmdQuota(q, timeNow())
	case "harness":
		fs := flag.NewFlagSet("harness", flag.ExitOnError)
		dir := fs.String("C", gdir, "стартовая директория")
		name := fs.String("harness", "", "имя харнеса, перебивает детект")
		fs.Parse(args[1:])
		msg, err = cmdHarness(*dir, *name)
	case "budget":
		if len(args) > 1 {
			fail(fmt.Errorf("жду: budget"))
		}
		msg, err = cmdBudget(gdir, timeNow())
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
