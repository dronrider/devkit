package main

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/dronrider/devkit/internal/stage"
)

// Пульс задачи: одна ручка на всё, из чего собрано кольцо в шапке разговора
// (макет пользователя). Раздельными запросами это стоило бы четырёх походов на
// каждый оборот опроса: доска, реестр чатов, хвосты транскриптов и записи
// этапов. Экран получает готовые поля и считает по ним только давность, потому
// что она живёт между опросами.

// svgNS это пространство имён векторной графики: тем же именем зовёт его
// статика, когда собирает кольцо. Сторож внешних хостов знает его отсюда, а не
// строкой в самом тесте.
const svgNS = "http://www.w3.org/2000/svg"

// pulseQuiet это граница молчания: записей в транскрипте не было дольше срока.
// Порог грубый и стоит одной константой нарочно, его ещё двигать по ощущениям.
const pulseQuiet = 60 * time.Second

// pulseHeld это срок, в течение которого незакрытый вызов инструмента считается
// работой. Одним pulseQuiet работу не померить: `go test ./...` идёт две минуты
// и в транскрипт всё это время не пишет ничего, а агент за ним занят. Мера та
// же, что у занятости сессии (sessionBusy): вызов без ответа старше получаса
// это брошенный хвост закрытого окна, а не работа.
const pulseHeld = 30 * time.Minute

// pulseTail это сколько байт хвоста транскрипта читается ради последнего
// события: запись хода бывает длинной, но десятка кусков хватает, чтобы найти
// последний вызов инструмента.
const pulseTail = 64 << 10

// Фазы конвейера задачи в порядке хода. Пять слов и ни одним больше: столько
// сегментов у кольца, и шестое там негде показать.
const (
	phaseCode   = "код"
	phaseTests  = "тесты"
	phaseReview = "ревью"
	phaseMerge  = "слияние"
	phaseShip   = "выкат"
)

var pulsePhases = []string{phaseCode, phaseTests, phaseReview, phaseMerge, phaseShip}

// Виды шкалы кольца. Правило тут одно и оно жёсткое: кольцо не закрашивает
// того, чего не знает. Молчит источник, значит шкалы нет вовсе, а кольцо
// остаётся индикатором состояния; выдавать незнание за пройденную фазу нельзя,
// потому что «идёт первая из пяти» человек читает как знание о ходе работы.
const (
	// scaleNone это отсутствие шкалы: сегментов нет, есть только состояние.
	scaleNone = ""
	// scaleStages это конвейер задачи: пять фаз из записи ~/.devkit/runs и
	// секции строки. Рисуется только там, где запись есть либо задача уже в
	// Check, то есть слита и выкачена.
	scaleStages = "stages"
	// scaleGoal это прогресс цели: доля закрытых задач цели. Конвейер задачи
	// цели неприменим по смыслу, цель не проходит код с ревью, она режется на
	// задачи, и её ход это они.
	scaleGoal = "goal"
)

// Состояния кольца целиком. Разница между «молчит» и «пусто» не косметическая:
// молчащий агент жив и его можно спросить, а пустого разговора нет вовсе.
const (
	pulseWork  = "working"
	pulseWait  = "waiting"
	pulseHush  = "silent"
	pulseEmpty = "empty"
)

// pulseIdle это состояние отдельного разговора: сессия жива, событий давно нет,
// но человека никто не спрашивал. Ожиданием такое звать нельзя: повод
// idle_prompt из журнала уведомителя значит «клиент ждёт ввода», и от вопроса
// агента он отличается ровно тем, что вопроса нет. Раз назвав простой
// ожиданием, экран заставляет человека искать вопрос, которого не было.
const pulseIdle = "idle"

// PulsePhase это одно деление шкалы: имя словом, пройдено ли оно и идёт ли
// прямо сейчас. Процентов у деления нет: их неоткуда взять, а нарисованные на
// глаз они врали бы точнее, чем честное «пройдена или нет».
type PulsePhase struct {
	Name string `json:"name"`
	Done bool   `json:"done"`
	Now  bool   `json:"now,omitempty"`
}

