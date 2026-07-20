package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
)

const usageText = `shipctl: слияние и откат задач по правилам доски (RULES.board.md)

  status                          очередь выката по секциям доски и вердикт,
                                  можно ли сливать
  merge <ID> --test "cmd"         предусловия (чистое дерево, задача в
        [--deploy "cmd"] [--push] In progress, Check пуст, ревью без открытых
                                  замечаний), ребейз фичеветки на main, тесты,
                                  fast-forward-слияние, выкат, перевод в Check
  revert <ID> [--test "cmd"]      откат коммитов задачи с main (ищутся по ID
         [-m "..."] [--push]      в subject, коммиты доски не трогаются)
                                  и возврат задачи в In progress

Команды тестов и выката передаются строкой и выполняются через sh -c. Без
--deploy команда выката берётся из .devkit/deploy.local (гитигнорнут): shipctl
катит её сам при autonomous=true, иначе оставляет пользователю. Явный --deploy
это указание выкатить прямо сейчас, оно сильнее конфига.
Доску двигает taskctl (нужен в PATH), коммит доски делается сам; --push
отправляет результат в origin, без него пуш остаётся за пользователем. При
autonomous=true merge пушит сам и без флага: иначе origin отстал бы от прода.
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
	case "status":
		fs := flag.NewFlagSet("status", flag.ExitOnError)
		dir := fs.String("C", gdir, "стартовая директория")
		fs.Parse(args[1:])
		msg, err = cmdStatus(root(*dir))
	case "merge":
		if len(args) < 2 || strings.HasPrefix(args[1], "-") {
			fail(fmt.Errorf("жду: merge <ID> --test \"cmd\" [--deploy \"cmd\"] [--push]"))
		}
		fs := flag.NewFlagSet("merge", flag.ExitOnError)
		dir := fs.String("C", gdir, "стартовая директория")
		p := MergeParams{ID: args[1]}
		fs.StringVar(&p.Test, "test", "", "команда тестов проекта (sh -c)")
		fs.StringVar(&p.Deploy, "deploy", "", "команда выката, без неё выкат за пользователем")
		fs.BoolVar(&p.Push, "push", false, "запушить main и доску после слияния")
		fs.Parse(args[2:])
		msg, err = cmdMerge(root(*dir), p)
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
