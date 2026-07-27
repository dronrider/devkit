package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Ключи секций и заголовки те же, что в taskctl. Парсер здесь нарочно
// терпимый и только читающий: инварианты доски держит taskctl lint, а
// shipctl нужны лишь ID, заголовок и секция.
var sectByPrefix = []struct{ prefix, key string }{
	{"## In progress", "in-progress"},
	{"## Check", "check"},
	{"## Backlog", "backlog"},
	{"## Blocked", "blocked"},
}

type row struct{ ID, Title, Type, Cost string }

type board struct {
	sects map[string][]row
}

func findRoot(start string) (string, error) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "docs", "TASKS.md")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("не нашёл docs/TASKS.md вверх от %s", start)
		}
		dir = parent
	}
}

func loadBoard(root string) (*board, error) {
	data, err := os.ReadFile(filepath.Join(root, "docs", "TASKS.md"))
	if err != nil {
		return nil, err
	}
	b := &board{sects: map[string][]row{}}
	sect, table := "", 0
	for _, ln := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(ln, "## ") {
			sect, table = "", 0
			for _, sp := range sectByPrefix {
				if strings.HasPrefix(ln, sp.prefix) {
					sect = sp.key
				}
			}
			continue
		}
		t := strings.TrimSpace(ln)
		if sect == "" || !strings.HasPrefix(t, "|") {
			continue
		}
		table++
		if table <= 2 { // шапка таблицы и разделитель
			continue
		}
		cells := strings.Split(strings.Trim(t, "|"), "|")
		if len(cells) < 2 {
			continue
		}
		r := row{
			ID:    strings.TrimSpace(cells[0]),
			Title: strings.TrimSpace(cells[1]),
		}
		if len(cells) > 2 {
			r.Type = strings.TrimSpace(cells[2])
		}
		// Колонка «Цена» есть только в семиколоночных досках, в старом
		// формате шестая ячейка это ссылка.
		if len(cells) >= 7 {
			r.Cost = strings.TrimSpace(cells[5])
		}
		b.sects[sect] = append(b.sects[sect], r)
	}
	return b, nil
}

// sectOf возвращает секцию задачи, пустую строку если её нет на доске.
func (b *board) sectOf(id string) string {
	for sect, rows := range b.sects {
		for _, r := range rows {
			if r.ID == id {
				return sect
			}
		}
	}
	return ""
}

// rowOf возвращает строку задачи, nil если её нет на доске.
func (b *board) rowOf(id string) *row {
	for _, rows := range b.sects {
		for i := range rows {
			if rows[i].ID == id {
				return &rows[i]
			}
		}
	}
	return nil
}

// openReviewNotes возвращает замечания раздела «Ревью» файла задачи без
// зафиксированного исхода: по RULES.board.md у каждой строки должен быть итог
// «исправлено» либо «отклонено» с причиной.
func openReviewNotes(root, id string) ([]string, error) {
	data, err := os.ReadFile(filepath.Join(root, "docs", "tasks", id+".md"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var open []string
	in := false
	for _, ln := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(ln, "## ") {
			in = strings.HasPrefix(ln, "## Ревью")
			continue
		}
		t := strings.TrimSpace(ln)
		if !in || (!strings.HasPrefix(t, "- ") && !strings.HasPrefix(t, "* ")) {
			continue
		}
		low := strings.ToLower(t)
		if !strings.Contains(low, "исправлено") && !strings.Contains(low, "отклонено") {
			open = append(open, t)
		}
	}
	return open, nil
}
