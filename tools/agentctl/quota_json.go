package main

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"
)

// Имя и версия машинного ответа quota --json: внешний потребитель (дежурный
// восстановитель конвейера, DK-1645) сверяется с ними, а не угадывает формат по
// наличию полей, и смена смысла без смены версии для него незамеченной не
// останется.
const (
	quotaJSONSchema  = "agentctl/quota"
	quotaJSONVersion = 1
)

// quotaBucketJSON это строка снимка в машинном виде. Пустой reset значит
// нетронутое окно: отсчёт до сброса не начат, как и в человеческой печати.
type quotaBucketJSON struct {
	Name  string  `json:"name"`
	Used  float64 `json:"used"`
	Reset string  `json:"reset"`
}

// quotaJSON это машинный вердикт по готовому снимку. Available консервативен:
// всякая неизвестность (нет снимка, протух, пометки, предупреждения разбора,
// пробел в объявленных бакетах, исчерпанное или сгоревшее окно) закрывает
// запуск, и пустой reason значит «запускать можно». Complete показывает
// отдельно, все ли бакеты профиля в снимке есть: доступности без полноты не
// бывает, а глазом пробел виден быстрее отдельным полем.
type quotaJSON struct {
	Schema    string            `json:"schema"`
	Version   int               `json:"version"`
	Harness   string            `json:"harness"`
	Taken     string            `json:"taken"`
	Fresh     bool              `json:"fresh"`
	Complete  bool              `json:"complete"`
	Available bool              `json:"available"`
	Reason    string            `json:"reason"`
	Buckets   []quotaBucketJSON `json:"buckets"`
}

// cmdQuotaJSON читает снимок и отвечает вердиктом. Чтение то же, что у
// человеческой печати: q.read, свежесть по snapshot.fresh, причины по
// snapshot.ageWarn, второго разбора снимка тут нет.
func cmdQuotaJSON(q *quotaSpec, now time.Time) (string, error) {
	s, err := q.read()
	if err != nil {
		return "", err
	}
	missing := missingQuotaBuckets(q, s)
	out := quotaJSON{
		Schema:   quotaJSONSchema,
		Version:  quotaJSONVersion,
		Harness:  q.Harness,
		Fresh:    s.fresh(now),
		Complete: len(q.Buckets) > 0 && len(missing) == 0,
		Buckets:  make([]quotaBucketJSON, 0, len(s.Buckets)),
	}
	if !s.Taken.IsZero() {
		out.Taken = s.Taken.Format(quotaTimeLayout)
	}
	for _, b := range s.Buckets {
		reset := ""
		if !b.Reset.IsZero() {
			reset = b.Reset.Format(quotaTimeLayout)
		}
		out.Buckets = append(out.Buckets, quotaBucketJSON{Name: b.Name, Used: b.Used, Reset: reset})
	}
	out.Reason = quotaRefusal(q, s, now, missing)
	out.Available = out.Reason == ""
	data, err := json.Marshal(out)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// missingQuotaBuckets это объявленные профилем бакеты, которых в снимке нет:
// по ним считается полнота, имена идут в порядке объявления.
func missingQuotaBuckets(q *quotaSpec, s snapshot) []string {
	var missing []string
	for _, name := range q.Buckets {
		if _, ok := s.bucket(name); !ok {
			missing = append(missing, name)
		}
	}
	return missing
}

// quotaRefusal проходит консервативные проверки по порядку и возвращает первую
// причину отказа; пустая строка значит «запускать можно». Порядок фиксирован,
// от состояния снимка к его содержимому: одинаковый файл получает одинаковую
// причину, и потребитель не видит скачущих формулировок. Пробел в бакетах
// становится отказом здесь же: запуск по неизвестному остатку невозможен, и
// ответа «неполно, но доступно» не существует.
func quotaRefusal(q *quotaSpec, s snapshot, now time.Time, missing []string) string {
	if len(q.Buckets) == 0 {
		return "в профиле нет бакетов квоты"
	}
	if w := s.ageWarn(q.From, now); w != "" {
		return w
	}
	if len(s.Warns) > 0 {
		return strings.Join(s.Warns, "; ")
	}
	if names := s.partialNames(); len(names) > 0 {
		return "бакеты без своей цифры: " + noteList(s.Partial, names)
	}
	if names := s.borrowedNames(); len(names) > 0 {
		return "цифра бакетов не своя: " + noteList(s.Borrowed, names)
	}
	if len(missing) > 0 {
		return "в снимке нет бакетов: " + strings.Join(missing, ", ")
	}
	for _, b := range s.Buckets {
		if math.IsNaN(b.Used) || math.IsInf(b.Used, 0) || b.Used < 0 {
			return "неверное значение бакета " + b.Name
		}
		if b.Used > 0 && b.Reset.IsZero() {
			return "неизвестный срок бакета " + b.Name
		}
		if b.Used >= 1 {
			return fmt.Sprintf("бакет %s исчерпан", b.Name)
		}
		if !b.Reset.IsZero() && !b.Reset.After(now) {
			return fmt.Sprintf("у бакета %s дата сброса %s уже прошла", b.Name, b.Reset.Format(quotaTimeLayout))
		}
	}
	return ""
}

// noteList собирает «имя (причина)» для помеченных бакетов: причина без имени
// молчит, а имя без причины смотрит в файл.
func noteList(m map[string]string, names []string) string {
	parts := make([]string, 0, len(names))
	for _, name := range names {
		parts = append(parts, name+" ("+m[name]+")")
	}
	return strings.Join(parts, ", ")
}
