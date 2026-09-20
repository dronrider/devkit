package main

// Сквозные статьи свода (DK-913). Этап это работа субагента по строке задачи, и
// им меряется исполнитель. Диспетчер, груминг черновика, виток цели и стенд не
// ложатся ни в один этап: диспетчер идёт параллельно всем, постановка идёт до
// строки в работе, виток и тики фона не привязаны к задаче вовсе, а сессии
// стенда живут в снесённом временном доме. Поэтому расход головных сессий
// раскладывается статьями поверх свода DK-912, а стенд приносит свои числа сам,
// отметкой прогона в разделе «Проверка».

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/dronrider/devkit/internal/sessions"
	"github.com/dronrider/devkit/internal/spend"
	"github.com/dronrider/devkit/internal/stage"
	"github.com/dronrider/devkit/internal/taskform"
)

// Виды статей.
const (
	itemOrch  = "оркестрация"
	itemSetup = "постановка"
	itemBack  = "фон"
	itemStand = "стенд"
)

// Ключи статей, которые не ID записи. «Без привязки» это ход диспетчера пачки
// до первой работы с ID: сессия ведёт несколько задач, и делить такой ход
// поровну значило бы потерять то, ради чего статья заведена. «Машина» это фон
// без цели: догон доски, тики сторожа, безголовый заход дашборда.
const (
	keyLoose   = "без привязки"
	keyMachine = "машина"
)

// Причины, по которым чисел у статьи нет. Заход второго харнеса выглядит
// отсюда так же, как стёртый журнал: журналов вне ~/.claude на машине нет.
const (
	whyNoTranscript = "транскрипта головной сессии нет в журналах харнеса claude: второй харнес либо журнал стёрт"
	whyNoCarrier    = "носителя в реестре сессий нет, безголовый заход от разговора человека не отличить"
)

// spendIDRe ловит ID записи в тексте работы журнала агентов («Исполнитель
// DK-910», «Груминг черновика DK-913»). Префикс доски тут любой: досок на
// машине не одна.
var spendIDRe = regexp.MustCompile(`\b[A-Z]{2,10}-[0-9]{1,6}\b`)

// spendSetupWords это слова, которыми работа над записью называет себя в
// журнале агентов. По ним ход головной сессии ложится статьёй «постановка», а
// не «оркестрация»: у груминга черновика и нарезки цели своя статья, и заводят
// их до того, как у задачи появится хоть один этап.
var spendSetupWords = []string{"груминг", "черновик", "нарезк", "постановк", "интервью"}

// spendEvent это событие журнала агентов, названное ID записи: запуск работы,
// её конец, команда оболочки с ID в тексте. Текст события дальше разбора не
// едет: наружу свод отдаёт суммы.
type spendEvent struct {
	at    time.Time
	id    string
	setup bool
}

// spendSetup это этап «постановка» живой записи: чья сессия его вела, по какой
// записи и когда. Он и есть первый признак статьи «постановка».
type spendSetup struct {
	id      string
	session string
	from    time.Time
	to      time.Time
}

// spendCrew это машинное окружение свода: реестр чатов, журнал агентов, реестр
// целей и этапы постановки. Всё это лежит на машине и переживает задачу.
type spendCrew struct {
	home   string
	binds  map[string]sessions.Bind
	events map[string][]spendEvent
	goals  map[string]string
	setups []spendSetup
	turns  map[string][]spend.Turn
	blind  map[string]bool
}

// newSpendCrew собирает окружение свода один раз за заход.
func newSpendCrew(home, root string) *spendCrew {
	c := &spendCrew{
		home:   home,
		binds:  map[string]sessions.Bind{},
		events: map[string][]spendEvent{},
		goals:  map[string]string{},
		turns:  map[string][]spend.Turn{},
		blind:  map[string]bool{},
	}
	for sid, recs := range sessions.LoadAll(home) {
		c.binds[sid] = sessions.Last(recs)
	}
	c.events = spendEvents(home)
	c.goals = spendGoals(home)
	for _, rec := range stage.List(home, root) {
		for i, s := range rec.Stages {
			if s.Kind != stage.Setup || s.Session == "" {
				continue
			}
			to := s.End
			if to.IsZero() && i+1 < len(rec.Stages) {
				to = rec.Stages[i+1].Start
			}
			c.setups = append(c.setups, spendSetup{id: rec.ID, session: s.Session, from: s.Start, to: to})
		}
	}
	return c
}

