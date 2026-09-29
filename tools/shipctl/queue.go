package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

// Очередь слияний (DK-1218, третья строка DoD цели DK-1084).
//
// Слияние идёт минутами: ребейз, полный прогон в свежем дереве, выкат. Замок
// конвейера один, и второй заход раньше отбивался мгновенно. Пока слияния
// жал человек, этого хватало, а семь готовых веток 28 сентября шли весь
// день: событию «замок освободился» получателя нет, и повторные заходы
// диспетчеры писали одноразовыми циклами в scratchpad. Один вёл файл
// очереди под nohup, второй дёргал ship раз в десять секунд, третий ждал
// спада нагрузки. Отбитую ветку такой цикл выкидывал независимо от причины.
//
// Механика devkit устроена иначе, и состоит она из трёх частей.
//
// Состав очереди производный. Это строки In progress, у которых есть ветка с
// кодом, по убыванию ранга. Отдельного «поставить в очередь» нет: строка
// попадает туда, доведённая до готовности, и уходит слиянием. Готовность
// спрашивается не отдельным списком проверок, а самим слиянием: ворота merge
// (ревью, тесты, сценарий, стенд, обкатка, рёбра) стоят до ребейза и
// прогона, поэтому неготовая строка отказывает дёшево, без единого теста.
//
// Наклейки машинные. Число повторов, причина прошлого отказа и снятие с
// очереди живут в .devkit/merge-queue.json рядом с журналом прогонов: это
// состояние конвейера этой машины, а не доски, и в git ему делать нечего.
//
// Разливает тик. `devkitctl watch` зовёт `shipctl queue --drain` раз в пять
// минут, тем же порядком, каким DK-306 отдал ему разлив поезда: у devkit нет
// долгоживущего процесса кроме тика, и заводить демона ради очереди значило
// бы заводить ему надзор, перезапуск и журнал, которые у тика уже есть
// (решение «место очереди», DK-1218).

// mergeQueuePath это наклейки очереди внутри .devkit.
const mergeQueuePath = ".devkit/merge-queue.json"

// queueRetryLimit это потолок повторов одной ветки. Три, столько же держит
// catchUpLimit на том же классе препятствия: первые заходы это случайность
// занятой машины, а три подряд значат сломанный компонент, и разбирать его
// надо человеку (решение «потолок повторов», DK-1218).
const queueRetryLimit = 3

// queueMark это наклейка одной задачи: сколько раз её отбили, чем в
// последний раз и снята ли она с очереди.
type queueMark struct {
	Tries  int       `json:"tries"`
	Last   string    `json:"last,omitempty"`
	At     time.Time `json:"at,omitempty"`
	Held   bool      `json:"held,omitempty"`
	Reason string    `json:"reason,omitempty"`
}

// queueState это весь файл наклеек.
type queueState struct {
	Tasks map[string]*queueMark `json:"tasks"`
}

// loadQueue читает наклейки. Файла нет значит наклеек нет, и это штатное
// состояние свежего проекта. Порченый файл тоже не отказ: наклейки это
// наблюдение поверх производного состава, и потерять их дешевле, чем встать
// конвейером.
func loadQueue(root string) *queueState {
	st := &queueState{Tasks: map[string]*queueMark{}}
	data, err := os.ReadFile(filepath.Join(root, mergeQueuePath))
	if err != nil {
		return st
	}
	if err := json.Unmarshal(data, st); err != nil || st.Tasks == nil {
		return &queueState{Tasks: map[string]*queueMark{}}
	}
	return st
}

// saveQueue пишет наклейки. Без .devkit писать некуда, как и журналу
// прогонов: обвязку заводит devkitctl.
func saveQueue(root string, st *queueState) error {
	dir := filepath.Join(root, ".devkit")
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return nil
	}
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(root, mergeQueuePath), append(data, '\n'), 0o644)
}

// queueLockPath это короткий замок наклеек, свой и отдельный от замка
// конвейера (.devkit/ship.lock): взятие замка конвейера ради наклейки
// вставало бы в очередь за чужим слиянием на двадцать минут, а самой
// наклейке нужно продержать замок миллисекунды (DK-1229).
const queueLockPath = mergeQueuePath + ".lock"

