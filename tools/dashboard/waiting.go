package main

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/dronrider/devkit/internal/chat"
)

// Состояние «ждёт человека» у строки доски (LLD DK-430, решение 4). Задача
// встаёт на первом же вопросе агента, и до этого поля человек узнавал про
// простой только чипом парковки, то есть уже после того, как заход кончился.
// Источников три, и они ранжированы от точного к запасному: живой признак
// ожидания .ask во входе задачи, машинный разряд причины блока «вопрос: ...» и
// повод idle_prompt из журнала уведомителя по сессии, которую реестр чатов
// связал с задачей. Пустое поле значит, что никто никого не ждёт, а у
// непустого источник назван всегда: «спросил агент» и «повод из журнала
// уведомителя» это разной точности знание, и путать их нельзя.

// Машинные разряды источника, от точного к запасному.
const (
	waitAsk    = "ask"
	waitWidget = "widget"
	waitParked = "parked"
	waitIdle   = "idle"
)

// Состояние словом: его и видно чипом на строке доски.
const (
	waitAskState  = "ждёт ответа"
	waitWidState  = "клиент ждёт ответа"
	waitParkState = "припаркована вопросом"
	waitIdleState = "сессия ждёт ввода"
)

// Подпись источника словами: по ней человек понимает, насколько знанию верить.
const (
	waitAskNote  = "спросил агент"
	waitWidNote  = "вопрос в панели клиента"
	waitParkNote = "парковка"
	waitIdleNote = "повод из журнала уведомителя"
)

// Waiting это состояние ожидания строки: состояние словом, машинный разряд
// источника, подпись, вопросы пачкой в том порядке, в каком их задал
// инструмент ожидания, момент начала и срок в unix-секундах и ждущая сессия.
// Времена в тех же единицах, что stage_since у этапа: считает по ним клиент,
// и второго разбора времени на экране не заводится.
type Waiting struct {
	State     string   `json:"state"`
	Source    string   `json:"source"`
	Note      string   `json:"note"`
	Questions []string `json:"questions,omitempty"`
	Since     int64    `json:"since,omitempty"`
	Until     int64    `json:"until,omitempty"`
	Session   string   `json:"session,omitempty"`
	// Task называет задачу, по которой задан вопрос. Строке доски это поле не
	// нужно, у неё задача и есть сама строка, а в разговоре, раздавшем работу,
	// вопрос приходит без строки, и относить его без имени задачи не к чему.
	Task string `json:"task,omitempty"`
}

// askWaiting читает первый источник: признак ожидания во входе задачи
// основного чекаута, туда его кладёт taskctl ask. Сессия в поле это внешняя
// сессия реестра, потому что у субагента своей нет, и ответ панели уходит по
// тому же адресу, по которому ждёт инструмент.
func askWaiting(tree, id string, now time.Time) (Waiting, bool) {
	path := chat.AskPath(tree, chat.TaskName(id))
	a, ok := chat.ReadAsk(path)
	if !ok {
		return Waiting{}, false
	}
	if !a.Live(now) {
		// Срок вышел, а признак лежит: ждущего за ним нет, снять признак ему
		// было нечем (убитый ход, упавший процесс). Брошенное ожидание снимает
		// страховка сторожка, а показывать его живым нельзя: чип с вышедшим
		// сроком врал бы, что ответа ещё ждут. Признак без срока (DK-715) тут
		// не попадает вовсе: Live для него всегда true.
		return Waiting{}, false
	}
	w := Waiting{State: waitAskState, Source: waitAsk, Note: waitAskNote,
		Until: a.UnixUntil(), Session: a.Session, Task: id,
		// Варианты идут теми же строками, что у розданного вопроса. Прежде тут
		// брался один q.Text, и вопрос своей задачи приезжал на экран без
		// вариантов: человек видел «куда катить» и ни одного ответа (DK-652).
		Questions: askLines(a.Questions)}
	// Начало берётся у самого файла: своего поля старта в признаке нет, а
	// положен он ровно в тот момент, когда заход встал.
	if fi, err := os.Stat(path); err == nil {
		w.Since = fi.ModTime().Unix()
	}
	return w, true
}

// handedAsk это живой признак ожидания, найденный по сессии, а не по строке
// доски: имя разговора нужно ответу, разобранный признак экрану.
type handedAsk struct {
	Name  string
	Ask   chat.Ask
	Since int64
}

