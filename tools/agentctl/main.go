package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/dronrider/devkit/internal/frame"
	"github.com/dronrider/devkit/internal/stage"
)

const usageText = `agentctl: выбор исполнителя под задачу по метаданным доски (RULES.board.md)

  pick <ID> [--record]    вердикт, каким исполнителем закрывать задачу: три
       [--role exec|      машинные строки model (модель активного харнеса),
        review]           effort: low|medium|high|xhigh|max и tier:
       [--goal <файл>]    mini|base|pro|max, четвёртая строка задачи и причина;
                          --record отмечает этап работы в записи ~/.devkit/runs
                          (разработка либо ревью), откуда её читает дашборд, а
                          taskctl на смене статуса уносит пакетом в раздел «Ход
                          работы» файла задачи, --role review отдаёт вердикт
                          для агента-ревьювера (ярус ниже исполнителя, пол base),
                          --goal режет вердикт потолком яруса из раздела
                          «Бюджет» файла цели
  run <ID> [--record]     делегирование задачи: печатается тот же вердикт, что
      [--role exec|       у pick, а дальше режим [delegate] харнеса назначения
       review]            ступени решает, кто исполняет. native это инструкция
      [--goal <файл>]     диспетчеру спавнить субагента (выход 0), cli это
      [--workdir <dir>]   подпроцесс в рабочей директории с кодом выхода
                          наружу, none и уехавшая ступень с native это отказ
                          «делегировать нечем» выходом 3. Подпроцессу ставятся
                          DEVKIT_HARNESS харнеса назначения и ограничитель
                          вложенности DEVKIT_RUN_DEPTH=1; --workdir это дерево
                          задачи (по умолчанию корень проекта)
  stage <ID> [<вид>]      этап работы над задачей: без вида печатает живой этап
        [--note <текст>]  и накопленный пакет, с видом отмечает начало нового.
        [--by <модель>]   Виды: разработка, ревью, проверка, снаружи, уточнение.
        [--turns N        Первые два ставит pick --record сам, ожидание снаружи
         --minutes M]     ставит taskctl на смене статуса, руками отмечают
                          уточнение и проверку. Проверка это прогон сценария не
                          автором правки, --by называет прогнавшую модель, и
                          taskctl close сверяет её с исполнителем разработки.
                          Ревью сверх кругов pick несёт активную работу:
                          --turns и --minutes (только вместе, с --by) кладут
                          ходы и минуты без ожидания, taskctl review stats
                          сводит их по уровням против бюджета review.conf
  spend --goal <файл>     гейт бюджета цели: первая строка машинная (gate: ok
       [--record]         либо gate: over), вторая называет потраченное по
                          каждому бакету против потолка из раздела «Бюджет»
                          файла цели. Расход считается суммой пошаговых дельт
                          между снимками квоты из раздела «Журнал» и текущим
                          снимком; --record дописывает текущий снимок в «Журнал»
  lap --goal <файл>       строка витка в «Журнал» файла цели: начало берётся из
      --note <текст>      последнего снимка квоты (его кладёт гейт первым шагом
      --marker <маркер>   витка), конец это момент вызова, --start ставит начало
      [--start <время>]   руками. Маркер стопа (done, over, wait-human, stuck)
                          добавляет к строке время цикла и число витков
  tally --goal <файл>     сводка итога: куда ушло время задач цели. Состав
                          берётся из раздела «Задачи цели», времена из «Хода
                          работы» файлов задач, разбивка идёт по видам этапов
  quota [refresh]         снимок остатка лимитов: без аргумента печатает
       [--harness <имя>]  разобранный ~/.devkit/quota/<харнес>.local активного
       [--if-stale]       харнеса (бакеты, возраст, pace,
                          статус), refresh снимает остаток тем способом, что
                          объявил профиль (панель /usage в одноразовой
                          tmux-сессии либо съёмщик из kit/harness/snap), и
                          переписывает файл; --if-stale снимает только протухший
                          снимок, на этом режиме стоит хук старта сессии
                          (hooks/README.md); --harness читает и снимает чужую
                          подписку, не поднимая её сессию; refresh --all
                          обходит все включённые харнесы с объявленной квотой,
                          на этом режиме стоит периодический съём (тик
                          сторожка цикла цели и демон дашборда)
  harness [--harness      окно в резолв харнеса: активный инструмент и чем он
           <имя>]         определён, включённый список после слияния слоёв,
          [--json]        маппинг ярусов, режим делегирования, снимок квоты;
                          --harness перебивает детект, как и DEVKIT_HARNESS.
                          --json печатает машинную раскладку подписок для чужой
                          программы: имя, признаки включён и по умолчанию, чем
                          поднимается клиент, имена пар окружения; значений
                          окружения в ответе нет никогда
  exec --harness <имя>    запуск команды на выбранной подписке: кладёт пары
       -- <команда>       окружения харнеса из машинного слоя и DEVKIT_HARNESS,
                          дальше поднимает команду и отдаёт наружу её код
                          выхода. Вердикта, ярусов и ролей не считает, поэтому
                          годится и там, где вердикта нет (сессия конвейера,
                          груминг черновика). Ограничитель вложенности не
                          ставится: поднятая сессия делегировать вправе
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
exec-* для исполнения и review-* для ревью). Само делегирование гоняет команда
run: инструмент со своим спавном получает инструкцию, чужой поднимается
подпроцессом. Правила маппинга (тип, цена,
неопределённость из разбивки ранга) описаны в tools/agentctl/README.md.
Общий флаг -C <dir>: откуда искать корень (директорию с docs/TASKS.md),
ставится и перед командой, и после неё.
Флаги подкоманд стоят где угодно относительно позиционных: и до ID, и после,
и между ними; лишний позиционный не выбрасывается молча, а отбивается ошибкой.
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

// needArgs проверяет число позиционных через общий frame.NeedArgs, а выход
// через os.Exit держит локальный fail. Разбор позиционных сам по себе
// (frame.ParseArgs) снимает позиционные из хвоста после fs.Parse, так что флаг
// стоит где угодно, а лишний позиционный отбивается здесь, а не молчит
// (DK-236).
func needArgs(pos []string, min, max int, usage string) {
	if err := frame.NeedArgs(pos, min, max, usage); err != nil {
		fail(err)
	}
}

func main() {
	if versionRequested() {
		return
	}
	// Порог свежести снимка настраивается машинным файлом, и подтягивается он
	// до разбора команды: порог держат и съём, и корректор вердикта, а кривое
	// значение валит команду с причиной, не съезжая молча на умолчание.
	if err := loadSnapshotMaxAge(); err != nil {
		fail(err)
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
	touchWork(args)
	var msg string
	var err error
	switch args[0] {
	case "pick":
		fs := flag.NewFlagSet("pick", flag.ExitOnError)
		dir := fs.String("C", gdir, "стартовая директория")
		record := fs.Bool("record", false, "дописать строку исполнения в файл задачи")
		role := fs.String("role", roleExec, "роль субагента: exec или review")
		goal := fs.String("goal", "", "файл цели, из него берётся потолок яруса")
		pos := frame.ParseArgs(fs, args[1:])
		needArgs(pos, 1, 1, "pick <ID> [--record] [--role exec|review] [--goal <файл>]")
		root, rerr := findRoot(*dir)
		if rerr != nil {
			fail(rerr)
		}
		msg, err = cmdPick(root, pos[0], *record, *role, *goal)
	case "run":
		fs := flag.NewFlagSet("run", flag.ExitOnError)
		dir := fs.String("C", gdir, "стартовая директория")
		record := fs.Bool("record", false, "дописать строку исполнения в файл задачи")
		role := fs.String("role", roleExec, "роль исполнителя: exec или review")
		goal := fs.String("goal", "", "файл цели, из него берётся потолок яруса")
		workdir := fs.String("workdir", "", "рабочая директория задачи, по умолчанию корень проекта")
		pos := frame.ParseArgs(fs, args[1:])
		needArgs(pos, 1, 1, "run <ID> [--record] [--role exec|review] [--goal <файл>] [--workdir <dir>]")
		root, rerr := findRoot(*dir)
		if rerr != nil {
			fail(rerr)
		}
		// Вывод подпроцесса идёт наружу по ходу дела, а не собирается в строку:
		// делегированная сессия живёт минутами, и молчащий терминал до её конца
		// неотличим от повисшего.
		code, rerr := cmdRun(root, pos[0], *record, *role, *goal, *workdir, os.Stdout, os.Stderr)
		if rerr != nil {
			fail(rerr)
		}
		logRun(code)
		os.Exit(code)
	case "stage":
		fs := flag.NewFlagSet("stage", flag.ExitOnError)
		dir := fs.String("C", gdir, "стартовая директория")
		note := fs.String("note", "", "текст записи, он уедет в «Ход работы» файла задачи")
		by := fs.String("by", "", "кто прогнал сценарий либо провёл ревью: имя модели")
		turns := fs.Int("turns", 0, "ходы ревью без ожидания, только с --minutes и видом ревью")
		minutes := fs.Int("minutes", 0, "минуты ревью без ожидания, только с --turns и видом ревью")
		pos := frame.ParseArgs(fs, args[1:])
		needArgs(pos, 1, 2, "stage <ID> [<вид>] [--note <текст>] [--by <модель>] [--turns N --minutes M]")
		root, rerr := findRoot(*dir)
		if rerr != nil {
			fail(rerr)
		}
		kind := ""
		if len(pos) == 2 {
			kind = pos[1]
		}
		msg, err = cmdStage(root, pos[0], kind, *note, *by, *turns, *minutes, timeNow())
	case "spend":
		fs := flag.NewFlagSet("spend", flag.ExitOnError)
		dir := fs.String("C", gdir, "стартовая директория")
		goal := fs.String("goal", "", "файл цели с разделами «Бюджет» и «Журнал»")
		record := fs.Bool("record", false, "дописать текущий снимок квоты в «Журнал» файла цели")
		pos := frame.ParseArgs(fs, args[1:])
		needArgs(pos, 0, 0, "spend --goal <файл цели> [--record]")
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
	case "lap":
		fs := flag.NewFlagSet("lap", flag.ExitOnError)
		dir := fs.String("C", gdir, "стартовая директория")
		goal := fs.String("goal", "", "файл цели с разделом «Журнал»")
		note := fs.String("note", "", "что виток сделал")
		marker := fs.String("marker", "", "маркер выхода витка")
		from := fs.String("start", "", "начало витка, 2006-01-02 15:04; по умолчанию момент последнего снимка квоты")
		pos := frame.ParseArgs(fs, args[1:])
		needArgs(pos, 0, 0, "lap --goal <файл цели> --note <текст> --marker <маркер> [--start <время>]")
		if *goal == "" || *note == "" || *marker == "" {
			fail(fmt.Errorf("жду: lap --goal <файл цели> --note <текст> --marker <маркер>"))
		}
		var start time.Time
		if *from != "" {
			start, err = time.ParseInLocation(stage.LineStamp, *from, time.Local)
			if err != nil {
				fail(fmt.Errorf("начало витка %q не разобрано, жду «2006-01-02 15:04»", *from))
			}
		}
		root, rerr := findRoot(*dir)
		if rerr != nil {
			fail(rerr)
		}
		msg, err = cmdLap(root, *goal, *note, *marker, start, timeNow())
	case "tally":
		fs := flag.NewFlagSet("tally", flag.ExitOnError)
		dir := fs.String("C", gdir, "стартовая директория")
		goal := fs.String("goal", "", "файл цели с разделом «Задачи цели»")
		pos := frame.ParseArgs(fs, args[1:])
		needArgs(pos, 0, 0, "tally --goal <файл цели>")
		if *goal == "" {
			fail(fmt.Errorf("жду: tally --goal <файл цели>"))
		}
		root, rerr := findRoot(*dir)
		if rerr != nil {
			fail(rerr)
		}
		msg, err = cmdTally(root, *goal)
	case "quota":
		// Корень с доской команде не нужен: снимок лежит на уровне машины, а не
		// проекта. Профиль харнеса при этом нужен: он говорит, чем снимать,
		// какие бакеты бывают и в какой файл директории они ложатся. Флаг
		// --harness читает и снимает чужую подписку из любой сессии: вторая
		// подписка активной бывает только внутри собственного подпроцесса, и
		// без флага её остаток снаружи недосягаем.
		fs := flag.NewFlagSet("quota", flag.ExitOnError)
		name := fs.String("harness", "", "имя харнеса, чей остаток читает команда, перебивает детект")
		ifStale := fs.Bool("if-stale", false, "снимать, только если снимок протух")
		all := fs.Bool("all", false, "снять остаток всех включённых харнесов с объявленной квотой")
		pos := frame.ParseArgs(fs, args[1:])
		if len(pos) > 1 || (len(pos) == 1 && pos[0] != "refresh") {
			fail(fmt.Errorf("жду: quota [refresh] [--harness <имя>] [--if-stale] [--all]"))
		}
		if *all {
			if *name != "" {
				fail(fmt.Errorf("флаги --all и --harness вместе не работают: --all и так обходит все включённые харнесы"))
			}
			if len(pos) == 0 {
				fail(fmt.Errorf("флаг --all идёт вместе с refresh: quota refresh --all"))
			}
			specs, serr := quotaSpecsAll(gdir)
			if serr != nil {
				fail(serr)
			}
			msg, err = cmdQuotaRefreshAll(specs, timeNow(), *ifStale)
			break
		}
		q, qerr := quotaSpecFor(gdir, *name)
		if qerr != nil {
			fail(qerr)
		}
		if len(pos) == 0 {
			if *ifStale {
				fail(fmt.Errorf("флаг --if-stale идёт вместе с refresh: quota refresh --if-stale"))
			}
			msg, err = cmdQuota(q, timeNow())
			break
		}
		msg, _, err = cmdQuotaRefresh(q, timeNow(), *ifStale)
	case "harness":
		fs := flag.NewFlagSet("harness", flag.ExitOnError)
		dir := fs.String("C", gdir, "стартовая директория")
		name := fs.String("harness", "", "имя харнеса, перебивает детект")
		asJSON := fs.Bool("json", false, "машинная раскладка подписок JSON-ом")
		needArgs(frame.ParseArgs(fs, args[1:]), 0, 0, "harness [--harness <имя>] [--json]")
		if *asJSON {
			// Пара флагов бессмысленна вместе: машинный вид не резолвит активный
			// харнес вовсе, и перебивать в нём нечего.
			if *name != "" {
				fail(fmt.Errorf("флаги --json и --harness вместе не работают: --json отдаёт раскладку машины целиком, а --harness перебивает детект активного"))
			}
			msg, err = cmdHarnessJSON(*dir)
			break
		}
		msg, err = cmdHarness(*dir, *name)
	case "exec":
		fs := flag.NewFlagSet("exec", flag.ExitOnError)
		dir := fs.String("C", gdir, "стартовая директория")
		name := fs.String("harness", "", "имя харнеса, чьё окружение получает команда")
		pos := frame.ParseArgs(fs, args[1:])
		needArgs(pos, 1, -1, "exec --harness <имя> -- <команда>")
		// Вывод команды идёт наружу по ходу дела, а не собирается в строку: под
		// exec живут сессии агентов, и молчащий терминал до их конца неотличим от
		// повисшего.
		code, rerr := cmdExec(*dir, *name, pos, os.Stdout, os.Stderr)
		if rerr != nil {
			fail(rerr)
		}
		logRun(code)
		os.Exit(code)
	case "budget":
		needArgs(args[1:], 0, 0, "budget")
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
