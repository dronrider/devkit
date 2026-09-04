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

// Ответ человека сессии, которая стоит на вопросе инструмента ожидания. Ждёт
// его процесс taskctl ask внутри хода Bash, а сокет клиента слышит только сам
// клиент: реплика, ушедшая сокетом, легла бы в очередь следующего хода, и
// ожидание добрало бы свой срок с готовым ответом на руках. Поэтому при живом
// признаке ожидания ручка чата кладёт реплику во вход разговора (LLD DK-430,
// решение 2: вход принадлежит разговору, реплика сессии идёт с адресатом).

// writeAskSign кладёт признак ожидания разговора так же, как его пишет
// taskctl ask: срок первой строкой, ниже ждущая сессия, задача и вопросы.
func writeAskSign(t *testing.T, tree, name, sid, task string, until time.Time) {
	t.Helper()
	err := chat.WriteAsk(tree, name, chat.Ask{Until: until, Session: sid, Task: task,
		Questions: []chat.Question{{Text: "режем строку или поднимаем цену"}}})
	if err != nil {
		t.Fatal(err)
	}
}

// askSayEnv поднимает сессию, ведущую задачу XR-4, с живым сокетом клиента и
// живым терминалом (tmux). Терминал живёт затем, чтобы стоять на дороге,
// которую снимает признак ожидания (askSayEnvNoTerm поднимает то же без него).
func askSayEnv(t *testing.T, sid string) (*testEnv, *http.Client, func() []string) {
	t.Helper()
	e, c := chatEnv(t)
	writeSession(t, e.home, e.proj, "", sid, plainTalk, time.Now())
	writeBinds(t, e.home, fmt.Sprintf("2026-08-28T12:00:00 сессия %s задача XR-4 проект demo "+
		"дерево %s транскрипт /tmp/t.jsonl источник заказ повод startup tmux task-XR-4\n", sid, e.proj))
	sock, frames := countingSock(t)
	writePeerSock(t, e.home, sid, os.Getpid(), sock)
	writeScript(t, e.bin, "tmux", `case "$1" in ls) printf 'task-XR-4\t1\t1754770421\n';; esac
exit 0`)
	return e, c, frames
}

// askSayEnvNoTerm это askSayEnv без живого терминала: сессия та же, задача та
// же, а tmux ни одного окна не называет. Признак ожидания без такого терминала
// остаётся дорогой ответа: держать её тут и проверяют оставшиеся тесты файла.
func askSayEnvNoTerm(t *testing.T, sid string) (*testEnv, *http.Client, func() []string) {
	t.Helper()
	e, c := chatEnv(t)
	writeSession(t, e.home, e.proj, "", sid, plainTalk, time.Now())
	writeBinds(t, e.home, fmt.Sprintf("2026-08-28T12:00:00 сессия %s задача XR-4 проект demo "+
		"дерево %s транскрипт /tmp/t.jsonl источник заказ повод startup tmux -\n", sid, e.proj))
	sock, frames := countingSock(t)
	writePeerSock(t, e.home, sid, os.Getpid(), sock)
	writeScript(t, e.bin, "tmux", `exit 0`)
	return e, c, frames
}

// askSayEnvTerm это askSayEnv с окном, чьи клавиши стенд записывает в файл:
// им проверяется, что ответ на живой признак уезжает терминалом, а не файлом.
func askSayEnvTerm(t *testing.T, sid string) (*testEnv, *http.Client, string) {
	t.Helper()
	e, c := chatEnv(t)
	writeSession(t, e.home, e.proj, "", sid, plainTalk, time.Now())
	writeBinds(t, e.home, fmt.Sprintf("2026-08-28T12:00:00 сессия %s задача XR-4 проект demo "+
		"дерево %s транскрипт /tmp/t.jsonl источник заказ повод startup tmux task-XR-4\n", sid, e.proj))
	sent := filepath.Join(e.home, "sent.log")
	writeScript(t, e.bin, "tmux", `case "$1" in
ls) printf 'task-XR-4\t1\t1754770421\n';;
capture-pane) printf '';;
send-keys) shift; echo "$@" >> `+sent+`;;
esac
exit 0`)
	return e, c, sent
}

// chatLines читает вход разговора дерева: доставленная строка из него уходит,
// поэтому лежащая строка тут и есть неразобранная реплика.
func chatLines(t *testing.T, tree, name string) []string {
	t.Helper()
	return chat.ReadLines(filepath.Join(tree, ".devkit", "chat", name+".in"))
}

