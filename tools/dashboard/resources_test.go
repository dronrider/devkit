package main

import (
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dronrider/devkit/internal/machload"
)

// Фикстура снимка процессов: сессия с детьми и внуком, дашборд со своим
// ребёнком, поднятый из сессии, но корень сам по себе, и одинокий чужой
// процесс без корня.
var resProcsFixture = []machload.Proc{
	{PID: 100, PPID: 1, PCPU: 10, RSSKB: 100000},
	{PID: 110, PPID: 100, PCPU: 20, RSSKB: 200000},
	{PID: 111, PPID: 110, PCPU: 5, RSSKB: 50000},
	{PID: 200, PPID: 100, PCPU: 7, RSSKB: 70000},
	{PID: 210, PPID: 200, PCPU: 3, RSSKB: 30000},
	{PID: 300, PPID: 1, PCPU: 1, RSSKB: 1000},
}

// В реестре две живые сессии, но процессов второй в снимке нет: ушедшая
// сессия не занимает ни кольцо, ни топ.
var resPeersFixture = map[string]peer{
	"sess-one": {PID: 100, SessionID: "sess-one", Name: "задача один"},
	"sess-two": {PID: 250, SessionID: "sess-two", Name: "задача два"},
}

// resSeams подменяет швы съёма на фикстуры: десять ядер, дашборд это PID 200
// из снимка, память занята ровно наполовину.
func resSeams(t *testing.T) {
	t.Helper()
	oldProcs, oldPressure, oldLoad, oldReg, oldSelf, oldCores :=
		resProcs, resPressure, resLoadAvg, resPeers, resSelf, resCores
	resProcs = func() ([]machload.Proc, error) { return resProcsFixture, nil }
	resPressure = func() (machload.Mem, error) {
		return machload.Mem{Total: 16 << 30, Used: 8 << 30, UsedPct: 50}, nil
	}
	resLoadAvg = func() ([3]float64, error) { return [3]float64{1, 2, 3}, nil }
	resPeers = func(string) map[string]peer { return resPeersFixture }
	resSelf = func() int { return 200 }
	resCores = func() int { return 10 }
	t.Cleanup(func() {
		resProcs, resPressure, resLoadAvg, resPeers, resSelf, resCores =
			oldProcs, oldPressure, oldLoad, oldReg, oldSelf, oldCores
	})
}

func getRes(t *testing.T, e *testEnv) ResView {
	t.Helper()
	c := e.loggedClient(t)
	resp, err := c.Get(e.srv.URL + "/api/resources")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("код ответа %d, жду 200", resp.StatusCode)
	}
	var out ResView
	if err := json.Unmarshal([]byte(body(t, resp)), &out); err != nil {
		t.Fatalf("ответ ресурсов не разобрался: %v", err)
	}
	return out
}

