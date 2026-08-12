package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
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

// Link это ячейка ссылки строки. В корп-контуре она несёт ключ тикета
// зеркальной строки, и по ней start именует ветку (DK-124); дома ссылка ведёт
// на файл задачи, и никто её не читает.
type row struct{ ID, Title, Type, Cost, Link string }

type board struct {
	sects map[string][]row
}

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
			r.Link = strings.TrimSpace(cells[6])
		} else if len(cells) == 6 {
			r.Link = strings.TrimSpace(cells[5])
		}
		b.sects[sect] = append(b.sects[sect], r)
	}
	return b, nil
}

// failSufRe вытаскивает причину из пометки провала проверки в заголовке
// строки. Ставит пометку taskctl fail, и по ней видно, что за задачей остался
// сломанный прод: очередь тогда стоит, даже когда строка ушла из Check.
var failSufRe = regexp.MustCompile(`\[провал: ([^|\[\]]*)\]`)

type failure struct{ ID, Reason string }

// failedChecks собирает задачи с непогашенным провалом проверки. Секции
// перебираются фиксированным порядком, чтобы отказ merge и строка status не
// плясали от прогона к прогону.
func failedChecks(b *board) []failure {
	var out []failure
	for _, key := range []string{"in-progress", "blocked", "check", "backlog"} {
		for _, r := range b.sects[key] {
			if m := failSufRe.FindStringSubmatch(r.Title); m != nil {
				out = append(out, failure{r.ID, strings.TrimSpace(m[1])})
			}
		}
	}
	return out
}

// failedOf возвращает причину провала за задачей, пустую строку если провала
// за ней не числится.
func failedOf(b *board, id string) string {
	for _, f := range failedChecks(b) {
		if f.ID == id {
			return f.Reason
		}
	}
	return ""
}

// brokenProd описывает сломанный прод одинаково в отказах merge, ship и в
// строке status: чья это задача, чем сломано и чем чинится.
func brokenProd(f failure) string {
	return fmt.Sprintf("провал проверки за %s (%s): прод считается сломанным, чинить shipctl revert %s либо форвард-фиксом и shipctl merge %s",
		f.ID, f.Reason, f.ID, f.ID)
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
// зафиксированного исхода: по RULES.board.md у каждого элемента списка должен
// стоять итог «исправлено» либо «отклонено» с причиной. Замечанием считается
// элемент списка целиком (маркерная строка вместе со строками продолжения), а
// вердикт без замечаний замечанием не считается. Критерий тот же, каким судит
// taskctl outcome, иначе merge и review show разойдутся на одной строке.
func openReviewNotes(root, id string) ([]string, error) {
	data, err := os.ReadFile(filepath.Join(root, "docs", "tasks", id+".md"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var open []string
	for _, item := range reviewItems(string(data), "## Ревью") {
		if reviewOutcome(item) == "" {
			open = append(open, strings.TrimSpace(item))
		}
	}
	return open, nil
}