// PulseAgent это строка выпадающего списка кольца: чей разговор, чем занят и
// когда в последний раз подавал признаки жизни. Since в unix-секундах теми же
// единицами, что stage_since у строки доски: давность считает клиент, и
// второго разбора времени на экране не заводится.
type PulseAgent struct {
	Session string `json:"session"`
	Name    string `json:"name"`
	// Title это заголовок разговора: имя tmux-сессии говорит про заход, а
	// заголовок про предмет, и без него два чата одной задачи в списке
	// неразличимы (замечание про «почему в кольце два агента»).
	Title string `json:"title,omitempty"`
	State string `json:"state"`
	// Tool это имя инструмента хода, About короткий довод к нему, Sub имя
	// субагента. Три поля врозь: склеенные, они читались одним предложением.
	Tool  string `json:"tool,omitempty"`
	About string `json:"about,omitempty"`
	Sub   string `json:"sub,omitempty"`
	Since int64  `json:"since,omitempty"`
	// WaitSince это момент, с которого ждут ответа. Пусто у всех, кроме
	// ждущего: у работающего и простаивающего ждать нечего.
	WaitSince int64 `json:"wait_since,omitempty"`
	// Held говорит, что работа видна не свежей записью, а вызовом инструмента
	// без ответа: агент занят долгой командой, и в журнал он всё это время не
	// пишет. Экран подписывает такой ход «идёт», а не давностью молчания.
	Held bool `json:"held,omitempty"`
	// Own говорит, что это тот самый разговор, который открыт в панели.
	Own  bool   `json:"own,omitempty"`
	Task string `json:"task,omitempty"`
}

// Pulse это ответ ручки целиком.
type Pulse struct {
	Task string `json:"task,omitempty"`
	// State это состояние кольца: working, waiting, silent, empty.
	State string `json:"state"`
	// Scale называет, какая под кольцом шкала: пусто значит, что шкалы нет и
	// делений рисовать не из чего. Молчащий источник тут не повод показать
	// пустую пятёрку фаз: незнание, нарисованное шкалой, читается как знание.
	Scale  string       `json:"scale"`
	Phases []PulsePhase `json:"phases,omitempty"`
	// Done и Total это задачи цели, закрытые и всего. Только у scaleGoal.
	Done  int `json:"done,omitempty"`
	Total int `json:"total,omitempty"`
	// Goal говорит, что строка это цель: у неё конвейер задачи неприменим по
	// смыслу, и шкала считает задачи.
	Goal bool `json:"goal,omitempty"`
	// Phase это фаза, которая идёт прямо сейчас, словом. Пусто у задачи, чей
	// этап не отмечен, и у разговора без задачи.
	Phase string `json:"phase,omitempty"`
	// Flow говорит, работает ли хоть кто-то: по нему бежит дуга поверх
	// сегментов. Считается он по агентам, а не по свежести последней записи:
	// запись бывает свежей и у разговора, которому только что ответил человек.
	Flow bool `json:"flow"`
	// Count это агентов в чатах задачи, а Working, Waiting и Idle разбивка по
	// состояниям. Разбивка тут не украшение: одно число на всех врало, потому
	// что простаивающий второй час разговор оно считало наравне с работающим,
	// и «два агента» в середине кольца читалось как «двое работают».
	Count   int `json:"count"`
	Working int `json:"working"`
	Waiting int `json:"waiting,omitempty"`
	Idle    int `json:"idle,omitempty"`
	// Parked говорит, что ждёт сама строка, а живой сессии за ней нет ни
	// одной. Это состояние задачи, а не событие в разговоре. Кольцо такое
	// ожидание рисовало красным ореолом с цифрой, ореол моргал, как тревога, и
	// не объяснял ничего (замечание пользователя по снимку DK-466).
	Parked bool `json:"parked,omitempty"`
	// Block это причина блокировки со строки доски, дословно. Ею подписан чип
	// рядом с кольцом: раз уж ожидание видно, сказать про него надо словами.
	// Пусто у строки, которая не заблокирована.
	Block string `json:"block,omitempty"`
	// About и Since это чем занят и когда последний раз подавал голос тот
	// агент, по которому подписана шапка: самый свежий из живых.
	Tool  string `json:"tool,omitempty"`
	About string `json:"about,omitempty"`
	Sub   string `json:"sub,omitempty"`
	Since int64  `json:"since,omitempty"`
	// Wait это ожидание строки: вопрос агента (.ask) либо парковка задачи
	// вопросом. Повод idle_prompt сюда не идёт, он простой, а не вопрос.
	Wait *Waiting `json:"wait,omitempty"`
	// Own это тот разговор, который открыт в панели, отдельно от остальных.
	// Кольцо считает задачу целиком, а слова под названием разговора обязаны
	// говорить про него самого: вопрос соседней сессии в шапке открытого чата
	// читался бы как вопрос к этому чату, и человек искал бы в ленте вопрос,
	// которого там нет.
	Own *PulseAgent `json:"own,omitempty"`
	// Plan это план ведущей живой сессии: пункты её todo-списка целиком, как их
	// написал сам агент. По ним кольцо и рисует деления, а подсказка список.
	// Плана нет это обычный случай: сессия его не заводила.
	Plan []planItem `json:"plan,omitempty"`
	// OwnWait это ожидание самого открытого разговора: вопрос, адресованный
	// его сессии, либо парковка задачи, за которую он отвечает.
	OwnWait *Waiting `json:"own_wait,omitempty"`
	// Asks это вопросы, адресованные открытому разговору работой, которую он
	// раздал: субагент ходит с ID внешней сессии, делегата приводит к родителю
	// реестр, а признак ожидания лежит под именем чужой задачи. Ближний срок
	// первым, отвечают им по одному.
	Asks []Waiting `json:"own_asks,omitempty"`
	// Quiet это порог молчания в секундах: клиент подписывает им тишину, а
	// своей границы не заводит.
	Quiet  int          `json:"quiet"`
	Agents []PulseAgent `json:"agents"`
}

