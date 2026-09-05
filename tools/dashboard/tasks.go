package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// Правка строки и файла задачи с дашборда (LLD DK-112, «Экран задачи»):
// заголовок, слагаемые ранга, цена, зависимости в обе стороны и заведение
// файла задачи. Доску правит taskctl подпроцессом, своей записи в
// docs/TASKS.md у сервера нет: правду про формат держит утилита, и её отказы
// (кривая разбивка ранга, цикл зависимостей, линкованный worktree) доезжают
// до экрана её же словами. Порядок строк Backlog утилита выводит из ранга
// сама, поэтому ручки «поставить строку на место N» в API нет.

// taskTextLimit ограничивает тело правки файла задачи: это постановка на
// пару экранов, а не вложение.
const taskTextLimit = 256 << 10

// rankNames это слагаемые ранга по RANKING.md в порядке колонки R. Дашборд
// называет их в отказах, а проверку значений держит taskctl.
var rankNames = [5]string{"серьёзность", "ценность", "неопределённость", "поправка на баг", "рычаг"}

// boardRow это строка доски, как её отдаёт taskctl list --json, плюс секция,
// в которой она нашлась.
type boardRow struct {
	ID    string   `json:"id"`
	Title string   `json:"title"`
	After []string `json:"after,omitempty"`
	// Accept это вид приёмки задачи (agent, mixed, user). Его отдаёт taskctl,
	// разобрав суффикс заголовка строки «[приёмка: ...]» (LLD DK-292): вид
	// назначается на доске, и своего признака дашборд не заводит. Пусто у
	// агентского вида, он умолчание и суффикса не носит.
	Accept string `json:"accept,omitempty"`
	// Order это заказ headless-сессии дословно, той же строкой, что уйдёт
	// агенту (rowOrder, tools/dashboard/runs.go): подсказка кнопки читает
	// готовое поле, а не собирает заказ второй раз. Пусто у строки цели (её
	// виток сочиняет goal-run) и у проверенной строки с пользовательской
	// приёмкой (закрытие идёт без сессии, closeFromCheck).
	Order   string   `json:"order,omitempty"`
	Fail    string   `json:"fail,omitempty"`
	Block   string   `json:"block,omitempty"`
	Type    string   `json:"type"`
	P       string   `json:"p"`
	R       int      `json:"r"`
	RParts  []int    `json:"r_parts"`
	Cost    string   `json:"cost"`
	Link    string   `json:"link"`
	Notes   []string `json:"notes,omitempty"`
	Sect    string   `json:"sect"`
	Section string   `json:"section"`
	// Run это признак идущей работы: чем работа видна (tmux, registry,
	// session, теми же словами, что Via у живой работы) либо gone у строки в
	// работе, за которой живой сессии нет. Пусто у стоящей задачи. Признак
	// приезжает вместе со строкой, потому что собранный на клиенте из строки и
	// списка работ он отвечал ровно на один вопрос, «есть ли сейчас работа с
	// таким же ID», и оборванный конвейер в нём был неотличим от очереди.
	Run string `json:"run,omitempty"`
	// RunBusy говорит, идёт ли по строке ход прямо сейчас. Признак отдельный от
	// Run: работа бывает видна записью реестра или транскриптом, а ход в ней не
	// идёт. По нему экран решает, предлагать ли строке продолжение: вводная
	// продолжения, ушедшая в живой ход, сбивает агента.
	RunBusy bool `json:"run_busy,omitempty"`
	// RunState это состояние строки свёрткой по её рабочим сессиям: busy (ход
	// идёт хоть у одной), waiting (кто-то ждёт человека, а хода нет), idle
	// (сессии на месте, а ход стоит), dead (живой сессии не видно). Пусто у
	// строки, за которой работы нет вовсе. Признак приезжает готовым: собрать
	// его на клиенте нечем, состояния сессий у строки нет.
	RunState string `json:"run_state,omitempty"`
	// RunStopping говорит, что стоп по строке уже нажат и дожимается: ход
	// прерван, а фоновая работа сессии ещё идёт (stopwait.go). Кнопка от этого
	// не меняется, меняется подсказка: работа останавливается, второго нажатия
	// не нужно.
	RunStopping bool `json:"run_stopping,omitempty"`
	// RunChat это разговор с идущим ходом по этой строке: в него и ведёт иконка
	// чата. Пусто у строки без работающей сессии, и иконка тогда открывает
	// адрес задачи, как открывала.
	RunChat string `json:"run_chat,omitempty"`
	// Harness называет подписку, которой закрывать проверенную строку: ту, на
	// которой работу начинали.
	Harness string `json:"harness,omitempty"`
	// Stage это вид деятельности строки словом (разработка, ревью, снаружи,
	// уточнение), а StageSince момент начала этапа в unix-секундах. Отмечает
	// этап конвейер записью за пределами репозитория (DK-338), дашборд её
	// только читает. Пусто у строки без отмеченного этапа и у оборванного
	// этапа, за которым живой сессии нет.
	Stage      string `json:"stage,omitempty"`
	StageSince int64  `json:"stage_since,omitempty"`
	// Waiting это состояние «ждёт человека»: кто кого ждёт, с какой точностью
	// это известно и до какого срока (waiting.go, LLD DK-430, решение 4).
	// Пусто, когда никто никого не ждёт; у непустого источник назван всегда.
	Waiting *Waiting `json:"waiting,omitempty"`
	// Closed это дата закрытия у строки, собранной из архива: на доске такой
	// строки уже нет, а экран задачи её всё равно открывает, потому что в
	// выдачу поиска архив входит наравне с доской.
	Closed string `json:"closed,omitempty"`
}

// Признак идущей работы словами: tmux это сессия конвейера, её «Стоп» снимает
// убийством; chat это наша же работа, идущая в окне разговора, там «Стоп»
// прерывает ход и оставляет разговор жить; registry это цикл цели, поднятый
// другой сессией; session это работа в чужом окне, снимать её отсюда нечем;
// gone это строка в работе, за которой живой сессии нет.
const (
	runTmux   = workViaTmux
	runChat   = "chat"
	runGone   = "gone"
	sectRun   = "in-progress"
	sectCheck = "check"
)

