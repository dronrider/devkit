package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
)

const usageText = `shipctl: слияние и откат задач по правилам доски (RULES.board.md)

  status                          очередь выката по секциям доски, поезд,
                                  worktree задач и вердикт, можно ли сливать
  start <ID> [--slug хвост]       взять задачу в работу в отдельном дереве:
        [--push]                  ветка по ID в git worktree рядом с проектом
                                  (../<проект>-<id>), задача из Backlog
                                  переводится в In progress; основной чекаут
                                  остаётся на main, и параллельные сессии не
                                  толкаются в одном рабочем дереве
  merge <ID> [--test "cmd"]       предусловия (чистое дерево, задача в
        [--deploy "cmd"] [--push] In progress, прод не сломан, Check пуст,
        [--train]                 ревью без открытых замечаний), ребейз ветки
                                  на main, тесты,
                                  fast-forward-слияние, выкат, перевод в Check;
                                  --train копит задачу в поезд: сливает без
                                  выката, задача остаётся в In progress
  ship [--deploy "cmd"] [--push]  выкат поезда: один деплой на все слитые
                                  после прошлого выката задачи, все разом в
                                  Check, тег deployed сдвигается на main
  revert <ID> [--test "cmd"]      откат коммитов задачи с main (ищутся по ID
         [-m "..."] [--push]      в subject, коммиты доски не трогаются),
                                  возврат задачи в In progress и снятие
                                  признака провала проверки; задачу из
                                  невыкаченного поезда снимает без деплоя

Команды тестов и выката передаются строкой и выполняются через sh -c. Без
--test команда тестов берётся из ключа test в .devkit/deploy.local, а нет ни
флага, ни ключа значит отказ: ветка сливается только зелёной. Ключ читает
только merge: у revert пустой --test это осознанное «без прогона». Без
--deploy команда выката берётся из .devkit/deploy.local (гитигнорнут): shipctl
катит её сам при autonomous=true, иначе оставляет пользователю. Явный --deploy
это указание выкатить прямо сейчас, оно сильнее конфига.
Доску двигает taskctl (нужен в PATH), коммит доски делается сам; --push
отправляет результат в origin, без него пуш остаётся за пользователем. При
autonomous=true merge и revert пушат сами и без флага, иначе origin отстал
бы от прода; revert тогда и повторный выкат катит сам.
Сообщение коммита-отката по умолчанию «revert: <ID> откат ...», флаг -m
задаёт своё, если в проекте белый список префиксов.
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

// helpRequested ищет -h/--help среди аргументов до разбора, как в taskctl:
// «merge --help» без этого отбивался как merge без ID, а не показывал справку.
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
	var msg string
	var err error
	switch args[0] {
	case "status":
		fs := flag.NewFlagSet("status", flag.ExitOnError)
		dir := fs.String("C", gdir, "стартовая директория")
		fs.Parse(args[1:])
		msg, err = cmdStatus(root(*dir))
	case "start":
		if len(args) < 2 || strings.HasPrefix(args[1], "-") {
			fail(fmt.Errorf("жду: start <ID> [--slug хвост] [--push]"))
		}
		fs := flag.NewFlagSet("start", flag.ExitOnError)
		dir := fs.String("C", gdir, "стартовая директория")
		p := StartParams{ID: args[1]}
		fs.StringVar(&p.Slug, "slug", "", "хвост имени ветки: <id>-<хвост>")
		fs.BoolVar(&p.Push, "push", false, "запушить коммит доски после перевода в In progress")
		fs.Parse(args[2:])
		msg, err = cmdStart(root(*dir), p)
	case "merge":
		if len(args) < 2 || strings.HasPrefix(args[1], "-") {
			fail(fmt.Errorf("жду: merge <ID> --test \"cmd\" [--deploy \"cmd\"] [--push]"))
		}
		fs := flag.NewFlagSet("merge", flag.ExitOnError)
		dir := fs.String("C", gdir, "стартовая директория")
		p := MergeParams{ID: args[1]}
		fs.StringVar(&p.Test, "test", "", "команда тестов проекта (sh -c)")
		fs.StringVar(&p.Deploy, "deploy", "", "команда выката, без неё выкат за пользователем")
		fs.BoolVar(&p.Train, "train", false, "слить в поезд: без выката, задача остаётся в In progress")
		fs.BoolVar(&p.Push, "push", false, "запушить main и доску после слияния")
		fs.Parse(args[2:])
		msg, err = cmdMerge(root(*dir), p)
	case "ship":
		fs := flag.NewFlagSet("ship", flag.ExitOnError)
		dir := fs.String("C", gdir, "стартовая директория")
		p := ShipParams{}
		fs.StringVar(&p.Deploy, "deploy", "", "команда выката, без неё берётся из .devkit/deploy.local")
		fs.BoolVar(&p.Push, "push", false, "запушить main, тег и доску после выката")
		fs.Parse(args[1:])
		msg, err = cmdShip(root(*dir), p)
	case "revert":
		if len(args) < 2 || strings.HasPrefix(args[1], "-") {
			fail(fmt.Errorf("жду: revert <ID> [--test \"cmd\"] [-m \"...\"] [--push]"))
		}
		fs := flag.NewFlagSet("revert", flag.ExitOnError)
		dir := fs.String("C", gdir, "стартовая директория")
		p := RevertParams{ID: args[1]}
		fs.StringVar(&p.Test, "test", "", "команда тестов после отката (sh -c)")
		fs.StringVar(&p.Msg, "m", "", "сообщение коммита-отката")
		fs.BoolVar(&p.Push, "push", false, "запушить откат и доску")
		fs.Parse(args[2:])
		msg, err = cmdRevert(root(*dir), p)
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
