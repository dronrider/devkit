package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/dronrider/devkit/internal/taskhead"
)

// Подъём головы задачи (DK-931): `taskctl run <ID>` ведёт лестницу носителей
// из internal/taskhead. Сама лестница, замок и ключи профиля живут там, а тут
// только флаги, поиск дерева devkit и код выхода.

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
	harness := taskhead.HarnessName(o.harness)
	adopt, err := taskhead.AdoptWait()
	if err != nil {
		return taskhead.Request{}, err
	}
	return taskhead.Request{ID: id, Root: root, Project: filepath.Base(root), Home: home, Devkit: dk,
		Harness: harness, Model: o.model, Order: o.order, Again: o.again, Hidden: o.hidden, Adopt: adopt,
		Pick: func() (string, error) { return runPick(root, id, harness) }}, nil
}

// runPickWait ограничивает вопрос вердикту: подъём не должен висеть на agentctl.
const runPickWait = 30 * time.Second

// runPick спрашивает модель головы у `agentctl pick` так же, как исполнителя
// назначает диспетчер: с --record, чтобы этап разработки лёг в запись, и с
// --goal, когда файл задачи ссылается на цель. Харнес вердикту называется тем
// же, каким поднимается голова: модель у каждого харнеса своя. Отказ ложится
// строкой в журнал .devkit/log, а подъём идёт дальше без модели.
func runPick(root, id, harness string) (string, error) {
	model, err := askPick(root, id, harness)
	if err != nil {
		logLine(root, "run "+id+" без модели: "+strings.Join(strings.Fields(err.Error()), " "), 1)
	}
	return model, err
}

func askPick(root, id, harness string) (string, error) {
	bin, err := exec.LookPath("agentctl")
	if err != nil {
		return "", fmt.Errorf("agentctl pick не позвать: agentctl не нашёлся в PATH")
	}
	args := []string{"pick", id, "--record"}
	if goal := taskGoal(root, id); goal != "" {
		args = append(args, "--goal", goal)
	}
	ctx, cancel := context.WithTimeout(context.Background(), runPickWait)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), taskhead.HarnessEnv+"="+harness)
	out, err := cmd.Output()
	if err != nil {
		said := ""
		if ee, ok := err.(*exec.ExitError); ok {
			said = strings.TrimSpace(string(ee.Stderr))
		}
		return "", fmt.Errorf("agentctl pick %s отказал: %v %s", id, err, said)
	}
	for _, ln := range strings.Split(string(out), "\n") {
		if m, ok := strings.CutPrefix(strings.TrimSpace(ln), "model:"); ok && strings.TrimSpace(m) != "" {
			return strings.TrimSpace(m), nil
		}
	}
	return "", fmt.Errorf("agentctl pick %s модели не назвал", id)
}

// taskGoal это путь файла цели от корня, когда шапка файла задачи несёт строку
// «Цель: ...». Пусто, когда строки нет или файла цели нет на месте.
func taskGoal(root, id string) string {
	data, err := os.ReadFile(taskFilePath(root, id))
	if err != nil {
		return ""
	}
	for _, ln := range strings.Split(string(data), "\n") {
		s := strings.TrimSpace(ln)
		if strings.HasPrefix(s, "## ") {
			return ""
		}
		if !strings.HasPrefix(s, "Цель:") {
			continue
		}
		key := goalKey(s)
		if key == "" {
			return ""
		}
		rel := filepath.Join("docs", "tasks", key+".md")
		if _, err := os.Stat(filepath.Join(root, rel)); err != nil {
			return ""
		}
		return rel
	}
	return ""
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