// handedAsks находит вопросы, адресованные разговору sid, под какой бы задачей
// они ни лежали. Субагент ходит с ID внешней сессии, и признак, положенный им
// из чужого дерева, несёт сессию раздавшего разговора; у делегата второй
// подписки сессия своя, и его вопрос приводит к родителю реестр. До этого
// скана вопрос был виден только в разговоре задачи, а человек сидел в том
// разговоре, откуда работу раздал, и молчал до самой парковки (живые случаи
// DK-517, DK-543 и слияние цели DK-397). Вопросов бывает несколько, пачка
// исполнителей спрашивает вразнобой: отвечают им по одному, ближний срок
// первым.
func handedAsks(projPath, sid string, b sessionBinds, now time.Time) []handedAsk {
	if sid == "" {
		return nil
	}
	entries, err := os.ReadDir(chat.Root(projPath))
	if err != nil {
		return nil
	}
	var out []handedAsk
	for _, e := range entries {
		name, found := strings.CutSuffix(e.Name(), chat.AskSuffix)
		if !found || e.IsDir() {
			continue
		}
		path := chat.AskPath(projPath, name)
		a, ok := chat.ReadAsk(path)
		if !ok || !a.Live(now) {
			continue
		}
		if !askForChat(a, sid, b) {
			continue
		}
		h := handedAsk{Name: name, Ask: a}
		if fi, err := os.Stat(path); err == nil {
			h.Since = fi.ModTime().Unix()
		}
		out = append(out, h)
	}
	// Порядок по времени файла, а не по сроку: признак без срока (DK-715) не
	// назовёт, кто спросил раньше, а mtime это тот же момент, когда встал сам
	// вопрос.
	sort.Slice(out, func(i, j int) bool { return out[i].Since < out[j].Since })
	return out
}

// askForChat говорит, адресован ли признак ожидания разговору sid. Своя сессия
// и делегат второй подписки сходятся по реестру, а безадресный признак
// (сессия не назвалась вовсе) относится к разговору своей задачи: taskctl ask
// в этом случае прямо говорит, что ждёт безадресные реплики, и панель кладёт
// ответ ровно такой строкой. Прежде такой признак скан пропускал, и вопрос
// ждущей сессии до панели не доезжал ни текстом, ни вариантами.
func askForChat(a chat.Ask, sid string, b sessionBinds) bool {
	if a.Session == sid {
		return true
	}
	if a.Session != "" {
		return b[a.Session].Parent == sid
	}
	task := strings.TrimSpace(b[sid].Task)
	return task != "" && strings.EqualFold(task, strings.TrimSpace(a.Task))
}

// askSignWhy рассказывает, что дорога признака ожидания увидела, когда вопроса
// для разговора у неё не нашлось. Развилок тут три, и до этой правки все они
// сходились в одну строку журнала «разбор надо чинить»: вопросов за разговором
// нет вовсе, признак лежит живым, но ждёт чужую сессию, признак лежит с вышедшим
// сроком. Разложить их без второго захода это и есть смысл строки.
func askSignWhy(projPath, sid string, b sessionBinds, now time.Time) string {
	entries, err := os.ReadDir(chat.Root(projPath))
	if err != nil && !os.IsNotExist(err) {
		return "вход разговоров не читается: " + err.Error()
	}
	var alien, stale []string
	for _, e := range entries {
		name, found := strings.CutSuffix(e.Name(), chat.AskSuffix)
		if !found || e.IsDir() {
			continue
		}
		a, ok := chat.ReadAsk(chat.AskPath(projPath, name))
		if !ok {
			continue
		}
		who := a.Task
		if who == "" {
			who = name
		}
		if !a.Live(now) {
			stale = append(stale, fmt.Sprintf("%s (срок вышел в %s)", who, a.Until.Format("15:04:05")))
			continue
		}
		if !askForChat(a, sid, b) {
			alien = append(alien, fmt.Sprintf("%s (ждёт сессию %s)", who, shortSession(a.Session)))
		}
	}
	sort.Strings(alien)
	sort.Strings(stale)
	switch {
	case len(alien) > 0:
		return "признак лежит, но не за этим разговором: " + strings.Join(alien, ", ")
	case len(stale) > 0:
		return "живого признака нет, лежит просроченный: " + strings.Join(stale, ", ")
	}
	return "вопросов за разговором нет"
}

// shortSession режет id сессии до первого куска: в строку журнала целиком он не
// нужен, а по восьми знакам сессия находится и в реестре, и в журнале
// уведомителя.
func shortSession(sid string) string {
	if sid == "" {
		return "безадресно"
	}
	if head, _, ok := strings.Cut(sid, "-"); ok {
		return head
	}
	return sid
}