// spendEvents читает журнал агентов ~/.devkit/agents.log: кто из головных
// сессий какую работу и когда поднимал. Сессия там обрезана до восьми знаков, и
// сверка идёт по префиксу. Непонятая строка пропускается: журнал общий,
// писателей у него несколько, и чужая строка не повод рвать свод.
func spendEvents(home string) map[string][]spendEvent {
	out := map[string][]spendEvent{}
	data, err := os.ReadFile(spendAgentsLog(home))
	if err != nil {
		return out
	}
	for _, ln := range strings.Split(string(data), "\n") {
		f := strings.Fields(ln)
		if len(f) < 4 || f[1] != "сессия" {
			continue
		}
		at, err := time.ParseInLocation(sessions.Stamp, f[0], time.Local)
		if err != nil {
			continue
		}
		text, ok := cutSpendText(ln)
		if !ok {
			continue
		}
		id := spendIDRe.FindString(text)
		if id == "" {
			continue
		}
		sid := f[2]
		out[sid] = append(out[sid], spendEvent{at: at, id: id, setup: spendSetupText(text)})
	}
	for sid := range out {
		ev := out[sid]
		sort.Slice(ev, func(i, j int) bool { return ev[i].at.Before(ev[j].at) })
	}
	return out
}

// spendAgentsLog называет журнал агентов в доме.
func spendAgentsLog(home string) string {
	return filepath.Join(home, ".devkit", "agents.log")
}

// cutSpendText отдаёт текст работы из строки журнала: всё, что стоит в ёлочках
// после слова «текст». Дальше разбора текст не едет.
func cutSpendText(line string) (string, bool) {
	i := strings.Index(line, "текст «")
	if i < 0 {
		return "", false
	}
	rest := line[i+len("текст «"):]
	if j := strings.LastIndex(rest, "»"); j >= 0 {
		rest = rest[:j]
	}
	return rest, true
}

// spendSetupText отвечает, говорит ли текст работы о постановке.
func spendSetupText(text string) bool {
	low := strings.ToLower(text)
	for _, w := range spendSetupWords {
		if strings.Contains(low, w) {
			return true
		}
	}
	return false
}

// spendGoals читает реестр целей ~/.devkit/goals: какая сессия ведёт цикл какой
// цели. Запись перезаписывается каждым заходом цикла, поэтому цели узнаётся
// последний виток, а прежние идут фоном машины.
func spendGoals(home string) map[string]string {
	out := map[string]string{}
	paths, _ := filepath.Glob(filepath.Join(home, ".devkit", "goals", "*.watch"))
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		goal, sess := "", ""
		for _, ln := range strings.Split(string(data), "\n") {
			k, v, ok := strings.Cut(ln, "=")
			if !ok {
				continue
			}
			switch strings.TrimSpace(k) {
			case "goal":
				goal = strings.TrimSpace(v)
			case "session":
				sess = strings.TrimSpace(v)
			}
		}
		if goal != "" && sess != "" {
			out[sess] = goal
		}
	}
	return out
}

// place называет статью и ключ одного хода головной сессии. Порядок признаков
// такой: этап постановки записи старше всего, дальше последнее по времени
// событие журнала агентов с ID, дальше носитель сессии. Ход, которому не нашлось
// ничего, статьи не получает вовсе и уходит в строку «вне статей»: так выглядит
// разговор человека без привязки.
func (c *spendCrew) place(sid string, at time.Time) (string, string, bool) {
	for _, s := range c.setups {
		if s.session != sid || at.Before(s.from) {
			continue
		}
		if s.to.IsZero() || at.Before(s.to) {
			return itemSetup, s.id, true
		}
	}
	if ev, ok := c.lastEvent(sid, at); ok {
		if ev.setup {
			return itemSetup, ev.id, true
		}
		return itemOrch, ev.id, true
	}
	return c.carrier(sid)
}

