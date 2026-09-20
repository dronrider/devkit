package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/dronrider/devkit/internal/stage"
	"github.com/dronrider/devkit/internal/taskform"
)

// taskctl stops сводит остановки конвейера по разрядам парковки (DK-396).
// Считает, сколько раз задачи вставали на каждый машинный разряд причины
// blocked и сколько простаивали. Разряды те же четыре, что держит
// parkPrefixes (LLD DK-400, решение 2, LLD DK-756, решения 5 и 6), кроме
// «слияние:» и «закрытие:». Это очередь своих задач по LLD DK-933, а не
// повод звать человека, и в счёт остановок она не идёт.
//
// Источник тот же, что у `taskctl pilot`. Команда не диффует историю доски
// коммит за коммитом, а читает то, что о ней уже осело на диске. Вход в
// Blocked открывает этап «снаружи» с запиской «блок: <причина>»
// (tools/taskctl/stage.go, openWait). Выход из статуса уносит пакет
// этапов в раздел «Ход работы» файла задачи (flushStages), и так закрытая
// остановка становится строкой истории. Ещё не закрытая (задача стоит в
// Blocked прямо сейчас) остаётся живой записью в ~/.devkit/runs, и её
// простой считается до текущего момента, как в pilot.

// stopsWindowLayout это форма даты окна: день без времени, тот же формат, что
// у pilot.
const stopsWindowLayout = "2006-01-02"

// stopsClasses это разряды сводки, ровно четыре человеческих повода звать
// человека, в порядке печати.
var stopsClasses = []string{"вопрос", "окружение", "автор", "спор"}

// stopsOutsideNotePrefix это начало записки ожидания, которую ставит
// openWait при входе в Blocked: «блок: <причина>». У Check записка другая
// («проверка после выката»), и такой этап тут не в счёт остановок.
const stopsOutsideNotePrefix = "блок: "

// stopEvent это одна остановка: задача, разряд причины, её начало и
// длительность в часах.
type stopEvent struct {
	id    string
	class string
	start time.Time
	hours float64
}

// stopsClassOf достаёт разряд из записки этапа «снаружи». Пустая строка
// значит, что разряда нет. Либо это не блокировка (записка «проверка после
// выката» у Check), либо причина не входит в перечень человеческих разрядов
// (машинное ожидание соседа «слияние:»/«закрытие:» либо взвод, отдельного
// этапа они не заводят).
func stopsClassOf(note string) string {
	reason := strings.TrimPrefix(note, stopsOutsideNotePrefix)
	if reason == note {
		return ""
	}
	first, _, _ := strings.Cut(reason, ":")
	first = strings.TrimSpace(first)
	for _, c := range stopsClasses {
		if first == c {
			return c
		}
	}
	return ""
}

// collectFlushedStops обходит файлы задач (живые и архив, тем же шагом, что
// pilot) и разбирает раздел «Ход работы» на закрытые остановки. Пакет этапов
// уезжает туда при смене статуса. Значит, остановка уже кончилась и несёт
// свою длительность.
func collectFlushedStops(root string) ([]stopEvent, error) {
	base := filepath.Join(root, "docs", "tasks")
	var events []stopEvent
	err := filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "drafts" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".md") {
			return nil
		}
		id := strings.TrimSuffix(d.Name(), ".md")
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		for _, ln := range taskform.SectionLines(string(data), stageSection) {
			s, dur, ok := stage.ParseLine(ln)
			if !ok || !stage.IsWait(s.Kind) {
				continue
			}
			class := stopsClassOf(s.Note)
			if class == "" {
				continue
			}
			events = append(events, stopEvent{id: id, class: class, start: s.Start, hours: dur.Hours()})
		}
		return nil
	})
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("каталога docs/tasks нет, читать нечего: %v", err)
		}
		return nil, err
	}
	return events, nil
}

