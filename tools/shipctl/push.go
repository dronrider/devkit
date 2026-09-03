package main

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/dronrider/devkit/internal/reviewnote"
)

// PushParams это параметры команды push.
type PushParams struct {
	CheckOnly bool
	// RemoteSHA и LocalSHA нужны только с CheckOnly: их называет git в
	// строке stdin вызова pre-push, сама команда push вычисляет диапазон
	// сама через origin/main и текущий main.
	RemoteSHA, LocalSHA string
}

// cmdPush пушит main калиткой DK-602: пропускает диапазон, где каждый
// код-коммит (коммит, чей дифф выходит за пределы docs/TASKS.md,
// docs/TASKS-archive.md и docs/tasks/) несёт в subject ID задачи не из
// Backlog, а голый код без такого ID отбивает по-старому. До этой калитки
// хук pre-push смотрел на диапазон целиком: один код-коммит без пуша (мелочь
// в main по однокоммитному исключению) запирал пуш чистой доски всем
// сессиям до следующего выката (DK-602).
//
// Проверка диапазона общая с хуком: pre-push зовёт `shipctl push
// --check-only <remote_sha> <local_sha>` и решает по коду выхода, не
// дублируя разбор коммитов в shell. С CheckOnly команда только проверяет
// названную пару sha и ничего не пушит; без него она сама находит текущий
// main и origin/main и после успешной проверки пушет.
func cmdPush(root string, p PushParams) (string, error) {
	if p.CheckOnly {
		if err := rangeVerdict(root, p.RemoteSHA, p.LocalSHA); err != nil {
			return "", err
		}
		if err := reviewTraceGate(root, p.RemoteSHA, p.LocalSHA); err != nil {
			return "", err
		}
		return fmt.Sprintf("диапазон %s..%s пропущен: доска либо код со следом ревью",
			short(p.RemoteSHA), short(p.LocalSHA)), nil
	}
	primary, _, err := primaryRoot(root)
	if err != nil {
		return "", err
	}
	root = primary
	if corpActive(root) {
		return "", corpRefused("push")
	}
	unlock, err := acquireLock(root)
	if err != nil {
		return "", err
	}
	defer unlock()
	main, err := preflight(root)
	if err != nil {
		return "", err
	}
	if err := freshMain(root, main); err != nil {
		return "", err
	}
	localSHA, err := git(root, "rev-parse", main)
	if err != nil {
		return "", err
	}
	remoteSHA, err := git(root, "rev-parse", "origin/"+main)
	if err != nil {
		return "", err
	}
	if remoteSHA == localSHA {
		return fmt.Sprintf("%s и origin/%s совпадают, пушить нечего", main, main), nil
	}
	if err := rangeVerdict(root, remoteSHA, localSHA); err != nil {
		return "", err
	}
	if _, err := git(root, "push"); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s запушен, %s..%s", main, short(remoteSHA), short(localSHA)), nil
}

