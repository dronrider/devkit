package main

import (
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/dronrider/devkit/internal/frame"
)

const usageText = `taskctl: механика канбан-доски docs/TASKS.md

Смотреть доску:
  list [backlog|in-progress|check|blocked]    доска по секциям без прозы; без
                                              аргумента Backlog обрезан до 10 строк.
                                              Под строкой Check пометка «держит
                                              очередь или нет, сценарий агентский
                                              или пользовательский», под любой
                                              строкой возраст («строка не
                                              двигалась N дней»), когда его видно
                                              посчитать; нет данных, нет и строки
  show <ID>                                   строка задачи, секция, те же пометки,
                                              файл задачи (закрытые ищутся в архиве)
  id                                          следующий свободный ID
  draft list [--json]                         накопитель черновиков: ID, первая
                                              строка, возраст, метка уровня
                                              разбора, пометка «отложен»; список
                                              идёт от высокого уровня к низкому,
                                              немаркированные и отложенные
                                              стоят в конце
  batch [--limit N]                           кандидаты в поезд выката и причина
                                              отказа по каждой остальной строке
  kinds                                       сводка по видам приёмки: счёт,
                                              ошибки назначения, пересмотры

Менять доску:
  draft ["текст"]                             записать сырую идею мимо доски:
                                              файл docs/tasks/drafts/<ID>.md,
                                              ID берётся сам, без текста читается
                                              stdin; оформляет её потом
                                              add --id <ID> (черновик переезжает
                                              в docs/tasks/<ID>.md сам)
  draft defer <ID> "причина"                  отложить разобранный черновик:
                                              раздел «Грумминг» в его файле
  draft defer <ID> --clear                    снять пометку об отложенном
  draft prio <ID> high|mid|low                пометить черновик уровнем разбора:
                                              строка в шапке файла, сортировка
                                              накопителя от высокого к низкому
  draft prio <ID> --clear                     снять метку уровня разбора
  draft attach <ID> <TASK-ID>                 приписать черновик к стоящей
                                              строке: текст разделом в файл
                                              задачи, черновик удаляется
  draft drop <ID> --reason "..."              удалить протухший черновик,
                                              причина уезжает в коммит
  add --title "..." --type bug|task|LLD --rank "а+б+в+г+д"
      [--cost S|M|L|XL] [--link "..."] [--status ...] [--id XR-NNN] [--reason "..."]
                                              завести задачу (по умолчанию в Backlog;
                                              без --link и файла в ячейке будет «-»)
  move <ID> <статус> [--reason "..."]         перевести между статусами
  fail <ID> --reason "..."                    провал проверки: прод сломан,
                                              задача обратно в In progress,
                                              очередь выката встаёт
  fail <ID> --clear                           прод починен, признак снят (сами
                                              его гасят shipctl merge, ship, revert)
  set <ID> [--title "..."] [--type ...] [--rank "..."] [--cost ...] [--link "..."]
                                              поправить ячейки строки
  file <ID>                                   создать docs/tasks/<ID>.md и ссылку в строке
  close <ID> [--commit sha1,sha2] [--date ГГГГ-ММ-ДД] [--link "..."]
                                              в архив + файл задачи в tasks/archive/<год>/,
                                              со всех строк снимается «[после <ID>]»
  dep add <ID> <DEP-ID>                       ID делается после DEP-ID
  dep rm <ID> <DEP-ID>                        снять зависимость
  dep list [ID]                               кто после кого; без ID вся доска
  sort                                        пересортировать Backlog по R
  lint                                        проверить инварианты доски и архива
  init --prefix XR [--name "..."]             скелет доски в корне репозитория

Ревью задачи (раздел «Ревью» в docs/tasks/<ID>.md):
  review add <ID> "суть замечания"            дописать замечание, файл задачи
                                              создаётся сам
  review resolve <ID> <N> fixed|rejected [--reason "..."]
                                              зафиксировать исход замечания N
  review show <ID>                            замечания с номерами и исходами
  review stats                                свод по живым задачам и архиву

У list, show, dep list и draft list есть флаг --json: машинный вывод для дашборда и
прочей автоматики, печатный вывод не меняется; list --json отдаёт Backlog
целиком, без обрезки.
У изменяющих команд флаги -m "docs(tasks): ..." и --push: закоммитить ровно
тронутые файлы доски (и запушить), чужой индекс не задевается.
Флаг стоит где угодно: и до позиционных аргументов, и после них, и между ними
(«review add XR-1 -m "коммит" "текст"» это то же самое, что «review add XR-1
"текст" -m "коммит"»); текст, начинающийся с дефиса, передаётся после «--».
Лишний позиционный аргумент не выбрасывается молча, а отбивается ошибкой.
Статусы принимаются в любом регистре, «In progress» = in-progress.
Сумма R и бакет P считаются из разбивки --rank сами, руками их не передать.
Колонка «Цена» это грубая оценка затрат агента на исполнение (шкала в
RANKING.md), в ранг не входит; «-» значит «не оценено».
Общий флаг -C <dir>: откуда искать корень репозитория (по умолчанию текущая
директория), ставится и перед командой, и после неё.
`