// askLines разворачивает вопросы признака в строки экрана: текст вопроса, под
// ним варианты, рекомендованный со звёздочкой. Плоские строки тут не
// огрубление: человек отвечает словами в поле ввода, и варианты ему подсказка,
// а не кнопки.
func askLines(qs []chat.Question) []string {
	var out []string
	for _, q := range qs {
		if text := strings.TrimSpace(q.Text); text != "" {
			out = append(out, text)
		}
		for _, o := range q.Options {
			mark := "- "
			if o.Recommended {
				mark = "* "
			}
			if lab := strings.TrimSpace(o.Label + " " + o.Note); lab != "" {
				out = append(out, mark+lab)
			}
		}
	}
	return out
}

// askKindAgent называет вид экрана «вопрос агента»: тот же виджет, что у
// вопроса клиента, но собран из признака ожидания, а не из снимка панели tmux.
// Виду тут не украшение: способ ответа у него другой, ответ уезжает во вход
// разговора, а не клавишами в чужое окно.
const askKindAgent = "agent"

// askFreeWord это подпись пункта со свободным ответом. Виджет клиента печатает
// её сам, а вопросу агента её пишем мы: пункт нужен там же, где у клиента, и
// называться должен теми же словами.
const askFreeWord = "свой ответ"

// askAdviceWord помечает вариант, который агент назвал рекомендованным.
// Звёздочка строк экрана тут не годится: у кнопки пояснение стоит словами.
const askAdviceWord = "советует агент"

// askStepCut это длина имени шага в полосе табов. Полный вопрос в таб не
// влезает, а первых слов хватает, чтобы узнать, куда переключаешься.
const askStepCut = 28

// agentAsk это вопрос агента для виджета панели. Поля повторяют снимок панели
// клиента (kind, text, options, steps), и рисует их та же рисовалка; своего тут
// три поля, и все три про то, чего у клиентского вопроса нет: чья задача ждёт,
// до какого срока и какие вопросы стоят за этим следующими.
type agentAsk struct {
	Kind    string      `json:"kind"`
	Task    string      `json:"task,omitempty"`
	Until   int64       `json:"until,omitempty"`
	Rest    []string    `json:"rest,omitempty"`
	Text    string      `json:"text,omitempty"`
	Options []tmuxPick  `json:"options,omitempty"`
	Steps   []agentStep `json:"steps,omitempty"`
}

// agentStep это один вопрос пачки. Вопросы едут на экран все разом, а не по
// одному: переход между шагами у вопроса агента ничего не отправляет и никуда
// не ходит, ответы копятся в самой панели и уезжают одной репликой.
type agentStep struct {
	Name    string     `json:"name"`
	Now     bool       `json:"now,omitempty"`
	Text    string     `json:"text,omitempty"`
	Options []tmuxPick `json:"options,omitempty"`
}

// agentAskOf собирает виджет из признака ожидания. Пустой вид значит, что
// показывать нечего: вопрос без текста и без вариантов рисовать не на чем.
func agentAskOf(h handedAsk) agentAsk {
	out := agentAsk{Kind: askKindAgent, Task: h.Ask.Task, Until: h.Ask.UnixUntil()}
	for i, q := range h.Ask.Questions {
		text := strings.TrimSpace(q.Text)
		if text == "" {
			continue
		}
		step := agentStep{Name: fmt.Sprintf("%d. %s", i+1, truncate(text, askStepCut)),
			Text: text, Options: agentPicks(q.Options)}
		out.Steps = append(out.Steps, step)
	}
	if len(out.Steps) == 0 {
		return agentAsk{}
	}
	out.Steps[0].Now = true
	out.Text, out.Options = out.Steps[0].Text, out.Steps[0].Options
	if len(out.Steps) == 1 {
		out.Steps = nil
	}
	return out
}

// agentPicks переводит варианты вопроса в пункты виджета. Пункт свободного
// ответа стоит последним и у вопроса без вариантов тоже: сказать своими словами
// человек вправе всегда.
func agentPicks(opts []chat.Option) []tmuxPick {
	var out []tmuxPick
	for _, o := range opts {
		pick := tmuxPick{Text: strings.TrimSpace(o.Label), Desc: strings.TrimSpace(o.Note)}
		if pick.Text == "" {
			continue
		}
		if o.Recommended {
			pick.Desc = strings.TrimSpace(askAdviceWord + ". " + pick.Desc)
		}
		out = append(out, pick)
	}
	return append(out, tmuxPick{Kind: pickFree, Text: askFreeWord})
}

// handedWaiting переводит найденный признак в состояние ожидания для экрана:
// та же точность, что у первого источника, спросил агент.
func handedWaiting(h handedAsk) Waiting {
	return Waiting{State: waitAskState, Source: waitAsk, Note: waitAskNote,
		Task: h.Ask.Task, Questions: askLines(h.Ask.Questions),
		Since: h.Since, Until: h.Ask.UnixUntil(), Session: h.Ask.Session}
}