// collectLiveStops дописывает остановки задач, которые стоят в Blocked прямо
// сейчас. Их пакет этапов ещё не уехал в файл, он лежит живой записью runs.
// Строки рисуются тем же `stage.Lines`, каким флашится пакет на смене
// статуса, только конец подставляется текущим моментом. Длительность
// незакрытой остановки читается тем же разбором `stage.ParseLine`, что и у
// закрытых, без второй копии форматирования дат.
func collectLiveStops(root, runsDir string, b *Board, now time.Time) []stopEvent {
	suffix := "-" + stage.Slug(stage.MainRoot(root)) + ".run"
	var events []stopEvent
	for _, r := range b.Rows {
		rec, err := stage.Load(filepath.Join(runsDir, r.ID+suffix))
		if err != nil || len(rec.Stages) == 0 {
			continue
		}
		for _, ln := range stage.Lines(rec.Stages, now) {
			s, dur, ok := stage.ParseLine(ln)
			if !ok || !stage.IsWait(s.Kind) {
				continue
			}
			class := stopsClassOf(s.Note)
			if class == "" {
				continue
			}
			events = append(events, stopEvent{id: r.ID, class: class, start: s.Start, hours: dur.Hours()})
		}
	}
	return events
}

// medianHours отдаёт медиану простоя по срезу. Своя функция вместо
// переиспользования review.go. Там медиана считает целые минуты, а простой
// парковки живёт в часах с дробной частью (слагаемые ранга черновика сами
// меряли его так: «17 задач простояли... максимум 12.2 часа»).
func medianHours(vals []float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	s := append([]float64(nil), vals...)
	sort.Float64s(s)
	n := len(s)
	if n%2 == 1 {
		return s[n/2]
	}
	return (s[n/2-1] + s[n/2]) / 2
}

// stopsHeadline печатает окно счёта словами. С датой, если она задана,
// либо «за всю историю», когда окна нет.
func stopsHeadline(sinceArg string) string {
	if sinceArg == "" {
		return "остановки конвейера по разрядам парковки, за всю историю"
	}
	return "остановки конвейера по разрядам парковки, с " + sinceArg
}

// cmdStops печатает счёт остановок по четырём разрядам парковки за окно:
// число, доля от всех остановок окна и медиана простоя в часах у каждого.
// Команда ничего не меняет, читает то, что уже лежит на диске.
func cmdStops(root, sinceArg, runsArg string) (string, error) {
	var since time.Time
	if sinceArg != "" {
		s, err := time.Parse(stopsWindowLayout, sinceArg)
		if err != nil {
			return "", fmt.Errorf("--since ждёт дату вида 2026-09-01: %v", err)
		}
		since = s
	}
	events, err := collectFlushedStops(root)
	if err != nil {
		return "", err
	}
	b, err := LoadBoard(boardPath(root))
	if err != nil {
		return "", err
	}
	runsDir := runsArg
	if runsDir == "" {
		runsDir = stage.Dir(stage.Home())
	}
	events = append(events, collectLiveStops(root, runsDir, b, timeNow())...)
	if !since.IsZero() {
		cut := events[:0:0]
		for _, e := range events {
			if !e.start.Before(since) {
				cut = append(cut, e)
			}
		}
		events = cut
	}
	out := []string{stopsHeadline(sinceArg)}
	if len(events) == 0 {
		out = append(out, "в этом окне остановок нет")
		return strings.Join(out, "\n"), nil
	}
	total := len(events)
	for _, class := range stopsClasses {
		var hours []float64
		for _, e := range events {
			if e.class == class {
				hours = append(hours, e.hours)
			}
		}
		share := 100 * float64(len(hours)) / float64(total)
		out = append(out, fmt.Sprintf("%s: %d (%.1f%%), медиана простоя %.1f ч",
			class, len(hours), share, medianHours(hours)))
	}
	out = append(out, fmt.Sprintf("итого остановок: %d", total))
	return strings.Join(out, "\n"), nil
}
