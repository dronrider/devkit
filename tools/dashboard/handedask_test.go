package main

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dronrider/devkit/internal/chat"
)

// Вопрос розданной работы доезжает до раздавшего разговора. Субагент ходит с
// ID внешней сессии, и признак ожидания, который он кладёт под именем своей
// задачи, несёт сессию того разговора, откуда работу раздали. До скана по
// сессии вопрос был виден только в разговоре задачи, человек сидел в другом,
// срок выходил молча, и работа вставала парковкой (живые случаи DK-517,
// DK-543 и слияние цели DK-397).

// handedBind это строка реестра про сессию с задачей и родителем разом: у
// bindRecord нет родителя, у bindParent задачи, а делегату нужны оба поля.
func handedBind(home, stamp, sid, task, parent string) string {
	return fmt.Sprintf("%s сессия %s задача %s проект demo дерево /tmp/demo "+
		"транскрипт %s источник заказ повод startup tmux - родитель %s\n",
		stamp, sid, task, standTranscript(home, sid), parent)
}

// Пульс открытого разговора видит вопрос, который лежит под именем чужой
// задачи, но адресован его сессии: раздавший разговор ждёт человека, и кольцо
// с плашкой обязаны об этом сказать.
func TestPulseHandedAskReachesDispatcher(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.Local)
	e, c := pulseEnv(t, now)
	seen := now.Add(-5 * time.Second)
	writeSession(t, e.home, e.proj, "", "aaa-1", pulseTranscript(seen, "Bash", "go build ./..."), seen)
	writeBinds(t, e.home, bindRecord(e.home, "2026-08-20T11:59:00", "aaa-1", "XR-1", "заказ"))
	writeAskFor(t, e.proj, "XR-9", "aaa-1", now.Add(20*time.Minute))

	p := getPulse(t, e, c, "task=XR-1&sid=aaa-1")
	if p.State != pulseWait {
		t.Fatalf("кольцо не показало вопрос розданной работы: %q", p.State)
	}
	if p.OwnWait == nil || p.OwnWait.Task != "XR-9" {
		t.Fatalf("ожидание открытого разговора не назвало задачу вопроса: %+v", p.OwnWait)
	}
	if len(p.Asks) != 1 || len(p.Asks[0].Questions) == 0 {
		t.Fatalf("вопросы для плашки не приехали: %+v", p.Asks)
	}
	if p.Own == nil || p.Own.State != pulseWait {
		t.Fatalf("открытый разговор не назван ждущим: %+v", p.Own)
	}
	if p.Waiting < 1 {
		t.Errorf("ждущих в кольце %d", p.Waiting)
	}
}

// Делегат второй подписки ходит со своей сессией, и признак несёт её, а не
// раздавший разговор: к родителю вопрос приводит реестр по полю «родитель».
func TestPulseHandedAskViaParent(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.Local)
	e, c := pulseEnv(t, now)
	seen := now.Add(-5 * time.Second)
	writeSession(t, e.home, e.proj, "", "aaa-1", pulseTranscript(seen, "Bash", "go build ./..."), seen)
	writeBinds(t, e.home,
		bindRecord(e.home, "2026-08-20T11:59:00", "aaa-1", "XR-1", "заказ"),
		handedBind(e.home, "2026-08-20T11:59:30", "bbb-7", "-", "aaa-1"))
	writeAskFor(t, e.proj, "XR-9", "bbb-7", now.Add(20*time.Minute))

	p := getPulse(t, e, c, "task=XR-1&sid=aaa-1")
	if p.OwnWait == nil || p.OwnWait.Task != "XR-9" {
		t.Fatalf("вопрос делегата не дошёл до раздавшего разговора: %+v", p.OwnWait)
	}
	if p.State != pulseWait {
		t.Errorf("кольцо не показало ожидание: %q", p.State)
	}
}

