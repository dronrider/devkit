package main

import (
	"regexp"
	"strings"

	"github.com/dronrider/devkit/internal/sessions"
)

// Привязка разговора к задаче по факту работы (POC ветки poc-chat). Сессия
// харнеса, двинувшая строку доски, тем самым сказала о себе, что работает над
// задачей: угадывать её по первой реплике больше не нужно, а привязок у одной
// сессии столько, скольких задач она коснулась. Строку кладёт сама утилита,
// потому что только у неё под рукой и ID задачи, и окружение сессии.

// touchCmds это команды, которые считаются работой над задачей. Чтение
// (list, show, batch) сюда не идёт: спросить про строку это не работать над
// ней, и разговор бы привязывался к каждой задаче, о которой сессия справилась.
var touchCmds = map[string]bool{
	"add": true, "move": true, "close": true, "ask": true, "fail": true,
	"set": true, "file": true, "review": true, "dep": true, "progress": true,
}

var touchIDRe = regexp.MustCompile(`^[A-Za-z]{2,10}-\d{1,6}$`)

// touchWork отмечает работу сессии над задачей: ID берётся первым похожим
// словом команды, потому что стоит он у разных подкоманд в разных местах.
// Конец работы отмечается той же дорогой в обратную сторону, разбор в
// touchDone.
func touchWork(args []string) {
	if len(args) == 0 || !touchCmds[args[0]] {
		return
	}
	for i, a := range args[1:] {
		if !touchIDRe.MatchString(a) {
			continue
		}
		why := "taskctl " + strings.Join(args[:min(2, len(args))], " ")
		if touchDone(args, i+1) {
			sessions.Release(a, why)
			return
		}
		sessions.Touch(a, why)
		return
	}
}

// touchDone отвечает, кончилась ли этой командой работа сессии над задачей.
// Кончилась она у закрытия строки и у перевода её из In progress: дальше по
// строке никто не работает, и держать за ней «Стоп» вместо кнопки запуска
// нечем (DoD DK-716). Отвязка при этом своя у каждой сессии: соседних задач
// того же разговора запись не трогает, а чужие сессии снимают себя сами тем же
// порядком.
//
// Перевод обратно в In progress работой и остаётся: строку двигает тот, кто её
// берёт, и отвязывать его от собственной задачи было бы ровно наоборот.
func touchDone(args []string, at int) bool {
	switch args[0] {
	case "close":
		return true
	case "move":
		// Статус стоит следующим словом за ID, а не третьим аргументом:
		// ключи вроде --dry-run встают где угодно, и счёт по позиции читал бы
		// статусом первый попавшийся флаг.
		for _, a := range args[at+1:] {
			if strings.HasPrefix(a, "-") {
				continue
			}
			return normalizeStatus(a) != SectInProgress
		}
	}
	return false
}
