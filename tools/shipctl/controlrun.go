package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/dronrider/devkit/internal/checkrun"
)

// Контроль задачи по сроку (DK-1470): подъём проверки работ за N дней до
// срока. Выкат и тик зовут `shipctl check-run` для строк Check, а контроль
// зовёт тик сторожка командой `shipctl control-run`: повод у контроля не
// выкат, а окно у срока. Отбор общий internal/checkrun, свой у команды только
// вход: строки берутся из секций работы, окно решает ControlNeed, заказ
// инспектору собирает ControlOrder.

// controlRuns поднимает контрольную проверку по названным строкам, а без имён
// по всем строкам работы с суффиксом контроля.
func controlRuns(root string, ids []string) ([]checkrun.Report, error) {
	b, err := loadBoard(root)
	if err != nil {
		return nil, fmt.Errorf("доска не прочиталась: %v", err)
	}
	rows := boardCheckRows(b)
	return checkrun.ControlRun(newShipCarrier(root), root, rows, ids, time.Now()), nil
}

// cmdControlRun это вход `shipctl control-run`: машинный вид --json отдаёт
// исход по строкам, тик по нему помнит подъёмы дня и повтор отказа зовёт
// человека один раз, как у check-run.
func cmdControlRun(root string, ids []string, asJSON bool) (string, bool, error) {
	reps, err := controlRuns(root, ids)
	if err != nil {
		return "", true, err
	}
	failed := checkrun.Failed(reps)
	if asJSON {
		data, err := json.Marshal(reps)
		if err != nil {
			return "", true, err
		}
		return string(data), failed, nil
	}
	var lines []string
	for _, r := range reps {
		lines = append(lines, r.Line)
	}
	return strings.Join(lines, "\n"), failed, nil
}