// runRank ранжирует признаки: у одной строки бывает и запись реестра, и своя
// сессия, и решает та, с которой человеку есть что сделать. Снимаемая работа
// сильнее всех, дальше своё окно разговора, дальше чужая работа.
var runRank = map[string]int{runTmux: 3, runChat: 2}

// runMarkOf называет признак одной работы. Род тут не про имя окна, а про то,
// чем работу останавливают: конвейер снимают убийством сессии, а ход в окне
// разговора прерывают, оставляя разговор жить (DoD DK-716).
func runMarkOf(w Work) string {
	if w.Via == workViaTmux {
		return runTmux
	}
	if w.Own && w.Tmux != "" {
		return runChat
	}
	return w.Via
}

// runMarks собирает живые работы по ID: работа без ID это интерактивная
// сессия с неузнанной задачей, строки на доске у неё нет.
// runMarks сводит живые работы в признаки строк: чем работа видна и идёт ли по
// ней ход прямо сейчас.
//
// Работа нашей живой tmux-сессии помечается tmux, каким бы путём её ни узнали.
// Прежде тут стоял голый Via, а у цели работа приезжает реестром: по этому пути
// идущая цель выглядела остановленной, и вместо «Стопа» экран предлагал
// продолжить её посреди хода (замечание пользователя, цель DK-446).
func runMarks(works []Work) map[string]string {
	live := map[string]string{}
	for _, w := range works {
		// Разговор о задаче признака работы строке не даёт: чат её не ведёт, и
		// строка остаётся такой, какой была без него (leadsTask).
		if w.Talk {
			continue
		}
		mark := runMarkOf(w)
		for _, id := range workRows(w) {
			// Сильнейший признак выигрывает: у одной строки бывает и запись
			// реестра, и сессия, и решает та, которую можно снять.
			if runRank[live[id]] > runRank[mark] {
				continue
			}
			live[id] = mark
		}
	}
	return live
}

// stateMarks сводит состояние строки по её рабочим сессиям (DoD DK-716).
// Свёртка простая: ход идёт, если идёт хоть у одной; ждёт человека, если ждёт
// хоть одна и никто не работает; иначе строка простаивает. Признак отдельный
// от того, чем работа видна: запись реестра и транскрипт остаются на месте и
// после конца хода, а продолжение предлагать можно только стоящей строке.
func stateMarks(works []Work) map[string]string {
	state := map[string]string{}
	for _, w := range works {
		if w.Talk || w.Live == "" {
			continue
		}
		for _, id := range workRows(w) {
			if stateRank[state[id]] > stateRank[w.Live] {
				continue
			}
			state[id] = w.Live
		}
	}
	return state
}

// stateRank ранжирует состояния для свёртки: идущий ход старше ожидания,
// ожидание старше простоя, простой старше мёртвой сессии.
var stateRank = map[string]int{workBusy: 4, workWait: 3, workIdle: 2, workDead: 1}

// chatMarks называет разговор, в который ведёт иконка чата у строки: сессию с
// идущим ходом, а при нескольких свежайшую по последней реплике (DoD DK-716).
// Прежде иконка открывала адрес задачи, панель показывала список её чатов, и
// до разговора с идущим ходом человек делал ещё один клик, выбирая его
// глазами по времени. Пусто у строки, за которой не работает ни одна наша сессия: адрес
// задачи там и есть правильный вход, он откроет список или заведёт новый чат.
func chatMarks(works []Work) map[string]string {
	best := map[string]Work{}
	for _, w := range works {
		if w.Talk || w.Session == "" {
			continue
		}
		if w.Live != workBusy && w.Live != workWait {
			continue
		}
		for _, id := range workRows(w) {
			cur, hit := best[id]
			if !hit || stateRank[w.Live] > stateRank[cur.Live] ||
				(w.Live == cur.Live && w.Moved > cur.Moved) {
				best[id] = w
			}
		}
	}
	out := map[string]string{}
	for id, w := range best {
		out[id] = w.Session
	}
	return out
}

// stopMarks называет строки, по которым стоп уже нажат и дожимается. Признак
// собирается по тем же работам, что и остальные: заказ дожима лежит при окне
// разговора, а строка спрашивает про себя.
func stopMarks(works []Work) map[string]bool {
	out := map[string]bool{}
	for _, w := range works {
		if w.Talk || !w.Stopping {
			continue
		}
		for _, id := range workRows(w) {
			out[id] = true
		}
	}
	return out
}

// busyMarks называет строки, по которым ход идёт прямо сейчас.
func busyMarks(works []Work) map[string]bool {
	busy := map[string]bool{}
	for id, st := range stateMarks(works) {
		if st == workBusy {
			busy[id] = true
		}
	}
	return busy
}

// rowRun называет признак строки: у живой работы это то, чем она видна, у
// строки из In progress с нашими кончившимися сессиями gone. Оборванный
// конвейер иначе выглядит штатной очередью: строка стоит в работе, кнопка
// предлагает продолжить, и сказать, идёт ли кто-то по ней прямо сейчас, нечем
// (хвост DK-314).
//
// Третьего ответа, other («исполнителя не видно»), тут больше нет. Он стоял на
// том, что дашборд не узнал сессии, и попадал ровно в те задачи, которые
// человек вёл из дашборда: работа шла в боковом дереве, привязки к строке не
// заводила, и экран объявлял её взятой на другой машине, а плашка формы
// говорила это целым абзацем. По этой машине «взяли в другом месте» не
// проверяется вовсе, а отсутствие исполнителя человек и так видит по
// отсутствию живой работы (решение пользователя).
func rowRun(live, mine map[string]string, id, key string) string {
	if via, hit := live[id]; hit {
		return via
	}
	// Наши сессии задачи есть, просто ни одна не жива: работа наша, и с ней
	// можно разговаривать и продолжать.
	if key == sectRun {
		if _, ours := mine[id]; ours {
			return runGone
		}
	}
	return ""
}

