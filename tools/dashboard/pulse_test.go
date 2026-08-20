package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dronrider/devkit/internal/chat"
	"github.com/dronrider/devkit/internal/stage"
)

// Доска для пульса: задача в работе и задача на проверке. Слияние с выкатом
// порознь тут не видны, и Check это единственная секция, по которой обе фазы
// закрываются.
const pulseBoardJSON = `{"prefix":"XR","sections":[` +
	`{"key":"in-progress","title":"In progress","rows":[` +
	`{"id":"XR-1","title":"Задача в работе","type":"task","p":"P2","r":31,"r_parts":[25,3,1,0,2],"cost":"-","link":"-"}]},` +
	`{"key":"check","title":"Check","rows":[` +
	`{"id":"XR-3","title":"Задача на проверке","type":"task","p":"P2","r":32,"r_parts":[25,4,1,0,2],"cost":"-","link":"-"}]}]}`

// Рубеж пройденных фаз собирается из секции строки, живого этапа записи
// ~/.devkit/runs и того, чем агент занят прямо сейчас. Ожидания тут выписаны
// руками по смыслу конвейера: посчитанные тем же кодом они сошлись бы с любым
// его поведением.
func TestPulsePhaseCut(t *testing.T) {
	cases := []struct {
		name    string
		sect    string
		live    string
		seen    []string
		testing bool
		cut     int
		phase   string
	}{
		{"в очереди фаз нет", "backlog", "", nil, false, 0, ""},
		{"взята в работу, идёт код", sectRun, stage.Dev, nil, false, 0, phaseCode},
		{"агент гоняет тесты, код пройден", sectRun, stage.Dev, nil, true, 1, phaseTests},
		{"уточнение это та же разработка", sectRun, stage.Ask, nil, false, 0, phaseCode},
		{"на ревью код с тестами позади", sectRun, stage.Review, nil, false, 2, phaseReview},
		{"проверка после выката закрывает всё", "check", stage.Outside, nil, false, 5, ""},
		{"блок после ревью кода не теряет", "blocked", stage.Outside,
			[]string{stage.Dev, stage.Review}, false, 2, ""},
		{"строка в работе без записи этапа", sectRun, "", nil, false, 0, phaseCode},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			seen := map[string]bool{}
			for _, k := range c.seen {
				seen[k] = true
			}
			cut, phase := pulsePhaseCut(c.sect, c.live, seen, c.testing)
			if cut != c.cut || phase != c.phase {
				t.Fatalf("рубеж %d фаза %q, ждал %d и %q", cut, phase, c.cut, c.phase)
			}
			list := pulsePhaseList(cut, phase)
			if len(list) != 5 {
				t.Fatalf("фаз не пять: %d", len(list))
			}
			done := 0
			for _, ph := range list {
				if ph.Done {
					done++
				}
			}
			if done != c.cut {
				t.Fatalf("закрашено %d фаз, ждал %d", done, c.cut)
			}
		})
	}
}

// Прогон тестов узнаётся по самому ходу агента: это единственный сигнал про
// фазу «тесты», который у дашборда вообще есть.
func TestPulseTesting(t *testing.T) {
	for _, said := range []string{"Bash go test ./tools/...", "Bash regcheck --base main", "Bash PYTEST=1 pytest -q"} {
		if !pulseTesting(said) {
			t.Errorf("прогон тестов не узнан: %q", said)
		}
	}
	for _, said := range []string{"Bash go build ./...", "Read app.js", "Edit pulse.go"} {
		if pulseTesting(said) {
			t.Errorf("за тесты принят чужой ход: %q", said)
		}
	}
}

// pulseTranscript собирает транскрипт с последним вызовом инструмента в
// названный момент: по нему и считается, чем агент занят.
func pulseTranscript(at time.Time, tool, arg string) string {
	stamp := at.UTC().Format(time.RFC3339)
	return fmt.Sprintf(`{"type":"user","message":{"role":"user","content":"выполни XR-1"},"timestamp":%q,"gitBranch":"main"}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","name":%q,"input":{"command":%q}}]},"timestamp":%q}
`, stamp, tool, arg, stamp)
}