// queueMutate это цикл прочитать-поменять-записать наклеек под своим
// коротким замком: держится он на весь цикл и отпускается сразу же. Занятость
// тут ждётся, а не отбивается (LOCK_EX без LOCK_NB), потому что держатель
// уходит за миллисекунды, и повтору незачем отваливаться отказом только
// оттого, что тик и ручной разлив сошлись на одном файле. Без .devkit писать
// некуда, как и в saveQueue, поэтому замок не заводится вовсе, и fn работает
// на пустом состоянии.
func queueMutate(root string, fn func(st *queueState)) {
	dir := filepath.Join(root, ".devkit")
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		fn(loadQueue(root))
		return
	}
	f, err := os.OpenFile(filepath.Join(root, queueLockPath), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		// Замок не открылся значит писать особо некуда: наклейка это
		// наблюдение поверх производного состава, а не критичное состояние, и
		// цикл идёт без него, тем же соображением, что и порченый файл в
		// loadQueue.
		fn(loadQueue(root))
		return
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		// Замок не взялся (диск переполнен, файловая система не держит
		// flock): та же наблюдательная природа наклейки, что и у не
		// открывшегося файла замка чуть выше, цикл идёт без него.
		fn(loadQueue(root))
		return
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	st := loadQueue(root)
	fn(st)
	saveQueue(root, st)
}

// mark достаёт наклейку задачи, заводя пустую.
func (st *queueState) mark(id string) *queueMark {
	if m := st.Tasks[id]; m != nil {
		return m
	}
	m := &queueMark{}
	st.Tasks[id] = m
	return m
}

// queueItem это строка очереди: задача, её ранг и наклейка.
type queueItem struct {
	ID    string
	Title string
	Rank  int
	Mark  queueMark
}

// queueRows собирает состав очереди: строки In progress с веткой, по
// убыванию ранга. Равный ранг держит порядок доски, поэтому сортировка
// устойчивая: у пары строк с одним числом порядок не должен плясать от
// прогона к прогону.
//
// Ветка ищется по имени, а не по выложенному дереву: копию окна переключают
// на другую задачу, и дозревающая ветка живёт одним именем. Слияние такую
// строку не возьмёт и скажет про это словами, а очередь обязана её показать,
// иначе строка пропадала бы из виду вместе с причиной.
func queueRows(root string, b *board) ([]queueItem, error) {
	names, err := git(root, "branch", "--list", "--format=%(refname:short)")
	if err != nil {
		return nil, err
	}
	branches := strings.Split(names, "\n")
	st := loadQueue(root)
	var items []queueItem
	for _, r := range b.sects["in-progress"] {
		has := false
		for _, n := range branches {
			if branchOfTask(strings.TrimSpace(n), r.ID) {
				has = true
				break
			}
		}
		if !has {
			continue
		}
		items = append(items, queueItem{ID: r.ID, Title: r.Title, Rank: r.Rank, Mark: *st.mark(r.ID)})
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].Rank > items[j].Rank })
	return items, nil
}

// queueLines печатает очередь строками: позиция, задача, ранг, повторы и
// причина прошлого отказа. Снятая строка идёт с пометкой и позиции не
// занимает: она в составе не участвует, пока её не вернули.
func queueLines(items []queueItem) []string {
	var out []string
	pos := 0
	for _, it := range items {
		if it.Mark.Held {
			out = append(out, fmt.Sprintf("    снята %s (ранг %d): %s", it.ID, it.Rank, it.Mark.Reason))
			continue
		}
		pos++
		line := fmt.Sprintf("    %d. %s (ранг %d)", pos, it.ID, it.Rank)
		if it.Mark.Tries > 0 {
			line += fmt.Sprintf(", повторов %d из %d: %s", it.Mark.Tries, queueRetryLimit, it.Mark.Last)
		}
		out = append(out, line)
	}
	return out
}

// queueRetry ставит ветку в хвост: повтор считается, причина запоминается, и
// на потолке строка уходит из очереди. Второе значение говорит, осталась ли
// она в составе. Уход по потолку оставляет тот же след, что уход по своей
// красноте: запись в файле задачи и уведомление. Без них автор узнавал бы про
// отставленную ветку только грепом по журналу сторожка (замечание ревью круга
// 1, DK-1218).
func queueRetry(root, id, why string) (string, bool) {
	var m queueMark
	queueMutate(root, func(st *queueState) {
		mk := st.mark(id)
		mk.Tries++
		mk.Last, mk.At = why, time.Now()
		if mk.Tries >= queueRetryLimit {
			mk.Held, mk.Reason = true, fmt.Sprintf("повторов %d из %d, последний отказ: %s", mk.Tries, queueRetryLimit, why)
		}
		m = *mk
	})
	if m.Held {
		return fmt.Sprintf("%s ушла из очереди слияний: %s", id, m.Reason) + queueTrace(root, id, m.Reason), false
	}
	return fmt.Sprintf("%s встала в хвост очереди слияний, повтор %d из %d: %s", id, m.Tries, queueRetryLimit, why), true
}

// queueHold снимает ветку с очереди: повторять нечего, нужны руки.
func queueHold(root, id, why string) string {
	queueMutate(root, func(st *queueState) {
		mk := st.mark(id)
		mk.Held, mk.Reason, mk.At = true, why, time.Now()
		mk.Last = why
	})
	return fmt.Sprintf("%s снята с очереди слияний: %s", id, why)
}