// Чужой и протухший вопросы к открытому разговору не липнут: у чужого сессия
// не эта и родитель не этот, у протухшего ждущего за признаком больше нет.
func TestPulseHandedAskIgnoresForeignAndStale(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.Local)
	e, c := pulseEnv(t, now)
	seen := now.Add(-5 * time.Second)
	writeSession(t, e.home, e.proj, "", "aaa-1", pulseTranscript(seen, "Bash", "go build ./..."), seen)
	writeBinds(t, e.home, bindRecord(e.home, "2026-08-20T11:59:00", "aaa-1", "XR-1", "заказ"))
	writeAskFor(t, e.proj, "XR-8", "zzz-9", now.Add(20*time.Minute))
	writeAskFor(t, e.proj, "XR-9", "aaa-1", now.Add(-time.Minute))

	p := getPulse(t, e, c, "task=XR-1&sid=aaa-1")
	if p.OwnWait != nil || len(p.Asks) != 0 {
		t.Fatalf("чужой или протухший вопрос приписан разговору: %+v %+v", p.OwnWait, p.Asks)
	}
}

// foreignProject кладёт рядом с demo второй проект машины и отдаёт его путь:
// признак вопроса по чужой задаче лежит в дереве её проекта, а не в том, где
// живёт спрашивающий разговор.
func foreignProject(t *testing.T, e *testEnv) string {
	t.Helper()
	dir := filepath.Join(e.home, "projects", "other")
	mkProject(t, dir)
	return dir
}

// Скан признаков идёт по всем деревьям машины, и порядок вопросов сводится
// один на все деревья: спросившие в разных проектах отвечают вразнобой, и
// первым обязан встать спросивший раньше, а не спросивший раньше своего
// дерева. Варианты вопроса разворачиваются в строки экрана с пометкой
// рекомендованного.
// Порядок вопросов без срока (DK-715) считается по времени файла, а не по
// дедлайну: срока у признака больше нет, и отвечают тому, кто спросил раньше,
// а не тому, у кого «горит» сильнее. Время файла тут проставлено руками
// (os.Chtimes), а не выведено из порядка записи: обе записи в тесте ложатся
// в одну секунду по живым часам, а mtime у признака секундной точности.
func TestHandedAsksOrderAndLines(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.Local)
	e, c := pulseEnv(t, now)
	foreign := foreignProject(t, e)
	seen := now.Add(-5 * time.Second)
	writeSession(t, e.home, e.proj, "", "aaa-1", pulseTranscript(seen, "Bash", "go build ./..."), seen)
	writeBinds(t, e.home, bindRecord(e.home, "2026-08-20T11:59:00", "aaa-1", "XR-1", "заказ"))
	early := chat.Ask{Session: "aaa-1", Task: "OT-2",
		Questions: []chat.Question{{Text: "режем строку"}}}
	late := chat.Ask{Session: "aaa-1", Task: "XR-3",
		Questions: []chat.Question{{Text: "куда катить", Options: []chat.Option{
			{Label: "в прод", Note: "сразу", Recommended: true},
			{Label: "в стенд"},
		}}}}
	trees := map[string]string{"OT-2": foreign, "XR-3": e.proj}
	stamps := map[string]time.Time{"OT-2": now.Add(-10 * time.Minute), "XR-3": now.Add(-time.Minute)}
	for _, a := range []chat.Ask{early, late} {
		tree := trees[a.Task]
		if err := chat.WriteAsk(tree, chat.TaskName(a.Task), a); err != nil {
			t.Fatal(err)
		}
		at := stamps[a.Task]
		if err := os.Chtimes(chat.AskPath(tree, chat.TaskName(a.Task)), at, at); err != nil {
			t.Fatal(err)
		}
	}
	p := getPulse(t, e, c, "task=XR-1&sid=aaa-1")
	if len(p.Asks) != 2 || p.Asks[0].Task != "OT-2" || p.Asks[1].Task != "XR-3" {
		t.Fatalf("порядок вопросов не по времени файла: %+v", p.Asks)
	}
	want := []string{"куда катить", "* в прод сразу", "- в стенд"}
	if strings.Join(p.Asks[1].Questions, "|") != strings.Join(want, "|") {
		t.Fatalf("строки вопроса собрались не так: %q", p.Asks[1].Questions)
	}
	if p.Asks[1].Until != 0 {
		t.Fatalf("срок не нулевой у признака без дедлайна: %+v", p.Asks[1])
	}
}

