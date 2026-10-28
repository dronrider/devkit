package main

import (
	"fmt"
	"strconv"

	"github.com/dronrider/devkit/internal/control"
)

// Контроль задачи (DK-1470): строка берётся на карандаш суффиксом
// «[контроль: N]», где N это за сколько дней до срока контроль поднимает
// проверку работ. Риски срыва кладутся черновиком, отдельных строк контроль
// не заводит, вопросов человеку не больше потолка DK-400, и разбор суффикса
// лежит в общем каркасе internal/control: его читают отбор проверки
// internal/checkrun и носители подъёма. Контроль требует срока: без даты
// окно «за N дней до» не от чего считать, и установка контроля у строки без
// срока не проходит.

// controlSufRe разбирает суффикс «[контроль: N]». На него ссылается
// splitTitle, снимая суффикс при разборе заголовка.
var controlSufRe = control.SufRe

// controlOf читает число дней контроля из заголовка, второй возврат пуст у
// строки без контроля.
func controlOf(title string) (int, bool) {
	return control.Of(title)
}

// parseControl проверяет значение флага --control: «-» снимает контроль,
// иначе ждётся целое число дней 0..365. Ноль осмыслен: проверка в сам день
// срока.
func parseControl(v string) (int, error) {
	if v == "-" {
		return -1, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("--control %q не читается, жду число дней 0..365 или «-» для снятия контроля", v)
	}
	if n < 0 || n > 365 {
		return 0, fmt.Errorf("--control %d вне шкалы, жду число дней 0..365: ноль это проверка в день срока, год сверху", n)
	}
	return n, nil
}

// controlDays вынимает число дней из хвоста controlSuf, каким его отдал
// splitTitle; пустой хвост это отсутствие контроля.
func controlDays(suf string) (int, bool) {
	if suf == "" {
		return 0, false
	}
	return controlOf(suf)
}
