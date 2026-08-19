package main

import (
	"path/filepath"
	"regexp"
	"strings"

	"github.com/dronrider/devkit/internal/sessions"
)

// Привязка разговора к цели по факту работы (POC ветки poc-chat). Виток цели
// не двигает её строку и потому мимо отметок taskctl проходит целиком: назвать
// себя работой цели он может только там, где сам её называет, то есть ключом
// --goal у вердикта и у замера бюджета.

var touchGoalRe = regexp.MustCompile(`([A-Za-z]{2,10}-\d{1,6})`)

var touchCmds = map[string]bool{"pick": true, "run": true, "spend": true, "budget": true}

// touchWork отмечает работу сессии над целью: ID берётся из имени файла цели,
// названного ключом --goal.
func touchWork(args []string) {
	if len(args) == 0 || !touchCmds[args[0]] {
		return
	}
	for i, a := range args {
		val := ""
		if v, ok := strings.CutPrefix(a, "--goal="); ok {
			val = v
		} else if a == "--goal" && i+1 < len(args) {
			val = args[i+1]
		}
		if val == "" {
			continue
		}
		if m := touchGoalRe.FindString(filepath.Base(val)); m != "" {
			sessions.Touch(m, "agentctl "+args[0]+" --goal")
		}
		return
	}
}
