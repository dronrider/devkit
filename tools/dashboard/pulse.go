package main

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
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

// pulseQuiet это граница молчания: событий не было дольше срока, значит агент
// молчит, а не работает. Порог грубый и стоит одной константой нарочно, его
// ещё двигать по ощущениям.
const pulseQuiet = 60 * time.Second

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

// Состояния кольца целиком. Разница между «молчит» и «пусто» не косметическая:
// молчащий агент жив и его можно спросить, а пустого разговора нет вовсе.
const (
	pulseWork  = "working"
	pulseWait  = "waiting"
	pulseHush  = "silent"
	pulseEmpty = "empty"
)

// PulsePhase это одна фаза кольца: имя словом, пройдена ли она и идёт ли прямо
// сейчас. Процентов у фазы нет: их неоткуда взять, а нарисованные на глаз они
// врали бы точнее, чем честное «пройдена или нет».
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
	State   string `json:"state"`
	About   string `json:"about,omitempty"`
	Since   int64  `json:"since,omitempty"`
	Task    string `json:"task,omitempty"`
}

// Pulse это ответ ручки целиком.
type Pulse struct {
	Task string `json:"task,omitempty"`
	// State это состояние кольца: working, waiting, silent, empty.
	State  string       `json:"state"`
	Phases []PulsePhase `json:"phases"`
	// Phase это фаза, которая идёт прямо сейчас, словом. Пусто у задачи, чей
	// этап не отмечен, и у разговора без задачи.
	Phase string `json:"phase,omitempty"`
	// Flow говорит, текут ли события: последнее моложе pulseQuiet. По нему
	// бежит дуга поверх сегментов.
	Flow bool `json:"flow"`
	// Count это агентов в чатах задачи, Waiting из них ждущих человека.
	Count   int `json:"count"`
	Waiting int `json:"waiting,omitempty"`
	// About и Since это чем занят и когда последний раз подавал голос тот
	// агент, по которому подписана шапка: самый свежий из живых.
	About string `json:"about,omitempty"`
	Since int64  `json:"since,omitempty"`
	// Wait это состояние ожидания строки, как его собирает waiting.go: слово
	// состояния и подпись источника едут в строку под названием разговора.
	Wait *Waiting `json:"wait,omitempty"`
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
func pulsePhaseCut(sect string, live string, seen map[string]bool, testing bool) (int, string) {
	if sect == "check" || sect == "done" {
		return len(pulsePhases), ""
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
	default:
		if sect == sectRun {
			if testing {
				cut, now = 1, phaseTests
			} else {
				cut, now = 0, phaseCode
			}
		}
	}
	// История этапов старше живого: задача, побывавшая на ревью и уехавшая в
	// блок, кода с тестами обратно не теряет.
	if seen[stage.Review] && cut < 2 {
		cut = 2
	}
	return cut, now
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

// pulseArg вытаскивает из вызова инструмента то, чем он подписывается на
// экране: команду у Bash, путь у чтения и правки, заказ у делегирования.
var pulseArgKeys = []string{"command", "file_path", "pattern", "path", "prompt", "description"}

func pulseArg(input map[string]any) string {
	for _, key := range pulseArgKeys {
		v, ok := input[key].(string)
		if !ok || strings.TrimSpace(v) == "" {
			continue
		}
		return truncate(strings.Join(strings.Fields(v), " "), 48)
	}
	return ""
}

// pulseSeen читает хвост транскрипта: когда там было последнее событие и чем
// агент занят. Инструмент берётся последний по файлу, потому что именно он и
// идёт прямо сейчас, пока ответа на него нет.
func pulseSeen(path string) (time.Time, string) {
	f, err := os.Open(path)
	if err != nil {
		return time.Time{}, ""
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return time.Time{}, ""
	}
	from := int64(0)
	if fi.Size() > pulseTail {
		from = fi.Size() - pulseTail
	}
	if _, err := f.Seek(from, 0); err != nil {
		return time.Time{}, ""
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return time.Time{}, ""
	}
	last := time.Time{}
	about := ""
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
		if at, err := time.Parse(time.RFC3339, rec.Timestamp); err == nil && at.After(last) {
			last = at
		}
		var blocks []struct {
			Type  string         `json:"type"`
			Name  string         `json:"name"`
			Input map[string]any `json:"input"`
		}
		if json.Unmarshal(rec.Message.Content, &blocks) != nil {
			continue
		}
		for _, b := range blocks {
			if b.Type != "tool_use" || b.Name == "" {
				continue
			}
			about = strings.TrimSpace(b.Name + " " + pulseArg(b.Input))
		}
	}
	return last, about
}

// pulseTrace дочитывает боковые журналы субагентов. Пока сессия ждёт Task, её
// собственный транскрипт молчит минутами, и без этого дочитывания работающий
// агент выходил бы молчащим (та же находка, что у ленты разговора).
func pulseTrace(path string) (time.Time, string) {
	last, about := pulseSeen(path)
	dir := subDir(path)
	items, err := os.ReadDir(dir)
	if err != nil {
		return last, about
	}
	for _, it := range items {
		if it.IsDir() || !strings.HasSuffix(it.Name(), ".jsonl") {
			continue
		}
		at, said := pulseSeen(filepath.Join(dir, it.Name()))
		if at.After(last) {
			last = at
			if said != "" {
				about = said
			}
		}
	}
	return last, about
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
func (s *server) handlePulse(w http.ResponseWriter, r *http.Request) {
	found := s.findProject(w, r, "пульс задачи")
	if found == nil {
		return
	}
	id := strings.TrimSpace(r.URL.Query().Get("task"))
	sid := strings.TrimSpace(r.URL.Query().Get("sid"))
	now := s.now()
	out := Pulse{Task: id, State: pulseEmpty, Quiet: int(pulseQuiet / time.Second),
		Phases: pulsePhaseList(0, ""), Agents: []PulseAgent{}}

	paths := map[string]string{}
	for _, f := range sessionFiles(s.transcriptRoots(), found.Path) {
		paths[f.ID] = f.path
	}
	fresh := time.Time{}
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
		at, about := time.Time{}, ""
		if p, ok := paths[e.ID]; ok {
			at, about = pulseTrace(p)
		}
		a := PulseAgent{Session: e.ID, Name: pulseName(e), About: about, State: pulseHush}
		if len(e.Tasks) > 0 {
			a.Task = e.Tasks[0]
		}
		if !at.IsZero() {
			a.Since = at.Unix()
			if now.Sub(at) < pulseQuiet {
				a.State = pulseWork
			}
		}
		if at.After(fresh) {
			fresh = at
			if about != "" {
				out.About = about
			}
			if pulseTesting(about) {
				testing = true
			}
		}
		out.Agents = append(out.Agents, a)
	}
	sort.SliceStable(out.Agents, func(i, j int) bool { return out.Agents[i].Since > out.Agents[j].Since })
	out.Count = len(out.Agents)
	if !fresh.IsZero() {
		out.Since = fresh.Unix()
		out.Flow = now.Sub(fresh) < pulseQuiet
	}

	if id != "" {
		if raw, err := s.projectBoard(found.Path); err == nil {
			if rows, err := parseBoardRows(raw); err == nil {
				if row, hit := rows[id]; hit {
					seen, live := s.taskStages(found.Path, id)
					cut, phase := pulsePhaseCut(row.Sect, live, seen, testing)
					out.Phases, out.Phase = pulsePhaseList(cut, phase), phase
					if wt, ok := s.waitLookup(found.Path)(id, row.Sect, row.Block); ok {
						out.Wait = &wt
					}
				}
			}
		}
	}

	switch {
	case out.Wait != nil:
		out.State = pulseWait
		// Ждут те агенты, чьи разговоры стоят молча: работающий агент вопроса
		// не задавал. Разговора может не быть вовсе (вопрос задал субагент
		// закрытого захода), и тогда ждущий один, сама строка.
		for i := range out.Agents {
			if out.Agents[i].State != pulseWork {
				out.Agents[i].State = pulseWait
				out.Waiting++
			}
		}
		if out.Waiting == 0 {
			out.Waiting = 1
		}
	case out.Count == 0:
		out.State = pulseEmpty
	case out.Flow:
		out.State = pulseWork
	default:
		out.State = pulseHush
	}
	writeJSON(w, http.StatusOK, out)
}
