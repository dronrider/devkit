package main

import (
	"github.com/dronrider/devkit/internal/stage"
)

// stageMark это живой этап строки, как он уезжает на экран: вид деятельности
// словом и время начала в unix-секундах, теми же единицами, что Started у живой
// работы. Клиент считает по ним, сколько этап идёт, и своего разбора времени не
// заводит.
type stageMark struct {
	Kind  string
	Since int64
}

// liveStages собирает живые этапы задач проекта: запись на задачу пишет
// конвейер (agentctl pick --record, taskctl move), лежат они на уровне машины в
// ~/.devkit/runs и разделены полем root. Дашборд их только читает: писать этап
// он не вправе, иначе работа, поднятая из терминала, осталась бы неотмеченной.
func (s *server) liveStages(projectPath string) map[string]stageMark {
	out := map[string]stageMark{}
	for _, rec := range stage.List(s.cfg.Home, projectPath) {
		live, ok := rec.Live()
		if !ok {
			continue
		}
		out[rec.ID] = stageMark{Kind: live.Kind, Since: live.Start.Unix()}
	}
	return out
}

// rowStage называет вид деятельности строки. Оборванный этап за работу не
// выдаётся: запись осталась на диске, а сессии за ней нет, и это тот же случай,
// что gone у признака Run, только заметить его без проверки нечем, потому что
// файл переживает и умершую сессию. Ожидание снаружи проверкой не задевается:
// там ждут человека, живой сессии за таким этапом нет по смыслу, и требовать её
// значило бы гасить единственный этап, который экран и должен показать серым.
func rowStage(stages map[string]stageMark, run, id string) (string, int64) {
	mark, hit := stages[id]
	if !hit {
		return "", 0
	}
	// Оборванный этап на экран не идёт: сессии, которая его вела, нет ни живой,
	// ни нашей вовсе (признаки gone и other).
	if stage.NeedsSession(mark.Kind) && (run == "" || run == runGone || run == runOther) {
		return "", 0
	}
	return mark.Kind, mark.Since
}
