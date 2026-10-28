// Package control читает суффикс контроля «[контроль: N]» заголовка строки
// доски (DK-1470): N это за сколько дней до срока контроль поднимает проверку
// работ. Разбор лежит в общем каркасе, а не в taskctl: суффикс пишет
// `taskctl set --control`, а читают его отбор проверки internal/checkrun и
// носители подъёма, и вторая копия регулярки разошлась бы с первой на первой
// правке, как расходился разбор приёмки до internal/accept.
package control

import (
	"fmt"
	"regexp"
	"strconv"
	"time"
)

// SufRe узнаёт суффикс контроля в конце заголовка. Суффикс стоит между сроком
// и приёмкой, а читатели снимают хвосты с конца, поэтому привязки к концу
// достаточно. taskctl ссылается на неё из splitTitle, как на приёмку из
// internal/accept.
var SufRe = regexp.MustCompile(`\s*\[контроль: ([0-9]{1,3})\]\s*$`)

// tailRe это хвосты [приёмка: ...], [провал: ...] и [блок: ...], которые стоят
// после [контроль: N] в фиксированном порядке (LLD DK-292, решение 3).
// Привязанный к концу SufRe их не пробивает, поэтому Of и DueOf снимают их
// прежде, чем искать контроль и срок, тем же порядком, что splitTitle в
// taskctl: сначала хвост, и потом предыдущий хвост.
var tailRe = regexp.MustCompile(`\s*\[(?:приёмка: (?:agent|mixed|user)|провал: [^|\[]*|блок: [^|\[]*)\]\s*$`)

// tails снимает хвосты после контроля, оставляя заголовок, у которого конец
// это контроль, срок либо основа.
func tails(title string) string {
	rest := title
	for {
		m := tailRe.FindString(rest)
		if m == "" {
			break
		}
		rest = rest[:len(rest)-len(m)]
	}
	return rest
}

// Of читает число дней контроля из заголовка строки. Ноль значит «проверка в
// сам день срока», false значит «строка без контроля».
func Of(title string) (int, bool) {
	if m := SufRe.FindStringSubmatch(tails(title)); m != nil {
		if n, err := strconv.Atoi(m[1]); err == nil {
			return n, true
		}
	}
	return 0, false
}

// Suffix собирает суффикс заголовка по числу дней, ноль суффикса не снимает:
// снятие это отсутствие контроля, а не его нулевое значение.
func Suffix(days int) string {
	return fmt.Sprintf(" [контроль: %d]", days)
}

// Due это срок из заголовка, копия регулярки taskctl. Она нужна отбору
// проверки, который считает окно «за N дней до срока» без похода в taskctl;
// копия трёх строк дешевле зависимости утилит друг от друга, тем же правилом
// internal/team держит копию лестницы ярусов.
var dueRe = regexp.MustCompile(`\s*\[срок: ([0-9]{4}-[0-9]{2}-[0-9]{2})\]\s*$`)

// DueOf читает дату срока из заголовка с контролем, пустая строка значит
// «срока нет»: контроль всегда стоит правее срока, и снимать его обязан сам
// читатель.
func DueOf(title string) string {
	if m := dueRe.FindStringSubmatch(SufRe.ReplaceAllString(tails(title), "")); m != nil {
		return m[1]
	}
	return ""
}

// Window решает, настало ли окно контроля: за days до срока или позже. Окно
// открывается за days до срока и остаётся открытым после него: пока строка не
// закрыта, контроль продолжает проверку и напоминания, а не замолкает в день
// срока. Кривая дата закрывает окно: разбор её отдал бы контроль строке с
// опечаткой в сроке.
func Window(due string, days int, today string) bool {
	d, err := time.Parse(time.DateOnly, due)
	if err != nil {
		return false
	}
	n, err := time.Parse(time.DateOnly, today)
	if err != nil {
		return false
	}
	left := d.Sub(n).Hours() / 24
	return left <= float64(days)
}

// Overdue считает, сколько дней срок висит просроченным: ноль и меньше значит
// «просрочки нет». Кривая дата это ноль, контроль кривой строки не дожимает.
func Overdue(due, today string) int {
	d, err := time.Parse(time.DateOnly, due)
	if err != nil {
		return 0
	}
	n, err := time.Parse(time.DateOnly, today)
	if err != nil {
		return 0
	}
	return int(n.Sub(d).Hours() / 24)
}
