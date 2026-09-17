package main

import (
	"fmt"
	"math"
	"os"
	"strings"
	"time"
)

// Ответ сверки. ok это бюджет, который влезает в остаток с запасом, tight это
// бюджет, съедающий остаток бакета почти целиком, unknown это случай, когда
// сверять не с чем: снимка нет, он пуст либо нужных бакетов в нём не оказалось.
// Третье значение заведено ради честности: «ok» на пустом снимке читалось бы
// как проверенный запас, хотя ничего не проверялось.
const (
	fitOK      = "ok"
	fitTight   = "tight"
	fitUnknown = "unknown"
)

// Доля остатка, с которой бюджет считается съедающим бакет почти целиком:
// четыре пятых. Порог грубый сознательно, он отделяет «цель влезает» от «цель
// выбирает бакет до сброса», а точность тут мнимая: бакеты общие на машину и
// чужая сессия тратит те же проценты.
const (
	fitShareNum = 4
	fitShareDen = 5
)

// Хвост предупреждения: сверка не отказ, решение остаётся за человеком.
const fitTightTail = "отказом это не становится: поднять числа, срезать состав либо дождаться сброса"

// bucketLeft это остаток бакета до сброса в процентных пунктах, теми же целыми
// пунктами, какими бюджет цели задан в постановке.
func bucketLeft(b bucket) int {
	left := 100 - int(math.Round(b.Used*100))
	if left < 0 {
		return 0
	}
	return left
}

// fitTightBucket отвечает, съедает ли бюджет почти весь остаток. Сравнение
// идёт в целых пунктах без деления, чтобы порог не плясал от округления.
func fitTightBucket(limit, left int) bool { return limit*fitShareDen >= left*fitShareNum }

// cmdFit сверяет бюджет цели с остатком бакетов до сброса. Гейт витка
// (cmdSpend) отвечает на другой вопрос: сколько цель уже потратила из своего
// потолка. Здесь вопрос постановки: влезает ли сам потолок в то, что на машине
// осталось до сброса окна. Без этой сверки цель с бюджетом week_all <= 40 при
// остатке 49 пп выглядит на постановке обычной, а бакет выбирает целиком.
// Первая строка машинная, вторая называет бакеты с датой сброса.
func cmdFit(root, goalPath string, now time.Time) (string, error) {
	path, err := goalPathOf(root, goalPath)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	hc := resolveHarnessContext(root, "")
	budget, err := parseGoalBudget(goalSection(string(data), goalBudgetSection), hc.Quota)
	if err != nil {
		return "", err
	}
	if len(budget.Limits) == 0 {
		return "", fmt.Errorf("в разделе «Бюджет» файла %s нет ни одной строки «бюджет: <бакет> <= <проценты>», сверять с остатком нечего", path)
	}

	var s snapshot
	var warns []string
	if hc.Quota == nil {
		warns = append(warns, hc.quotaWhy()+", остаток до сброса неизвестен")
	} else {
		s, err = hc.Quota.read()
		if err != nil {
			return "", err
		}
		switch {
		case s.empty():
			warns = append(warns, "снимка квоты нет, остаток до сброса неизвестен; снять: agentctl quota refresh")
		case !s.fresh(now):
			warns = append(warns, "снимок квоты "+snapshotAge(s, now)+", остаток мог уйти; переснять: agentctl quota refresh")
		}
		if w := hc.Quota.legacyWarn(); w != "" {
			warns = append(warns, w)
		}
		warns = append(warns, s.Warns...)
	}

	fit := fitUnknown
	var parts, tight []string
	for _, l := range budget.Limits {
		b, ok := s.bucket(l.Bucket)
		if !ok {
			warns = append(warns, fmt.Sprintf("бакета %s в снимке нет, сверять его не с чем", l.Bucket))
			continue
		}
		// Дата сброса позади значит, что окно снимка уже сменилось: остаток в
		// нём от старого окна, и сравнивать с ним бюджет нечестно.
		if b.status(now) == statusExpired {
			warns = append(warns, fmt.Sprintf("у бакета %s дата сброса (%s) уже прошла, остаток в снимке от старого окна; переснять: agentctl quota refresh",
				l.Bucket, b.Reset.Format(quotaTimeLayout)))
			continue
		}
		left := bucketLeft(b)
		part := fmt.Sprintf("%s: бюджет %d пп из остатка %d пп, %s", l.Bucket, l.Limit, left, bucketWhen(b))
		if fitTightBucket(l.Limit, left) {
			part += ", съедает почти весь остаток"
			tight = append(tight, l.Bucket)
		}
		parts = append(parts, part)
	}
	switch {
	case len(parts) == 0:
	case len(tight) > 0:
		fit = fitTight
	default:
		fit = fitOK
	}
	if fit == fitTight {
		parts = append(parts, fmt.Sprintf("бюджет цели съедает почти весь остаток по %s до сброса, %s; %s",
			strings.Join(tight, ", "), fitTightTail, goalMeasureTail))
	}
	if fit == fitUnknown {
		parts = append(parts, "сверить бюджет с остатком не с чем")
	}
	line := strings.Join(parts, "; ")
	for _, w := range warns {
		line += "; " + w
	}
	return fmt.Sprintf("fit: %s\n%s", fit, line), nil
}