// pulsePhaseCut считает рубеж пройденного по тому, что о задаче знают доска и
// запись этапов. Слияние и выкат порознь тут не видны: их гоняет shipctl одним
// заходом, и задача попадает в Check уже слитой и выкаченной, поэтому обе фазы
// закрываются вместе с секцией. Фаза «тесты» узнаётся по живому ходу агента:
// раз он прямо сейчас гоняет тесты, код он уже написал.
//
// Третий ответ говорит, есть ли о фазах достоверное знание. Записи этапов у
// задачи может не быть вовсе (её пишет конвейер, а работу поднимают и руками),
// и тогда о ходе задачи не известно ничего: шкалу в этом случае не рисуют, а не
// показывают «идёт первая из пяти».
func pulsePhaseCut(sect string, live string, seen map[string]bool, testing bool) (int, string, bool) {
	if sect == "check" || sect == "done" {
		// Задача в Check слита и выкачена, и это знание со строки доски, а не
		// догадка: пять фаз позади независимо от того, вёл ли кто-то запись.
		return len(pulsePhases), "", true
	}
	if len(seen) == 0 && live == "" {
		return 0, "", false
	}
	cut, now := 0, ""
	switch live {
	case stage.Review:
		cut, now = 2, phaseReview
	case stage.Dev, stage.Ask:
		if testing {
			cut, now = 1, phaseTests
		} else {
			cut, now = 0, phaseCode
		}
	}
	// История этапов старше живого: задача, побывавшая на ревью и уехавшая в
	// блок, кода с тестами обратно не теряет.
	if seen[stage.Review] && cut < 2 {
		cut = 2
	}
	return cut, now, true
}

// pulsePhaseList раскладывает рубеж по именам фаз.
func pulsePhaseList(cut int, now string) []PulsePhase {
	out := make([]PulsePhase, 0, len(pulsePhases))
	for i, name := range pulsePhases {
		out = append(out, PulsePhase{Name: name, Done: i < cut, Now: name == now})
	}
	return out
}

// taskStages отдаёт виды деятельности, которые задача уже проходила, и живой
// этап. Читается та же запись ~/.devkit/runs, что и у строки доски.
func (s *server) taskStages(projectPath, id string) (map[string]bool, string) {
	seen := map[string]bool{}
	live := ""
	for _, rec := range stage.List(s.cfg.Home, projectPath) {
		if rec.ID != id {
			continue
		}
		for _, st := range rec.Stages {
			seen[st.Kind] = true
		}
		if last, ok := rec.Live(); ok {
			live = last.Kind
		}
	}
	return seen, live
}