// boardRuns дописывает каждой строке ответа taskctl признак идущей работы и
// заказ headless-сессии (DK-286). Ответ пересобирается по общим картам, а не
// по типу boardRow: сервер отдаёт доску как есть, и часть полей строки (дата
// правки, пометки) он не знает вовсе, а разбор в типизированную строку их бы
// потерял. Неразобранный ответ уезжает нетронутым: без признака строка
// рисуется по-старому, а вот без доски экран пуст.
func boardRuns(raw json.RawMessage, works []Work, mine map[string]string,
	stages map[string]stageMark, wait func(id, sect, block string) (Waiting, bool)) json.RawMessage {
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		return raw
	}
	var secs []map[string]json.RawMessage
	if err := json.Unmarshal(doc["sections"], &secs); err != nil {
		return raw
	}
	live, state, chats := runMarks(works), stateMarks(works), chatMarks(works)
	stopping := stopMarks(works)
	for _, sec := range secs {
		var key string
		json.Unmarshal(sec["key"], &key)
		var rows []map[string]json.RawMessage
		if err := json.Unmarshal(sec["rows"], &rows); err != nil {
			return raw
		}
		for _, row := range rows {
			var id, title, accept string
			json.Unmarshal(row["id"], &id)
			json.Unmarshal(row["title"], &title)
			json.Unmarshal(row["accept"], &accept)
			run := rowRun(live, mine, id, key)
			// Подписка задачи едет строкой Check: закрытие пойдёт той же, на
			// которой работу начинали, и кнопка называет её человеку до
			// нажатия, а не спрашивает выбором.
			if key == sectCheck {
				if h := mine[id]; h != "" {
					mark, err := json.Marshal(h)
					if err != nil {
						return raw
					}
					row["harness"] = mark
				}
			}
			if run != "" {
				mark, err := json.Marshal(run)
				if err != nil {
					return raw
				}
				row["run"] = mark
			}
			// Состояние строки свёрткой по её рабочим сессиям: ход идёт, ждёт
			// человека, простаивает. Признак отдельный от run: работа бывает
			// видна записью реестра или транскриптом, а ход в ней не идёт, и
			// продолжение такой строке предлагать можно.
			if st := state[id]; st != "" {
				mark, err := json.Marshal(st)
				if err != nil {
					return raw
				}
				row["run_state"] = mark
				if st == workBusy {
					if mark, err = json.Marshal(true); err != nil {
						return raw
					}
					row["run_busy"] = mark
				}
			}
			if kind, since := rowStage(stages, run, id); kind != "" {
				mark, err := json.Marshal(kind)
				if err != nil {
					return raw
				}
				row["stage"] = mark
				if mark, err = json.Marshal(since); err != nil {
					return raw
				}
				row["stage_since"] = mark
			}
			if sid := chats[id]; sid != "" {
				mark, err := json.Marshal(sid)
				if err != nil {
					return raw
				}
				row["run_chat"] = mark
			}
			if stopping[id] {
				mark, err := json.Marshal(true)
				if err != nil {
					return raw
				}
				row["run_stopping"] = mark
			}
			if wait != nil {
				var block string
				json.Unmarshal(row["block"], &block)
				if w, ok := wait(id, key, block); ok {
					mark, err := json.Marshal(w)
					if err != nil {
						return raw
					}
					row["waiting"] = mark
				}
			}
			if order := rowOrder(key, id, accept, title); order != "" {
				mark, err := json.Marshal(order)
				if err != nil {
					return raw
				}
				row["order"] = mark
			}
		}
		marked, err := json.Marshal(rows)
		if err != nil {
			return raw
		}
		sec["rows"] = marked
	}
	sections, err := json.Marshal(secs)
	if err != nil {
		return raw
	}
	doc["sections"] = sections
	out, err := json.Marshal(doc)
	if err != nil {
		return raw
	}
	return out
}

// acceptUser это пользовательский вид приёмки: задачу принимает человек
// глазами, и закрытие такой строки с экрана идёт мимо сессии агента.
const acceptUser = "user"

// boardSect это секция доски со строками, как её отдаёт taskctl list --json.
type boardSect struct {
	Key   string     `json:"key"`
	Title string     `json:"title"`
	Rows  []boardRow `json:"rows"`
}

