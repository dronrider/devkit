package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/dronrider/devkit/internal/frame"
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
  code <ID> [--dry-run] [--probe] открыть окно vscode на копии окна
                                  (../<проект>-<поставщик>) и переключить её на
                                  ветку задачи; ключи второй подписки едут в
                                  настройки самой копии, а порядок первого
                                  запуска с нуля лежит в tools/shipctl/README.md.
                                  --dry-run печатает окружение и не запускает
                                  редактор, --probe стучится в endpoint
                                  подписки и называет модель из ответа
  merge <ID> [--test "cmd"]       предусловия (чистое дерево, задача в
        [--deploy "cmd"] [--push] In progress, прод не сломан, Check пуст,
        [--train]                 ревью без открытых замечаний), ребейз ветки
                                  на main, тесты,
                                  fast-forward-слияние, выкат, перевод в Check;
                                  --train копит задачу в поезд: сливает без
                                  выката, задача остаётся в In progress
  ship [--deploy "cmd"] [--push]  выкат поезда: один деплой на все слитые
        [--drain]                 после прошлого выката задачи, все разом в
                                  Check, тег deployed сдвигается на main;
                                  --drain это разлив без сессии (close, watch):
                                  пустой поезд, занятая очередь, занятый
                                  конвейер и сломанный прод молчат нулём
                                  вместо ошибки, а провал
                                  деплоя вдобавок ставит признак провала на
                                  первую задачу состава, чтобы следующий разлив
                                  промолчал сам
  smoke <ID> [--push]             отметить прогон агентской части сценария
                                  после выката: строка «smoke прогнан, <дата>»
                                  в разделе «Выкат» файла задачи; с отметкой
                                  очередь выката свободна, а закрытие идёт
                                  своим ходом: агентскую строку доводит до Done
                                  тик devkitctl watch, приёмка видов mixed и
                                  user остаётся в Check за человеком
  revert <ID> [--test "cmd"]      откат коммитов задачи с main (ищутся по ID
         [-m "..."] [--push]      в subject, коммиты доски не трогаются),
                                  возврат задачи в In progress и снятие
                                  признака провала проверки; задачу из
                                  невыкаченного поезда снимает без деплоя
  push [--check-only <remote_sha> пуш main калиткой DK-602: пропускает
        <local_sha>]              диапазон, где каждый код-коммит (дифф вне
                                  docs/TASKS.md, docs/TASKS-archive.md и
                                  docs/tasks/) несёт в subject ID задачи не из
                                  Backlog, а голый код без такого ID отбивает
                                  как раньше; мелочь, слитая в main мимо
                                  ship/merge (однокоммитный багфикс), больше не
                                  запирает следующий пуш чистой доски.
                                  --check-only только проверяет названную пару
                                  sha и ничего не пушит, этим флагом её зовёт
                                  hooks/pre-push вместо своего разбора диапазона

