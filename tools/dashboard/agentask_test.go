package main

import (
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dronrider/devkit/internal/chat"
)

// Вопрос агента доезжает до панели виджетом, а не подсказкой поля ввода. Заход
// спросил человека инструментом ожидания и стоит, пока ответа нет: до этой
// правки вопрос по своей же задаче не показывался вовсе (живой случай DK-650,
// восемь минут ожидания и парковка), а розданный приезжал плоскими строками.

// writeAskPack кладёт признак ожидания с пачкой вопросов, как его пишет
// taskctl ask.
func writeAskPack(t *testing.T, tree, task, sid string, until time.Time, qs ...chat.Question) {
	t.Helper()
	a := chat.Ask{Until: until, Session: sid, Task: task, Questions: qs}
	if err := chat.WriteAsk(tree, chat.TaskName(task), a); err != nil {
		t.Fatal(err)
	}
}

// askOf спрашивает ручку вопроса и разбирает виджет из ответа.
func askOf(t *testing.T, e *testEnv, c *http.Client, sid string) agentAsk {
	t.Helper()
	resp := doReq(t, c, "GET", e.srv.URL+"/api/projects/demo/chats/"+sid+"/ask", "")
	text := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("вопрос агента: %d %s", resp.StatusCode, text)
	}
	var got struct {
		Ask agentAsk `json:"ask"`
	}
	if err := json.Unmarshal([]byte(text), &got); err != nil {
		t.Fatalf("ответ ручки не разобрался: %v\n%s", err, text)
	}
	return got.Ask
}

// Живой tmux вопросу агента не нужен: признак это файл, и разбирать чужое окно
// незачем. Прежде ручка отвечала отказом «не живёт в нашей tmux» раньше, чем
// вообще смотрела на признак.
func TestChatAskAgentWithoutTmux(t *testing.T) {
	e, c := chatEnv(t)
	sid := "aaaa1111-1111-4111-8111-111111111111"
	writeAskPack(t, e.proj, "XR-9", sid, time.Now().Add(5*time.Minute), chat.Question{
		Text: "куда катить",
		Options: []chat.Option{
			{Label: "в прод", Note: "сразу", Recommended: true},
			{Label: "в стенд"},
		},
	})

	ask := askOf(t, e, c, sid)
	if ask.Kind != askKindAgent || ask.Task != "XR-9" {
		t.Fatalf("виджет вопроса агента не собрался: %+v", ask)
	}
	if ask.Text != "куда катить" {
		t.Errorf("текст вопроса потерялся: %q", ask.Text)
	}
	if len(ask.Options) != 3 {
		t.Fatalf("варианты и свободный ответ не приехали: %+v", ask.Options)
	}
	if ask.Options[0].Text != "в прод" || !strings.Contains(ask.Options[0].Desc, askAdviceWord) {
		t.Errorf("рекомендованный вариант не помечен: %+v", ask.Options[0])
	}
	if ask.Options[2].Kind != pickFree {
		t.Errorf("своими словами ответить нечем: %+v", ask.Options[2])
	}
	if ask.Until != 0 && ask.Steps != nil {
		t.Errorf("одиночный вопрос приехал шагами: %+v", ask.Steps)
	}
}

// Пачка вопросов это шаги: они едут на экран все разом, и ответы копятся в
// самой панели. Очередь следом идущих вопросов названа отдельным полем.
func TestChatAskAgentPackSteps(t *testing.T) {
	e, c := chatEnv(t)
	sid := "aaaa1111-1111-4111-8111-111111111111"
	writeAskPack(t, e.proj, "XR-9", sid, time.Now().Add(5*time.Minute),
		chat.Question{Text: "куда катить", Options: []chat.Option{{Label: "в прод"}}},
		chat.Question{Text: "когда катить", Options: []chat.Option{{Label: "утром"}}})
	writeAskPack(t, e.proj, "XR-8", sid, time.Now().Add(20*time.Minute),
		chat.Question{Text: "режем строку"})
	// Порядок теперь по времени файла, а не по сроку (DK-715: срока у нового
	// признака нет вовсе), и оба признака легли в один и тот же тест в одну
	// секунду по живым часам: время файла тут проставлено руками.
	now := time.Now()
	if err := os.Chtimes(chat.AskPath(e.proj, chat.TaskName("XR-9")), now.Add(-time.Minute), now.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(chat.AskPath(e.proj, chat.TaskName("XR-8")), now, now); err != nil {
		t.Fatal(err)
	}

	ask := askOf(t, e, c, sid)
	if ask.Task != "XR-9" {
		t.Fatalf("отвечать зовут не тому, кто спросил раньше: %+v", ask)
	}
	if len(ask.Steps) != 2 || !ask.Steps[0].Now || ask.Steps[1].Text != "когда катить" {
		t.Fatalf("шаги пачки собрались не так: %+v", ask.Steps)
	}
	if !strings.Contains(ask.Steps[0].Name, "куда катить") {
		t.Errorf("имя шага не называет вопрос: %q", ask.Steps[0].Name)
	}
	if len(ask.Rest) != 1 || ask.Rest[0] != "XR-8" {
		t.Errorf("очередь вопросов не названа: %+v", ask.Rest)
	}
}

// Чужой и протухший вопросы виджетом не встают, и разговор без нашей tmux
// отвечает словами, а не отказом: панель спрашивает всякий открытый разговор, и
// «ни на чём не стоит» такой же ответ, как сам вопрос.
func TestChatAskQuietWithoutAsk(t *testing.T) {
	e, c := chatEnv(t)
	sid := "aaaa1111-1111-4111-8111-111111111111"
	writeAskPack(t, e.proj, "XR-9", sid, time.Now().Add(-time.Minute),
		chat.Question{Text: "куда катить"})

	resp := doReq(t, c, "GET", e.srv.URL+"/api/projects/demo/chats/"+sid+"/ask", "")
	text := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("молчащий разговор: %d %s", resp.StatusCode, text)
	}
	if strings.Contains(text, `"ask"`) {
		t.Errorf("протухший вопрос приехал виджетом: %s", text)
	}
	if !strings.Contains(text, "вопросов агента за ним нет") {
		t.Errorf("ручка не сказала, почему показывать нечего: %s", text)
	}
}