// parseBoardSects разбирает ответ taskctl секциями в их порядке: поиску нужен
// порядок доски, а не алфавит ID.
func parseBoardSects(raw json.RawMessage) ([]boardSect, error) {
	var v struct {
		Sections []boardSect `json:"sections"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, err
	}
	return v.Sections, nil
}

// parseBoardRows раскладывает ответ taskctl по ID: экрану задачи нужна одна
// строка, но заголовки соседей нужны карточке зависимостей.
func parseBoardRows(raw json.RawMessage) (map[string]boardRow, error) {
	sects, err := parseBoardSects(raw)
	if err != nil {
		return nil, err
	}
	rows := map[string]boardRow{}
	for _, sec := range sects {
		for _, row := range sec.Rows {
			row.Sect, row.Section = sec.Key, sec.Title
			rows[row.ID] = row
		}
	}
	return rows, nil
}

// taskctlDo гоняет изменяющую доску команду taskctl в корне проекта. Отказ
// утилиты это 400 с её же словами: рубежи живут в ней, и переписывать их
// дашборд не берётся. Ненайденный бинарь и сорванный срок это 502, там
// сломан не ввод, а окружение.
func taskctlDo(dir string, args ...string) (string, int, error) {
	return taskctlDoIn(dir, "", args...)
}

// taskctlWrite это taskctlDo для команд, которые доску меняют. Память ответа по
// этому проекту снимается тем же движением: правка пришла нашей рукой, и ждать,
// пока её заметит отпечаток файла, незачем. Сброс идёт и после отказа утилиты:
// половина команд успевает тронуть доску до того, как ответить ненулевым кодом.
func (s *server) taskctlWrite(dir string, args ...string) (string, int, error) {
	out, code, err := taskctlDo(dir, args...)
	s.forgetBoard(dir)
	return out, code, err
}

// taskctlWriteIn это taskctlWrite с текстом на входе подпроцесса: так уезжает
// запись накопителя.
func (s *server) taskctlWriteIn(dir, stdin string, args ...string) (string, int, error) {
	out, code, err := taskctlDoIn(dir, stdin, args...)
	s.forgetBoard(dir)
	return out, code, err
}

// taskctlDoIn это taskctlDo с текстом на входе подпроцесса: так уезжает текст
// черновика (разбор аргументов и страж подкоманд его бы не пропустили).
func taskctlDoIn(dir, stdin string, args ...string) (string, int, error) {
	bin := taskctlPath()
	if bin == "" {
		return "", http.StatusBadGateway, errors.New(taskctlMissing())
	}
	out, err := runProcIn(stdin, bin, append(args, "-C", dir)...)
	if err != nil {
		var ee *exec.ExitError
		if !errors.As(err, &ee) {
			// Сорванный срок и прочие обломы запуска это поломка окружения, а
			// не отказ утилиты: ненулевой код она вернуть не успела.
			return "", http.StatusBadGateway, errors.New(procErr(err))
		}
		return "", http.StatusBadRequest, errors.New(procErr(err))
	}
	return strings.TrimSpace(string(out)), 0, nil
}

// taskRow находит проект и строку задачи на его доске; не найдя, отвечает
// словами сам и возвращает ok=false.
func (s *server) taskRow(w http.ResponseWriter, r *http.Request) (found *Project, id string, row boardRow, rows map[string]boardRow, ok bool) {
	return s.taskRowOf(w, r, false)
}

// taskRowOf это тот же разбор со скидкой на архив. Закрытая задача строки на
// доске не имеет, и правки ей ни к чему, а вот прочитать её надо: в выдачу
// поиска архив входит наравне с доской, и нажатие на найденную строку прежде
// упиралось в отказ вместо экрана (замечание 4 четырнадцатого круга POC).
func (s *server) taskRowOf(w http.ResponseWriter, r *http.Request, archive bool) (found *Project, id string, row boardRow, rows map[string]boardRow, ok bool) {
	found = s.findProject(w, r, "задача")
	if found == nil {
		return nil, "", boardRow{}, nil, false
	}
	id = r.PathValue("id")
	if !goalIDRe.MatchString(id) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("%q не похоже на ID задачи", id)})
		return nil, "", boardRow{}, nil, false
	}
	raw, err := s.projectBoard(found.Path)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return nil, "", boardRow{}, nil, false
	}
	rows, err = parseBoardRows(raw)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": fmt.Sprintf("ответ taskctl не разобрался: %v", err)})
		return nil, "", boardRow{}, nil, false
	}
	row, hit := rows[id]
	if !hit {
		if archive {
			if arch, closed := archiveRows(found.Path)[id]; closed {
				return found, id, boardRow{ID: id, Title: arch.Title, Closed: arch.Closed,
					Type: "task", Cost: "-"}, rows, true
			}
		}
		gone := map[string]string{"error": rowGone(found, id)}
		// Строки нет, а запись накопителя с тем же ID лежит на месте: экран
		// задачи по этому слову уходит на экран записи, и упоминание ID в
		// разговоре ведёт туда, куда человек метил.
		if draftHere(found.Path, id) {
			gone["draft"] = id
		}
		writeJSON(w, http.StatusNotFound, gone)
		return nil, "", boardRow{}, nil, false
	}
	return found, id, row, rows, true
}

// rowGone называет судьбу строки, которой на доске нет. Закрытая задача с
// доски уезжает в архив, и «на доске нет строки» на устаревшем экране читается
// как поломка, хотя всё сработало: человек закрыл задачу с одного устройства, а
// нажал с другого (DK-289). Архив читается файлом, тем же порядком, что у
// состава цели.
func rowGone(found *Project, id string) string {
	row, closed := archiveRows(found.Path)[id]
	if !closed {
		// Черновик до грумминга строки на доске не имеет вовсе, и «нет строки»
		// про него сказано верно, но не про то: файл записи лежит на месте, и
		// человек, нажавший на ID в разговоре, шёл именно к ней.
		if draftHere(found.Path, id) {
			_, rel := draftPathOf(found.Path, id)
			return fmt.Sprintf("%s это запись накопителя (%s), строки на доске %s у неё пока нет",
				id, rel, found.Name)
		}
		return fmt.Sprintf("на доске %s нет строки %s", found.Name, id)
	}
	when := ""
	if row.Closed != "" {
		when = " " + row.Closed
	}
	return fmt.Sprintf("задача %s уже закрыта%s и уехала в архив: экран устарел, строки на доске %s больше нет",
		id, when, found.Name)
}

// depRef это соседняя строка в карточке зависимостей: ID с заголовком и
// секцией, чтобы «после» и «держит» читались без похода на доску.
type depRef struct {
	ID      string `json:"id"`
	Title   string `json:"title,omitempty"`
	Sect    string `json:"sect,omitempty"`
	Section string `json:"section,omitempty"`
	R       int    `json:"r,omitempty"`
	P       string `json:"p,omitempty"`
	Note    string `json:"note,omitempty"`
}

func depRefs(ids []string, rows map[string]boardRow) []depRef {
	out := []depRef{}
	for _, id := range ids {
		row, hit := rows[id]
		if !hit {
			// Зависимость без строки это закрытая задача: она уехала в архив,
			// и молчать про неё нельзя, иначе ID выглядит опечаткой.
			out = append(out, depRef{ID: id, Note: "нет на доске: закрыта или в архиве"})
			continue
		}
		out = append(out, depRef{ID: id, Title: row.Title, Sect: row.Sect, Section: row.Section, R: row.R, P: row.P})
	}
	return out
}

// taskDeps спрашивает зависимости строки в обе стороны у самой утилиты:
// «после» лежит в заголовке строки, «держит» считается обратным поиском по
// доске, и оба направления знает dep list --json.
func taskDeps(dir, id string) (after, blocks []string, err error) {
	bin := taskctlPath()
	if bin == "" {
		return nil, nil, errors.New(taskctlMissing())
	}
	out, err := runProc(bin, "dep", "list", id, "--json", "-C", dir)
	if err != nil {
		return nil, nil, fmt.Errorf("taskctl dep list %s: %s", id, procErr(err))
	}
	var v struct {
		After  []string `json:"after"`
		Blocks []string `json:"blocks"`
	}
	if err := json.Unmarshal(out, &v); err != nil {
		return nil, nil, fmt.Errorf("ответ taskctl dep list не разобрался: %v", err)
	}
	return v.After, v.Blocks, nil
}

func taskFileRel(id string) string {
	return filepath.ToSlash(filepath.Join("docs", "tasks", id+".md"))
}

// archiveFile ищет постановку закрытой задачи. Закрытие уносит файл в
// docs/tasks/archive/<год>/, и год этот заранее неизвестен, поэтому каталоги
// перебираются; на своём прежнем месте файл тоже проверяется, потому что
// перенос делает закрытие, а не сам архив.
func archiveFile(projectPath, id string) (string, string, bool) {
	rels := []string{taskFileRel(id)}
	years, _ := os.ReadDir(filepath.Join(projectPath, "docs", "tasks", "archive"))
	for _, y := range years {
		if y.IsDir() {
			rels = append(rels, filepath.ToSlash(filepath.Join("docs", "tasks", "archive", y.Name(), id+".md")))
		}
	}
	for _, rel := range rels {
		if text, err := os.ReadFile(filepath.Join(projectPath, filepath.FromSlash(rel))); err == nil {
			return rel, string(text), true
		}
	}
	return "", "", false
}

// handleTask отдаёт всё, из чего рисуется экран задачи: строку доски,
// зависимости в обе стороны и текст файла задачи.
func (s *server) handleTask(w http.ResponseWriter, r *http.Request) {
	found, id, row, rows, ok := s.taskRowOf(w, r, true)
	if !ok {
		return
	}
	// Закрытая задача читается файлом и только: ни зависимостей, ни идущей
	// работы, ни ожидания человека у неё нет и быть не может.
	if row.Closed != "" {
		resp := map[string]any{"project": found.Name, "id": id, "row": row,
			"after": []depRef{}, "blocks": []depRef{}}
		if rel, text, hit := archiveFile(found.Path, id); hit {
			resp["file"] = rel
			resp["text"] = text
		} else {
			resp["note"] = fmt.Sprintf("файла задачи %s нет: закрытая задача осталась одной строкой архива", taskFileRel(id))
		}
		writeJSON(w, http.StatusOK, resp)
		return
	}
	after, blocks, err := taskDeps(found.Path, id)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	// Признак идущей работы едет строкой и сюда: экран задачи спрашивает о ней
	// то же самое, что список, а доска в этот момент лежит в памяти процесса и
	// второго подпроцесса taskctl не стоит.
	if raw, err := s.projectBoard(found.Path); err == nil {
		view, _ := parseBoardView(raw)
		mine := s.taskChats(found.Path)
		works := s.liveWorks(found.Path, view.Prefix, raw)
		// Экран задачи судит о строке тем же способом, что список: иначе одна и
		// та же задача выглядела бы своей на одном экране и брошенной на
		// другом.
		row.Run = rowRun(runMarks(works), mine, id, row.Sect)
		row.RunState = stateMarks(works)[id]
		row.RunBusy = row.RunState == workBusy
		row.RunChat = chatMarks(works)[id]
		row.RunStopping = stopMarks(works)[id]
		if row.Sect == sectCheck {
			row.Harness = mine[id]
		}
		row.Stage, row.StageSince = rowStage(s.liveStages(found.Path), row.Run, id)
	}
	// Ожидание человека едет и сюда: врезка панели чата берёт вопрос и срок с
	// той же строки, что и чип на карточке, а не вторым разбором признака.
	if w, ok := s.waitLookup(found.Path)(id, row.Sect, row.Block); ok {
		row.Waiting = &w
	}
	row.Order = rowOrder(row.Sect, id, row.Accept, row.Title)
	resp := map[string]any{
		"project": found.Name,
		"id":      id,
		"row":     row,
		"after":   depRefs(after, rows),
		"blocks":  depRefs(blocks, rows),
	}
	rel := taskFileRel(id)
	var fileText string
	if text, err := os.ReadFile(filepath.Join(found.Path, filepath.FromSlash(rel))); err == nil {
		resp["file"] = rel
		resp["text"] = string(text)
		fileText = string(text)
	} else if doc, docText, ok := linkedTaskDoc(found.Path, id, row.Link); ok {
		// Ссылка строки ведёт не в файл задачи, а в другой документ, обычно
		// LLD: постановка у такой строки есть, и «файла нет» про неё врёт.
		resp["doc"] = doc
		resp["docText"] = docText
	} else {
		// Пустой текст без причины неотличим от пустого файла: сервер говорит,
		// что файла нет и чем дыра чинится. С минуты заведения файл кладёт сам
		// add, и дыра это строка до рубежа либо снятый руками файл.
		resp["note"] = fmt.Sprintf("файла задачи %s нет: строка без файла это дыра, чинит её кнопка «Завести файл» (taskctl file)", rel)
	}
	// Блок «Связи»: дизайны задачи и упомянутые в постановке задачи (круг 2
	// POC DK-470).
	if links := taskLinks(found.Path, id, row.Link, fileText, rows, after, blocks); links != nil {
		resp["links"] = links
	}
	writeJSON(w, http.StatusOK, resp)
}

// linkedTaskDoc читает документ по ссылке строки, когда та ведёт не в
// docs/tasks/<ID>.md: у LLD-задач вместо файла задачи в колонке «Ссылка»
// стоит сам дизайн (RULES.board.md, «Трекинг задач» п. 6). Путь отдаётся
// относительно docs/, тем же счётом, что у linkPath и линтера taskctl.
func linkedTaskDoc(projectPath, id, link string) (string, string, bool) {
	target := linkPath(link)
	if target == "" || target == "tasks/"+id+".md" {
		return "", "", false
	}
	full := filepath.Join(projectPath, "docs", filepath.FromSlash(target))
	if info, err := os.Stat(full); err != nil || info.Size() > searchFileMax {
		return "", "", false
	}
	data, err := os.ReadFile(full)
	if err != nil {
		return "", "", false
	}
	return target, string(data), true
}

// lldRefRe ловит путь в docs/lld как угодно записанный: markdown-ссылкой от
// docs/ или docs/tasks/ («lld/...», «../lld/...») и прозой в бэктиках
// («docs/lld/...»), потому что в живых файлах задач встречаются все три
// написания. Та же регулярка стоит в taskctl (view.go): признак один, а
// общего пакета под два вхождения не заводится до утверждения POC.
var lldRefRe = regexp.MustCompile(`(?:^|[^A-Za-z0-9_])(lld/[A-Za-z0-9._-]+\.md)`)

// lldRefs собирает из текста файла задачи ссылки на дизайн: до сих пор путь
// к LLD жил только прозой внутри файла, и с экрана задачи его было не
// открыть. Путь отдаётся относительно docs/, как и у linkedTaskDoc.
func lldRefs(projectPath, text string) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range lldRefRe.FindAllStringSubmatch(text, -1) {
		rel := m[1]
		if seen[rel] {
			continue
		}
		seen[rel] = true
		if _, err := os.Stat(filepath.Join(projectPath, "docs", filepath.FromSlash(rel))); err != nil {
			continue
		}
		out = append(out, rel)
	}
	return out
}

// handleDoc отдаёт документ из docs/ на чтение: экран задачи и поиск
// открывают по нему LLD, до сих пор недостижимый с доски. Ручка читает
// только markdown внутри docs/ и ничего не правит: постановки и дизайны
// правятся в репозитории, а не с телефона.
func (s *server) handleDoc(w http.ResponseWriter, r *http.Request) {
	found := s.findProject(w, r, "документ")
	if found == nil {
		return
	}
	rel := strings.TrimSpace(r.URL.Query().Get("path"))
	if !safeRel(rel) || !strings.HasSuffix(rel, ".md") {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("%q не похоже на путь документа внутри docs/", rel)})
		return
	}
	full := filepath.Join(found.Path, "docs", filepath.FromSlash(rel))
	info, err := os.Stat(full)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "документа docs/" + rel + " нет"})
		return
	}
	if info.Size() > searchFileMax {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": fmt.Sprintf("docs/%s толще потолка %d КБ, читать его на экране всё равно нельзя", rel, searchFileMax>>10)})
		return
	}
	data, err := os.ReadFile(full)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "docs/" + rel + " не прочитался: " + err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"path": "docs/" + rel, "text": string(data)})
}

// rankArg собирает разбивку ранга для taskctl set. Слагаемые правятся по
// одному: пропущенное (null) остаётся прежним, разом идёт весь список.
// Пределы значений проверяет утилита по RANKING.md, дашборд их не повторяет.
func rankArg(parts []*int, cur []int) (string, error) {
	if len(parts) != len(rankNames) {
		return "", fmt.Errorf("в разбивке ранга %d слагаемых, жду %d по RANKING.md: %s",
			len(parts), len(rankNames), strings.Join(rankNames[:], ", "))
	}
	merged := mergeRank(parts, cur)
	out := make([]string, len(merged))
	for i, v := range merged {
		out[i] = strconv.Itoa(v)
	}
	return strings.Join(out, "+"), nil
}

// mergeRank накладывает правку слагаемых на то, что стоит в строке:
// пропущенное (null) остаётся прежним. По этой же склейке проверяется тип,
// потому что правка едет одним запросом.
func mergeRank(parts []*int, cur []int) []int {
	out := make([]int, len(parts))
	for i, p := range parts {
		switch {
		case p != nil:
			out[i] = *p
		case i < len(cur):
			out[i] = cur[i]
		}
	}
	return out
}

// parseRank разбирает разбивку строкой «а+б+в+г+д». Кривую строку тут никто не
// ругает: пределы и формат держит taskctl, дашборду разбор нужен ровно для
// сверки поправки на баг с типом.
func parseRank(s string) []int {
	fields := strings.Split(s, "+")
	out := make([]int, 0, len(fields))
	for _, f := range fields {
		v, err := strconv.Atoi(strings.TrimSpace(f))
		if err != nil {
			return nil
		}
		out = append(out, v)
	}
	return out
}

// bugPartRefusal держит шкалу RANKING.md: поправка на баг это про дефект или
// регресс, у новой работы её не бывает. Правило живёт и на клиенте, но ручка
// повторяет его своими руками: экран не единственный, кто ходит в API.
func bugPartRefusal(typ string, parts []int) string {
	if typ != "task" || len(parts) < 4 || parts[3] == 0 {
		return ""
	}
	return "поправка на баг у типа task не ставится: по RANKING.md " +
		"она про дефект или регресс, а не про новую работу; смени тип на bug"
}

// boardCommitMsg собирает subject коммита доски: ID в subject обязателен, по
// нему находятся коммиты задачи (ядро правил доски).
func boardCommitMsg(id, what string) string {
	return fmt.Sprintf("docs(tasks): %s %s", id, what)
}

// closedTaskPaths собирает пути, которые тронуло закрытие: обе доски и файл
// задачи, уехавший в архив. Год каталога архива не угадывается, а ищется:
// close ставит его по дате закрытия, и около полуночи она разойдётся с датой
// на часах сервера. Старый путь файла едет рядом с новым, потому что
// переименование лежит в индексе после git mv и без него коммит унёс бы
// половину переезда.
func closedTaskPaths(dir, id string) []string {
	paths := []string{
		filepath.ToSlash(filepath.Join("docs", "TASKS.md")),
		filepath.ToSlash(filepath.Join("docs", "TASKS-archive.md")),
	}
	for _, moved := range globSorted(filepath.Join(dir, "docs", "tasks", "archive", "*", id+".md")) {
		rel, err := filepath.Rel(dir, moved)
		if err != nil {
			continue
		}
		paths = append(paths, taskFileRel(id), filepath.ToSlash(rel))
	}
	return paths
}

// closeFromCheck закрывает проверенную задачу прямо с экрана: taskctl close и
// коммит доски, без сессии агента. Так закрывается только пользовательская
// приёмка: человек уже принял работу глазами, и поднимать ради одной команды
// headless-сессию значит платить минутами ожидания и квотой за подтверждение
// того, что и так проверено (DK-289). Агентский вид остаётся за сессией, там
// закрытие это работа.
func (s *server) closeFromCheck(w http.ResponseWriter, found *Project, id string) {
	out, code, err := s.taskctlWrite(found.Path, "close", id)
	if err != nil {
		s.logf("закрытие %s в %s не удалось: %v", id, found.Name, err)
		writeJSON(w, code, map[string]string{"error": err.Error()})
		return
	}
	resp := map[string]string{"id": id, "kind": "close", "message": out}
	if note := commitDocs(found.Path, boardCommitMsg(id, "закрыта с дашборда"),
		closedTaskPaths(found.Path, id)...); note != "" {
		resp["note"] = note
		s.logf("закрытие %s в %s: %s", id, found.Name, note)
	}
	s.logf("закрытие %s в %s: %s", id, found.Name, out)
	writeJSON(w, http.StatusOK, resp)
}

func (s *server) handleTaskPatch(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "чужой Origin"})
		return
	}
	found, id, row, _, ok := s.taskRow(w, r)
	if !ok {
		return
	}
	var body struct {
		Title  string `json:"title"`
		Type   string `json:"type"`
		Rank   string `json:"rank"`
		RParts []*int `json:"r_parts"`
		Cost   string `json:"cost"`
		Link   string `json:"link"`
		// Expect это разбивка, которую отправитель считает сегодняшней. С ней
		// едет откат жеста перетаскивания: пока человек читал строку
		// результата, строку могли поправить из соседней сессии, и молча
		// затирать чужую правку прежними слагаемыми нельзя (LLD DK-328,
		// решение 1).
		Expect []int `json:"expect_r_parts"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "жду JSON с полями title, type, r_parts (или rank), cost, link"})
		return
	}
	if body.Expect != nil && !slices.Equal(body.Expect, row.RParts) {
		writeJSON(w, http.StatusConflict, map[string]string{
			"error": fmt.Sprintf("строку поправили, ранг сейчас %d (%s), откат не применён",
				row.R, rankText(row.RParts))})
		return
	}
	rank := body.Rank
	if body.RParts != nil {
		if rank != "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": "жду либо rank строкой «а+б+в+г+д», либо r_parts списком слагаемых, но не оба сразу"})
			return
		}
		parts, err := rankArg(body.RParts, row.RParts)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		rank = parts
	}
	args := []string{"set", id}
	for _, f := range []struct{ flag, val string }{
		{"--title", body.Title}, {"--type", body.Type}, {"--rank", rank},
		{"--cost", body.Cost}, {"--link", body.Link},
	} {
		if f.val != "" {
			args = append(args, f.flag, f.val)
		}
	}
	if len(args) == 2 {
		// Порядок строк выводится из ранга, и ручки «поставить строку на место
		// N» тут нет: менять нечего это менять нечего.
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "нечего менять, жду title, type, r_parts (или rank), cost или link; " +
				"порядок строк на доске выводится из ранга, ставить строку на место N нечем"})
		return
	}
	// Тип и слагаемые правятся одним запросом, поэтому сверяется то, что
	// получится после правки: пропущенное поле берётся из строки.
	typ := body.Type
	if typ == "" {
		typ = row.Type
	}
	merged := row.RParts
	if rank != "" {
		if parsed := parseRank(rank); parsed != nil {
			merged = parsed
		}
	}
	if refusal := bugPartRefusal(typ, merged); refusal != "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": refusal})
		return
	}
	out, code, err := s.taskctlWrite(found.Path, args...)
	if err != nil {
		s.logf("правка строки %s в %s не удалась: %v", id, found.Name, err)
		writeJSON(w, code, map[string]string{"error": err.Error()})
		return
	}
	resp := map[string]any{"id": id, "message": out}
	// Фактическое место строки едет тем же ответом: клиент считает ранг щели
	// сам, но это превью по той доске, которую видел экран, а порядок строк
	// правит утилита по свежей доске.
	if spot := s.rowSpot(found.Path, id); spot != nil {
		resp["place"] = spot
	}
	if note := commitDocs(found.Path, boardCommitMsg(id, "правка строки с дашборда"),
		filepath.ToSlash(filepath.Join("docs", "TASKS.md"))); note != "" {
		resp["note"] = note
		s.logf("правка строки %s в %s: %s", id, found.Name, note)
	}
	s.logf("правка строки %s в %s: %s", id, found.Name, out)
	writeJSON(w, http.StatusOK, resp)
}

