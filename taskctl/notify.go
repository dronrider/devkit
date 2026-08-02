package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// notifyScript ищет hooks/notify.py: DEVKIT_HOME, сам корень проекта (когда
// доска и есть devkit), соседний клон devkit, ~/projects/devkit. Порядок тот
// же, что у harnessDir в agentctl/harness.go; общего пакета у taskctl,
// shipctl и agentctl нет, заводить его ради одной функции не стали (DK-063).
func notifyScript(root string) (string, error) {
	var cands []string
	if v := os.Getenv("DEVKIT_HOME"); v != "" {
		cands = append(cands, filepath.Join(v, "hooks", "notify.py"))
	}
	cands = append(cands,
		filepath.Join(root, "hooks", "notify.py"),
		filepath.Join(filepath.Dir(root), "devkit", "hooks", "notify.py"))
	if home, err := os.UserHomeDir(); err == nil {
		cands = append(cands, filepath.Join(home, "projects", "devkit", "hooks", "notify.py"))
	}
	for _, c := range cands {
		if fi, err := os.Stat(c); err == nil && !fi.IsDir() {
			return c, nil
		}
	}
	return "", fmt.Errorf("hooks/notify.py не нашёлся, искал: %s; указать: DEVKIT_HOME=<путь к devkit>",
		strings.Join(cands, ", "))
}

// notify зовёт уведомитель громко: задача доехала до Check или встала на
// блокере, поводы, ради которых человек подходит к машине (RULES.board.md,
// «Ветки, ревью и деплой» п. 8). Move из-за непосланного уведомления падать
// не должен, поэтому ошибка возвращается строкой-припиской к сообщению
// команды, а не всплывает наверх: молчать про непосланное нельзя, но и
// держать саму доску несдвинутой из-за этого тоже.
func notify(root, title, body string) string {
	script, err := notifyScript(root)
	if err != nil {
		return "\nуведомление не отправлено: " + err.Error()
	}
	cmd := exec.Command("python3", script, title, body)
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Sprintf("\nуведомление не отправлено: %v (%s)", err, strings.TrimSpace(string(out)))
	}
	return ""
}