// Инструменты, чей вызов означает прогон тестов. Список грубый нарочно: цена
// ошибки тут одна закрашенная секция кольца.
var pulseTestWords = []string{"go test", "pytest", "npm test", "regcheck", "obeycheck", "make test"}

func pulseTesting(about string) bool {
	low := strings.ToLower(about)
	for _, w := range pulseTestWords {
		if strings.Contains(low, w) {
			return true
		}
	}
	return false
}

// pulseArg подписывает вызов инструмента тем же разбором, каким его подписывает
// лента: главный довод вызова (команда, файл, шаблон), а при его отсутствии
// пояснение хода словами. Второго словаря полей тут не заводится: разъехавшись
// с лентой, подпись в кольце звала бы ход иначе, чем запись под ним.
func pulseArg(input map[string]any) string {
	said := toolNote(input)
	if said == "" {
		said = toolAbout(input)
	}
	return truncate(said, 52)
}

// pulseStep это то, что видно в хвосте журнала: когда там была последняя
// запись, чем занят агент и висит ли незакрытый вызов инструмента. Последнее и
// отличает долгую команду от молчания.
type pulseStep struct {
	At time.Time
	// Tool это имя инструмента, About довод хода (команда, файл, пояснение
	// словами), а Sub имя субагента, если ход его. Три поля врозь, потому что
	// склеенные в одну строку они читались кашей: «последний ход SendMessage
	// Кольцо врёт прогрессом: чинить класс» это имя инструмента, слипшееся с
	// первым предложением реплики (замечание пользователя по снимку).
	Tool  string
	About string
	Sub   string
	Held  bool
}

// pulseSeen читает хвост транскрипта: когда там было последнее событие, чем
// агент занят и остался ли вызов без ответа. Инструмент берётся последний
// открытый, а когда открытых нет, последний по файлу: именно он и был ходом.
// pulseSeen читается на каждом тике кольца по всем боковым журналам сессии, а
// журналы у долгого разговора это сотня файлов: без памяти процесса кольцо
// перечитывало их хвосты по кругу и стоило дороже самой ленты (жалоба
// пользователя про тормоза). Ответ зависит только от хвоста файла, поэтому
// отпечаток файла его и сторожит, тем же приёмом, что у кусков ленты (feed.go).
var pulseSteps struct {
	sync.Mutex
	stamp map[string]string
	m     map[string]pulseStep
}

// forgetPulseSteps снимает память на ходы: нужна стендам.
func forgetPulseSteps() {
	pulseSteps.Lock()
	pulseSteps.m, pulseSteps.stamp = nil, nil
	pulseSteps.Unlock()
}

func pulseSeen(path string) pulseStep {
	fi, err := os.Stat(path)
	if err != nil {
		return pulseStep{}
	}
	stamp := fileStamp(fi)
	pulseSteps.Lock()
	step, hit := pulseSteps.m[path]
	same := pulseSteps.stamp[path] == stamp
	pulseSteps.Unlock()
	if hit && same {
		return step
	}
	step = pulseRead(path)
	pulseSteps.Lock()
	if pulseSteps.m == nil || len(pulseSteps.m) > feedKeep {
		pulseSteps.m, pulseSteps.stamp = map[string]pulseStep{}, map[string]string{}
	}
	pulseSteps.m[path], pulseSteps.stamp[path] = step, stamp
	pulseSteps.Unlock()
	return step
}

