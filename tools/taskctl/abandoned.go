package main

import (
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/dronrider/devkit/internal/merged"
	"github.com/dronrider/devkit/internal/peers"
	"github.com/dronrider/devkit/internal/stage"
)

// Вопрос «какие строки In progress брошены» задаёт тик сторожка
// (tools/devkitctl/watch.py). Долг, взятый у диффа на сдаче, попадает на конец
// жизни исполнителя: сессия закрывает основную задачу и выходит, а строка
// остаётся в In progress без ветки и сессии, и её не видит ни очередь, ни
// повторный pick (DK-1417). Ответ считается тут, а не в самом тике, тем же
// правилом, что у closable: следы работы и возраст строки живут у taskctl, и
// вторая копия этих правил в питоне разошлась бы с первой на первой же правке.

// abandonedHoursEnv переопределяет срок простоя в часах для стендов, тем же
// приёмом, что и потолки планировщика (slotTreesEnv в slot.go).
const abandonedHoursEnv = "DEVKIT_ABANDONED_HOURS"

// abandonedHoursDefault это срок простоя строки без следов работы, трое
// суток. Живой носитель оставляет след за минуты: ветку заводит board-task,
// привязку по факту работы пишет хук касания, слитая работа видна в main
// сразу. Худший законный простой без следа это круги чужого ревью у соседней
// сдачи (DK-1152 шла два дня), и срок стоит выше него с запасом на выходные.
const abandonedHoursDefault = 72

// abandonedAfter отдаёт срок простоя строки. Окружение перебивает часы для
// стендов.
func abandonedAfter() time.Duration {
	if v := os.Getenv(abandonedHoursEnv); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			return time.Duration(n) * time.Hour
		}
	}
	return time.Duration(abandonedHoursDefault) * time.Hour
}

// abandonedRow это строка In progress, кандидат возврата в Backlog, вместе с
// причиной отказа для остальных: список печатается машине, причины человеку,
// который спросил, почему строка стоит.
type abandonedRow struct {
	ID     string
	Reason string // пусто значит «возвращать можно»
}

// holdsRow называет след работы, который держит строку In progress: дерево
// задачи, живая сессия из реестра чатов либо слитость работы в main. Пусто у
// строки, не держащейся ничем: такой взял носитель, умерший до первого следа.
// Молчащая сессия держит как живая: окно живо и вернётся к строке. Ошибка git
// у слитости считается держанием: спросить выйдет следующим тиком, а вернуть
// строку с живой работой дороже, чем подержать брошенную ещё один срок.
func holdsRow(root string, r *Row, sv *stageView) string {
	if taskHasTree(root, r.ID) {
		return "дерево задачи"
	}
	if sv != nil {
		if life := peers.Judge(sv.peers, rowSessions(sv, r.ID), sv.now); life.State != peers.Gone {
			return lifeWords(life)
		}
	}
	main, err := merged.Main(root)
	if err != nil {
		return "слитость не спросилась"
	}
	v, err := merged.Task(root, main, r.ID)
	if err != nil {
		return "слитость не спросилась"
	}
	if v.Merged {
		return "слита в main, ждёт выката"
	}
	return ""
}

// rowSessions называет сессии, которые могут держать строку: та же выборка,
// что у строки этапа (taskSessions), а при открытом этапе записи ещё и его
// сессия. Строке без записи остаются рабочие привязки реестра чатов.
func rowSessions(sv *stageView, id string) []string {
	var st stage.Stage
	if rec, ok := sv.record(id); ok {
		if live, ok := rec.Live(); ok {
			st = live
		}
	}
	return sv.taskSessions(id, st)
}

// abandonedOverdue отвечает, простояла ли строка дольше срока возврата.
// Возраст считается тем же blame, что и подпись возраста (boardTimes): строка
// не попадала в коммит или git недоступен, и возраста нет, а без возраста
// возврата нет.
func abandonedOverdue(times map[int]int64, lineIdx int) bool {
	sec, ok := times[lineIdx]
	if !ok {
		return false
	}
	return timeNow().Sub(time.Unix(sec, 0)) >= abandonedAfter()
}

// abandoned отбирает строки In progress к возврату в Backlog: не цель, без
// следов работы и старше срока. Цель держит свой надзор по записи реестра
// целей, и общий возврат её не трогает.
func abandoned(root string, b *Board, times map[int]int64, sv *stageView) []abandonedRow {
	var out []abandonedRow
	for _, r := range b.Sects[SectInProgress].Rows {
		if goalRow(r.Title) {
			continue
		}
		switch hold := holdsRow(root, r, sv); {
		case hold != "":
			out = append(out, abandonedRow{r.ID, hold})
		case !abandonedOverdue(times, r.LineIdx):
			out = append(out, abandonedRow{r.ID, "простой меньше срока"})
		default:
			out = append(out, abandonedRow{ID: r.ID})
		}
	}
	return out
}

// cmdAbandoned печатает брошенные строки по одному ID в строке, а отказы
// уводит под строку «отказано:». Формат это контракт с тиком сторожка, тот же,
// что у closable: голый ID значит «возвращать можно», и никакая проза над ним
// не встаёт.
func cmdAbandoned(root string) (string, error) {
	b, err := LoadBoard(boardPath(root))
	if err != nil {
		return "", err
	}
	times, err := boardTimesChecked(root)
	if err != nil {
		return "", err
	}
	rows := abandoned(root, b, times, loadStageView(root))
	var ready, refused []string
	for _, r := range rows {
		if r.Reason == "" {
			ready = append(ready, r.ID)
		} else {
			refused = append(refused, "  "+r.ID+": "+r.Reason)
		}
	}
	var out []string
	out = append(out, ready...)
	if len(ready) == 0 {
		out = append(out, "возвращать нечего")
	}
	if len(refused) > 0 {
		out = append(out, "отказано:")
		out = append(out, refused...)
	}
	return strings.Join(out, "\n"), nil
}