// Вопрос по задаче чужого проекта доезжает пульсу спрашивающего разговора.
// Признак лежит в дереве задачи, туда его кладёт taskctl ask основного
// чекаута, а разговор спрашивающего живёт в другом проекте: конвейер
// диспетчера спрашивает по чужой доске из своего окна, и скан по одному
// дереву оставлял разговор без вопроса вовсе (живой случай DK-1379).
func TestPulseHandedAskFromForeignProject(t *testing.T) {
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.Local)
	e, c := pulseEnv(t, now)
	foreign := foreignProject(t, e)
	seen := now.Add(-5 * time.Second)
	writeSession(t, e.home, e.proj, "", "aaa-1", pulseTranscript(seen, "Bash", "go build ./..."), seen)
	writeBinds(t, e.home, bindRecord(e.home, "2026-10-14T11:59:00", "aaa-1", "XR-1", "заказ"))
	writeAskFor(t, foreign, "OT-7", "aaa-1", now.Add(20*time.Minute))

	p := getPulse(t, e, c, "task=XR-1&sid=aaa-1")
	if p.OwnWait == nil || p.OwnWait.Task != "OT-7" {
		t.Fatalf("вопрос чужого дерева не дошёл до разговора спрашивающего: %+v", p.OwnWait)
	}
	if len(p.Asks) != 1 || len(p.Asks[0].Questions) == 0 {
		t.Fatalf("вопросы для плашки не приехали: %+v", p.Asks)
	}
	if p.State != pulseWait {
		t.Errorf("кольцо не показало ожидание: %q", p.State)
	}
}

// Тот же вопрос виджетом у ручки вопроса: галочки вариантов вешаются на
// строки вопроса в разговоре спрашивающего, какой бы проект ни держал задачу.
func TestChatAskHandedFromForeignProject(t *testing.T) {
	sid := "aaaa1111-1111-4111-8111-111111111111"
	e, c := chatEnv(t)
	foreign := foreignProject(t, e)
	writeAskPack(t, foreign, "OT-7", sid, time.Now().Add(5*time.Minute), chat.Question{
		Text: "снимаем ребро или ждём",
		Options: []chat.Option{
			{Label: "снимаем", Note: "слиянием", Recommended: true},
			{Label: "ждём"},
		},
	})

	ask := askOf(t, e, c, sid)
	if ask.Kind != askKindAgent || ask.Task != "OT-7" {
		t.Fatalf("виджет вопроса чужого дерева не собрался: %+v", ask)
	}
	if len(ask.Options) != 2 || ask.Options[0].Text != "снимаем" {
		t.Fatalf("варианты чужого дерева не приехали: %+v", ask.Options)
	}
}

// Ответ репликой в разговоре спрашивающего ложится во вход дерева задачи и
// снимает признак там же: вход слушает ждущий инструмент именно того дерева,
// а припаркованную строку будит её сторожок.
func TestChatSaySettlesForeignAsk(t *testing.T) {
	sid := "abab1111-2222-4333-8444-555566667777"
	e, c, frames := askSayEnvNoTerm(t, sid)
	foreign := foreignProject(t, e)
	writeAskSign(t, foreign, "task-OT-7", sid, "OT-7", time.Now().Add(5*time.Minute))

	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/say",
		sayBody("снимаем слиянием", "h-3"))
	said := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("ответ раздавшего разговора отбит: %d %s", resp.StatusCode, said)
	}
	if got := frames(); len(got) != 0 {
		t.Fatalf("реплика ушла в сокет клиента мимо ждущего: %q", got)
	}
	lines := chatLines(t, foreign, "task-OT-7")
	if len(lines) != 1 {
		t.Fatalf("во входе дерева задачи строк %d, ждали одну: %q", len(lines), lines)
	}
	if chat.Addressee(lines[0]) != "" || chat.Said(lines[0]) != "снимаем слиянием" {
		t.Fatalf("реплика легла не той строкой: %q", lines[0])
	}
	if _, ok := chat.ReadAsk(chat.AskPath(foreign, "task-OT-7")); ok {
		t.Fatal("признак ожидания в дереве задачи не снялся")
	}
	if lines := chatLines(t, e.proj, "task-OT-7"); len(lines) != 0 {
		t.Fatalf("ответ лёг в дерево разговора, где его никто не читает: %q", lines)
	}
	if !strings.Contains(said, `"ask"`) || !strings.Contains(said, "OT-7") {
		t.Fatalf("ручка не назвала дорогу и задачу вопроса: %s", said)
	}
}