// Признак ожидания и запись этапа помечены местным временем без зоны, как их
// пишет конвейер, поэтому «сейчас» у стенда тоже местное.

// pulseEnv поднимает окружение с доской pulseBoardJSON и заданным «сейчас».
func pulseEnv(t *testing.T, now time.Time) (*testEnv, *http.Client) {
	t.Helper()
	e := newTestEnv(t)
	writeScript(t, e.bin, "taskctl", fmt.Sprintf("echo '%s'", pulseBoardJSON))
	writeScript(t, e.bin, "tmux", "exit 1")
	e.s.now = func() time.Time { return now }
	return e, e.loggedClient(t)
}

func getPulse(t *testing.T, e *testEnv, c *http.Client, query string) Pulse {
	t.Helper()
	resp := doReq(t, c, "GET", e.srv.URL+"/api/projects/demo/pulse?"+query, "")
	text := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("пульс: %d %s", resp.StatusCode, text)
	}
	var p Pulse
	if err := json.Unmarshal([]byte(text), &p); err != nil {
		t.Fatalf("ответ пульса не разобрался: %v\n%s", err, text)
	}
	return p
}

// Работающий агент: событие в транскрипте моложе порога молчания, и кольцо
// говорит про работу, дуга бежит, а фаза узнана по прогону тестов.
func TestPulseWorking(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.Local)
	e, c := pulseEnv(t, now)
	seen := now.Add(-12 * time.Second)
	writeSession(t, e.home, e.proj, "", "aaa-1", pulseTranscript(seen, "Bash", "go test ./tools/..."), seen)
	writeBinds(t, e.home, bindRecord("2026-08-20T11:59:00", "aaa-1", "XR-1", "заказ"))

	p := getPulse(t, e, c, "task=XR-1")
	if p.State != pulseWork {
		t.Fatalf("состояние кольца %q, ждал работу", p.State)
	}
	if !p.Flow {
		t.Error("дуга не бежит при свежем событии")
	}
	if p.Count != 1 {
		t.Errorf("агентов в кольце %d, ждал одного", p.Count)
	}
	if p.Since != seen.Unix() {
		t.Errorf("момент последнего события %d, ждал %d", p.Since, seen.Unix())
	}
	if !strings.Contains(p.About, "go test") {
		t.Errorf("чем занят агент: %q", p.About)
	}
	if p.Phase != phaseTests {
		t.Errorf("фаза %q, ждал тесты", p.Phase)
	}
	if len(p.Agents) != 1 || p.Agents[0].State != pulseWork {
		t.Fatalf("список агентов: %+v", p.Agents)
	}
	if p.Quiet != 60 {
		t.Errorf("порог молчания %d с", p.Quiet)
	}
}

// Молчание отличается от пустоты: сессия жива, а событий давно нет. Прежде
// одно от другого было неотличимо, и молчащий агент выглядел бы отсутствующим.
func TestPulseSilentNotEmpty(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.Local)
	e, c := pulseEnv(t, now)
	seen := now.Add(-14 * time.Minute)
	// Транскрипт тронут недавно, а последнее событие в нём давнее: сессия жива,
	// но молчит.
	writeSession(t, e.home, e.proj, "", "aaa-1", pulseTranscript(seen, "Bash", "go build ./..."),
		now.Add(-2*time.Minute))
	writeBinds(t, e.home, bindRecord("2026-08-20T11:45:00", "aaa-1", "XR-1", "заказ"))

	p := getPulse(t, e, c, "task=XR-1")
	if p.State != pulseHush {
		t.Fatalf("состояние кольца %q, ждал молчание", p.State)
	}
	if p.Flow {
		t.Error("дуга бежит при молчащем агенте")
	}
	if p.Count != 1 || p.Agents[0].State != pulseHush {
		t.Fatalf("список агентов: %+v", p.Agents)
	}
	if !strings.Contains(p.About, "go build") {
		t.Errorf("последний ход не назван: %q", p.About)
	}
}