// Сессия стоит на вопросе, а живого терминала у неё нет (delegate-субагент
// или дерево без tmux): реплика идёт во вход разговора с адресатом, а сокет
// клиента её не получает вовсе.
func TestChatSayGoesToInputWhileAskWaits(t *testing.T) {
	sid := "dddd4444-4444-4444-8444-444444444444"
	e, c, frames := askSayEnvNoTerm(t, sid)
	writeAskSign(t, e.proj, "task-XR-4", sid, "XR-4", time.Now().Add(5*time.Minute))

	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/say",
		sayBody("режем, две половины по шву", "m-1"))
	said := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("ответ ждущей сессии отбит: %d %s", resp.StatusCode, said)
	}
	if got := frames(); len(got) != 0 {
		t.Fatalf("реплика ушла в сокет клиента мимо ждущего инструмента: %q", got)
	}
	lines := chatLines(t, e.proj, "task-XR-4")
	if len(lines) != 1 {
		t.Fatalf("во входе разговора строк %d, ждали одну: %q", len(lines), lines)
	}
	if chat.Addressee(lines[0]) != sid {
		t.Fatalf("реплика легла без адресата, её заберёт чужой ход: %q", lines[0])
	}
	if chat.Said(lines[0]) != "режем, две половины по шву" {
		t.Fatalf("текст реплики потерялся: %q", lines[0])
	}
	if !strings.Contains(said, `"ask"`) {
		t.Fatalf("ручка не назвала дорогу ожиданием: %s", said)
	}
	if _, ok := chat.ReadAsk(chat.AskPath(e.proj, "task-XR-4")); ok {
		t.Fatal("признак ожидания пережил ответ: панель показывала бы отвеченный вопрос")
	}
}

// Признак без срока (DK-715): хук PreToolUse кладёт его без дедлайна, и живёт
// он до ответа, а не до часов. Ответ снимает признак так же, как снимал бы
// признак со сроком: часов тут нет вовсе, и снимает его сам ответ. Сессия тут
// без живого терминала, и ответ едет файлом, как ждущему без tmux.
func TestChatSayDropsTheForeverAsk(t *testing.T) {
	sid := "aaaa7777-7777-4777-8777-777777777777"
	e, c, frames := askSayEnvNoTerm(t, sid)
	if err := chat.WriteAsk(e.proj, "task-XR-4", chat.Ask{Session: sid, Task: "XR-4",
		Questions: []chat.Question{{Text: "режем строку или поднимаем цену"}}}); err != nil {
		t.Fatal(err)
	}

	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/say",
		sayBody("режем, две половины по шву", "m-4"))
	said := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("ответ ждущей сессии отбит: %d %s", resp.StatusCode, said)
	}
	if got := frames(); len(got) != 0 {
		t.Fatalf("реплика ушла в сокет клиента мимо ждущего инструмента: %q", got)
	}
	lines := chatLines(t, e.proj, "task-XR-4")
	if len(lines) != 1 || chat.Said(lines[0]) != "режем, две половины по шву" {
		t.Fatalf("во входе разговора не тот ответ: %q", lines)
	}
	if _, ok := chat.ReadAsk(chat.AskPath(e.proj, "task-XR-4")); ok {
		t.Fatal("признак без срока пережил ответ: панель показывала бы вопрос вечно")
	}
}

// Сессия с признаком ожидания, но живым терминалом (DK-715): её ход кончился
// хуком, а не смертью процесса, и клавиши она снова принимает. Ответ едет ей
// терминальной дорогой, а не файлом, который никто не прочитает: живого
// читателя входа разговора у сессии больше нет вовсе.
func TestChatSayAnsweredByKeysDropsTheAsk(t *testing.T) {
	sid := "bbbb8888-8888-4888-8888-888888888888"
	e, c, sent := askSayEnvTerm(t, sid)
	if err := chat.WriteAsk(e.proj, "task-XR-4", chat.Ask{Session: sid, Task: "XR-4",
		Questions: []chat.Question{{Text: "режем строку или поднимаем цену"}}}); err != nil {
		t.Fatal(err)
	}

	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/say",
		sayBody("режем, две половины по шву", "m-5"))
	said := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("ответ ждущей сессии отбит: %d %s", resp.StatusCode, said)
	}
	if !strings.Contains(said, `"send-keys"`) {
		t.Fatalf("ответ на живой признак не поехал терминальной дорогой: %s", said)
	}
	if keys := readFile(t, sent); !strings.Contains(keys, "режем, две половины по шву") {
		t.Fatalf("ответ не подан клавишами: %q", keys)
	}
	if lines := chatLines(t, e.proj, "task-XR-4"); len(lines) != 0 {
		t.Fatalf("ответ лёг во вход, который никто не прочитает: %q", lines)
	}
	if _, ok := chat.ReadAsk(chat.AskPath(e.proj, "task-XR-4")); ok {
		t.Fatal("признак пережил ответ клавишами: панель показывала бы вопрос вечно")
	}
}

