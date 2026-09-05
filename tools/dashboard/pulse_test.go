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
	`{"id":"XR-1","title":"Задача в работе","type":"task","p":"P2","r":31,"r_parts":[25,3,1,0,2],"cost":"-","link":"-"},` +
	`{"id":"XR-7","title":"Цель: пробный цикл","type":"task","p":"P2","r":41,"r_parts":[25,9,3,0,4],"cost":"XL","link":"-"},` +
	`{"id":"XR-8","title":"Вторая задача цели","type":"task","p":"P2","r":30,"r_parts":[25,2,1,0,2],"cost":"-","link":"-"}]},` +
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
		known   bool
	}{
		{"без записи этапов о фазах не известно ничего", "backlog", "", nil, false, 0, "", false},
		{"строка в работе без записи этапа шкалы не даёт", sectRun, "", nil, false, 0, "", false},
		{"взята в работу, идёт код", sectRun, stage.Dev, nil, false, 0, phaseCode, true},
		{"агент гоняет тесты, код пройден", sectRun, stage.Dev, nil, true, 1, phaseTests, true},
		{"уточнение это та же разработка", sectRun, stage.Ask, nil, false, 0, phaseCode, true},
		{"на ревью код с тестами позади", sectRun, stage.Review, nil, false, 2, phaseReview, true},
		{"проверка после выката закрывает всё", "check", stage.Outside, nil, false, 5, "", true},
		{"Check знает про пять фаз и без записи", "check", "", nil, false, 5, "", true},
		{"блок после ревью кода не теряет", "blocked", stage.Outside,
			[]string{stage.Dev, stage.Review}, false, 2, "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			seen := map[string]bool{}
			for _, k := range c.seen {
				seen[k] = true
			}
			cut, phase, known := pulsePhaseCut(c.sect, c.live, seen, c.testing)
			if known != c.known {
				t.Fatalf("знание о фазах %v, ждал %v", known, c.known)
			}
			if cut != c.cut || phase != c.phase {
				t.Fatalf("рубеж %d фаза %q, ждал %d и %q", cut, phase, c.cut, c.phase)
			}
			if !known {
				return
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

// pulseHeldTranscript собирает транскрипт, где вызов инструмента ушёл, а ответа
// на него ещё нет: ровно так выглядит агент посреди долгой команды.
func pulseHeldTranscript(at time.Time, tool, arg string) string {
	stamp := at.UTC().Format(time.RFC3339)
	return fmt.Sprintf(`{"type":"user","message":{"role":"user","content":"выполни XR-1"},"timestamp":%q,"gitBranch":"main"}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":%q,"input":{"command":%q}}]},"timestamp":%q}
`, stamp, tool, arg, stamp)
}

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
	// Запись этапа тут обязательна: без неё о фазах не известно ничего, и
	// шкалы у кольца нет вовсе.
	writeStageRecord(t, e.home, e.proj, "XR-1", stage.Dev, now.Add(-10*time.Minute))

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
	if p.Tool != "Bash" || !strings.Contains(p.About, "go test") {
		t.Errorf("чем занят агент: %q", p.About)
	}
	if p.Phase != phaseTests || p.Scale != scaleStages {
		t.Errorf("шкала %q, фаза %q, ждал тесты по конвейеру", p.Scale, p.Phase)
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
	// Кольцо молчит, а сам разговор простаивает: у кольца это состояние
	// задачи, у агента его собственное, и слова у них разные.
	if p.Count != 1 || p.Agents[0].State != pulseIdle {
		t.Fatalf("список агентов: %+v", p.Agents)
	}
	if p.Tool != "Bash" || !strings.Contains(p.About, "go build") {
		t.Errorf("последний ход не назван: %q", p.About)
	}
}

// План молчащего чата кольцо берёт из файла и подхватывает его переписывание:
// транскрипт у простаивающей сессии не растёт, и заметить смену плана можно
// только по самому файлу (жалоба пользователя: пункты дописываются, кольцо
// стоит).
func TestPulsePlanFileOfSilentChat(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.Local)
	e, c := pulseEnv(t, now)
	seen := now.Add(-40 * time.Minute)
	writeSession(t, e.home, e.proj, "", "aaa-1", pulseTranscript(seen, "Bash", "go build ./..."),
		now.Add(-2*time.Minute))
	writeBinds(t, e.home, bindRecord("2026-08-20T11:15:00", "aaa-1", "XR-1", "заказ"))

	if err := os.MkdirAll(planDir(e.home), 0o755); err != nil {
		t.Fatal(err)
	}
	file := planPath(e.home, "aaa-1")
	first := `[{"text":"разобрать находку","state":"in_progress"}]`
	if err := os.WriteFile(file, []byte(first), 0o644); err != nil {
		t.Fatal(err)
	}
	p := getPulse(t, e, c, "task=XR-1")
	if len(p.Plan) != 1 || p.Plan[0].Text != "разобрать находку" {
		t.Fatalf("план молчащего чата не пришёл в кольцо: %+v", p.Plan)
	}

	// Пункты дописаны, транскрипт при этом не тронут.
	second := `[{"text":"разобрать находку","state":"completed"},{"text":"починить подачу","state":"in_progress"},{"text":"прогнать стенды","state":"pending"}]`
	if err := os.WriteFile(file, []byte(second), 0o644); err != nil {
		t.Fatal(err)
	}
	p = getPulse(t, e, c, "task=XR-1")
	if len(p.Plan) != 3 || p.Plan[0].State != "completed" || p.Plan[2].Text != "прогнать стенды" {
		t.Fatalf("дописанные пункты плана до кольца не доехали: %+v", p.Plan)
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
	// Записи этапов у задачи нет, и шкалы тоже нет: кольцо остаётся
	// индикатором состояния, а не рисует пять неизвестных делений.
	if p.Scale != scaleNone || len(p.Phases) != 0 {
		t.Fatalf("шкала взялась из ниоткуда: %q, делений %d", p.Scale, len(p.Phases))
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
	// Вопрос адресован этой самой сессии, и открытый разговор отвечает за него.
	own := getPulse(t, e, c, "task=XR-1&sid=aaa-1")
	if own.Own == nil || own.Own.State != pulseWait {
		t.Fatalf("открытый разговор не назван ждущим: %+v", own.Own)
	}
	if own.OwnWait == nil {
		t.Fatal("ожидание открытого разговора не приехало")
	}
	if own.Own.WaitSince == 0 {
		t.Error("не сказано, с какого момента ждут")
	}
}

// Ждёт одна сессия задачи, а открытый разговор в это время работает: кольцо
// красное, потому что ход задачи стоит, а слова под названием разговора
// говорят про него самого. Прежде шапка работающего чата уверяла, что вопрос
// задан тут, и человек искал в ленте вопрос, которого в ней нет.
func TestPulseNeighbourWaitsOwnWorks(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.Local)
	e, c := pulseEnv(t, now)
	work := now.Add(-27 * time.Second)
	idle := now.Add(-2 * time.Hour)
	writeSession(t, e.home, e.proj, "", "aaa-1", pulseTranscript(work, "Bash", "go build ./..."), work)
	writeSession(t, e.home, e.proj, "", "bbb-2", pulseTranscript(idle, "Read", "app.js"), now.Add(-time.Minute))
	writeBinds(t, e.home,
		bindRecord("2026-08-20T10:00:00", "aaa-1", "XR-1", "заказ"),
		bindRecord("2026-08-20T10:00:00", "bbb-2", "XR-1", "заказ"))
	writeAskFor(t, e.proj, "XR-1", "bbb-2", now.Add(20*time.Minute))

	p := getPulse(t, e, c, "task=XR-1&sid=aaa-1")
	if p.State != pulseWait {
		t.Fatalf("кольцо не показало вопрос соседней сессии: %q", p.State)
	}
	if p.Waiting != 1 || p.Count != 2 {
		t.Errorf("в кольце %d агентов и %d ждущих", p.Count, p.Waiting)
	}
	// Вопрос тут настоящий и лежит в разговоре bbb-2, а не в причине блока:
	// тихой парковка (DK-696, ревью второго круга) его не делает, кольцо обязано
	// моргать.
	if p.Parked {
		t.Error("живой вопрос соседней сессии помечен тихой парковкой")
	}
	if p.Own == nil || p.Own.State != pulseWork {
		t.Fatalf("открытый разговор не назван работающим: %+v", p.Own)
	}
	if p.OwnWait != nil {
		t.Errorf("чужой вопрос приписан открытому разговору: %+v", p.OwnWait)
	}
	for _, a := range p.Agents {
		want := pulseWork
		if a.Session == "bbb-2" {
			want = pulseWait
		}
		if a.State != want {
			t.Errorf("сессия %s в состоянии %q, ждал %q", a.Session, a.State, want)
		}
	}
}

// Доска для теста тихой парковки: задача стоит в Blocked с машинным разрядом
// причины «вопрос: ...», как её паркует сторожок (tools/devkitctl/watch.py).
const pulseParkedBoardJSON = `{"prefix":"XR","sections":[` +
	`{"key":"blocked","title":"Blocked","rows":[{"id":"XR-1","title":"Задача припаркована","type":"task","p":"P1","r":40,"r_parts":[25,5,5,5,0],"cost":"-","link":"-","sect":"blocked","block":"вопрос: куда катить дальше"}]}]}`

// Строка припаркована вопросом, а рядом с ней жив неродственный разговор той
// же задачи: вопрос всё равно лежит в причине блока, а не в его ленте, и
// живой сосед не делает вопрос доступным ни в одном разговоре. Прежде кольцо
// считало парковку тихой только при нулевом счёте агентов, и живой сосед
// включал моргание там, где вопроса нет и быть не может (приёмка человека
// DK-696, 2026-09-05: DK-565, разговор без вопроса при мигающем кольце).
func TestPulseParkedRingStaysQuietWithLiveNeighbour(t *testing.T) {
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.Local)
	e := newTestEnv(t)
	writeScript(t, e.bin, "taskctl", fmt.Sprintf("echo '%s'", pulseParkedBoardJSON))
	writeScript(t, e.bin, "tmux", "exit 1")
	e.s.now = func() time.Time { return now }
	c := e.loggedClient(t)

	seen := now.Add(-12 * time.Second)
	writeSession(t, e.home, e.proj, "", "aaa-1", pulseTranscript(seen, "Bash", "go build ./..."), seen)
	writeBinds(t, e.home, bindRecord("2026-09-05T11:59:00", "aaa-1", "XR-1", "заказ"))

	p := getPulse(t, e, c, "task=XR-1")
	if p.State != pulseWait {
		t.Fatalf("состояние кольца %q, ждал ожидание", p.State)
	}
	if p.Count == 0 {
		t.Fatal("в кольце нет живого соседа, стенд ничего не проверяет")
	}
	if !p.Parked {
		t.Errorf("кольцо не помечено тихой парковкой при живом соседе: %+v", p)
	}
}

// Повод idle_prompt из журнала уведомителя это простой, а не вопрос: сессия
// жива и её можно спросить, но человека никто не спрашивал. Выданный за
// ожидание он красил кольцо работающей задачи чужой тревогой.
func TestPulseIdlePromptIsNotWaiting(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.Local)
	e, c := pulseEnv(t, now)
	idle := now.Add(-2 * time.Hour)
	writeSession(t, e.home, e.proj, "", "aaa-1", pulseTranscript(idle, "Read", "app.js"), now.Add(-time.Minute))
	writeBinds(t, e.home, bindRecord("2026-08-20T10:00:00", "aaa-1", "XR-1", "заказ"))
	writeIdlePrompt(t, e.home, "aaa-1", now.Add(-2*time.Hour))

	p := getPulse(t, e, c, "task=XR-1&sid=aaa-1")
	if p.Wait != nil {
		t.Fatalf("простой выдан за вопрос человеку: %+v", p.Wait)
	}
	if p.State != pulseHush {
		t.Errorf("состояние кольца %q, ждал молчание", p.State)
	}
	if len(p.Agents) != 1 || p.Agents[0].State != pulseIdle {
		t.Fatalf("сессия не названа простаивающей: %+v", p.Agents)
	}
	if p.Own == nil || p.Own.State != pulseIdle {
		t.Fatalf("открытый разговор не назван простаивающим: %+v", p.Own)
	}
}

// Список агентов называет каждый разговор: имя захода и предмет. Два чата одной
// задачи иначе неразличимы, и человек спрашивает, откуда в кольце второй агент.
func TestPulseAgentsNamed(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.Local)
	e, c := pulseEnv(t, now)
	seen := now.Add(-12 * time.Second)
	body := `{"type":"summary","summary":"Видеть ход работы агентов в чате"}` + "\n" +
		pulseTranscript(seen, "Bash", "go test ./tools/...")
	writeSession(t, e.home, e.proj, "", "aaa-1", body, seen)
	writeBinds(t, e.home, bindRecord("2026-08-20T11:59:00", "aaa-1", "XR-1", "заказ"))

	p := getPulse(t, e, c, "task=XR-1&sid=aaa-1")
	if len(p.Agents) != 1 {
		t.Fatalf("агентов %d", len(p.Agents))
	}
	a := p.Agents[0]
	if a.Title != "Видеть ход работы агентов в чате" {
		t.Errorf("предмет разговора: %q", a.Title)
	}
	if !a.Own {
		t.Error("открытый разговор в списке не помечен")
	}
	if a.Name == "" {
		t.Error("разговор без имени")
	}
}