// queueFree возвращает ветку в очередь и обнуляет повторы: препятствие
// разобрано руками, и прошлые отказы к следующему заходу отношения не имеют.
func queueFree(root, id string) string {
	queueMutate(root, func(st *queueState) {
		mk := st.mark(id)
		mk.Held, mk.Reason, mk.Tries, mk.Last = false, "", 0, ""
		mk.At = time.Now()
	})
	return fmt.Sprintf("%s вернулась в очередь слияний, повторы обнулены", id)
}

// queueClear убирает наклейку целиком: задача слита, и её счёт повторов
// следующему кругу доработки не нужен.
func queueClear(root, id string) {
	queueMutate(root, func(st *queueState) {
		if _, ok := st.Tasks[id]; !ok {
			return
		}
		delete(st.Tasks, id)
	})
}

// QueueParams это параметры команды queue.
type QueueParams struct {
	Drain  bool
	Hold   string // ID, снимаемый с очереди
	Free   string // ID, возвращаемый в очередь
	Reason string // причина снятия
	Test   string
	Push   bool
	Say    func(string)
}

// noQueue это маркер исхода «разливать нечего», по которому тик отличает
// пустой заход от значимого, тем же приёмом, что «разлив не нужен» у ship.
const noQueue = "очередь слияний пуста"

// cmdQueue печатает очередь, снимает и возвращает ветки, а под --drain
// сливает одну готовую.
func cmdQueue(root string, p QueueParams) (string, error) {
	primary, _, err := primaryRoot(root)
	if err != nil {
		return "", err
	}
	root = primary
	if corpActive(root) {
		return "", corpRefused("queue")
	}
	switch {
	case p.Hold != "" && p.Free != "":
		return "", fmt.Errorf("--hold и --free вместе не имеют смысла: строка либо снята с очереди, либо в ней")
	case p.Hold != "":
		if p.Reason == "" {
			return "", fmt.Errorf("снятию с очереди нужна причина: --reason \"чем снята\"")
		}
		return queueHold(root, p.Hold, p.Reason), nil
	case p.Free != "":
		return queueFree(root, p.Free), nil
	case p.Drain:
		return queueDrain(root, p)
	}
	b, err := loadBoard(root)
	if err != nil {
		return "", err
	}
	items, err := queueRows(root, b)
	if err != nil {
		return "", err
	}
	if len(items) == 0 {
		return noQueue + ": ни одной строки In progress с веткой", nil
	}
	return "очередь слияний:\n" + strings.Join(queueLines(items), "\n"), nil
}

// queueDrain сливает одну готовую ветку из головы очереди. Одну, а не все:
// слияние девкита с полным прогоном идёт от десяти до двадцати минут, и тик
// приходит каждые пять, поэтому следующая ветка едет следующим заходом, а не
// держит один вызов на час. Строки, чьи ворота готовности не сошлись,
// перебираются насквозь и стоят дёшево: ворота merge отказывают до ребейза и
// без единого теста.
//
// Отказ разбирается по причине, и исходов три.
//
//   - Своя краснота, конфликт ребейза: повторять нечего, строка уходит из
//     очереди с записью в файл задачи.
//   - Нагрузочное падение вне диффа: строка встаёт в хвост, повтор считается,
//     разлив идёт дальше по составу. Машина освободится, и следующий заход
//     пройдёт.
//   - Прочая краснота вне диффа: краснеет сам main, и гнать через него
//     остальные ветки незачем. Повтор считается, а проход останавливается:
//     один прогон на тик вместо всего состава.
func queueDrain(root string, p QueueParams) (string, error) {
	b, err := loadBoard(root)
	if err != nil {
		return "", err
	}
	// Сломанный прод держит очередь целиком, и разлив тут молчит своим
	// маркером, а не перебирает состав, упираясь в один и тот же отказ.
	if fails := failedChecks(b); len(fails) > 0 {
		return noQueue + ": " + brokenProd(fails[0]), nil
	}
	items, err := queueRows(root, b)
	if err != nil {
		return "", err
	}
	var notes, skipped []string
	for _, it := range items {
		if it.Mark.Held {
			continue
		}
		merged, note, stop, skip := queueTry(root, it.ID, p)
		if note != "" {
			notes = append(notes, note)
		}
		if merged {
			queueClear(root, it.ID)
			return strings.Join(notes, "\n"), nil
		}
		if stop {
			return strings.Join(notes, "\n"), nil
		}
		if skip != "" {
			skipped = append(skipped, it.ID+": "+skip)
		}
	}
	if len(notes) > 0 {
		return strings.Join(notes, "\n"), nil
	}
	if len(skipped) > 0 {
		return fmt.Sprintf("%s: готовых веток нет.\n    %s", noQueue, strings.Join(skipped, "\n    ")), nil
	}
	return noQueue + ": ни одной строки In progress с веткой", nil
}