// parkedByAskBoardJSON это доска с XR-4 в Blocked машинной причиной «вопрос:
// ...»: settleAsk обязана свести такую строку в In progress тем же вопросом,
// на который она встала, и не тронуть строку, припаркованную другой причиной.
const parkedByAskBoardJSON = `{"prefix":"XR","sections":[` +
	`{"key":"blocked","title":"Blocked","rows":[` +
	`{"id":"XR-4","title":"Начатая задача","type":"task","p":"P2","r":31,"r_parts":[25,3,1,0,2],` +
	`"cost":"-","link":"-","block":"вопрос: режем строку или поднимаем цену"}]}]}`

// Сессия с живым терминалом отвечает на вопрос, который её и припарковал:
// settleAsk сама возвращает строку в In progress тем же ходом, что снимает
// признак, не дожидаясь тика сторожка (DK-715).
func TestChatSayAnsweredByKeysUnparksTheRow(t *testing.T) {
	sid := "cccc9999-9999-4999-8999-999999999999"
	e, c := chatEnv(t)
	writeSession(t, e.home, e.proj, "", sid, plainTalk, time.Now())
	writeBinds(t, e.home, fmt.Sprintf("2026-08-28T12:00:00 сессия %s задача XR-4 проект demo "+
		"дерево %s транскрипт /tmp/t.jsonl источник заказ повод startup tmux task-XR-4\n", sid, e.proj))
	sent := filepath.Join(e.home, "sent.log")
	moved := filepath.Join(e.home, "moved.log")
	writeScript(t, e.bin, "tmux", `case "$1" in
ls) printf 'task-XR-4\t1\t1754770421\n';;
capture-pane) printf '';;
send-keys) shift; echo "$@" >> `+sent+`;;
esac
exit 0`)
	writeScript(t, e.bin, "taskctl", `case "$*" in
*"move XR-4 in-progress"*) echo "$@" >> `+moved+`; echo '{"ok":true}';;
*) echo '`+parkedByAskBoardJSON+`';;
esac`)
	if err := chat.WriteAsk(e.proj, "task-XR-4", chat.Ask{Session: sid, Task: "XR-4",
		Questions: []chat.Question{{Text: "режем строку или поднимаем цену"}}}); err != nil {
		t.Fatal(err)
	}

	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/say",
		sayBody("режем, две половины по шву", "m-6"))
	said := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("ответ отбит: %d %s", resp.StatusCode, said)
	}
	if !strings.Contains(said, `"send-keys"`) {
		t.Fatalf("ответ не поехал терминальной дорогой: %s", said)
	}
	if keys := readFile(t, moved); !strings.Contains(keys, "move XR-4 in-progress") || !strings.Contains(keys, "--push") {
		t.Fatalf("строка не сведена в In progress: %q", keys)
	}
	if _, ok := chat.ReadAsk(chat.AskPath(e.proj, "task-XR-4")); ok {
		t.Fatal("признак пережил ответ клавишами")
	}
}

// parkedGoalBoardJSON это доска с целью DK-713 в Blocked машинной причиной
// «вопрос: ...»: утренний случай 2026-09-04, диспетчер цели спросил из
// tmux-чата, панель диалог не показала, и цель простояла четыре часа.
const parkedGoalBoardJSON = `{"prefix":"DK","sections":[` +
	`{"key":"blocked","title":"Blocked","rows":[` +
	`{"id":"DK-713","title":"Цель: обкатать вторую подписку","type":"task","p":"P1","r":41,` +
	`"r_parts":[25,9,3,0,4],"cost":"XL","link":"-","block":"вопрос: продолжать той же веткой или новой"}]}]}`

