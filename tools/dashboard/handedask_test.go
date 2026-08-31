package main

import (
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dronrider/devkit/internal/chat"
	"github.com/dronrider/devkit/internal/sessions"
)

// Вопрос розданной работы доезжает до раздавшего разговора. Субагент ходит с
// ID внешней сессии, и признак ожидания, который он кладёт под именем своей
// задачи, несёт сессию того разговора, откуда работу раздали. До скана по
// сессии вопрос был виден только в разговоре задачи, человек сидел в другом,
// срок выходил молча, и работа вставала парковкой (живые случаи DK-517,
// DK-543 и слияние цели DK-397).

// handedBind это строка реестра про сессию с задачей и родителем разом: у
// bindRecord нет родителя, у bindParent задачи, а делегату нужны оба поля.
func handedBind(stamp, sid, task, parent string) string {
	return fmt.Sprintf("%s сессия %s задача %s проект demo дерево /tmp/demo "+
		"транскрипт /tmp/%s.jsonl источник заказ повод startup tmux - родитель %s\n",
		stamp, sid, task, sid, parent)
}

// Пульс открытого разговора видит вопрос, который лежит под именем чужой
// задачи, но адресован его сессии: раздавший разговор ждёт человека, и кольцо
// с плашкой обязаны об этом сказать.
func TestPulseHandedAskReachesDispatcher(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.Local)
	e, c := pulseEnv(t, now)
	seen := now.Add(-5 * time.Second)
	writeSession(t, e.home, e.proj, "", "aaa-1", pulseTranscript(seen, "Bash", "go build ./..."), seen)
	writeBinds(t, e.home, bindRecord("2026-08-20T11:59:00", "aaa-1", "XR-1", "заказ"))
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
		bindRecord("2026-08-20T11:59:00", "aaa-1", "XR-1", "заказ"),
		handedBind("2026-08-20T11:59:30", "bbb-7", "-", "aaa-1"))
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
	writeBinds(t, e.home, bindRecord("2026-08-20T11:59:00", "aaa-1", "XR-1", "заказ"))
	writeAskFor(t, e.proj, "XR-8", "zzz-9", now.Add(20*time.Minute))
	writeAskFor(t, e.proj, "XR-9", "aaa-1", now.Add(-time.Minute))

	p := getPulse(t, e, c, "task=XR-1&sid=aaa-1")
	if p.OwnWait != nil || len(p.Asks) != 0 {
		t.Fatalf("чужой или протухший вопрос приписан разговору: %+v %+v", p.OwnWait, p.Asks)
	}
}

// Скан признаков: ближний срок первым, варианты вопроса разворачиваются в
// строки экрана с пометкой рекомендованного.
func TestHandedAsksOrderAndLines(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.Local)
	far := chat.Ask{Until: now.Add(30 * time.Minute), Session: "aaa-1", Task: "XR-2",
		Questions: []chat.Question{{Text: "режем строку"}}}
	near := chat.Ask{Until: now.Add(5 * time.Minute), Session: "aaa-1", Task: "XR-3",
		Questions: []chat.Question{{Text: "куда катить", Options: []chat.Option{
			{Label: "в прод", Note: "сразу", Recommended: true},
			{Label: "в стенд"},
		}}}}
	for _, a := range []chat.Ask{far, near} {
		if err := chat.WriteAsk(dir, chat.TaskName(a.Task), a); err != nil {
			t.Fatal(err)
		}
	}
	got := handedAsks(dir, "aaa-1", sessions.Binds{}, now)
	if len(got) != 2 || got[0].Ask.Task != "XR-3" || got[1].Ask.Task != "XR-2" {
		t.Fatalf("порядок вопросов не по сроку: %+v", got)
	}
	w := handedWaiting(got[0])
	want := []string{"куда катить", "* в прод сразу", "- в стенд"}
	if strings.Join(w.Questions, "|") != strings.Join(want, "|") {
		t.Fatalf("строки вопроса собрались не так: %q", w.Questions)
	}
	if w.Task != "XR-3" || w.Until != near.Until.Unix() {
		t.Fatalf("задача или срок потерялись: %+v", w)
	}
	if deleg := handedAsks(dir, "up-1", sessions.Binds{"aaa-1": {Parent: "up-1"}}, now); len(deleg) != 2 {
		t.Fatalf("вопросы делегата не привелись к родителю: %+v", deleg)
	}
}

// Реплика раздавшего разговора уходит во вход, который слушает инструмент
// ожидания, а не в сокет клиента: ждущий сидит в ходе Bash и сокета не слышит,
// а реплика в сокете легла бы в очередь следующего хода, и ожидание добрало бы
// срок с готовым ответом на руках.
func TestChatSaySettlesHandedAsk(t *testing.T) {
	sid := "abab1111-2222-4333-8444-555566667777"
	e, c, frames := askSayEnv(t, sid)
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
	e, c, frames := askSayEnv(t, sid)
	writeBinds(t, e.home,
		fmt.Sprintf("2026-08-28T12:00:00 сессия %s задача XR-4 проект demo дерево %s "+
			"транскрипт /tmp/t.jsonl источник заказ повод startup tmux task-XR-4\n", sid, e.proj),
		handedBind("2026-08-28T12:01:00", deleg, "-", sid))
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
// полем own_wait, а сам вопрос рисуется виджетом с кнопками
// (TestStaticAgentAskWidget). Плашки с плоскими строками больше нет: она
// показывала варианты текстом и не показывала вопрос своей задачи вовсе
// (DK-652).
func TestStaticHandedAskWiring(t *testing.T) {
	js := readFile(t, filepath.Join("static", "app.js"))
	for _, want := range []string{"own_wait", "paintAgentAsk"} {
		if !strings.Contains(js, want) {
			t.Errorf("в static/app.js нет %q: вопрос агента не соберётся", want)
		}
	}
	css := readFile(t, filepath.Join("static", "style.css"))
	for _, want := range []string{".caskopt{", ".casklist{"} {
		if !strings.Contains(css, want) {
			t.Errorf("в static/style.css нет %q", want)
		}
	}
}
