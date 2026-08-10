package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Доска берётся подпроцессом taskctl list --json, а не своим разбором
// markdown: правда о формате одна и живёт в утилите (LLD DK-112, «Откуда
// сервер берёт данные»). Честная ошибка «taskctl не нашёлся» полезнее
// выживания с собственным разбором, который молча устареет.

// taskctlBin подменяется тестами, живой сервер зовёт бинарь из PATH.
var taskctlBin = "taskctl"

func taskctlMissing() string {
	if _, err := exec.LookPath(taskctlBin); err != nil {
		return "taskctl не нашёлся в PATH: доски не читаются, поставить бинари: devkitctl update"
	}
	return ""
}

// boardJSON отдаёт доску проекта как есть, байтами ответа taskctl.
func boardJSON(dir string) (json.RawMessage, error) {
	out, err := exec.Command(taskctlBin, "list", "--json", "-C", dir).Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && len(ee.Stderr) > 0 {
			return nil, fmt.Errorf("taskctl list --json в %s: %s", dir, strings.TrimSpace(string(ee.Stderr)))
		}
		if m := taskctlMissing(); m != "" {
			return nil, errors.New(m)
		}
		return nil, fmt.Errorf("taskctl list --json в %s: %v", dir, err)
	}
	return json.RawMessage(bytes.TrimSpace(out)), nil
}

// boardView это минимум, который сервер сам вычитывает из ответа taskctl:
// префикс для привязки tmux-сессий и счётчики секций для списка проектов.
type boardView struct {
	Prefix   string `json:"prefix"`
	Sections []struct {
		Key  string            `json:"key"`
		Rows []json.RawMessage `json:"rows"`
	} `json:"sections"`
}

func parseBoardView(raw json.RawMessage) (boardView, error) {
	var v boardView
	err := json.Unmarshal(raw, &v)
	return v, err
}

// Work это живая работа проекта: tmux-сессия goal-*/task-* либо цель из
// реестра ~/.devkit/goals (цикл, ведущийся снаружи, без своей tmux-сессии).
type Work struct {
	ID   string `json:"id"`
	Kind string `json:"kind"` // goal | task
	Via  string `json:"via"`  // tmux | registry
}

// liveWorks собирает работы проекта. tmux-сессии на машине общие, к проекту
// они привязываются префиксом ID с его доски; доска без префикса работ из
// tmux не получает. Реестр целей привязан корнем и добирает цели, которые
// ведёт живой чат без tmux-сессии.
func liveWorks(projectPath, prefix, home string) []Work {
	// Пустой список, а не null: клиент и smoke-сценарий различают «работ нет»
	// и «поля нет».
	works := []Work{}
	seen := map[string]bool{}
	if prefix != "" {
		for _, name := range tmuxSessions() {
			for _, kind := range []string{"goal", "task"} {
				id, ok := strings.CutPrefix(name, kind+"-")
				if !ok || !strings.HasPrefix(id, prefix+"-") {
					continue
				}
				works = append(works, Work{ID: id, Kind: kind, Via: "tmux"})
				seen[kind+"-"+id] = true
			}
		}
	}
	for _, path := range globSorted(filepath.Join(home, ".devkit", "goals", "*.watch")) {
		entry := readEntry(path)
		goal, root := entry["goal"], entry["root"]
		if goal == "" || root == "" || filepath.Clean(root) != filepath.Clean(projectPath) {
			continue
		}
		if seen["goal-"+goal] {
			continue
		}
		works = append(works, Work{ID: goal, Kind: "goal", Via: "registry"})
	}
	return works
}

func tmuxSessions() []string {
	out, err := exec.Command("tmux", "ls", "-F", "#{session_name}").Output()
	if err != nil {
		return nil
	}
	return strings.Fields(string(out))
}

func globSorted(pattern string) []string {
	paths, _ := filepath.Glob(pattern)
	return paths
}

// readEntry читает файл «ключ = значение», как записи реестра целей.
func readEntry(path string) map[string]string {
	data := map[string]string{}
	text, err := os.ReadFile(path)
	if err != nil {
		return data
	}
	for _, ln := range strings.Split(string(text), "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" || strings.HasPrefix(ln, "#") {
			continue
		}
		if k, v, ok := strings.Cut(ln, "="); ok {
			data[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	return data
}