// Фазы читаются с записи этапов задачи: секция строки говорит про рубеж, а
// живой этап про то, что идёт сейчас.
func TestPulsePhasesFromStageRecord(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.Local)
	e, c := pulseEnv(t, now)
	writeStageRecord(t, e.home, e.proj, "XR-1", stage.Review, now.Add(-3*time.Minute))

	p := getPulse(t, e, c, "task=XR-1")
	if p.Scale != scaleStages {
		t.Fatalf("шкала %q, ждал конвейер задачи", p.Scale)
	}
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

// writeAskFor кладёт вопрос, адресованный названной сессии.
func writeAskFor(t *testing.T, tree, id, sid string, until time.Time) {
	t.Helper()
	a := chat.Ask{Until: until, Session: sid, Task: id,
		Questions: []chat.Question{{Text: "чем красить кольцо"}}}
	if err := chat.WriteAsk(tree, chat.TaskName(id), a); err != nil {
		t.Fatal(err)
	}
}

// writeIdlePrompt кладёт в журнал уведомителя повод «клиент ждёт ввода»: тот
// самый запасной источник, который ожиданием считаться перестал. Строка идёт
// готовой фикстурой формата хука (idleLine), а ID сессии в журнале обрезан до
// восьми знаков, как его пишет notify.py.
func writeIdlePrompt(t *testing.T, home, sid string, at time.Time) {
	t.Helper()
	short := sid
	if len(short) > 8 {
		short = short[:8]
	}
	writeNotifyLog(t, home, []string{idleLine(at.Format("2006-01-02T15:04:05"), short, idlePromptReason)})
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

// Список кольца не показывается по наведению: показ по hover держал его
// открытым поверх снятого класса, и второе нажатие по кольцу выглядело не
// работающим, а увести список с экрана было нечем вовсе (жалоба пользователя).
// Открытие и закрытие идут одним классом, и правило стиля тут одно.
func TestStaticRingPopOpensByClassOnly(t *testing.T) {
	css := readFile(t, filepath.Join("static", "style.css"))
	if strings.Contains(css, ".ringwrap:hover .pop") {
		t.Error("список кольца снова показывается по наведению: закрыть его кликом нельзя")
	}
	if !strings.Contains(css, ".ringwrap.open .pop{display:block}") {
		t.Error("в static/style.css нет показа списка кольца по классу open")
	}
}

// Долгая команда это работа, а не молчание: `go test ./...` идёт две минуты и
// в журнал всё это время не пишет, а вызов стоит без ответа. Прежде такой агент
// выходил простаивающим, и кольцо серело посреди работы.
func TestPulseHeldToolCountsAsWork(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.Local)
	e, c := pulseEnv(t, now)
	began := now.Add(-3 * time.Minute)
	writeSession(t, e.home, e.proj, "", "aaa-1",
		pulseHeldTranscript(began, "Bash", "go test ./tools/..."), began)
	writeBinds(t, e.home, bindRecord("2026-08-20T11:50:00", "aaa-1", "XR-1", "заказ"))

	p := getPulse(t, e, c, "task=XR-1&sid=aaa-1")
	if p.State != pulseWork {
		t.Fatalf("состояние кольца %q, ждал работу: вызов стоит без ответа", p.State)
	}
	if p.Working != 1 || p.Idle != 0 {
		t.Errorf("разбивка: работают %d, простаивают %d", p.Working, p.Idle)
	}
	if len(p.Agents) != 1 || !p.Agents[0].Held {
		t.Fatalf("долгий ход не помечен: %+v", p.Agents)
	}
	if p.Agents[0].Tool != "Bash" || !strings.Contains(p.Agents[0].About, "go test") {
		t.Errorf("подпись хода: %q", p.Agents[0].About)
	}
	// Брошенный хвост закрытого окна работой не считается: вызов без ответа
	// старше получаса это не команда, а сессия, которую убили. Транскрипт при
	// этом тронут недавно, иначе разговор выпал бы из списка живых по своему
	// сроку и проверять было бы нечего.
	later := began.Add(pulseHeld + time.Minute)
	e.s.now = func() time.Time { return later }
	writeSession(t, e.home, e.proj, "", "aaa-1",
		pulseHeldTranscript(began, "Bash", "go test ./tools/..."), later.Add(-time.Minute))
	dead := getPulse(t, e, c, "task=XR-1&sid=aaa-1")
	if dead.State != pulseHush || dead.Working != 0 {
		t.Fatalf("брошенный вызов выдан за работу: %q, работают %d", dead.State, dead.Working)
	}
}