// parkedWaiting читает второй источник: строку в Blocked с машинным разрядом
// причины «вопрос: ...». Ждать тут уже некому, заход кончился рубежом, и от
// первого источника это отличается словами состояния.
func parkedWaiting(sect, block string) (Waiting, bool) {
	if !parkedBlock(sect, block) {
		return Waiting{}, false
	}
	w := Waiting{State: waitParkState, Source: waitParked, Note: waitParkNote}
	text := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(block), askBlockWord))
	if text != "" {
		w.Questions = []string{text}
	}
	return w, true
}

// idleWaitTTL это срок, после которого повод из журнала перестаёт быть
// ожиданием. Отбоя у idle_prompt нет: сессия, которой ответили, второго
// события не шлёт, и без срока чип горел бы на строке неделю. Полсуток это
// граница между «человек отошёл» и «сессию бросили».
const idleWaitTTL = 12 * time.Hour

// waitTail это глубина хвоста журнала уведомителя, по которой считается
// запасной источник. Тот же хвост, каким лента отвечает по умолчанию: события
// глубже него старше суток на любой живой машине.
const waitTail = tailDefault

// idleWaits собирает третий источник: последнее событие каждой сессии в
// журнале уведомителя. Ожиданием считается только само событие idle_prompt,
// стоящее последним: сессия, которая после него сходила ход, оставила бы в
// журнале конец хода, и ожидание с неё снято. Второй возврат это невязки,
// «обрезанный ID сессии -> почему событие осталось без задачи»: их называет
// лента, потому что событие живёт в ней, а на строке доски его нет вовсе.
func idleWaits(lines []string, b sessionBinds, now time.Time) (map[string]Waiting, map[string]string) {
	last := map[string]Notification{}
	for _, ln := range lines {
		n, ok := parseNotifyLine(ln)
		if !ok || n.Session == "" || n.sandboxSkipped() {
			continue
		}
		last[n.Session] = n
	}
	waits := map[string]Waiting{}
	unclaimed := map[string]string{}
	for short, n := range last {
		if n.Reason != idlePromptReason {
			continue
		}
		at, err := time.ParseInLocation(bindStamp, n.Time, time.Local)
		if err != nil || now.Sub(at) > idleWaitTTL {
			continue
		}
		sid, rec, why := bindByPrefix(b, short)
		if why != "" {
			unclaimed[short] = why
			continue
		}
		if rec.Task == "" {
			continue
		}
		// Свежайшее событие выигрывает: у задачи бывает несколько заходов
		// подряд, и ждёт человека последний.
		if old, hit := waits[rec.Task]; hit && old.Since >= at.Unix() {
			continue
		}
		waits[rec.Task] = Waiting{State: waitIdleState, Source: waitIdle, Note: waitIdleNote,
			Since: at.Unix(), Session: sid}
	}
	return waits, unclaimed
}

// idlePromptReason это повод харнеса «сессия ждёт ввода»: его шлёт хук
// уведомителя, когда ход кончился и окно стоит с пустым приглашением.
const idlePromptReason = "idle_prompt"

// bindByPrefix связывает событие журнала с записью реестра. В журнале
// уведомителя ID сессии обрезан до восьми знаков (hookio.claude_code_session),
// а в реестре лежит целиком, поэтому сопоставление идёт по префиксу. Под один
// префикс попадают две записи с разными задачами, значит сказать, чья это
// работа, нечем: третий возврат называет причину словами, а событие остаётся
// без задачи, вместо того чтобы выбираться наугад.
func bindByPrefix(b sessionBinds, short string) (sid string, rec sessionBind, why string) {
	var tasks []string
	for id, r := range b {
		if !strings.HasPrefix(id, short) {
			continue
		}
		if r.Task != "" && !slicesHas(tasks, r.Task) {
			tasks = append(tasks, r.Task)
		}
		if sid == "" || r.Time > rec.Time {
			sid, rec = id, r
		}
	}
	if len(tasks) > 1 {
		sort.Strings(tasks)
		return "", sessionBind{}, fmt.Sprintf(
			"обрезанный до восьми знаков ID сессии %s носят несколько записей реестра чатов (задачи %s): к какой из них отнести событие, реестром не сказать",
			short, strings.Join(tasks, ", "))
	}
	return sid, rec, ""
}