// rankText собирает разбивку ранга словами отказа: та же запись «а+б+в+г+д»,
// какой её знают taskctl и форма задачи.
func rankText(parts []int) string {
	out := make([]string, len(parts))
	for i, v := range parts {
		out[i] = strconv.Itoa(v)
	}
	return strings.Join(out, "+")
}

// rowNear это сосед строки на доске: ID с заголовком и рангом. Заголовок едет
// рядом, чтобы «строка встала между DK-319 и DK-326» читалось без второго
// запроса за доской.
type rowNear struct {
	ID    string `json:"id"`
	Title string `json:"title,omitempty"`
	R     int    `json:"r"`
}

// rowSpot это место строки после правки: свежие слагаемые с суммой, бакет и
// соседи по секции сверху и снизу. Порядок Backlog выводится из ранга, считает
// его taskctl, и место тут читается с уже переписанной доски, а не
// предсказывается.
type rowSpot struct {
	Sect   string   `json:"sect"`
	R      int      `json:"r"`
	RParts []int    `json:"r_parts"`
	P      string   `json:"p"`
	Above  *rowNear `json:"above,omitempty"`
	Below  *rowNear `json:"below,omitempty"`
}

func near(row boardRow) *rowNear {
	return &rowNear{ID: row.ID, Title: row.Title, R: row.R}
}

