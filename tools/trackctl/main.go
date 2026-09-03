package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/dronrider/devkit/internal/frame"
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
  edit <KEY> [ключи]              правка тикета по названным осям: оценка
      --estimate V                (4h), исполнитель, статус, заголовок, тип,
      --assignee U                произвольное поле (--field имя=значение,
      --status S                  повторяем) и комментарий; без ключей тикет
      --title T                   не трогается вовсе
      --type T
      --field имя=значение
      --comment текст
  sync [--if-stale]               pull статусов: доска догоняет тикеты,
                                  в трекер прогон не пишет; --if-stale
                                  гоняет только по протухшей отметке
  review <KEY> [--mr <url>]       завести строку ревью чужого тикета:
                                  зеркальная строка с пометкой сценария,
                                  файл задачи с постановкой тикета и ссылкой
                                  на MR; тикет не трогается вовсе, повторный
                                  вызов находит стоящую строку

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
Флаги подкоманд стоят где угодно относительно позиционных: и до ключа, и после,
и между ними; лишний позиционный не выбрасывается молча, а отбивается ошибкой.
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
		needArgs(frame.ParseArgs(fs, args[1:]), 0, 0, "status")
		logStart = *dir
		msg, err = cmdStatus(root(*dir))
	case "issue":
		fs := flag.NewFlagSet("issue", flag.ExitOnError)
		dir := fs.String("C", gdir, "стартовая директория")
		pos := frame.ParseArgs(fs, args[1:])
		needArgs(pos, 1, 1, "issue <KEY>")
		logStart = *dir
		msg, err = cmdIssue(root(*dir), pos[0])
	case "take":
		fs := flag.NewFlagSet("take", flag.ExitOnError)
		dir := fs.String("C", gdir, "стартовая директория")
		pos := frame.ParseArgs(fs, args[1:])
		needArgs(pos, 1, 1, "take <KEY>")
		logStart = *dir
		msg, err = cmdTake(root(*dir), pos[0])
	case "submit":
		fs := flag.NewFlagSet("submit", flag.ExitOnError)
		dir := fs.String("C", gdir, "стартовая директория")
		logOnly := fs.Bool("log-only", false, "написать ворклоги и не трогать статус")
		pos := frame.ParseArgs(fs, args[1:])
		needArgs(pos, 1, 1, "submit <KEY> [--log-only]")
		logStart = *dir
		msg, err = cmdSubmit(root(*dir), pos[0], *logOnly)
	case "edit":
		fs := flag.NewFlagSet("edit", flag.ExitOnError)
		dir := fs.String("C", gdir, "стартовая директория")
		estimate := fs.String("estimate", "", "оценка трекера, например 4h")
		assignee := fs.String("assignee", "", "исполнитель: accountId на v3, name на v2")
		status := fs.String("status", "", "перевод в статус трекера одним шагом")
		title := fs.String("title", "", "заголовок тикета")
		kind := fs.String("type", "", "тип тикета")
		var fields fieldList
		fs.Var(&fields, "field", "произвольное поле имя=значение, повторяем")
		comment := fs.String("comment", "", "комментарий в тикет")
		pos := frame.ParseArgs(fs, args[1:])
		needArgs(pos, 1, 1, "edit <KEY> [--estimate V] [--assignee U] [--status S] [--title T] [--type T] [--field имя=значение] [--comment текст]")
		logStart = *dir
		msg, err = cmdEdit(root(*dir), pos[0], editAxes{
			estimate: *estimate,
			assignee: *assignee,
			status:   *status,
			title:    *title,
			kind:     *kind,
			fields:   fields,
			comment:  *comment,
		})
	case "sync":
		fs := flag.NewFlagSet("sync", flag.ExitOnError)
		dir := fs.String("C", gdir, "стартовая директория")
		ifStale := fs.Bool("if-stale", false, "гонять, только если отметка прогона протухла")
		needArgs(frame.ParseArgs(fs, args[1:]), 0, 0, "sync [--if-stale]")
		logStart = *dir
		msg, err = cmdSync(root(*dir), *ifStale)
	case "review":
		fs := flag.NewFlagSet("review", flag.ExitOnError)
		dir := fs.String("C", gdir, "стартовая директория")
		mr := fs.String("mr", "", "ссылка на MR, когда адаптер её не ищет сам либо нашёл не одну")
		pos := frame.ParseArgs(fs, args[1:])
		needArgs(pos, 1, 1, "review <KEY> [--mr <url>]")
		logStart = *dir
		msg, err = cmdReview(root(*dir), pos[0], *mr)
	case "help":
		fmt.Print(usageText)
		return
	default:
		fmt.Fprintf(os.Stderr, "неизвестная команда %q\n\n%s", args[0], usageText)
		logRun(2)
		os.Exit(2)
	}
	if err != nil {
		// Частичный вывод при отказе не выбрасывается: edit с несколькими
		// осями обязан показать, какие успели уехать до отказа трекера.
		if msg != "" {
			fmt.Println(msg)
		}
		fail(err)
	}
	logRun(0)
	fmt.Println(msg)
}
