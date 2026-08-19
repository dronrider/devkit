package main

import (
	"regexp"

	"github.com/dronrider/devkit/internal/sessions"
)

// Привязка разговора к задаче по факту работы (POC ветки poc-chat): движение
// кода это работа над задачей ровно так же, как движение строки доски, и
// сессия, заведшая ветку или слившая её, называет себя в реестре сама.

var touchCmds = map[string]bool{
	"start": true, "merge": true, "smoke": true, "ship": true, "revert": true, "code": true,
}

var touchIDRe = regexp.MustCompile(`^[A-Za-z]{2,10}-\d{1,6}$`)

func touchWork(args []string) {
	if len(args) == 0 || !touchCmds[args[0]] {
		return
	}
	for _, a := range args[1:] {
		if touchIDRe.MatchString(a) {
			sessions.Touch(a, "shipctl "+args[0])
			return
		}
	}
}
