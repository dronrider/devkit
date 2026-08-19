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
func touchWork(args []string) {
	if len(args) == 0 || !touchCmds[args[0]] {
		return
	}
	for _, a := range args[1:] {
		if touchIDRe.MatchString(a) {
			sessions.Touch(a, "taskctl "+strings.Join(args[:min(2, len(args))], " "))
			return
		}
	}
}