// commitFlags вешает на изменяющую команду флаги -m/--push.
func commitFlags(fs *flag.FlagSet, c *CommitOpts) {
	fs.StringVar(&c.Msg, "m", "", "закоммитить тронутые файлы с этим сообщением")
	fs.BoolVar(&c.Push, "push", false, "после коммита сделать git push (только с -m)")
}

// Справка обещает общий флаг, значит -C обязан работать и перед подкомандой,
// а не только внутри её FlagSet. Вырезаем его до выбора команды.
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

// needArgs проверяет число позиционных аргументов: и нехватку, и лишнее.
// Сам разбор уехал в общий каркас (frame.ParseArgs), а fail у каждой утилиты
// свой и в общий модуль не въехал (LLD DK-237), поэтому тонкая обёртка вокруг
// frame.NeedArgs живёт тут: точки вызова остаются лаконичными, а выход через
// os.Exit держит локальный fail.
func needArgs(pos []string, min, max int, usage string) {
	if err := frame.NeedArgs(pos, min, max, usage); err != nil {
		fail(err)
	}
}

// helpRequested ищет -h/--help среди аргументов до разбора: подкоманды с
// позиционными аргументами до своего FlagSet не доходят, и «show --help»
// принимал флаг за ID с ответом «--help нет на доске».
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