func almost(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

// Сводка собирается одной картинкой: кольца в процентах всей машины, полосы
// тремя владельцами, топ из сессий, что реально занимают машину.
func TestResourcesView(t *testing.T) {
	e := newTestEnv(t)
	e.s.now = func() time.Time { return time.Date(2026, 10, 15, 12, 0, 0, 0, time.Local) }
	resSeams(t)
	// Каталог первой сессии ведёт в проект стенда: имя строки топа ссылается
	// в разговор, а разговор адресуется доской своего проекта.
	demo := ""
	projects, _ := e.s.projects()
	for _, p := range projects {
		if p.Name == "demo" {
			demo = p.Path
		}
	}
	if demo == "" {
		t.Fatal("проект стенда не нашёлся")
	}
	withCwd := map[string]peer{}
	for id, p := range resPeersFixture {
		if id == "sess-one" {
			p.Cwd = demo
		}
		withCwd[id] = p
	}
	oldPeers := resPeers
	resPeers = func(string) map[string]peer { return withCwd }
	t.Cleanup(func() { resPeers = oldPeers })

	view := getRes(t, e)
	if view.Note != "" {
		t.Fatalf("съём цел, а ответ жалуется: %s", view.Note)
	}
	if view.Cores != 10 {
		t.Fatalf("ядер %d, жду 10", view.Cores)
	}
	// Сумма ps по снимку это 46 долей ядра, на десяти ядрах машины 4.6%.
	if !almost(view.CPU.Pct, 4.6) {
		t.Fatalf("процент процессора %g, жду 4.6", view.CPU.Pct)
	}
	if len(view.CPU.Parts) != 3 {
		t.Fatalf("долей владельцев %d, жду три", len(view.CPU.Parts))
	}
	sess, dash, other := view.CPU.Parts[0], view.CPU.Parts[1], view.CPU.Parts[2]
	if sess.Name != resSessKey || !almost(sess.CPU, 3.5) || sess.RSSKB != 350000 {
		t.Fatalf("доля сессий %+v", sess)
	}
	if dash.Name != resDashKey || !almost(dash.CPU, 1) || dash.RSSKB != 100000 {
		t.Fatalf("доля дашборда %+v", dash)
	}
	if other.Name != resOtherKey || !almost(other.CPU, 0.1) || other.RSSKB != 1000 {
		t.Fatalf("доля прочего %+v", other)
	}
	if !almost(view.Mem.Pct, 50) || view.Mem.UsedKB != 8<<20 || view.Mem.TotalKB != 16<<20 {
		t.Fatalf("память %+v", view.Mem)
	}
	if len(view.Mem.Parts) != 3 || view.Mem.Parts[0].RSSKB != 350000 {
		t.Fatalf("доли памяти %+v", view.Mem.Parts)
	}
	if len(view.Load) != 3 || view.Load[2] != 3 {
		t.Fatalf("средняя нагрузка %v, жду минуту, пять и пятнадцать", view.Load)
	}
	// Живых сессий считает реестр, а не снимок процессов: и та, что процессов
	// не занимает, стоит в числе.
	if view.Sessions != 2 {
		t.Fatalf("живых сессий %d, жду две", view.Sessions)
	}
	// Ушедшая сессия без процессов в топ не попадает: топ из занимающих.
	if len(view.TopCPU) != 1 || view.TopCPU[0].ID != "sess-one" || view.TopCPU[0].Name != "задача один" {
		t.Fatalf("топ по процессору %+v", view.TopCPU)
	}
	if !almost(view.TopCPU[0].CPU, 3.5) || view.TopCPU[0].RSSKB != 350000 {
		t.Fatalf("строка топа %+v", view.TopCPU[0])
	}
	if view.TopCPU[0].Proj != "demo" {
		t.Fatalf("проект строки топа %q, жду demo", view.TopCPU[0].Proj)
	}
	if len(view.TopMem) != 1 || view.TopMem[0].ID != "sess-one" {
		t.Fatalf("топ по памяти %+v", view.TopMem)
	}
	if view.TopMem[0].Proj != "demo" {
		t.Fatalf("проект строки топа по памяти %q, жду demo", view.TopMem[0].Proj)
	}
}

// Проект строки топа узнаётся по рабочему каталогу сессии: ворктри внутри
// чекаута достаётся самому глубокому корню, чужой каталог остаётся без
// проекта, а сессия без каталога не трогается вовсе.
func TestResTopProj(t *testing.T) {
	rows := []ResSession{{ID: "in-tree"}, {ID: "in-checkout"}, {ID: "outside"}, {ID: "nowhere"}}
	projects := []Project{
		{Name: "outer", Path: "/machine/outer"},
		{Name: "inner", Path: "/machine/outer/inner"},
	}
	resTopProj(rows, map[string]string{
		"in-tree":     "/machine/outer/inner/tree",
		"in-checkout": "/machine/outer",
		"outside":     "/machine/elsewhere",
	}, projects)
	want := []string{"inner", "outer", "", ""}
	for i := range rows {
		if rows[i].Proj != want[i] {
			t.Fatalf("проект строки %s: %q, жду %q", rows[i].ID, rows[i].Proj, want[i])
		}
	}
}

// Отказ съёма едет словами в заметку, а не нулями: нуль на кольце неотличим
// от свободной машины. Остальные съёмы при этом доходят до ответа.
func TestResourcesRefusalInWords(t *testing.T) {
	e := newTestEnv(t)
	resSeams(t)
	old := resProcs
	resProcs = func() ([]machload.Proc, error) { return nil, errors.New("ps не поднят: стенд") }
	t.Cleanup(func() { resProcs = old })

	view := getRes(t, e)
	if view.Note == "" {
		t.Fatal("съём процессов упал, а заметка молчит")
	}
	if view.CPU.Parts != nil {
		t.Fatalf("отказавший съём оставил доли: %+v", view.CPU.Parts)
	}
	if len(view.TopCPU) != 0 || len(view.TopMem) != 0 {
		t.Fatalf("без снимка процессов топов нет: %+v %+v", view.TopCPU, view.TopMem)
	}
	if !almost(view.Mem.Pct, 50) {
		t.Fatalf("память жива и без ps: %+v", view.Mem)
	}
	if len(view.Load) != 3 {
		t.Fatalf("средняя нагрузка жива и без ps: %v", view.Load)
	}
}

// Ручка держит один съём: два запроса, взявшихся разом, поднимают один ps, и
// оба уезжают с одним ответом. Встреча запросов сводится каналами, а не
// выдержкой времени.
func TestResourcesSingleFlight(t *testing.T) {
	e := newTestEnv(t)
	resSeams(t)
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	var calls atomic.Int32
	old := resProcs
	resProcs = func() ([]machload.Proc, error) {
		calls.Add(1)
		once.Do(func() { close(started) })
		<-release
		return resProcsFixture, nil
	}
	t.Cleanup(func() { resProcs = old })

	c := e.loggedClient(t)
	var wg sync.WaitGroup
	var first, second ResView
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			resp, err := c.Get(e.srv.URL + "/api/resources")
			if err != nil {
				t.Error(err)
				return
			}
			var out ResView
			if err := json.Unmarshal([]byte(body(t, resp)), &out); err != nil {
				t.Error(err)
				return
			}
			if n == 0 {
				first = out
			} else {
				second = out
			}
		}(i)
	}
	<-started
	close(release)
	wg.Wait()
	if got := calls.Load(); got != 1 {
		t.Fatalf("съёмов поднято %d, жду один на два запроса", got)
	}
	if first.At != second.At {
		t.Fatalf("запросы уехали с разными ответами: %d и %d", first.At, second.At)
	}
}

// Память ответа живёт секунду: подряд идущие круги не поднимают ps заново, а
// после срока следующий запрос снимает свежий.
func TestResourcesMemo(t *testing.T) {
	e := newTestEnv(t)
	base := time.Date(2026, 10, 15, 12, 0, 0, 0, time.Local)
	e.s.now = func() time.Time { return base }
	resSeams(t)
	var calls atomic.Int32
	old := resProcs
	resProcs = func() ([]machload.Proc, error) {
		calls.Add(1)
		return resProcsFixture, nil
	}
	t.Cleanup(func() { resProcs = old })

	if view := getRes(t, e); !almost(view.CPU.Pct, 4.6) {
		t.Fatalf("первый снимок не собрался: %+v", view)
	}
	if view := getRes(t, e); calls.Load() != 1 {
		t.Fatalf("съёмов %d внутри срока памяти, жду один", calls.Load())
	} else if !almost(view.CPU.Pct, 4.6) {
		t.Fatalf("ответ из памяти неполон: %+v", view)
	}
	e.s.now = func() time.Time { return base.Add(2 * resMemoTTL) }
	getRes(t, e)
	if calls.Load() != 2 {
		t.Fatalf("после срока памяти съёмов %d, жду два", calls.Load())
	}
}
