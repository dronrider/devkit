package main

import (
	"fmt"
	"os"
	"strings"
)

func cmdFileTail(root, id, what string) (string, error) {
	if !idRe.MatchString(id) {
		return "", fmt.Errorf("неверный ID %q", id)
	}
	what = strings.TrimSpace(what)
	if what == "" {
		return "", fmt.Errorf("пустой хвост")
	}
	path := taskFileAbs(root, id)
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	start, end := -1, len(lines)
	for i, line := range lines {
		if !strings.HasPrefix(line, "## ") {
			continue
		}
		if start >= 0 {
			end = i
			break
		}
		if strings.TrimSpace(line) == stageSection {
			start = i
		}
	}
	item := fmt.Sprintf("- Хвост: %s, снят стопом конвейера, %s.", what, timeNow().Format(draftDateLayout))
	if start < 0 {
		if len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) != "" {
			lines = append(lines, "")
		}
		lines = append(lines, stageSection, "", item)
	} else {
		at := end
		for at > start+1 && strings.TrimSpace(lines[at-1]) == "" {
			at--
		}
		lines = append(lines[:at], append([]string{item}, lines[at:]...)...)
	}
	if err = os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0644); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s: хвост записан", id), nil
}