// rangeVerdict решает критерий DK-602 для диапазона remote..local: пуск
// разрешён, если каждый коммит диапазона либо чистая доска (boardOnly), либо
// код с ID задачи в subject, которая на доске сейчас не в Backlog. Голый код
// без такого ID и код с ID из Backlog отбивают весь диапазон разом, отказ
// называет первый такой коммит по имени, чтобы находка сразу показывала, что
// чинить. ID ищется тем же способом, что и владение коммитом в revert
// (firstID): первый токен вида «<префикс>-<число>» в subject, префикс берётся
// с доски, а не гадается по любым заглавным буквам, иначе «UTF-8» в тексте
// коммита занял бы его место (как уже бывало у ownsSubject).
func rangeVerdict(root, remoteSHA, localSHA string) error {
	if remoteSHA == "" || localSHA == "" {
		return fmt.Errorf("пустой sha в диапазоне пуша")
	}
	log, err := git(root, "log", "--reverse", remoteSHA+".."+localSHA, "--format=%H%x09%s")
	if err != nil {
		return err
	}
	log = strings.TrimSpace(log)
	if log == "" {
		return nil
	}
	b, err := loadBoard(root)
	if err != nil {
		// Доски в репозитории нет вовсе (MR чужого трекера, ветка без
		// трекера): ID задачи взять неоткуда, и критерий DK-602 тут
		// неприменим. Код такого репозитория судит ворот следа ревью, он
		// стоит следом и доски не требует.
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	pref := b.prefixOr("DK")
	for _, ln := range strings.Split(log, "\n") {
		sha, subj, ok := strings.Cut(ln, "\t")
		if !ok {
			continue
		}
		// --no-renames: без него --name-only печатает у переименования
		// только путь назначения, и перенос кода в docs/tasks/ сошёл бы за
		// доску (та же дыра, которую закрывал старый хук до этой калитки).
		files, err := git(root, "show", "--no-renames", "--name-only", "--pretty=", sha)
		if err != nil {
			return err
		}
		if boardOnly(files) {
			continue
		}
		id := firstID(subj, pref)
		if id == "" {
			return fmt.Errorf("код без ID задачи в subject у %s %q: рубеж пускает диапазон, только когда каждый код-коммит несёт ID вида %s-NNN не из Backlog", short(sha), subj, pref)
		}
		switch b.sectOf(id) {
		case "backlog":
			return fmt.Errorf("%s ещё в Backlog у %s %q: код с её ID до взятия задачи в работу рубеж считает чужим", id, short(sha), subj)
		case "":
			// Живой доски мало: loadBoard не видит docs/TASKS-archive.md, а
			// фикс уже закрытой и заархивированной задачи это обычный,
			// ожидаемый код-коммит (сама DK-602 сюда попадёт после своего
			// закрытия). Отбой только когда ID не найден нигде, ни в
			// работе, ни в архиве: тогда это опечатка, выдуманный номер или
			// задача, которую ещё не завели.
			inArchive, err := archiveHas(root, id)
			if err != nil {
				return err
			}
			if !inArchive {
				return fmt.Errorf("%s не найдена ни на доске, ни в архиве у %s %q: похоже на опечатку, выдуманный номер или незаведённую задачу; легитимный код закрытой задачи нашёлся бы в docs/TASKS-archive.md", id, short(sha), subj)
			}
		}
	}
	return nil
}

// short укорачивает sha для сообщений и ошибок, полный sha возвращается как
// есть, когда он и так короче.
func short(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

// reviewTraceGate требует след ревью у диапазона, в котором есть код. Правило
// одно на все происхождения правки: код агента не уходит в origin и не
// становится MR без ревью, а ревью идёт по скиллу review свежим контекстом, не
// тем, который правку писал. След у ревью бывает двух видов, и вороту довольно
// любого: git-заметка ревью ровно на HEAD (её пишет `taskctl review level` и
// `taskctl review clean` там, где доски нет) либо строка уровня в файле задачи
// ветки доски. Заметка судится по HEAD, а не по любому предку: код, дописанный
// после ревью, ревью не проходил, и след предка про него ничего не говорит.
//
// Чистая доска без единого код-коммита проходит без следа: правило требует
// пушить коммит доски сразу, а ревьюить там нечего. Прямая команда пользователя
// (DEVKIT_PUSH_OK=1) снимает этот ворот вместе с рубежом пуша: она снимается
// раньше, в самом хуке, и до проверки дело не доходит.
func reviewTraceGate(root, remoteSHA, localSHA string) error {
	code, err := hasCodeCommit(root, remoteSHA, localSHA)
	if err != nil || !code {
		return err
	}
	head, err := git(root, "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	noted, err := reviewnote.Has(root, "HEAD")
	if err != nil {
		return err
	}
	if noted {
		return nil
	}
	seen := "заметки ревью на HEAD нет"
	if id := branchTaskID(root); id != "" {
		if hasReviewLevel(readTaskDoc(root, id)) {
			return nil
		}
		seen = fmt.Sprintf("заметки ревью на HEAD нет, строки уровня в docs/tasks/%s.md тоже", id)
	}
	return fmt.Errorf(`нет следа ревью у кода в диапазоне %s..%s (%s, HEAD %s).
Ревью идёт по скиллу review свежим контекстом, а не тем, который писал правку.
След ставится одной командой:
  вне доски: taskctl review level <ярлык> <0-3> "причина" (заметка git на HEAD, ref %s),
    ревью без замечаний это taskctl review clean <ярлык> "пояснение";
  у ветки задачи доски: taskctl review level <ID> <0-3> "причина", строка встаёт в docs/tasks/<ID>.md.
Прямая команда пользователя снимает рубеж переменной DEVKIT_PUSH_OK=1`,
		short(remoteSHA), short(localSHA), seen, short(head), reviewnote.Ref)
}

// hasCodeCommit говорит, есть ли в диапазоне хоть один код-коммит, то есть
// коммит с диффом за пределами доски. Критерий тот же, что у rangeVerdict, и
// --no-renames тут по той же причине: без него перенос кода в docs/tasks/ сошёл
// бы за доску.
func hasCodeCommit(root, remoteSHA, localSHA string) (bool, error) {
	if remoteSHA == "" || localSHA == "" {
		return false, fmt.Errorf("пустой sha в диапазоне пуша")
	}
	log, err := git(root, "log", "--format=%H", remoteSHA+".."+localSHA)
	if err != nil {
		return false, err
	}
	for _, sha := range strings.Fields(log) {
		files, err := git(root, "show", "--no-renames", "--name-only", "--pretty=", sha)
		if err != nil {
			return false, err
		}
		if !boardOnly(files) {
			return true, nil
		}
	}
	return false, nil
}

// branchTaskID достаёт ID задачи из имени текущей ветки: ветку задачи зовут
// строчным ID с хвостом-слагом или без (`dk-005`, `dk-005-worktree`), тем же
// правилом её узнаёт merge (branchOfTask). Пустая строка значит, что ветка не
// про задачу доски или доски тут нет вовсе, и след ревью остаётся искать в
// заметке.
func branchTaskID(root string) string {
	b, err := loadBoard(root)
	if err != nil {
		return ""
	}
	pref := b.prefixOr("DK")
	branch, err := git(root, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return ""
	}
	m := regexp.MustCompile(`(?i)^`+regexp.QuoteMeta(pref)+`-([0-9]+)($|-)`).
		FindStringSubmatch(strings.TrimSpace(branch))
	if m == nil {
		return ""
	}
	return pref + "-" + m[1]
}

// checkRoot находит дерево для проверки диапазона. Доска главнее: в проекте с
// доской проверка идёт от её корня и ничего не меняется. Без доски берётся
// вершина git-дерева, потому что ворот следа ревью стоит и там, где задач нет
// вовсе, а прежний отказ «не нашёл docs/TASKS.md» запирал бы там любой пуш.
func checkRoot(dir string) (string, error) {
	if root, err := findRoot(dir); err == nil {
		return root, nil
	}
	top, err := git(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", fmt.Errorf("ни доски, ни git-дерева вверх от %s: проверять диапазон не в чем", dir)
	}
	return strings.TrimSpace(top), nil
}
