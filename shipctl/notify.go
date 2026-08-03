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
// же, что у harnessDir в agentctl/harness.go и у одноимённой функции в
// taskctl; общего пакета у отдельных go-модулей devkit нет, заводить его ради
// одной функции не стали (DK-063).
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

// notify зовёт уведомитель громко: выкат упал, и прод в этот момент может
// быть сломан, самый громкий из трёх поводов (RULES.board.md, «Ветки, ревью
// и деплой» п. 8). Ошибка отправки не должна прятать саму ошибку выката,
// поэтому возвращается строкой-припиской к сообщению об ошибке, а не
// отдельным error: молчать про непосланное уведомление нельзя.
func notify(root, title, body string) string {
	script, err := notifyScript(root)
	if err != nil {
		return "\nуведомление не отправлено: " + err.Error()
	}
	cmd := exec.Command("python3", script, title, body)
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Sprintf("\nуведомление не отправлено: %v (%s)", err, strings.TrimSpace(string(out)))
	}
	// Уведомитель отрабатывает и без баннера (корень в песочнице): причину
	// пропуска он печатает сам, приписка доносит её до вывода команды.
	if note := strings.TrimSpace(string(out)); note != "" {
		return "\n" + note
	}
	return ""
}
