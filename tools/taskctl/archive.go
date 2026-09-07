package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// ArchRow это строка таблицы архива: ID, Задача, Тип, P, Закрыто, Ссылка.
type ArchRow struct {
	LineIdx int
	Cells   []string
	ID      string
	Num     int
}

type Archive struct {
	Path  string
	Lines []string
	Rows  []*ArchRow
}

func LoadArchive(path string) (*Archive, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	a := &Archive{Path: path, Lines: strings.Split(string(data), "\n")}
	table := 0 // 0 до таблицы, 1 после шапки, 2 после разделителя
	for i, ln := range a.Lines {
		trimmed := strings.TrimSpace(ln)
		if !strings.HasPrefix(trimmed, "|") {
			continue
		}
		switch table {
		case 0:
			table = 1
		case 1:
			if !strings.HasPrefix(trimmed, "|-") {
				return nil, fmt.Errorf("%s:%d: ожидался разделитель таблицы", path, i+1)
			}
			table = 2
		default:
			cells, err := splitCells(ln)
			if err != nil {
				return nil, fmt.Errorf("%s:%d: %v", path, i+1, err)
			}
			if len(cells) != 6 {
				return nil, fmt.Errorf("%s:%d: ожидалось 6 колонок, получилось %d", path, i+1, len(cells))
			}
			m := idRe.FindStringSubmatch(cells[0])
			if m == nil {
				return nil, fmt.Errorf("%s:%d: не разобран ID %q", path, i+1, cells[0])
			}
			num, _ := strconv.Atoi(m[2])
			a.Rows = append(a.Rows, &ArchRow{LineIdx: i, Cells: cells, ID: cells[0], Num: num})
		}
	}
	return a, nil
}

func (a *Archive) has(id string) bool {
	return a.find(id) != nil
}

func (a *Archive) find(id string) *ArchRow {
	for _, r := range a.Rows {
		if r.ID == id {
			return r
		}
	}
	return nil
}

// archiveAfterNote стоит на месте направления «после» у архивной строки:
// close снимает маркер и в архив его не переносит (TestCloseStripsDeps), а
// восстановить его из текстов доски и архива нечем. Обратное направление
// («кого держит») считается по доске и у архивной строки живёт: маркеры
// `[после <ID>]` на доске целят и в закрытые задачи.
const archiveAfterNote = "неизвестно (закрытие снимает маркер «после», в архив он не переезжает)"

// archiveTaskFile ищет постановку закрытой задачи. Закрытие уносит файл в
// docs/tasks/archive/<год>/, год берётся из даты закрытия; файл, закрытый до
// этого порядка, лежит на прежнем месте, и оно проверяется вторым. Путь
// отдаётся от корня проекта, как у show для живой строки.
func archiveTaskFile(root, id, closed string) (string, bool) {
	var rels []string
	if len(closed) >= 4 {
		rels = append(rels, filepath.Join("docs", "tasks", "archive", closed[:4], id+".md"))
	}
	rels = append(rels, filepath.Join("docs", "tasks", id+".md"))
	for _, rel := range rels {
		if _, err := os.Stat(filepath.Join(root, rel)); err == nil {
			return filepath.ToSlash(rel), true
		}
	}
	return "", false
}

// appendRow дописывает строку в конец файла архива (архив append-only,
// таблица кончается вместе с файлом).
func appendArchiveRow(path string, cells []string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	s := string(data)
	if !strings.HasSuffix(s, "\n") {
		s += "\n"
	}
	s += "| " + strings.Join(cells, " | ") + " |\n"
	return os.WriteFile(path, []byte(s), 0o644)
}