// rowSpot читает место строки заново: доска уже переписана утилитой, отпечаток
// файла сменился, и память процесса отдаёт свежий ответ сама. Место не
// прочиталось, значит его в ответе нет вовсе: правка при этом прошла, и
// выдуманные соседи хуже их отсутствия.
func (s *server) rowSpot(dir, id string) *rowSpot {
	raw, err := s.projectBoard(dir)
	if err != nil {
		return nil
	}
	sects, err := parseBoardSects(raw)
	if err != nil {
		return nil
	}
	for _, sec := range sects {
		for i, row := range sec.Rows {
			if row.ID != id {
				continue
			}
			spot := &rowSpot{Sect: sec.Key, R: row.R, RParts: row.RParts, P: row.P}
			if i > 0 {
				spot.Above = near(sec.Rows[i-1])
			}
			if i+1 < len(sec.Rows) {
				spot.Below = near(sec.Rows[i+1])
			}
			return spot
		}
	}
	return nil
}

// handleTaskFilePost чинит строке без файла дыру руками утилиты: taskctl file
// создаёт docs/tasks/<ID>.md по шаблону и чинит ссылку в строке доски. Со
// минуты заведения файл кладёт сам add, и ручка достаётся строкам до рубежа
// либо со снятым руками файлом.
func (s *server) handleTaskFilePost(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "чужой Origin"})
		return
	}
	found, id, _, _, ok := s.taskRow(w, r)
	if !ok {
		return
	}
	out, code, err := taskctlDo(found.Path, "file", id)
	if err != nil {
		s.logf("заведение файла %s в %s не удалось: %v", id, found.Name, err)
		writeJSON(w, code, map[string]string{"error": err.Error()})
		return
	}
	resp := map[string]string{"id": id, "file": taskFileRel(id), "message": out}
	if note := commitDocs(found.Path, boardCommitMsg(id, "файл задачи заведён с дашборда"),
		filepath.ToSlash(filepath.Join("docs", "TASKS.md")), taskFileRel(id)); note != "" {
		resp["note"] = note
		s.logf("заведение файла %s в %s: %s", id, found.Name, note)
	}
	s.logf("файл задачи %s в %s: %s", id, found.Name, out)
	writeJSON(w, http.StatusOK, resp)
}