// Число в середине кольца считает работающих, а простаивающие идут своей
// строкой разбивки: сложенные вместе они врали, что работа кипит вдвоём, тогда
// как второй разговор задачи стоит без хода второй час.
func TestPulseCountsSplitByState(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.Local)
	e, c := pulseEnv(t, now)
	work := now.Add(-26 * time.Second)
	idle := now.Add(-2 * time.Hour)
	writeSession(t, e.home, e.proj, "", "aaa-1", pulseTranscript(work, "Bash", "go build ./..."), work)
	writeSession(t, e.home, e.proj, "", "bbb-2", pulseTranscript(idle, "Read", "app.js"), now.Add(-time.Minute))
	writeBinds(t, e.home,
		bindRecord("2026-08-20T10:00:00", "aaa-1", "XR-1", "заказ"),
		bindRecord("2026-08-20T10:00:00", "bbb-2", "XR-1", "заказ"))

	p := getPulse(t, e, c, "task=XR-1&sid=aaa-1")
	if p.Count != 2 || p.Working != 1 || p.Idle != 1 || p.Waiting != 0 {
		t.Fatalf("разбивка: всего %d, работают %d, простаивают %d, ждут %d",
			p.Count, p.Working, p.Idle, p.Waiting)
	}
	if p.State != pulseWork || !p.Flow {
		t.Errorf("состояние кольца %q, дуга %v", p.State, p.Flow)
	}
}

