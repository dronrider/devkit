package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// writeTmuxLiveFake кладёт фикстуру tmux для экрана агента: ls отвечает
// строками формата с табами, capture-pane отдаёт снимок и пишет вызов в
// журнал, чужое имя сессии это ненулевой код, как у живого tmux.
func writeTmuxLiveFake(t *testing.T, bin, logPath string) {
	t.Helper()
	body := `echo "$@" >> "` + logPath + `"
case "$1" in
ls)
  printf 'goal-XR-9\t1\t1754770421\ntask-XR-5\t2\t1754770500\n';;
capture-pane)
  if [ "$4" != "=goal-XR-9:" ]; then
    echo "can't find session" >&2
    exit 1
  fi
  printf 'виток 3 поднят, ход витка ниже\n$ taskctl list\n';;
esac
exit 0`
	writeScript(t, bin, "tmux", body)
}

func tmuxEnv(t *testing.T) (*testEnv, *http.Client, string) {
	t.Helper()
	e := newTestEnv(t)
	tmuxLog := filepath.Join(e.home, "tmux.log")
	writeTmuxLiveFake(t, e.bin, tmuxLog)
	return e, e.loggedClient(t), tmuxLog
}

// API tmux называет поднятые сессии с окнами и временем создания (шаг 4
// сценария DK-219).
func TestTmuxListNames(t *testing.T) {
	e, c, _ := tmuxEnv(t)
	resp := doReq(t, c, "GET", e.srv.URL+"/api/tmux", "")
	var got struct {
		Sessions []tmuxSession `json:"sessions"`
	}
	if err := json.Unmarshal([]byte(body(t, resp)), &got); err != nil {
		t.Fatal(err)
	}
	want := []tmuxSession{
		{Name: "goal-XR-9", Windows: 1, Created: 1754770421},
		{Name: "task-XR-5", Windows: 2, Created: 1754770500},
	}
	if !reflect.DeepEqual(got.Sessions, want) {
		t.Errorf("сессии: %+v, ожидал %+v", got.Sessions, want)
	}
}

// tmux без единой сессии это штатно пустой список со словами «сессий нет»,
// а не ошибка и не молчание.
func TestTmuxListNoServer(t *testing.T) {
	e := newTestEnv(t)
	writeScript(t, e.bin, "tmux", "exit 1")
	c := e.loggedClient(t)
	resp := doReq(t, c, "GET", e.srv.URL+"/api/tmux", "")
	text := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("tmux без сервера: %d %s", resp.StatusCode, text)
	}
	for _, want := range []string{`"sessions":[]`, "tmux-сессий нет"} {
		if !strings.Contains(text, want) {
			t.Errorf("в ответе нет %q: %s", want, text)
		}
	}
}

