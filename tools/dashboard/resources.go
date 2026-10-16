package main

import (
	"net/http"
	"os"
	"runtime"
	"runtime/debug"
	"sort"
	"strings"
	"time"

	"github.com/dronrider/devkit/internal/machload"
	"github.com/dronrider/devkit/internal/peers"
)

// Сводка ресурсов машины: кольца процессора и памяти у иконки, полосы с
// разбивкой по владельцам и топ сессий. Съём стоит трёх подпроцессов (ps,
// vm_stat и системные вызовы), экраны опрашивают его каждые пару секунд, и
// без памяти на ответ каждый круг поднимал бы их заново.

// Ключи владельцев в ответе: клиент красит полосы по ключу, а слова для экрана
// держит у себя.
const (
	resSessKey  = "sessions"
	resDashKey  = "dashboard"
	resOtherKey = "other"
)

// resSessPrefix помечает корень атрибуции как сессию реестра: по нему доли
// сессий сворачиваются в одну полосу и разворачиваются в топ.
const resSessPrefix = "s:"

// resTopLen это длина топа сессий: пять строк по процессору и пять по памяти.
const resTopLen = 5

// Швы съёма для стенда: боевая машина зовёт настоящие ps, системные вызовы и
// реестр сессий, тест кладёт свои ответы.
var (
	resProcs    = func() ([]machload.Proc, error) { return machload.Procs(runProcHome) }
	resPressure = func() (machload.Mem, error) { return machload.MemPressure(runProcHome) }
	resLoadAvg  = machload.LoadAvg
	resPeers    = func(home string) map[string]peer { return peers.Load(home, true) }
	resSelf     = os.Getpid
	resCores    = runtime.NumCPU
)

// ResView это сводка ресурсов машины для колонки и карточки ресурсов.
type ResView struct {
	CPU      ResCPU       `json:"cpu"`
	Mem      ResMem       `json:"mem"`
	Load     []float64    `json:"load,omitempty"`
	Cores    int          `json:"cores"`
	Sessions int          `json:"sessions,omitempty"`
	TopCPU   []ResSession `json:"top_cpu,omitempty"`
	TopMem   []ResSession `json:"top_mem,omitempty"`
	Note     string       `json:"note,omitempty"`
	At       int64        `json:"at"`
}

// ResCPU это процессор машины: процент всей машины и доли владельцев. Проценты
// всей машины, а не одного ядра: кольцо и полосы живут в одной шкале.
type ResCPU struct {
	Pct   float64   `json:"pct"`
	Parts []ResPart `json:"parts,omitempty"`
}

// ResMem это память машины: занятая доля давления и доли владельцев.
// Память в килобайтах, как её отдаёт ps.
type ResMem struct {
	Pct     float64   `json:"pct"`
	UsedKB  int64     `json:"used_kb"`
	TotalKB int64     `json:"total_kb"`
	Parts   []ResPart `json:"parts,omitempty"`
}

// ResPart это доля владельца в съёме: процессор в процентах машины и
// резидентная память в килобайтах.
type ResPart struct {
	Name  string  `json:"name"`
	CPU   float64 `json:"cpu_pct"`
	RSSKB int64   `json:"rss_kb"`
}

// ResSession это строка топа: сессия реестра с её процессором и памятью.
// Proj называет доску, к которой сессия относится по своему рабочему каталогу:
// имя строки ведёт в разговор, а разговор адресуется проектом.
type ResSession struct {
	ID    string  `json:"id"`
	Name  string  `json:"name,omitempty"`
	Proj  string  `json:"proj,omitempty"`
	CPU   float64 `json:"cpu_pct"`
	RSSKB int64   `json:"rss_kb"`
}

// resMemoTTL это срок жизни снимка в памяти процесса: экраны опрашивают ручку
// каждые пару секунд, и взявшиеся разом круги не должны поднимать по ps на
// каждый. После срока следующий запрос снимает заново.
const resMemoTTL = time.Second