// Ход подписывается тем же разбором, что и лента: имя инструмента с главным
// доводом. Прежде подпись брала первое попавшееся поле ввода, и два разных
// разговора выходили с одинаковым «SendMessage».
func TestPulseAboutNamesTheStep(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.Local)
	e, c := pulseEnv(t, now)
	seen := now.Add(-9 * time.Second)
	writeSession(t, e.home, e.proj, "", "aaa-1", pulseTranscript(seen, "Bash", "go test ./tools/..."), seen)
	writeBinds(t, e.home, bindRecord("2026-08-20T11:59:00", "aaa-1", "XR-1", "заказ"))

	p := getPulse(t, e, c, "task=XR-1&sid=aaa-1")
	// Имя инструмента и довод хода едут врозь: склеенные в одну строку, они
	// читались кашей вроде «последний ход SendMessage Кольцо врёт прогрессом:
	// чинить класс», где имя инструмента не отличить от начала реплики
	// (замечание пользователя по снимку).
	if p.Agents[0].Tool != "Bash" || p.Agents[0].About != "go test ./tools/..." {
		t.Fatalf("подпись хода: инструмент %q, довод %q", p.Agents[0].Tool, p.Agents[0].About)
	}
	if strings.Contains(p.Agents[0].About, "Bash") {
		t.Errorf("имя инструмента затесалось в довод: %q", p.Agents[0].About)
	}
}

