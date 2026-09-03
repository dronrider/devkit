package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Порог свежести отметки sync. Сессий на машине за день несколько, и прогон на
// каждом старте был бы походом в сеть на ровном месте; рабочего дня хватает,
// чтобы чужой переход тикета доехал до доски в тот же день.
const syncMaxAge = 8 * time.Hour

// Порядок секций доски по конвейеру. Blocked в него не входит: это не стадия, а
// признак остановки, и сравнивать его с Check нечем.
var sectOrder = map[string]int{sectBacklog: 0, sectInProgress: 1, sectCheck: 2, sectDone: 3}

// cmdSync это pull статусов: по каждой зеркальной строке снимается статус
// тикета, и доска догоняет трекер. В трекер sync не пишет вовсе, переходы
// делают команды границ (take, submit), поэтому доска впереди тикета это
// находка: границу прошли мимо команды.
func cmdSync(root string, ifStale bool) (string, error) {
	if ifStale {
		if age, fresh := syncFresh(root); fresh {
			return fmt.Sprintf("отметка sync свежая (%s назад, порог %s), прогона нет", humanAge(age), humanAge(syncMaxAge)), nil
		}
	}
	tr, err := openTracker(root)
	if err != nil {
		return "", err
	}
	rows, err := loadBoardRows(root, tr.bind.Key)
	if err != nil {
		return "", err
	}
	var lines []string
	moved, found, seen := 0, 0, 0
	for _, row := range rows {
		if row.Ticket == "" {
			continue
		}
		if isReviewRow(row) {
			// У строки ревью своя судьба, не статус тикета: решает её MR, не
			// sync (LLD DK-756, часть решения 7). Пока эта судьба не считана
			// (DK-759), sync строку не двигает и в трекер за ней не ходит.
			continue
		}
		seen++
		line, kind, err := syncRow(root, tr, row)
		if err != nil {
			return "", err
		}
		switch kind {
		case "move":
			moved++
		case "finding":
			found++
		}
		if line != "" {
			lines = append(lines, line)
		}
	}
	if seen == 0 {
		lines = append(lines, "зеркальных строк на доске нет: ни одна ссылка не ведёт на тикет "+tr.bind.Key)
	} else {
		lines = append(lines, fmt.Sprintf("зеркальных строк: %d, доска догнала: %d, находок: %d", seen, moved, found))
	}
	if err := markSync(root); err != nil {
		return "", err
	}
	return strings.Join(lines, "\n"), nil
}

// syncRow сверяет одну зеркальную строку. Тикет в конечном статусе строку не
// закрывает: закрытие требует даты и коммитов, и решает его диспетчер.
func syncRow(root string, tr *tracker, row boardRow) (line, kind string, err error) {
	t, err := tr.adapter.fetch(row.Ticket)
	if err != nil {
		return "", "", err
	}
	sect, ok := tr.contour.sectionOf(t.Status)
	if !ok {
		return fmt.Sprintf("%s: %s", row.ID, tr.contour.unknownStatus(t)), "finding", nil
	}
	if sect == row.Sect {
		return "", "", nil
	}
	if sect == sectDone {
		return fmt.Sprintf("%s: %s", row.ID, doneHint(t)), "finding", nil
	}
	if ob, okb := sectOrder[row.Sect]; okb {
		if ot, okt := sectOrder[sect]; okt && ob > ot {
			return fmt.Sprintf("находка: строка %s в «%s», а тикет %s всё ещё в «%s»: границу прошли мимо «trackctl take» и «trackctl submit», доска впереди тикета",
				row.ID, boardNames[row.Sect], t.Key, t.Status), "finding", nil
		}
	}
	if err := moveRow(root, row.ID, sect); err != nil {
		return "", "", err
	}
	return fmt.Sprintf("%s: доска догоняет тикет %s («%s»), строка ушла в %s", row.ID, t.Key, t.Status, boardNames[sect]), "move", nil
}

// syncFresh отвечает, свежа ли отметка последнего прогона. Отметки нет значит
// прогон нужен: sync не гонялся ни разу.
func syncFresh(root string) (time.Duration, bool) {
	fi, err := os.Stat(filepath.Join(root, syncMarkPath))
	if err != nil {
		return 0, false
	}
	age := time.Since(fi.ModTime())
	return age, age < syncMaxAge
}

// markSync освежает отметку прогона. Читает её status по времени файла, поэтому
// содержимое здесь только для человека, заглянувшего в файл глазами.
func markSync(root string) error {
	path := filepath.Join(root, syncMarkPath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(time.Now().Format(stampLayout)+"\n"), 0o644)
}

func humanAge(d time.Duration) string {
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%d мин", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d ч", int(d.Hours()))
	default:
		return fmt.Sprintf("%d дн", int(d.Hours()/24))
	}
}
