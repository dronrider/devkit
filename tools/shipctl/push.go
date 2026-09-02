package main

import (
	"fmt"
	"strings"
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
		return fmt.Sprintf("диапазон %s..%s пропущен: доска либо код с ID задачи не из Backlog",
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
		if b.sectOf(id) == "backlog" {
			return fmt.Errorf("%s ещё в Backlog у %s %q: код с её ID до взятия задачи в работу рубеж считает чужим", id, short(sha), subj)
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