// pulseGoalDoc собирает файл цели с разделом «Задачи цели»: тем же разделом
// читает состав экран цели.
func pulseGoalDoc(id string, ids ...string) string {
	body := "# " + id + ": Цель: пробный цикл\n\n## Задачи цели\n\n"
	for _, one := range ids {
		body += "- " + one + " что-то сделать\n"
	}
	return body
}

// pulseArchive собирает архив: по строке архива задача цели считается закрытой.
func pulseArchive(ids ...string) string {
	body := "# Архив (префикс XR)\n\n| ID | Задача | Тип | P | Закрыто | Ссылка |\n" +
		"|--------|--------|-----|---|---------|--------|\n"
	for _, id := range ids {
		body += "| " + id + " | закрытая задача | task | P2 | 2026-08-01 | - |\n"
	}
	return body
}

// У цели конвейер задачи неприменим по смыслу: она не проходит код с ревью, а
// режется на задачи, и её ход это доля закрытых. Считает их тот же разбор,
// каким живёт экран цели, второго счёта тут не заводится.
func TestPulseGoalScaleCountsTasks(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.Local)
	e, c := pulseEnv(t, now)
	writeGoalDoc(t, e.proj, "XR-7", pulseGoalDoc("XR-7", "XR-1", "XR-8", "XR-9", "XR-10"))
	writeAt(t, filepath.Join(e.proj, "docs", "TASKS-archive.md"), pulseArchive("XR-9", "XR-10"))
	// Запись этапов у цели есть, и она всё равно не даёт шкалы конвейера:
	// вид строки старше записи.
	writeStageRecord(t, e.home, e.proj, "XR-7", stage.Dev, now.Add(-time.Hour))

	p := getPulse(t, e, c, "task=XR-7")
	if !p.Goal {
		t.Fatal("строка цели не узнана")
	}
	if p.Scale != scaleGoal {
		t.Fatalf("шкала %q, ждал задачи цели", p.Scale)
	}
	if p.Total != 4 || p.Done != 2 {
		t.Fatalf("задач цели %d, закрыто %d, ждал 4 и 2", p.Total, p.Done)
	}
	if len(p.Phases) != 0 || p.Phase != "" {
		t.Errorf("цели приписали фазы конвейера: %+v %q", p.Phases, p.Phase)
	}
}