// Регрессия DK-652: своё ожидание гасило скан вопросов. Вопрос, заданный по
// своей же задаче, заполнял OwnWait раньше скана, скан не шёл вовсе, и от
// вопроса на экране оставалась одна подсказка поля ввода. Заодно варианты: их
// теряло чтение признака по строке доски, которое брало один текст вопроса.
func TestPulseOwnAskKeepsScanAndOptions(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.Local)
	e, c := pulseEnv(t, now)
	seen := now.Add(-5 * time.Second)
	writeSession(t, e.home, e.proj, "", "aaa-1", pulseTranscript(seen, "Bash", "taskctl ask XR-1"), seen)
	writeBinds(t, e.home, bindRecord("2026-08-20T11:59:00", "aaa-1", "XR-1", "заказ"))
	writeAskPack(t, e.proj, "XR-1", "aaa-1", now.Add(20*time.Minute), chat.Question{
		Text:    "куда катить",
		Options: []chat.Option{{Label: "в прод", Recommended: true}, {Label: "в стенд"}},
	})

	p := getPulse(t, e, c, "task=XR-1&sid=aaa-1")
	if len(p.Asks) != 1 || p.Asks[0].Task != "XR-1" {
		t.Fatalf("вопрос своей задачи не попал в скан: %+v", p.Asks)
	}
	if p.OwnWait == nil || len(p.OwnWait.Questions) < 3 {
		t.Fatalf("варианты своего вопроса потерялись: %+v", p.OwnWait)
	}
	if !strings.Contains(strings.Join(p.OwnWait.Questions, "|"), "* в прод") {
		t.Errorf("рекомендованный вариант не помечен: %q", p.OwnWait.Questions)
	}
	if p.Wait == nil || len(p.Wait.Questions) < 3 {
		t.Errorf("ожидание строки доски приехало без вариантов: %+v", p.Wait)
	}
}

// Стенд фронта: блок вопроса рисуется из фикстуры признака, варианты видны
// кнопками, пачка идёт шагами, а ответ уезжает репликой разговора, не
// клавишами в чужое окно. Проверка по тексту static/app.js тут не годится, и
// постановка называет этот класс дыры прямо: разметку держал и прежний тест, а
// человек вопроса не видел. Стенд рисует блок в поддельном DOM
// (testdata/poc_agentask.mjs), вид кнопок меряет testdata/poc_caskopt.mjs. Без
// node шаг пропускается: узел стенда, а не рабочей части.
func TestStaticAgentAskWidget(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node не найден: стенд вопроса агента пропущен")
	}
	out, err := exec.Command(node, filepath.Join("testdata", "poc_agentask.mjs"),
		filepath.Join("static", "app.js")).CombinedOutput()
	if err != nil {
		t.Fatalf("вопрос агента в панели: %v\n%s", err, out)
	}
	t.Log(strings.TrimSpace(string(out)))
}

// Шапка вопроса (пароль и агент) собирает заголовок и остаток времени соседними
// инлайн-элементами, и без правила отступа они слипаются. Стенд проверяет, что
// правило задано в стиле .caskh .n, а не пробелом в разметке (DK-784).
func TestStaticAskHeaderGap(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node не найден: стенд зазора шапки пропущен")
	}
	out, err := exec.Command(node, filepath.Join("testdata", "poc_caskhgap.mjs"),
		filepath.Join("static", "app.js")).CombinedOutput()
	if err != nil {
		t.Fatalf("зазор в шапке вопроса: %v\n%s", err, out)
	}
	t.Log(strings.TrimSpace(string(out)))
}
