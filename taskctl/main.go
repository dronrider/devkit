package main

import (
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
)

const usageText = `taskctl: механика канбан-доски docs/TASKS.md

Смотреть доску:
  list [backlog|in-progress|check|blocked]    доска по секциям без прозы; без
                                              аргумента Backlog обрезан до 10 строк
  show <ID>                                   строка задачи, секция, файл задачи
                                              (закрытые ищутся в архиве)
  id                                          следующий свободный ID

Менять доску:
  add --title "..." --type bug|task|LLD --rank "а+б+в+г+д"
      [--link "..."] [--status ...] [--id XR-NNN] [--reason "..."]
                                              завести задачу (по умолчанию в Backlog;
                                              без --link и файла в ячейке будет «-»)
  move <ID> <статус> [--reason "..."]         перевести между статусами
  set <ID> [--title "..."] [--type ...] [--rank "..."] [--link "..."]
                                              поправить ячейки строки
  file <ID>                                   создать docs/tasks/<ID>.md и ссылку в строке
  close <ID> [--commit sha1,sha2] [--date ГГГГ-ММ-ДД] [--link "..."]
                                              в архив + файл задачи в tasks/archive/<год>/
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

У изменяющих команд флаги -m "docs(tasks): ..." и --push: закоммитить ровно
тронутые файлы доски (и запушить), чужой индекс не задевается.
Статусы принимаются в любом регистре, «In progress» = in-progress.
Сумма R и бакет P считаются из разбивки --rank сами, руками их не передать.
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

func fail(err error) {
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
	var msg string
	var err error
	switch args[0] {
	case "init":
		fs := flag.NewFlagSet("init", flag.ExitOnError)
		dir := fs.String("C", gdir, "стартовая директория")
		var p InitParams
		fs.StringVar(&p.Prefix, "prefix", "", "префикс ID задач, заглавными (XR)")
		fs.StringVar(&p.Name, "name", "", "название проекта в шапке, по умолчанию имя директории")
		fs.Parse(args[1:])
		msg, err = cmdInit(*dir, p)
	case "add":
		fs := flag.NewFlagSet("add", flag.ExitOnError)
		dir := fs.String("C", gdir, "стартовая директория")
		var p AddParams
		fs.StringVar(&p.ID, "id", "", "ID задачи, по умолчанию следующий свободный")
		fs.StringVar(&p.Title, "title", "", "заголовок строки")
		fs.StringVar(&p.Type, "type", "task", "тип: bug / task / LLD")
		fs.StringVar(&p.Rank, "rank", "", "разбивка ранга «а+б+в+г+д»")
		fs.StringVar(&p.Link, "link", "", "ячейка ссылки, по умолчанию файл задачи")
		fs.StringVar(&p.Status, "status", "backlog", "секция доски")
		fs.StringVar(&p.Reason, "reason", "", "причина блокировки (для blocked)")
		commitFlags(fs, &p.Commit)
		fs.Parse(args[1:])
		msg, err = cmdAdd(root(*dir), p)
	case "move":
		if len(args) < 3 {
			fail(fmt.Errorf("жду: move <ID> <статус> [--reason ...]"))
		}
		fs := flag.NewFlagSet("move", flag.ExitOnError)
		dir := fs.String("C", gdir, "стартовая директория")
		reason := fs.String("reason", "", "причина блокировки (для blocked)")
		var c CommitOpts
		commitFlags(fs, &c)
		fs.Parse(args[3:])
		msg, err = cmdMove(root(*dir), args[1], args[2], *reason, c)
	case "set":
		if len(args) < 2 {
			fail(fmt.Errorf("жду: set <ID> [--title ...] [--type ...] [--rank ...] [--link ...]"))
		}
		fs := flag.NewFlagSet("set", flag.ExitOnError)
		dir := fs.String("C", gdir, "стартовая директория")
		p := SetParams{ID: args[1]}
		fs.StringVar(&p.Title, "title", "", "новый заголовок строки")
		fs.StringVar(&p.Type, "type", "", "новый тип: bug / task / LLD")
		fs.StringVar(&p.Rank, "rank", "", "новая разбивка ранга «а+б+в+г+д»")
		fs.StringVar(&p.Link, "link", "", "новая ячейка ссылки")
		commitFlags(fs, &p.Commit)
		fs.Parse(args[2:])
		msg, err = cmdSet(root(*dir), p)
	case "file":
		if len(args) < 2 {
			fail(fmt.Errorf("жду: file <ID>"))
		}
		fs := flag.NewFlagSet("file", flag.ExitOnError)
		dir := fs.String("C", gdir, "стартовая директория")
		var c CommitOpts
		commitFlags(fs, &c)
		fs.Parse(args[2:])
		msg, err = cmdFile(root(*dir), args[1], c)
	case "list":
		fs := flag.NewFlagSet("list", flag.ExitOnError)
		dir := fs.String("C", gdir, "стартовая директория")
		sect := ""
		if len(args) > 1 && !strings.HasPrefix(args[1], "-") {
			sect = args[1]
			fs.Parse(args[2:])
		} else {
			fs.Parse(args[1:])
		}
		msg, err = cmdList(root(*dir), sect)
	case "show":
		if len(args) < 2 {
			fail(fmt.Errorf("жду: show <ID>"))
		}
		fs := flag.NewFlagSet("show", flag.ExitOnError)
		dir := fs.String("C", gdir, "стартовая директория")
		fs.Parse(args[2:])
		msg, err = cmdShow(root(*dir), args[1])
	case "review":
		if len(args) < 2 {
			fail(fmt.Errorf("жду: review add|resolve|show|stats ..."))
		}
		switch args[1] {
		case "add":
			if len(args) < 4 {
				fail(fmt.Errorf("жду: review add <ID> \"суть замечания\""))
			}
			fs := flag.NewFlagSet("review add", flag.ExitOnError)
			dir := fs.String("C", gdir, "стартовая директория")
			var c CommitOpts
			commitFlags(fs, &c)
			fs.Parse(args[4:])
			msg, err = cmdReviewAdd(root(*dir), args[2], args[3], c)
		case "resolve":
			if len(args) < 5 {
				fail(fmt.Errorf("жду: review resolve <ID> <N> fixed|rejected [--reason \"...\"]"))
			}
			num, aerr := strconv.Atoi(args[3])
			if aerr != nil {
				fail(fmt.Errorf("номер замечания %q не число", args[3]))
			}
			fs := flag.NewFlagSet("review resolve", flag.ExitOnError)
			dir := fs.String("C", gdir, "стартовая директория")
			reason := fs.String("reason", "", "причина отклонения (для rejected)")
			var c CommitOpts
			commitFlags(fs, &c)
			fs.Parse(args[5:])
			msg, err = cmdReviewResolve(root(*dir), args[2], num, args[4], *reason, c)
		case "show":
			if len(args) < 3 {
				fail(fmt.Errorf("жду: review show <ID>"))
			}
			fs := flag.NewFlagSet("review show", flag.ExitOnError)
			dir := fs.String("C", gdir, "стартовая директория")
			fs.Parse(args[3:])
			msg, err = cmdReviewShow(root(*dir), args[2])
		case "stats":
			fs := flag.NewFlagSet("review stats", flag.ExitOnError)
			dir := fs.String("C", gdir, "стартовая директория")
			fs.Parse(args[2:])
			msg, err = cmdReviewStats(root(*dir))
		default:
			fail(fmt.Errorf("неизвестная подкоманда review %q, жду add / resolve / show / stats", args[1]))
		}
	case "close":
		if len(args) < 2 {
			fail(fmt.Errorf("жду: close <ID> [--commit ...] [--date ...]"))
		}
		fs := flag.NewFlagSet("close", flag.ExitOnError)
		dir := fs.String("C", gdir, "стартовая директория")
		p := CloseParams{ID: args[1]}
		fs.StringVar(&p.Commits, "commit", "", "хеши коммитов через запятую")
		fs.StringVar(&p.Date, "date", "", "дата закрытия, по умолчанию сегодня")
		fs.StringVar(&p.Link, "link", "", "ячейка ссылки в архиве, по умолчанию собирается сама")
		commitFlags(fs, &p.Commit)
		fs.Parse(args[2:])
		msg, err = cmdClose(root(*dir), p)
	case "sort":
		fs := flag.NewFlagSet("sort", flag.ExitOnError)
		dir := fs.String("C", gdir, "стартовая директория")
		var c CommitOpts
		commitFlags(fs, &c)
		fs.Parse(args[1:])
		msg, err = cmdSort(root(*dir), c)
	case "lint":
		fs := flag.NewFlagSet("lint", flag.ExitOnError)
		dir := fs.String("C", gdir, "стартовая директория")
		fs.Parse(args[1:])
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
				os.Exit(1)
			}
		}
	case "id":
		fs := flag.NewFlagSet("id", flag.ExitOnError)
		dir := fs.String("C", gdir, "стартовая директория")
		fs.Parse(args[1:])
		msg, err = cmdID(root(*dir))
	case "help", "-h", "--help":
		fmt.Print(usageText)
		return
	default:
		fmt.Fprintf(os.Stderr, "неизвестная команда %q\n\n%s", args[0], usageText)
		os.Exit(2)
	}
	if err != nil {
		fail(err)
	}
	fmt.Println(msg)
}