// Живых сессий у задачи нет: кольцо пустое и числа не носит.
func TestPulseEmpty(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.Local)
	e, c := pulseEnv(t, now)
	p := getPulse(t, e, c, "task=XR-1")
	if p.State != pulseEmpty {
		t.Fatalf("состояние кольца %q, ждал пустоту", p.State)
	}
	if p.Count != 0 || len(p.Agents) != 0 {
		t.Fatalf("в пустом кольце нашлись агенты: %+v", p.Agents)
	}
	if len(p.Phases) != 5 {
		t.Fatalf("фаз не пять: %d", len(p.Phases))
	}
}

// Вопрос человеку старше всего остального: агент при этом жив и свеж, а кольцо
// всё равно красное, потому что без ответа заход не двинется.
func TestPulseWaitingBeatsWork(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.Local)
	e, c := pulseEnv(t, now)
	seen := now.Add(-5 * time.Second)
	writeSession(t, e.home, e.proj, "", "aaa-1", pulseTranscript(seen, "Bash", "go build ./..."), seen)
	writeBinds(t, e.home, bindRecord("2026-08-20T11:59:00", "aaa-1", "XR-1", "заказ"))
	writeAskFile(t, e.proj, "XR-1", now.Add(20*time.Minute))

	p := getPulse(t, e, c, "task=XR-1")
	if p.State != pulseWait {
		t.Fatalf("состояние кольца %q, ждал ожидание", p.State)
	}
	if p.Wait == nil || p.Wait.Source != waitAsk {
		t.Fatalf("источник ожидания не назван: %+v", p.Wait)
	}
	if p.Waiting < 1 {
		t.Errorf("ждущих в кольце %d", p.Waiting)
	}
}

// Фазы читаются с записи этапов задачи: секция строки говорит про рубеж, а
// живой этап про то, что идёт сейчас.
func TestPulsePhasesFromStageRecord(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.Local)
	e, c := pulseEnv(t, now)
	writeStageRecord(t, e.home, e.proj, "XR-1", stage.Review, now.Add(-3*time.Minute))

	p := getPulse(t, e, c, "task=XR-1")
	if p.Phase != phaseReview {
		t.Fatalf("фаза %q, ждал ревью", p.Phase)
	}
	done := 0
	for _, ph := range p.Phases {
		if ph.Done {
			done++
		}
	}
	if done != 2 {
		t.Fatalf("закрашено %d фаз, ждал две", done)
	}
	// Задача на проверке слита и выкачена: кольцо у неё полное.
	full := getPulse(t, e, c, "task=XR-3")
	for _, ph := range full.Phases {
		if !ph.Done {
			t.Fatalf("у проверенной задачи фаза %q не пройдена", ph.Name)
		}
	}
}

// writeAskFile кладёт признак ожидания во вход задачи, как это делает taskctl ask.
func writeAskFile(t *testing.T, tree, id string, until time.Time) {
	t.Helper()
	a := chat.Ask{Until: until, Session: "aaa-1", Task: id,
		Questions: []chat.Question{{Text: "чем красить кольцо"}}}
	if err := chat.WriteAsk(tree, chat.TaskName(id), a); err != nil {
		t.Fatal(err)
	}
}

// writeStageRecord кладёт запись этапа задачи, как её пишет конвейер.
func writeStageRecord(t *testing.T, home, root, id, kind string, at time.Time) {
	t.Helper()
	path := stage.Path(home, root, id)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	text := fmt.Sprintf("id = %s\nroot = %s\nэтап = %s | %s | взял\n",
		id, root, kind, at.Format(stage.Stamp))
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Кольцо в шапке разговора: место, четыре состояния, фазы сегментами и список
// агентов. Предмет проверки это собранная разметка, а не написанное в
// исходнике, поэтому статика поднимается в node с заглушкой DOM (стенд
// testdata/poc_ring.mjs). Без node шаг пропускается: узел стенда, а не рабочей
// части.
func TestStaticPulseRing(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node не найден: стенд кольца агентов пропущен")
	}
	out, err := exec.Command(node, filepath.Join("testdata", "poc_ring.mjs"),
		filepath.Join("static", "app.js")).CombinedOutput()
	if err != nil {
		t.Fatalf("кольцо агентов: %v\n%s", err, out)
	}
	t.Log(strings.TrimSpace(string(out)))
}
