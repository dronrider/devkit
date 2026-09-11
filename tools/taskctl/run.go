package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/dronrider/devkit/internal/taskhead"
)

// Подъём головы задачи (DK-931): `taskctl run <ID>` ведёт лестницу носителей
// из internal/taskhead. Сама лестница, замок и ключи профиля живут там, а тут
// только флаги, поиск дерева devkit и код выхода.

// runHarnessEnv называет профиль харнеса, когда флага нет. Та же переменная
// называет активный харнес у agentctl.
const runHarnessEnv = "DEVKIT_HARNESS"

const runDefaultHarness = "claude-code"

type runOpts struct {
	harness, model, order, again string
	hidden                       bool
}

// runRequest собирает заказ подъёма из флагов и окружения.
func runRequest(root, home, id string, o runOpts) (taskhead.Request, error) {
	id = strings.ToUpper(strings.TrimSpace(id))
	if !touchIDRe.MatchString(id) {
		return taskhead.Request{}, fmt.Errorf("%q не похож на ID задачи", id)
	}
	dk := taskhead.Devkit(root, home)
	if dk == "" {
		return taskhead.Request{}, fmt.Errorf("дерево devkit с %s не нашлось ни в DEVKIT_HOME, ни в "+
			".devkit/devkit проекта, ни рядом с ним, ни в ~/projects/devkit; указать: DEVKIT_HOME=<путь к devkit>",
			taskhead.TaskRunRel)
	}
	harness := o.harness
	if harness == "" {
		harness = strings.TrimSpace(os.Getenv(runHarnessEnv))
	}
	if harness == "" {
		harness = runDefaultHarness
	}
	var adopt time.Duration
	if v := strings.TrimSpace(os.Getenv(taskhead.AdoptEnv)); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return taskhead.Request{}, fmt.Errorf("%s ждёт число секунд, а пришло %q", taskhead.AdoptEnv, v)
		}
		adopt = time.Duration(n) * time.Second
	}
	return taskhead.Request{ID: id, Root: root, Project: filepath.Base(root), Home: home, Devkit: dk,
		Harness: harness, Model: o.model, Order: o.order, Again: o.again, Hidden: o.hidden, Adopt: adopt}, nil
}

// cmdRun поднимает голову и отдаёт вывод с кодом выхода. Ошибка это сломанная
// раскладка, у неё код taskhead.CodeSetup.
func cmdRun(root, id string, o runOpts) (string, int, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", taskhead.CodeSetup, err
	}
	q, err := runRequest(root, home, id, o)
	if err != nil {
		return "", taskhead.CodeSetup, err
	}
	res, err := taskhead.Raise(q)
	if err != nil {
		return "", taskhead.CodeSetup, err
	}
	out := strings.Join(res.Lines, "\n")
	return out + "\n" + fmt.Sprintf("ступень: %s, код %d", res.Rung, res.Code) + "\n", res.Code, nil
}