func pulseRead(path string) pulseStep {
	var step pulseStep
	f, err := os.Open(path)
	if err != nil {
		return step
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return step
	}
	from := int64(0)
	if fi.Size() > pulseTail {
		from = fi.Size() - pulseTail
	}
	if _, err := f.Seek(from, 0); err != nil {
		return step
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return step
	}
	open := map[string][2]string{}
	order := []string{}
	for _, ln := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(ln) == "" {
			continue
		}
		var rec struct {
			Timestamp string `json:"timestamp"`
			Message   struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal([]byte(ln), &rec) != nil {
			continue
		}
		if at, err := time.Parse(time.RFC3339, rec.Timestamp); err == nil && at.After(step.At) {
			step.At = at
		}
		var blocks []struct {
			Type      string         `json:"type"`
			ID        string         `json:"id"`
			ToolUseID string         `json:"tool_use_id"`
			Name      string         `json:"name"`
			Input     map[string]any `json:"input"`
		}
		if json.Unmarshal(rec.Message.Content, &blocks) != nil {
			continue
		}
		for _, b := range blocks {
			switch b.Type {
			case "tool_use":
				if b.Name == "" {
					continue
				}
				step.Tool, step.About = b.Name, pulseArg(b.Input)
				if b.ID != "" {
					open[b.ID] = [2]string{b.Name, step.About}
					order = append(order, b.ID)
				}
			case "tool_result":
				delete(open, b.ToolUseID)
			}
		}
	}
	// Незакрытый вызов старше остальных описывает ход точнее последней записи:
	// пока команда идёт, ответа на неё нет, а после неё в журнале успевает
	// появиться чужая запись.
	for i := len(order) - 1; i >= 0; i-- {
		if said, hit := open[order[i]]; hit {
			step.Tool, step.About, step.Held = said[0], said[1], true
			break
		}
	}
	return step
}

// pulseTrace дочитывает боковые журналы субагентов. Пока сессия ждёт Task, её
// собственный транскрипт держит один незакрытый вызов и молчит минутами, и без
// этого дочитывания работающий агент выходил бы молчащим (та же находка, что у
// ленты разговора). Ход субагента и подписывается им: «субагент <заказ>: Bash
// go test» говорит, кто именно занят, а «Task» в шапке не говорит ничего.
func pulseTrace(path string) pulseStep {
	step := pulseSeen(path)
	dir := subDir(path)
	items, err := os.ReadDir(dir)
	if err != nil {
		return step
	}
	labels := subLogs(path)
	names := map[string]string{}
	for _, rec := range labels {
		names[rec.File] = rec.Label
	}
	for _, it := range items {
		if it.IsDir() || !strings.HasSuffix(it.Name(), ".jsonl") {
			continue
		}
		file := filepath.Join(dir, it.Name())
		sub := pulseSeen(file)
		if !sub.At.After(step.At) {
			continue
		}
		step.At = sub.At
		if sub.Tool == "" && sub.About == "" {
			continue
		}
		step.Tool, step.About, step.Held = sub.Tool, sub.About, sub.Held
		// Имя субагента режется коротко и стоит своим полем: строка узкая, и
		// заказ целиком не оставил бы места самому ходу, ради которого строка
		// и нужна.
		step.Sub = truncate(names[file], 22)
	}
	return step
}

// pulseName подписывает разговор в списке кольца: имя tmux-сессии говорит про
// заход больше, чем начало первой реплики, а без него остаётся короткий id.
func pulseName(e chatEntry) string {
	if e.Tmux != "" {
		return e.Tmux
	}
	if len(e.ID) > 8 {
		return "сессия " + e.ID[:8]
	}
	return "сессия " + e.ID
}

// handlePulse собирает пульс разговора: задача берётся из запроса, а список
// агентов из реестра привязок чатов, то есть тех же чатов, что стоят в
// выпадающем списке шапки.
// pulseAsking считает настоящее ожидание строки: вопрос агента (.ask) старше
// всего, за ним парковка задачи вопросом. Третий источник waiting.go, повод
// idle_prompt из журнала уведомителя, сюда не идёт нарочно: он говорит лишь
// «клиент ждёт ввода», то есть про простой чужой сессии, и выданный за вопрос
// он красил шапку работающего разговора чужой тревогой.
func (s *server) pulseAsking(projPath, id, sect, block string) (Waiting, bool) {
	if id == "" {
		return Waiting{}, false
	}
	if w, ok := askWaiting(projPath, id, s.now()); ok {
		return w, true
	}
	return parkedWaiting(sect, block)
}

// pulseTitle это предмет разговора одной строкой: заголовок харнеса старше
// первой реплики, как и в списке чатов.
func pulseTitle(e chatEntry) string {
	for _, v := range []string{e.Summary, e.Title} {
		if t := strings.TrimSpace(v); t != "" {
			return truncate(strings.Join(strings.Fields(t), " "), 60)
		}
	}
	return ""
}

