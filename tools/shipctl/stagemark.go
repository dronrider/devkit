package main

import (
	"time"

	"github.com/dronrider/devkit/internal/stage"
)

// Этапы слияния и выката ставит сам shipctl (DK-911): он и есть инструмент,
// который начинает эту деятельность, и диспетчеру помнить о команде записи не
// надо. Этап открывается под взятым замком и закрывается по концу команды
// своим концом: смена статуса уносит пакет только у merge, а у выката строки
// уже стоят в Check, и без своего конца выкат висел бы живым этапом до
// закрытия задачи. Ожидание очереди пишется на отказе по занятому замку и
// закрывается тем, что следующий заход открыл слияние либо выкат.
//
// Провал записи ни одну команду не роняет, как и у остальных писателей: без
// отметки конвейер работает, просто молча.

// stageOpen открывает этап задачи по основному чекауту.
func stageOpen(root, id, kind, note string) {
	stage.Open(stage.Home(), stage.MainRoot(root), id, kind, note, time.Now())
}

// stageClose закрывает живой этап названного вида, чужой живой этап не
// трогается.
func stageClose(root, id, kind string) {
	stage.Close(stage.Home(), stage.MainRoot(root), id, kind, time.Now(), "")
}

// stageQueue отмечает ожидание очереди у названных задач. Разлив под
// --drain бьёт в занятый замок тиком за тиком, и второе ожидание поверх
// живого первого не открывается: одна остановка, одна строка.
func stageQueue(root string, ids []string, note string) {
	home, main := stage.Home(), stage.MainRoot(root)
	for _, id := range ids {
		if rec, err := stage.Load(stage.Path(home, main, id)); err == nil {
			if live, ok := rec.Live(); ok && live.Kind == stage.WaitQueue {
				continue
			}
		}
		stage.Open(home, main, id, stage.WaitQueue, note, time.Now())
	}
}

// queueTrain называет состав поезда для ожидания очереди у ship: замок занят,
// и состав считается без него, читая доску и лог как есть. Не посчитался,
// значит отмечать некого, и отказ по замку уходит как раньше.
func queueTrain(root string) []string {
	b, err := loadBoard(root)
	if err != nil {
		return nil
	}
	main, err := mainBranch(root)
	if err != nil {
		return nil
	}
	train, _, err := trainTasks(root, main, b)
	if err != nil {
		return nil
	}
	return train
}
