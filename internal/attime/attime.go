// Package attime разбирает назначенный час: абсолютное местное время, до
// которого ждут. Час пишут два носителя (решение 6 LLD DK-933). Условие «час» у
// `agentctl wait` ждёт внутри хода, взвод строки доски ждёт дольше хода, и
// разбор времени у них общий: одна и та же запись не должна значить у них
// разное.
package attime

import (
	"fmt"
	"strings"
	"time"
)

// Полные формы. Пробел и буква T равноправны: первый пишет человек, вторую
// печатают отметки ожидания.
var full = []string{
	"2006-01-02 15:04:05", "2006-01-02T15:04:05",
	"2006-01-02 15:04", "2006-01-02T15:04",
}

// Короткие формы без даты.
var clock = []string{"15:04", "15:04:05"}

// Parse разбирает час в поясе now. Короткая форма «02:00» значит ближайший
// такой момент строго после now: сегодня, если он ещё впереди, иначе завтра.
// Относительного срока тут нет намеренно. Смысл записи «через шесть часов»
// зависел бы от минуты чтения, и после сна машины она лгала бы.
func Parse(s string, now time.Time) (time.Time, error) {
	s = strings.TrimSpace(s)
	loc := now.Location()
	for _, l := range full {
		if t, err := time.ParseInLocation(l, s, loc); err == nil {
			return t, nil
		}
	}
	for _, l := range clock {
		t, err := time.ParseInLocation(l, s, loc)
		if err != nil {
			continue
		}
		at := time.Date(now.Year(), now.Month(), now.Day(), t.Hour(), t.Minute(), t.Second(), 0, loc)
		if !at.After(now) {
			at = time.Date(now.Year(), now.Month(), now.Day()+1, t.Hour(), t.Minute(), t.Second(), 0, loc)
		}
		return at, nil
	}
	return time.Time{}, fmt.Errorf("час %q не разобран, жду 02:00 либо 2026-09-12 02:00", s)
}