Команды тестов и выката передаются строкой и выполняются через sh -c. Без
--test команда тестов берётся из ключа test в .devkit/deploy.local, а нет ни
флага, ни ключа значит отказ: ветка сливается только зелёной. Ключ читает
только merge: у revert пустой --test это осознанное «без прогона». Без
--deploy команда выката берётся из .devkit/deploy.local (гитигнорнут): shipctl
катит её сам при autonomous=true, иначе оставляет пользователю. Явный --deploy
это указание выкатить прямо сейчас, оно сильнее конфига. Шаг выката идёт под
пределом времени: ключ deploy_timeout там же, умолчание 30m, вставшая команда
убивается с потомками и валит выкат.
Доску двигает taskctl (нужен в PATH), коммит доски делается сам; --push
отправляет результат в origin, без него пуш остаётся за пользователем. При
autonomous=true merge и revert пушат сами и без флага, иначе origin отстал
бы от прода; revert тогда и повторный выкат катит сам.
Каталог конфига второй подписки лежит в машинном слое, вне репозиториев
(~/.devkit/claude-glm/settings.json): болванку раскладывает devkitctl doctor
--fix, endpoint и токен пользователь вписывает туда один раз, а нехватку ключей
называет devkitctl doctor. Токен shipctl не печатает никогда, у него только
признак «есть» или «нет».
Сообщение коммита-отката по умолчанию «revert: <ID> откат ...», флаг -m
задаёт своё, если в проекте белый список префиксов.
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
	touchWork(args)
	var msg string
	var err error
	switch args[0] {
	case "status":
		fs := flag.NewFlagSet("status", flag.ExitOnError)
		dir := fs.String("C", gdir, "стартовая директория")
		needArgs(frame.ParseArgs(fs, args[1:]), 0, 0, "status")
		msg, err = cmdStatus(root(*dir))
	case "start":
		fs := flag.NewFlagSet("start", flag.ExitOnError)
		dir := fs.String("C", gdir, "стартовая директория")
		p := StartParams{}
		fs.StringVar(&p.Slug, "slug", "", "хвост имени ветки: <id>-<хвост>")
		fs.BoolVar(&p.Push, "push", false, "запушить коммит доски после перевода в In progress")
		pos := frame.ParseArgs(fs, args[1:])
		needArgs(pos, 1, 1, "start <ID> [--slug хвост] [--push]")
		p.ID = pos[0]
		msg, err = cmdStart(root(*dir), p)
	case "code":
		fs := flag.NewFlagSet("code", flag.ExitOnError)
		dir := fs.String("C", gdir, "стартовая директория")
		p := CodeParams{}
		fs.BoolVar(&p.DryRun, "dry-run", false, "напечатать окружение и не запускать редактор")
		fs.BoolVar(&p.Probe, "probe", false, "сходить в endpoint второй подписки и назвать модель из ответа")
		pos := frame.ParseArgs(fs, args[1:])
		needArgs(pos, 1, 1, "code <ID> [--dry-run] [--probe]")
		p.ID = pos[0]
		msg, err = cmdCode(root(*dir), p)
	case "merge":
		fs := flag.NewFlagSet("merge", flag.ExitOnError)
		dir := fs.String("C", gdir, "стартовая директория")
		p := MergeParams{}
		fs.StringVar(&p.Test, "test", "", "команда тестов проекта (sh -c)")
		fs.StringVar(&p.Deploy, "deploy", "", "команда выката, без неё выкат за пользователем")
		fs.BoolVar(&p.Train, "train", false, "слить в поезд: без выката, задача остаётся в In progress")
		fs.BoolVar(&p.Push, "push", false, "запушить main и доску после слияния")
		pos := frame.ParseArgs(fs, args[1:])
		needArgs(pos, 1, 1, "merge <ID> --test \"cmd\" [--deploy \"cmd\"] [--push]")
		p.ID = pos[0]
		msg, err = cmdMerge(root(*dir), p)
	case "ship":
		fs := flag.NewFlagSet("ship", flag.ExitOnError)
		dir := fs.String("C", gdir, "стартовая директория")
		p := ShipParams{}
		fs.StringVar(&p.Deploy, "deploy", "", "команда выката, без неё берётся из .devkit/deploy.local")
		fs.BoolVar(&p.Push, "push", false, "запушить main, тег и доску после выката")
		fs.BoolVar(&p.Drain, "drain", false, "разлив: пустой поезд, занятая очередь, занятый конвейер и сломанный прод молчат нулём, провал деплоя ставит признак провала")
		needArgs(frame.ParseArgs(fs, args[1:]), 0, 0, "ship [--deploy \"cmd\"] [--push] [--drain]")
		msg, err = cmdShip(root(*dir), p)
	case "smoke":
		fs := flag.NewFlagSet("smoke", flag.ExitOnError)
		dir := fs.String("C", gdir, "стартовая директория")
		p := SmokeParams{}
		fs.BoolVar(&p.Push, "push", false, "запушить коммит отметки")
		pos := frame.ParseArgs(fs, args[1:])
		needArgs(pos, 1, 1, "smoke <ID> [--push]")
		p.ID = pos[0]
		msg, err = cmdSmoke(root(*dir), p)
	case "revert":
		fs := flag.NewFlagSet("revert", flag.ExitOnError)
		dir := fs.String("C", gdir, "стартовая директория")
		p := RevertParams{}
		fs.StringVar(&p.Test, "test", "", "команда тестов после отката (sh -c)")
		fs.StringVar(&p.Msg, "m", "", "сообщение коммита-отката")
		fs.BoolVar(&p.Push, "push", false, "запушить откат и доску")
		pos := frame.ParseArgs(fs, args[1:])
		needArgs(pos, 1, 1, "revert <ID> [--test \"cmd\"] [-m \"...\"] [--push]")
		p.ID = pos[0]
		msg, err = cmdRevert(root(*dir), p)
	case "push":
		fs := flag.NewFlagSet("push", flag.ExitOnError)
		dir := fs.String("C", gdir, "стартовая директория")
		p := PushParams{}
		fs.BoolVar(&p.CheckOnly, "check-only", false, "только проверить диапазон remote_sha local_sha, не пушить (для hooks/pre-push)")
		pos := frame.ParseArgs(fs, args[1:])
		if p.CheckOnly {
			needArgs(pos, 2, 2, "push --check-only <remote_sha> <local_sha>")
			p.RemoteSHA, p.LocalSHA = pos[0], pos[1]
		} else {
			needArgs(pos, 0, 0, "push")
		}
		msg, err = cmdPush(root(*dir), p)
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