// Цель без нарезки шкалы не имеет: раздела «Задачи цели» в файле нет, считать
// нечего, и ноль задач из нуля тут был бы такой же выдумкой, как пустая
// пятёрка фаз у задачи без записи.
func TestPulseGoalWithoutCutHasNoScale(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.Local)
	e, c := pulseEnv(t, now)

	p := getPulse(t, e, c, "task=XR-7")
	if !p.Goal {
		t.Fatal("строка цели не узнана")
	}
	if p.Scale != scaleNone || p.Total != 0 {
		t.Fatalf("ненарезанная цель нарисовала шкалу: %q, задач %d", p.Scale, p.Total)
	}
}

// Задача без записи этапов шкалы не получает вовсе: кольцо остаётся
// индикатором состояния. Прежде оно показывало «идёт первая из пяти» и там,
// где о ходе задачи не известно ничего.
func TestPulseNoStageRecordNoScale(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.Local)
	e, c := pulseEnv(t, now)
	seen := now.Add(-9 * time.Second)
	writeSession(t, e.home, e.proj, "", "aaa-1", pulseTranscript(seen, "Bash", "go build ./..."), seen)
	writeBinds(t, e.home, bindRecord("2026-08-20T11:59:00", "aaa-1", "XR-1", "заказ"))

	p := getPulse(t, e, c, "task=XR-1&sid=aaa-1")
	if p.Scale != scaleNone {
		t.Fatalf("шкала взялась из ниоткуда: %q", p.Scale)
	}
	if len(p.Phases) != 0 || p.Phase != "" {
		t.Fatalf("деления нарисованы без источника: %+v %q", p.Phases, p.Phase)
	}
	// Состояние при этом на месте: без шкалы кольцо не перестаёт показывать,
	// что работа идёт.
	if p.State != pulseWork || p.Working != 1 {
		t.Errorf("состояние потерялось вместе со шкалой: %q, работают %d", p.State, p.Working)
	}
}

