package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Доску trackctl только читает: зеркальная строка нужна ему ценой (из неё
// считается эстимейт), суммой ранга (из неё кормится необязательный rank) и
// секцией (её догоняет sync). Правит доску taskctl, и sync зовёт его командой,
// а не переписывает таблицу сам.
const boardPath = "docs/TASKS.md"

// Заголовки секций доски: текст тот же, что читает taskctl, порядок важен для
// префиксного разбора («## In progress» длиннее «## In»).
var boardSections = []struct{ prefix, sect string }{
	{"## In progress", sectInProgress},
	{"## Check", sectCheck},
	{"## Backlog", sectBacklog},
	{"## Blocked", sectBlocked},
}

// boardRow это разобранная строка доски: то немногое из неё, что нужно
// командам границ. Title нужен только review: он ищет по нему пометку
// сценария, take и sync заголовок не читают.
type boardRow struct {
	ID     string
	Sect   string
	Title  string
	Cost   string
	Rank   int
	Ticket string
}

// loadBoardRows читает доску и отдаёт её строки. Ключ тикета берётся из ссылки
// строки: зеркальная строка это обычная строка доски, ссылка которой ведёт на
// тикет, и другого признака у неё нет.
func loadBoardRows(root, projKey string) ([]boardRow, error) {
	data, err := os.ReadFile(filepath.Join(root, boardPath))
	if err != nil {
		return nil, err
	}
	keyRe, err := ticketRe(projKey)
	if err != nil {
		return nil, err
	}
	var rows []boardRow
	sect := ""
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "## ") {
			sect = ""
			for _, s := range boardSections {
				if strings.HasPrefix(line, s.prefix) {
					sect = s.sect
					break
				}
			}
			continue
		}
		if sect == "" || !strings.HasPrefix(strings.TrimSpace(line), "|") {
			continue
		}
		cells := tableCells(line)
		if len(cells) < 6 || cells[0] == "ID" || strings.HasPrefix(cells[0], "---") {
			continue
		}
		row := boardRow{ID: cells[0], Sect: sect, Title: cells[1], Rank: leadingInt(cells[4])}
		if len(cells) >= 7 {
			row.Cost, row.Ticket = cells[5], ticketFrom(keyRe, cells[6])
		} else {
			// Доски, заведённые до колонки «Цена»: ссылка идёт шестой.
			row.Ticket = ticketFrom(keyRe, cells[5])
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// ticketRe собирает поиск ключа тикета проекта. Границы нужны обе: без правой
// ABC-12 нашёлся бы в ABC-120, без левой в XABC-12.
func ticketRe(projKey string) (*regexp.Regexp, error) {
	if projKey == "" {
		return nil, fmt.Errorf("привязка не назвала ключ проекта, зеркальную строку не по чему опознать")
	}
	return regexp.Compile(`(?i)(^|[^0-9A-Za-z])(` + regexp.QuoteMeta(projKey) + `-[0-9]+)($|[^0-9])`)
}

func ticketFrom(re *regexp.Regexp, cell string) string {
	m := re.FindStringSubmatch(cell)
	if m == nil {
		return ""
	}
	return strings.ToUpper(m[2])
}

// tableCells режет строку таблицы на ячейки без обрамляющих труб.
func tableCells(line string) []string {
	line = strings.TrimSpace(line)
	line = strings.TrimPrefix(line, "|")
	line = strings.TrimSuffix(line, "|")
	parts := strings.Split(line, "|")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		out = append(out, strings.TrimSpace(p))
	}
	return out
}

// leadingInt достаёт сумму ранга из ячейки «13 (0+8+1+0+4)»: разбивку считает
// taskctl, здесь нужна только сумма.
func leadingInt(cell string) int {
	i := 0
	for i < len(cell) && cell[i] >= '0' && cell[i] <= '9' {
		i++
	}
	n, err := strconv.Atoi(cell[:i])
	if err != nil {
		return 0
	}
	return n
}

// rowForTicket ищет зеркальную строку тикета.
func rowForTicket(rows []boardRow, key string) *boardRow {
	for i := range rows {
		if strings.EqualFold(rows[i].Ticket, key) {
			return &rows[i]
		}
	}
	return nil
}

// boardStatusName переводит секцию контура в имя статуса, которое понимает
// taskctl move.
func boardStatusName(sect string) string {
	if sect == sectInProgress {
		return "in-progress"
	}
	return sect
}

// moveRow двигает строку доски. Вызовом taskctl, а не правкой таблицы: доску
// правит её утилита, и обход этого правила разошёлся бы с ней на первом же
// изменении формата. Подменяется в тестах, чтобы прогон не требовал taskctl в
// PATH.
var moveRow = func(root, id, sect string) error {
	cmd := exec.Command("taskctl", "-C", root, "move", id, boardStatusName(sect))
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("taskctl move %s %s: %v: %s", id, boardStatusName(sect), err, strings.TrimSpace(string(out)))
	}
	return nil
}