// resFlight это идущий съём ресурсов машины. Пока он летит, своего съёма никто
// больше не поднимает: запросы либо ждут его ответа, либо берут прежний.
type resFlight struct {
	done chan struct{}
	view ResView
}

// handleResources отдаёт сводку ресурсов машины. Ручка своя, а не поле ответа
// /api/quota: подписка машинная, но ресурсы отдельное знание со своим съёмом
// и своим тактом опроса.
func (s *server) handleResources(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.resSnapshot())
}

// resSnapshot отдаёт сводку, свежую или из памяти. Память короткая: данные
// ресурсов живут секундами, и протухший снимок хуже подъёма нового.
func (s *server) resSnapshot() ResView {
	s.resMu.Lock()
	if !s.resBorn.IsZero() && s.now().Sub(s.resBorn) < resMemoTTL {
		view := s.resView
		s.resMu.Unlock()
		return view
	}
	if fl := s.resFly; fl != nil {
		s.resMu.Unlock()
		<-fl.done
		s.resMu.Lock()
		view := s.resView
		s.resMu.Unlock()
		return view
	}
	fl := &resFlight{done: make(chan struct{})}
	s.resFly = fl
	s.resMu.Unlock()
	s.resTakeoff(fl)
	s.resMu.Lock()
	view := s.resView
	s.resMu.Unlock()
	return view
}

// resTakeoff это один съём ресурсов: сборка сводки, запись в память и подъём
// ждущих. Паника ловится тут, а не в net/http: запись полёта осталась бы
// занятой навсегда, и следующие запросы вставали бы на ожидании без срока.
// Слова уезжают в заметку сводки, стек в журнал.
func (s *server) resTakeoff(fl *resFlight) {
	defer func() {
		if v := recover(); v != nil {
			rec := &recovered{val: v, stack: debug.Stack()}
			fl.view = ResView{At: s.now().UnixMilli(), Note: s.notePanic("съём ресурсов машины", rec)}
		}
		s.resMu.Lock()
		s.resFly = nil
		s.resView = fl.view
		s.resBorn = s.now()
		s.resMu.Unlock()
		close(fl.done)
	}()
	fl.view = s.resCollect()
}

// resCollect снимает машину одной картинкой: ps одним подпроцессом, память и
// средняя нагрузка системным вызовом. Отказ любого съёма едет словами в
// заметку сводки, а не нулями: нуль на кольце неотличим от свободной машины.
func (s *server) resCollect() ResView {
	view := ResView{At: s.now().UnixMilli(), Cores: resCores()}
	var notes []string
	var parts []ResPart
	procs, err := resProcs()
	if err != nil {
		notes = append(notes, err.Error())
	} else {
		reg := resPeers(s.cfg.Home)
		view.Sessions = len(reg)
		spread := machload.Attribute(procs, resRoots(reg))
		parts = resParts(spread, view.Cores)
		view.CPU = ResCPU{Pct: machPct(machload.Total(procs).CPU, coresF(view.Cores)), Parts: parts}
		view.TopCPU, view.TopMem = resTops(spread, reg, view.Cores)
		cwds := resCwds(reg)
		projects, _ := s.projects()
		resTopProj(view.TopCPU, cwds, projects)
		resTopProj(view.TopMem, cwds, projects)
	}
	if mem, err := resPressure(); err != nil {
		notes = append(notes, err.Error())
	} else {
		view.Mem = ResMem{Pct: mem.UsedPct, UsedKB: mem.Used / 1024, TotalKB: mem.Total / 1024, Parts: parts}
	}
	if load, err := resLoadAvg(); err != nil {
		notes = append(notes, err.Error())
	} else {
		view.Load = load[:]
	}
	view.Note = strings.Join(notes, "; ")
	return view
}

