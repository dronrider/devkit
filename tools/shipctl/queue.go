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

	"github.com/dronrider/devkit/internal/trace"
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
	Tries           int         `json:"tries"`
	Code            string      `json:"code,omitempty"`
	Last            string      `json:"last,omitempty"`
	At              time.Time   `json:"at,omitempty"`
	Held            bool        `json:"held,omitempty"`
	Reason          string      `json:"reason,omitempty"`
	Block           *queueBlock `json:"block,omitempty"`
	RegcheckPending bool        `json:"regcheck_pending,omitempty"`
}

const queueRegcheck = "regcheck"

type queueBlock struct {
	Code   string    `json:"code"`
	Since  time.Time `json:"since"`
	Reason string    `json:"reason"`
	Owner  string    `json:"owner"`
	Resume string    `json:"resume"`
	Head   string    `json:"head,omitempty"`
	Base   string    `json:"base,omitempty"`
	Log    string    `json:"log,omitempty"`
}

func (b queueBlock) line() string {
	return fmt.Sprintf("ожидает %s: %s. Ответственный: %s. Повтор: %s",
		sinceWords(time.Since(b.Since)), b.Reason, b.Owner, b.Resume)
}

func queueSetBlock(root, id string, block queueBlock) (queueBlock, bool) {
	changed := false
	queueMutate(root, func(st *queueState) {
		mark := st.mark(id)
		if block.Code == queueRegcheck {
			mark.RegcheckPending = true
		}
		if previous := mark.Block; previous != nil {
			block.Since = previous.Since
			if *previous == block {
				return
			}
		}
		block.Since = time.Now()
		mark.Block = &block
		changed = true
	})
	return block, changed
}

func queueClearBlock(root, id string) {
	queueMutate(root, func(st *queueState) {
		if mark := st.Tasks[id]; mark != nil {
			mark.Block = nil
			mark.RegcheckPending = false
		}
	})
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
	f, err := os.CreateTemp(dir, "ship.lock.queue-state-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, writeErr := f.Write(append(data, '\n'))
	if err := errors.Join(writeErr, f.Close()); err != nil {
		return err
	}
	return os.Rename(f.Name(), filepath.Join(root, mergeQueuePath))
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
// строку берёт после первого коммита за пределами файлов доски. Одна
// постановка остаётся разработкой, без попытки слияния и эскалации.
func queueRows(root string, b *board) ([]queueItem, error) {
	names, err := git(root, "branch", "--list", "--format=%(refname:short)")
	if err != nil {
		return nil, err
	}
	branches := strings.Split(names, "\n")
	main, err := mainBranch(root)
	if err != nil {
		return nil, err
	}
	current, err := git(root, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return nil, err
	}
	st := loadQueue(root)
	var items []queueItem
	for _, r := range b.sects["in-progress"] {
		var code string
		for _, branch := range branches {
			if !branchOfTask(branch, r.ID) {
				continue
			}
			code, err = queueCode(root, main, branch)
			if err != nil {
				return nil, err
			}
			if code != "" {
				break
			}
		}
		if code == "" {
			continue
		}
		mark := *st.mark(r.ID)
		if mark.Code != "" {
			branch := current
			if current == main {
				wt, err := taskWorktree(root, r.ID)
				if err != nil {
					return nil, err
				}
				branch = ""
				if wt != nil {
					branch = wt.Branch
				}
			}
			if branchOfTask(branch, r.ID) {
				code, err := queueCode(root, main, branch)
				if err != nil {
					return nil, err
				}
				if code != "" && code != mark.Code {
					mark = queueMark{Block: mark.Block, RegcheckPending: mark.RegcheckPending}
				}
			}
		}
		items = append(items, queueItem{ID: r.ID, Title: r.Title, Rank: r.Rank, Mark: mark})
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].Rank > items[j].Rank })
	return items, nil
}

// queueCode пропускает коммит с записью о снятии и другие коммиты доски.
func queueCode(root, main, ref string) (string, error) {
	commits, err := git(root, "log", main+".."+ref, "--format=%H")
	if err != nil {
		return "", err
	}
	for _, sha := range strings.Fields(commits) {
		files, err := git(root, "show", "--name-only", "--pretty=", sha)
		if err != nil {
			return "", err
		}
		if !boardOnly(root, files) {
			return sha, nil
		}
	}
	return "", nil
}

