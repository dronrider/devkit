package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Раздел «Выкат» в файле задачи держит коммиты, которые shipctl слил под этим
// ID. До него связь задачи с её коммитами выводилась поиском ID в subject
// (codeCommits, taskCommits), и ветка, где ID в сообщения не попал, пропадала
// разом из очереди, из состава поезда и из отката. Пропадала молча: пустой
// ответ там неотличим от честного «кода нет». Запись эту связь фиксирует,
// поиск по subject остаётся для задач, слитых до неё или руками мимо shipctl.
const mergedSection = "## Выкат"

func taskFilePath(root, id string) string {
	return filepath.Join(root, "docs", "tasks", id+".md")
}

// mergedShas возвращает коммиты из раздела «Выкат» файла задачи. Нет файла
// или раздела значит «записи нет», и это не ошибка: так живут все задачи,
// слитые до появления раздела.
func mergedShas(root, id string) ([]string, error) {
	data, err := os.ReadFile(taskFilePath(root, id))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var shas []string
	for _, ln := range sectionLines(string(data), mergedSection) {
		t := strings.TrimSpace(ln)
		if !strings.HasPrefix(t, "- ") {
			continue
		}
		_, list, ok := strings.Cut(t, ":")
		if !ok {
			continue
		}
		for _, part := range strings.Split(list, ",") {
			if s := strings.TrimSpace(part); isSha(s) {
				shas = append(shas, s)
			}
		}
	}
	return shas, nil
}

// isSha отсеивает прозу в строке записи: коммит это семь и больше знаков
// шестнадцатеричного числа.
func isSha(s string) bool {
	if len(s) < 7 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// inRecord сверяет полный sha из лога с записанным сокращённым.
func inRecord(rec []string, sha string) bool {
	for _, r := range rec {
		if strings.HasPrefix(sha, r) || strings.HasPrefix(r, sha) {
			return true
		}
	}
	return false
}

// recordMerge дописывает в файл задачи коммиты слитого диапазона и коммитит
// правку. Файла может не быть (однострочная задача из бэклога), тогда его
// заводит taskctl вместе со ссылкой в строке; отказ taskctl не роняет
// слияние, файл тогда пишется голым заголовком.
func recordMerge(root, id string, shas []string) (string, error) {
	if len(shas) == 0 {
		return "", nil
	}
	path := taskFilePath(root, id)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		exec.Command("taskctl", "-C", root, "file", id).Run()
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return "", err
		}
		data = []byte("# " + id + "\n")
	}
	line := "- " + time.Now().Format("2006-01-02") + " слито: " + strings.Join(shas, ", ")
	if err := os.WriteFile(path, []byte(appendToSection(string(data), line)), 0o644); err != nil {
		return "", err
	}
	if _, err := git(root, "add", "--", "docs/TASKS.md", "docs/tasks/"+id+".md"); err != nil {
		return "", err
	}
	if _, err := git(root, "commit", "-m", fmt.Sprintf("docs(tasks): %s коммиты слияния", id),
		"--", "docs/TASKS.md", "docs/tasks/"+id+".md"); err != nil {
		return "", err
	}
	return git(root, "rev-parse", "--short", "HEAD")
}

// appendToSection дописывает строку в конец раздела «Выкат», заводя сам
// раздел, если его нет. Круг доработки после возврата из Check это второе
// слияние той же задачи, и его коммиты встают следующей строкой, а не
// затирают прежние.
func appendToSection(doc, line string) string {
	lines := strings.Split(strings.TrimRight(doc, "\n"), "\n")
	mask := fenceMask(lines)
	start := -1
	for i, ln := range lines {
		if !mask[i] && strings.HasPrefix(ln, mergedSection) {
			start = i
			break
		}
	}
	if start < 0 {
		return strings.Join(lines, "\n") + "\n\n" + mergedSection + "\n\n" + line + "\n"
	}
	end := len(lines)
	for i := start + 1; i < len(lines); i++ {
		if !mask[i] && strings.HasPrefix(lines[i], "## ") {
			end = i
			break
		}
	}
	for end > start+1 && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}
	out := append([]string{}, lines[:end]...)
	out = append(out, line)
	out = append(out, lines[end:]...)
	return strings.Join(out, "\n") + "\n"
}