// Ненайденный tmux называется и здесь: «tmux нет» и «сессий нет» это разные
// ответы.
func TestTmuxListMissingNamed(t *testing.T) {
	e := newTestEnv(t)
	if err := os.Remove(filepath.Join(e.bin, "tmux")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", e.bin)
	c := e.loggedClient(t)
	for _, path := range []string{"/api/tmux", "/api/tmux/goal-XR-9"} {
		resp := doReq(t, c, "GET", e.srv.URL+path, "")
		text := body(t, resp)
		if resp.StatusCode != http.StatusBadGateway || !strings.Contains(text, "tmux не нашёлся") {
			t.Errorf("%s без tmux: %d %s", path, resp.StatusCode, text)
		}
	}
}

// Снимок пейна идёт через capture-pane с точным именем сессии (=имя: с
// двоеточием цели-пейна): без знака равенства tmux взял бы сессию по
// префиксу и снимок пришёл бы от соседки.
func TestTmuxPaneSnapshot(t *testing.T) {
	e, c, tmuxLog := tmuxEnv(t)
	resp := doReq(t, c, "GET", e.srv.URL+"/api/tmux/goal-XR-9", "")
	var got struct {
		Name string `json:"name"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal([]byte(body(t, resp)), &got); err != nil {
		t.Fatal(err)
	}
	if got.Name != "goal-XR-9" || got.Text != "виток 3 поднят, ход витка ниже\n$ taskctl list\n" {
		t.Fatalf("снимок: %+v", got)
	}
	if log := readFile(t, tmuxLog); !strings.Contains(log, "capture-pane -p -t =goal-XR-9:") {
		t.Errorf("tmux позван не так: %s", log)
	}
}

// Сессии нет, и снимок отвечает 404 со словами, а не пустым экраном.
func TestTmuxPaneNotFound(t *testing.T) {
	e, c, _ := tmuxEnv(t)
	resp := doReq(t, c, "GET", e.srv.URL+"/api/tmux/goal-XR-777", "")
	text := body(t, resp)
	if resp.StatusCode != http.StatusNotFound || !strings.Contains(text, "tmux-сессия goal-XR-777 не найдена") {
		t.Fatalf("снимок несуществующей сессии: %d %s", resp.StatusCode, text)
	}
}

// Имя сессии с чужими символами отбивается до подпроцесса.
func TestTmuxPaneBadName(t *testing.T) {
	e, c, tmuxLog := tmuxEnv(t)
	resp := doReq(t, c, "GET", e.srv.URL+"/api/tmux/%3Brm%20-rf", "")
	text := body(t, resp)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("кривое имя: %d %s", resp.StatusCode, text)
	}
	if log := readFile(t, tmuxLog); strings.Contains(log, "capture-pane") {
		t.Errorf("кривое имя дошло до tmux: %s", log)
	}
}

// Зависший capture-pane снимается по сроку, а не держит горутину запроса:
// снимок это подпроцесс со сроком, как все чужие программы сервера.
func TestTmuxPaneHungAnsweredWithError(t *testing.T) {
	e, c, _ := tmuxEnv(t)
	writeScript(t, e.bin, "tmux", "sleep 60")
	old := procTimeout
	procTimeout = 200 * time.Millisecond
	t.Cleanup(func() { procTimeout = old })

	start := time.Now()
	resp := doReq(t, c, "GET", e.srv.URL+"/api/tmux/goal-XR-9", "")
	text := body(t, resp)
	if took := time.Since(start); took > 5*time.Second {
		t.Fatalf("ответ занял %v: срок подпроцесса не сработал", took)
	}
	if resp.StatusCode != http.StatusBadGateway || !strings.Contains(text, "по сроку") {
		t.Fatalf("зависший снимок: %d %s", resp.StatusCode, text)
	}
}

// Клиент, поднятый в незнакомом каталоге, встаёт на вопросе о доверии и до
// ответа не делает ни хода. Панель дашборда показывала при этом пустую ленту, а
// ответить человек мог только руками в tmux (замечание пользователя). Снимок
// панели разбирается на вопрос и варианты: по ним панель собирает кнопки.
func TestParseTmuxAsk(t *testing.T) {
	// Снимок снят с живой застрявшей сессии, слово в слово.
	pane := strings.Join([]string{
		"────────────────────────────────────────────",
		" Accessing workspace:",
		"",
		" /Users/rider/projects/xr-proxy",
		"",
		" Quick safety check: Is this a project you created or one you trust?",
		"",
		" Claude Code'll be able to read, edit, and execute files here.",
		"",
		" Security guide",
		"",
		" ❯ 1. Yes, I trust this folder",
		"   2. No, exit",
		"",
		" Enter to confirm · Esc to cancel",
	}, "\n")

	ask := parseTmuxAsk(pane)
	if len(ask.Options) != 2 {
		t.Fatalf("вариантов разобрано %d, жду два: %+v", len(ask.Options), ask)
	}
	if ask.Options[0].Text != "Yes, I trust this folder" || ask.Options[1].Text != "No, exit" {
		t.Errorf("варианты разобраны не теми словами: %+v", ask.Options)
	}
	// У вопроса доверия отвечают номером пункта: подсказки про стрелки под ним
	// нет, и способ ответа берётся с самой панели.
	if ask.Keys != askKeysDigit {
		t.Errorf("способ ответа у вопроса доверия назван %q, жду номер пункта", ask.Keys)
	}
	if ask.At != 1 {
		t.Errorf("курсор клиента стоит на %d, жду на первом пункте", ask.At)
	}
	// Текст вопроса это весь абзац над вариантами, а не последняя его строка:
	// без каталога и самого вопроса человек читал бы одно «Security guide»
	// (живая проверка на застрявшей сессии).
	for _, want := range []string{"xr-proxy", "Quick safety check", "Security guide"} {
		if !strings.Contains(ask.Text, want) {
			t.Errorf("в тексте вопроса нет %q: %q", want, ask.Text)
		}
	}

	// Работающий клиент ни о чём не спрашивает: вопросом считается только блок
	// вариантов, и нумерованный список в выводе команды за него не выдаётся.
	work := strings.Join([]string{
		" Всё идёт своим ходом:",
		" 1. первый шаг сделан",
		"",
		" пишу дальше",
	}, "\n")
	if got := parseTmuxAsk(work); len(got.Options) != 0 {
		t.Errorf("вывод работающего клиента принят за вопрос: %+v", got)
	}
	if got := parseTmuxAsk(""); len(got.Options) != 0 {
		t.Errorf("пустой снимок принят за вопрос: %+v", got)
	}
}

// livePollPane это снимок живой панели с опросом агента, слово в слово (сессия
// chat-2 проекта xr-proxy, живой случай пользователя). Панель дашборда не
// показывала этот вопрос вовсе: пояснения под вариантами рвали блок, от него
// оставался один пункт, и вопрос не доезжал. Человек писал реплики, они
// доходили, а клиент их не читал, потому что ждал выбора.
var livePollPane = strings.Join([]string{
	"\u23fa Пока разведка идёт, уточню фактуру.",
	strings.Repeat("\u2500", 56),
	"\u2190  \u2610 Площадка  \u2610 Симптом  \u2714 Submit  \u2192",
	"",
	"Где именно MAX ломается под прокси?",
	"",
	"\u276f 1. [ ] На телефоне (Android-клиент)",
	"  Туннель поднят приложением xr-android, MAX на том же телефоне",
	"  2. [ ] На устройствах за роутером",
	"  OpenWRT с xr-client, перехват TCP через TPROXY",
	"  3. [ ] Везде одинаково",
	"  И там, и там",
	"  4. [ ] Type something",
	"     Next",
	strings.Repeat("\u2500", 56),
	"  5. Chat about this",
	"",
	"Enter to select \u00b7 Tab/Arrow keys to navigate \u00b7 Esc to cancel",
	"",
	"  @ dashboard\u276f",
	"    Завёл черновик?",
}, "\n")

// Опрос агента с вариантами разбирается так же, как вопрос доверия: тот же
// блок, те же кнопки. Пояснение под вариантом блок не рвёт, кнопка отправки
// («Next») идёт остановкой курсора наравне с вариантами, а полоса шагов едет
// своим полем, потому что она про многошаговость, а не про слова вопроса.
func TestParseTmuxAskWidget(t *testing.T) {
	ask := parseTmuxAsk(livePollPane)
	if len(ask.Options) != 6 {
		t.Fatalf("остановок разобрано %d, жду шесть (4 варианта, Next и Chat about this): %+v",
			len(ask.Options), ask.Options)
	}
	if ask.Options[0].Text != "На телефоне (Android-клиент)" {
		t.Errorf("флажок не отрезан от слов варианта: %q", ask.Options[0].Text)
	}
	for i, want := range []string{"off", "off", "off", "off", "", ""} {
		if ask.Options[i].Mark != want {
			t.Errorf("состояние флажка %d разобрано как %q, жду %q", i+1, ask.Options[i].Mark, want)
		}
	}
	if ask.Options[3].Kind != pickFree {
		t.Errorf("свободный ответ не узнан: %+v", ask.Options[3])
	}
	if ask.Options[4].Kind != pickNext || ask.Options[4].Text != "Next" {
		t.Errorf("кнопка виджета не узнана: %+v", ask.Options[4])
	}
	if ask.Options[5].Text != "Chat about this" {
		t.Errorf("последний вариант потерялся: %+v", ask.Options[5])
	}
	if ask.At != 1 {
		t.Errorf("курсор клиента стоит на %d, жду на первом варианте", ask.At)
	}
	// Способ ответа берётся с панели: под виджетом клиент сам пишет, что тут
	// ходят стрелками.
	if ask.Keys != askKeysArrows {
		t.Errorf("способ ответа назван %q, жду стрелки", ask.Keys)
	}
	if ask.Text != "Где именно MAX ломается под прокси?" {
		t.Errorf("текст вопроса разобран как %q", ask.Text)
	}
	// Полоса шагов едет своим полем: значки флажков в тексте вопроса человеку
	// ни о чём не говорят.
	if len(ask.Steps) != 3 || ask.Steps[0].Name != "Площадка" || ask.Steps[2].Name != "Submit" {
		t.Fatalf("полоса шагов разобрана не так: %+v", ask.Steps)
	}
	if ask.Steps[0].Done || !ask.Steps[2].Done {
		t.Errorf("пройденность шагов разобрана не так: %+v", ask.Steps)
	}
}

// livePollTabs это снимок живой панели с опросом из трёх шагов, слово в слово
// (своя одноразовая сессия с тем же виджетом). Открытый шаг клиент помечает
// только заливкой, поэтому снимок снят с раскраской: без неё панель не знает,
// на каком табе стоит человек.
var livePollTabs = strings.Join([]string{
	strings.Repeat("\u2500", 56),
	"\u001b[39m\u2190  \u001b[38;5;16m\u001b[48;5;153m \u2610 Место \u001b[39m\u001b[49m " +
		" \u2610 Тип неисправности  \u2612 Сроки  \u2714 Submit  \u2192\u001b[39m",
	"",
	"Где ломается?",
	"",
	"\u276f 1. [ ] На телефоне",
	"  Проблема только на мобильном устройстве",
	"  2. [ ] За роутером",
	"  Проблема в конкретной сети",
	"  3. [\u2714] Везде",
	"  Проблема видна на всех устройствах",
	"  4. [ ] Type something",
	"     Next",
	strings.Repeat("\u2500", 56),
	"  5. Chat about this",
	"",
	"Enter to select \u00b7 Tab/Arrow keys to navigate \u00b7 Esc to cancel",
}, "\n")

// Шаги опроса это табы: по ним ходят свободно, ответа на текущий шаг для
// перехода не нужно, а ответы копятся (проверено на живой панели). Открытый
// таб, отвеченные табы, пояснения под вариантами и служебные пункты виджета
// разбираются каждый своим полем: экран показывает их по-своему и по-русски.
func TestParseTmuxAskTabs(t *testing.T) {
	ask := parseTmuxAsk(livePollTabs)
	if len(ask.Steps) != 4 {
		t.Fatalf("шагов разобрано %d, жду четыре: %+v", len(ask.Steps), ask.Steps)
	}
	if !ask.Steps[0].Now || ask.Steps[1].Now {
		t.Errorf("открытый шаг разобран не тот: %+v", ask.Steps)
	}
	if ask.Steps[0].Done || !ask.Steps[2].Done {
		t.Errorf("отвеченность шагов разобрана не так: %+v", ask.Steps)
	}
	if ask.Steps[1].Name != "Тип неисправности" {
		t.Errorf("имя шага разобрано с мусором: %q", ask.Steps[1].Name)
	}
	// Пояснение под вариантом это его поле, а не отдельная строка блока: без
	// него выбор делается вслепую, а в панели пояснения терялись вовсе.
	if ask.Options[0].Desc != "Проблема только на мобильном устройстве" {
		t.Errorf("пояснение варианта не разобрано: %+v", ask.Options[0])
	}
	if ask.Options[0].Text != "На телефоне" {
		t.Errorf("слова варианта склеились с пояснением: %q", ask.Options[0].Text)
	}
	if ask.Options[2].Mark != "on" {
		t.Errorf("отмеченный флажок разобран как %q", ask.Options[2].Mark)
	}
	// Служебные пункты названы видом, а не словами: слова у них английские, и
	// показывает их экран своими.
	for i, want := range []string{"", "", "", pickFree, pickNext, pickChat} {
		if ask.Options[i].Kind != want {
			t.Errorf("вид пункта %d разобран как %q, жду %q", i+1, ask.Options[i].Kind, want)
		}
	}
	if ask.Text != "Где ломается?" {
		t.Errorf("текст вопроса разобран как %q", ask.Text)
	}
}

// Вопрос с одиночным выбором клиент печатает без флажков вовсе, а свободный
// ответ у него зовётся с точкой на конце. Ни то, ни другое разбор не роняет.
func TestParseTmuxAskSingle(t *testing.T) {
	pane := strings.Join([]string{
		"\u001b[39m\u2190  \u2612 Место  \u001b[48;5;153m \u2610 Симптом \u001b[49m  \u2714 Submit  \u2192",
		"",
		"Что именно?",
		"",
		"\u276f 1. Картинки",
		"     Проблемы с загрузкой изображений",
		"  2. Звонки",
		"     Проблемы со звуком",
		"  3. Type something.",
		strings.Repeat("\u2500", 56),
		"  4. Chat about this",
		"",
		"Enter to select \u00b7 Tab/Arrow keys to navigate \u00b7 Esc to cancel",
	}, "\n")
	ask := parseTmuxAsk(pane)
	if len(ask.Options) != 4 {
		t.Fatalf("остановок разобрано %d, жду четыре: %+v", len(ask.Options), ask.Options)
	}
	if ask.Options[0].Mark != "" {
		t.Errorf("у вопроса без флажков появилось состояние: %+v", ask.Options[0])
	}
	// Отвеченный вариант одиночного выбора клиент помечает галочкой в конце
	// строки: знак этот состояние, а не часть слов (живая проверка).
	picked := parseTmuxAsk(strings.Replace(pane, "1. Картинки", "1. Картинки \u2714", 1))
	if picked.Options[0].Text != "Картинки" || picked.Options[0].Mark != "on" {
		t.Errorf("галочка одиночного выбора разобрана не так: %+v", picked.Options[0])
	}
	if ask.Options[2].Kind != pickFree {
		t.Errorf("свободный ответ с точкой на конце не узнан: %+v", ask.Options[2])
	}
	if !ask.Steps[1].Now {
		t.Errorf("открытый шаг разобран не тот: %+v", ask.Steps)
	}
}

// Сводкой виджет кончает опрос. Панель показывает её итогом с одной кнопкой, а
// не ещё одним опросом (замечание пользователя), и для этого сводка разбирается
// своим видом: пары «вопрос-ответ», предупреждение и кнопка отправки.
func TestParseTmuxAskReview(t *testing.T) {
	pane := strings.Join([]string{
		"\u001b[39m\u2190  \u2610 Место  \u2612 Симптом  \u2612 Сроки \u001b[48;5;153m \u2714 Submit \u001b[49m \u2192",
		"Review your answers",
		"\u26a0 You have not answered all questions",
		" \u25cf Что именно?",
		"   \u2192 Картинки",
		" \u25cf Когда началось?",
		"   \u2192 Вчера",
		"Ready to submit your answers?",
		"\u276f 1. Submit answers",
		"  2. Cancel",
		"Enter to confirm \u00b7 Esc to cancel",
	}, "\n")
	ask := parseTmuxAsk(pane)
	if ask.Kind != askKindReview {
		t.Fatalf("сводка не узнана: %+v", ask)
	}
	if ask.Warn == "" || !strings.Contains(ask.Warn, "not answered all") {
		t.Errorf("предупреждение сводки не разобрано: %q", ask.Warn)
	}
	if len(ask.Said) != 2 || ask.Said[0].Q != "Что именно?" || ask.Said[0].A != "Картинки" {
		t.Fatalf("пары «вопрос-ответ» разобраны не так: %+v", ask.Said)
	}
	if ask.Said[1].Q != "Когда началось?" || ask.Said[1].A != "Вчера" {
		t.Errorf("вторая пара сводки разобрана не так: %+v", ask.Said[1])
	}
	if ask.Options[0].Kind != pickSubmit {
		t.Errorf("кнопка отправки сводки не узнана: %+v", ask.Options[0])
	}
	// Английские строки виджета в текст вопроса не идут: у сводки свой вид и
	// свои слова на экране.
	if ask.Text != "" {
		t.Errorf("сводка приехала ещё и текстом вопроса: %q", ask.Text)
	}
}

// Переход по табам идёт стрелками от открытого шага: столько нажатий, сколько
// шагов между ними, и ни одного лишнего. Ответа на текущий шаг для перехода не
// нужно.
func TestTmuxStepToKeys(t *testing.T) {
	ask := parseTmuxAsk(livePollTabs)
	for _, tc := range []struct {
		name string
		step int
		keys []string
	}{
		{"вперёд на два", 3, []string{"Right", "Right"}},
		{"на соседний", 2, []string{"Right"}},
		{"на свой же", 1, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newTestEnv(t)
			sent := filepath.Join(e.home, "sent.log")
			writeScript(t, e.bin, "tmux", `case "$1" in
send-keys) shift; echo "$@" >> `+sent+`;;
esac
exit 0`)
			t.Setenv("PATH", e.bin+string(os.PathListSeparator)+os.Getenv("PATH"))
			if err := tmuxStepTo("chat-2", ask, tc.step); err != nil {
				t.Fatalf("переход не подался: %v", err)
			}
			var got []string
			for _, ln := range strings.Split(strings.TrimSpace(readFile(t, sent)), "\n") {
				if strings.TrimSpace(ln) == "" {
					continue
				}
				got = append(got, strings.TrimSpace(strings.TrimPrefix(ln, "-t =chat-2:")))
			}
			if strings.Join(got, "|") != strings.Join(tc.keys, "|") {
				t.Errorf("клавиши перехода не те: %q, жду %q", got, tc.keys)
			}
		})
	}
	// Открытого шага не видно, значит и считать переход не от чего: молча
	// нажимать стрелки вслепую нельзя, уедет в чужой вопрос.
	blind := ask
	blind.Steps = []tmuxStep{{Name: "Место"}, {Name: "Симптом"}}
	if err := tmuxStepTo("chat-2", blind, 2); err == nil {
		t.Error("переход без открытого шага не отбит")
	}
}

// Ответ виджету подаётся теми клавишами, какими его подаёт человек: ход
// стрелками от той остановки, где стоит курсор, и Enter. Номер пункта тут не
// работает вовсе (проверено на живой панели), и слать его значило бы отвечать
// в никуда.
func TestTmuxAnswerWidgetKeys(t *testing.T) {
	ask := parseTmuxAsk(livePollPane)
	for _, tc := range []struct {
		name   string
		option int
		text   string
		keys   []string
	}{
		{"третий вариант", 3, "", []string{"Down", "Down", "Enter"}},
		{"кнопка отправки", 5, "", []string{"Down", "Down", "Down", "Down", "Enter"}},
		{"первый вариант с курсора", 1, "", []string{"Enter"}},
		{"свободный ответ", 4, "везде, кроме роутера",
			[]string{"Down", "Down", "Down", "Enter", "-l везде, кроме роутера", "Enter"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newTestEnv(t)
			sent := filepath.Join(e.home, "sent.log")
			writeScript(t, e.bin, "tmux", `case "$1" in
send-keys) shift; echo "$@" >> `+sent+`;;
esac
exit 0`)
			t.Setenv("PATH", e.bin+string(os.PathListSeparator)+os.Getenv("PATH"))
			if err := tmuxAnswer("chat-2", ask, tc.option, tc.text); err != nil {
				t.Fatalf("ответ не подался: %v", err)
			}
			var got []string
			for _, ln := range strings.Split(strings.TrimSpace(readFile(t, sent)), "\n") {
				got = append(got, strings.TrimSpace(strings.TrimPrefix(ln, "-t =chat-2:")))
			}
			if strings.Join(got, "|") != strings.Join(tc.keys, "|") {
				t.Errorf("клавиши поданы не те: %q, жду %q", got, tc.keys)
			}
		})
	}
}

// Кривая перерисовка оставляет варианты и подсказку, а знак курсора теряет:
// ходить стрелками не от чего, и отказ обязан зваться errAskBlind, чтобы
// дорога реплики отличала его от железного сбоя tmux и вела реплику запасной
// дорогой (живой случай chat-34).
func TestTmuxAnswerBlindWidgetRefuses(t *testing.T) {
	ask := parseTmuxAsk(crookedPane)
	if ask.Keys != askKeysArrows {
		t.Fatalf("подсказка не распознана как ход стрелками: %q", ask.Keys)
	}
	if len(ask.Options) != 2 {
		t.Fatalf("вариантов разобрано %d, жду два: %+v", len(ask.Options), ask.Options)
	}
	if ask.At != 0 {
		t.Fatalf("курсор найден на пункте %d, жду слепой виджет", ask.At)
	}
	if err := tmuxAnswer("chat-2", ask, 1, ""); !errors.Is(err, errAskBlind) {
		t.Fatalf("слепой виджет не назван errAskBlind: %v", err)
	}
}

// Многошаговый опрос: ответ на шаг приводит следующий, и панель обязана
// показать его так же, а не считать разговор продолженным. Для дашборда это
// значит, что снимок следующего шага разбирается тем же разбором, со своей
// полосой и своим курсором.
func TestParseTmuxAskNextStep(t *testing.T) {
	next := strings.Join([]string{
		strings.Repeat("\u2500", 56),
		"\u2190  \u2714 Площадка  \u2610 Симптом  \u2610 Submit  \u2192",
		"",
		"Что именно происходит с MAX?",
		"",
		"  1. [ ] Не открываются картинки",
		"  Текст доходит, вложения нет",
		"\u276f 2. [\u2714] Звонки не соединяются",
		"  Сигналинг проходит, медиа нет",
		"     Next",
		"",
		"Enter to select \u00b7 Tab/Arrow keys to navigate \u00b7 Esc to cancel",
	}, "\n")
	ask := parseTmuxAsk(next)
	if len(ask.Options) != 3 {
		t.Fatalf("следующий шаг разобран в %d остановок, жду три: %+v", len(ask.Options), ask.Options)
	}
	if ask.At != 2 {
		t.Errorf("курсор следующего шага стоит на %d, жду на втором", ask.At)
	}
	// Отмеченный флажок клиент печатает знаком галочки, а не буквой: живая
	// проверка на своей сессии показала «[\u2714]», и без этого знака галочка
	// оставалась в словах варианта («[\u2714] За роутером» кнопкой).
	if ask.Options[1].Mark != "on" || ask.Options[1].Text != "Звонки не соединяются" {
		t.Errorf("отмеченный флажок разобран не так: %+v", ask.Options[1])
	}
	if ask.Text != "Что именно происходит с MAX?" {
		t.Errorf("текст следующего шага разобран как %q", ask.Text)
	}
	if len(ask.Steps) != 3 || !ask.Steps[0].Done || ask.Steps[1].Done {
		t.Fatalf("полоса шагов следующего шага разобрана не так: %+v", ask.Steps)
	}
}

// liveEchoPane это строка ввода клиента с репликой человека из трёх пунктов:
// живой случай, панель показала эту реплику блоком «Клиент ждёт ответа», взяв
// заголовком кусок её же текста, а пунктами 2 и 3 варианты ответа. Клиент при
// этом ничего не спрашивал: он ждал не выбора, а работы.
var liveEchoPane = strings.Join([]string{
	"  Разобрал накопитель, поднял три сессии груминга.",
	"",
	strings.Repeat("\u2500", 56),
	"\u276f 1. Я просил убрать кнопку «Новая задача» из таба черновиков, она уже",
	"  есть в меню плюса рядом с поиском",
	"  2. Кнопку «Разобрать выбранное» переименовать в «Провести груминг»",
	"  3. В раздел черновиков ты добавил галку, и строка разъехалась на две",
	strings.Repeat("\u2500", 56),
	"  auto mode on (shift+tab to cycle)",
}, "\n")

// livePrintedPane это ответ агента списком, слово в слово со снимка живой
// панели (сессия chat-DK-181-1). Пункты тут это его собственный текст, и
// вопросом клиента они не были ни разу.
var livePrintedPane = strings.Join([]string{
	"  Найденное исполнителем вне задачи, жду вашего слова, что заводить:",
	"",
	"  1. Хук check-reread отказал ревьюверу «файл уже прочитан в этой сессии»",
	"  2. Фоновые субагенты порой глохли без уведомления о завершении",
	"  3. Окружение машины: PATH резолвит agentctl в устаревшую копию",
	"  4. Красный TestCodeOpensWindow под харнесом glm",
	"",
	"\u273b Churned for 15m 26s",
	strings.Repeat("\u2500", 56),
	"\u276f Заведи 1 и 2, по третьей отдельно разберёмся",
	strings.Repeat("\u2500", 56),
}, "\n")

// liveThemePane это выбор темы клиента, слово в слово со снимка живой панели
// (сессия chat-10). Подсказки навигации в снимке не видно вовсе: она уехала за
// нижнюю кромку панели вместе с образцом раскраски. Виджет тут настоящий, а
// блока по нему не будет, и это осознанный размен: галочка с курсором стоят и в
// чужом тексте, а промолчать дешевле, чем показать человеку кнопки от списка,
// который агент просто напечатал.
var liveThemePane = strings.Join([]string{
	" Let's get started.",
	"",
	" Choose the text style that looks best with your terminal",
	" To change this later, run /theme",
	"",
	"   1. Auto (match terminal)",
	" \u276f 2. Dark mode \u2714",
	"   3. Light mode",
	"   4. Dark mode (colorblind-friendly)",
	"",
	" \u254c\u254c\u254c\u254c\u254c\u254c\u254c\u254c\u254c\u254c",
	"  1  function greet() {",
	"  2 -  console.log(\"Hello, World!\");",
	"  2 +  console.log(\"Hello, Claude!\");",
	"  3  }",
	" \u254c\u254c\u254c\u254c\u254c\u254c\u254c\u254c\u254c\u254c",
	"  Syntax theme: Monokai Extended (ctrl+t to disable)",
}, "\n")

// liveListPane это ответ агента со списком задач, каким его показал второй
// снимок пользователя: строки списка обрезаны по ширине панели терминала, а
// панель дашборда сделала из них кнопки, взяв заголовком блока кусок того же
// ответа. Список тут и нумерованный, и маркированный: под опрос попадал всякий
// набор коротких строк подряд.
var liveListPane = strings.Join([]string{
	"  DK-517 (черновик): правило в RULES.md про порядок правки доски.",
	"",
	"  \u041fачка на выполнение, в порядке зависимостей:",
	"",
	"  1. DK-312 (S, ранг 62): рубеж длинного вывода Bash, cmdout забирает",
	"  2. DK-313 (S, ранг 58): выжимка агенту вместо полного тела ответа",
	"  3. DK-516 (M, ранг 71): парковка задачи машинным разрядом причины",
	"  4. DK-517 (черновик): правило в RULES.md, оформить строкой после",
	"",
	"  Зависимости:",
	"  - DK-312 раньше DK-313: хвост забирается до того, как режется",
	"  - DK-516 раньше DK-517: правило пишется по машинному разряду",
	"  - DK-517 последней",
	"",
	"\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500",
	"\u276f ",
	"\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500",
}, "\n")

// Блок «Клиент ждёт ответа» встаёт только там, где клиент правда стоит на
// своём виджете. Пронумерованных строк для этого мало ни при каких условиях:
// ими клиент печатает и эхо реплики человека, и собственный ответ, а знак
// курсора стоит в начале строки ввода так же, как перед выбранным вариантом.
func TestParseTmuxAskNotWidget(t *testing.T) {
	for _, tc := range []struct {
		name string
		pane string
	}{
		{"эхо реплики человека", liveEchoPane},
		{"ответ агента списком", livePrintedPane},
		{"список задач в ответе агента", liveListPane},
		{"тот же список без подсказки навигации", liveThemePane},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseTmuxAsk(tc.pane); len(got.Options) != 0 {
				t.Fatalf("панель принята за вопрос клиента: текст %q, варианты %+v",
					got.Text, got.Options)
			}
		})
	}
}

// Обратная сторона того же рубежа: настоящий виджет узнаётся по тому, что
// печатает он сам. У выбора темы это галочка выбранного пункта, у вопроса
// доверия и опроса подсказка навигации, у сводки её пары «вопрос-ответ».
func TestParseTmuxAskStillWidget(t *testing.T) {
	// Вопрос доверия каталогу: подсказка навигации стоит прямо под вариантами,
	// как её и печатает клиент.
	trust := parseTmuxAsk(strings.Join([]string{
		" Quick safety check: Is this a project you created or one you trust?",
		"",
		" \u276f 1. Yes, I trust this folder",
		"   2. No, exit",
		"",
		" Enter to confirm \u00b7 Esc to cancel",
	}, "\n"))
	if len(trust.Options) != 2 || trust.At != 1 {
		t.Fatalf("вопрос доверия перестал узнаваться: %+v", trust)
	}
	if trust.Keys != askKeysDigit {
		t.Errorf("способ ответа у вопроса доверия назван %q, жду номер пункта", trust.Keys)
	}
	poll := parseTmuxAsk(livePollPane)
	if len(poll.Options) != 6 || poll.Keys != askKeysArrows {
		t.Errorf("опрос агента перестал узнаваться: %+v", poll)
	}
}

// Список агента над настоящим виджетом достаётся тексту разговора, а кнопками
// становятся варианты самого виджета: блок берётся нижний, со следом клиента.
func TestParseTmuxAskPrintedAboveWidget(t *testing.T) {
	pane := livePrintedPane + "\n" + strings.Join([]string{
		" Quick safety check: Is this a project you created or one you trust?",
		"",
		" \u276f 1. Yes, I trust this folder",
		"   2. No, exit",
		"",
		" Enter to confirm \u00b7 Esc to cancel",
	}, "\n")
	ask := parseTmuxAsk(pane)
	if len(ask.Options) != 2 {
		t.Fatalf("вариантов разобрано %d, жду два варианта виджета: %+v", len(ask.Options), ask.Options)
	}
	if ask.Options[0].Text != "Yes, I trust this folder" {
		t.Errorf("кнопками стали не варианты виджета: %+v", ask.Options)
	}
	if strings.Contains(ask.Text, "check-reread") {
		t.Errorf("в текст вопроса уехал ответ агента: %q", ask.Text)
	}
}

// liveReviewPane это сводка живого опроса, слово в слово со снимка панели
// (сессия chat-98, опрос про чай, поднятый пользователем для проверки). Своей
// подсказки навигации сводка не печатает вовсе, и рубеж подсказки прятал её
// целиком: человек отвечал на оба вопроса и оставался перед пустой панелью,
// хотя виджет ждал последнего нажатия.
var liveReviewPane = strings.Join([]string{
	"  варианта с короткими пояснениями: (1) какой чай заварить, (2) на сколько",
	"  минут. Больше ничего не делай, дождись ответа и просто поблагодари.",
	"",
	strings.Repeat("\u2500", 60),
	"\u2190  \u2612 Чай  \u2612 Время  \u2714 Submit  \u2192",
	"",
	"Review your answers",
	"",
	" \u25cf Какой чай заварить?",
	"   \u2192 Чёрный",
	" \u25cf На сколько минут заваривать?",
	"   \u2192 3 минуты",
	"",
	"Ready to submit your answers?",
	"",
	"\u276f 1. Submit answers",
	"  2. Cancel",
	"",
	"  @ dashboard\u276f",
	"    Позови инструмент AskUserQuestion с двумя вопросами",
}, "\n")

// Сводка опроса узнаётся своими словами и парами ответов, а не подсказкой
// навигации: подсказки под ней нет ни одной, и это проверено на живом снимке.
// Рубеж от чужих списков сводка держит не хуже: заголовок, значки пар и полосу
// шагов печатает сам виджет, в выводе агента их не бывает.
func TestParseTmuxAskLiveReview(t *testing.T) {
	ask := parseTmuxAsk(liveReviewPane)
	if ask.Kind != askKindReview {
		t.Fatalf("живая сводка опроса не узнана: %+v", ask)
	}
	if len(ask.Options) != 2 || ask.Options[0].Kind != pickSubmit {
		t.Fatalf("кнопки сводки разобраны не так: %+v", ask.Options)
	}
	if len(ask.Said) != 2 || ask.Said[0].Q != "Какой чай заварить?" || ask.Said[0].A != "Чёрный" {
		t.Fatalf("пары «вопрос-ответ» живой сводки разобраны не так: %+v", ask.Said)
	}
	if len(ask.Steps) != 3 || ask.Steps[2].Name != "Submit" {
		t.Errorf("полоса шагов живой сводки разобрана не так: %+v", ask.Steps)
	}
	// Тот же рубеж, что и был: без слов сводки и без подсказки блока нет.
	bare := strings.Replace(liveReviewPane, "Review your answers", "Вот что вышло", 1)
	bare = strings.Replace(bare, "Ready to submit your answers?", "Что дальше?", 1)
	bare = strings.Replace(bare, " \u25cf Какой чай заварить?", " Какой чай заварить?", 1)
	bare = strings.Replace(bare, " \u25cf На сколько минут заваривать?", " На сколько минут заваривать?", 1)
	bare = strings.Replace(bare, "   \u2192 Чёрный", "   Чёрный", 1)
	bare = strings.Replace(bare, "   \u2192 3 минуты", "   3 минуты", 1)
	if got := parseTmuxAsk(bare); len(got.Options) != 0 {
		t.Errorf("тот же экран без слов виджета принят за вопрос: %+v", got)
	}
}

// liveTrustBarePane это вопрос доверия каталогу, снятый с живой панели
// 2026-08-28 (сессия dk486-probe в каталоге, которого клиент не знал). Номеров
// у вариантов нет вовсе, а курсор стоит на «No, exit»: свежий клиент рисует
// этот виджет иначе, чем рисовал прежде, и прежний разбор его не видел.
// Именно так встали два чата xr-proxy, о которых сказал пользователь.
var liveTrustBarePane = strings.Join([]string{
	"",
	strings.Repeat("─", 100),
	" Accessing workspace:",
	"",
	" /private/tmp/dk486-untrusted",
	"",
	" Quick safety check: Is this a project you created or one you trust? (Like your own code, a",
	" well-known open source project, or work from your team). If not, take a moment to review what's in",
	" this folder first.",
	"",
	" Claude Code'll be able to read, edit, and execute files here.",
	"",
	" Security guide",
	"",
	" ❯ No, exit",
	"   Yes, I trust this folder",
	"",
	" Enter to confirm · Esc to cancel",
}, "\n")

// Вопрос доверия без номеров узнаётся так же, как узнавался нумерованный, и
// курсор в нём виден: клиент ставит его на «No, exit», то есть слепое
// подтверждение снимает сессию.
func TestParseTmuxAskBareTrust(t *testing.T) {
	ask := parseTmuxAsk(liveTrustBarePane)
	if len(ask.Options) != 2 {
		t.Fatalf("вопрос доверия без номеров разобран в %d вариантов: %+v", len(ask.Options), ask.Options)
	}
	if ask.Options[0].Text != "No, exit" || ask.Options[1].Text != "Yes, I trust this folder" {
		t.Fatalf("варианты разобраны не те: %+v", ask.Options)
	}
	if ask.At != 1 {
		t.Errorf("курсор стоит на %d, а клиент держит его на первом пункте", ask.At)
	}
	// Номер пункта в таком виджете не работает вовсе: пунктов клиент не
	// нумеровал, и отвечать ему надо ходом курсора.
	if ask.Keys != askKeysArrows {
		t.Errorf("способ ответа назван %q, жду ход стрелками", ask.Keys)
	}
	if !strings.Contains(ask.Text, "Quick safety check") {
		t.Errorf("текст вопроса собран не с панели: %q", ask.Text)
	}
}

// Рубеж виджета у блока без номеров тот же: без подсказки навигации под
// вариантами показывать нечего, иначе кнопками стал бы всякий абзац под
// строкой ввода клиента.
func TestParseTmuxAskBareNotWidget(t *testing.T) {
	for _, tc := range []struct {
		name string
		pane string
	}{
		{"тот же вопрос без подсказки", strings.Replace(liveTrustBarePane,
			" Enter to confirm · Esc to cancel", " auto mode on (shift+tab to cycle)", 1)},
		{"реплика человека и текст под ней", strings.Join([]string{
			strings.Repeat("─", 56),
			"❯ Заведи 1 и 2, по третьей отдельно разберёмся",
			"  а четвёртую отложи",
			strings.Repeat("─", 56),
		}, "\n")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseTmuxAsk(tc.pane); len(got.Options) != 0 {
				t.Fatalf("панель принята за вопрос клиента: %+v", got.Options)
			}
		})
	}
}

// Ответ виджету без номеров подаётся ходом курсора: клиент держит его на «No,
// exit», и слепой Enter снял бы сессию вместо подтверждения доверия.
func TestTmuxAnswerBareTrustKeys(t *testing.T) {
	ask := parseTmuxAsk(liveTrustBarePane)
	for _, tc := range []struct {
		name   string
		option int
		keys   []string
	}{
		{"доверяю", 2, []string{"Down", "Enter"}},
		{"не доверяю", 1, []string{"Enter"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newTestEnv(t)
			sent := filepath.Join(e.home, "sent.log")
			writeScript(t, e.bin, "tmux", `case "$1" in
send-keys) shift; echo "$@" >> `+sent+`;;
esac
exit 0`)
			t.Setenv("PATH", e.bin+string(os.PathListSeparator)+os.Getenv("PATH"))
			if err := tmuxAnswer("chat-2", ask, tc.option, ""); err != nil {
				t.Fatalf("ответ не подался: %v", err)
			}
			var got []string
			for _, ln := range strings.Split(strings.TrimSpace(readFile(t, sent)), "\n") {
				got = append(got, strings.TrimSpace(strings.TrimPrefix(ln, "-t =chat-2:")))
			}
			if strings.Join(got, "|") != strings.Join(tc.keys, "|") {
				t.Errorf("клавиши поданы не те: %q, жду %q", got, tc.keys)
			}
		})
	}
}