// slicesHas это поиск слова в коротком списке задач: списков тут две-три
// строки, и заводить ради них множество дороже, чем пройти их насквозь.
func slicesHas(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

// widgetWaits собирает второй источник: клиента, который прямо сейчас стоит на
// своём виджете в tmux. Прежде такой вопрос был виден только в открытой панели
// разговора, и человек про него не знал вовсе, пока не открывал чат: два чата
// xr-proxy простояли так до вечера (замечание пользователя 2026-08-28). На
// вопросе доверия каталогу первый источник и третий молчат оба, потому что
// сессия ещё не родилась и в журнале уведомителя её нет ни строкой, а панель
// клиента вопрос показывает.
func (s *server) widgetWaits(b sessionBinds) map[string]Waiting {
	alive := tmuxAliveFn()
	out := map[string]Waiting{}
	seen := map[string]string{}
	for sid, rec := range b {
		if rec.Task == "" || rec.Tmux == "" {
			continue
		}
		// У задачи бывает несколько заходов подряд, и ждёт человека последний:
		// без этого выбор зависел бы от порядка обхода карты.
		if at, hit := seen[rec.Task]; hit && at >= rec.Time {
			continue
		}
		name := strings.SplitN(rec.Tmux, ":", 2)[0]
		if !alive(name) {
			continue
		}
		ask := s.tmuxAsking(name)
		if len(ask.Options) == 0 {
			continue
		}
		w := Waiting{State: waitWidState, Source: waitWidget, Note: waitWidNote, Session: sid}
		if text := strings.TrimSpace(ask.Text); text != "" {
			w.Questions = []string{text}
		}
		out[rec.Task] = w
		seen[rec.Task] = rec.Time
	}
	return out
}

// waitScan это разобранный запасной источник на один заход ответа: карта
// ожиданий по задачам и невязки по обрезанным сессиям. Журнал и реестр
// читаются один раз на весь ответ, а не на каждую строку доски.
type waitScan struct {
	widget    map[string]Waiting
	idle      map[string]Waiting
	unclaimed map[string]string
}

// waitScan читает журнал уведомителя и реестр чатов. Нечитаемый журнал это
// пустой запасной источник, а не отказ: первые два источника от него не
// зависят вовсе.
func (s *server) waitScan() waitScan {
	b := s.binds()
	out := waitScan{widget: s.widgetWaits(b)}
	data, err := os.ReadFile(s.notifyPath())
	if err != nil {
		return out
	}
	out.idle, out.unclaimed = idleWaits(tailLines(data, waitTail), b, s.now())
	return out
}

// waitLookup собирает разбор ожидания на один ответ сервера: журнал, реестр и
// часы берутся разом, а дальше каждая строка спрашивает своё состояние.
// Порядок источников тут и есть их ранг, и первый найденный выигрывает.
func (s *server) waitLookup(projPath string) func(id, sect, block string) (Waiting, bool) {
	scan := s.waitScan()
	now := s.now()
	return func(id, sect, block string) (Waiting, bool) {
		if w, ok := askWaiting(projPath, id, now); ok {
			return w, true
		}
		// Живой виджет идёт раньше парковки: у припаркованной задачи захода
		// нет вовсе, а тут клиент стоит и ждёт нажатия прямо сейчас, и это
		// знание точнее и полезнее.
		if w, ok := scan.widget[id]; ok {
			return w, true
		}
		if w, ok := parkedWaiting(sect, block); ok {
			return w, true
		}
		if w, ok := scan.idle[id]; ok {
			return w, true
		}
		return Waiting{}, false
	}
}

// nameWaitTasks доводит события ожидания в ленте до задачи. Задача и проект у
// события собраны по имени рабочего дерева (hooks/notify.py), поэтому у сессии
// главного чекаута поле задачи пустое, и связать событие со строкой доски
// может только реестр чатов. Не связав, лента говорит почему: событие без
// задачи бывает и честным (самопроверка канала), и спорным, и разница между
// ними это подпись.
func (s *server) nameWaitTasks(items []Notification) {
	need := false
	for _, n := range items {
		if n.Kind == waitKind && n.ID == "" && n.Session != "" {
			need = true
			break
		}
	}
	if !need {
		return
	}
	b := s.binds()
	for i := range items {
		n := &items[i]
		if n.Kind != waitKind || n.ID != "" || n.Session == "" {
			continue
		}
		_, rec, why := bindByPrefix(b, n.Session)
		switch {
		case why != "":
			n.Note = why
		case rec.Task != "":
			n.ID, n.Project = rec.Task, orElse(n.Project, rec.Project)
		}
	}
}

// orElse оставляет первое непустое: проект у события стоит своим полем, а
// реестр подставляется только там, где поля не было вовсе.
func orElse(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