// handleTaskFilePut кладёт текст файла задачи целиком. Своей команды на это у
// taskctl нет, файл пишется напрямую, как сообщение витку в messages.go, но
// заводится он всё равно утилитой: та же команда чинит ссылку в строке.
func (s *server) handleTaskFilePut(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "чужой Origin"})
		return
	}
	found, id, _, _, ok := s.taskRow(w, r)
	if !ok {
		return
	}
	var body struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, taskTextLimit)).Decode(&body); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": fmt.Sprintf("текст длиннее предела %d КБ: в файле задачи лежит постановка, а не вложение", taskTextLimit/1024)})
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "жду JSON {\"text\": \"...\"}"})
		return
	}
	rel := taskFileRel(id)
	path := filepath.Join(found.Path, filepath.FromSlash(rel))
	if !isFile(path) {
		writeJSON(w, http.StatusNotFound, map[string]string{
			"error": fmt.Sprintf("файла задачи %s нет: сначала заведи его кнопкой «Завести файл» (taskctl file), она же чинит ссылку в строке", rel)})
		return
	}
	if strings.TrimSpace(body.Text) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "пустой текст затёр бы постановку: жду JSON {\"text\": \"...\"}"})
		return
	}
	text := strings.TrimRight(body.Text, "\n") + "\n"
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": fmt.Sprintf("файл задачи не записался: %v", err)})
		return
	}
	resp := map[string]string{"id": id, "file": rel,
		"message": fmt.Sprintf("текст %s записан", rel)}
	if note := commitDocs(found.Path, boardCommitMsg(id, "правка файла задачи с дашборда"), rel); note != "" {
		resp["note"] = note
		s.logf("правка файла %s в %s: %s", id, found.Name, note)
	}
	s.logf("файл задачи %s в %s переписан с дашборда", id, found.Name)
	writeJSON(w, http.StatusOK, resp)
}

