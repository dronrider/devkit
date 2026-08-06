package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
)

const usageText = `trackctl: разговор с трекером задач корп-контура (docs/lld/DK-074-corp-contour.md)

  status                          привязка проекта, контур, адаптер с перечнем
                                  отсутствующих необязательных операций и
                                  свежесть последнего sync
  issue <KEY>                     тикет глазами адаптера: статус и секция
                                  доски, тип, заголовок, оценка
  take <KEY>                      взять тикет в работу: переход в целевой
                                  статус секции In progress, assign на
                                  пользователя контура и оценка из цены
                                  зеркальной строки доски
  submit <KEY> [--log-only]       сдать тикет: ворклоги по фактам работы и
                                  переход в целевой статус секции Check;
                                  --log-only пишет время и статус не трогает
  sync [--if-stale]               pull статусов: доска догоняет тикеты,
                                  в трекер прогон не пишет; --if-stale
                                  гоняет только по протухшей отметке

Ключ пишется целиком (ABC-12) либо одним номером, префикс тогда берётся из
привязки. Всё, что ходит в трекер, живёт здесь: taskctl остаётся чистой
механикой доски и в сеть не ходит ни одной командой.

Конфига два уровня. Машинный контур ~/.devkit/tracker/<имя>.local это свойство
компании (адаптер, адрес, откуда взять токен, пользователь, таблица статусов,
поля переходов, коэффициенты оценки), проектная привязка .devkit/tracker.local
это свойство проекта (имя контура, ключ проекта, шаблон ветки, путь корп-клона).
Разбор в tools/trackctl/README.md.

Общий флаг -C <dir>: откуда искать корень рабочих файлов devkit, ставится и
перед командой, и после неё.
`

// globalDir вырезает -C до выбора команды, как в соседях: справка обещает
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
		logStart = *dir
		msg, err = cmdStatus(root(*dir))
	case "issue":
		if len(args) < 2 || strings.HasPrefix(args[1], "-") {
			fail(fmt.Errorf("жду: issue <KEY>"))
		}
		fs := flag.NewFlagSet("issue", flag.ExitOnError)
		dir := fs.String("C", gdir, "стартовая директория")
		fs.Parse(args[2:])
		logStart = *dir
		msg, err = cmdIssue(root(*dir), args[1])
	case "take":
		if len(args) < 2 || strings.HasPrefix(args[1], "-") {
			fail(fmt.Errorf("жду: take <KEY>"))
		}
		fs := flag.NewFlagSet("take", flag.ExitOnError)
		dir := fs.String("C", gdir, "стартовая директория")
		fs.Parse(args[2:])
		logStart = *dir
		msg, err = cmdTake(root(*dir), args[1])
	case "submit":
		if len(args) < 2 || strings.HasPrefix(args[1], "-") {
			fail(fmt.Errorf("жду: submit <KEY> [--log-only]"))
		}
		fs := flag.NewFlagSet("submit", flag.ExitOnError)
		dir := fs.String("C", gdir, "стартовая директория")
		logOnly := fs.Bool("log-only", false, "написать ворклоги и не трогать статус")
		fs.Parse(args[2:])
		logStart = *dir
		msg, err = cmdSubmit(root(*dir), args[1], *logOnly)
	case "sync":
		fs := flag.NewFlagSet("sync", flag.ExitOnError)
		dir := fs.String("C", gdir, "стартовая директория")
		ifStale := fs.Bool("if-stale", false, "гонять, только если отметка прогона протухла")
		fs.Parse(args[1:])
		logStart = *dir
		msg, err = cmdSync(root(*dir), *ifStale)
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
