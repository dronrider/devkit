package main

import (
	"fmt"
	"strings"
)

// cmdSubmit это вторая команда границы: набранный факт работы уезжает
// ворклогами, а тикет переходит в целевой статус секции Check. Порядок такой и
// есть порядок: сперва время, потом переход, иначе на границе оказался бы
// тикет без единого ворклога. Отдельного планировщика нет и имитации
// человеческого темпа тоже: трекер получает реальные цифры в момент события
// (docs/lld/DK-074-corp-contour.md, «Оценка и ворклоги: команды границ»).
//
// logOnly пишет накопившееся и статус не трогает: у долгой задачи время можно
// доливать, не дожидаясь границы.
func cmdSubmit(root, arg string, logOnly bool) (string, error) {
	tr, err := openTracker(root)
	if err != nil {
		return "", err
	}
	key := ticketKey(tr.bind, arg)
	t, err := tr.adapter.fetch(key)
	if err != nil {
		return "", err
	}
	sect, ok := tr.contour.sectionOf(t.Status)
	if !ok {
		return tr.contour.unknownStatus(t), nil
	}
	if sect == sectDone {
		return doneHint(t), nil
	}
	lines, err := writeWorklogs(root, tr, t)
	if err != nil {
		return "", err
	}
	if logOnly {
		lines = append(lines, "статус не трогаю: прогон с --log-only")
		return strings.Join(lines, "\n"), nil
	}
	if sect == sectCheck {
		lines = append(lines, fmt.Sprintf("тикет %s уже в «%s», перехода не делаю", t.Key, t.Status))
		return strings.Join(lines, "\n"), nil
	}
	target, err := tr.contour.targetStatus(sectCheck)
	if err != nil {
		return "", err
	}
	if err := tr.adapter.transition(t.Key, target, tr.contour.fieldsFor(sectCheck)); err != nil {
		return "", err
	}
	lines = append(lines, fmt.Sprintf("тикет %s: «%s» -> «%s»", t.Key, t.Status, target))
	return strings.Join(lines, "\n"), nil
}

// writeWorklogs раскладывает факт по календарным дням и пишет те, которых в
// журнале ещё нет. Комментария к ворклогу нет и не будет: тексты в трекер
// автоматика не сочиняет вовсе, их пишет человек или агент под его глазами.
func writeWorklogs(root string, tr *tracker, t ticket) ([]string, error) {
	runs, err := runTimes(root)
	if err != nil {
		return nil, err
	}
	repo := repoDir(root, tr.bind)
	commits, err := commitTimes(repo, t.Key)
	if err != nil {
		return nil, err
	}
	days := workDays(runs, commits)
	if len(days) == 0 {
		return []string{fmt.Sprintf("фактов работы над %s не нашёл: в %s нет коммитов с ключом тикета, писать нечего", t.Key, repo)}, nil
	}
	written, err := writtenDays(root, t.Key)
	if err != nil {
		return nil, err
	}
	var lines []string
	var skipped []string
	for _, d := range days {
		if written[d.Date] {
			skipped = append(skipped, d.Date)
			continue
		}
		spent := spentHours(d.Spent)
		if err := tr.adapter.worklog(t.Key, d.Date, spent); err != nil {
			return nil, err
		}
		if err := markWorklog(root, t.Key, d.Date, spent); err != nil {
			return nil, err
		}
		lines = append(lines, fmt.Sprintf("ворклог: %s %s", d.Date, spent))
	}
	if len(skipped) > 0 {
		lines = append(lines, fmt.Sprintf("уже записано раньше, пропускаю: %s", strings.Join(skipped, ", ")))
	}
	return lines, nil
}