func (s *server) handleTaskDepAdd(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "чужой Origin"})
		return
	}
	found, id, _, _, ok := s.taskRow(w, r)
	if !ok {
		return
	}
	var body struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil || body.ID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "жду JSON {\"id\": \"DK-NNN\"}: после какой задачи делается эта"})
		return
	}
	dep := body.ID
	if !goalIDRe.MatchString(dep) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("%q не похоже на ID задачи", dep)})
		return
	}
	s.depChange(w, found, id, dep, "add")
}

func (s *server) handleTaskDepRm(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "чужой Origin"})
		return
	}
	found, id, _, _, ok := s.taskRow(w, r)
	if !ok {
		return
	}
	dep := r.PathValue("dep")
	if !goalIDRe.MatchString(dep) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("%q не похоже на ID задачи", dep)})
		return
	}
	s.depChange(w, found, id, dep, "rm")
}

// depChange гоняет dep add/rm и коммитит доску. Цикл зависимостей, живую
// строку с незакрытой зависимостью и прочие рубежи держит утилита, её слова
// и уезжают на экран.
func (s *server) depChange(w http.ResponseWriter, found *Project, id, dep, sub string) {
	out, code, err := s.taskctlWrite(found.Path, "dep", sub, id, dep)
	if err != nil {
		s.logf("зависимость %s от %s в %s (%s) не прошла: %v", id, dep, found.Name, sub, err)
		writeJSON(w, code, map[string]string{"error": err.Error()})
		return
	}
	what := fmt.Sprintf("зависимость от %s с дашборда", dep)
	if sub == "rm" {
		what = fmt.Sprintf("зависимость от %s снята с дашборда", dep)
	}
	resp := map[string]string{"id": id, "dep": dep, "message": out}
	if note := commitDocs(found.Path, boardCommitMsg(id, what),
		filepath.ToSlash(filepath.Join("docs", "TASKS.md"))); note != "" {
		resp["note"] = note
		s.logf("зависимость %s от %s в %s: %s", id, dep, found.Name, note)
	}
	s.logf("зависимость %s от %s в %s: %s", id, dep, found.Name, out)
	writeJSON(w, http.StatusOK, resp)
}