// resParts делит доли атрибуции по владельцам: сессии реестра одним куском,
// дашборд сам по себе, остальное прочим. Проценты переводятся в шкалу всей
// машины, ту же, что у кольца.
func resParts(spread map[string]machload.Spread, cores int) []ResPart {
	f := coresF(cores)
	var sess machload.Spread
	for key, sp := range spread {
		if _, ok := strings.CutPrefix(key, resSessPrefix); ok {
			sess.CPU += sp.CPU
			sess.RSSKB += sp.RSSKB
		}
	}
	dash := spread[resDashKey]
	other := spread[machload.Other]
	return []ResPart{
		{Name: resSessKey, CPU: machPct(sess.CPU, f), RSSKB: sess.RSSKB},
		{Name: resDashKey, CPU: machPct(dash.CPU, f), RSSKB: dash.RSSKB},
		{Name: resOtherKey, CPU: machPct(other.CPU, f), RSSKB: other.RSSKB},
	}
}

// resRoots собирает корни атрибуции: PID сессии -> её ключ, PID дашборда ->
// свой ключ.
func resRoots(reg map[string]peer) map[int]string {
	roots := map[int]string{resSelf(): resDashKey}
	for _, p := range reg {
		roots[p.PID] = resSessPrefix + p.SessionID
	}
	return roots
}

// resTops собирает топы сессий по процессору и по памяти из долей атрибуции.
// Порядок устойчив: при равных числах вперёд идёт сессия с меньшим ID.
func resTops(spread map[string]machload.Spread, reg map[string]peer, cores int) (topCPU, topMem []ResSession) {
	f := coresF(cores)
	names := map[string]string{}
	for _, p := range reg {
		names[p.SessionID] = p.Name
	}
	rows := make([]ResSession, 0, len(spread))
	for key, sp := range spread {
		id, ok := strings.CutPrefix(key, resSessPrefix)
		if !ok {
			continue
		}
		rows = append(rows, ResSession{ID: id, Name: names[id], CPU: machPct(sp.CPU, f), RSSKB: sp.RSSKB})
	}
	n := resTopLen
	if n > len(rows) {
		n = len(rows)
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].CPU != rows[j].CPU {
			return rows[i].CPU > rows[j].CPU
		}
		return rows[i].ID < rows[j].ID
	})
	topCPU = append(topCPU, rows[:n]...)
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].RSSKB != rows[j].RSSKB {
			return rows[i].RSSKB > rows[j].RSSKB
		}
		return rows[i].ID < rows[j].ID
	})
	topMem = append(topMem, rows[:n]...)
	return topCPU, topMem
}

// resCwds собирает рабочие каталоги живых сессий по их id: каталог говорит, к
// какому чекауту сессия относится, а значит, чья доска ей хозяин.
func resCwds(reg map[string]peer) map[string]string {
	cwds := make(map[string]string, len(reg))
	for _, p := range reg {
		if p.Cwd != "" {
			cwds[p.SessionID] = p.Cwd
		}
	}
	return cwds
}

// resTopProj называет проект строк топа по рабочему каталогу сессии: имя строки
// ведёт в разговор, а разговор живёт у доски своего проекта. Сессии одной машины
// раскиданы по проектам, и совпадение доски на экране не гарантировано. Из
// подходящих корней берётся самый глубокий: ворктри лежит внутри чекаута.
func resTopProj(rows []ResSession, cwds map[string]string, projects []Project) {
	for i := range rows {
		dir := cwds[rows[i].ID]
		if dir == "" {
			continue
		}
		best, deep := "", 0
		for _, p := range projects {
			if p.Path == "" ||
				dir != p.Path && !strings.HasPrefix(dir, p.Path+string(os.PathSeparator)) {
				continue
			}
			if len(p.Path) > deep {
				best, deep = p.Name, len(p.Path)
			}
		}
		rows[i].Proj = best
	}
}

// coresF это число ядер для перевода сумм ps в проценты машины: ноль ядер
// живой машине не бывает, а стенду хватает и одного.
func coresF(n int) float64 {
	if n < 1 {
		return 1
	}
	return float64(n)
}

// machPct переводит сумму ps в проценты машины и держит её в шкале кольца.
func machPct(sum float64, cores float64) float64 {
	pct := sum / cores
	if pct < 0 {
		return 0
	}
	if pct > 100 {
		return 100
	}
	return pct
}