// queueTry ведёт один заход слияния и разбирает его исход. merged говорит,
// что ветка уехала в main, note это строка отчёта, stop просит остановить
// проход, а skip это причина, по которой строка оказалась неготовой: она
// печатается хвостом отчёта, иначе пустой заход выглядел бы бездействием без
// повода.
func queueTry(root, id string, p QueueParams) (merged bool, note string, stop bool, skip string) {
	say := p.Say
	if say == nil {
		say = func(string) {}
	}
	say("очередь слияний: беру " + id)
	// Ожидания замка тут нет нарочно. Тик сам и есть цикл повтора: он приходит
	// раз в пять минут, а собственное предыдущее слияние держит замок десять
	// или двадцать. Ожидание внутри захода копило бы ждущие shipctl, отодвигало
	// разлив поезда и подъём проверяющего того же захода, а на 35-й минуте
	// потолок подпроцесса снял бы заход посреди слияния, оставив ребейз без
	// fast-forward (замечание ревью круга 1, DK-1218).
	out, mergeErr := cmdMerge(root, MergeParams{
		ID: id, Test: p.Test, Train: true, Push: p.Push, Say: say,
	})
	if mergeErr == nil {
		return true, id + " слита очередью: " + firstLine(out), false, ""
	}
	var red *redError
	switch {
	case errors.As(mergeErr, &red) && red.Own:
		return false, queueOutOfLine(root, id, red.Why), true, ""
	case errors.As(mergeErr, &red) && red.Load:
		// Нагрузочная краснота и есть признак загруженной машины, и добивать её
		// полным прогоном по остальному составу значит давить ту же машину,
		// от чего лечит цель DK-1084. Ветка уже в хвосте, повтор посчитан, и
		// следующую возьмёт следующий тик через пять минут (замечание ревью
		// круга 1, DK-1218).
		note, _ = queueRetry(root, id, red.Why)
		stageQueue(root, []string{id}, "очередь слияний: "+note)
		return false, note + "; разлив остановлен: машина занята, следующую ветку возьмёт следующий заход", true, ""
	case errors.As(mergeErr, &red):
		note, _ = queueRetry(root, id, red.Why)
		stageQueue(root, []string{id}, "очередь слияний: "+note)
		return false, note + "; разлив остановлен: краснота вне диффа задачи это красный main, и гнать через него остальные ветки незачем", true, ""
	case errors.Is(mergeErr, errRebaseConflict):
		return false, queueOutOfLine(root, id, "конфликт ребейза, разводить руками"), true, ""
	case errors.Is(mergeErr, errLockBusy):
		return false, noQueue + ": " + mergeErr.Error(), true, ""
	}
	// Всё остальное это ворота готовности: строка ещё не доведена, и разлив
	// идёт дальше по составу. Отказ ворот стоит дёшево (они проверяются до
	// ребейза и до единого теста), а причина уезжает хвостом отчёта.
	return false, "", false, firstLine(mergeErr.Error())
}

// queueOutOfLine выводит строку из очереди наклейкой и оставляет след.
func queueOutOfLine(root, id, why string) string {
	return queueHold(root, id, why) + queueTrace(root, id, why)
}

// queueTrace оставляет след снятия там, где его найдёт автор: запись в файле
// задачи уезжает коммитом ветки, уведомление уходит сразу. Наклейка машинная
// и живёт отдельно, её ставит queueHold либо потолок повторов в queueRetry.
// Дерева у ветки может не быть (копию окна переключили), и тогда остаётся
// наклейка с уведомлением: писать в файл на main нельзя, он стал бы
// незакоммиченной правкой и отбил следующее слияние.
func queueTrace(root, id, why string) string {
	note := ""
	if wt, err := taskWorktree(root, id); err == nil && wt != nil {
		if err := appendRecord(wt.Path, id, "снята с очереди слияний: "+why); err == nil {
			rel := "docs/tasks/" + id + ".md"
			if _, err := git(wt.Path, "commit", "-m",
				fmt.Sprintf("docs(tasks): %s снята с очереди слияний", id), "--", rel); err == nil {
				note += ", запись в " + rel
			}
		}
	}
	return note + notify(root, id, "очередь слияний: "+id+" снята", why)
}

// firstLine берёт первую строку многострочного отчёта: в строку разлива идёт
// исход, а подробности остаются в отчёте самого слияния.
func firstLine(s string) string {
	first, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return first
}

// sayNow печатает ход работы сразу, не дожидаясь отчёта команды. Ожидание
// замка идёт минутами, и молчащий процесс в эти минуты неотличим от
// зависшего.
func sayNow(line string) {
	fmt.Println(line)
	os.Stdout.Sync()
}
