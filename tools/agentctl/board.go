package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Парсер тот же, что в shipctl: нарочно терпимый и только читающий,
// инварианты доски держит taskctl lint. agentctl нужны тип, цена и
// разбивка ранга.
var sectPrefixes = []string{"## In progress", "## Check", "## Backlog", "## Blocked"}

type row struct{ ID, Title, Type, Rank, Cost string }

// findRoot перед подъёмом смотрит редирект корп-контура: в корп-клоне доска
// лежит в боковой директории, и её называет ключ devkit.local (corp.go).
func findRoot(start string) (string, error) {
	if local := corpLocal(start); local != "" {
		return local, nil
	}
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
			return "", corpRootErr(start)
		}
		dir = parent
	}
}

func loadRows(root string) ([]row, error) {
	data, err := os.ReadFile(filepath.Join(root, "docs", "TASKS.md"))
	if err != nil {
		return nil, err
	}
	var rows []row
	in, table := false, 0
	for _, ln := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(ln, "## ") {
			in, table = false, 0
			for _, p := range sectPrefixes {
				if strings.HasPrefix(ln, p) {
					in = true
				}
			}
			continue
		}
		t := strings.TrimSpace(ln)
		if !in || !strings.HasPrefix(t, "|") {
			continue
		}
		table++
		if table <= 2 { // шапка таблицы и разделитель
			continue
		}
		cells := strings.Split(strings.Trim(t, "|"), "|")
		if len(cells) < 5 {
			continue
		}
		r := row{
			ID:    strings.TrimSpace(cells[0]),
			Title: strings.TrimSpace(cells[1]),
			Type:  strings.TrimSpace(cells[2]),
			Rank:  strings.TrimSpace(cells[4]),
			Cost:  "-",
		}
		// Колонка «Цена» есть только в семиколоночных досках, в старом
		// формате шестая ячейка это ссылка.
		if len(cells) >= 7 {
			r.Cost = strings.TrimSpace(cells[5])
		}
		rows = append(rows, r)
	}
	return rows, nil
}

func rowOf(rows []row, id string) *row {
	for i := range rows {
		if rows[i].ID == id {
			return &rows[i]
		}
	}
	return nil
}

// uncertainty достаёт третье слагаемое из ячейки ранга вида «8 (0+3+1+0+4)».
// Нечитаемая разбивка (старая доска, прочерк) даёт -1: неизвестность, а не ноль.
// После пяти слагаемых в скобке идёт хвост поправок через запятую
// («40 (25+8+1+0+4, S+2, от DK-473)», DK-428). Слагаемые считаются до первой
// запятой, иначе последнее из них склеивается с первой поправкой и вся ячейка
// становится нечитаемой.
func uncertainty(rank string) int {
	i := strings.Index(rank, "(")
	j := strings.Index(rank, ")")
	if i < 0 || j < i {
		return -1
	}
	inner := rank[i+1 : j]
	if c := strings.Index(inner, ","); c >= 0 {
		inner = inner[:c]
	}
	parts := strings.Split(inner, "+")
	if len(parts) != 5 {
		return -1
	}
	n, err := strconv.Atoi(strings.TrimSpace(parts[2]))
	if err != nil {
		return -1
	}
	return n
}
