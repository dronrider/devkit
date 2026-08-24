package main

import (
	"encoding/json"
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
	if !ask.Options[3].Free {
		t.Errorf("свободный ответ не узнан: %+v", ask.Options[3])
	}
	if !ask.Options[4].Submit || ask.Options[4].Text != "Next" {
		t.Errorf("кнопка отправки не узнана: %+v", ask.Options[4])
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