// Утренний случай DK-713 (стенд DoD DK-715): диспетчер цели зовёт
// AskUserQuestion из живого tmux-чата, хук перехватывает вызов и паркует
// цель. Вопрос обязан быть виден в панели блоком (GET .../ask), ответ кнопкой
// обязан дойти в ту же живую сессию клавишами, а не потеряться в файле, и
// цель обязана вернуться в In progress тем же ответом, а не стоять часами.
func TestMorningCaseGoalAskReachesLiveSession(t *testing.T) {
	sid := "dddd0713-0713-4713-8713-071307130713"
	e, c := chatEnv(t)
	writeSession(t, e.home, e.proj, "", sid, plainTalk, time.Now())
	writeBinds(t, e.home, fmt.Sprintf("2026-09-04T09:59:00 сессия %s задача DK-713 проект demo "+
		"дерево %s транскрипт /tmp/t.jsonl источник заказ повод startup tmux chat-DK-713-1\n", sid, e.proj))
	sent := filepath.Join(e.home, "sent.log")
	moved := filepath.Join(e.home, "moved.log")
	writeScript(t, e.bin, "tmux", `case "$1" in
ls) printf 'chat-DK-713-1\t1\t1754770421\n';;
capture-pane) printf '';;
send-keys) shift; echo "$@" >> `+sent+`;;
esac
exit 0`)
	writeScript(t, e.bin, "taskctl", `case "$*" in
*"move DK-713 in-progress"*) echo "$@" >> `+moved+`; echo '{"ok":true}';;
*) echo '`+parkedGoalBoardJSON+`';;
esac`)
	if err := chat.WriteAsk(e.proj, chat.TaskName("DK-713"), chat.Ask{Session: sid, Task: "DK-713",
		Questions: []chat.Question{{Text: "продолжать той же веткой или новой", Options: []chat.Option{
			{Label: "той же", Recommended: true}, {Label: "новой"}}}}}); err != nil {
		t.Fatal(err)
	}

	// Панель рисует блок из признака: живого tmux-снимка ей для этого не
	// нужно, чужое окно спрашивать незачем.
	ask := askOf(t, e, c, sid)
	if ask.Kind != askKindAgent || ask.Task != "DK-713" {
		t.Fatalf("вопрос цели не встал виджетом в панели: %+v", ask)
	}
	if ask.Text != "продолжать той же веткой или новой" {
		t.Errorf("текст вопроса не доехал: %q", ask.Text)
	}

	// Ответ кнопкой уходит той же дорогой, что и любая реплика панели: живая
	// сессия отвечает на клавиши, признак снимается ответом, а не сроком,
	// и цель возвращается в In progress тем же ходом.
	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/say",
		sayBody("той же", "goal-m-1"))
	said := body(t, resp)
	if resp.StatusCode != http.StatusOK || !strings.Contains(said, `"send-keys"`) {
		t.Fatalf("ответ не дошёл живой сессии клавишами: %d %s", resp.StatusCode, said)
	}
	if keys := readFile(t, sent); !strings.Contains(keys, "той же") {
		t.Fatalf("ответ не подан клавишами: %q", keys)
	}
	if keys := readFile(t, moved); !strings.Contains(keys, "move DK-713 in-progress") || !strings.Contains(keys, "--push") {
		t.Fatalf("цель не сведена в In progress ответом: %q", keys)
	}
	if _, ok := chat.ReadAsk(chat.AskPath(e.proj, chat.TaskName("DK-713"))); ok {
		t.Fatal("признак пережил ответ: панель показывала бы вопрос вечно, а цель стояла бы дальше")
	}
}

// Признак протух: ждущего за ним нет, и реплика идёт обычной дорогой, клавишами
// в живую tmux-сессию разговора (DK-480). Без срока всякий брошенный признак
// уводил бы разговор во вход, где реплику никто не читает до следующего хода.
func TestChatSayIgnoresStaleAsk(t *testing.T) {
	sid := "eeee5555-5555-4555-8555-555555555555"
	e, c, frames := askSayEnv(t, sid)
	writeAskSign(t, e.proj, "task-XR-4", sid, "XR-4", time.Now().Add(-time.Minute))

	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/say",
		sayBody("ответ на протухший вопрос", "m-2"))
	said := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("реплика отбита: %d %s", resp.StatusCode, said)
	}
	if !strings.Contains(said, `"send-keys"`) {
		t.Fatalf("реплика не поехала терминальной дорогой: %s", said)
	}
	if got := frames(); len(got) != 0 {
		t.Fatalf("реплика ушла в сокет мимо терминала: %q", got)
	}
	if lines := chatLines(t, e.proj, "task-XR-4"); len(lines) != 0 {
		t.Fatalf("реплика легла во вход, где её никто не ждёт: %q", lines)
	}
}

// Вопрос задала другая сессия того же разговора: реплика этой сессии едет
// своей дорогой, а не во вход. Иначе ответ одному собеседнику забрал бы ждущий
// сосед, и оба разговора получили бы не своё.
func TestChatSayIgnoresAskOfOtherSession(t *testing.T) {
	sid := "ffff6666-6666-4666-8666-666666666666"
	e, c, frames := askSayEnv(t, sid)
	writeAskSign(t, e.proj, "task-XR-4", "9999aaaa-7777-4777-8777-777777777777", "XR-4",
		time.Now().Add(5*time.Minute))

	resp := doReq(t, c, "POST", e.srv.URL+"/api/projects/demo/chats/"+sid+"/say",
		sayBody("это ответ не тому, кто ждёт", "m-3"))
	said := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("реплика отбита: %d %s", resp.StatusCode, said)
	}
	if !strings.Contains(said, `"send-keys"`) {
		t.Fatalf("реплика не поехала терминальной дорогой: %s", said)
	}
	if got := frames(); len(got) != 0 {
		t.Fatalf("реплика ушла в сокет мимо терминала: %q", got)
	}
	if lines := chatLines(t, e.proj, "task-XR-4"); len(lines) != 0 {
		t.Fatalf("реплика легла во вход чужого ожидания: %q", lines)
	}
}
