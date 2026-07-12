package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
)

const usageText = `taskctl: механика канбан-доски docs/TASKS.md

Команды:
  init --prefix XR [--name "..."]             скелет доски в корне репозитория
                                              (docs/TASKS.md, TASKS-archive.md, docs/tasks/)
  id                                          следующий свободный ID
  add --title "..." --type bug|task|LLD --rank "а+б+в+г+д"
      [--link "..."] [--status backlog|in-progress|check|blocked]
      [--id XR-NNN] [--reason "..."]          завести задачу (по умолчанию в Backlog)
  move <ID> <backlog|in-progress|check|blocked> [--reason "..."]
                                              перевести между статусами
  close <ID> [--commit sha1,sha2] [--date ГГГГ-ММ-ДД] [--link "..."]
                                              в архив + файл задачи в tasks/archive/<год>/
  sort                                        пересортировать Backlog по R
  lint                                        проверить инварианты доски и архива

Общий флаг -C <dir>: откуда искать корень репозитория (по умолчанию текущая
директория), ставится и перед командой, и после неё.
Сумма R и бакет P считаются из разбивки --rank сами, руками их не передать.
`

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
		fs.Parse(args[1:])
		msg, err = cmdAdd(root(*dir), p)
	case "move":
		if len(args) < 3 {
			fail(fmt.Errorf("жду: move <ID> <статус> [--reason ...]"))
		}
		fs := flag.NewFlagSet("move", flag.ExitOnError)
		dir := fs.String("C", gdir, "стартовая директория")
		reason := fs.String("reason", "", "причина блокировки (для blocked)")
		fs.Parse(args[3:])
		msg, err = cmdMove(root(*dir), args[1], args[2], *reason)
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
		fs.Parse(args[2:])
		msg, err = cmdClose(root(*dir), p)
	case "sort":
		fs := flag.NewFlagSet("sort", flag.ExitOnError)
		dir := fs.String("C", gdir, "стартовая директория")
		fs.Parse(args[1:])
		msg, err = cmdSort(root(*dir))
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