func root(dir string) string {
	r, err := findRoot(dir)
	if err != nil {
		fail(err)
	}
	return r
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
	if args[0] == "draft" && len(args) > 1 && draftSubs[args[1]] {
		logCmd += " " + args[1]
	}
	if (args[0] == "review" || args[0] == "dep") && len(args) > 1 {
		logCmd += " " + args[1]
	}
	var msg string
	var err error
	switch args[0] {
	case "init":
		fs := flag.NewFlagSet("init", flag.ExitOnError)
		dir := fs.String("C", gdir, "стартовая директория")
		var p InitParams
		fs.StringVar(&p.Prefix, "prefix", "", "префикс ID задач, заглавными (XR)")
		fs.StringVar(&p.Name, "name", "", "название проекта в шапке, по умолчанию имя директории")
		needArgs(frame.ParseArgs(fs, args[1:]), 0, 0, "init --prefix XR [--name \"...\"]")
		msg, err = cmdInit(*dir, p)
	case "add":
		fs := flag.NewFlagSet("add", flag.ExitOnError)
		dir := fs.String("C", gdir, "стартовая директория")
		var p AddParams
		fs.StringVar(&p.ID, "id", "", "ID задачи, по умолчанию следующий свободный")
		fs.StringVar(&p.Title, "title", "", "заголовок строки")
		fs.StringVar(&p.Type, "type", "task", "тип: bug / task / LLD")
		fs.StringVar(&p.Rank, "rank", "", "разбивка ранга «а+б+в+г+д»")
		fs.StringVar(&p.Cost, "cost", "", "цена исполнения S / M / L / XL, по умолчанию «-»")
		fs.StringVar(&p.Link, "link", "", "ячейка ссылки, по умолчанию файл задачи")
		fs.StringVar(&p.Status, "status", "backlog", "секция доски")
		fs.StringVar(&p.Accept, "accept", "", "вид приёмки: agent / mixed / user (обязателен)")
		fs.StringVar(&p.Barrier, "barrier", "", "ключ барьера из шести, обязателен для mixed и user")
		commitFlags(fs, &p.Commit)
		needArgs(frame.ParseArgs(fs, args[1:]), 0, 0, "add --title \"...\" --type bug|task|LLD --rank \"а+б+в+г+д\" --accept agent|mixed|user")
		msg, err = cmdAdd(root(*dir), p)
	case "draft":
		// Подкоманда узнаётся точным совпадением первого позиционного
		// аргумента, тем же способом, каким узнавался list: иначе
		// «draft defer XR-008 причина» завёл бы черновик с текстом «defer»,
		// выбросив ID и причину.
		sub := ""
		if len(args) > 1 && draftSubs[args[1]] {
			sub = args[1]
		}
		switch sub {
		case "list":
			fs := flag.NewFlagSet("draft list", flag.ExitOnError)
			dir := fs.String("C", gdir, "стартовая директория")
			jsonOut := fs.Bool("json", false, "машинный вывод JSON")
			needArgs(frame.ParseArgs(fs, args[2:]), 0, 0, "draft list [--json]")
			if *jsonOut {
				msg, err = cmdDraftListJSON(root(*dir))
				break
			}
			msg, err = cmdDraftList(root(*dir))
		case "defer":
			fs := flag.NewFlagSet("draft defer", flag.ExitOnError)
			dir := fs.String("C", gdir, "стартовая директория")
			clear := fs.Bool("clear", false, "снять пометку об отложенном")
			var c CommitOpts
			commitFlags(fs, &c)
			pos := frame.ParseArgs(fs, args[2:])
			needArgs(pos, 1, 2, "draft defer <ID> \"причина\" либо draft defer <ID> --clear")
			reason := ""
			if len(pos) > 1 {
				reason = pos[1]
			}
			msg, err = cmdDraftDefer(root(*dir), pos[0], reason, *clear, c)
		case "prio":
			fs := flag.NewFlagSet("draft prio", flag.ExitOnError)
			dir := fs.String("C", gdir, "стартовая директория")
			clear := fs.Bool("clear", false, "снять метку уровня разбора")
			var c CommitOpts
			commitFlags(fs, &c)
			pos := frame.ParseArgs(fs, args[2:])
			needArgs(pos, 1, 2, "draft prio <ID> high|mid|low либо draft prio <ID> --clear")
			level := ""
			if len(pos) > 1 {
				level = pos[1]
			}
			msg, err = cmdDraftPrio(root(*dir), pos[0], level, *clear, c)
		case "attach":
			fs := flag.NewFlagSet("draft attach", flag.ExitOnError)
			dir := fs.String("C", gdir, "стартовая директория")
			var c CommitOpts
			commitFlags(fs, &c)
			pos := frame.ParseArgs(fs, args[2:])
			needArgs(pos, 2, 2, "draft attach <ID> <TASK-ID>")
			msg, err = cmdDraftAttach(root(*dir), pos[0], pos[1], c)
		case "drop":
			fs := flag.NewFlagSet("draft drop", flag.ExitOnError)
			dir := fs.String("C", gdir, "стартовая директория")
			reason := fs.String("reason", "", "чем черновик протух, одна строка")
			var c CommitOpts
			commitFlags(fs, &c)
			pos := frame.ParseArgs(fs, args[2:])
			needArgs(pos, 1, 1, "draft drop <ID> --reason \"...\"")
			msg, err = cmdDraftDrop(root(*dir), pos[0], *reason, c)
		default:
			fs := flag.NewFlagSet("draft", flag.ExitOnError)
			dir := fs.String("C", gdir, "стартовая директория")
			var c CommitOpts
			commitFlags(fs, &c)
			pos := frame.ParseArgs(fs, args[1:])
			// Страж стоит до счёта аргументов: у «draft add "текст"» лишний
			// аргумент есть, но сказать про него надо не «лишний», а что
			// подкоманды add у draft нет.
			if len(pos) > 0 {
				if gerr := draftTextGuard(pos[0]); gerr != nil {
					fail(gerr)
				}
			}
			needArgs(pos, 0, 1, "draft [\"текст\"]")
			text := ""
			if len(pos) == 1 {
				text = pos[0]
			}
			if text == "" {
				text, err = readStdin()
				if err != nil {
					fail(err)
				}
			}
			msg, err = cmdDraft(root(*dir), text, c)
		}
	case "move":
		fs := flag.NewFlagSet("move", flag.ExitOnError)
		dir := fs.String("C", gdir, "стартовая директория")
		reason := fs.String("reason", "", "причина блокировки (для blocked)")
		var c CommitOpts
		commitFlags(fs, &c)
		pos := frame.ParseArgs(fs, args[1:])
		needArgs(pos, 2, 2, "move <ID> <статус> [--reason ...]")
		msg, err = cmdMove(root(*dir), pos[0], pos[1], *reason, c)
	case "fail":
		fs := flag.NewFlagSet("fail", flag.ExitOnError)
		dir := fs.String("C", gdir, "стартовая директория")
		var p FailParams
		fs.StringVar(&p.Reason, "reason", "", "чем сломан прод, одна строка")
		fs.BoolVar(&p.Clear, "clear", false, "снять признак провала: прод починен")
		commitFlags(fs, &p.Commit)
		pos := frame.ParseArgs(fs, args[1:])
		needArgs(pos, 1, 1, "fail <ID> --reason \"...\" либо fail <ID> --clear")
		p.ID = pos[0]
		msg, err = cmdFail(root(*dir), p)
	case "set":
		fs := flag.NewFlagSet("set", flag.ExitOnError)
		dir := fs.String("C", gdir, "стартовая директория")
		var p SetParams
		fs.StringVar(&p.Title, "title", "", "новый заголовок строки")
		fs.StringVar(&p.Type, "type", "", "новый тип: bug / task / LLD")
		fs.StringVar(&p.Rank, "rank", "", "новая разбивка ранга «а+б+в+г+д»")
		fs.StringVar(&p.Cost, "cost", "", "новая цена исполнения S / M / L / XL («-» = не оценено)")
		fs.StringVar(&p.Link, "link", "", "новая ячейка ссылки")
		fs.StringVar(&p.Accept, "accept", "", "новый вид приёмки: agent / mixed / user")
		commitFlags(fs, &p.Commit)
		pos := frame.ParseArgs(fs, args[1:])
		needArgs(pos, 1, 1, "set <ID> [--title ...] [--type ...] [--rank ...] [--cost ...] [--link ...] [--accept ...]")
		p.ID = pos[0]
		msg, err = cmdSet(root(*dir), p)
	case "file":
		fs := flag.NewFlagSet("file", flag.ExitOnError)
		dir := fs.String("C", gdir, "стартовая директория")
		var c CommitOpts
		commitFlags(fs, &c)
		pos := frame.ParseArgs(fs, args[1:])
		needArgs(pos, 1, 1, "file <ID>")
		msg, err = cmdFile(root(*dir), pos[0], c)
	case "list":
		fs := flag.NewFlagSet("list", flag.ExitOnError)
		dir := fs.String("C", gdir, "стартовая директория")
		jsonOut := fs.Bool("json", false, "машинный вывод JSON, Backlog целиком")
		pos := frame.ParseArgs(fs, args[1:])
		needArgs(pos, 0, 1, "list [backlog|in-progress|check|blocked] [--json]")
		sect := ""
		if len(pos) == 1 {
			sect = pos[0]
		}
		if *jsonOut {
			msg, err = cmdListJSON(root(*dir), sect)
		} else {
			msg, err = cmdList(root(*dir), sect)
		}
	case "show":
		fs := flag.NewFlagSet("show", flag.ExitOnError)
		dir := fs.String("C", gdir, "стартовая директория")
		jsonOut := fs.Bool("json", false, "машинный вывод JSON")
		pos := frame.ParseArgs(fs, args[1:])
		needArgs(pos, 1, 1, "show <ID> [--json]")
		if *jsonOut {
			msg, err = cmdShowJSON(root(*dir), pos[0])
		} else {
			msg, err = cmdShow(root(*dir), pos[0])
		}
	case "review":
		if len(args) < 2 {
			fail(fmt.Errorf("жду: review add|resolve|show|stats ..."))
		}
		switch args[1] {
		case "add":
			fs := flag.NewFlagSet("review add", flag.ExitOnError)
			dir := fs.String("C", gdir, "стартовая директория")
			var c CommitOpts
			commitFlags(fs, &c)
			pos := frame.ParseArgs(fs, args[2:])
			needArgs(pos, 2, 2, "review add <ID> \"суть замечания\"")
			msg, err = cmdReviewAdd(root(*dir), pos[0], pos[1], c)
		case "resolve":
			fs := flag.NewFlagSet("review resolve", flag.ExitOnError)
			dir := fs.String("C", gdir, "стартовая директория")
			reason := fs.String("reason", "", "причина отклонения (для rejected)")
			var c CommitOpts
			commitFlags(fs, &c)
			pos := frame.ParseArgs(fs, args[2:])
			needArgs(pos, 3, 3, "review resolve <ID> <N> fixed|rejected [--reason \"...\"]")
			num, aerr := strconv.Atoi(pos[1])
			if aerr != nil {
				fail(fmt.Errorf("номер замечания %q не число", pos[1]))
			}
			msg, err = cmdReviewResolve(root(*dir), pos[0], num, pos[2], *reason, c)
		case "show":
			fs := flag.NewFlagSet("review show", flag.ExitOnError)
			dir := fs.String("C", gdir, "стартовая директория")
			pos := frame.ParseArgs(fs, args[2:])
			needArgs(pos, 1, 1, "review show <ID>")
			msg, err = cmdReviewShow(root(*dir), pos[0])
		case "stats":
			fs := flag.NewFlagSet("review stats", flag.ExitOnError)
			dir := fs.String("C", gdir, "стартовая директория")
			needArgs(frame.ParseArgs(fs, args[2:]), 0, 0, "review stats")
			msg, err = cmdReviewStats(root(*dir))
		default:
			fail(fmt.Errorf("неизвестная подкоманда review %q, жду add / resolve / show / stats", args[1]))
		}
	case "close":
		fs := flag.NewFlagSet("close", flag.ExitOnError)
		dir := fs.String("C", gdir, "стартовая директория")
		var p CloseParams
		fs.StringVar(&p.Commits, "commit", "", "хеши коммитов через запятую")
		fs.StringVar(&p.Date, "date", "", "дата закрытия, по умолчанию сегодня")
		fs.StringVar(&p.Link, "link", "", "ячейка ссылки в архиве, по умолчанию собирается сама")
		commitFlags(fs, &p.Commit)
		pos := frame.ParseArgs(fs, args[1:])
		needArgs(pos, 1, 1, "close <ID> [--commit ...] [--date ...]")
		p.ID = pos[0]
		msg, err = cmdClose(root(*dir), p)
	case "dep":
		if len(args) < 2 {
			fail(fmt.Errorf("жду: dep add|rm|list ..."))
		}
		switch args[1] {
		case "add", "rm":
			fs := flag.NewFlagSet("dep "+args[1], flag.ExitOnError)
			dir := fs.String("C", gdir, "стартовая директория")
			var c CommitOpts
			commitFlags(fs, &c)
			pos := frame.ParseArgs(fs, args[2:])
			needArgs(pos, 2, 2, fmt.Sprintf("dep %s <ID> <DEP-ID>", args[1]))
			p := DepParams{ID: pos[0], DepID: pos[1], Commit: c}
			if args[1] == "add" {
				msg, err = cmdDepAdd(root(*dir), p)
			} else {
				msg, err = cmdDepRm(root(*dir), p)
			}
		case "list":
			fs := flag.NewFlagSet("dep list", flag.ExitOnError)
			dir := fs.String("C", gdir, "стартовая директория")
			jsonOut := fs.Bool("json", false, "машинный вывод JSON")
			pos := frame.ParseArgs(fs, args[2:])
			needArgs(pos, 0, 1, "dep list [ID] [--json]")
			id := ""
			if len(pos) == 1 {
				id = pos[0]
			}
			if *jsonOut {
				msg, err = cmdDepListJSON(root(*dir), id)
			} else {
				msg, err = cmdDepList(root(*dir), id)
			}
		default:
			fail(fmt.Errorf("неизвестная подкоманда dep %q, жду add / rm / list", args[1]))
		}
	case "sort":
		fs := flag.NewFlagSet("sort", flag.ExitOnError)
		dir := fs.String("C", gdir, "стартовая директория")
		var c CommitOpts
		commitFlags(fs, &c)
		needArgs(frame.ParseArgs(fs, args[1:]), 0, 0, "sort")
		msg, err = cmdSort(root(*dir), c)
	case "lint":
		fs := flag.NewFlagSet("lint", flag.ExitOnError)
		dir := fs.String("C", gdir, "стартовая директория")
		needArgs(frame.ParseArgs(fs, args[1:]), 0, 0, "lint")
		var finds []string
		finds, err = cmdLint(root(*dir))
		if err == nil {
			if len(finds) == 0 {
				msg = "доска и архив в порядке"
			} else {
				for _, f := range finds {
					fmt.Println(f)
				}
				fmt.Fprintf(os.Stderr, "находок: %d\n", len(finds))
				logRun(1)
				os.Exit(1)
			}
		}
	case "batch":
		fs := flag.NewFlagSet("batch", flag.ExitOnError)
		dir := fs.String("C", gdir, "стартовая директория")
		limit := fs.Int("limit", batchDefaultLimit, "сколько задач берём в пачку")
		needArgs(frame.ParseArgs(fs, args[1:]), 0, 0, "batch [--limit N]")
		msg, err = cmdBatch(root(*dir), *limit)
	case "kinds":
		fs := flag.NewFlagSet("kinds", flag.ExitOnError)
		dir := fs.String("C", gdir, "стартовая директория")
		needArgs(frame.ParseArgs(fs, args[1:]), 0, 0, "kinds")
		msg, err = cmdKinds(root(*dir))
	case "id":
		fs := flag.NewFlagSet("id", flag.ExitOnError)
		dir := fs.String("C", gdir, "стартовая директория")
		needArgs(frame.ParseArgs(fs, args[1:]), 0, 0, "id")
		msg, err = cmdID(root(*dir))
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