// Задача в Check знает про пять пройденных фаз и без записи этапов: слияние с
// выкатом это секция строки, а не догадка.
func TestPulseCheckScaleWithoutRecord(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.Local)
	e, c := pulseEnv(t, now)

	p := getPulse(t, e, c, "task=XR-3")
	if p.Scale != scaleStages {
		t.Fatalf("шкала %q, ждал конвейер задачи", p.Scale)
	}
	for _, ph := range p.Phases {
		if !ph.Done {
			t.Fatalf("у проверенной задачи фаза %q не пройдена", ph.Name)
		}
	}
}

// Кольцо не перечитывает боковые журналы каждым тиком: ход сессии зависит от
// хвоста файла, и неизменившийся файл отвечает из памяти процесса. Прежде
// сотня журналов долгого разговора перечитывалась на каждый опрос кольца, и
// кольцо выходило дороже самой ленты.
func TestPulseRemembersJournalSteps(t *testing.T) {
	e := newTestEnv(t)
	forgetPulseSteps()
	path := writeSession(t, e.home, e.proj, "", "aaa-1", transcriptFixture, time.Now())
	for i := 0; i < 40; i++ {
		fatSubLog(t, path, fmt.Sprintf("p%02d", i), 1<<20)
	}
	at := time.Now()
	first := pulseTrace(path)
	cold := time.Since(at)
	at = time.Now()
	again := pulseTrace(path)
	warm := time.Since(at)
	if first.At.IsZero() || first != again {
		t.Fatalf("ход кольца разошёлся: %+v против %+v", first, again)
	}
	if warm > cold/5 || warm > 30*time.Millisecond {
		t.Fatalf("повторный опрос кольца стоит %v против первого %v", warm, cold)
	}
}

// Панель, открытая по заблокированной задаче без разговоров: кольцо не моргает
// тревогой, причина блока сказана словами, о пустоте говорится один раз, а
// плашка поля нейтральная. Предмет проверки это собранная разметка, поэтому
// статика поднимается в node с заглушкой DOM (стенд testdata/poc_taskpanel.mjs).
// Без node шаг пропускается: узел стенда, а не рабочей части.
func TestStaticTaskPanelBlocked(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node не найден: стенд панели заблокированной задачи пропущен")
	}
	out, err := exec.Command(node, filepath.Join("testdata", "poc_taskpanel.mjs"),
		filepath.Join("static", "app.js")).CombinedOutput()
	if err != nil {
		t.Fatalf("панель заблокированной задачи: %v\n%s", err, out)
	}
	t.Log(strings.TrimSpace(string(out)))
}
