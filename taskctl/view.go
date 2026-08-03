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

// ensureTaskFile создаёт файл задачи docs/tasks/<ID>.md со строкой-заголовком,
// если его ещё нет, и отвечает, был ли он создан. Общая часть между cmdFile и
// cmdReviewAdd: там файл заводится сам, не будь у задачи файла, но ссылку в
// строке доски эти две команды чинят по-разному (см. cmdReviewAdd).
func ensureTaskFile(root, id string, row *Row) (bool, error) {
	abs := filepath.Join(root, "docs", "tasks", id+".md")
	if _, err := os.Stat(abs); err == nil {
		return false, nil
	} else if !os.IsNotExist(err) {
		return false, err
	}
	base, deps, _, _ := splitTitle(row.Title)
	title := joinTitle(base, deps, "", "")
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return false, err
	}
	if err := os.WriteFile(abs, []byte(fmt.Sprintf("# %s: %s\n", id, title)), 0o644); err != nil {
		return false, err
	}
	return true, nil
}

// cmdFile создаёт файл задачи docs/tasks/<ID>.md со строкой-заголовком и
// ставит ссылку на него в строку доски. Обе части идемпотентны: существующий
// файл не трогается, совпавшая ссылка не переписывается, а если на входе уже
// есть и то и другое, команда об этом сообщает и выходит с нулём, не пытаясь
// закоммитить пустое изменение (при -m/--push второй подряд вызов не создаёт
// нового коммита).
func cmdFile(root, id string, c CommitOpts) (string, error) {
	if err := c.validate(); err != nil {
		return "", err
	}
	if err := boardGuard(root, "file"); err != nil {
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
	var done []string
	created, err := ensureTaskFile(root, id, row)
	if err != nil {
		return "", err
	}
	if created {
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
		return fmt.Sprintf("у %s уже есть и файл, и ссылка на него", id), nil
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
	clean := boardClean(root)
	var times map[int]int64
	if clean {
		times = boardTimes(root)
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
			out = append(out, rowNotes(root, key, r, times, clean)...)
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
		drafts, err := loadDrafts(root)
		if err != nil {
			return "", err
		}
		if line := draftsLine(drafts); line != "" {
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n"), nil
}

// rowNotes собирает пометки строки, которые list и show печатают отдельной
// строкой под самой строкой таблицы: они выводятся на лету и в саму доску
// не пишутся (RULES.board.md запрещает заводить под них колонку). Метки
// Check печатаются только в её секции, возраст, когда его удалось посчитать,
// в любой; нет ни того, ни другого, значит nil, и вывод не меняется вовсе.
func rowNotes(root, sect string, r *Row, times map[int]int64, clean bool) []string {
	var notes []string
	if sect == SectCheck {
		if m := checkMarkLabel(root, r.ID); m != "" {
			notes = append(notes, m)
		}
	}
	if a := ageLabel(times, r.LineIdx, clean); a != "" {
		notes = append(notes, a)
	}
	if len(notes) == 0 {
		return nil
	}
	return []string{"  " + strings.Join(notes, ", ")}
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
		out = append(out, rowNotes(root, row.Sect, row, showTimes(root), true)...)
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
	drafts, err := loadDrafts(root)
	if err != nil {
		return "", err
	}
	if d := findDraft(drafts, id); d != nil {
		text, err := os.ReadFile(d.Path)
		if err != nil {
			return "", err
		}
		head := fmt.Sprintf("%s черновик (записан %s), docs/tasks/drafts/%s.md, оформить: taskctl add --id %s ...",
			id, ageWords(d.Age), id, id)
		return head + "\n" + strings.TrimRight(string(text), "\n"), nil
	}
	return "", fmt.Errorf("%s нет ни на доске, ни в архиве, ни в черновиках", id)
}

// showTimes считает даты строк для одной задачи тем же путём, что и list:
// blame один на файл, поэтому отдельной дешёвой ветки под show не нужно.
// Грязная доска гасит возраст там же, где и в list, через пустую карту.
func showTimes(root string) map[int]int64 {
	if !boardClean(root) {
		return nil
	}
	return boardTimes(root)
}