// sinceWords складывает возраст одной строкой словами: минуты в первые часы,
// часы до суток, дальше дни. Возраст печатается у снятой строки, чтобы разбор
// её откладывался не бесконечно (DK-1322).
func sinceWords(d time.Duration) string {
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%d мин", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d ч", int(d.Hours()))
	default:
		return fmt.Sprintf("%d дн", int(d.Hours()/24))
	}
}

// queueHeldLine печатает снятую строку: позицию в очереди она не занимает, а
// возраст и причина говорят, что за работа стоит и почему её нет в разливе.
// Возраст печатается рядом с причиной: снятая строка неделями без него
// неотличима от вчерашней, и разбор её откладывается до бесконечности
// (DK-1322).
func queueHeldLine(it queueItem) string {
	age := ""
	if !it.Mark.At.IsZero() {
		age = ", " + sinceWords(time.Since(it.Mark.At)) + " назад"
	}
	return fmt.Sprintf("снята %s (ранг %d%s): %s", it.ID, it.Rank, age, it.Mark.Reason)
}

// queueLines печатает очередь строками: позиция, задача, ранг, повторы и
// причина прошлого отказа. Снятая строка идёт с пометкой и позиции не
// занимает: она в составе не участвует, пока её не вернули.
func queueLines(items []queueItem) []string {
	var out []string
	pos := 0
	for _, it := range items {
		if it.Mark.Held {
			out = append(out, "    "+queueHeldLine(it))
			continue
		}
		pos++
		line := fmt.Sprintf("    %d. %s (ранг %d)", pos, it.ID, it.Rank)
		if it.Mark.Tries > 0 {
			line += fmt.Sprintf(", повторов %d из %d: %s", it.Mark.Tries, queueRetryLimit, it.Mark.Last)
		}
		if it.Mark.Block != nil {
			line += ", " + it.Mark.Block.line()
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
func queueRetry(root, id, why, before, code string) (string, bool) {
	var m queueMark
	var heldNow bool
	queueMutate(root, func(st *queueState) {
		mk := st.mark(id)
		if mk.Held && (mk.Code == "" || mk.Code == before) {
			m = *mk
			return
		}
		if mk.Last != why || mk.Code != before {
			*mk = queueMark{}
		}
		mk.Tries++
		mk.Last, mk.Code, mk.At = why, code, time.Now()
		if mk.Tries >= queueRetryLimit {
			heldNow = true
			mk.Held, mk.Reason = true, fmt.Sprintf("повторов %d из %d, последний отказ: %s", mk.Tries, queueRetryLimit, why)
		}
		m = *mk
	})
	if m.Held {
		line := fmt.Sprintf("%s ушла из очереди слияний: %s", id, m.Reason)
		if heldNow {
			line += queueTrace(root, id, m.Reason)
		}
		return line, false
	}
	return fmt.Sprintf("%s встала в хвост очереди слияний, повтор %d из %d: %s", id, m.Tries, queueRetryLimit, why), true
}

// queueHold снимает ветку с очереди: повторять нечего, нужны руки.
func queueHold(root, id, why string) string {
	queueMutate(root, func(st *queueState) {
		mk := st.mark(id)
		mk.Held, mk.Reason, mk.At = true, why, time.Now()
		mk.Last, mk.Code, mk.Block = why, "", nil
	})
	return fmt.Sprintf("%s снята с очереди слияний: %s", id, why)
}

// queueFree возвращает ветку в очередь и обнуляет повторы: препятствие
// разобрано руками, и прошлые отказы к следующему заходу отношения не имеют.
func queueFree(root, id string) string {
	queueMutate(root, func(st *queueState) {
		mk := st.mark(id)
		*mk = queueMark{At: time.Now(), RegcheckPending: mk.RegcheckPending}
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

type queueReport struct {
	ID      string      `json:"id,omitempty"`
	Line    string      `json:"line"`
	Failed  bool        `json:"failed,omitempty"`
	Waiting bool        `json:"waiting,omitempty"`
	Held    bool        `json:"held,omitempty"`
	Merged  bool        `json:"merged,omitempty"`
	Block   *queueBlock `json:"block,omitempty"`
	Changed bool        `json:"changed,omitempty"`
}

func cmdQueueJSON(root string, p QueueParams) (string, error) {
	reports := []queueReport{}
	p.Report = func(r queueReport) { reports = append(reports, r) }
	p.Say = nil
	line, err := cmdQueue(root, p)
	if err != nil {
		line = err.Error()
		reports = append(reports, queueReport{Line: line, Failed: true})
	}
	data, marshalErr := json.Marshal(struct {
		Line  string        `json:"line"`
		Items []queueReport `json:"items"`
	}{line, reports})
	return string(data), marshalErr
}

// QueueParams это параметры команды queue.
type QueueParams struct {
	Report func(queueReport)
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
		// Ручное снятие оставляет тот же след, что и снятие разливом: запись
		// с поручением в файле задачи и уведомление. Раньше hold был тих:
		// наклейка ставилась, а автор узнавал о снятии только грепом очереди
		// (DK-1322).
		return queueOutOfLine(root, p.Hold, p.Reason), nil
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
	// Два разлива не должны читать очередь между прогоном и записью отказа.
	f, err := os.OpenFile(filepath.Join(root, lockPath+".queue"), os.O_CREATE|os.O_RDWR, 0o644)
	switch {
	case err == nil:
		defer f.Close()
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return noQueue + ": другой разлив уже идёт", nil
		}
		if err != nil {
			return "", err
		}
	case !os.IsNotExist(err):
		return "", err
	}
	b, err := loadBoard(root)
	if err != nil {
		return "", err
	}
	items, err := queueRows(root, b)
	if err != nil {
		return "", err
	}
	var notes, skipped, held []string
	for _, it := range items {
		if it.Mark.Held {
			// Снятая строка стоит в составе без попытки слияния, и молчание
			// о ней приравнивало очередь из одних снятых к пустой: тик печатал
			// «очередь слияний пуста», и стоящая работа была видна только
			// отдельным вызовом queue (DK-1334).
			line := queueHeldLine(it)
			held = append(held, line)
			if p.Report != nil {
				p.Report(queueReport{ID: it.ID, Line: line, Held: true})
			}
			continue
		}
		merged, note, stop, skip := queueTry(root, it, p)
		if note != "" {
			notes = append(notes, note)
		}
		if merged {
			queueClear(root, it.ID)
			return strings.Join(append(notes, skipped...), "\n"), nil
		}
		if stop {
			return strings.Join(append(notes, skipped...), "\n"), nil
		}
		if skip != "" {
			skipped = append(skipped, it.ID+": "+skip)
		}
	}
	if len(notes) > 0 {
		return strings.Join(notes, "\n"), nil
	}
	if len(skipped) > 0 {
		tail := append(skipped, held...)
		return fmt.Sprintf("%s: готовых веток нет.\n    %s", noQueue, strings.Join(tail, "\n    ")), nil
	}
	if len(held) > 0 {
		return "очередь слияний: готовых веток нет, стоит снятая работа\n    " + strings.Join(held, "\n    "), nil
	}
	return noQueue + ": ни одной строки In progress с веткой", nil
}

// queueTry ведёт один заход слияния и разбирает его исход. merged говорит,
// что ветка уехала в main, note это строка отчёта, stop просит остановить
// проход, а skip это причина, по которой строка оказалась неготовой: она
// печатается хвостом отчёта, иначе пустой заход выглядел бы бездействием без
// повода.
func queueTry(root string, item queueItem, p QueueParams) (merged bool, note string, stop bool, skip string) {
	id := item.ID
	report := queueReport{ID: id, Failed: true}
	defer func() {
		if p.Report == nil {
			return
		}
		report.Line = note
		if skip != "" {
			report.Line = skip
		}
		if merged {
			report.Failed, report.Merged = false, true
		}
		p.Report(report)
	}()
	say := p.Say
	if say == nil {
		say = func(string) {}
	}
	started := false
	// Ожидания замка тут нет нарочно. Тик сам и есть цикл повтора: он приходит
	// раз в пять минут, а собственное предыдущее слияние держит замок десять
	// или двадцать. Ожидание внутри захода копило бы ждущие shipctl, отодвигало
	// разлив поезда и подъём проверяющего того же захода, а на 35-й минуте
	// потолок подпроцесса снял бы заход посреди слияния, оставив ребейз без
	// fast-forward (замечание ревью круга 1, DK-1218).
	out, mergeErr := cmdMerge(root, MergeParams{
		ID: id, Test: p.Test, Train: true, Push: p.Push, Say: say,
		retryRegcheck: item.Mark.RegcheckPending,
		queueStart: func() {
			started = true
			if item.Mark.Block != nil || item.Mark.RegcheckPending {
				queueClearBlock(root, id)
			}
			say("очередь слияний: беру " + id)
		},
	})
	if mergeErr == nil {
		return true, id + " слита очередью: " + firstLine(out), false, ""
	}
	var red *redError
	var waiting *dependencyWait
	var regcheck *trace.RegcheckPending
	saveBlock := func(block queueBlock) {
		block.Owner = "исполнитель " + id
		block, report.Changed = queueSetBlock(root, id, block)
		report.Block = &block
	}
	switch {
	case errors.As(mergeErr, &regcheck):
		report.Failed, report.Waiting = false, true
		reason := "нет зелёного regcheck от " + regcheck.Since.Format(time.RFC3339)
		saveBlock(queueBlock{
			Code: queueRegcheck, Reason: reason, Head: regcheck.Head, Base: regcheck.Base, Log: regcheck.LogPath,
			Resume: "успешный regcheck на актуальной базе в дереве задачи",
		})
		return false, "", false, report.Block.line()
	case errors.As(mergeErr, &waiting):
		report.Failed, report.Waiting = false, true
		reason := firstLine(mergeErr.Error())
		saveBlock(queueBlock{Code: "dependency", Reason: reason, Resume: "выполнена предпосылка"})
		return false, "", false, reason
	case errors.As(mergeErr, &red) && red.Own:
		return false, queueOutOfLine(root, id, red.Why), true, ""
	case errors.As(mergeErr, &red):
		before, err := queueCode(root, red.base, red.before)
		if err != nil {
			return false, "", false, firstLine(err.Error())
		}
		code := before
		if red.tested != red.before {
			code, err = queueCode(root, red.base, red.tested)
			if err != nil {
				return false, "", false, firstLine(err.Error())
			}
		}
		note, _ = queueRetry(root, id, red.Why, before, code)
		stageQueue(root, []string{id}, "очередь слияний: "+note)
		if red.Load {
			return false, note + "; разлив остановлен: машина занята, следующую ветку возьмёт следующий заход", true, ""
		}
		return false, note + "; разлив остановлен: краснота вне диффа задачи это красный main, и гнать через него остальные ветки незачем", true, ""
	case errors.Is(mergeErr, errRebaseConflict):
		return false, queueOutOfLine(root, id, "конфликт ребейза, разводить руками"), true, ""
	case errors.Is(mergeErr, errLockBusy):
		report.Failed, report.Waiting = false, true
		return false, noQueue + ": " + mergeErr.Error(), true, ""
	}
	reason := firstLine(mergeErr.Error())
	if !started {
		saveBlock(queueBlock{Code: "preflight", Reason: reason, Resume: "после устранения причины на следующем тике"})
	}
	return false, "", false, reason
}

// queueOutOfLine выводит строку из очереди наклейкой и оставляет след.
func queueOutOfLine(root, id, why string) string {
	return queueHold(root, id, why) + queueTrace(root, id, why)
}

// queueAssign формулирует поручение по снятой строке: разобрать причину
// снятия и вернуть строку в очередь. Одна форма на все причины, потому что
// разбор причины это дело исполнителя, а поручение одно: починить и вернуть.
// Вместе с записью и уведомлением оно доводит исход до исполнителя, а
// поднимает строку с этим поручением lift_rows (DK-1322).
func queueAssign(id string) string {
	return fmt.Sprintf("поручение: разобрать снятие и вернуть строку в очередь (shipctl queue --free %s)", id)
}

// queueTrace оставляет след снятия там, где его найдёт автор: запись с
// поручением в файле задачи уезжает коммитом ветки, то же поручение уходит в
// уведомлении. Уведомление без поручения не кончается разбором: автор
// узнаёт, что строка снята, но не то, что с ней делать, и строка стоит
// мёртвым грузом (DK-1322). Наклейка машинная и живёт отдельно, её ставит
// queueHold либо потолок повторов в queueRetry. Дерева у ветки может не быть
// (копию окна переключили), и тогда остаётся наклейка с уведомлением: писать
// в файл на main нельзя, он стал бы незакоммиченной правкой и отбил
// следующее слияние.
func queueTrace(root, id, why string) string {
	assign := queueAssign(id)
	note := ""
	if wt, err := taskWorktree(root, id); err == nil && wt != nil {
		if err := appendRecord(wt.Path, id, "снята с очереди слияний: "+why+"; "+assign); err == nil {
			rel := "docs/tasks/" + id + ".md"
			if _, err := git(wt.Path, "commit", "-m",
				fmt.Sprintf("docs(tasks): %s снята с очереди слияний", id), "--", rel); err == nil {
				note += ", запись в " + rel
			}
		}
	}
	return note + notify(root, id, "очередь слияний: "+id+" снята", why+"; "+assign)
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