// Реплика раздавшего разговора уходит во вход, который слушает инструмент
// ожидания, а не в сокет клиента: ждущий сидит в ходе Bash и сокета не слышит,
// а реплика в сокете легла бы в очередь следующего хода, и ожидание добрало бы
// срок с готовым ответом на руках.
func TestChatSaySettlesHandedAsk(t *testing.T) {
	sid := "abab1111-2222-4333-8444-555566667777"
	e, c, frames := askSayEnvNoTerm(t, sid)
	writeAskSign(t, e.proj, "task-XR-9", sid, "XR-9", time.Now().Add(5*time.Minute))

	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/say",
		sayBody("склейка, а не копия", "h-1"))
	said := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("ответ раздавшего разговора отбит: %d %s", resp.StatusCode, said)
	}
	if got := frames(); len(got) != 0 {
		t.Fatalf("реплика ушла в сокет клиента мимо ждущего инструмента: %q", got)
	}
	lines := chatLines(t, e.proj, "task-XR-9")
	if len(lines) != 1 {
		t.Fatalf("во входе разговора строк %d, ждали одну: %q", len(lines), lines)
	}
	// Строка безадресная: её берёт и само ожидание, и сторожок, когда срок
	// вышел и задача уже припаркована вопросом.
	if chat.Addressee(lines[0]) != "" {
		t.Fatalf("реплика легла с адресатом, парковку она не разбудит: %q", lines[0])
	}
	if chat.Said(lines[0]) != "склейка, а не копия" {
		t.Fatalf("текст реплики потерялся: %q", lines[0])
	}
	if !strings.Contains(said, `"ask"`) || !strings.Contains(said, "XR-9") {
		t.Fatalf("ручка не назвала дорогу и задачу вопроса: %s", said)
	}
}

// Вопрос задал делегат второй подписки: сессия в признаке его собственная, и к
// раздавшему разговору её приводит поле родителя в реестре.
func TestChatSaySettlesDelegateAsk(t *testing.T) {
	sid := "cdcd1111-2222-4333-8444-555566667777"
	deleg := "efef1111-2222-4333-8444-555566667777"
	e, c, frames := askSayEnvNoTerm(t, sid)
	writeBinds(t, e.home,
		fmt.Sprintf("2026-08-28T12:00:00 сессия %s задача XR-4 проект demo дерево %s "+
			"транскрипт "+standTranscript(e.home, "t")+" источник заказ повод startup tmux -\n", sid, e.proj),
		handedBind(e.home, "2026-08-28T12:01:00", deleg, "-", sid))
	writeAskSign(t, e.proj, "task-XR-9", deleg, "XR-9", time.Now().Add(5*time.Minute))

	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/say",
		sayBody("делим на два витка", "h-2"))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("ответ отбит: %d %s", resp.StatusCode, body(t, resp))
	}
	if got := frames(); len(got) != 0 {
		t.Fatalf("реплика ушла в сокет мимо ждущего делегата: %q", got)
	}
	if lines := chatLines(t, e.proj, "task-XR-9"); len(lines) != 1 {
		t.Fatalf("во входе разговора строк %d, ждали одну: %q", len(lines), lines)
	}
}

// Проводка вопроса розданной работы в статике: ожидание разговора приезжает
// полем own_wait, а галочки вешаются при строках вариантов в ленте
// (askPickWire, стенд TestStaticAskPicksInReply). Карточки с шапкой тут
// больше нет: вопрос идёт текстом в ленте (DK-1374), и в чужом чате его
// держит задача разговора, а не шапка блока (DK-1204).
func TestStaticHandedAskWiring(t *testing.T) {
	js := readFile(t, filepath.Join("static", "app.js"))
	for _, want := range []string{"own_wait", "function askPickWire(", "askPickSaid("} {
		if !strings.Contains(js, want) {
			t.Errorf("в static/app.js нет %q: вопрос агента не соберётся", want)
		}
	}
	css := readFile(t, filepath.Join("static", "style.css"))
	for _, want := range []string{".caskopt{", ".casklist{", ".caskpick{", ".casklit{"} {
		if !strings.Contains(css, want) {
			t.Errorf("в static/style.css нет %q", want)
		}
	}
}