// carrier относит ход, за которым не стоит ни одна работа с ID, к носителю
// сессии: цикл цели идёт фоном на ID цели, безголовый заход фоном машины,
// конвейер задачи её оркестрацией, а диспетчер пачки без задачи в реестре
// строкой «оркестрация без привязки».
func (c *spendCrew) carrier(sid string) (string, string, bool) {
	if goal, ok := c.goals[sid]; ok {
		return itemBack, goal, true
	}
	b := c.binds[sid]
	if b.Carrier == "виток" {
		return itemBack, spendMachineKey(b), true
	}
	if b.Task != "" {
		return itemOrch, b.Task, true
	}
	if b.Carrier != "" {
		return itemBack, spendMachineKey(b), true
	}
	if len(c.events[spendShort(sid)]) > 0 {
		return itemOrch, keyLoose, true
	}
	return "", "", false
}

// spendMachineKey называет фон машины проектом, из корня которого заход шёл.
func spendMachineKey(b sessions.Bind) string {
	name := b.Project
	if name == "" && b.Tree != "" {
		name = filepath.Base(b.Tree)
	}
	if name == "" {
		return keyMachine
	}
	return keyMachine + " " + name
}

// lastEvent отдаёт последнее событие сессии, случившееся не позже хода.
func (c *spendCrew) lastEvent(sid string, at time.Time) (spendEvent, bool) {
	ev := c.events[spendShort(sid)]
	best, ok := spendEvent{}, false
	for _, e := range ev {
		if e.at.After(at) {
			break
		}
		best, ok = e, true
	}
	return best, ok
}

// spendShort обрезает сессию до восьми знаков: столько от неё пишет журнал
// агентов.
func spendShort(sid string) string {
	if len(sid) > 8 {
		return sid[:8]
	}
	return sid
}

// sessionTurns читает ходы головной сессии, запоминая прочитанное: одна сессия
// ведёт несколько задач, и за заход свода её спрашивают не раз. Второе значение
// false значит, что транскрипта нет вовсе.
func (c *spendCrew) sessionTurns(sid string) ([]spend.Turn, bool) {
	if t, ok := c.turns[sid]; ok {
		return t, true
	}
	if c.blind[sid] {
		return nil, false
	}
	path := c.binds[sid].Transcript
	if path == "" {
		if p, ok := spend.SessionFile(c.home, sid); ok {
			path = p
		}
	}
	if path == "" {
		c.blind[sid] = true
		return nil, false
	}
	turns, err := spend.ReadTurns(path)
	if err != nil {
		c.blind[sid] = true
		return nil, false
	}
	c.turns[sid] = turns
	return turns, true
}

// spendItem это статья свода: вид, ключ, расход и сколько источников за ним
// стоит.
type spendItem struct {
	kind  string
	key   string
	usage spend.Usage
	parts int
	blind int
	why   string
}

// spendItems это копилка статей по паре «вид, ключ».
type spendItems struct {
	at    map[string]*spendItem
	order []string
}

func newSpendItems() *spendItems {
	return &spendItems{at: map[string]*spendItem{}}
}

func (s *spendItems) add(kind, key string, u spend.Usage) {
	it := s.get(kind, key)
	it.usage = it.usage.Add(u)
}

func (s *spendItems) get(kind, key string) *spendItem {
	k := kind + "\x00" + key
	if it, ok := s.at[k]; ok {
		return it
	}
	it := &spendItem{kind: kind, key: key}
	s.at[k] = it
	s.order = append(s.order, k)
	return it
}

// list отдаёт статьи в порядке видов, а внутри вида по выводу: дорогое первым.
func (s *spendItems) list() []*spendItem {
	var out []*spendItem
	for _, k := range s.order {
		out = append(out, s.at[k])
	}
	rank := map[string]int{itemOrch: 0, itemSetup: 1, itemBack: 2, itemStand: 3}
	sort.SliceStable(out, func(i, j int) bool {
		if rank[out[i].kind] != rank[out[j].kind] {
			return rank[out[i].kind] < rank[out[j].kind]
		}
		if out[i].usage.Output != out[j].usage.Output {
			return out[i].usage.Output > out[j].usage.Output
		}
		return out[i].key < out[j].key
	})
	return out
}

