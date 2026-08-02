package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Сколько строк Backlog показывает list без аргумента. Верх отсортирован по R,
// хвост редко нужен, а доска на десятки задач съедает контекст сессии.
const listBacklogTop = 10

var sectTitles = map[string]string{
	SectInProgress: "In progress",
	SectCheck:      "Check",
	SectBacklog:    "Backlog",
	SectBlocked:    "Blocked",
}

// cmdFile создаёт файл задачи docs/tasks/<ID>.md со строкой-заголовком и
// ставит ссылку на него в строку доски. Существующий файл не трогается,
// команда тогда только чинит ссылку.
func cmdFile(root, id string, c CommitOpts) (string, error) {
	if err := c.validate(); err != nil {
		return "", err
	}
	b, err := LoadBoard(boardPath(root))
	if err != nil {
		return "", err
	}
	row := b.find(id)
	if row == nil {
		return "", fmt.Errorf("%s нет на доске", id)
	}
	rel := fmt.Sprintf("tasks/%s.md", id)
	abs := filepath.Join(root, "docs", rel)
	var done []string
	if _, err := os.Stat(abs); os.IsNotExist(err) {
		base, deps, _, _ := splitTitle(row.Title)
		title := joinTitle(base, deps, "", "")
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(abs, []byte(fmt.Sprintf("# %s: %s\n", id, title)), 0o644); err != nil {
			return "", err
		}
		done = append(done, fmt.Sprintf("docs/%s создан", rel))
	}
	if want := fmt.Sprintf("[%s](%s)", rel, rel); row.Link != want {
		row.Link = want
		b.Lines[row.LineIdx] = formatRow(row)
		if err := b.Save(); err != nil {
			return "", err
		}
		done = append(done, "ссылка в строке обновлена")
	}
	if len(done) == 0 {
		return "", fmt.Errorf("у %s уже есть и файл, и ссылка на него", id)
	}
	tail, err := c.apply(root, []string{filepath.Join("docs", "TASKS.md"), filepath.Join("docs", rel)})
	if err != nil {
		return "", err
	}
	return strings.Join(done, ", ") + tail, nil
}

// cmdList печатает доску по секциям без прозы; без аргумента Backlog обрезан
// до listBacklogTop строк, с аргументом секция выводится целиком.
func cmdList(root, sect string) (string, error) {
	b, err := LoadBoard(boardPath(root))
	if err != nil {
		return "", err
	}
	var out []string
	section := func(key string, limit int) {
		sec := b.Sects[key]
		head := fmt.Sprintf("%s (%d)", sectTitles[key], len(sec.Rows))
		rows := sec.Rows
		if limit > 0 && len(rows) > limit {
			head = fmt.Sprintf("%s (%d, первые %d; целиком: taskctl list %s)",
				sectTitles[key], len(rows), limit, key)
			rows = rows[:limit]
		}
		out = append(out, head)
		if len(rows) == 0 {
			out = append(out, "Нет.")
		}
		for _, r := range rows {
			out = append(out, b.Lines[r.LineIdx])
		}
	}
	if sect != "" {
		key := normalizeStatus(sect)
		if _, ok := b.Sects[key]; !ok {
			return "", fmt.Errorf("неизвестная секция %q, жду backlog / in-progress / check / blocked", sect)
		}
		section(key, 0)
	} else {
		for _, key := range []string{SectInProgress, SectCheck, SectBacklog, SectBlocked} {
			limit := 0
			if key == SectBacklog {
				limit = listBacklogTop
			}
			section(key, limit)
		}
	}
	return strings.Join(out, "\n"), nil
}

// cmdShow печатает строку задачи, её секцию и путь файла задачи; закрытые
// задачи ищутся в архиве.
func cmdShow(root, id string) (string, error) {
	b, err := LoadBoard(boardPath(root))
	if err != nil {
		return "", err
	}
	if row := b.find(id); row != nil {
		out := []string{fmt.Sprintf("%s в %s", id, row.Sect), b.Lines[row.LineIdx]}
		sides := depSides(b)
		s := sides[id]
		if s == nil {
			s = &struct{ after, blocks []string }{}
		}
		out = append(out, fmt.Sprintf("после: %s", joinOrDash(s.after)), fmt.Sprintf("держит: %s", joinOrDash(s.blocks)))
		rel := fmt.Sprintf("tasks/%s.md", id)
		if _, err := os.Stat(filepath.Join(root, "docs", rel)); err == nil {
			out = append(out, "файл задачи: docs/"+rel)
		} else {
			out = append(out, fmt.Sprintf("файла задачи нет (создаст taskctl file %s)", id))
		}
		return strings.Join(out, "\n"), nil
	}
	arch, err := LoadArchive(archivePath(root))
	if err != nil {
		return "", err
	}
	for _, r := range arch.Rows {
		if r.ID == id {
			return fmt.Sprintf("%s в архиве (закрыта %s)\n%s", id, r.Cells[4], arch.Lines[r.LineIdx]), nil
		}
	}
	return "", fmt.Errorf("%s нет ни на доске, ни в архиве", id)
}