// pulseWaits отвечает, ждут ли ответа от этой сессии. Вопрос агента называет
// сессию, которой ответ и уйдёт, и ждёт ровно она; безадресный вопрос и
// парковка ждут строку целиком, а не кого-то одного, и сессию тогда назвать
// нечем.
func pulseWaits(w Waiting, ok bool, sid string) bool {
	return ok && w.Session != "" && w.Session == sid
}

func (s *server) handlePulse(w http.ResponseWriter, r *http.Request) {
	found := s.findProject(w, r, "пульс задачи")
	if found == nil {
		return
	}
	id := strings.TrimSpace(r.URL.Query().Get("task"))
	sid := strings.TrimSpace(r.URL.Query().Get("sid"))
	now := s.now()
	out := Pulse{Task: id, State: pulseEmpty, Scale: scaleNone,
		Quiet: int(pulseQuiet / time.Second), Agents: []PulseAgent{}}

	// Строка доски читается раньше агентов: по ней считаются и фазы, и то,
	// ждут ли человека на самом деле.
	sect, block, prefix := "", "", ""
	rows := map[string]boardRow{}
	rowHit, goalRow := false, false
	if id != "" {
		if raw, err := s.projectBoard(found.Path); err == nil {
			view, _ := parseBoardView(raw)
			prefix = view.Prefix
			if parsed, err := parseBoardRows(raw); err == nil {
				rows = parsed
				if row, hit := rows[id]; hit {
					sect, block, rowHit = row.Sect, row.Block, true
					goalRow = isGoalTitle(row.Title)
				}
			}
		}
	}
	ask, asking := s.pulseAsking(found.Path, id, sect, block)

	paths := map[string]string{}
	for _, f := range sessionFiles(s.transcriptRoots(), found.Path) {
		paths[f.ID] = f.path
	}
	last := time.Time{}
	testing := false
	for _, e := range s.chatEntries(found.Path, chatListLimit) {
		// Кольцо считает агентов задачи, а без задачи в адресе один открытый
		// разговор: чужие чаты к нему отношения не имеют.
		if id != "" {
			if !hasTask(e.Tasks, id) {
				continue
			}
		} else if e.ID != sid {
			continue
		}
		if e.State != chatLive && e.State != chatVscode {
			continue
		}
		step := pulseStep{}
		if p, ok := paths[e.ID]; ok {
			step = pulseTrace(p)
		}
		a := PulseAgent{Session: e.ID, Name: pulseName(e), Title: pulseTitle(e),
			Tool: step.Tool, About: step.About, Sub: step.Sub,
			State: pulseIdle, Own: e.ID == sid}
		if len(e.Tasks) > 0 {
			a.Task = e.Tasks[0]
		}
		if !step.At.IsZero() {
			a.Since = step.At.Unix()
			// Работой считается свежая запись либо вызов инструмента, на
			// который ещё нет ответа: длинная команда пишет в журнал один раз
			// и молчит до конца, и по одной свежести она неотличима от
			// брошенной сессии.
			fresh := now.Sub(step.At) < pulseQuiet
			held := step.Held && now.Sub(step.At) < pulseHeld
			if fresh || held {
				a.State = pulseWork
				a.Held = step.Held && !fresh
			}
		}
		// Ждёт та сессия, которой вопрос и адресован. Работа тут не спорит с
		// ожиданием: агент, задавший вопрос, стоит на нём и ходов не делает, а
		// свежая метка в транскрипте у него от самого вопроса.
		if pulseWaits(ask, asking, e.ID) {
			a.State, a.WaitSince = pulseWait, ask.Since
		}
		if step.At.After(last) {
			last = step.At
			if step.Tool != "" || step.About != "" {
				out.Tool, out.About, out.Sub = step.Tool, step.About, step.Sub
			}
			// План берётся у ведущей сессии, той же, чей ход подписывает
			// кольцо: у соседнего разговора задачи план свой, и смешанные они
			// читались бы как один список.
			if p, ok := paths[e.ID]; ok {
				out.Plan = planOf(s.cfg.Home, e.ID, e.Tmux, p, s.now())
			}
			if pulseTesting(step.Tool + " " + step.About) {
				testing = true
			}
		}
		out.Agents = append(out.Agents, a)
	}
	sort.SliceStable(out.Agents, func(i, j int) bool { return out.Agents[i].Since > out.Agents[j].Since })
	out.Count = len(out.Agents)
	if !last.IsZero() {
		out.Since = last.Unix()
	}

	switch {
	case rowHit && goalRow:
		// Цель не проходит код с ревью: её ход это доля закрытых задач цели,
		// и считает их тот же разбор, каким живёт экран цели.
		out.Goal = true
		if counts, ok := s.goalProgress(found.Path, id, prefix, rows); ok {
			out.Scale, out.Done, out.Total = scaleGoal, counts.Closed, counts.Total
		}
	case rowHit:
		seen, live := s.taskStages(found.Path, id)
		if cut, phase, known := pulsePhaseCut(sect, live, seen, testing); known {
			out.Scale, out.Phases, out.Phase = scaleStages, pulsePhaseList(cut, phase), phase
		}
	}
	if asking {
		out.Wait = &ask
	}
	// Открытый разговор отвечает за себя: его состояние и ожидание считаются по
	// нему, а не по соседям с той же задачи.
	for i := range out.Agents {
		if out.Agents[i].Own {
			own := out.Agents[i]
			out.Own = &own
			break
		}
	}
	if out.Own != nil && out.Own.State == pulseWait {
		out.OwnWait = &ask
	} else if asking && ask.Session == "" && out.Own != nil {
		// Безадресное ожидание это ожидание строки, и отвечает за него любой
		// разговор задачи: ответ уедет во вход задачи, откуда его возьмёт тот,
		// кто задачу продолжит.
		out.OwnWait = &ask
		out.Own.State, out.Own.WaitSince = pulseWait, ask.Since
	}
	// Вопросы от розданной работы: признак ожидания лежит под именем чужой
	// задачи, и по строке панели его не найти, а ждёт ответа ровно этот
	// разговор. До скана вопрос ложился туда, где его никто не смотрел, срок
	// выходил, и работа вставала парковкой (живой случай: слияние цели
	// DK-397).
	// Своё ожидание скан больше не гасит. Условие «ожидания нет» отсекало
	// ровно вопрос, заданный по своей же задаче: он заполнял OwnWait раньше, и
	// от него на экране оставалась одна подсказка поля ввода (DK-652). Свой
	// вопрос находится тем же сканом, потому что сессией в признаке названа эта
	// же сессия, и приезжает на экран с вариантами.
	if sid != "" {
		for _, h := range handedAsks(found.Path, sid, s.binds(), now) {
			out.Asks = append(out.Asks, handedWaiting(h))
		}
	}
	if len(out.Asks) > 0 {
		out.OwnWait = &out.Asks[0]
		for i := range out.Agents {
			if out.Agents[i].Session == sid {
				out.Agents[i].State, out.Agents[i].WaitSince = pulseWait, out.Asks[0].Since
			}
		}
		if out.Own != nil {
			out.Own.State, out.Own.WaitSince = pulseWait, out.Asks[0].Since
		}
	}

	// Разбивка считается после того, как открытый разговор разобрался со своим
	// ожиданием: безадресный вопрос переводит его в ждущие уже здесь.
	for _, a := range out.Agents {
		switch a.State {
		case pulseWork:
			out.Working++
		case pulseWait:
			out.Waiting++
		default:
			out.Idle++
		}
	}
	// Ждущей сессии в списке может не быть вовсе: вопрос задал закрывшийся
	// заход, а у парковки сессии нет по смыслу. Ждёт тогда сама строка, и это
	// один ждущий, а не ноль.
	if (asking || len(out.Asks) > 0) && out.Waiting == 0 {
		out.Waiting = 1
	}
	// Ожидание без единой живой сессии идёт от самой строки. Экран говорит о
	// нём словами и гасит тревогу кольца: моргать тут нечему, разговор никто не
	// ведёт.
	out.Parked = asking && out.Count == 0
	out.Block = block
	out.Flow = out.Working > 0

	switch {
	case asking, len(out.Asks) > 0:
		out.State = pulseWait
	case out.Count == 0:
		out.State = pulseEmpty
	case out.Working > 0:
		out.State = pulseWork
	default:
		out.State = pulseHush
	}
	writeJSON(w, http.StatusOK, out)
}