// spendStands читает отметки стенда файла задачи: расход прогона кладёт туда
// сам obeycheck, пока цел его временный дом. Прогон без `--task` следа не
// оставляет и в свод не входит вовсе.
func spendStands(root, id string, p spendPeriod) (spend.Usage, int) {
	path, ok := spendTaskFile(root, id)
	if !ok {
		return spend.Usage{}, 0
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return spend.Usage{}, 0
	}
	var out spend.Usage
	runs := 0
	for _, m := range taskform.StandMarks(string(data)) {
		if !m.Tokens() {
			continue
		}
		if p.set && !p.holds(m.When) {
			continue
		}
		out = out.Add(spend.Usage{Turns: m.Turns, Output: m.Output, Input: m.Input, CacheRead: m.CacheRead})
		runs++
	}
	return out, runs
}

// taskItems раскладывает статьями расход головных сессий, причастных к задаче.
// Сессия причастна, когда она поднимала работу с этим ID, ведёт задачу по
// реестру чатов, писала её этап либо вела по ней постановку. Ход, ушедший
// другой задаче той же сессии, сюда не попадает: делёж идёт по ходам, а не по
// сессиям целиком.
func (c *spendCrew) taskItems(id string, stages []spendStage, p spendPeriod) (*spendItems, spend.Usage, int) {
	items := newSpendItems()
	var loose spend.Usage
	blind := 0
	for _, sid := range c.taskSessions(id, stages) {
		turns, ok := c.sessionTurns(sid)
		if !ok {
			blind++
			continue
		}
		seen := map[string]bool{}
		for _, t := range turns {
			if p.set && !p.holds(t.At) {
				continue
			}
			kind, key, ok := c.place(sid, t.At)
			if !ok {
				continue
			}
			switch {
			case key == id:
				items.add(kind, key, t.Usage)
				if !seen[kind] {
					seen[kind] = true
					items.get(kind, key).parts++
				}
			case key == keyLoose:
				loose = loose.Add(t.Usage)
			}
		}
	}
	return items, loose, blind
}

// taskSessions называет головные сессии, причастные к задаче, в устойчивом
// порядке.
func (c *spendCrew) taskSessions(id string, stages []spendStage) []string {
	pick := map[string]bool{}
	for sid, b := range c.binds {
		if b.Task == id {
			pick[sid] = true
		}
		for _, e := range c.events[spendShort(sid)] {
			if e.id == id {
				pick[sid] = true
				break
			}
		}
	}
	for _, st := range stages {
		if st.session != "" {
			pick[st.session] = true
		}
	}
	for _, s := range c.setups {
		if s.id == id {
			pick[s.session] = true
		}
	}
	out := make([]string, 0, len(pick))
	for sid := range pick {
		out = append(out, sid)
	}
	sort.Strings(out)
	return out
}

// periodItems раскладывает статьями головные потоки машины за срез. Потоки
// субагентов сюда не приезжают: они целиком принадлежат этапам задач.
func (c *spendCrew) periodItems(streams []spend.Stream, p spendPeriod) (*spendItems, spend.Usage, spend.Usage, int) {
	items := newSpendItems()
	var outside, total spend.Usage
	blind := 0
	for _, st := range streams {
		if st.Session == "" {
			continue
		}
		turns, err := spend.ReadTurns(st.Path)
		if err != nil {
			blind++
			continue
		}
		for _, t := range turns {
			if p.set && !p.holds(t.At) {
				continue
			}
			total = total.Add(t.Usage)
			kind, key, ok := c.place(st.Session, t.At)
			if !ok {
				outside = outside.Add(t.Usage)
				continue
			}
			items.add(kind, key, t.Usage)
		}
	}
	return items, outside, total, blind
}
