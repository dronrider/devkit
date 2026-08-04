package main

import (
	"fmt"
	"strings"
)

// sectionOf это чтение таблицы: статус тикета ищется по спискам секций, и
// найденная секция и есть секция доски. Маппинг многие-к-одному, чужая
// детализация (три вида ревью, две очереди тестирования) складывается в одну
// секцию, новых секций доски она не порождает. Регистр не в счёт: трекеры
// пишут статусы то капсом, то с большой буквы, и различать их тут нечего.
func (c *contour) sectionOf(status string) (string, bool) {
	status = strings.TrimSpace(status)
	for _, sect := range statusSections {
		for _, s := range c.Status[sect] {
			if strings.EqualFold(strings.TrimSpace(s), status) {
				return sect, true
			}
		}
	}
	return "", false
}

// targetStatus это запись: целевой статус секции это первый элемент её списка,
// остальные синонимы для чтения.
func (c *contour) targetStatus(sect string) (string, error) {
	if sect == sectDone {
		return "", fmt.Errorf("в done автоматика не пишет: тикет закрывает чужой процесс")
	}
	vals := c.Status[sect]
	if len(vals) == 0 {
		return "", fmt.Errorf("контур %s не расписал секцию [status].%s, некуда переводить тикет", c.Name, sect)
	}
	return vals[0], nil
}

// fieldsFor отдаёт обязательные поля перехода из секции [fields_<секция>].
// Подстановка одна, {user} разворачивается в пользователя контура; чего в
// секции нет, то и не передаётся, и отказ трекера остаётся честным отказом с
// перечнем недостающего.
func (c *contour) fieldsFor(sect string) map[string]string {
	src := c.Fields[sect]
	if len(src) == 0 {
		return nil
	}
	out := make(map[string]string, len(src))
	for k, v := range src {
		out[k] = strings.ReplaceAll(v, "{user}", c.User)
	}
	return out
}

// unknownStatus это находка, а не ошибка разбора: статуса нет ни в одном
// списке контура. Ни тикет, ни строка доски при этом не двигаются, лечится
// находка строчкой в таблице.
func (c *contour) unknownStatus(t ticket) string {
	return fmt.Sprintf("находка: статус «%s» тикета %s не расписан в контуре %s (%s), ни тикет, ни строка доски не двигаются; впишите статус в нужную секцию [status]",
		t.Status, t.Key, c.Name, c.Path)
}

// doneHint это подсказка про закрытие. Тикет в конечном статусе для доски не
// находка: закрытие требует даты и коммитов, и решает его диспетчер.
func doneHint(t ticket) string {
	return fmt.Sprintf("тикет %s в конечном статусе «%s»: строку доски закрывает «taskctl close <ID>», автоматика в done не пишет никогда", t.Key, t.Status)
}
