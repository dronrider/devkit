package main

import (
	"fmt"
	"math"
	"strings"
	"time"
)

// correctModel зовёт корректор на самом дорогом ярусе, который маппинг выдаёт
// задачам пачки: цена M с неопределённостью 0-1 по умолчанию идёт сильной
// моделью (pickTier), а подъёмы дешёвых ярусов бакетов не добавляют, mini и
// base тратят из одного week_all, а pro и max добавляют week_max. budget читает
// потолок по тому, что корректор сделал с opus, вместо того чтобы прикидывать
// темп расхода сам: своё предсказание разошлось бы с корректором ровно там,
// где ошибка дороже всего, на подъёме opus -> fable.
func correctModel(q *quotaSpec, s snapshot, now time.Time) correction {
	return correctTier(q, tierPro, false, s, now)
}

// freshSurplus отвечает, весь ли набор бакетов яруса в профиците на свежем
// снимке. Вопрос тот же, которым correctTier проверяет подъём на ступень
// выше, только заданный явно для бакетов конкретного яруса, а не яруса
// назначения: budget им пользуется, чтобы отличить «не двигает, потому что
// свой бакет в профиците» от «не двигает по любой другой причине». Пустой
// набор бакетов профицитом не считается, профилю без объявления расхода
// отвечать нечем.
func freshSurplus(q *quotaSpec, s snapshot, now time.Time, tier string) bool {
	need := q.Spend[tier]
	return len(need) > 0 && s.fresh(now) && allWithStatus(s, need, statusSurplus, now)
}

// budgetBuckets это бакеты, чей статус объясняет потолок пачки: всегда те, из
// которых тратит opus, и вдобавок бакеты яруса max, когда корректор поднял
// вердикт до fable, туда ведь уйдёт часть запусков пачки.
func budgetBuckets(q *quotaSpec, c correction) []string {
	names := append([]string{}, q.Spend[tierPro]...)
	if c.shifted() && !c.Down {
		for _, n := range q.Spend[tierMax] {
			if !contains(names, n) {
				names = append(names, n)
			}
		}
	}
	return names
}

// batchPairTail это хвост причины: почему потолок вообще меряется штуками
// задач, а не процентом лимита. Задача расходует пару запусков, исполнителя и
// ревьювера, и это единственная переменная, которую budget добавляет к ответу
// корректора от себя.
const batchPairTail = ", на задачу идёт пара запусков (исполнитель и ревьювер)"

// budgetLimit это таблица «Размер пачки» из DK-048: потолок читается по тому,
// что correctModel сделал с opus, а не по своему рассуждению про проценты.
// Асимметрия свежести приходит из того же корректора: дефицит сдвигает вниз
// на снимке любой давности, а профицит поднимает только на свежем.
func budgetLimit(q *quotaSpec, c correction, s snapshot, now time.Time) (int, string) {
	switch {
	case c.Down:
		return 1, fmt.Sprintf("корректор сдвигает opus вниз (%s), потолок 1: пачку не собирать, работать по одной", c.Note)
	case c.shifted():
		return 3, fmt.Sprintf("корректор поднимает opus до fable (%s), потолок 3: верхний ярус дорогой, впятером его не тратим", c.Note)
	case freshSurplus(q, s, now, tierPro):
		return 5, fmt.Sprintf("корректор вердикт не двигает, %s в профиците на свежем снимке, потолок 5%s",
			strings.Join(q.Spend[tierPro], ", "), batchPairTail)
	default:
		return 3, budgetDefaultWhy(q, s, now)
	}
}

// budgetDefaultWhy разбирает, какая из остальных причин привела к потолку по
// умолчанию: снимка нет, он протух, у нужного бакета уже прошла дата сброса,
// либо расход просто в норме. Строка называет способ снять снимок там, где
// это имеет смысл, как это делает pick: отсутствие данных не ошибка, но и не
// молчание.
func budgetDefaultWhy(q *quotaSpec, s snapshot, now time.Time) string {
	tail := ", потолок 3" + batchPairTail
	switch {
	case s.empty():
		return "снимка квоты нет" + tail + "; снять: agentctl quota refresh"
	case !s.fresh(now):
		return "снимок квоты протух, вверх корректор не двигает" + tail + "; переснять: agentctl quota refresh"
	}
	for _, name := range q.Spend[tierPro] {
		if b, ok := s.bucket(name); ok && b.status(now) == statusExpired {
			return fmt.Sprintf("бакет %s уже за датой сброса", name) + tail
		}
	}
	return "корректор вердикт не двигает" + tail
}

// budgetSummary печатает бакеты, по которым принят потолок, тем же форматом,
// каким их печатает cmdQuota: строку вердикта тогда можно читать без сверки с
// отдельным вызовом quota. Бакета без данных в снимке нет, значит печатать
// нечего, причина остаётся в why.
func budgetSummary(q *quotaSpec, c correction, why string, s snapshot, now time.Time) string {
	var parts []string
	for _, name := range budgetBuckets(q, c) {
		b, ok := s.bucket(name)
		if !ok {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s: потрачено %d%%, %s, pace %.1f, %s",
			name, int(math.Round(b.Used*100)), bucketWhen(b), b.pace(now), b.status(now)))
	}
	parts = append(parts, why)
	return strings.Join(parts, "; ")
}

// cmdBudget считает потолок пачки для taskctl batch --limit. Пачка из пяти
// задач это десять запусков (исполнитель и ревьювер на каждую), а pick
// корректирует вердикт на одну задачу за раз и про расход пачки не знает
// ничего, поэтому потолок считается отдельной командой там, где живёт снимок
// остатка. Первая строка машинная, вторая называет бакет, темп, статус и
// причину потолка.
func cmdBudget(root string, now time.Time) (string, error) {
	hc := resolveHarnessContext(root, "")
	if hc.Quota == nil {
		// Снимать остаток нечем вовсе: причина уходит в строку, потому что
		// потолок по умолчанию выглядел бы совершенно обычным.
		return fmt.Sprintf("batch: 3\n%s, потолок по умолчанию 3", hc.quotaWhy()), nil
	}
	s, err := hc.Quota.read()
	if err != nil {
		return "", err
	}
	c := correctModel(hc.Quota, s, now)
	limit, why := budgetLimit(hc.Quota, c, s, now)
	line := budgetSummary(hc.Quota, c, why, s, now)
	if w := hc.Quota.legacyWarn(); w != "" {
		line += "; " + w
	}
	for _, w := range s.Warns {
		line += "; " + w
	}
	return fmt.Sprintf("batch: %d\n%s", limit, line), nil
}
